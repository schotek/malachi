// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package smtp

import (
	"bytes"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Extra header fields are written as given; one that is not a plain field
// or would override the builder's fails the build, never folds into a
// second field.
func TestBuildExtraHeaders(t *testing.T) {
	raw := build(t, BuildInput{From: alice, To: []api.Address{bob}, Subject: "Away", Text: "x",
		Headers: []Header{{"Auto-Submitted", "auto-replied"}, {"X-Loop", "alice@example.org"}}})
	lines := rawHeaderLines(t, raw)
	for _, want := range []string{"Auto-Submitted: auto-replied", "X-Loop: alice@example.org"} {
		found := false
		for _, l := range lines {
			if l == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no %q in %q", want, lines)
		}
	}
	if n := headerCount(t, raw, "Auto-Submitted"); n != 1 {
		t.Errorf("%d Auto-Submitted fields", n)
	}

	for _, h := range []Header{
		{"Auto-Submitted", "auto-replied\r\nBcc: evil@example.net"},
		{"Auto-Submitted", "auto-replied\nX: y"},
		{"Auto-Submitted", "a\x00b"},
		{"Auto-Submitted", "naïve"},
		{"Auto-Submitted", ""},
		{"Auto-Submitted", "   "},
		{"Auto-Submitted", strings.Repeat("a", maxHeaderBytes+1)},
		{"Auto Submitted", "x"},
		{"Auto-Submitted:", "x"},
		{"X-Evil\r\nBcc", "x"},
		{"", "x"},
		{"-X", "x"},
		{"1X", "x"},
		{"Bcc", "evil@example.net"},
		{"bcc", "evil@example.net"},
		{"To", "evil@example.net"},
		{"From", "evil@example.net"},
		{"Subject", "x"},
		{"Message-ID", "<x@y>"},
		{"Content-Type", "text/html"},
		{"MIME-Version", "2.0"},
		{"Reply-To", "evil@example.net"},
		{strings.Repeat("X", 77), "x"},
	} {
		var buf bytes.Buffer
		err := BuildMessage(&buf, BuildInput{From: alice, To: []api.Address{bob}, Subject: "s", Text: "x", Headers: []Header{h}})
		if err == nil {
			t.Errorf("%q: %q was accepted:\n%s", h.Name, h.Value, buf.String())
		}
	}
	many := make([]Header, maxExtraHeaders+1)
	for i := range many {
		many[i] = Header{"X-A", "b"}
	}
	var buf bytes.Buffer
	if err := BuildMessage(&buf, BuildInput{From: alice, To: []api.Address{bob}, Text: "x", Headers: many}); err == nil {
		t.Error("too many extra fields accepted")
	}
}
