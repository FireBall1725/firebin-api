// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package handlers

import (
	"errors"
	"fmt"
	neturl "net/url"
	"strings"
	"testing"
)

// Every case here came out of one real "Mirror missing" run against a live
// inventory, so the names are the parts that actually failed.
func TestNormalizeDatasheetURL(t *testing.T) {
	cases := []struct {
		name, in, want, wantErr string
	}{
		{
			// CL10A105KO8NNNC, CL10A106MQ8NNNC, CL10B104KB8NNWC all stored their
			// datasheet link this way and were rejected as "unsupported".
			name: "protocol-relative gets https",
			in:   "//product.samsungsem.com/mlcc/CL10A105KO8NNNC.pdf",
			want: "https://product.samsungsem.com/mlcc/CL10A105KO8NNNC.pdf",
		},
		{name: "https is untouched", in: "https://example.com/a.pdf", want: "https://example.com/a.pdf"},
		{name: "http is allowed", in: "http://example.com/a.pdf", want: "http://example.com/a.pdf"},
		{name: "surrounding space is trimmed", in: "  https://example.com/a.pdf  ", want: "https://example.com/a.pdf"},
		{name: "query and fragment survive", in: "//h/a.pdf?v=2#p1", want: "https://h/a.pdf?v=2#p1"},

		{name: "empty", in: "   ", wantErr: "no datasheet URL"},
		{name: "no scheme", in: "example.com/a.pdf", wantErr: "no scheme"},
		// Old manufacturer sites still publish ftp links; the message should say
		// which scheme rather than a bare "unsupported".
		{name: "ftp names the scheme", in: "ftp://ftp.example.com/a.pdf", wantErr: `cannot download over "ftp"`},
		{name: "no host", in: "https:///a.pdf", wantErr: "no host"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := normalizeDatasheetURL(c.in)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("want an error containing %q, got %q", c.wantErr, got)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("error %q does not mention %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestIsRetriableFetch(t *testing.T) {
	retriable := map[string]error{
		// 2171790001 (Molex) failed with exactly this and was never retried.
		"http2 stream reset": errors.New("stream error: stream ID 1; INTERNAL_ERROR; received from peer"),
		"connection reset":   errors.New("read tcp 10.0.0.1:1->2.2.2.2:443: read: connection reset by peer"),
		"unexpected EOF":     errors.New("unexpected EOF"),
		"dns":                errors.New("dial tcp: lookup nowhere.example: no such host"),
		"timeout":            errors.New("i/o timeout"),
		"url error":          &neturl.Error{Op: "Get", URL: "https://x", Err: errors.New("boom")},
	}
	for name, err := range retriable {
		t.Run("retry/"+name, func(t *testing.T) {
			if !isRetriableFetch(err) {
				t.Errorf("%v should be retriable", err)
			}
		})
	}

	// A settled answer must not be asked twice. Retrying a 403 only asks the CDN
	// to refuse again, and doubles the wait on a large backfill.
	permanent := map[string]error{
		"forbidden":  fmt.Errorf("the vendor refused an automated download (403); the datasheet link still works in a browser"),
		"dead link":  fmt.Errorf("the datasheet URL is dead (404)"),
		"not a pdf":  fmt.Errorf("the URL did not return a PDF (likely a dead link serving an error page)"),
		"too large":  fmt.Errorf("datasheet is larger than the configured limit"),
		"bad scheme": fmt.Errorf(`cannot download over "ftp"; only http and https are supported`),
		"nil":        nil,
	}
	for name, err := range permanent {
		t.Run("no-retry/"+name, func(t *testing.T) {
			if isRetriableFetch(err) {
				t.Errorf("%v should NOT be retriable", err)
			}
		})
	}
}

// A 403 is not a broken link and the message has to say so, because the part
// keeps a datasheet URL that opens perfectly well in a browser. Telling the user
// "returned 403" invites them to go looking for a fault that is not theirs.
func TestForbiddenMessageExplainsItself(t *testing.T) {
	err := fmt.Errorf("the vendor refused an automated download (%d); the datasheet link still works in a browser, so open it and upload the PDF if you want a copy", 403)
	for _, want := range []string{"refused an automated download", "still works in a browser", "upload"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should mention %q: %s", want, err)
		}
	}
}
