// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package vcard

import (
	"bytes"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func fixNow(t *testing.T) {
	t.Helper()
	old := now
	now = func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = old })
}

// copyCard returns a deep copy.
func copyCard(c *Card) *Card {
	n := *c
	n.Emails = slices.Clone(c.Emails)
	n.Phones = slices.Clone(c.Phones)
	n.Addresses = slices.Clone(c.Addresses)
	n.URLs = slices.Clone(c.URLs)
	return &n
}

// lineDiff returns the unfolded lines (CRLF or LF) of a that are not in b
// and those of b that are not in a, as multisets.
func lineDiff(a, b []byte) (removed, added []string) {
	split := func(x []byte) []string {
		s := strings.ReplaceAll(string(x), "\r\n", "\n")
		return strings.Split(strings.ReplaceAll(s, "\n ", ""), "\n")
	}
	la, lb := split(a), split(b)
	count := map[string]int{}
	for _, l := range lb {
		count[l]++
	}
	for _, l := range la {
		if count[l] > 0 {
			count[l]--
		} else {
			removed = append(removed, l)
		}
	}
	count = map[string]int{}
	for _, l := range la {
		count[l]++
	}
	for _, l := range lb {
		if count[l] > 0 {
			count[l]--
		} else {
			added = append(added, l)
		}
	}
	return removed, added
}

func TestPatchNoChangeIsIdentical(t *testing.T) {
	for _, f := range corpusFiles(t) {
		raw, _ := os.ReadFile(f)
		c, err := Parse(raw)
		if err != nil {
			continue
		}
		out, err := Patch(raw, c, copyCard(c))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if !bytes.Equal(out, raw) {
			t.Errorf("%s: unchanged patch changed the bytes", f)
		}
	}
}

func patchApple(t *testing.T, edit func(c *Card)) (raw, out []byte, back *Card) {
	t.Helper()
	fixNow(t)
	raw = readCorpus(t, "apple-icloud-3.0.vcf")
	before := mustParse(t, raw)
	after := copyCard(before)
	edit(after)
	out, err := Patch(raw, before, after)
	if err != nil {
		t.Fatal(err)
	}
	return raw, out, mustParse(t, out)
}

func TestPatchChangesOnlyTheEditedLine(t *testing.T) {
	raw, out, back := patchApple(t, func(c *Card) { c.Emails[1].Address = "jana.n@acme.example" })
	removed, added := lineDiff(raw, out)
	eq(t, "removed", removed, []string{"EMAIL;type=INTERNET;type=WORK:jana.novakova@acme.example", "REV:2026-09-01T10:00:00Z"})
	eq(t, "added", added, []string{"EMAIL;type=INTERNET;type=WORK:jana.n@acme.example", "REV:2026-10-30T12:00:00Z"})
	eq(t, "email", back.Emails[1], Email{"work", "jana.n@acme.example"})
	if !bytes.Contains(out, []byte("PHOTO;ENCODING=b;TYPE=JPEG:")) || !bytes.Contains(out, []byte("X-SOCIALPROFILE")) {
		t.Error("unknown lines lost")
	}
}

func TestPatchRemoveEmailTakesItsGroup(t *testing.T) {
	raw, out, back := patchApple(t, func(c *Card) { c.Emails = slices.Delete(c.Emails, 2, 3) })
	removed, _ := lineDiff(raw, out)
	eq(t, "removed", removed, []string{"item2.EMAIL;type=INTERNET:jn@club.example", "item2.X-ABLabel:Klub", "REV:2026-09-01T10:00:00Z"})
	eq(t, "emails", len(back.Emails), 2)
}

func TestPatchRemoveAddressTakesXABADR(t *testing.T) {
	raw, out, back := patchApple(t, func(c *Card) { c.Addresses = nil })
	removed, _ := lineDiff(raw, out)
	if !slices.Contains(removed, "item4.X-ABADR:cz") {
		t.Errorf("removed %q", removed)
	}
	eq(t, "addresses", len(back.Addresses), 0)
}

func TestPatchMiddleRemovalKeepsOthersLines(t *testing.T) {
	raw, out, back := patchApple(t, func(c *Card) { c.Phones = slices.Delete(c.Phones, 1, 2) })
	removed, added := lineDiff(raw, out)
	eq(t, "removed", removed, []string{"TEL;type=WORK;type=VOICE:+420 222 333 444", "REV:2026-09-01T10:00:00Z"})
	eq(t, "added", added, []string{"REV:2026-10-30T12:00:00Z"})
	eq(t, "phones", back.Phones, []Phone{{"mobile", "+420 777 123 456"}, {"Main", "+420 602 000 111"}})
}

func TestPatchAddItems(t *testing.T) {
	_, out, back := patchApple(t, func(c *Card) {
		c.Emails = append(c.Emails, Email{"home", "new@home.example"}, Email{"Chata", "chata@x.example"})
		c.URLs = append(c.URLs, URL{"work", "https://w.example"})
	})
	eq(t, "emails", back.Emails[3:], []Email{{"home", "new@home.example"}, {"Chata", "chata@x.example"}})
	eq(t, "urls", back.URLs[1], URL{"work", "https://w.example"})
	if !bytes.Contains(out, []byte("EMAIL;TYPE=INTERNET;TYPE=HOME:new@home.example\r\n")) ||
		!bytes.Contains(out, []byte("item6.EMAIL;TYPE=INTERNET:chata@x.example\r\nitem6.X-ABLabel:Chata\r\n")) {
		t.Errorf("new lines:\n%s", out)
	}
	// New emails follow the last EMAIL and its group.
	i := bytes.Index(out, []byte("item2.X-ABLabel:Klub"))
	j := bytes.Index(out, []byte("new@home.example"))
	if i < 0 || j < i {
		t.Error("new email not after the last one")
	}
}

func TestPatchLabels(t *testing.T) {
	raw, out, back := patchApple(t, func(c *Card) {
		c.Emails[0].Label = "work"   // X-ABLabel Other → TYPE
		c.Emails[1].Label = "Office" // TYPE → new group with X-ABLabel
		c.Emails[2].Label = "Golf"   // X-ABLabel rewritten
	})
	eq(t, "emails", back.Emails, []Email{{"work", "jana@example.cz"}, {"Office", "jana.novakova@acme.example"}, {"Golf", "jn@club.example"}})
	removed, added := lineDiff(raw, out)
	slices.Sort(removed)
	slices.Sort(added)
	eq(t, "removed", removed, []string{"EMAIL;type=INTERNET;type=WORK:jana.novakova@acme.example", "REV:2026-09-01T10:00:00Z",
		"item1.EMAIL;type=INTERNET;type=pref:jana@example.cz", "item1.X-ABLabel:_$!<Other>!$_", "item2.X-ABLabel:Klub"})
	eq(t, "added", added, []string{"REV:2026-10-30T12:00:00Z", "item1.EMAIL;type=INTERNET;type=pref;TYPE=WORK:jana@example.cz",
		"item2.X-ABLabel:Golf", "item6.EMAIL;type=INTERNET:jana.novakova@acme.example", "item6.X-ABLabel:Office"})
}

func TestPatchSingleFields(t *testing.T) {
	raw, out, back := patchApple(t, func(c *Card) {
		c.DisplayName = "Jana N."
		c.Org = "Beta a.s."
		c.Title = ""
		c.Nickname = "J"
		c.Birthday = Birthday{0, 4, 1}
		c.Note = "jen jeden; řádek, nový\nřádek"
		c.Name.Middle = ""
	})
	eq(t, "fn", back.DisplayName, "Jana N.")
	eq(t, "org", back.Org, "Beta a.s.")
	eq(t, "title", back.Title, "")
	eq(t, "birthday", back.Birthday, Birthday{0, 4, 1})
	eq(t, "note", back.Note, "jen jeden; řádek, nový\nřádek")
	eq(t, "name", back.Name, Name{Given: "Jana", Family: "Nováková", Prefix: "Ing.", Suffix: "Ph.D."})
	_, added := lineDiff(raw, out)
	for _, want := range []string{"ORG:Beta a.s.;Vývoj", "BDAY;X-APPLE-OMIT-YEAR=1604:1604-04-01", `NOTE:jen jeden\; řádek\, nový\nřádek`} {
		if !slices.Contains(added, want) {
			t.Errorf("missing %q in %q", want, added)
		}
	}
	if bytes.Contains(out, []byte("TITLE")) {
		t.Error("TITLE not removed")
	}
	// A known year drops Apple's parameter.
	_, out, back = patchApple(t, func(c *Card) { c.Birthday = Birthday{1990, 3, 15} })
	if !bytes.Contains(out, []byte("\r\nBDAY:1990-03-15\r\n")) || back.Birthday != (Birthday{1990, 3, 15}) {
		t.Errorf("bday: %+v", back.Birthday)
	}
}

func TestPatchClearFN(t *testing.T) {
	_, _, back := patchApple(t, func(c *Card) { c.DisplayName = "" })
	eq(t, "fn", back.DisplayName, "Ing. Jana Marie Nováková Ph.D.")
}

func TestPatchVersion4(t *testing.T) {
	fixNow(t)
	raw := readCorpus(t, "nextcloud-4.0.vcf")
	before := mustParse(t, raw)
	after := copyCard(before)
	after.Phones[0].Number = "+420 601 000 000"
	after.Phones = append(after.Phones, Phone{"mobile", "+1 2"})
	after.Birthday = Birthday{1970, 5, 6}
	after.Name = Name{}
	after.Addresses[0].City = "Brno"
	out, err := Patch(raw, before, after)
	if err != nil {
		t.Fatal(err)
	}
	removed, added := lineDiff(raw, out)
	slices.Sort(removed)
	slices.Sort(added)
	eq(t, "removed", removed, []string{`ADR;TYPE=home;LABEL="Na Příkopě 1\nPraha":;;Na Příkopě 1;Praha;;110 00;CZ`,
		"BDAY:--0229", "N:Dvořák;Petr;;;", "REV:20260901T100000Z", `TEL;VALUE=uri;TYPE="cell,voice":tel:+420-601-222-333`})
	eq(t, "added", added, []string{`ADR;TYPE=home;LABEL="Na Příkopě 1\nPraha":;;Na Příkopě 1;Brno;;110 00;CZ`,
		"BDAY:19700506", "REV:20261030T120000Z", "TEL;TYPE=cell:+1 2", `TEL;VALUE=uri;TYPE="cell,voice":tel:+420 601 000 000`})
	back := mustParse(t, out)
	eq(t, "phones", back.Phones, after.Phones)
}

func TestPatchVersion21(t *testing.T) {
	fixNow(t)
	raw := readCorpus(t, "outlook-export-2.1.vcf")
	before := mustParse(t, raw)
	after := copyCard(before)
	after.Title = "Ředitel"
	after.Emails = append(after.Emails, Email{"work", "t@w.example"})
	after.Phones[0].Label = "home"
	out, err := Patch(raw, before, after)
	if err != nil {
		t.Fatal(err)
	}
	_, added := lineDiff(raw, out)
	slices.Sort(added)
	eq(t, "added", added, []string{"EMAIL;INTERNET;WORK:t@w.example", "REV:2026-10-30T12:00:00Z",
		"TEL;VOICE;HOME:+420 123 456 789", "TITLE;CHARSET=UTF-8;ENCODING=QUOTED-PRINTABLE:=C5=98editel"})
	back := mustParse(t, out)
	eq(t, "back", back, Clean(after))
}

func TestPatchKeepsServerChangesOfOtherFields(t *testing.T) {
	fixNow(t)
	raw := readCorpus(t, "google-3.0.vcf")
	before := mustParse(t, raw)
	server := bytes.Replace(raw, []byte("TITLE:Engineer"), []byte("TITLE:Manager"), 1)
	server = bytes.Replace(server, []byte("TEL;TYPE=WORK:+1 555 765 4321"), []byte("TEL;TYPE=WORK:+1 555 000 0000"), 1)
	after := copyCard(before)
	after.Note = "edited"
	after.Phones[1].Number = "+1 999"                // changed here and on the server
	after.Emails = slices.Delete(after.Emails, 0, 1) // removed here
	out, err := Patch(server, before, after)
	if err != nil {
		t.Fatal(err)
	}
	back := mustParse(t, out)
	eq(t, "title", back.Title, "Manager")
	eq(t, "note", back.Note, "edited")
	eq(t, "emails", back.Emails, []Email{{"work", "john.smith@corp.example"}})
	// The server's own change of the phone stays, the user's value is added.
	eq(t, "phones", back.Phones, []Phone{{"mobile", "(555) 123-4567"}, {"work", "+1 555 000 0000"}, {"Pager", "+1 555 000 1111"}, {"work", "+1 999"}})
}

func TestPatchMultipleCardsAndNoEnd(t *testing.T) {
	raw := readCorpus(t, "pathological/multiple-cards.vcf")
	before := mustParse(t, raw)
	after := copyCard(before)
	after.Emails[0].Address = "changed@example.org"
	out, err := Patch(raw, before, after)
	if err != nil {
		t.Fatal(err)
	}
	removed, added := lineDiff(raw, out)
	eq(t, "removed", removed, []string{"EMAIL:first@example.org"})
	eq(t, "added", added, []string{"EMAIL:changed@example.org"})

	raw = readCorpus(t, "pathological/no-end.vcf")
	before = mustParse(t, raw)
	after = copyCard(before)
	after.Title = "T"
	out, err = Patch(raw, before, after)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "title", mustParse(t, out).Title, "T")
	eq(t, "emails", mustParse(t, out).Emails, before.Emails)
}

func TestPatchErrors(t *testing.T) {
	if _, err := Patch([]byte("x"), &Card{}, &Card{}); err == nil {
		t.Error("no card")
	}
	if _, err := Patch(card("FN:x"), nil, &Card{}); err == nil {
		t.Error("nil card")
	}
}

func TestNew(t *testing.T) {
	fixNow(t)
	c := &Card{
		Name:        Name{Given: "Žofie", Family: "Šťastná"},
		Org:         "Org; with, specials",
		Emails:      []Email{{"work", "z@w.example"}, {"Chalupa", "z@ch.example"}, {"", "  "}},
		Phones:      []Phone{{"mobile", "+420 1"}, {"fax", "+420 2"}},
		Addresses:   []Address{{Label: "home", Street: "Ulice 1\nPatro 2", City: "Praha", Country: "CZ"}},
		URLs:        []URL{{"", `https://x.example/a\b`}},
		Birthday:    Birthday{0, 12, 24},
		Note:        strings.Repeat("Dlouhá poznámka se znaky ěščřžýáíé. ", 10),
		DisplayName: "",
	}
	for _, ver := range []string{"3.0", "4.0"} {
		raw := New(c, ver, "uid-1")
		for _, l := range strings.Split(string(raw), "\r\n") {
			if len(l) > foldOctets {
				t.Errorf("%s: line of %d octets", ver, len(l))
			}
			if !utf8.ValidString(l) {
				t.Errorf("%s: fold splits UTF-8: %q", ver, l)
			}
		}
		back := mustParse(t, raw)
		want := Clean(c)
		want.Version = ver
		want.UID = "uid-1"
		want.DisplayName = "Žofie Šťastná"
		if !reflect.DeepEqual(back, want) {
			t.Errorf("%s: round trip\n got %+v\nwant %+v\n%s", ver, back, want, raw)
		}
		// Patching a new card with no change keeps it.
		out, err := Patch(raw, back, copyCard(back))
		if err != nil || !bytes.Equal(out, raw) {
			t.Errorf("%s: patch of a new card: %v", ver, err)
		}
	}
	if !bytes.Contains(New(c, "4.0", "u"), []byte("\r\nBDAY:--1224\r\n")) || !bytes.Contains(New(c, "3.0", "u"), []byte("\r\nBDAY:--12-24\r\n")) {
		t.Error("birthday without a year")
	}
}

func TestFold(t *testing.T) {
	s := "NOTE:" + strings.Repeat("ž", 100)
	out := fold(s, "\r\n")
	got, _ := splitLinesString(out)
	if got != s {
		t.Errorf("unfold(fold) = %q", got)
	}
}

func splitLinesString(s string) (string, int) {
	ls := splitLines([]byte(s))
	return string(ls[0].buf), len(ls)
}
