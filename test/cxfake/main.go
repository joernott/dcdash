// SPDX-License-Identifier: GPL-3.0-or-later

// Command cxfake serves an in-memory Aruba CX switch (internal/switches/aoscx/cxfake) for
// the end-to-end tests (web/e2e/server.sh), over HTTPS with the given certificate, which
// dcdash pins by its fingerprint like a real switch's self-signed certificate. The fake
// holds no real data.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/joernott/dcdash/internal/switches/aoscx/cxfake"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:18030", "listen address")
	cert := flag.String("cert", "", "TLS certificate (PEM)")
	key := flag.String("key", "", "TLS key (PEM)")
	user := flag.String("user", "dcdash", "user name")
	password := flag.String("password", "cx-e2e-test-only", "password")
	flag.Parse()

	f := cxfake.New(*user, *password)
	f.Hostname = "sw-e2e"
	srv := &http.Server{Addr: *listen, Handler: f, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("cxfake listening on https://%s", *listen)
	log.Fatal(srv.ListenAndServeTLS(*cert, *key))
}
