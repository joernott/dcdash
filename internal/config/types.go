// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Duration is a time.Duration that is written as a Go duration string ("15s", "8h") in YAML.
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q: %w", n.Line, s, err)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// D returns the value as time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// Secret is a sensitive configuration value. In YAML it is given as
//
//   - "env:NAME"   the value of environment variable NAME
//   - "file:/path" the content of the file (one trailing newline is removed)
//   - anything else is used literally
//
// so that configuration files can be shared without containing secrets.
// Secrets are never printed: String, MarshalYAML and MarshalJSON return a mask.
type Secret struct {
	value string
}

const secretMask = "********"

// NewSecret creates a secret from a literal value.
func NewSecret(v string) Secret { return Secret{value: v} }

// UnmarshalYAML implements yaml.Unmarshaler.
func (s *Secret) UnmarshalYAML(n *yaml.Node) error {
	var raw string
	if err := n.Decode(&raw); err != nil {
		return err
	}
	v, err := resolveSecret(raw)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	s.value = v
	return nil
}

func resolveSecret(raw string) (string, error) {
	switch {
	case strings.HasPrefix(raw, "env:"):
		name := strings.TrimPrefix(raw, "env:")
		v, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("secret: environment variable %q is not set", name)
		}
		return v, nil
	case strings.HasPrefix(raw, "file:"):
		path := strings.TrimPrefix(raw, "file:")
		b, err := os.ReadFile(path) // #nosec G304 -- path is given by the administrator
		if err != nil {
			return "", fmt.Errorf("secret: %w", err)
		}
		v := strings.TrimSuffix(string(b), "\n")
		return strings.TrimSuffix(v, "\r"), nil
	default:
		return raw, nil
	}
}

// Value returns the secret in clear text.
func (s Secret) Value() string { return s.value }

// IsSet reports whether the secret is non-empty.
func (s Secret) IsSet() bool { return s.value != "" }

// String implements fmt.Stringer and never reveals the value.
func (s Secret) String() string {
	if s.value == "" {
		return ""
	}
	return secretMask
}

// MarshalYAML implements yaml.Marshaler and never reveals the value.
func (s Secret) MarshalYAML() (any, error) { return s.String(), nil }

// MarshalJSON implements json.Marshaler and never reveals the value.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + s.String() + `"`), nil }
