// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package crm

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// guid is a GUID of the right shape for item n.
func guid(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

func item(kind api.CrmItemKind, n int, name string, reason api.CrmReason) api.CrmItem {
	return api.CrmItem{Kind: kind, GUID: guid(n), Name: name, Reason: reason, URL: "https://open.eway-crm.com/?l=" + guid(n)}
}

func ref(kind api.CrmItemKind, n int) api.CrmItemRef {
	return api.CrmItemRef{Kind: kind, GUID: guid(n)}
}

// show writes the groups as "kind: name[x][*] …", x checked, * superior.
func show(d *Dialog) string {
	var parts []string
	for _, g := range d.Groups() {
		var es []string
		for _, e := range g.Entries {
			s := e.Item.Name
			if e.Checked {
				s += "x"
			}
			if e.Superior {
				s += "*"
			}
			es = append(es, s)
		}
		parts = append(parts, string(g.Kind)+": "+strings.Join(es, " "))
	}
	return strings.Join(parts, " | ")
}

// A lookup of one message that is not saved.
func lookup() api.CrmLookupResult {
	return api.CrmLookupResult{
		AccountID: "e1",
		Saved:     []api.CrmSavedMessage{{MessageID: "m_1"}},
		Suggestions: []api.CrmItem{
			item(api.CrmDeal, 5, "Fleet renewal", api.CrmReasonContact),
			item(api.CrmContact, 1, "Novák, Jan", api.CrmReasonAddress),
			item(api.CrmCompany, 2, "Acme", api.CrmReasonDomain),
			item(api.CrmCompany, 3, "Acme Holding", api.CrmReasonContact),
			item(api.CrmProject, 6, "Rollout", api.CrmReasonCompany),
			// Invalid: an unknown kind, a GUID of another shape, a repeat.
			{Kind: "lead", GUID: guid(9), Name: "x"},
			{Kind: api.CrmContact, GUID: "not-a-guid", Name: "y"},
			item(api.CrmContact, 1, "Novák again", api.CrmReasonAddress),
		},
	}
}

func TestDialogPrefill(t *testing.T) {
	d := NewDialog(lookup())
	if d.Editing() || d.IsEmpty() || d.Partial() || d.Private() || d.AccountID() != "e1" {
		t.Errorf("state %+v", d)
	}
	// Contacts and companies by address or domain checked; the rest not;
	// no deal flagged Superior, so none checked.
	want := "contact: Novák, Janx | company: Acmex Acme Holding | deal: Fleet renewal | project: Rollout"
	if got := show(d); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if _, ok := d.Superior(); ok {
		t.Error("a superior item")
	}
	if d.Groups()[0].Entries[0].Item.URL == "" || d.Groups()[0].Entries[0].CanBeSuperior() || !d.Groups()[2].Entries[0].CanBeSuperior() {
		t.Error("entry")
	}

	// Exactly one open deal: the daemon flags it, the dialog checks it as
	// superior.
	res := lookup()
	res.Suggestions[0].Superior = true
	res.Partial = true
	d = NewDialog(res)
	if got := show(d); !strings.Contains(got, "deal: Fleet renewalx*") || !d.Partial() {
		t.Errorf("flagged superior: %s", got)
	}
	if NewDialog(api.CrmLookupResult{AccountID: "e1"}).IsEmpty() != true {
		t.Error("empty")
	}
}

func TestDialogSuperior(t *testing.T) {
	d := NewDialog(lookup())
	// The first deal or project checked becomes superior, another a link.
	if !d.Toggle(ref(api.CrmProject, 6), true) || !d.Toggle(ref(api.CrmDeal, 5), true) {
		t.Fatal("toggle")
	}
	if got := show(d); !strings.Contains(got, "deal: Fleet renewalx |") || !strings.Contains(got, "project: Rolloutx*") {
		t.Errorf("first checked is superior: %s", got)
	}
	if d.Toggle(ref(api.CrmDeal, 5), true) {
		t.Error("toggle to the same state changed something")
	}
	// The marker moves.
	if !d.SetSuperior(ref(api.CrmDeal, 5)) {
		t.Fatal("set superior")
	}
	if got := show(d); !strings.Contains(got, "deal: Fleet renewalx*") || !strings.Contains(got, "project: Rolloutx") {
		t.Errorf("moved: %s", got)
	}
	// Unchecking the superior leaves none; the next one checked takes it.
	d.Toggle(ref(api.CrmDeal, 5), false)
	if _, ok := d.Superior(); ok {
		t.Errorf("superior after uncheck: %s", show(d))
	}
	d.Toggle(ref(api.CrmProject, 6), false)
	d.Toggle(ref(api.CrmProject, 6), true)
	if s, ok := d.Superior(); !ok || s != ref(api.CrmProject, 6) {
		t.Errorf("rechecked: %s", show(d))
	}
	// SetSuperior checks an unchecked one; contacts cannot be superior;
	// unknown items are ignored.
	d.Toggle(ref(api.CrmDeal, 5), false)
	if !d.SetSuperior(ref(api.CrmDeal, 5)) || !d.Checked(ref(api.CrmDeal, 5)) {
		t.Error("set superior of an unchecked deal")
	}
	if d.SetSuperior(ref(api.CrmContact, 1)) || d.SetSuperior(ref(api.CrmDeal, 77)) || d.Toggle(ref(api.CrmDeal, 77), true) {
		t.Error("superior of a contact or unknown item")
	}
	// Upper-case GUIDs name the same item.
	if !d.Checked(api.CrmItemRef{Kind: api.CrmDeal, GUID: strings.ToUpper(guid(5))}) {
		t.Error("upper-case GUID")
	}
}

func TestDialogSaveNew(t *testing.T) {
	d := NewDialog(lookup())
	d.Toggle(ref(api.CrmProject, 6), true)
	d.Toggle(ref(api.CrmDeal, 5), true)
	d.SetPrivate(true)
	if !d.CanSave() || !d.Changed() {
		t.Error("a new save can always go")
	}
	p, ok := d.SaveParams("m1", []api.MessageID{"m_1", "m_2"})
	if !ok {
		t.Fatal("params")
	}
	want := api.CrmSaveParams{
		AccountID: "e1", MessageAccountID: "m1", MessageIDs: []api.MessageID{"m_1", "m_2"},
		Link:     []api.CrmItemRef{ref(api.CrmContact, 1), ref(api.CrmCompany, 2), ref(api.CrmDeal, 5)},
		Superior: &api.CrmItemRef{Kind: api.CrmProject, GUID: guid(6)},
		Private:  true,
	}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("got  %+v\nwant %+v", p, want)
	}
	// Nothing checked: the message is saved without links.
	empty := NewDialog(api.CrmLookupResult{AccountID: "e1", Saved: []api.CrmSavedMessage{{MessageID: "m_1"}}})
	if p, ok := empty.SaveParams("m1", []api.MessageID{"m_1"}); !ok || p.Link != nil || p.Superior != nil || !empty.CanSave() {
		t.Errorf("empty %+v", p)
	}
	for _, ids := range [][]api.MessageID{nil, make([]api.MessageID, api.MaxCrmMessages+1)} {
		if _, ok := d.SaveParams("m1", ids); ok {
			t.Errorf("%d messages", len(ids))
		}
	}
}

// One saved message: the record checked, the result a diff.
func savedLookup() api.CrmLookupResult {
	res := lookup()
	res.Saved[0].Record = &api.CrmRecord{
		AccountID: "e1", ItemGUID: guid(100), Private: true, URL: "https://open.eway-crm.com/?l=e",
		Items: []api.CrmItem{
			item(api.CrmContact, 1, "Novák, Jan", api.CrmReasonSaved),
			item(api.CrmCompany, 3, "Acme Holding", api.CrmReasonSaved),
			{Kind: api.CrmDeal, GUID: guid(5), Name: "Fleet renewal", Reason: api.CrmReasonSaved, Superior: true},
		},
	}
	return res
}

func TestDialogEdit(t *testing.T) {
	d := NewDialog(savedLookup())
	if !d.Editing() || !d.Private() {
		t.Fatal("editing")
	}
	// The record's items checked, the suggestions after them unchecked.
	want := "contact: Novák, Janx | company: Acme Holdingx Acme | deal: Fleet renewalx* | project: Rollout"
	if got := show(d); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if d.Changed() || d.CanSave() {
		t.Error("nothing changed yet")
	}
	d.SetPrivate(false)
	if !d.CanSave() {
		t.Error("privacy changed")
	}
	p, _ := d.SaveParams("m1", []api.MessageID{"m_1"})
	if p.Link != nil || p.Unlink != nil || p.Superior != nil || p.Private {
		t.Errorf("only privacy %+v", p)
	}
	d.SetPrivate(true)

	// Uncheck a company, check another, make the project superior while
	// the old deal stays checked (as a plain link).
	d.Toggle(ref(api.CrmCompany, 3), false)
	d.Toggle(ref(api.CrmCompany, 2), true)
	d.SetSuperior(ref(api.CrmProject, 6))
	p, ok := d.SaveParams("m1", []api.MessageID{"m_1"})
	if !ok {
		t.Fatal("params")
	}
	want2 := api.CrmSaveParams{
		AccountID: "e1", MessageAccountID: "m1", MessageIDs: []api.MessageID{"m_1"},
		Link:     []api.CrmItemRef{ref(api.CrmCompany, 2), ref(api.CrmDeal, 5)},
		Unlink:   []api.CrmItemRef{ref(api.CrmCompany, 3)},
		Superior: &api.CrmItemRef{Kind: api.CrmProject, GUID: guid(6)},
		Private:  true,
	}
	if !reflect.DeepEqual(p, want2) {
		t.Errorf("got  %+v\nwant %+v", p, want2)
	}
	// Unchecking the recorded superior unlinks it.
	d = NewDialog(savedLookup())
	d.Toggle(ref(api.CrmDeal, 5), false)
	p, _ = d.SaveParams("m1", []api.MessageID{"m_1"})
	if !reflect.DeepEqual(p.Unlink, []api.CrmItemRef{ref(api.CrmDeal, 5)}) || p.Superior != nil || p.Link != nil {
		t.Errorf("unlink superior %+v", p)
	}
	// Unchecked and checked again: no change.
	d.Toggle(ref(api.CrmDeal, 5), true)
	if d.Changed() {
		t.Errorf("rechecked superior is a change: %s", show(d))
	}
	// Editing saves exactly one message.
	if _, ok := d.SaveParams("m1", []api.MessageID{"m_1", "m_2"}); ok {
		t.Error("edit of two messages")
	}
}

func TestDialogMerge(t *testing.T) {
	d := NewDialog(lookup())
	d.Toggle(ref(api.CrmDeal, 5), true)
	n := d.Merge(api.CrmSearchResult{Items: []api.CrmItem{
		item(api.CrmDeal, 5, "Fleet renewal (search)", api.CrmReasonSearch),
		{Kind: api.CrmProject, GUID: guid(7), Name: "Pilot", Reason: api.CrmReasonSearch, Superior: true},
		item(api.CrmContact, 8, "Dvořák, Petr", api.CrmReasonSearch),
		{Kind: api.CrmContact, GUID: "bad", Name: "z"},
	}})
	if n != 2 {
		t.Errorf("added %d", n)
	}
	want := "contact: Novák, Janx Dvořák, Petr | company: Acmex Acme Holding | deal: Fleet renewalx* | project: Rollout Pilot"
	if got := show(d); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if p, ok := d.SearchParams("  ac  "); !ok || p.Query != "ac" || p.AccountID != "e1" {
		t.Errorf("search %+v", p)
	}
	for _, q := range []string{"", " a ", strings.Repeat("x", api.MaxCrmQueryBytes+1)} {
		if _, ok := d.SearchParams(q); ok {
			t.Errorf("search %q", q)
		}
	}
}

// Hostile names are cleaned and capped; a URL that is not https dropped.
func TestDialogCleansItems(t *testing.T) {
	res := api.CrmLookupResult{AccountID: "e1", Suggestions: []api.CrmItem{{
		Kind: api.CrmContact, GUID: strings.ToUpper(guid(1)), Reason: api.CrmReasonAddress,
		Name: "Eve\u202e\x00 \n Evil" + strings.Repeat("ž", 400), Detail: "a\tb", URL: "javascript:alert(1)",
	}}}
	e := NewDialog(res).Groups()[0].Entries[0]
	if !strings.HasPrefix(e.Item.Name, "Eve Evil") || len(e.Item.Name) > api.MaxCrmNameBytes || !strings.HasSuffix(e.Item.Name, "…") {
		t.Errorf("name %q (%d bytes)", e.Item.Name, len(e.Item.Name))
	}
	if e.Item.Detail != "a b" || e.Item.URL != "" || e.Item.GUID != guid(1) {
		t.Errorf("entry %+v", e.Item)
	}
}

// A save names at most api.MaxCrmItems items: the check over it is refused.
func TestDialogLimit(t *testing.T) {
	res := api.CrmLookupResult{AccountID: "e1", Saved: []api.CrmSavedMessage{{MessageID: "m_1"}}}
	for i := 0; i <= api.MaxCrmItems; i++ {
		res.Suggestions = append(res.Suggestions, item(api.CrmContact, i, fmt.Sprint("c", i), api.CrmReasonContact))
	}
	d := NewDialog(res)
	for i := 0; i < api.MaxCrmItems; i++ {
		if !d.Toggle(ref(api.CrmContact, i), true) {
			t.Fatalf("check %d", i)
		}
	}
	if d.Toggle(ref(api.CrmContact, api.MaxCrmItems), true) || d.Checked(ref(api.CrmContact, api.MaxCrmItems)) {
		t.Error("over the limit")
	}
	if p, ok := d.SaveParams("m1", []api.MessageID{"m_1"}); !ok || len(p.Link) != api.MaxCrmItems || !d.CanSave() {
		t.Errorf("at the limit %d %v", len(p.Link), ok)
	}
}

// The quiet line under the groups: shown with contacts or companies
// listed but no deal or project, gone once a search adds one, never on an
// empty dialog.
func TestSearchHint(t *testing.T) {
	res := lookup()
	var only []api.CrmItem
	for _, it := range res.Suggestions {
		if !CanBeSuperior(it.Kind) {
			only = append(only, it)
		}
	}
	res.Suggestions = only
	d := NewDialog(res)
	if !d.OffersSearchHint() {
		t.Error("contacts and companies only: hint expected")
	}
	d.Merge(api.CrmSearchResult{Items: []api.CrmItem{item(api.CrmDeal, 9, "Found deal", api.CrmReasonSearch)}})
	if d.OffersSearchHint() {
		t.Error("a deal listed: no hint")
	}
	if NewDialog(api.CrmLookupResult{}).OffersSearchHint() {
		t.Error("empty dialog: no hint, the empty text speaks")
	}
	if NewDialog(lookup()).OffersSearchHint() {
		t.Error("deals in the lookup: no hint")
	}
}
