// SPDX-License-Identifier: GPL-3.0-or-later

package pvefake

import (
	"crypto/sha1" // #nosec G505 -- the digest of Proxmox is a SHA-1 over the configuration, not a security feature
	"encoding/hex"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Configuration changes of VMs: GET .../pending and POST .../config (see
// docs/adr/0005-proxmox-vm-configuration.md). Changes of a running VM are
// pending, except for disks, network devices and the options that never need
// a restart.

var (
	diskKey   = regexp.MustCompile(`^(scsi|virtio|sata|ide)[0-9]+$`)
	unusedKey = regexp.MustCompile(`^unused[0-9]+$`)
	netKey    = regexp.MustCompile(`^net[0-9]+$`)
	newVolume = regexp.MustCompile(`^([a-z][a-z0-9_-]*):([0-9]+)$`)
)

// hotOption reports whether a change of key applies to a running VM at once.
func hotOption(key string) bool {
	switch key {
	case "name", "description", "tags", "onboot", "protection":
		return true
	}
	return diskKey.MatchString(key) || unusedKey.MatchString(key) || netKey.MatchString(key)
}

// defaults are the configuration values every fake VM has unless set.
func defaults(g *Guest) map[string]string {
	return map[string]string{"name": g.Name, "cores": "2", "memory": "2048", "net0": "virtio=BC:24:11:00:00:01,bridge=vmbr0"}
}

// effectiveConfig is the active configuration. Call with mu held.
func effectiveConfig(g *Guest) map[string]string {
	cfg := defaults(g)
	maps.Copy(cfg, g.Config)
	for k := range g.Removed {
		delete(cfg, k)
	}
	if g.Tags != "" {
		cfg["tags"] = g.Tags
	}
	return cfg
}

// digest of the configuration including pending changes. Call with mu held.
func digest(g *Guest) string {
	cfg := effectiveConfig(g)
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(cfg)) {
		b.WriteString(k + "=" + cfg[k] + "\n")
	}
	for _, k := range slices.Sorted(maps.Keys(g.Pending)) {
		b.WriteString("pending " + k + "=" + g.Pending[k] + "\n")
	}
	for _, k := range slices.Sorted(maps.Keys(g.PendingDelete)) {
		b.WriteString("delete " + k + "\n")
	}
	sum := sha1.Sum([]byte(b.String())) // #nosec G401 -- see import
	return hex.EncodeToString(sum[:])
}

// pending handles GET .../qemu/{vmid}/pending.
func (s *Server) pending(w http.ResponseWriter, g *Guest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := effectiveConfig(g)
	keys := map[string]bool{}
	for k := range cfg {
		keys[k] = true
	}
	for k := range g.Pending {
		keys[k] = true
	}
	for k := range g.PendingDelete {
		keys[k] = true
	}
	out := []map[string]any{{"key": "digest", "value": digest(g)}}
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		e := map[string]any{"key": k}
		if v, ok := cfg[k]; ok {
			e["value"] = v
		}
		if v, ok := g.Pending[k]; ok {
			e["pending"] = v
		}
		if g.PendingDelete[k] {
			e["delete"] = 1
		}
		out = append(out, e)
	}
	writeData(w, out)
}

// setConfig sets an active value. Call with mu held.
func setConfig(g *Guest, k, v string) {
	if g.Config == nil {
		g.Config = map[string]string{}
	}
	g.Config[k] = v
	delete(g.Removed, k)
	if k == "name" {
		g.Name = v
	}
	if k == "tags" {
		g.Tags = v
	}
}

// removeConfig removes an active value. Call with mu held.
func removeConfig(g *Guest, k string) {
	delete(g.Config, k)
	if g.Removed == nil {
		g.Removed = map[string]bool{}
	}
	g.Removed[k] = true
	if k == "tags" {
		g.Tags = ""
	}
}

// freeUnused returns the first free unusedN key. Call with mu held.
func freeUnused(cfg map[string]string) string {
	for i := 0; ; i++ {
		if k := "unused" + strconv.Itoa(i); cfg[k] == "" {
			return k
		}
	}
}

// configure handles POST .../qemu/{vmid}/config.
func (s *Server) configure(w http.ResponseWriter, r *http.Request, g *Guest, p, user string) {
	f := s.record(r, p)
	s.mu.Lock()
	defer s.mu.Unlock()
	id := strconv.Itoa(g.VMID)
	if d := f.Get("digest"); d != "" && d != digest(g) {
		// Proxmox checks the digest before it starts the task.
		fail(w, http.StatusInternalServerError, "checksum mismatch (file change by other user?)")
		return
	}
	exit := s.taskExit("qmconfig")
	if exit != "OK" {
		writeData(w, s.newTask(g.Node, "qmconfig", id, user, exit))
		return
	}
	running := g.Status != "stopped"
	for _, k := range strings.Split(f.Get("revert"), ",") {
		delete(g.Pending, k)
		delete(g.PendingDelete, k)
	}
	for _, k := range strings.Split(f.Get("delete"), ",") {
		cfg := effectiveConfig(g)
		switch {
		case k == "":
		case diskKey.MatchString(k) && cfg[k] != "":
			vol, _, _ := strings.Cut(cfg[k], ",")
			setConfig(g, freeUnused(cfg), vol)
			removeConfig(g, k)
		case running && !hotOption(k):
			if g.PendingDelete == nil {
				g.PendingDelete = map[string]bool{}
			}
			g.PendingDelete[k] = true
		default:
			removeConfig(g, k)
		}
	}
	for k := range f {
		v := f.Get(k)
		switch {
		case k == "digest" || k == "delete" || k == "revert" || k == "background_delay":
			continue
		case diskKey.MatchString(k):
			vol, opts, _ := strings.Cut(v, ",")
			if m := newVolume.FindStringSubmatch(vol); m != nil {
				s.seq++
				size := m[2] + "G"
				v = m[1] + ":vm-" + id + "-disk-" + strconv.Itoa(s.seq)
				if opts != "" {
					v += "," + opts
				}
				v += ",size=" + size
			} else {
				for uk, uv := range effectiveConfig(g) {
					if unusedKey.MatchString(uk) && uv == vol {
						removeConfig(g, uk)
					}
				}
			}
		}
		if running && !hotOption(k) {
			if g.Pending == nil {
				g.Pending = map[string]string{}
			}
			g.Pending[k] = v
			continue
		}
		setConfig(g, k, v)
	}
	writeData(w, s.newTask(g.Node, "qmconfig", id, user, "OK"))
}

// ApplyPending applies the pending changes of a VM, like a restart.
func (s *Server) ApplyPending(vmid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.guests {
		if g.VMID != vmid {
			continue
		}
		for k, v := range g.Pending {
			setConfig(g, k, v)
		}
		for k := range g.PendingDelete {
			removeConfig(g, k)
		}
		g.Pending, g.PendingDelete = nil, nil
	}
}
