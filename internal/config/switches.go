// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"net/url"
	"regexp"
	"strconv"
	"time"
)

var apiVersionPattern = regexp.MustCompile(`^v\d+\.\d+$`)

// SwitchTypeAOSCX is an Aruba CX switch (AOS-CX 10.x REST API).
const SwitchTypeAOSCX = "aoscx"

// Switches configures the network switches managed by dcdash (see
// docs/adr/0008-aruba-cx.md).
type Switches []Switch

// Switch is one switch (or stack), reached over its REST API.
type Switch struct {
	// Name identifies the switch in the UI and in permissions
	// (switch:<name>:<resource>:<action>).
	Name string `yaml:"name"`
	// Type of the switch API; "aoscx" (default) is the only one so far.
	Type string `yaml:"type"`
	// URL of the switch without path, e.g. https://sw1.example.com.
	URL string `yaml:"url"`
	// Insecure allows an http:// URL (password in clear text). Testing only.
	Insecure bool `yaml:"insecure"`
	// TLS: the factory certificates are self-signed and not issued for a host
	// name, so pin them with fingerprint; ca_file for certificates of your CA.
	TLS TLSClient `yaml:"tls"`
	// Username and Password of a switch account. The role "administrators"
	// allows changes, "operators" only reading.
	Username string `yaml:"username"`
	Password Secret `yaml:"password"`
	// APIVersion is the REST API version, e.g. "v10.08". Empty: the newest
	// version the switch offers.
	APIVersion string `yaml:"api_version"`
	// Timeout of a single request.
	Timeout Duration `yaml:"timeout"`
}

// DefaultSwitchTimeout is used when a switch sets no timeout.
const DefaultSwitchTimeout = 20 * time.Second

// RequestTimeout returns timeout with its default applied.
func (s Switch) RequestTimeout() time.Duration {
	if s.Timeout <= 0 {
		return DefaultSwitchTimeout
	}
	return s.Timeout.D()
}

func (s Switches) applyDefaults() {
	for i := range s {
		if s[i].Type == "" {
			s[i].Type = SwitchTypeAOSCX
		}
	}
}

func (s Switches) validate(add func(string, ...any)) {
	seen := map[string]bool{}
	for i, sw := range s {
		pre := "switches[" + strconv.Itoa(i) + "]"
		if !instanceNamePattern.MatchString(sw.Name) {
			add("%s.name %q: lower-case letters, digits, '.', '-' and '_' (max. 64)", pre, sw.Name)
		} else if seen[sw.Name] {
			add("%s.name %q: duplicate", pre, sw.Name)
		}
		seen[sw.Name] = true
		if sw.Type != SwitchTypeAOSCX {
			add("%s.type %q: must be aoscx", pre, sw.Type)
		}
		u, err := url.Parse(sw.URL)
		switch {
		case err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && u.Scheme != "http"):
			add("%s.url %q: must be https://host[:port] without a path", pre, sw.URL)
		case u.Scheme == "http" && !sw.Insecure:
			add("%s.url %q: http sends the password in clear text; use https or set insecure: true", pre, sw.URL)
		}
		sw.TLS.validate(pre+".tls.", add)
		if sw.Username == "" {
			add("%s.username: required", pre)
		}
		if !sw.Password.IsSet() {
			add("%s.password: required", pre)
		}
		if sw.APIVersion != "" && !apiVersionPattern.MatchString(sw.APIVersion) {
			add("%s.api_version %q: like v10.08", pre, sw.APIVersion)
		}
		if sw.Timeout < 0 {
			add("%s.timeout: must not be negative", pre)
		}
	}
}
