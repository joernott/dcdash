// SPDX-License-Identifier: GPL-3.0-or-later

package aoscx_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/switches/aoscx"
	"github.com/joernott/dcdash/internal/switches/aoscx/cxfake"
	"github.com/joernott/dcdash/internal/tlsutil"
)

func newClient(t *testing.T, fake *cxfake.Server, change func(*config.Switch)) *aoscx.Client {
	t.Helper()
	hs := httptest.NewServer(fake)
	t.Cleanup(hs.Close)
	cfg := config.Switch{Name: "sw1", Type: config.SwitchTypeAOSCX, URL: hs.URL, Insecure: true,
		Username: "dcdash", Password: config.NewSecret("pw")}
	if change != nil {
		change(&cfg)
	}
	c, err := aoscx.New(cfg, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestInventoryReusesTheSession(t *testing.T) {
	ctx := context.Background()
	fake := cxfake.New("dcdash", "pw")
	fake.Subsystems["chassis,2"] = cxfake.Product{ProductName: "6000 48G", SerialNumber: "CN2"}
	c := newClient(t, fake, nil)
	inv, err := c.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Hostname != "sw-test" || inv.Platform != "6000" || inv.Firmware != "PL.10.08.1010" ||
		!inv.Booted.Equal(time.Unix(1795091390, 0)) || len(inv.Members) != 2 || inv.Members[0].Name != "1" ||
		inv.Members[0].Serial != "CN00000001" || inv.Members[1].Product != "6000 48G" {
		t.Errorf("%+v", inv)
	}
	if _, err := c.Inventory(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.Logins() != 1 || c.APIVersion() != "v10.08" {
		t.Errorf("logins %d, version %q", fake.Logins(), c.APIVersion())
	}
	if err := c.Close(ctx); err != nil || fake.Sessions() != 0 {
		t.Errorf("close: %v, %d sessions", err, fake.Sessions())
	}
	if err := c.Close(ctx); err != nil {
		t.Errorf("second close: %v", err)
	}
}

func TestExpiredSessionLogsInAgain(t *testing.T) {
	ctx := context.Background()
	fake := cxfake.New("dcdash", "pw")
	c := newClient(t, fake, nil)
	if _, err := c.Inventory(ctx); err != nil {
		t.Fatal(err)
	}
	fake.ExpireSessions()
	if _, err := c.Inventory(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.Logins() != 2 || fake.Sessions() != 1 {
		t.Errorf("logins %d, sessions %d", fake.Logins(), fake.Sessions())
	}
}

func TestConfiguredVersionAndCSRF(t *testing.T) {
	ctx := context.Background()
	fake := cxfake.New("dcdash", "pw")
	fake.CSRF = true
	c := newClient(t, fake, func(s *config.Switch) { s.APIVersion = cxfake.Version })
	// A changing request with the token gets past the CSRF check (404: the fake
	// has no such object).
	if err := c.Do(ctx, http.MethodPut, "/system/vlans/4000", map[string]any{"name": "x"}, nil); !errors.Is(err, aoscx.ErrNotFound) {
		t.Errorf("put: %v", err)
	}
	if slices.Contains(fake.Requests(), "GET /rest") {
		t.Errorf("version discovered although configured: %v", fake.Requests())
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	fake := cxfake.New("dcdash", "pw")
	if _, err := newClient(t, fake, func(s *config.Switch) { s.Password = config.NewSecret("wrong") }).Inventory(ctx); !errors.Is(err, aoscx.ErrUnauthorized) {
		t.Errorf("wrong password: %v", err)
	}

	fake.MaxSessions = 1
	c := newClient(t, fake, nil)
	if _, err := c.Inventory(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := newClient(t, fake, nil).Inventory(ctx); !errors.Is(err, aoscx.ErrSessionLimit) {
		t.Errorf("session limit: %v", err)
	}

	fake.FailNext(http.MethodGet, "/system", http.StatusInternalServerError)
	var e *aoscx.Error
	if _, err := c.Inventory(ctx); !errors.As(err, &e) || e.Status != 500 || e.Text != "injected failure" {
		t.Errorf("server error: %v", err)
	}
	// 10.08 has no PATCH and closes the connection.
	if err := c.Do(ctx, http.MethodPatch, "/system/vlans/4000", map[string]any{}, nil); !errors.Is(err, aoscx.ErrUnavailable) {
		t.Errorf("patch: %v", err)
	}
	unreachable, err := aoscx.New(config.Switch{Name: "x", URL: "http://127.0.0.1:1", Username: "u", Password: config.NewSecret("p")}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unreachable.Inventory(ctx); !errors.Is(err, aoscx.ErrUnavailable) {
		t.Errorf("unreachable: %v", err)
	}
}

func TestPinnedCertificate(t *testing.T) {
	ctx := context.Background()
	fake := cxfake.New("dcdash", "pw")
	hs := httptest.NewTLSServer(fake)
	t.Cleanup(hs.Close)
	pin := tlsutil.Fingerprint(hs.Certificate().Raw)
	for _, tc := range []struct {
		fingerprint string
		ok          bool
	}{{pin, true}, {"00:" + pin[3:], false}} {
		c, err := aoscx.New(config.Switch{Name: "sw1", URL: hs.URL, Username: "dcdash", Password: config.NewSecret("pw"),
			TLS: config.TLSClient{Fingerprint: tc.fingerprint}}, zerolog.Nop())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Inventory(ctx); (err == nil) != tc.ok {
			t.Errorf("fingerprint %s: %v", tc.fingerprint, err)
		}
	}
}
