// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package integration

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/switches/aoscx"
)

// TestArubaCX reads from a real Aruba CX switch (DCDASH_IT_ARUBA_URL, _FINGERPRINT,
// _USER, _PASSWORD). It changes nothing.
func TestArubaCX(t *testing.T) {
	rawURL := os.Getenv("DCDASH_IT_ARUBA_URL")
	if rawURL == "" {
		t.Skip("DCDASH_IT_ARUBA_URL not set")
	}
	ctx := context.Background()
	c, err := aoscx.New(config.Switch{Name: "it", Type: config.SwitchTypeAOSCX, URL: rawURL,
		TLS:      config.TLSClient{Fingerprint: os.Getenv("DCDASH_IT_ARUBA_FINGERPRINT")},
		Username: requireEnv(t, "DCDASH_IT_ARUBA_USER"), Password: config.NewSecret(requireEnv(t, "DCDASH_IT_ARUBA_PASSWORD"))},
		zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(ctx); err != nil {
			t.Errorf("logout: %v", err)
		}
	})
	// Parallel requests share one session.
	var wg sync.WaitGroup
	invs := make([]aoscx.Inventory, 4)
	errs := make([]error, 4)
	for i := range invs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			invs[i], errs[i] = c.Inventory(ctx)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	inv := invs[0]
	t.Logf("%s: platform %s, firmware %s, API %s, booted %s, members %+v", inv.Hostname, inv.Platform, inv.Firmware,
		c.APIVersion(), inv.Booted, inv.Members)
	if inv.Hostname == "" || inv.Firmware == "" || !strings.HasPrefix(c.APIVersion(), "v10.") || len(inv.Members) == 0 ||
		inv.Members[0].Serial == "" || inv.Booted.IsZero() {
		t.Errorf("%+v", inv)
	}
}
