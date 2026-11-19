// SPDX-License-Identifier: GPL-3.0-or-later

package nbfake

import (
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/netbox"
)

// NewService starts a fake with the dcdash tag and the cluster "Lab"
// (mapped to the Proxmox cluster lab) and returns it with a NetBox service
// that writes to it. For the tests of the modules that write to NetBox.
func NewService(t testing.TB, auditLog *audit.Log) (*Server, *netbox.Service) {
	t.Helper()
	const token = "nbt_t.test-only" // #nosec G101 -- test-only token of the fake
	f := New(token)
	f.Seed(Tags, Object{"name": "dcdash", "slug": "dcdash"})
	f.Seed(Clusters, Object{"name": "Lab"})
	hs := httptest.NewServer(f)
	t.Cleanup(hs.Close)
	nb, err := netbox.NewService(config.NetBox{Name: "netbox", URL: hs.URL, Insecure: true, Token: config.NewSecret(token),
		WriteBack: true, Tag: "dcdash", OnDelete: config.NetBoxDeleteTagged, Clusters: map[string]string{"lab": "Lab"}},
		auditLog, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return f, nb
}
