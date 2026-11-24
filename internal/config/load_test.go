// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeDir writes files (name: content) into a new directory and returns it.
func writeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const switchYAML = `
switches:
  - name: %s
    url: https://%s.example.com
    username: admin
    password: secret
`

func switchFile(name string) string {
	return strings.ReplaceAll(switchYAML, "%s", name)
}

func TestLoadDirectoryMerges(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"00_server.yaml": testKeyYAML + minimal,
		"05_log.yaml":    "log:\n  level: debug\n",
		// Mappings merge key by key: server.listen next to server.tls from 00_server.yaml.
		"06_listen.yaml":       "server:\n  listen: \":9443\"\n",
		"80_switches.yaml":     switchFile("sw2"),
		"81_sw1.yaml":          switchFile("sw1"),
		"82_empty.yaml":        "# only a comment\n",
		"83_null.yaml":         "switches:\n",
		".hidden.yaml":         "log: {level: nonsense}\n",
		"README.md":            "not read",
		"90_old.yaml.dpkg-old": "log: {level: nonsense}\n",
		"sub/99_x.yaml":        "log: {level: nonsense}\n",
	})
	cfg, warnings, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, "single file") }) {
		t.Errorf("warnings = %v", warnings)
	}
	if cfg.Log.Level != "debug" || cfg.Server.Listen != ":9443" || cfg.Server.TLS.CertFile != "/etc/dcdash/cert.pem" {
		t.Errorf("merged: log %+v, server %+v", cfg.Log, cfg.Server)
	}
	// Lists are concatenated in file order.
	var names []string
	for _, s := range cfg.Switches {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"sw2", "sw1"}) {
		t.Errorf("switches = %v", names)
	}
	want := []string{"00_server.yaml", "05_log.yaml", "06_listen.yaml", "80_switches.yaml", "81_sw1.yaml", "82_empty.yaml", "83_null.yaml"}
	var got []string
	for _, s := range cfg.Sources {
		got = append(got, filepath.Base(s))
	}
	if !slices.Equal(got, want) {
		t.Errorf("sources = %v", got)
	}
}

func TestLoadDirectoryErrors(t *testing.T) {
	for _, c := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"conflicting value", map[string]string{
			"00_server.yaml": testKeyYAML + minimal,
			"05_log.yaml":    "log:\n  level: debug\n",
			"06_log.yaml":    "\nlog:\n  level: warn\n",
		}, []string{"log.level: set in", "05_log.yaml (line 2)", "06_log.yaml (line 3)"}},
		{"list and mapping", map[string]string{
			"00_server.yaml": testKeyYAML + minimal,
			"80_a.yaml":      switchFile("sw1"),
			"81_b.yaml":      "switches:\n  name: sw2\n",
		}, []string{"switches: a list in", "80_a.yaml", "but a mapping in", "81_b.yaml"}},
		{"unknown key", map[string]string{
			"00_server.yaml": testKeyYAML + minimal,
			"05_log.yaml":    "log:\n  level: debug\n  verbosity: true\n",
		}, []string{"05_log.yaml: parse config", "line 3", "verbosity"}},
		{"syntax error", map[string]string{
			"00_server.yaml": testKeyYAML + minimal,
			"05_log.yaml":    "log: [\n",
		}, []string{"05_log.yaml: parse config"}},
		{"top level", map[string]string{
			"00_server.yaml": testKeyYAML + minimal,
			"05_list.yaml":   "- a\n",
		}, []string{"05_list.yaml: line 1: the top level must be a mapping"}},
		{"duplicate switch is still validated", map[string]string{
			"00_server.yaml": testKeyYAML + minimal,
			"80_a.yaml":      switchFile("sw1"),
			"81_b.yaml":      switchFile("sw1"),
		}, []string{`switches[1].name "sw1": duplicate`}},
		{"no files", map[string]string{"README": "x"}, []string{"no *.yaml files"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := Load(writeDir(t, c.files))
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("missing %q in %v", w, err)
				}
			}
		})
	}
}

func TestLoadEqualValuesAndSingleFile(t *testing.T) {
	// The same value in two files is no conflict.
	dir := writeDir(t, map[string]string{
		"00_server.yaml": testKeyYAML + minimal + "log: {level: debug}\n",
		"05_log.yaml":    "log: {level: debug}\n",
	})
	if _, _, err := Load(dir); err != nil {
		t.Errorf("equal values: %v", err)
	}
	// A single file is read alone, with a warning.
	cfg, warnings, err := Load(filepath.Join(dir, "05_log.yaml"))
	if err == nil {
		t.Fatalf("a file without the server section must fail validation: %+v", cfg)
	}
	if !strings.Contains(err.Error(), "05_log.yaml") {
		t.Errorf("error names the file: %v", err)
	}
	if !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, "single file") }) {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestResolvePath(t *testing.T) {
	dir := writeDir(t, map[string]string{"config.d/00_server.yaml": "x", "config.yaml": "x"})
	def, legacy := filepath.Join(dir, "config.d"), filepath.Join(dir, "config.yaml")
	// Upgraded from 0.3: the old file wins over the example files of the package.
	if p, w := resolvePath(def, def, legacy); p != legacy || !strings.Contains(w, "move it into the directory") {
		t.Errorf("legacy: %q %q", p, w)
	}
	// An explicit path is used as given.
	if p, w := resolvePath(legacy+".d", def, legacy); p != legacy+".d" || w != "" {
		t.Errorf("explicit: %q %q", p, w)
	}
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}
	if p, w := resolvePath(def, def, legacy); p != def || w != "" {
		t.Errorf("migrated: %q %q", p, w)
	}
}
