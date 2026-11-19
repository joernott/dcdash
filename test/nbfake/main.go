// SPDX-License-Identifier: GPL-3.0-or-later

// Command nbfake serves an in-memory NetBox fake (internal/netbox/nbfake) for
// the end-to-end tests (web/e2e/server.sh). It listens on plain HTTP on the
// loopback interface; the fake holds no real data.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/joernott/dcdash/internal/netbox/nbfake"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:18020", "listen address")
	token := flag.String("token", "nbt_e2e.netbox-e2e-test-only", "API token")
	flag.Parse()

	f := nbfake.New(*token)
	seed(f)
	srv := &http.Server{Addr: *listen, Handler: f, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("nbfake listening on http://%s", *listen)
	log.Fatal(srv.ListenAndServe())
}

// seed adds the objects the end-to-end tests expect: the cluster "Lab" (mapped
// to the Proxmox cluster lab of pvefake), VLANs 100 and 150 with prefixes that
// match the Kea fake's subnets, and the dcdash tag.
func seed(f *nbfake.Server) {
	f.Seed(nbfake.Tags, nbfake.Object{"name": "dcdash", "slug": "dcdash"})
	f.Seed(nbfake.Clusters, nbfake.Object{"name": "Lab"})
	servers := f.Seed(nbfake.VLANs, nbfake.Object{"vid": int64(100), "name": "servers", "description": "Server network"})
	storage := f.Seed(nbfake.VLANs, nbfake.Object{"vid": int64(150), "name": "storage"})
	f.Seed(nbfake.Prefixes, nbfake.Object{"prefix": "192.0.2.0/24", "vlan": servers})
	f.Seed(nbfake.Prefixes, nbfake.Object{"prefix": "2001:db8:1::/64", "vlan": servers})
	f.Seed(nbfake.Prefixes, nbfake.Object{"prefix": "198.51.100.0/24", "vlan": storage})
	for _, a := range []string{"192.0.2.1/24", "192.0.2.2/24", "192.0.2.10/24"} {
		f.Seed(nbfake.IPs, nbfake.Object{"address": a, "status": "active"})
	}
}
