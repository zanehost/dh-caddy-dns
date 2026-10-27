package dreamhost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// defaultAPIURL is the DreamHost API endpoint. See
// https://help.dreamhost.com/hc/en-us/articles/217560167-API-overview
const defaultAPIURL = "https://api.dreamhost.com/"

// dnsRecord is a single record as returned by dns-list_records.
// DreamHost returns every field as a string.
type dnsRecord struct {
	AccountID string `json:"account_id"`
	Zone      string `json:"zone"`
	Record    string `json:"record"`
	Type      string `json:"type"`
	Value     string `json:"value"`
	Comment   string `json:"comment"`
	Editable  string `json:"editable"`
}

// apiError is an error response from the DreamHost API
// ({"result":"error","data":"<code>","reason":"..."}).
type apiError struct {
	Code   string
	Reason string
}

func (e apiError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("dreamhost: %s (%s)", e.Code, e.Reason)
	}
	return "dreamhost: " + e.Code
}

// call performs one DreamHost API command and returns the raw "data" payload.
func (p *Provider) call(ctx context.Context, cmd string, params url.Values) (json.RawMessage, error) {
	if p.APIKey == "" {
		return nil, fmt.Errorf("dreamhost: missing API key")
	}

	base := p.APIURL
	if base == "" {
		base = defaultAPIURL
	}

	q := url.Values{}
	for k, vs := range params {
		q[k] = vs
	}
	q.Set("key", p.APIKey)
	q.Set("cmd", cmd)
	q.Set("format", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base, strings.NewReader(q.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dreamhost: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var envelope struct {
		Result string          `json:"result"`
		Data   json.RawMessage `json:"data"`
		Reason string          `json:"reason"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("dreamhost: decoding response for %s: %w", cmd, err)
	}
	if envelope.Result != "success" {
		code := strings.Trim(string(envelope.Data), `"`)
		return nil, apiError{Code: code, Reason: envelope.Reason}
	}
	return envelope.Data, nil
}

// listRecords returns all DNS records on the account.
// The DreamHost API has no way to scope the listing to a zone.
func (p *Provider) listRecords(ctx context.Context) ([]dnsRecord, error) {
	data, err := p.call(ctx, "dns-list_records", nil)
	if err != nil {
		return nil, err
	}
	var records []dnsRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("dreamhost: decoding record list: %w", err)
	}
	return records, nil
}

// addRecord creates a record. name is a fully-qualified name without the
// trailing dot, as DreamHost expects.
func (p *Provider) addRecord(ctx context.Context, name, recType, value, comment string) error {
	params := url.Values{}
	params.Set("record", name)
	params.Set("type", recType)
	params.Set("value", value)
	if comment != "" {
		params.Set("comment", comment)
	}
	_, err := p.call(ctx, "dns-add_record", params)
	return err
}

// removeRecord deletes a record. All three fields must exactly match an
// existing editable record.
func (p *Provider) removeRecord(ctx context.Context, name, recType, value string) error {
	params := url.Values{}
	params.Set("record", name)
	params.Set("type", recType)
	params.Set("value", value)
	_, err := p.call(ctx, "dns-remove_record", params)
	return err
}

// isAlreadyExists reports whether err is DreamHost telling us the record
// is already present exactly as requested.
func isAlreadyExists(err error) bool {
	var apiErr apiError
	if !asAPIError(err, &apiErr) {
		return false
	}
	return strings.Contains(apiErr.Code, "already_exist") ||
		strings.Contains(apiErr.Code, "record_already_exists")
}

// isNoSuchRecord reports whether err is DreamHost telling us the record to
// remove does not exist.
func isNoSuchRecord(err error) bool {
	var apiErr apiError
	if !asAPIError(err, &apiErr) {
		return false
	}
	return strings.Contains(apiErr.Code, "no_such_record") ||
		strings.Contains(apiErr.Code, "no_record")
}

func asAPIError(err error, target *apiError) bool {
	e, ok := err.(apiError)
	if ok {
		*target = e
	}
	return ok
}
