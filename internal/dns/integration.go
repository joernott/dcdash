// SPDX-License-Identifier: GPL-3.0-or-later

package dns

import (
	"context"
	"time"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/rbac"
)

// Records requested by other modules, e.g. by the creation of a guest in the
// Proxmox module (task 11.14). PlanRecord checks the permission and the
// record when the user submits it; Create adds the record later.

// PlannedRecord is a checked record that is not created yet.
type PlannedRecord struct {
	s    *Service
	p    Provider
	zone string
	rec  Record
	// Target is the audit target, e.g. "cf/example.org".
	Target string
}

// PlanRecord checks that subj may add records to the zone and that the record
// is valid. A missing permission is an *rbac.DeniedError.
func (s *Service) PlanRecord(subj *rbac.Subject, provider, zone string, req RecordRequest) (*PlannedRecord, error) {
	p, ok := s.providers[provider]
	if !ok {
		return nil, invalid("unknown DNS provider %q", provider)
	}
	zone = NormalizeName(zone)
	if !ValidZoneName(zone) {
		return nil, invalid("invalid zone name %q", zone)
	}
	if perm := rbac.P(Module, p.Name(), zone, rbac.ActionWrite); !subj.Can(perm) {
		return nil, &rbac.DeniedError{Permission: perm}
	}
	rec, err := Normalize(req.record(), zone)
	if err != nil {
		return nil, err
	}
	return &PlannedRecord{s: s, p: p, zone: zone, rec: rec, Target: recordTarget(p, zone)}, nil
}

// Describe names the record for the progress of a job.
func (r *PlannedRecord) Describe() string {
	return r.rec.Name + " " + r.rec.Type + " " + r.rec.Content
}

// Create adds the record and records it in the audit log as
// dns.record.create, with the actor of entry.
func (r *PlannedRecord) Create(ctx context.Context, entry audit.Entry) error {
	set, err := r.p.Records(ctx, r.zone)
	rec := r.rec
	if err == nil {
		rec, err = r.p.CreateRecord(ctx, r.zone, r.rec, set.Version)
	}
	entry.Action, entry.Target, entry.Result = "dns.record.create", r.Target, audit.ResultSuccess
	entry.Details = recordDetails(rec, rec.ID)
	if err != nil {
		entry.Details["error"] = err.Error()
		entry.Result = audit.ResultFailure
	}
	_ = r.s.audit.Record(ctx, entry)
	if err == nil {
		base := entry
		base.Time, base.Action, base.Details = time.Time{}, "", nil
		r.s.recordChanged(ctx, base, r.Target, rec, nil, nil)
	}
	return err
}
