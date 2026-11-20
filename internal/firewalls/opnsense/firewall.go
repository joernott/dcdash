// SPDX-License-Identifier: GPL-3.0-or-later

package opnsense

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// ErrNoSavepoint means that the firewall has no savepoint API (OPNsense 26.7
// and later), so rule changes can't be rolled back automatically.
var ErrNoSavepoint = errors.New("opnsense: the firewall has no savepoint for filter rules")

// Alias is a firewall alias.
type Alias struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
	// Content are the entries (addresses, networks, ports, URLs, ...).
	Content     []string `json:"content"`
	Description string   `json:"description"`
}

// AliasTypes are the alias types dcdash edits; others are shown read-only.
var AliasTypes = []string{"host", "network", "port", "url", "urltable", "mac", "networkgroup"}

// Aliases reads the aliases (without the internal ones).
func (c *Client) Aliases(ctx context.Context) ([]Alias, error) {
	rows, err := searchAll[struct {
		UUID        flexString `json:"uuid"`
		Name        flexString `json:"name"`
		Type        flexString `json:"type"`
		Enabled     flexString `json:"enabled"`
		Content     flexString `json:"content"`
		Description flexString `json:"description"`
	}](ctx, c, "/firewall/alias/search_item")
	if err != nil {
		return nil, err
	}
	out := make([]Alias, 0, len(rows))
	for _, r := range rows {
		typ := strings.ToLower(string(r.Type))
		if typ == "internal" || typ == "external" {
			continue
		}
		out = append(out, Alias{UUID: string(r.UUID), Name: string(r.Name), Type: typ, Enabled: r.Enabled.bool(),
			Content: splitContent(string(r.Content)), Description: string(r.Description)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// splitContent splits the content of a search row: newlines or commas.
func splitContent(s string) []string {
	out := []string{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func aliasBody(a Alias) map[string]any {
	return map[string]any{"alias": map[string]any{
		"enabled": boolString(a.Enabled), "name": a.Name, "type": a.Type,
		"content": strings.Join(a.Content, "\n"), "description": a.Description,
	}}
}

// AddAlias creates an alias and returns its UUID.
func (c *Client) AddAlias(ctx context.Context, a Alias) (string, error) {
	var r struct {
		UUID string `json:"uuid"`
	}
	if err := c.Post(ctx, "/firewall/alias/add_item", aliasBody(a), &r); err != nil {
		return "", err
	}
	return r.UUID, nil
}

// SetAlias changes an alias.
func (c *Client) SetAlias(ctx context.Context, a Alias) error {
	return c.Post(ctx, "/firewall/alias/set_item/"+url.PathEscape(a.UUID), aliasBody(a), nil)
}

// DeleteAlias deletes an alias; OPNsense refuses aliases in use.
func (c *Client) DeleteAlias(ctx context.Context, uuid string) error {
	var r struct {
		Result string `json:"result"`
	}
	if err := c.Post(ctx, "/firewall/alias/del_item/"+url.PathEscape(uuid), nil, &r); err != nil {
		return err
	}
	if r.Result != "deleted" {
		return fmt.Errorf("%w: alias %s (%s)", ErrNotFound, uuid, r.Result)
	}
	return nil
}

// ApplyAliases loads the changed aliases into the packet filter.
func (c *Client) ApplyAliases(ctx context.Context) error {
	return c.Post(ctx, "/firewall/alias/reconfigure", nil, nil)
}

// Rule is a filter rule of the rules page "Rules [new]".
type Rule struct {
	UUID     string `json:"uuid"`
	Enabled  bool   `json:"enabled"`
	Sequence int    `json:"sequence"`
	// Action is "pass", "block" or "reject".
	Action    string `json:"action"`
	Quick     bool   `json:"quick"`
	Interface string `json:"interface"`
	// Direction is "in" or "out"; IPProtocol "inet", "inet6" or "inet46".
	Direction  string `json:"direction"`
	IPProtocol string `json:"ipProtocol"`
	// Protocol is "any", "TCP", "UDP", "TCP/UDP", "ICMP", ...
	Protocol        string `json:"protocol"`
	Source          string `json:"source"`
	SourceNot       bool   `json:"sourceNot"`
	SourcePort      string `json:"sourcePort"`
	Destination     string `json:"destination"`
	DestinationNot  bool   `json:"destinationNot"`
	DestinationPort string `json:"destinationPort"`
	Log             bool   `json:"log"`
	Description     string `json:"description"`
	// Legacy rules (from the old rules page) and automatic rules are read-only.
	Legacy bool `json:"legacy"`
}

// Rules reads the filter rules in their order.
func (c *Client) Rules(ctx context.Context) ([]Rule, error) {
	rows, err := searchAll[struct {
		UUID            flexString `json:"uuid"`
		Enabled         flexString `json:"enabled"`
		Sequence        flexString `json:"sequence"`
		Action          flexString `json:"action"`
		Quick           flexString `json:"quick"`
		Interface       flexString `json:"interface"`
		InterfaceName   flexString `json:"%interface"`
		Direction       flexString `json:"direction"`
		IPProtocol      flexString `json:"ipprotocol"`
		Protocol        flexString `json:"protocol"`
		SourceNet       flexString `json:"source_net"`
		SourceNot       flexString `json:"source_not"`
		SourcePort      flexString `json:"source_port"`
		DestinationNet  flexString `json:"destination_net"`
		DestinationNot  flexString `json:"destination_not"`
		DestinationPort flexString `json:"destination_port"`
		Log             flexString `json:"log"`
		Description     flexString `json:"description"`
		Legacy          flexString `json:"legacy"`
	}](ctx, c, "/firewall/filter/search_rule?show_all=1")
	if err != nil {
		return nil, err
	}
	// Automatic rules name the interface by its description; the others by its key
	// (opt1) with the description in %interface.
	keys := map[string]string{}
	for _, r := range rows {
		if r.InterfaceName != "" && !strings.Contains(string(r.Interface), ",") {
			keys[string(r.InterfaceName)] = string(r.Interface)
		}
	}
	out := make([]Rule, 0, len(rows))
	for _, r := range rows {
		if k, ok := keys[string(r.Interface)]; ok && r.InterfaceName == "" {
			r.Interface = flexString(k)
		}
		seq, _ := strconv.Atoi(string(r.Sequence))
		out = append(out, Rule{UUID: string(r.UUID), Enabled: r.Enabled.bool(), Sequence: seq, Action: string(r.Action),
			Quick: r.Quick.bool(), Interface: string(r.Interface), Direction: string(r.Direction), IPProtocol: string(r.IPProtocol),
			Protocol: string(r.Protocol), Source: string(r.SourceNet), SourceNot: r.SourceNot.bool(), SourcePort: string(r.SourcePort),
			Destination: string(r.DestinationNet), DestinationNot: r.DestinationNot.bool(), DestinationPort: string(r.DestinationPort),
			Log: r.Log.bool(), Description: string(r.Description), Legacy: r.Legacy.bool() || r.UUID == ""})
	}
	return out, nil
}

func ruleBody(r Rule) map[string]any {
	return map[string]any{"rule": map[string]any{
		"enabled": boolString(r.Enabled), "sequence": strconv.Itoa(r.Sequence), "action": r.Action, "quick": boolString(r.Quick),
		"interface": r.Interface, "direction": r.Direction, "ipprotocol": r.IPProtocol, "protocol": r.Protocol,
		"source_net": r.Source, "source_not": boolString(r.SourceNot), "source_port": r.SourcePort,
		"destination_net": r.Destination, "destination_not": boolString(r.DestinationNot), "destination_port": r.DestinationPort,
		"log": boolString(r.Log), "description": r.Description,
	}}
}

// AddRule creates a rule and returns its UUID. It takes effect after ApplyRules.
func (c *Client) AddRule(ctx context.Context, r Rule) (string, error) {
	var res struct {
		UUID string `json:"uuid"`
	}
	if err := c.Post(ctx, "/firewall/filter/add_rule", ruleBody(r), &res); err != nil {
		return "", err
	}
	return res.UUID, nil
}

// SetRule changes a rule. It takes effect after ApplyRules.
func (c *Client) SetRule(ctx context.Context, r Rule) error {
	return c.Post(ctx, "/firewall/filter/set_rule/"+url.PathEscape(r.UUID), ruleBody(r), nil)
}

// DeleteRule deletes a rule. It takes effect after ApplyRules.
func (c *Client) DeleteRule(ctx context.Context, uuid string) error {
	var r struct {
		Result string `json:"result"`
	}
	if err := c.Post(ctx, "/firewall/filter/del_rule/"+url.PathEscape(uuid), nil, &r); err != nil {
		return err
	}
	if r.Result != "deleted" {
		return fmt.Errorf("%w: rule %s (%s)", ErrNotFound, uuid, r.Result)
	}
	return nil
}

// HasSavepoints reports whether the firewall has the savepoint calls (removed
// in OPNsense 26.7). A GET doesn't create a savepoint: OPNsense answers it
// with a failed status, or 404 if the call doesn't exist.
func (c *Client) HasSavepoints(ctx context.Context) (bool, error) {
	err := c.Get(ctx, "/firewall/filter/savepoint", nil)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil && !errors.As(err, new(*ValidationError)) {
		return false, err
	}
	return true, nil
}

// Savepoint creates a savepoint of the filter configuration and returns its
// revision. It returns ErrNoSavepoint if the firewall has no savepoints.
func (c *Client) Savepoint(ctx context.Context) (string, error) {
	var r struct {
		Status   string     `json:"status"`
		Revision flexString `json:"revision"`
	}
	err := c.Post(ctx, "/firewall/filter/savepoint", nil, &r)
	if errors.Is(err, ErrNotFound) {
		return "", ErrNoSavepoint
	}
	if err != nil {
		return "", err
	}
	if r.Revision == "" {
		return "", fmt.Errorf("opnsense: savepoint: no revision (status %q)", r.Status)
	}
	return string(r.Revision), nil
}

// ApplyRules applies the changed rules with a savepoint revision: OPNsense
// reverts them after 60 seconds unless CancelRollback is called.
func (c *Client) ApplyRules(ctx context.Context, revision string) error {
	return c.Post(ctx, "/firewall/filter/apply/"+url.PathEscape(revision), nil, nil)
}

// CancelRollback keeps the rules applied with the revision.
func (c *Client) CancelRollback(ctx context.Context, revision string) error {
	return c.Post(ctx, "/firewall/filter/cancel_rollback/"+url.PathEscape(revision), nil, nil)
}

// Revert restores the savepoint at once.
func (c *Client) Revert(ctx context.Context, revision string) error {
	return c.Post(ctx, "/firewall/filter/revert/"+url.PathEscape(revision), nil, nil)
}

func boolString(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
