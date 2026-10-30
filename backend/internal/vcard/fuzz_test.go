// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package vcard

import (
	"bytes"
	"os"
	"reflect"
	"testing"
)

func seeds(f *testing.F) [][]byte {
	var out [][]byte
	for _, file := range corpusFiles(f) {
		raw, err := os.ReadFile(file)
		if err == nil && len(raw) <= 64<<10 {
			out = append(out, raw)
		}
	}
	return append(out, []byte("BEGIN:VCARD\nitem1.EMAIL;TYPE=\"a,b\";X=\"c:d\":\\n\r\n \xc5\r\n \xbd\nitem1.X-ABLabel:_$!<_$!<Home>!$_>!$_\nN;QUOTED-PRINTABLE:=\n=C5;=\nEND:VCARD"))
}

// FuzzParse: Parse never panics; a parsed card is clean, and a patch
// without a change returns the input unchanged.
func FuzzParse(f *testing.F) {
	for _, raw := range seeds(f) {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		c, err := Parse(raw)
		if err != nil {
			return
		}
		if !reflect.DeepEqual(Clean(c), c) {
			t.Fatalf("Clean changed a parsed card")
		}
		_ = SortKey(c)
		_ = DisplayNameOf(c)
		out, err := Patch(raw, c, Clean(c))
		if err != nil {
			t.Fatalf("Patch: %v", err)
		}
		if !bytes.Equal(out, raw) {
			t.Fatalf("Patch without a change changed the card")
		}
	})
}

// FuzzPatchPreserves: parse, change one field to a fuzzed value, patch,
// parse again: the field has the new value (cleaned) and nothing else
// changed.
func FuzzPatchPreserves(f *testing.F) {
	for i, raw := range seeds(f) {
		for field := range uint8(12) {
			f.Add(raw, field+uint8(i), "Nový ; hodnota, \\ x")
		}
	}
	f.Fuzz(func(t *testing.T, raw []byte, field uint8, value string) {
		before, err := Parse(raw)
		if err != nil {
			return
		}
		after := copyCard(before)
		switch field % 12 {
		case 0:
			after.DisplayName = value
			if Clean(after).DisplayName == "" {
				return // clearing FN writes a fallback name
			}
		case 1:
			after.Name.Family = value
			if before.Version >= "4" && Clean(after).Name == (Name{}) {
				return // an empty N goes in 4.0
			}
		case 2:
			after.Nickname = value
		case 3:
			after.Org = value
		case 4:
			after.Title = value
		case 5:
			after.Note = value
		case 6:
			after.Emails = append(after.Emails, Email{Label: value, Address: value + "@x.example"})
		case 7:
			if len(after.Emails) == 0 {
				return
			}
			after.Emails[0].Address = value
		case 8:
			if len(after.Phones) == 0 {
				return
			}
			after.Phones = after.Phones[1:]
		case 9:
			if len(after.Addresses) == 0 {
				after.Addresses = []Address{{Street: value}}
			} else {
				after.Addresses[0].City = value
			}
		case 10:
			after.URLs = append(after.URLs, URL{Label: value, URL: value})
		case 11:
			after.Birthday = Birthday{Year: len(value) * 37 % 3000, Month: len(value)%12 + 1, Day: len(value)%28 + 1}
		}
		want := Clean(after)
		if len(want.Emails) > MaxItems-1 || len(want.URLs) > MaxItems-1 {
			return // the cap of the parser hides further items
		}
		out, err := Patch(raw, before, after)
		if err != nil {
			t.Fatalf("Patch: %v", err)
		}
		back, err := Parse(out)
		if err != nil {
			if len(out) > MaxCardBytes {
				return
			}
			t.Fatalf("Parse of the patched card: %v", err)
		}
		if !reflect.DeepEqual(back, want) {
			t.Fatalf("field %d:\n got %+v\nwant %+v", field%12, back, want)
		}
	})
}
