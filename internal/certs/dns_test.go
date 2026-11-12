// SPDX-License-Identifier: GPL-3.0-or-later

package certs

import (
	"testing"

	"github.com/go-acme/lego/v5/challenge/dns01"

	"github.com/joernott/dcdash/internal/config"
)

func TestNewDNSProvider(t *testing.T) {
	cf := config.ACMEDNS{Provider: config.DNSProviderCloudflare}
	cf.Cloudflare.APIToken = config.NewSecret("token")
	if p, err := newDNSProvider(cf); err != nil || p == nil {
		t.Errorf("cloudflare: %v", err)
	}

	rfc := config.ACMEDNS{Provider: config.DNSProviderRFC2136, PropagationTimeout: config.Duration(1)}
	rfc.RFC2136 = config.RFC2136{
		Nameserver:    "ns1.example.com",
		TSIGKey:       "dcdash-acme",
		TSIGSecret:    config.NewSecret("c2VjcmV0c2VjcmV0c2VjcmV0"),
		TSIGAlgorithm: "hmac-sha256",
	}
	if p, err := newDNSProvider(rfc); err != nil || p == nil {
		t.Errorf("rfc2136: %v", err)
	}

	if _, err := newDNSProvider(config.ACMEDNS{Provider: "route53"}); err == nil {
		t.Error("expected error for unsupported provider")
	}
}

func TestChallengeOptions(t *testing.T) {
	if n := len(challengeOptions(config.ACMEDNS{DisableAuthoritativeCheck: true})); n != 1 {
		t.Errorf("got %d options", n)
	}
}

func TestConfigureResolvers(t *testing.T) {
	before := dns01.DefaultClient()
	t.Cleanup(func() { dns01.SetDefaultClient(before) })

	configureResolvers(config.ACMEDNS{})
	if dns01.DefaultClient() != before {
		t.Error("client replaced without resolvers")
	}
	configureResolvers(config.ACMEDNS{Resolvers: []string{"192.0.2.53", "[2001:db8::53]:5353"}})
	if dns01.DefaultClient() == before {
		t.Error("client not replaced")
	}
}
