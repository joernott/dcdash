// SPDX-License-Identifier: GPL-3.0-or-later

package help

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/joernott/dcdash/docs"
)

func TestAllPagesRender(t *testing.T) {
	s, err := New(docs.FS, "v0.1.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != len(Pages) {
		t.Fatalf("pages: %d", len(s.List()))
	}
	for _, p := range s.List() {
		if p.Title == "" || p.HTML != "" {
			t.Errorf("%s: title %q, list contains HTML: %v", p.Name, p.Title, p.HTML != "")
		}
	}
}

var (
	hrefRe = regexp.MustCompile(`href="([^"]*)"`)
	idRe   = regexp.MustCompile(`id="([^"]*)"`)
)

// Every link between documentation pages and every anchor must exist, so that
// the documentation has no dead links in the web interface.
func TestInternalLinksResolve(t *testing.T) {
	s, err := New(docs.FS, "dev")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]map[string]bool{}
	for _, name := range Pages {
		p, _ := s.Page(name)
		ids[name] = map[string]bool{}
		for _, m := range idRe.FindAllStringSubmatch(p.HTML, -1) {
			ids[name][m[1]] = true
		}
	}
	for _, name := range Pages {
		p, _ := s.Page(name)
		for _, m := range hrefRe.FindAllStringSubmatch(p.HTML, -1) {
			href := m[1]
			page, frag, _ := strings.Cut(href, "#")
			switch {
			case strings.HasPrefix(href, "#"):
				page = name
			case strings.HasPrefix(page, "/docs/"):
				page = strings.TrimPrefix(page, "/docs/")
			default:
				continue // external
			}
			if _, ok := ids[page]; !ok {
				t.Errorf("%s: link to unknown page %q", name, href)
				continue
			}
			if frag != "" && !ids[page][frag] {
				t.Errorf("%s: link %q: no heading %q in %s", name, href, frag, page)
			}
		}
	}
}

func renderOne(t *testing.T, md string) string {
	t.Helper()
	fsys := fstest.MapFS{}
	for _, p := range Pages {
		fsys[p+".md"] = &fstest.MapFile{Data: []byte("# " + p + "\n")}
	}
	fsys["user-guide.md"] = &fstest.MapFile{Data: []byte(md)}
	s, err := New(fsys, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.Page("user-guide")
	return p.HTML
}

func TestLinkRewriting(t *testing.T) {
	html := renderOne(t, "# Guide\n\n## Some `code` & Things\n\n"+
		"[a](security.md#ldap) [b](#some-code--things) [c](../configs/config.example.yaml) "+
		"[d](adr/0004-proxmox-provisioning.md#decisions) [e](https://example.com/x) [f](concept.md)\n")
	for _, want := range []string{
		`id="doc-some-code--things"`,
		`href="/docs/security#doc-ldap"`,
		`href="#doc-some-code--things"`,
		`href="https://github.com/joernott/dcdash/blob/v1.2.3/configs/config.example.yaml" target="_blank" rel="noopener noreferrer"`,
		`href="https://github.com/joernott/dcdash/blob/v1.2.3/docs/adr/0004-proxmox-provisioning.md#decisions"`,
		`href="https://example.com/x" target="_blank"`,
		`href="https://github.com/joernott/dcdash/blob/v1.2.3/docs/concept.md"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in\n%s", want, html)
		}
	}
}

func TestDevelopmentBuildsLinkToMain(t *testing.T) {
	fsys := fstest.MapFS{}
	for _, p := range Pages {
		fsys[p+".md"] = &fstest.MapFile{Data: []byte("# " + p + "\n\n[x](concept.md)\n")}
	}
	s, err := New(fsys, "dev")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.Page("dns")
	if !strings.Contains(p.HTML, "/blob/main/docs/concept.md") {
		t.Error(p.HTML)
	}
}

func TestRawHTMLIsNotRendered(t *testing.T) {
	html := renderOne(t, "# Guide\n\n<script>alert(1)</script>\n\nText <img src=x onerror=alert(1)> more\n\n[x](javascript:alert(1))\n")
	if strings.Contains(html, "<script") || strings.Contains(html, "<img") || strings.Contains(html, "onerror") {
		t.Errorf("raw HTML rendered: %s", html)
	}
	if strings.Contains(html, `href="javascript:`) {
		t.Errorf("javascript link rendered: %s", html)
	}
}

func TestDuplicateHeadings(t *testing.T) {
	html := renderOne(t, "# Guide\n\n## Example\n\n## Example\n")
	if !strings.Contains(html, `id="doc-example"`) || !strings.Contains(html, `id="doc-example-1"`) {
		t.Error(html)
	}
}

func TestAPI(t *testing.T) {
	s, err := New(docs.FS, "dev")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux, func(h http.Handler) http.Handler { return h })

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/docs", nil))
	var list struct{ Pages []Page }
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Pages) != len(Pages) || list.Pages[0].Name != "user-guide" {
		t.Errorf("list: %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/docs/security", nil))
	var p Page
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Name != "security" || !strings.Contains(p.HTML, "<h2") {
		t.Errorf("page: %d %.200s", rec.Code, rec.Body)
	}

	for _, name := range []string{"zz_prompts", "development", "..%2Fgo.mod", "nope"} {
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/docs/"+name, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}
