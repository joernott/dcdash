// SPDX-License-Identifier: GPL-3.0-or-later

package dns

import (
	"context"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/netbox"
)

// Write-back of A and AAAA records to NetBox (task 12.3.3,
// docs/adr/0006-netbox.md): the record's name is the DNS name of the address.

// SetNetBox connects the NetBox module; nil if not configured.
func (s *Service) SetNetBox(nb *netbox.Service) { s.netbox = nb }

// RecordResult is the response of creating or changing a record.
type RecordResult struct {
	Record
	// NetBox is the result of the write-back to NetBox; missing if the
	// record isn't an A or AAAA record or changes aren't written to NetBox.
	NetBox *netbox.Result `json:"netbox,omitempty"`
}

// DeleteResult is the response of deleting an A or AAAA record if the change
// was written to NetBox (otherwise the response is empty, 204).
type DeleteResult struct {
	NetBox *netbox.Result `json:"netbox"`
}

// addressRecord returns an A or AAAA record as NetBox sees it.
func addressRecord(rec Record) (netbox.DNSRecord, bool) {
	if rec.Type != "A" && rec.Type != "AAAA" {
		return netbox.DNSRecord{}, false
	}
	return netbox.DNSRecord{Name: rec.Name, Address: rec.Content}, true
}

// recordChanged writes a new or changed record to NetBox; prev is the record
// before a change.
func (s *Service) recordChanged(ctx context.Context, base audit.Entry, target string, rec Record, prev *Record) *netbox.Result {
	var before *netbox.DNSRecord
	if prev != nil {
		if b, ok := addressRecord(*prev); ok {
			before = &b
		}
	}
	now, ok := addressRecord(rec)
	switch {
	case ok:
		return s.netbox.SyncDNSName(ctx, base, target, now, before)
	case before != nil:
		return s.netbox.ClearDNSName(ctx, base, target, *before)
	}
	return nil
}

// recordDeleted clears the DNS name of a deleted A or AAAA record in NetBox.
func (s *Service) recordDeleted(ctx context.Context, base audit.Entry, target string, rec Record) *netbox.Result {
	if r, ok := addressRecord(rec); ok {
		return s.netbox.ClearDNSName(ctx, base, target, r)
	}
	return nil
}
