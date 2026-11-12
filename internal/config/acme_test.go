// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
	"time"
)

const acmeCloudflare = `
server:
  tls:
    mode: acme
    acme:
      accept_tos: true
      domains: [dcdash.example.com, "*.dcdash.example.com"]
      dns:
        provider: cloudflare
        cloudflare:
          api_token: token
`

func TestACMEDefaults(t *testing.T) {
	cfg, err := Parse(strings.NewReader(acmeCloudflare))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	a := cfg.Server.TLS.ACME
	if a.DirectoryURL != LetsEncryptDirectory || a.KeyType != "EC256" || a.RenewBefore.D() != 30*24*time.Hour {
		t.Errorf("defaults not applied: %+v", a)
	}
	if a.DNS.Cloudflare.APIToken.Value() != "token" {
		t.Error("api token not parsed")
	}
}

func TestACMERFC2136(t *testing.T) {
	t.Setenv("DCDASH_TEST_TSIG", "c2VjcmV0")
	cfg, err := Parse(strings.NewReader(`
server:
  tls:
    mode: acme
    acme:
      directory_url: https://ca.example.com/acme/acme/directory
      ca_file: /etc/dcdash/step-root.pem
      accept_tos: true
      domains: [dcdash.example.com]
      eab: {key_id: kid, hmac_key: secret}
      dns:
        provider: rfc2136
        rfc2136: {nameserver: ns1.example.com, tsig_key: acme, tsig_secret: "env:DCDASH_TEST_TSIG"}
        resolvers: [192.0.2.53, "192.0.2.54:5353"]
        disable_authoritative_check: true
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := cfg.Server.TLS.ACME.DNS.RFC2136
	if r.TSIGAlgorithm != "hmac-sha256" || r.TSIGSecret.Value() != "c2VjcmV0" {
		t.Errorf("unexpected rfc2136 config: %+v", r)
	}
}

func TestACMEValidation(t *testing.T) {
	_, err := Parse(strings.NewReader(`
server:
  tls:
    mode: acme
    acme:
      directory_url: http://insecure.example.com/directory
      domains: ["bad domain", "a..b"]
      key_type: DSA
      renew_before: 0s
      eab: {key_id: kid}
      dns:
        provider: rfc2136
        rfc2136: {tsig_algorithm: md5}
        resolvers: [""]
        propagation_timeout: -1s
`))
	if err == nil {
		t.Fatal("expected validation errors")
	}
	for _, want := range []string{
		"directory_url", "accept_tos", `invalid domain "bad domain"`, `invalid domain "a..b"`, "key_type",
		"renew_before", "eab", "rfc2136.nameserver", "tsig_key and tsig_secret", "tsig_algorithm",
		"dns.resolvers", "propagation_timeout",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s", want)
		}
	}
}

func TestACMEProviderRequired(t *testing.T) {
	_, err := Parse(strings.NewReader("server:\n  tls:\n    mode: acme\n    acme: {accept_tos: true, domains: [a.example]}\n"))
	if err == nil || !strings.Contains(err.Error(), "dns.provider") {
		t.Fatalf("expected provider error, got %v", err)
	}
	_, err = Parse(strings.NewReader("server:\n  tls:\n    mode: acme\n    acme: {accept_tos: true, domains: [a.example], dns: {provider: cloudflare}}\n"))
	if err == nil || !strings.Contains(err.Error(), "api_token") {
		t.Fatalf("expected api_token error, got %v", err)
	}
}

func TestWithDefaultPort(t *testing.T) {
	for in, want := range map[string]string{
		"ns1.example.com":      "ns1.example.com:53",
		"ns1.example.com:5353": "ns1.example.com:5353",
		"192.0.2.1":            "192.0.2.1:53",
		"2001:db8::1":          "[2001:db8::1]:53",
		"[2001:db8::1]:54":     "[2001:db8::1]:54",
	} {
		if got := WithDefaultPort(in); got != want {
			t.Errorf("WithDefaultPort(%q) = %q, want %q", in, got, want)
		}
	}
}
