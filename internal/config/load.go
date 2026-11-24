// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// DefaultPath is the configuration directory used when neither --config nor DCDASH_CONFIG
// is given.
const DefaultPath = "/etc/dcdash/config.d"

// LegacyPath is the single configuration file of dcdash 0.3 and earlier. It is read when
// DefaultPath doesn't exist.
const LegacyPath = "/etc/dcdash/config.yaml"

// Load reads and validates the configuration at path: a directory, whose *.yaml files are
// merged in alphabetical order, or a single file. Warnings (e.g. insecure file permissions)
// are returned separately and do not fail loading.
func Load(path string) (*Config, []string, error) {
	var warnings []string
	path, warning := resolvePath(path, DefaultPath, LegacyPath)
	if warning != "" {
		warnings = append(warnings, warning)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, warnings, fmt.Errorf("open config: %w", err)
	}
	files := []string{path}
	if fi.IsDir() {
		if files, err = yamlFiles(path); err != nil {
			return nil, warnings, err
		}
	} else if path != LegacyPath {
		warnings = append(warnings, fmt.Sprintf("configuration %s is a single file; dcdash reads a directory of *.yaml files (see docs/configuration.md)", path))
	}

	takeSecretWarnings() // from earlier parses
	cfg, fileWarnings, err := loadFiles(files)
	warnings = append(warnings, fileWarnings...)
	if err != nil {
		return nil, warnings, err
	}
	warnings = append(warnings, takeSecretWarnings()...)
	if !cfg.Auth.LDAP.Enabled() && !cfg.Auth.Local.Enabled {
		warnings = append(warnings, "no login method configured (auth.ldap.url, auth.ldap.servers or auth.local): nobody can log in")
	}
	if cfg.Server.TLS.Mode == TLSModeNone {
		warnings = append(warnings, "server.tls.mode is \"none\": serving plain HTTP, use only for development or behind a TLS-terminating proxy")
	}
	return cfg, warnings, nil
}

// resolvePath returns the path to read. An installation upgraded from dcdash 0.3 still has its
// configuration in the single file legacy, next to the example files the package installed
// in the directory def: as long as legacy exists, it is read instead of def, with a warning.
func resolvePath(path, def, legacy string) (string, string) {
	if path != def {
		return path, ""
	}
	if fi, err := os.Stat(legacy); err == nil && fi.Mode().IsRegular() {
		return legacy, fmt.Sprintf("reading the configuration from %s instead of the directory %s: move it into the directory and remove the example files there (see docs/configuration.md)", legacy, def)
	}
	return path, ""
}

// yamlFiles returns the *.yaml files of dir in alphabetical order, without hidden files and
// subdirectories.
func yamlFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
			continue // a subdirectory or a dangling link
		}
		files = append(files, p)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("config directory %s: no *.yaml files", dir)
	}
	slices.Sort(files)
	return files, nil
}

// source is the content of one configuration file.
type source struct {
	name string
	data []byte
}

// loadFiles reads the files, merges them and parses the result.
func loadFiles(files []string) (*Config, []string, error) {
	var (
		warnings []string
		sources  []source
	)
	for _, name := range files {
		f, err := os.Open(name) // #nosec G304 -- the paths are given by the administrator
		if err != nil {
			return nil, warnings, fmt.Errorf("open config: %w", err)
		}
		if w := checkPermissions(f); w != "" {
			warnings = append(warnings, w)
		}
		data, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			return nil, warnings, fmt.Errorf("read config %s: %w", name, err)
		}
		sources = append(sources, source{name: name, data: data})
	}
	cfg, err := parseSources(sources)
	if err != nil {
		return nil, warnings, err
	}
	cfg.Sources = files
	return cfg, warnings, nil
}

// parseSources merges the sources (see merge) and parses the result like Parse. A decoding
// error is reported with the file and line it comes from.
func parseSources(sources []source) (*Config, error) {
	if len(sources) == 1 {
		cfg, err := Parse(bytes.NewReader(sources[0].data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", sources[0].name, err)
		}
		return cfg, nil
	}
	merged, err := merge(sources)
	if err != nil {
		return nil, err
	}
	cfg, err := Parse(bytes.NewReader(merged))
	if errors.Is(err, errParse) {
		// Unknown keys and wrong types: find the file. Each file must decode on its own.
		for _, s := range sources {
			dec := yaml.NewDecoder(bytes.NewReader(s.data))
			dec.KnownFields(true)
			var c Config
			if ferr := dec.Decode(&c); ferr != nil && !errors.Is(ferr, io.EOF) {
				return nil, fmt.Errorf("%s: parse config: %w", s.name, ferr)
			}
		}
	}
	return cfg, err
}

// merge merges YAML documents into one, in order:
//   - mappings are merged key by key, recursively;
//   - lists are concatenated;
//   - a value (string, number, bool) set in two files is an error, unless both are equal;
//   - an empty value (null) is replaced by any other.
//
// Errors name the files and lines.
func merge(sources []source) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	origin := map[*yaml.Node]string{}
	for _, s := range sources {
		var doc yaml.Node
		if err := yaml.Unmarshal(s.data, &doc); err != nil {
			return nil, fmt.Errorf("%s: parse config: %w", s.name, err)
		}
		if len(doc.Content) == 0 {
			continue // empty or only comments
		}
		top := doc.Content[0]
		if isNull(top) {
			continue
		}
		if top.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s: line %d: the top level must be a mapping of sections", s.name, top.Line)
		}
		markOrigin(top, s.name, origin)
		if err := mergeMapping(root, top, "", origin); err != nil {
			return nil, err
		}
	}
	out, err := yaml.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("merge config: %w", err)
	}
	return out, nil
}

func markOrigin(n *yaml.Node, file string, origin map[*yaml.Node]string) {
	origin[n] = file
	for _, c := range n.Content {
		markOrigin(c, file, origin)
	}
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// mergeMapping merges the mapping src into dst; path is the key path of both.
func mergeMapping(dst, src *yaml.Node, path string, origin map[*yaml.Node]string) error {
	for i := 0; i+1 < len(src.Content); i += 2 {
		k, v := src.Content[i], src.Content[i+1]
		key := k.Value
		if path != "" {
			key = path + "." + k.Value
		}
		j := slices.IndexFunc(dst.Content, func(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Value == k.Value })
		if j < 0 || j%2 != 0 {
			dst.Content = append(dst.Content, k, v)
			continue
		}
		merged, err := mergeValue(dst.Content[j+1], v, key, origin)
		if err != nil {
			return err
		}
		dst.Content[j+1] = merged
	}
	return nil
}

// mergeValue merges src into dst and returns the result.
func mergeValue(dst, src *yaml.Node, path string, origin map[*yaml.Node]string) (*yaml.Node, error) {
	switch {
	case isNull(src):
		return dst, nil
	case isNull(dst):
		return src, nil
	case dst.Kind != src.Kind:
		return nil, fmt.Errorf("%s: %s in %s (line %d), but %s in %s (line %d)", path,
			kindName(dst), origin[dst], dst.Line, kindName(src), origin[src], src.Line)
	case dst.Kind == yaml.MappingNode:
		return dst, mergeMapping(dst, src, path, origin)
	case dst.Kind == yaml.SequenceNode:
		dst.Content = append(dst.Content, src.Content...)
		return dst, nil
	case dst.Kind == yaml.ScalarNode && dst.Value == src.Value && dst.Tag == src.Tag:
		return dst, nil
	}
	return nil, fmt.Errorf("%s: set in %s (line %d) and in %s (line %d)", path,
		origin[dst], dst.Line, origin[src], src.Line)
}

func kindName(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	}
	return "a value"
}
