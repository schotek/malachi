// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contactsctl

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
)

func janaCard() api.ContactCard {
	return api.ContactCard{ID: "p_1", BookID: "b_m1", Revision: "r1",
		Name: api.ContactName{Given: "Jana", Family: "Nováková"}, DisplayName: "Jana Nováková", Org: "Stavby",
		Emails: []api.ContactEmail{{Label: "work", Address: "jana@firma.cz"}},
		Phones: []api.ContactPhone{{Label: "mobile", Number: "+420 601"}}}
}

// contactSetup is a contact controller over the standard daemon's window
// (for the books), with contact.get answering card.
func contactSetup(t *testing.T, card api.ContactCard) (*ContactController, *daemon, *loop, *[]contacts.Question) {
	t.Helper()
	w, d, l := startWindow(t)
	cur := card
	d.on(api.MethodContactGet, func(raw json.RawMessage) (any, error) {
		var p api.ContactGetParams
		_ = json.Unmarshal(raw, &p)
		if p.ContactID != cur.ID {
			return nil, &api.Error{Code: api.CodeContactNotFound, Message: "x"}
		}
		return api.ContactGetResult{Contact: cur}, nil
	})
	c := NewContactController(options(d, l), w.Book)
	var qs []contacts.Question
	c.OnQuestion = func(q contacts.Question, answer func(bool)) {
		qs = append(qs, q)
		answer(true)
	}
	c.Open(card.ID)
	if !c.Loading() {
		t.Fatal("not loading")
	}
	l.run()
	return c, d, l, &qs
}

func TestContactDetail(t *testing.T) {
	c, _, _, _ := contactSetup(t, janaCard())
	d, ok := c.Detail()
	if !ok || d.Title != "Jana Nováková" || !c.CanEdit() || !c.CanDelete() {
		t.Fatalf("detail %+v", d)
	}
	if s := c.SourceLine(); s != "Address book Contacts · Microsoft 365 (jana@firma.cz)" {
		t.Fatal(s)
	}
	if !strings.HasPrefix(c.CopyText(), "Jana Nováková\nStavby\nwork email: jana@firma.cz") {
		t.Fatalf("%q", c.CopyText())
	}
}

func TestContactOpenGone(t *testing.T) {
	c, _, l, _ := contactSetup(t, janaCard())
	c.Open("p_none")
	l.run()
	if c.Card() != nil || c.Error() != "The contact no longer exists." {
		t.Fatalf("err %q", c.Error())
	}
	c.Open("")
	if c.Loading() || c.ID() != "" {
		t.Fatal("cleared")
	}
}

func TestContactEditUnchangedSendsNothing(t *testing.T) {
	c, d, _, _ := contactSetup(t, janaCard())
	c.BeginEdit()
	if !c.Editing() || c.Dirty() {
		t.Fatal("editing")
	}
	c.Save()
	if c.Editing() || d.count(api.MethodContactUpdate) != 0 {
		t.Fatal("unchanged sent")
	}
}

func TestContactUpdate(t *testing.T) {
	c, d, l, _ := contactSetup(t, janaCard())
	d.answer(api.MethodContactUpdate, api.ContactUpdateResult{Revision: "r2"})
	c.BeginEdit()
	c.Form().Title = "Ředitelka"
	c.Save()
	if !c.Saving() {
		t.Fatal("not saving")
	}
	l.run()
	var p api.ContactUpdateParams
	d.last(api.MethodContactUpdate, &p)
	if p.ContactID != "p_1" || p.Revision != "r1" || p.Card.Title != "Ředitelka" || p.Card.DisplayName != "Jana Nováková" {
		t.Fatalf("params %+v", p)
	}
	if c.Editing() || c.Saving() || d.count(api.MethodContactGet) != 2 {
		t.Fatal("after save")
	}
}

func TestContactValidationKeepsTheForm(t *testing.T) {
	c, d, _, _ := contactSetup(t, janaCard())
	c.BeginEdit()
	c.Form().Emails[0].Value = "nope"
	c.Save()
	if !c.Editing() || len(c.Problems()) != 1 || d.count(api.MethodContactUpdate) != 0 {
		t.Fatal("validation")
	}
}

func TestContactOfflineKeepsTheForm(t *testing.T) {
	c, d, l, _ := contactSetup(t, janaCard())
	d.fail(api.MethodContactUpdate, &api.Error{Code: api.CodeOffline, Message: "x"})
	c.BeginEdit()
	c.Form().Org = "Jiná"
	c.Save()
	l.run()
	if !c.Editing() || c.Form().Org != "Jiná" || c.Error() != "Contacts cannot be changed while offline." {
		t.Fatalf("err %q", c.Error())
	}
	c.CancelEdit()
	if c.Editing() || c.Error() != "" {
		t.Fatal("cancel")
	}
}

func TestContactConflictReloadKeepsTheUsersFields(t *testing.T) {
	c, d, l, qs := contactSetup(t, janaCard())
	server := janaCard()
	server.Revision = "r9"
	server.Org = "Novák a syn"
	server.Note = "nová"
	d.fail(api.MethodContactUpdate, &api.Error{Code: api.CodeContactConflict, Message: "x",
		Data: map[string]any{"server": server}})
	c.BeginEdit()
	c.Form().Title = "Ředitelka"
	c.Save()
	l.run()
	if len(*qs) != 1 || (*qs)[0].Confirm != "Reload" {
		t.Fatalf("questions %+v", *qs)
	}
	f := c.Form()
	if f == nil || f.Org != "Novák a syn" || f.Title != "Ředitelka" || f.Note != "nová" || f.Original.Revision != "r9" || c.Error() != "" {
		t.Fatalf("form %+v err %q", f, c.Error())
	}
	d.answer(api.MethodContactUpdate, api.ContactUpdateResult{Revision: "r10"})
	c.Save()
	l.run()
	var p api.ContactUpdateParams
	d.last(api.MethodContactUpdate, &p)
	if p.Revision != "r9" || p.Card.Org != "Novák a syn" || c.Editing() {
		t.Fatalf("second save %+v", p)
	}
}

func TestContactConflictWithoutDataAsksGet(t *testing.T) {
	c, d, l, qs := contactSetup(t, janaCard())
	d.fail(api.MethodContactUpdate, &api.Error{Code: api.CodeContactConflict, Message: "x"})
	c.BeginEdit()
	c.Form().Title = "X"
	c.Save()
	l.run()
	if len(*qs) != 1 || c.Form().Title != "X" {
		t.Fatal("conflict without data")
	}
	// Cancel keeps the form as it was.
	c.OnQuestion = func(_ contacts.Question, answer func(bool)) { answer(false) }
	c.Save()
	l.run()
	if !c.Editing() || c.Error() != "The contact changed meanwhile" {
		t.Fatalf("err %q", c.Error())
	}
	_ = d
}

func TestContactCreate(t *testing.T) {
	c, d, l, _ := contactSetup(t, janaCard())
	created := api.ContactID("")
	c.OnCreated = func(id api.ContactID) { created = id }
	d.answer(api.MethodContactCreate, api.ContactCreateResult{ContactID: "p_new", Revision: "n1"})
	a := contacts.NewAddFromChip(api.Address{Name: "Petr Svoboda", Address: "petr@example.com"}, "acc_d", books(), accounts(), en{})
	c.BeginNew(a.Book, a.Form())
	if !c.Editing() || c.Card() != nil {
		t.Fatal("new")
	}
	c.Save()
	l.run()
	var p api.ContactCreateParams
	d.last(api.MethodContactCreate, &p)
	if p.BookID != "b_d" || p.Card.DisplayName != "Petr Svoboda" || p.Card.Emails[0].Address != "petr@example.com" {
		t.Fatalf("params %+v", p)
	}
	if created != "p_new" || c.ID() != "p_new" || c.Editing() {
		t.Fatal("created")
	}
	// A refused create keeps the form with the sentence.
	d.fail(api.MethodContactCreate, &api.Error{Code: api.CodeAddressBookReadOnly, Message: "x"})
	c.BeginNew("b_m1", nil)
	c.Form().Given = "X"
	c.Save()
	l.run()
	if !c.Editing() || c.Error() != "This address book cannot be changed." {
		t.Fatal(c.Error())
	}
	// Consent names the account.
	d.fail(api.MethodContactCreate, &api.Error{Code: api.CodeContactsConsentRequired, Message: "x"})
	c.Save()
	l.run()
	if c.Error() != "Microsoft needs your permission to access the contacts of Microsoft 365 (jana@firma.cz)." {
		t.Fatal(c.Error())
	}
}

func TestContactDelete(t *testing.T) {
	c, d, l, qs := contactSetup(t, janaCard())
	deleted := api.ContactID("")
	c.OnDeleted = func(id api.ContactID) { deleted = id }
	d.answer(api.MethodContactDelete, api.ContactDeleteResult{})
	c.Delete()
	l.run()
	if len(*qs) != 1 || (*qs)[0].Heading != "Delete contact Jana Nováková?" ||
		!strings.Contains((*qs)[0].Body, "Microsoft 365 (jana@firma.cz)") {
		t.Fatalf("question %+v", *qs)
	}
	var p api.ContactDeleteParams
	d.last(api.MethodContactDelete, &p)
	if p.ContactID != "p_1" || p.Revision != "r1" || deleted != "p_1" || c.Card() != nil {
		t.Fatal("deleted")
	}
}

func TestContactDeleteCancelledAndConflict(t *testing.T) {
	c, d, l, _ := contactSetup(t, janaCard())
	c.OnQuestion = func(_ contacts.Question, answer func(bool)) { answer(false) }
	c.Delete()
	l.run()
	if d.count(api.MethodContactDelete) != 0 {
		t.Fatal("deleted after Cancel")
	}
	c.OnQuestion = func(_ contacts.Question, answer func(bool)) { answer(true) }
	server := janaCard()
	server.Revision = "r5"
	d.fail(api.MethodContactDelete, &api.Error{Code: api.CodeContactConflict, Message: "x", Data: map[string]any{"server": server}})
	c.Delete()
	l.run()
	if c.Card() == nil || c.Card().Revision != "r5" || c.Error() == "" {
		t.Fatal("conflict on delete")
	}
	// Read only: no delete, no edit.
	ro := janaCard()
	ro.ReadOnly = true
	c2, _, _, _ := contactSetup(t, ro)
	if c2.CanEdit() || c2.CanDelete() {
		t.Fatal("read only")
	}
}

func TestContactFollowsChanges(t *testing.T) {
	c, d, l, _ := contactSetup(t, janaCard())
	n := d.count(api.MethodContactGet)
	c.ContactsChanged(api.ContactsChangedNotification{AccountID: "acc_d"})
	c.ContactsChanged(api.ContactsChangedNotification{AccountID: "acc_m", BookIDs: []api.AddressBookID{"b_m2"}})
	l.run()
	if d.count(api.MethodContactGet) != n {
		t.Fatal("reloaded for another book")
	}
	c.ContactsChanged(api.ContactsChangedNotification{AccountID: "acc_m"})
	l.run()
	if d.count(api.MethodContactGet) != n+1 {
		t.Fatal("not reloaded")
	}
	c.BeginEdit()
	c.ContactsChanged(api.ContactsChangedNotification{})
	l.run()
	if d.count(api.MethodContactGet) != n+1 {
		t.Fatal("reloaded under the form")
	}
}

// A card of a Microsoft book is checked against Graph's limits before
// anything is sent; a field the daemon refuses is marked, not said.
func TestContactGraphLimitsAndRefusedField(t *testing.T) {
	c, d, l, _ := contactSetup(t, janaCard())
	c.BeginEdit()
	c.Form().URLs = []contacts.LabelledValue{{Value: "a.cz"}, {Value: "b.cz"}}
	c.Save()
	l.run()
	if d.count(api.MethodContactUpdate) != 0 || len(c.Problems()) != 1 || c.Problems()[0].Key != contacts.KeyURLs {
		t.Fatalf("graph limits: calls %d problems %+v", d.count(api.MethodContactUpdate), c.Problems())
	}
	c.Form().URLs = c.Form().URLs[:1]
	d.fail(api.MethodContactUpdate, &api.Error{Code: api.CodeInvalidArgument, Message: "x",
		Data: map[string]any{"field": "phones[0].number"}})
	c.Save()
	l.run()
	p := c.Problems()
	if !c.Editing() || c.Error() != "" || len(p) != 1 || p[0].Key != contacts.KeyPhones || p[0].Index != 0 {
		t.Errorf("refused field: editing %v err %q problems %+v", c.Editing(), c.Error(), p)
	}
}
