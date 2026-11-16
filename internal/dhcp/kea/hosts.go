// SPDX-License-Identifier: GPL-3.0-or-later

package kea

import (
	"context"
	"errors"
)

// Reservation sources.
const (
	SourceConfig   = "config"
	SourceDatabase = "database"
)

// IdentifierTypes are the identifier types of a reservation, in Kea's order of precedence.
var IdentifierTypes = []string{"hw-address", "duid", "client-id", "circuit-id", "flex-id"}

// Reservation is a host reservation (host_cmds).
type Reservation struct {
	// SubnetID 0 is a global reservation.
	SubnetID uint32 `json:"subnet-id"`
	// Exactly one identifier is set.
	HWAddress string `json:"hw-address,omitempty"`
	DUID      string `json:"duid,omitempty"`
	ClientID  string `json:"client-id,omitempty"`
	CircuitID string `json:"circuit-id,omitempty"`
	FlexID    string `json:"flex-id,omitempty"`
	// IPAddress (v4), IPAddresses and Prefixes (v6).
	IPAddress     string   `json:"ip-address,omitempty"`
	IPAddresses   []string `json:"ip-addresses,omitempty"`
	Prefixes      []string `json:"prefixes,omitempty"`
	Hostname      string   `json:"hostname,omitempty"`
	ClientClasses []string `json:"client-classes,omitempty"`
	Options       []Option `json:"option-data,omitempty"`
	// Boot fields (v4). Kea reports defaults ("0.0.0.0", "").
	NextServer     string `json:"next-server,omitempty"`
	ServerHostname string `json:"server-hostname,omitempty"`
	BootFileName   string `json:"boot-file-name,omitempty"`
	// Source is SourceConfig or SourceDatabase; set by dcdash, not sent to Kea.
	Source string `json:"-"`
}

// Identifier returns the identifier type and value.
func (r Reservation) Identifier() (typ, value string) {
	for _, p := range []struct{ t, v string }{
		{"hw-address", r.HWAddress}, {"duid", r.DUID}, {"client-id", r.ClientID},
		{"circuit-id", r.CircuitID}, {"flex-id", r.FlexID},
	} {
		if p.v != "" {
			return p.t, p.v
		}
	}
	return "", ""
}

// reservationPageSize is the page size when listing reservations.
const reservationPageSize = 500

// Reservations lists the reservations of all subnets (subnetID < 0) or of
// one subnet, from the configuration file and the host database. Each
// reservation's Source tells where it is stored.
func (c *Client) Reservations(ctx context.Context, subnetID int64) ([]Reservation, error) {
	var out []Reservation
	from, source := uint64(0), 0
	for {
		args := map[string]any{"limit": reservationPageSize, "source-index": source, "from": from}
		if subnetID >= 0 {
			args["subnet-id"] = subnetID
		}
		var page struct {
			Count int           `json:"count"`
			Hosts []Reservation `json:"hosts"`
			Next  struct {
				From        uint64 `json:"from"`
				SourceIndex int    `json:"source-index"`
			} `json:"next"`
		}
		r, err := c.Do(ctx, "reservation-get-page", args, &page)
		if err != nil {
			return nil, err
		}
		if r.Empty() || len(page.Hosts) == 0 {
			return out, nil
		}
		// The page comes from one source: next.source-index. Source 0 is the
		// configuration file, the others are host databases.
		src := SourceDatabase
		if page.Next.SourceIndex == 0 {
			src = SourceConfig
		}
		for _, h := range page.Hosts {
			h.Source = src
			out = append(out, h)
		}
		if page.Next.SourceIndex == source && page.Next.From == from {
			return out, errors.New("kea: reservation-get-page did not advance")
		}
		from, source = page.Next.From, page.Next.SourceIndex
	}
}

// Reservation returns a reservation by subnet and identifier from the given
// source (SourceConfig or SourceDatabase). found is false if there is none.
func (c *Client) Reservation(ctx context.Context, subnetID uint32, idType, id, source string) (res Reservation, found bool, err error) {
	target := "database"
	if source == SourceConfig {
		target = "memory"
	}
	r, err := c.Do(ctx, "reservation-get", map[string]any{
		"subnet-id": subnetID, "identifier-type": idType, "identifier": id, "operation-target": target,
	}, &res)
	if err != nil || r.Empty() {
		return Reservation{}, false, err
	}
	res.Source = source
	return res, true, nil
}

// AddReservation adds a reservation to the host database.
func (c *Client) AddReservation(ctx context.Context, res Reservation) error {
	_, err := c.Do(ctx, "reservation-add", map[string]any{"reservation": res, "operation-target": "database"}, nil)
	return err
}

// UpdateReservation replaces a reservation in the host database, found by
// its subnet and identifier.
func (c *Client) UpdateReservation(ctx context.Context, res Reservation) error {
	_, err := c.Do(ctx, "reservation-update", map[string]any{"reservation": res, "operation-target": "database"}, nil)
	return err
}

// DeleteReservation deletes a reservation from the host database. A missing
// reservation is not an error (deleted reports false).
func (c *Client) DeleteReservation(ctx context.Context, subnetID uint32, idType, id string) (deleted bool, err error) {
	r, err := c.Do(ctx, "reservation-del", map[string]any{
		"subnet-id": subnetID, "identifier-type": idType, "identifier": id, "operation-target": "database",
	}, nil)
	return err == nil && !r.Empty(), err
}
