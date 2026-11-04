// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package vcard

import (
	"strings"
	"testing"
	"time"
)

// TestPathologicalShapesStayLinear: inputs of the largest size in shapes
// that would be quadratic in a naive parser finish quickly.
func TestPathologicalShapesStayLinear(t *testing.T) {
	inputs := map[string]string{
		"folds":   "BEGIN:VCARD\r\nNOTE:" + strings.Repeat("\r\n x", 300000) + "\r\nEND:VCARD",
		"qp":      "BEGIN:VCARD\r\nNOTE;QUOTED-PRINTABLE:" + strings.Repeat("=\r\n", 300000) + "\r\nEND:VCARD",
		"nocolon": "BEGIN:VCARD\r\n" + strings.Repeat("x=\r\n", 250000),
		"longhdr": "BEGIN:VCARD\r\nNOTE" + strings.Repeat(";QUOTED-PRINTABLE", 50000) + ":a=\r\n" + strings.Repeat("=\r\n", 100000),
		"punct":   "BEGIN:VCARD\r\nFN:" + strings.Repeat("!", 1000) + "\r\nEND:VCARD",
		"escapes": "BEGIN:VCARD\r\nNOTE:" + strings.Repeat("\\", 1000000) + "\r\nEND:VCARD",
		"emails":  "BEGIN:VCARD\r\n" + strings.Repeat("item1.EMAIL:a@b\r\nitem1.X-ABLabel:x\r\n", 240) + "END:VCARD",
		"params":  "BEGIN:VCARD\r\nEMAIL" + strings.Repeat(";TYPE=a,b,c", 80000) + ":a@b\r\nEND:VCARD",
		"blanks":  "BEGIN:VCARD\r\n" + strings.Repeat("\r\n", 500000) + "END:VCARD",
	}
	for name, s := range inputs {
		raw := []byte(s)
		if len(raw) > MaxCardBytes {
			raw = raw[:MaxCardBytes]
		}
		start := time.Now()
		c, err := Parse(raw)
		if err == nil {
			after := copyCard(c)
			after.Note = "x"
			after.Emails = nil
			after.DisplayName = "y"
			_, err = Patch(raw, c, after)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s: %v (%v)", name, d, err)
		}
	}
}
