// SPDX-License-Identifier: GPL-3.0-or-later

package docs

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/joernott/dcdash/internal/rbac"
)

// modulePackages maps the modules of permissions to the packages that check them.
var modulePackages = map[string]string{
	"proxmox":   "proxmox",
	"dns":       "dns",
	"dhcp":      "dhcp",
	"netbox":    "netbox",
	"project":   "project",
	"switch":    "switches",
	"firewall":  "firewalls",
	"directory": "directories",
}

var (
	// A row of the reference: | `module:instance:resource:action` | ... |
	referenceRow = regexp.MustCompile("(?m)^\\| `(([a-z]+):[^:`]+:([^`]+):([a-z]+))` \\|")
	placeholder  = regexp.MustCompile(`<[^>]+>`)
	actionUse    = regexp.MustCompile(`rbac\.Action([A-Z][a-z]+)`)
	// Lines that build a permission resource, and in them: prefixes ("port/" + ...), resource
	// constants (ResourceSync = "sync"), and literal resources of rbac.P, whose module is
	// the package's or a literal one (rbac.P("project", name, "objects", ...)).
	resourceLine = regexp.MustCompile(`rbac\.P\(|perm\(|can\(|Resource`)
	prefixUse    = regexp.MustCompile(`"([a-z]+/)"`)
	constUse     = regexp.MustCompile(`Resource[A-Za-z]*\s*=\s*"([a-z]+)"`)
	literalUse   = regexp.MustCompile(`rbac\.P\((?:Module|"([a-z]+)"),[^,]+,\s*"([a-z]+)"`)
	systemPerm   = regexp.MustCompile(`P\("system", "core", "([a-z]+)", Action([A-Z][a-z]+)\)`)
)

func source(t *testing.T, dir string) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "internal", dir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources in %s: %v", dir, err)
	}
	var b strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestPermissionReference keeps docs/permissions.md complete: every row is a valid
// permission, and per module it names the same actions the code checks and every resource
// the code builds.
func TestPermissionReference(t *testing.T) {
	data, err := os.ReadFile("permissions.md")
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]map[string]bool{}
	resources := map[string][]string{}
	for _, m := range referenceRow.FindAllStringSubmatch(string(data), -1) {
		perm, module, resource, action := m[1], m[2], m[3], m[4]
		if _, err := rbac.Parse(placeholder.ReplaceAllString(perm, "x")); err != nil {
			t.Errorf("invalid permission in the reference: %s: %v", perm, err)
		}
		if actions[module] == nil {
			actions[module] = map[string]bool{}
		}
		actions[module][action] = true
		resources[module] = append(resources[module], resource)
	}
	for _, m := range append(slices.Collect(maps.Keys(modulePackages)), "system") {
		if len(actions[m]) == 0 {
			t.Errorf("module %s missing in the reference", m)
		}
	}
	has := func(module, resource string) bool { return slices.Contains(resources[module], resource) }

	for module, dir := range modulePackages {
		src := source(t, dir)
		code := map[string]bool{}
		for _, a := range actionUse.FindAllStringSubmatch(src, -1) {
			code[strings.ToLower(a[1])] = true
		}
		for a := range code {
			if !actions[module][a] {
				t.Errorf("%s: the code checks %q, the reference doesn't list it", module, a)
			}
		}
		for a := range actions[module] {
			if !code[a] {
				t.Errorf("%s: the reference lists %q, the code never checks it", module, a)
			}
		}
		for line := range strings.SplitSeq(src, "\n") {
			if !resourceLine.MatchString(line) {
				continue
			}
			for _, p := range prefixUse.FindAllStringSubmatch(line, -1) {
				if !slices.ContainsFunc(resources[module], func(r string) bool { return strings.HasPrefix(r, p[1]) }) {
					t.Errorf("%s: the code builds resources %q..., the reference has none", module, p[1])
				}
			}
			for _, f := range constUse.FindAllStringSubmatch(line, -1) {
				if !has(module, f[1]) {
					t.Errorf("%s: the code uses the resource %q, the reference doesn't list it", module, f[1])
				}
			}
			for _, f := range literalUse.FindAllStringSubmatch(line, -1) {
				m := module
				if f[1] != "" {
					m = f[1]
				}
				if f[2] != "x" && !has(m, f[2]) {
					t.Errorf("%s: the code uses the resource %q, the reference doesn't list it", m, f[2])
				}
			}
		}
	}

	// System permissions are defined in internal/rbac.
	for _, m := range systemPerm.FindAllStringSubmatch(source(t, "rbac"), -1) {
		if !has("system", m[1]) || !actions["system"][strings.ToLower(m[2])] {
			t.Errorf("system:core:%s:%s missing in the reference", m[1], strings.ToLower(m[2]))
		}
	}
}
