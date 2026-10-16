// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ics

import (
	"reflect"
	"testing"
)

// FuzzParse: Parse never panics or fails below MaxBytes, the accessors
// never panic, whatever the parser kept encodes, and a clean object reads
// back equal.
func FuzzParse(f *testing.F) {
	for _, data := range corpus(f) {
		if len(data) <= 64<<10 {
			f.Add(data)
		}
	}
	f.Add([]byte("BEGIN:VEVENT\nX;A=\"b\",c:\\n\r\n \xc5\r\n \xbd\nEND:VCALENDAR"))
	f.Fuzz(func(t *testing.T, data []byte) {
		o, err := Parse(data)
		if len(data) > MaxBytes {
			return
		}
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		exerciseAccessors(o)
		enc, err := Encode(o)
		if err != nil {
			t.Fatalf("Encode of a parsed object: %v", err)
		}
		back, err := Parse(enc)
		if err != nil {
			t.Fatalf("Parse(Encode): %v", err)
		}
		if !reflect.DeepEqual(back.Calendars, o.Calendars) {
			t.Fatalf("round trip changed the calendars")
		}
		if !o.HasProblems() && back.HasProblems() {
			t.Fatalf("clean object reads back with problems: %v", back.Problems)
		}
	})
}
