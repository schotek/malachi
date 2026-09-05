// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package safename

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFilename(t *testing.T) {
	cases := map[string]string{
		"report.pdf":           "report.pdf",
		"../../x":              "x",
		`..\..\x`:              "x",
		"/etc/passwd":          "passwd",
		"a\x00b\n.txt":         "ab.txt",
		".hidden":              "hidden",
		"...":                  Fallback,
		"":                     Fallback,
		"   ":                  Fallback,
		"  spaced.txt  ":       "spaced.txt",
		"Jörg's Übersicht.ods": "Jörg's Übersicht.ods",
		"\xff\xfebad.txt":      "bad.txt",
		// Bidi controls would make the displayed extension lie.
		"photo‮gnp.exe":     "photognp.exe",
		"‫x‬⁦y⁩.txt": "xy.txt",
		"‎name‏؜.pdf":     "name.pdf",
	}
	for in, want := range cases {
		if got := Filename(in); got != want {
			t.Errorf("Filename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilenameTruncates(t *testing.T) {
	long := strings.Repeat("ž", 300) + ".txt" // 2 bytes per rune
	got := Filename(long)
	if len(got) > MaxBytes || !utf8.ValidString(got) || !strings.HasSuffix(got, ".txt") {
		t.Errorf("truncated = %d bytes, valid=%v, %q", len(got), utf8.ValidString(got), got[len(got)-8:])
	}
	noExt := strings.Repeat("a", 400)
	if got := Filename(noExt); len(got) != MaxBytes {
		t.Errorf("no-ext truncate len = %d", len(got))
	}
}
