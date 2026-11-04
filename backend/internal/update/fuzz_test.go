// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package update

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// FuzzEnvelope feeds arbitrary bytes to Verify; it must never panic.
func FuzzEnvelope(f *testing.F) {
	seed(f)
	keys := testKeys(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = Verify(b, keys)
	})
}

// FuzzPayload feeds arbitrary bytes to Parse; an accepted manifest keeps
// the rules Select relies on.
func FuzzPayload(f *testing.F) {
	seedPayloads(f)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, b []byte) {
		m, _, err := Parse(b, now, time.Time{})
		if err != nil {
			return
		}
		for i := 1; i < len(m.Releases); i++ {
			if m.Releases[i].Version.Compare(m.Releases[i-1].Version) >= 0 {
				t.Fatal("releases out of order")
			}
		}
		for _, r := range m.Releases {
			if _, ok := r.Notes["en"]; !ok {
				t.Fatal("notes without en")
			}
			for ch, p := range r.Packages {
				if !KnownChannel(ch) || !AllowedURL(p.URL, downloadHosts) {
					t.Fatal("bad package")
				}
			}
		}
	})
}

func seed(f *testing.F) {
	entries, _ := os.ReadDir(fixtures)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			b, err := os.ReadFile(filepath.Join(fixtures, e.Name()))
			if err == nil {
				f.Add(b)
			}
		}
	}
	f.Add([]byte(`{"schema":1,"issuedAt":"2026-10-28T12:00:00Z","releases":[]}`))
}

// seedPayloads adds the decoded payloads of the fixtures.
func seedPayloads(f *testing.F) {
	entries, _ := os.ReadDir(fixtures)
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(fixtures, e.Name()))
		if err != nil {
			continue
		}
		var env struct{ Payload string }
		if json.Unmarshal(b, &env) == nil && env.Payload != "" {
			if p, err := base64.StdEncoding.DecodeString(env.Payload); err == nil {
				f.Add(p)
			}
		}
	}
	f.Add([]byte(`{"schema":1,"issuedAt":"2026-10-28T12:00:00Z","releases":[]}`))
}
