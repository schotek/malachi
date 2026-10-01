// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureReader(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join(testdata, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// The header-only parse gives what Parse gives for the curated headers,
// from the block alone.
func TestParseHeaderFieldsMatchesParse(t *testing.T) {
	for _, name := range []string{"bulk-newsletter-oneclick.eml", "bulk-list-golang.eml", "bulk-automated.eml", "bulk-broken-brackets.eml", "simple-text.eml"} {
		full, err := Parse(fixtureReader(t, name), DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		refs, h := ParseHeaderFields(fixtureReader(t, name), DefaultLimits())
		if len(h) != len(full.Headers) || len(refs) != len(full.References) {
			t.Errorf("%s: headers %v vs %v, references %v vs %v", name, h, full.Headers, refs, full.References)
		}
		for k, v := range full.Headers {
			if h[k] != v {
				t.Errorf("%s: %s = %q, want %q", name, k, h[k], v)
			}
		}
	}
}

func TestListPostIsCurated(t *testing.T) {
	_, h := ParseHeaderFields(fixtureReader(t, "bulk-list-golang.eml"), DefaultLimits())
	if h["List-Post"] != "<mailto:golang-nuts@googlegroups.com>" || h["List-Id"] == "" {
		t.Errorf("%v", h)
	}
}

// A List-Unsubscribe field keeps room for eight URIs of 2048 bytes; the
// other curated fields stay at MaxFieldBytes.
func TestListUnsubscribeCap(t *testing.T) {
	long := "<https://news.example.com/" + strings.Repeat("a", 3000) + ">, <mailto:unsub@news.example.com>"
	raw := "From: a@news.example.com\r\nList-Unsubscribe: " + long + "\r\nList-Id: " + strings.Repeat("x", 5000) + "\r\n\r\nbody"
	_, h := ParseHeaderFields(strings.NewReader(raw), DefaultLimits())
	if h["List-Unsubscribe"] != long {
		t.Errorf("List-Unsubscribe cut to %d bytes", len(h["List-Unsubscribe"]))
	}
	if len(h["List-Id"]) > DefaultLimits().MaxFieldBytes {
		t.Errorf("List-Id %d bytes", len(h["List-Id"]))
	}
}

func TestCountHeaderFields(t *testing.T) {
	got := CountHeaderFields(fixtureReader(t, "bulk-huge-unsubscribe.eml"), DefaultLimits(), "From", "List-Unsubscribe", "List-Unsubscribe-Post", "Reply-To")
	if got["From"] != 1 || got["List-Unsubscribe"] != 2 || got["List-Unsubscribe-Post"] != 1 || got["Reply-To"] != 0 {
		t.Errorf("%v", got)
	}
	if got := CountHeaderFields(strings.NewReader(""), DefaultLimits(), "From"); got["From"] != 0 {
		t.Errorf("empty: %v", got)
	}
}
