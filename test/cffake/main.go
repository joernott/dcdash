// SPDX-License-Identifier: GPL-3.0-or-later

// Command cffake serves the in-memory Cloudflare fake
// (internal/dns/cloudflare/cffake) for the end-to-end tests
// (web/e2e/server.sh). It listens on plain HTTP on the loopback interface:
// the Cloudflare SDK uses the system roots, and the fake holds no real data.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/joernott/dcdash/internal/dns/cloudflare/cffake"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:18007", "listen address")
	token := flag.String("token", "cf-e2e-test-only", "accepted API token")
	flag.Parse()

	f := cffake.New(*token, "example.org", "example.net")
	f.Add("example.org", cffake.Record{Name: "www.example.org", Type: "A", Content: "192.0.2.10", TTL: 1, Proxied: true})
	f.Add("example.org", cffake.Record{Name: "example.org", Type: "TXT", Content: `"v=spf1 -all"`, TTL: 3600})
	mux := http.NewServeMux()
	mux.Handle(cffake.Prefix, f)
	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("cffake listening on http://%s%s", *listen, cffake.Prefix)
	log.Fatal(srv.ListenAndServe())
}
