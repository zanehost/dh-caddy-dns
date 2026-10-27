// Package dreamhost implements a DNS record management client compatible
// with the libdns interfaces for DreamHost, and the Caddy DNS provider
// module that wraps it.
//
// DreamHost API notes that shape this implementation:
//   - The API is command-based (dns-list_records, dns-add_record,
//     dns-remove_record). There is no update command: changing a record
//     means removing it and adding a new one.
//   - Records cannot be listed per zone; dns-list_records returns every
//     record on the account and each row names its owning zone.
//   - TTLs are not configurable. TTL values on input records are ignored,
//     and records read back report a TTL of 0 (meaning "unknown" in libdns).
package dreamhost

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/libdns/libdns"
)

// Provider implements the libdns interfaces for DreamHost,
// and is also the Caddy module (see caddy.go).
type Provider struct {
	// APIKey is a DreamHost API key with access to the
	// dns-list_records, dns-add_record, and dns-remove_record
	// functions. Create one at https://panel.dreamhost.com/?tree=home.api
	APIKey string `json:"api_key,omitempty"`

	// APIURL optionally overrides the DreamHost API endpoint
	// (default https://api.dreamhost.com/). Mainly useful for testing.
	APIURL string `json:"api_url,omitempty"`

	// The DreamHost API applies account-wide rate limits and has no
	// transactional semantics, so mutations are serialized.
	mu sync.Mutex
}

const recordComment = "managed by Caddy via libdns"

// trimZone normalizes a libdns zone (which has a trailing dot) to the
// DreamHost form (no trailing dot).
func trimZone(zone string) string {
	return strings.TrimSuffix(zone, ".")
}

// inZone reports whether a DreamHost record row belongs to zone
// (DreamHost form, no trailing dot).
func inZone(rec dnsRecord, zone string) bool {
	return rec.Record == zone || strings.HasSuffix(rec.Record, "."+zone)
}

// toLibdns converts a DreamHost record row to a libdns record.
func toLibdns(rec dnsRecord, zone string) libdns.Record {
	rr := libdns.RR{
		Name: libdns.RelativeName(rec.Record, trimZone(zone)),
		Type: rec.Type,
		Data: rec.Value,
		// DreamHost does not expose TTLs; 0 means unknown.
	}
	if parsed, err := rr.Parse(); err == nil {
		return parsed
	}
	return rr
}

// fqdn returns the fully-qualified record name in DreamHost form
// (no trailing dot) for a libdns record name relative to zone.
func fqdn(name, zone string) string {
	abs := libdns.AbsoluteName(name, zone)
	return strings.TrimSuffix(abs, ".")
}

// GetRecords lists all the records in the zone.
func (p *Provider) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	all, err := p.listRecords(ctx)
	if err != nil {
		return nil, err
	}
	z := trimZone(zone)
	var records []libdns.Record
	for _, rec := range all {
		if inZone(rec, z) {
			records = append(records, toLibdns(rec, zone))
		}
	}
	return records, nil
}

// AppendRecords adds records to the zone. It returns the records that were added.
func (p *Provider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var added []libdns.Record
	for _, rec := range records {
		rr := rec.RR()
		name := fqdn(rr.Name, zone)
		err := p.addRecord(ctx, name, rr.Type, rr.Data, recordComment)
		if err != nil && !isAlreadyExists(err) {
			return added, fmt.Errorf("adding %s %s: %w", rr.Type, name, err)
		}
		added = append(added, rec)
	}
	return added, nil
}

// SetRecords sets the records in the zone, either by updating existing
// records or creating new ones. For each unique (name, type) pair in the
// input, any existing editable records of that name and type are replaced
// with exactly the given records. DreamHost has no update operation, so
// this is implemented as remove-then-add.
func (p *Provider) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	all, err := p.listRecords(ctx)
	if err != nil {
		return nil, err
	}
	z := trimZone(zone)

	// Group desired values by (fqdn, type).
	type key struct{ name, typ string }
	desired := make(map[key][]string)
	for _, rec := range records {
		rr := rec.RR()
		k := key{fqdn(rr.Name, zone), rr.Type}
		desired[k] = append(desired[k], rr.Data)
	}

	// Remove existing editable records of the same name and type whose
	// value is not among the desired ones.
	existing := make(map[key]map[string]bool) // values already present
	for _, rec := range all {
		if !inZone(rec, z) {
			continue
		}
		k := key{rec.Record, rec.Type}
		want, ok := desired[k]
		if !ok {
			continue
		}
		if contains(want, rec.Value) {
			if existing[k] == nil {
				existing[k] = make(map[string]bool)
			}
			existing[k][rec.Value] = true
			continue
		}
		if rec.Editable == "0" {
			return nil, fmt.Errorf("record %s %s %q exists but is not editable", rec.Type, rec.Record, rec.Value)
		}
		if err := p.removeRecord(ctx, rec.Record, rec.Type, rec.Value); err != nil && !isNoSuchRecord(err) {
			return nil, fmt.Errorf("removing %s %s: %w", rec.Type, rec.Record, err)
		}
	}

	// Add desired values that are not already present.
	var set []libdns.Record
	for _, rec := range records {
		rr := rec.RR()
		k := key{fqdn(rr.Name, zone), rr.Type}
		if existing[k][rr.Data] {
			set = append(set, rec)
			continue
		}
		err := p.addRecord(ctx, k.name, rr.Type, rr.Data, recordComment)
		if err != nil && !isAlreadyExists(err) {
			return set, fmt.Errorf("adding %s %s: %w", rr.Type, k.name, err)
		}
		set = append(set, rec)
	}
	return set, nil
}

// DeleteRecords deletes the records from the zone. Records with an empty
// Data field match all records of that name and type. It returns the
// records that were deleted.
func (p *Provider) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var deleted []libdns.Record
	var all []dnsRecord // fetched lazily, only if a wildcard delete needs it

	for _, rec := range records {
		rr := rec.RR()
		name := fqdn(rr.Name, zone)

		if rr.Data != "" {
			err := p.removeRecord(ctx, name, rr.Type, rr.Data)
			if err != nil && !isNoSuchRecord(err) {
				return deleted, fmt.Errorf("removing %s %s: %w", rr.Type, name, err)
			}
			if err == nil {
				deleted = append(deleted, rec)
			}
			continue
		}

		if all == nil {
			var err error
			all, err = p.listRecords(ctx)
			if err != nil {
				return deleted, err
			}
		}
		for _, existing := range all {
			if existing.Record != name || existing.Type != rr.Type || existing.Editable == "0" {
				continue
			}
			err := p.removeRecord(ctx, existing.Record, existing.Type, existing.Value)
			if err != nil && !isNoSuchRecord(err) {
				return deleted, fmt.Errorf("removing %s %s: %w", rr.Type, name, err)
			}
			if err == nil {
				deleted = append(deleted, toLibdns(existing, zone))
			}
		}
	}
	return deleted, nil
}

func contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

// Interface guards
var (
	_ libdns.RecordGetter   = (*Provider)(nil)
	_ libdns.RecordAppender = (*Provider)(nil)
	_ libdns.RecordSetter   = (*Provider)(nil)
	_ libdns.RecordDeleter  = (*Provider)(nil)
)
