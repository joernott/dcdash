// SPDX-License-Identifier: GPL-3.0-or-later

// Command opnfake serves an in-memory OPNsense firewall (internal/firewalls/opnsense/opnfake)
// for the end-to-end tests (web/e2e/server.sh), over HTTPS with the given certificate, which
// dcdash pins by its fingerprint. A rule that blocks "(self)" cuts the API off until the
// savepoint rolls it back. The fake holds no real data.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/joernott/dcdash/internal/firewalls/opnsense/opnfake"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:18040", "listen address")
	cert := flag.String("cert", "", "TLS certificate (PEM)")
	key := flag.String("key", "", "TLS key (PEM)")
	apiKey := flag.String("api-key", "e2e-key", "API key")
	secret := flag.String("api-secret", "opn-e2e-test-only", "API secret")
	flag.Parse()

	f := opnfake.New(*apiKey, *secret)
	f.CutsOff = func(r opnfake.Rule) bool { return r["action"] == "block" && r["destination_net"] == "(self)" }
	srv := &http.Server{Addr: *listen, Handler: f, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("opnfake listening on https://%s", *listen)
	log.Fatal(srv.ListenAndServeTLS(*cert, *key))
}
