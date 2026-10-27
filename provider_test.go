package dreamhost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/libdns/libdns"
)

// fakeDreamHost is an in-memory implementation of the three DNS commands
// of the DreamHost API.
type fakeDreamHost struct {
	records []dnsRecord
}

func (f *fakeDreamHost) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.Form.Get("key") != "testkey" {
			writeJSON(w, map[string]any{"result": "error", "data": "invalid_api_key"})
			return
		}
		switch r.Form.Get("cmd") {
		case "dns-list_records":
			writeJSON(w, map[string]any{"result": "success", "data": f.records})
		case "dns-add_record":
			rec := dnsRecord{
				Record:   r.Form.Get("record"),
				Type:     r.Form.Get("type"),
				Value:    r.Form.Get("value"),
				Comment:  r.Form.Get("comment"),
				Editable: "1",
				Zone:     "example.com",
			}
			for _, existing := range f.records {
				if existing.Record == rec.Record && existing.Type == rec.Type && existing.Value == rec.Value {
					writeJSON(w, map[string]any{"result": "error", "data": "record_already_exists_remove_first"})
					return
				}
			}
			f.records = append(f.records, rec)
			writeJSON(w, map[string]any{"result": "success", "data": "record_added"})
		case "dns-remove_record":
			name, typ, value := r.Form.Get("record"), r.Form.Get("type"), r.Form.Get("value")
			for i, existing := range f.records {
				if existing.Record == name && existing.Type == typ && existing.Value == value {
					if existing.Editable == "0" {
						writeJSON(w, map[string]any{"result": "error", "data": "record_not_editable"})
						return
					}
					f.records = append(f.records[:i], f.records[i+1:]...)
					writeJSON(w, map[string]any{"result": "success", "data": "record_removed"})
					return
				}
			}
			writeJSON(w, map[string]any{"result": "error", "data": "no_such_record"})
		default:
			writeJSON(w, map[string]any{"result": "error", "data": "unknown_cmd"})
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func newTestProvider(t *testing.T, fake *fakeDreamHost) *Provider {
	t.Helper()
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	return &Provider{APIKey: "testkey", APIURL: server.URL}
}

func rrs(records []libdns.Record) []libdns.RR {
	out := make([]libdns.RR, len(records))
	for i, r := range records {
		out[i] = r.RR()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Data < out[j].Data
	})
	return out
}

func TestGetRecords(t *testing.T) {
	fake := &fakeDreamHost{records: []dnsRecord{
		{Zone: "example.com", Record: "example.com", Type: "A", Value: "203.0.113.1", Editable: "1"},
		{Zone: "example.com", Record: "www.example.com", Type: "CNAME", Value: "example.com", Editable: "1"},
		{Zone: "other.net", Record: "other.net", Type: "A", Value: "203.0.113.9", Editable: "1"},
	}}
	p := newTestProvider(t, fake)

	got, err := p.GetRecords(context.Background(), "example.com.")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2: %+v", len(got), rrs(got))
	}
	r := rrs(got)
	if r[0].Name != "@" || r[0].Type != "A" || r[0].Data != "203.0.113.1" {
		t.Errorf("unexpected apex record: %+v", r[0])
	}
	if r[1].Name != "www" || r[1].Type != "CNAME" {
		t.Errorf("unexpected www record: %+v", r[1])
	}
}

func TestAppendRecords(t *testing.T) {
	fake := &fakeDreamHost{}
	p := newTestProvider(t, fake)

	added, err := p.AppendRecords(context.Background(), "example.com.", []libdns.Record{
		libdns.TXT{Name: "_acme-challenge", Text: "token123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 {
		t.Fatalf("got %d added, want 1", len(added))
	}
	if len(fake.records) != 1 {
		t.Fatalf("server has %d records, want 1", len(fake.records))
	}
	rec := fake.records[0]
	if rec.Record != "_acme-challenge.example.com" || rec.Type != "TXT" || rec.Value != "token123" {
		t.Errorf("unexpected stored record: %+v", rec)
	}

	// Appending the same record again is idempotent.
	if _, err := p.AppendRecords(context.Background(), "example.com.", added); err != nil {
		t.Fatalf("re-append: %v", err)
	}
	if len(fake.records) != 1 {
		t.Fatalf("server has %d records after re-append, want 1", len(fake.records))
	}
}

func TestSetRecords(t *testing.T) {
	fake := &fakeDreamHost{records: []dnsRecord{
		{Zone: "example.com", Record: "home.example.com", Type: "A", Value: "203.0.113.1", Editable: "1"},
		{Zone: "example.com", Record: "home.example.com", Type: "A", Value: "203.0.113.2", Editable: "1"},
		{Zone: "example.com", Record: "keep.example.com", Type: "TXT", Value: "untouched", Editable: "1"},
	}}
	p := newTestProvider(t, fake)

	_, err := p.SetRecords(context.Background(), "example.com.", []libdns.Record{
		libdns.RR{Name: "home", Type: "A", Data: "203.0.113.2"},
		libdns.RR{Name: "home", Type: "A", Data: "203.0.113.3"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var homeValues []string
	for _, rec := range fake.records {
		if rec.Record == "home.example.com" {
			homeValues = append(homeValues, rec.Value)
		}
	}
	sort.Strings(homeValues)
	want := []string{"203.0.113.2", "203.0.113.3"}
	if len(homeValues) != 2 || homeValues[0] != want[0] || homeValues[1] != want[1] {
		t.Errorf("home A records = %v, want %v", homeValues, want)
	}
	if len(fake.records) != 3 {
		t.Errorf("server has %d records, want 3 (unrelated record must be untouched)", len(fake.records))
	}
}

func TestDeleteRecords(t *testing.T) {
	fake := &fakeDreamHost{records: []dnsRecord{
		{Zone: "example.com", Record: "_acme-challenge.example.com", Type: "TXT", Value: "token1", Editable: "1"},
		{Zone: "example.com", Record: "_acme-challenge.example.com", Type: "TXT", Value: "token2", Editable: "1"},
		{Zone: "example.com", Record: "example.com", Type: "A", Value: "203.0.113.1", Editable: "1"},
	}}
	p := newTestProvider(t, fake)

	// Exact delete.
	deleted, err := p.DeleteRecords(context.Background(), "example.com.", []libdns.Record{
		libdns.TXT{Name: "_acme-challenge", Text: "token1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || len(fake.records) != 2 {
		t.Fatalf("after exact delete: %d deleted, %d remain", len(deleted), len(fake.records))
	}

	// Wildcard delete (no value): removes all remaining TXT of that name.
	deleted, err = p.DeleteRecords(context.Background(), "example.com.", []libdns.Record{
		libdns.RR{Name: "_acme-challenge", Type: "TXT"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || len(fake.records) != 1 {
		t.Fatalf("after wildcard delete: %d deleted, %d remain", len(deleted), len(fake.records))
	}
	if fake.records[0].Type != "A" {
		t.Errorf("remaining record should be the A record, got %+v", fake.records[0])
	}
}

func TestUnmarshalCaddyfile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   string
		wantKey string
		wantErr bool
	}{
		{"inline", `dreamhost mykey`, "mykey", false},
		{"block", "dreamhost {\n\tapi_key mykey\n}", "mykey", false},
		{"both", "dreamhost mykey {\n\tapi_key other\n}", "", true},
		{"missing", `dreamhost`, "", true},
		{"unknown directive", "dreamhost {\n\tbogus x\n}", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := new(Provider)
			err := p.UnmarshalCaddyfile(caddyfile.NewTestDispenser(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.APIKey != tc.wantKey {
				t.Errorf("APIKey = %q, want %q", p.APIKey, tc.wantKey)
			}
		})
	}
}
