// SPDX-License-Identifier: GPL-3.0-or-later

// Package project manages projects: groups of objects (guests, addresses, VLANs, ...) marked
// with the tag project-<name> in Proxmox and NetBox. Projects are defined in YAML files, one
// per project (see docs/adr/0007-projects.md).
package project

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// TagPrefix is the prefix of project tags.
const TagPrefix = "project-"

// DefaultColor is the NetBox tag color of projects without color.
const DefaultColor = "9e9e9e"

// Project is a project file.
type Project struct {
	// Name is the file name without .yaml.
	Name        string `yaml:"-" json:"name"`
	Title       string `yaml:"title" json:"title"`
	Color       string `yaml:"color" json:"color"`
	Description string `yaml:"description" json:"description"`
}

// Tag returns the tag of the project, project-<name>.
func (p Project) Tag() string { return TagPrefix + p.Name }

var (
	// The tag project-<name> must be a Proxmox tag ([a-z0-9_][a-z0-9_+.-]*)
	// and a NetBox slug (at most 100 characters); Proxmox limits tags to 64
	// characters in dcdash.
	namePattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,53}[a-z0-9])?$`)
	colorPattern = regexp.MustCompile(`^[0-9a-f]{6}$`)
)

// ValidName reports whether s is a valid project name.
func ValidName(s string) bool { return namePattern.MatchString(s) }

// NameOfTag returns the project name of a tag, or false if it is no project
// tag.
func NameOfTag(tag string) (string, bool) {
	name, ok := strings.CutPrefix(tag, TagPrefix)
	return name, ok && ValidName(name)
}

// maxFileSize limits a project file.
const maxFileSize = 64 << 10

// Load reads the project files of dir. Invalid files are skipped and
// returned as errors. A missing directory is no error.
func Load(dir string) ([]Project, []error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{fmt.Errorf("projects: %w", err)}
	}
	var (
		out  []Project
		errs []error
	)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p, err := loadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Name(), err))
			continue
		}
		if i := slices.IndexFunc(out, func(o Project) bool { return o.Title == p.Title }); i >= 0 {
			errs = append(errs, fmt.Errorf("%s: the title %q is used by %s.yaml (NetBox tag names are unique)", e.Name(), p.Title, out[i].Name))
			continue
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Project) int { return strings.Compare(a.Name, b.Name) })
	return out, errs
}

func loadFile(path string) (Project, error) {
	name := strings.TrimSuffix(filepath.Base(path), ".yaml")
	if !ValidName(name) {
		return Project{}, errors.New("the file name must be <name>.yaml; names have lower-case letters, digits and '-' (at most 55)")
	}
	f, err := os.Open(path) // #nosec G304 -- a file of the configured directory
	if err != nil {
		return Project{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return Project{}, err
	}
	if len(data) > maxFileSize {
		return Project{}, errors.New("file too large")
	}
	var p Project
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil && !errors.Is(err, io.EOF) {
		return Project{}, err
	}
	p.Name = name
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" {
		p.Title = p.Tag()
	}
	if len(p.Title) > 100 {
		return Project{}, errors.New("title: at most 100 characters")
	}
	p.Color = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(p.Color), "#"))
	if p.Color == "" {
		p.Color = DefaultColor
	}
	if !colorPattern.MatchString(p.Color) {
		return Project{}, fmt.Errorf("color %q: 6 hex digits, e.g. 2196f3", p.Color)
	}
	if len(p.Description) > 200 {
		return Project{}, errors.New("description: at most 200 characters")
	}
	return p, nil
}
