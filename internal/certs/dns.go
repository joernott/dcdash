// SPDX-License-Identifier: GPL-3.0-or-later

package certs

import (
	"fmt"
	"strings"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/challenge/dns01"
	"github.com/go-acme/lego/v5/providers/dns/cloudflare"
	"github.com/go-acme/lego/v5/providers/dns/dnsupdate"

	"github.com/joernott/dcdash/internal/config"
)

// newDNSProvider creates the DNS-01 challenge provider from the configuration.
// Only the providers dcdash supports are linked in, which keeps the binary small.
func newDNSProvider(c config.ACMEDNS) (challenge.Provider, error) {
	switch c.Provider {
	case config.DNSProviderCloudflare:
		cfg := cloudflare.NewDefaultConfig()
		cfg.AuthToken = c.Cloudflare.APIToken.Value()
		cfg.ZoneToken = c.Cloudflare.ZoneToken.Value()
		if c.PropagationTimeout > 0 {
			cfg.PropagationTimeout = c.PropagationTimeout.D()
		}
		p, err := cloudflare.NewDNSProviderConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("cloudflare DNS provider: %w", err)
		}
		return p, nil

	case config.DNSProviderRFC2136:
		r := c.RFC2136
		cfg := dnsupdate.NewDefaultConfig()
		cfg.Nameserver = config.WithDefaultPort(r.Nameserver)
		cfg.TSIGKey = r.TSIGKey
		cfg.TSIGSecret = r.TSIGSecret.Value()
		cfg.TSIGAlgorithm = r.TSIGAlgorithm
		if c.PropagationTimeout > 0 {
			cfg.PropagationTimeout = c.PropagationTimeout.D()
		}
		p, err := dnsupdate.NewDNSProviderConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("RFC 2136 DNS provider: %w", err)
		}
		return p, nil

	default:
		return nil, fmt.Errorf("unsupported DNS provider %q", c.Provider)
	}
}

// challengeOptions translates the propagation settings into lego options.
func challengeOptions(c config.ACMEDNS) []dns01.ChallengeOption {
	return []dns01.ChallengeOption{
		dns01.CondOptions(c.DisableAuthoritativeCheck, dns01.DisableAuthoritativeNssPropagationRequirement()),
	}
}

// configureResolvers sets the resolvers lego uses to look up zones and check
// record propagation. lego keeps them in a process-wide default client; dcdash
// runs a single ACME manager, so this is safe.
func configureResolvers(c config.ACMEDNS) {
	if len(c.Resolvers) == 0 {
		return
	}
	ns := make([]string, 0, len(c.Resolvers))
	for _, r := range c.Resolvers {
		ns = append(ns, config.WithDefaultPort(strings.TrimSpace(r)))
	}
	opts := dns01.NewOptions()
	opts.RecursiveNameservers = ns
	dns01.SetDefaultClient(dns01.NewClient(opts))
}
