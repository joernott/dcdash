// SPDX-License-Identifier: GPL-3.0-or-later

// Command keafake serves two in-memory Kea fakes (internal/dhcp/kea/keafake),
// a DHCPv4 and a DHCPv6 server, for the end-to-end tests (web/e2e/server.sh).
// It listens on plain HTTP on the loopback interface; the fakes hold no real
// data.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/joernott/dcdash/internal/dhcp/kea/keafake"
)

func main() {
	listen4 := flag.String("listen4", "127.0.0.1:18010", "listen address of the DHCPv4 server")
	listen6 := flag.String("listen6", "127.0.0.1:18011", "listen address of the DHCPv6 server")
	password := flag.String("password", "kea-e2e-test-only", "password of the user dcdash")
	flag.Parse()

	v4 := keafake.New("v4", "dcdash", *password,
		keafake.Subnet{ID: 1, Prefix: "192.0.2.0/24", Pools: []string{"192.0.2.100-192.0.2.199"}, Total: 100},
		keafake.Subnet{ID: 2, Prefix: "198.51.100.0/24", Pools: []string{"198.51.100.10-198.51.100.19"}, Total: 10},
	)
	v4.AddLease(keafake.Lease{IP: "192.0.2.100", HWAddress: "11:22:33:44:55:01", Hostname: "laptop", SubnetID: 1})
	v4.AddLease(keafake.Lease{IP: "192.0.2.101", HWAddress: "11:22:33:44:55:02", Hostname: "phone", SubnetID: 1})
	v4.AddLease(keafake.Lease{IP: "198.51.100.10", HWAddress: "11:22:33:44:55:03", Hostname: "camera", SubnetID: 2})
	v4.AddConfigReservation(keafake.Host{"subnet-id": 1, "hw-address": "aa:bb:cc:00:00:01", "ip-address": "192.0.2.10", "hostname": "printer"})

	v6 := keafake.New("v6", "dcdash", *password,
		keafake.Subnet{ID: 1, Prefix: "2001:db8:1::/64", Pools: []string{"2001:db8:1::1000-2001:db8:1::1fff"}, Total: 4096,
			PDPools: []map[string]any{{"prefix": "2001:db8:8000::", "prefix-len": 48, "delegated-len": 56}}, TotalPD: 256},
	)
	v6.AddLease(keafake.Lease{IP: "2001:db8:1::1000", DUID: "00:03:00:01:11:22:33:44:55:01", IAID: 1, SubnetID: 1})

	errs := make(chan error, 2)
	for addr, f := range map[string]*keafake.Server{*listen4: v4, *listen6: v6} {
		srv := &http.Server{Addr: addr, Handler: f, ReadHeaderTimeout: 10 * time.Second}
		log.Printf("keafake listening on http://%s", addr)
		go func() { errs <- srv.ListenAndServe() }()
	}
	log.Fatal(<-errs)
}
