// SPDX-License-Identifier: GPL-3.0-or-later

// Package help serves the documentation in the web interface. The Markdown
// files are embedded in the binary (package docs) and rendered to HTML once at
// startup.
//
// Raw HTML in the Markdown is not rendered, so the HTML only contains what
// goldmark generates. Links between documentation pages point to the
// documentation view of the web interface (/docs/<page>), links to other files
// of the repository point to GitHub, and heading IDs get the prefix "doc-" so
// that they cannot collide with IDs of the web interface.
package help

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/joernott/dcdash/internal/httpx"
)

// Pages are the documentation pages shown in the web interface, in menu
// order. Developer documents (concept, plan, prompts, development) are left
// out; links to them point to GitHub.
var Pages = []string{
	"user-guide", "admin-guide", "installation", "configuration", "security", "permissions",
	"certificates", "proxmox", "dns", "dhcp", "netbox", "projects", "switches", "firewalls",
	"directories",
	"monitoring",
}

// IDPrefix is put in front of heading IDs and of fragments of links.
const IDPrefix = "doc-"

// repoURL is where links to files outside the documentation point.
const repoURL = "https://github.com/joernott/dcdash/blob/"

// Page is one rendered documentation page.
type Page struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	HTML  string `json:"html,omitempty"`
}

// Service holds the rendered pages.
type Service struct {
	pages []Page
	index map[string]int
}

// releaseTag matches release versions, which have a tag on GitHub.
var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// New renders the pages from fsys (the docs directory). version selects the
// GitHub reference of links to other files: the release tag, or main for
// development builds.
func New(fsys fs.FS, version string) (*Service, error) {
	ref := "main"
	if releaseTag.MatchString(version) {
		ref = version
	}
	known := make(map[string]bool, len(Pages))
	for _, p := range Pages {
		known[p] = true
	}
	s := &Service{index: make(map[string]int, len(Pages))}
	for _, name := range Pages {
		src, err := fs.ReadFile(fsys, name+".md")
		if err != nil {
			return nil, fmt.Errorf("help: %w", err)
		}
		html, title, err := render(src, known, ref)
		if err != nil {
			return nil, fmt.Errorf("help: render %s: %w", name, err)
		}
		s.index[name] = len(s.pages)
		s.pages = append(s.pages, Page{Name: name, Title: title, HTML: html})
	}
	return s, nil
}

// Page returns a rendered page.
func (s *Service) Page(name string) (Page, bool) {
	i, ok := s.index[name]
	if !ok {
		return Page{}, false
	}
	return s.pages[i], true
}

// List returns the pages without their content.
func (s *Service) List() []Page {
	out := make([]Page, len(s.pages))
	for i, p := range s.pages {
		out[i] = Page{Name: p.Name, Title: p.Title}
	}
	return out
}

// Register adds GET /api/v1/docs and GET /api/v1/docs/{page}; guard must
// require a logged-in user.
func (s *Service) Register(mux *http.ServeMux, guard func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/docs", guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"pages": s.List()})
	})))
	mux.Handle("GET /api/v1/docs/{page}", guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.Page(r.PathValue("page"))
		if !ok {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "no such documentation page")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, p)
	})))
}

// render converts Markdown to HTML and returns it with the text of the first
// level-1 heading as title.
func render(src []byte, known map[string]bool, ref string) (string, string, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(&links{known: known, ref: ref}, 100)),
		),
	)
	ctx := parser.NewContext(parser.WithIDs(newIDs()))
	doc := md.Parser().Parse(text.NewReader(src), parser.WithContext(ctx))

	title := ""
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if h, ok := n.(*ast.Heading); ok && entering && h.Level == 1 {
			title = plainText(h, src)
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})

	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return "", "", err
	}
	return buf.String(), title, nil
}

// plainText returns the text of a node without markup.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
		case *ast.String:
			b.Write(t.Value)
		case *ast.CodeSpan:
			for ch := t.FirstChild(); ch != nil; ch = ch.NextSibling() {
				if tx, ok := ch.(*ast.Text); ok {
					b.Write(tx.Segment.Value(src))
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

// links rewrites link destinations (see the package documentation).
type links struct {
	known map[string]bool
	ref   string
}

func (l *links) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := n.(type) {
		case *ast.Link:
			dest, external := l.rewrite(string(t.Destination))
			t.Destination = []byte(dest)
			if external {
				t.SetAttributeString("target", "_blank")
				t.SetAttributeString("rel", "noopener noreferrer")
			}
		case *ast.Image:
			dest, _ := l.rewrite(string(t.Destination))
			t.Destination = []byte(dest)
		}
		return ast.WalkContinue, nil
	})
}

// rewrite returns the new destination of a link and whether it leaves dcdash.
func (l *links) rewrite(dest string) (string, bool) {
	u, err := url.Parse(dest)
	if err != nil {
		return dest, false
	}
	if u.Scheme != "" || u.Host != "" {
		return dest, true
	}
	fragment := ""
	if u.Fragment != "" {
		fragment = "#" + IDPrefix + u.Fragment
	}
	if u.Path == "" {
		return fragment, false // within the page
	}
	if name, ok := strings.CutSuffix(u.Path, ".md"); ok && !strings.Contains(name, "/") && l.known[name] {
		return "/docs/" + name + fragment, false
	}
	// Any other file of the repository, relative to docs/.
	target := path.Clean(path.Join("docs", u.Path))
	if strings.HasPrefix(target, "../") {
		return dest, false
	}
	gh := repoURL + l.ref + "/" + target
	if u.Fragment != "" {
		gh += "#" + u.Fragment
	}
	return gh, true
}

// ids generates heading IDs like GitHub does (lower case, punctuation
// removed, spaces as "-", "-1", "-2" for repeated headings), with IDPrefix,
// so that links written for GitHub keep working.
type ids struct {
	used map[string]bool
}

func newIDs() *ids { return &ids{used: map[string]bool{}} }

func (s *ids) Generate(value []byte, _ ast.NodeKind) []byte {
	var b strings.Builder
	for _, r := range strings.ToLower(string(value)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	base := b.String()
	id := base
	for i := 1; s.used[id]; i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	s.used[id] = true
	return []byte(IDPrefix + id)
}

func (s *ids) Put(value []byte) {
	s.used[strings.TrimPrefix(string(value), IDPrefix)] = true
}
