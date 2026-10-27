package dreamhost

// Live integration test against the real DreamHost API.
//
// It is skipped unless DREAMHOST_API_KEY is set. It creates one TXT record
// named _libdns-live-test in a zone on the account (the first zone found,
// or DREAMHOST_TEST_ZONE if set), verifies it is listed, then deletes it.
// DreamHost's control-panel/API view updates immediately, so this does not
// wait for nameserver propagation.
//
//	DREAMHOST_API_KEY=... DREAMHOST_TEST_ZONE=example.com go test -v -run TestLive

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/libdns/libdns"
)

const liveTestName = "_libdns-live-test"

func TestLive(t *testing.T) {
	apiKey := os.Getenv("DREAMHOST_API_KEY")
	if apiKey == "" {
		t.Skip("DREAMHOST_API_KEY not set; skipping live API test")
	}
	p := &Provider{APIKey: apiKey}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Pick a zone.
	zone := os.Getenv("DREAMHOST_TEST_ZONE")
	if zone == "" {
		all, err := p.listRecords(ctx)
		if err != nil {
			t.Fatalf("listing records: %v", err)
		}
		for _, rec := range all {
			if rec.Editable != "0" && rec.Zone != "" {
				zone = rec.Zone
				break
			}
		}
		if zone == "" {
			t.Fatal("no zone with editable records found; set DREAMHOST_TEST_ZONE")
		}
	}
	zone += "."
	t.Logf("using zone %s", zone)

	value := fmt.Sprintf("libdns-test-%d-%d", time.Now().Unix(), rand.Intn(1e6))
	testRecord := libdns.TXT{Name: liveTestName, Text: value}

	// Best-effort cleanup even if an assertion below fails.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		p.DeleteRecords(cleanupCtx, zone, []libdns.Record{
			libdns.RR{Name: liveTestName, Type: "TXT"}, // no value: delete all
		})
	})

	// Create.
	added, err := p.AppendRecords(ctx, zone, []libdns.Record{testRecord})
	if err != nil {
		t.Fatalf("AppendRecords: %v", err)
	}
	if len(added) != 1 {
		t.Fatalf("AppendRecords returned %d records, want 1", len(added))
	}
	t.Logf("created TXT %s.%s = %q", liveTestName, zone, value)

	// Read back.
	records, err := p.GetRecords(ctx, zone)
	if err != nil {
		t.Fatalf("GetRecords: %v", err)
	}
	found := false
	for _, rec := range records {
		rr := rec.RR()
		if rr.Name == liveTestName && rr.Type == "TXT" && rr.Data == value {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("created record not found among %d records in zone", len(records))
	}
	t.Log("record visible via GetRecords")

	// Delete.
	deleted, err := p.DeleteRecords(ctx, zone, []libdns.Record{testRecord})
	if err != nil {
		t.Fatalf("DeleteRecords: %v", err)
	}
	if len(deleted) != 1 {
		t.Fatalf("DeleteRecords returned %d records, want 1", len(deleted))
	}

	// Verify gone.
	records, err = p.GetRecords(ctx, zone)
	if err != nil {
		t.Fatalf("GetRecords after delete: %v", err)
	}
	for _, rec := range records {
		rr := rec.RR()
		if rr.Name == liveTestName && rr.Type == "TXT" && rr.Data == value {
			t.Fatal("record still present after DeleteRecords")
		}
	}
	t.Log("record deleted")
}
