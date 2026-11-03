// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package signature

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzSignedSlice: Locate never panics, and whatever it cuts out is a
// slice of the input (never rebuilt bytes) within the limits.
func FuzzSignedSlice(f *testing.F) {
	names, _ := filepath.Glob(filepath.Join(mimeDir, "*.eml"))
	for _, n := range names {
		b := filepath.Base(n)
		if strings.HasPrefix(b, "smime-") || strings.HasPrefix(b, "pgp-") || strings.HasPrefix(b, "skeleton-") {
			raw, err := os.ReadFile(n)
			if err == nil && len(raw) < 64<<10 {
				f.Add(raw)
			}
		}
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		loc, err := Locate(raw)
		if err != nil {
			if !errors.Is(err, ErrNone) || loc != nil {
				t.Fatalf("%v %v", loc, err)
			}
			return
		}
		if loc.Signed != nil && !bytes.Contains(raw, loc.Signed) {
			t.Error("signed bytes are not a slice of the message")
		}
		if len(loc.Signature) > MaxMessageBytes {
			t.Error("signature over the cap")
		}
		c := Canonical(loc.Signed)
		if bytes.Count(c, []byte("\n")) != bytes.Count(c, []byte("\r\n")) {
			t.Error("bare LF after canonicalisation")
		}
		_ = ProtectedHeaders(loc.Signed, raw)
	})
}

func TestCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"a\nb\r\nc\n": "a\r\nb\r\nc\r\n",
		"\n":          "\r\n",
		"a\r\n":       "a\r\n",
		"":            "",
	} {
		if got := string(Canonical([]byte(in))); got != want {
			t.Errorf("%q → %q", in, got)
		}
	}
}

func TestSplitMultipart(t *testing.T) {
	body := "pre\r\n--b\r\nA\r\n--b \t\r\nB\r\n\r\n--b--\r\nepilogue\r\n--b\r\nX\r\n"
	parts, closed, ok := splitMultipart([]byte(body), "b")
	if !ok || !closed || len(parts) != 2 || string(parts[0]) != "A" || string(parts[1]) != "B\r\n" {
		t.Errorf("%q %v %v", parts, closed, ok)
	}
	// "--bx" is not the delimiter of "b".
	parts, _, _ = splitMultipart([]byte("--b\r\nA\r\n--bx\r\nB\r\n--b--"), "b")
	if len(parts) != 1 || string(parts[0]) != "A\r\n--bx\r\nB" {
		t.Errorf("%q", parts)
	}
	if _, closed, _ := splitMultipart([]byte("--b\r\nA\r\n"), "b"); closed {
		t.Error("closed without the closing delimiter")
	}
}
