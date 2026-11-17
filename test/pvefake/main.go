// SPDX-License-Identifier: GPL-3.0-or-later

// Command pvefake serves the in-memory Proxmox fake (internal/proxmox/pvefake)
// over HTTPS for the end-to-end tests (web/e2e/server.sh).
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/joernott/dcdash/internal/proxmox/pvefake"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:18006", "listen address")
	cert := flag.String("cert", "", "TLS certificate (PEM)")
	key := flag.String("key", "", "TLS key (PEM)")
	token := flag.String("token", "dcdash@pve!e2e=e2e-test-only", "accepted API token (id=secret)")
	flag.Parse()

	f := pvefake.New(*token, []string{"pve1", "pve2"},
		pvefake.Guest{Type: "qemu", VMID: 101, Name: "web", Node: "pve1", Status: "running", Tags: "prod;web"},
		pvefake.Guest{Type: "qemu", VMID: 102, Name: "db", Node: "pve2", Status: "stopped"},
		pvefake.Guest{Type: "lxc", VMID: 200, Name: "proxy", Node: "pve1", Status: "running"},
		pvefake.Guest{Type: "qemu", VMID: 9000, Name: "debian-ci", Node: "pve2", Status: "stopped", Template: true, Config: map[string]string{
			"scsi0": "local-lvm:base-9000-disk-0,size=10G", "ide2": "local-lvm:vm-9000-cloudinit,media=cdrom",
			"boot": "order=scsi0", "net0": "virtio=BC:24:11:00:00:09,bridge=vmbr0"}},
	)
	f.Set(func(s *pvefake.Settings) { s.User, s.Password = "console@pve", "console-e2e" })
	srv := &http.Server{Addr: *listen, Handler: f, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("pvefake listening on https://%s", *listen)
	log.Fatal(srv.ListenAndServeTLS(*cert, *key))
}
