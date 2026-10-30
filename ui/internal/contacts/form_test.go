// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contacts

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestEditFormUnchanged(t *testing.T) {
	c := card()
	f := EditForm(c)
	if f.Dirty() || f.IsNew {
		t.Fatalf("dirty %v", f.Changed())
	}
	out := f.Card()
	if out.DisplayName != "Jana Nováková" || out.ID != "p_1" || out.Revision != "r1" {
		t.Fatalf("card %+v", out)
	}
	// Whitespace alone is no change.
	f.Given = " Jana "
	if f.Dirty() {
		t.Fatal("whitespace is a change")
	}
}

func TestEditFormChangesAndDisplayName(t *testing.T) {
	f := EditForm(card())
	f.Family = "Malá"
	f.Emails = append(f.Emails, LabelledValue{Label: "other", Value: "  "})
	if !slices.Equal(f.Changed(), []FieldKey{KeyFamily}) {
		t.Fatalf("changed %v", f.Changed())
	}
	c := f.Card()
	if c.DisplayName != "Jana Malá" || len(c.Emails) != 2 || len(c.Addresses) != 1 {
		t.Fatalf("card %+v", c)
	}
}

func TestDisplayNameOnlyCardKeepsItsName(t *testing.T) {
	c := api.ContactCard{ID: "p", DisplayName: "Pavel Novák", Phones: []api.ContactPhone{{Number: "1"}}}
	f := EditForm(c)
	if f.Given != "Pavel" || f.Family != "Novák" || f.Dirty() {
		t.Fatal("split")
	}
	f.Phones[0].Value = "2"
	out := f.Card()
	if out.Name != (api.ContactName{}) || out.DisplayName != "Pavel Novák" {
		t.Fatalf("an unchanged name changed: %+v", out.Name)
	}
}

func TestNewFormCard(t *testing.T) {
	f := NewForm("b_m1")
	if f.Dirty() {
		t.Fatal("new is dirty")
	}
	f.Org = "Acme"
	c := f.Card()
	if c.BookID != "b_m1" || c.DisplayName != "Acme" || c.Emails == nil || c.URLs == nil {
		t.Fatalf("%+v", c)
	}
	f.Org = ""
	f.Emails = []LabelledValue{{Value: "a@b.cz"}}
	if f.Card().DisplayName != "a@b.cz" {
		t.Fatal("address as name")
	}
}

func TestRows(t *testing.T) {
	f := NewForm("b")
	if i := f.AddRow(FieldEmail); i != 0 || f.Emails[0].Label != "work" {
		t.Fatal("first")
	}
	f.AddRow(FieldEmail)
	f.AddRow(FieldEmail)
	f.AddRow(FieldEmail)
	if got := []api.ContactLabel{f.Emails[1].Label, f.Emails[2].Label, f.Emails[3].Label}; !slices.Equal(got, []api.ContactLabel{"home", "other", "other"}) {
		t.Fatalf("labels %v", got)
	}
	if f.AddRow(FieldPhone); f.Phones[0].Label != "mobile" {
		t.Fatal("phone")
	}
	f.AddRow(FieldAddress)
	f.AddRow(FieldURL)
	f.RemoveRow(FieldEmail, 0)
	f.RemoveRow(FieldEmail, 9)
	if f.Count(FieldEmail) != 3 || f.Emails[0].Label != "home" || f.Count(FieldAddress) != 1 || f.Count(FieldURL) != 1 {
		t.Fatal("remove")
	}
	for f.CanAdd(FieldURL) {
		f.AddRow(FieldURL)
	}
	if f.AddRow(FieldURL) != -1 || f.Count(FieldURL) != api.MaxContactURLs {
		t.Fatal("limit")
	}
	f.RemoveRow(FieldPhone, 0)
	f.RemoveRow(FieldAddress, 0)
	f.RemoveRow(FieldURL, 0)
}

func TestValidate(t *testing.T) {
	f := NewForm("b")
	if p := f.Validate(tr); len(p) != 1 || p[0].Text != "Enter a name, a company or an email address." {
		t.Fatalf("%+v", p)
	}
	f.Given = "Jana"
	f.Emails = []LabelledValue{{Value: "jana@example.org"}, {Value: "Jana <j@x.cz>"}, {Value: "nope"}, {Value: ""}, {Value: "a@localhost"}}
	f.URLs = []LabelledValue{{Value: "example.org"}, {Value: "javascript:alert(1)"}, {Value: "https://ok.cz"}}
	f.Birthday = &api.ContactBirthday{Month: 2, Day: 30}
	f.Note = strings.Repeat("x", api.MaxContactNoteBytes+1)
	f.Org = strings.Repeat("o", api.MaxContactFieldBytes+1)
	var got []string
	for _, p := range f.Validate(tr) {
		got = append(got, p.Text)
		_ = p.Index
	}
	want := []string{"This text is too long.", "Jana <j@x.cz> is not a valid email address.", "nope is not a valid email address.",
		"a@localhost is not a valid email address.", "A website must be an address starting with http:// or https://.",
		"Enter a valid date.", "This text is too long."}
	if !slices.Equal(got, want) {
		t.Fatalf("problems\n%q", got)
	}
	f = NewForm("b")
	f.Emails = []LabelledValue{{Value: "jana@example.org"}}
	if len(f.Validate(tr)) != 0 {
		t.Fatal("an address alone is enough")
	}
}

func TestRebaseKeepsTheUsersChanges(t *testing.T) {
	f := EditForm(card())
	f.Title = "Ředitelka"
	f.Phones = []LabelledValue{{Label: "mobile", Value: "+420 777"}}
	server := card()
	server.Revision = "r2"
	server.Org = "Novák a syn"
	server.Title = "Nákupčí"
	server.Phones = append(server.Phones, api.ContactPhone{Number: "3"})
	server.Note = "Nová poznámka"
	f.Rebase(server)
	if f.Org != "Novák a syn" || f.Title != "Ředitelka" || f.Note != "Nová poznámka" || len(f.Phones) != 1 || f.Phones[0].Value != "+420 777" {
		t.Fatalf("rebased %+v", f)
	}
	if !slices.Equal(f.Changed(), []FieldKey{KeyTitle, KeyPhones}) || f.Card().Revision != "r2" {
		t.Fatalf("changed %v", f.Changed())
	}
	f.Saved("", "r3")
	if f.Dirty() || f.Original.Revision != "r3" || f.Original.Title != "Ředitelka" {
		t.Fatal("saved")
	}
}

func TestSplitName(t *testing.T) {
	for in, want := range map[string][2]string{"Petr Svoboda": {"Petr", "Svoboda"}, "Eva": {"Eva", ""}, "": {"", ""},
		"Jan  Amos Komenský": {"Jan Amos", "Komenský"}} {
		if g, f := SplitName(in); [2]string{g, f} != want {
			t.Errorf("%q → %q %q", in, g, f)
		}
	}
}

func TestAddFromChip(t *testing.T) {
	books, accs := sampleBooks(), sampleAccounts()
	a := NewAddFromChip(api.Address{Name: "Petr \u202eSvoboda", Address: "petr.svoboda@example.com"}, "acc_dav", books, accs, tr)
	if a.Name != "Petr Svoboda" || !a.SelectName || a.Warning == "" || a.Book != "b_dav" || !a.CanAdd() {
		t.Fatalf("%+v", a)
	}
	var texts []string
	for _, b := range a.Books {
		texts = append(texts, b.Text)
	}
	if !slices.Equal(texts, []string{"Personal (Nextcloud)", "Contacts (jana@firma.cz)", "Archive 2019 (jana@firma.cz)"}) {
		t.Fatalf("books %q", texts)
	}
	f := a.Form()
	if !f.IsNew || f.Book != "b_dav" || f.Given != "Petr" || f.Family != "Svoboda" || !reflect.DeepEqual(f.Emails, []LabelledValue{{Value: "petr.svoboda@example.com"}}) {
		t.Fatalf("form %+v", f)
	}
	// No name: nothing to warn about; an IMAP message goes to the first
	// visible writable book.
	a = NewAddFromChip(api.Address{Name: "x@y.cz", Address: "x@y.cz"}, "acc_imap", books, accs, tr)
	if a.Name != "" || a.SelectName || a.Warning != "" || a.Book != "b_dav" {
		t.Fatalf("%+v", a)
	}
	if DefaultBook(books, "b_m1", "") != "b_m1" || DefaultBook(books, "b_m2", "acc_m365") != "b_m1" || DefaultBook(books, "", "acc_m365") != "b_m1" {
		t.Fatal("defaults")
	}
	only := []api.AddressBook{{ID: "h", Kind: api.AddressBookKindGraph, Visible: false}}
	if DefaultBook(only, "", "") != "h" || DefaultBook(nil, "", "") != "" {
		t.Fatal("hidden fallback")
	}
	if ChipItemFor(true, nil) != ChipShow || ChipItemFor(false, books) != ChipAdd || ChipItemFor(false, books[2:3]) != ChipNone {
		t.Fatal("chip")
	}
	if ChipItemText(ChipShow, tr) != "Show Contact" || ChipItemText(ChipAdd, tr) != "Add to Contacts…" || ChipItemText(ChipNone, tr) != "" {
		t.Fatal("chip texts")
	}
	a.Address = "bad"
	if a.CanAdd() {
		t.Fatal("bad address")
	}
}
