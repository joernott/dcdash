// SPDX-License-Identifier: GPL-3.0-or-later

package pvefake

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Creating and deleting guests: next ID, storages, ISO images and container
// templates, bridges, clone, create, config, resize and delete.

// Call is a recorded write request of the fake.
type Call struct {
	Method string
	Path   string
	Form   url.Values
}

// Calls returns the recorded write requests (POST, PUT, DELETE) except power
// actions and consoles.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

func (s *Server) record(r *http.Request, p string) url.Values {
	_ = r.ParseForm()
	form := r.Form
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		form = r.PostForm
	}
	s.mu.Lock()
	s.calls = append(s.calls, Call{Method: r.Method, Path: p, Form: form})
	s.mu.Unlock()
	return form
}

type storage struct {
	name, typ, content string
	shared             bool
}

var storages = []storage{
	{"local", "dir", "iso,vztmpl,backup", false},
	{"local-lvm", "lvmthin", "images,rootdir", false},
	{"ceph", "rbd", "images,rootdir", true},
}

var volumes = map[string][]string{
	"local/iso":    {"local:iso/debian-13.4.0-amd64-netinst.iso", "local:iso/virtio-win.iso"},
	"local/vztmpl": {"local:vztmpl/debian-13-standard_13.1-2_amd64.tar.zst"},
}

func (s *Server) used(vmid int) bool {
	return slices.ContainsFunc(s.guests, func(g *Guest) bool { return g.VMID == vmid })
}

func (s *Server) nextID(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := r.URL.Query().Get("vmid"); v != "" {
		id, _ := strconv.Atoi(v)
		if s.used(id) || s.set.Taken[id] {
			fail(w, http.StatusBadRequest, "VM "+v+" already exists")
			return
		}
		writeData(w, v)
		return
	}
	id := 100
	for s.used(id) || s.set.Taken[id] {
		id++
	}
	writeData(w, strconv.Itoa(id))
}

func (s *Server) nodeStorages(w http.ResponseWriter, r *http.Request) {
	content := r.URL.Query().Get("content")
	out := []map[string]any{}
	for _, st := range storages {
		if content != "" && !slices.Contains(strings.Split(st.content, ","), content) {
			continue
		}
		out = append(out, map[string]any{"storage": st.name, "type": st.typ, "content": st.content, "enabled": 1,
			"active": 1, "shared": map[bool]int{true: 1}[st.shared], "avail": 100 << 30, "total": 200 << 30})
	}
	writeData(w, out)
}

func storageContent(w http.ResponseWriter, r *http.Request, store string) {
	out := []map[string]any{}
	for _, v := range volumes[store+"/"+r.URL.Query().Get("content")] {
		out = append(out, map[string]any{"volid": v, "size": 1 << 30, "format": "iso", "content": r.URL.Query().Get("content")})
	}
	writeData(w, out)
}

func network(w http.ResponseWriter) {
	writeData(w, []map[string]any{
		{"iface": "vmbr0", "type": "bridge", "bridge_vlan_aware": 1},
		{"iface": "vmbr1", "type": "bridge"},
	})
}

// newTask records a finished task. Call with mu held.
func (s *Server) newTask(node, typ, id, user, exit string, log ...string) string {
	s.seq++
	now := time.Now()
	upid := fmt.Sprintf("UPID:%s:%08X:%08X:%08X:%s:%s:%s:", node, 1000+s.seq, 5000+s.seq, now.Unix(), typ, id, user)
	if exit == "OK" {
		log = append(log, "TASK OK")
	} else {
		log = append(log, "TASK ERROR: "+exit)
	}
	s.tasks[upid] = &task{upid: upid, node: node, typ: typ, id: id, user: user, start: now, end: now, exit: exit, log: log}
	return upid
}

// taskExit returns the exit status of a new task of type typ.
func (s *Server) taskExit(typ string) string {
	if msg, ok := s.set.FailTasks[typ]; ok {
		return msg
	}
	return "OK"
}

var sizeOpt = regexp.MustCompile(`size=([0-9]+)G`)

// create handles POST /nodes/{node}/{qemu,lxc}.
func (s *Server) create(w http.ResponseWriter, r *http.Request, node, typ, p, user string) {
	f := s.record(r, p)
	vmid, _ := strconv.Atoi(f.Get("vmid"))
	s.mu.Lock()
	defer s.mu.Unlock()
	if vmid < 100 || s.used(vmid) || s.set.Taken[vmid] {
		fail(w, http.StatusInternalServerError, "unable to create VM "+f.Get("vmid")+" - VM already exists")
		return
	}
	name := f.Get("name")
	if typ == "lxc" {
		name = f.Get("hostname")
	}
	ttype := map[string]string{"qemu": "qmcreate", "lxc": "vzcreate"}[typ]
	exit := s.taskExit(ttype)
	if exit == "OK" {
		cfg := map[string]string{}
		for k := range f {
			cfg[k] = f.Get(k)
		}
		s.guests = append(s.guests, &Guest{Type: typ, VMID: vmid, Name: name, Node: node, Status: "stopped", Config: cfg})
	}
	writeData(w, s.newTask(node, ttype, f.Get("vmid"), user, exit))
}

// clone handles POST .../qemu/{vmid}/clone.
func (s *Server) clone(w http.ResponseWriter, r *http.Request, g *Guest, p, user string) {
	f := s.record(r, p)
	newid, _ := strconv.Atoi(f.Get("newid"))
	s.mu.Lock()
	defer s.mu.Unlock()
	if !g.Template && f.Get("full") != "1" {
		fail(w, http.StatusInternalServerError, "linked clone feature is not supported for drive 'scsi0'")
		return
	}
	if newid < 100 || s.used(newid) || s.set.Taken[newid] {
		fail(w, http.StatusInternalServerError, "unable to create VM "+f.Get("newid")+" - VM already exists")
		return
	}
	exit := s.taskExit("qmclone")
	if exit == "OK" {
		cfg := map[string]string{}
		for k, v := range g.Config {
			if k != "template" {
				cfg[k] = v
			}
		}
		s.guests = append(s.guests, &Guest{Type: "qemu", VMID: newid, Name: f.Get("name"), Node: g.Node, Status: "stopped", Config: cfg})
	}
	writeData(w, s.newTask(g.Node, "qmclone", strconv.Itoa(g.VMID), user, exit))
}

// update handles PUT .../config and .../resize.
func (s *Server) update(w http.ResponseWriter, r *http.Request, g *Guest, rest, p, user string) {
	f := s.record(r, p)
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.Config == nil {
		g.Config = map[string]string{}
	}
	if rest == "config" {
		for k := range f {
			g.Config[k] = f.Get(k)
		}
		if n := f.Get("name"); n != "" {
			g.Name = n
		}
		writeData(w, nil)
		return
	}
	disk, size := f.Get("disk"), f.Get("size")
	cur := g.Config[disk]
	if cur == "" {
		fail(w, http.StatusBadRequest, "disk '"+disk+"' does not exist")
		return
	}
	g.Config[disk] = sizeOpt.ReplaceAllString(cur, "size="+size)
	writeData(w, s.newTask(g.Node, "resize", strconv.Itoa(g.VMID), user, s.taskExit("resize")))
}

// destroy handles DELETE /nodes/{node}/{type}/{vmid}.
func (s *Server) destroy(w http.ResponseWriter, r *http.Request, g *Guest, p, user string) {
	s.record(r, p)
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.Status != "stopped" {
		fail(w, http.StatusInternalServerError, "VM "+strconv.Itoa(g.VMID)+" is running - destroy failed")
		return
	}
	prefix := map[string]string{"qemu": "qm", "lxc": "vz"}[g.Type]
	exit := s.taskExit(prefix + "destroy")
	if exit == "OK" {
		s.guests = slices.DeleteFunc(s.guests, func(x *Guest) bool { return x == g })
	}
	writeData(w, s.newTask(g.Node, prefix+"destroy", strconv.Itoa(g.VMID), user, exit))
}
