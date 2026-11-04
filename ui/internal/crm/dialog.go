// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package crm

import (
	"regexp"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
)

// Kinds are the kinds of CRM items in the order the dialog groups them:
// Contacts, Companies, Deals, Projects.
var Kinds = []api.CrmItemKind{api.CrmContact, api.CrmCompany, api.CrmDeal, api.CrmProject}

// CanBeSuperior reports a kind that can be a saved message's superior
// item (eWay-CRM's OUTLOOKPROJECT): a deal or a project.
func CanBeSuperior(k api.CrmItemKind) bool { return k == api.CrmDeal || k == api.CrmProject }

// guidShape is the shape of an eWay-CRM GUID (the daemon's
// eway.ValidGUID).
var guidShape = regexp.MustCompile(`^[0-9a-f]{8}-([0-9a-f]{4}-){3}[0-9a-f]{12}$`)

// knownKind reports one of Kinds.
func knownKind(k api.CrmItemKind) bool {
	for _, x := range Kinds {
		if x == k {
			return true
		}
	}
	return false
}

// refOf is an item's key: its kind and its GUID in lower case; ok false
// for an unknown kind or a GUID of another shape (such an item is left
// out: it could not be saved).
func refOf(kind api.CrmItemKind, guid string) (api.CrmItemRef, bool) {
	g := strings.ToLower(strings.TrimSpace(guid))
	if !knownKind(kind) || !guidShape.MatchString(g) {
		return api.CrmItemRef{}, false
	}
	return api.CrmItemRef{Kind: kind, GUID: g}, true
}

// cleanItem is it with its display texts cleaned and capped again and its
// URL dropped unless it is https.
func cleanItem(it api.CrmItem, ref api.CrmItemRef) api.CrmItem {
	return api.CrmItem{
		Kind:     ref.Kind,
		GUID:     ref.GUID,
		Name:     capBytes(jira.Clean(it.Name), api.MaxCrmNameBytes),
		Detail:   capBytes(jira.Clean(it.Detail), api.MaxCrmDetailBytes),
		Reason:   it.Reason,
		Superior: it.Superior,
		URL:      OpenURL(it.URL),
	}
}

// DialogEntry is a row of the dialog: an item with its check box and
// whether it is the superior item (the "Superior Item" marker, a radio
// among the checked deals and projects).
type DialogEntry struct {
	Item     api.CrmItem
	Checked  bool
	Superior bool
}

// Ref names the entry's item.
func (e DialogEntry) Ref() api.CrmItemRef {
	return api.CrmItemRef{Kind: e.Item.Kind, GUID: e.Item.GUID}
}

// CanBeSuperior: the entry offers the Superior Item marker.
func (e DialogEntry) CanBeSuperior() bool { return CanBeSuperior(e.Item.Kind) }

// DialogGroup is the entries of one kind, in Kinds' order.
type DialogGroup struct {
	Kind    api.CrmItemKind
	Entries []DialogEntry
}

// Dialog is the model of the Save to eWay-CRM dialog over the result of
// crm.lookup (and the Edit Links dialog of one saved message), with the
// search results merged in. Not safe for concurrent use.
//
// Prefill (docs/eway.md D9, §5 Q2): contacts and companies found by an
// address or a tracked domain are checked; the one deal or project the
// daemon flags Superior (only when exactly one open one was found) is
// checked as the superior item; everything else is listed unchecked. Over
// one saved message (Editing) the record's items are checked instead,
// the superior one as recorded, and the suggestions are listed unchecked.
//
// Superior item (§5 Q1): at most one at a time. Checking a deal or project
// while none is superior makes it the superior item; checking another
// adds a plain link. SetSuperior moves the marker. Unchecking the
// superior item leaves none (the next deal or project the user checks
// becomes it); nothing is promoted behind the user's back.
type Dialog struct {
	accountID api.AccountID
	messages  []api.MessageID
	editing   bool
	partial   bool

	items   map[api.CrmItemRef]api.CrmItem
	order   map[api.CrmItemKind][]api.CrmItemRef
	checked map[api.CrmItemRef]bool
	// superior is the superior item; the zero ref for none.
	superior api.CrmItemRef
	private  bool

	// The saved message's record when Editing.
	recorded         map[api.CrmItemRef]bool
	recordedSuperior api.CrmItemRef
	recordedPrivate  bool
}

// NewDialog builds the dialog over res, the answer of crm.lookup for the
// messages the dialog saves. It edits when res is about one message that
// is saved (Saved[0].Record).
func NewDialog(res api.CrmLookupResult) *Dialog {
	d := &Dialog{
		accountID: res.AccountID,
		partial:   res.Partial,
		items:     map[api.CrmItemRef]api.CrmItem{},
		order:     map[api.CrmItemKind][]api.CrmItemRef{},
		checked:   map[api.CrmItemRef]bool{},
		recorded:  map[api.CrmItemRef]bool{},
	}
	for _, s := range res.Saved {
		d.messages = append(d.messages, s.MessageID)
	}
	if len(res.Saved) == 1 && res.Saved[0].Record != nil {
		rec := res.Saved[0].Record
		d.editing = true
		d.private, d.recordedPrivate = rec.Private, rec.Private
		for _, it := range rec.Items {
			ref, ok := d.add(it)
			if !ok {
				continue
			}
			d.checked[ref], d.recorded[ref] = true, true
			if it.Superior && CanBeSuperior(ref.Kind) && d.superior == (api.CrmItemRef{}) {
				d.superior, d.recordedSuperior = ref, ref
			}
		}
	}
	for _, it := range res.Suggestions {
		ref, ok := d.add(it)
		if !ok || d.editing {
			continue
		}
		switch {
		case (ref.Kind == api.CrmContact || ref.Kind == api.CrmCompany) &&
			(it.Reason == api.CrmReasonAddress || it.Reason == api.CrmReasonDomain):
			d.checked[ref] = true
		case it.Superior && CanBeSuperior(ref.Kind) && d.superior == (api.CrmItemRef{}):
			d.checked[ref] = true
			d.superior = ref
		}
	}
	return d
}

// add lists it unless it is listed already or invalid; the ref either
// way when valid.
func (d *Dialog) add(it api.CrmItem) (api.CrmItemRef, bool) {
	ref, ok := refOf(it.Kind, it.GUID)
	if !ok {
		return ref, false
	}
	if _, known := d.items[ref]; !known {
		d.items[ref] = cleanItem(it, ref)
		d.order[ref.Kind] = append(d.order[ref.Kind], ref)
	}
	return ref, true
}

// AccountID is the eWay-CRM account crm.lookup answered for.
func (d *Dialog) AccountID() api.AccountID { return d.accountID }

// Messages are the messages of the lookup, in order.
func (d *Dialog) Messages() []api.MessageID { return append([]api.MessageID(nil), d.messages...) }

// Editing: the dialog edits the links of one saved message (its title is
// Edit Links).
func (d *Dialog) Editing() bool { return d.editing }

// Partial: the CRM did not answer every lookup in time; the dialog shows
// the hint (DialogStrings.Partial).
func (d *Dialog) Partial() bool { return d.partial }

// IsEmpty: nothing is listed; the dialog shows DialogStrings.Empty (the
// search still works).
func (d *Dialog) IsEmpty() bool { return len(d.items) == 0 }

// OffersSearchHint reports whether the dialog shows the quiet line that
// deals and projects are found by searching: something is listed, but
// no deal or project (owner, 2026-10-17: the empty space under two
// groups left the user wondering what was missing).
func (d *Dialog) OffersSearchHint() bool {
	if len(d.items) == 0 {
		return false
	}
	for ref := range d.items {
		if CanBeSuperior(ref.Kind) {
			return false
		}
	}
	return true
}

// Private is the Private check box (off for a new save, docs/eway.md
// §5 Q4; the record's when editing).
func (d *Dialog) Private() bool { return d.private }

// SetPrivate sets the Private check box.
func (d *Dialog) SetPrivate(on bool) { d.private = on }

// Superior is the superior item; false when there is none.
func (d *Dialog) Superior() (api.CrmItemRef, bool) {
	return d.superior, d.superior != (api.CrmItemRef{})
}

// Checked reports an item's check box.
func (d *Dialog) Checked(ref api.CrmItemRef) bool {
	r, ok := refOf(ref.Kind, ref.GUID)
	return ok && d.checked[r]
}

// Groups are the listed items by kind in Kinds' order, each in the order
// it was listed (the record's, the daemon's ranking, then search results);
// a kind without items is left out.
func (d *Dialog) Groups() []DialogGroup {
	var out []DialogGroup
	for _, k := range Kinds {
		refs := d.order[k]
		if len(refs) == 0 {
			continue
		}
		g := DialogGroup{Kind: k}
		for _, ref := range refs {
			g.Entries = append(g.Entries, DialogEntry{Item: d.items[ref], Checked: d.checked[ref], Superior: ref == d.superior})
		}
		out = append(out, g)
	}
	return out
}

// Toggle sets an item's check box; it reports whether anything changed.
// Checking a deal or project while none is superior makes it the superior
// item; unchecking the superior item leaves none. A check that would make
// the save change more than api.MaxCrmItems items is refused (false; the
// view leaves the box unchecked).
func (d *Dialog) Toggle(ref api.CrmItemRef, on bool) bool {
	r, ok := refOf(ref.Kind, ref.GUID)
	if _, known := d.items[r]; !ok || !known || d.checked[r] == on {
		return false
	}
	if !on {
		delete(d.checked, r)
		if d.superior == r {
			d.superior = api.CrmItemRef{}
		}
		return true
	}
	prevSuperior := d.superior
	d.checked[r] = true
	if CanBeSuperior(r.Kind) && d.superior == (api.CrmItemRef{}) {
		d.superior = r
	}
	if d.changeCount() > api.MaxCrmItems {
		delete(d.checked, r)
		d.superior = prevSuperior
		return false
	}
	return true
}

// SetSuperior makes a deal or project the superior item, checking it when
// it is not; false when it cannot be one, is not listed, or the check is
// refused (Toggle).
func (d *Dialog) SetSuperior(ref api.CrmItemRef) bool {
	r, ok := refOf(ref.Kind, ref.GUID)
	if _, known := d.items[r]; !ok || !known || !CanBeSuperior(r.Kind) || d.superior == r {
		return false
	}
	if !d.checked[r] && !d.Toggle(r, true) {
		return false
	}
	prev := d.superior
	d.superior = r
	if d.changeCount() > api.MaxCrmItems {
		d.superior = prev
		return false
	}
	return true
}

// Merge lists the items of a crm.search answer that are not listed yet,
// unchecked, after the others of their kind; checks and the superior
// item stay. It returns how many it added.
func (d *Dialog) Merge(res api.CrmSearchResult) int {
	n := 0
	for _, it := range res.Items {
		ref, ok := refOf(it.Kind, it.GUID)
		if !ok {
			continue
		}
		if _, known := d.items[ref]; known {
			continue
		}
		it.Superior = false
		d.add(it)
		n++
	}
	return n
}

// diff is what the save changes: items to link, to unlink, and the new
// superior item (nil: none added), each in the dialog's order.
func (d *Dialog) diff() (link, unlink []api.CrmItemRef, superior *api.CrmItemRef) {
	for _, k := range Kinds {
		for _, ref := range d.order[k] {
			cur, was := d.checked[ref], d.recorded[ref]
			switch {
			case cur && !was && ref != d.superior:
				link = append(link, ref)
			case !cur && was:
				unlink = append(unlink, ref)
			}
		}
	}
	if d.superior != (api.CrmItemRef{}) && d.superior != d.recordedSuperior {
		s := d.superior
		superior = &s
		// The superior item it replaces stays as a plain link when it
		// stays checked (eWay-CRM keeps one superior item per message).
		if old := d.recordedSuperior; old != (api.CrmItemRef{}) && d.checked[old] {
			link = append(link, old)
		}
	}
	return link, unlink, superior
}

// changeCount is how many items the save names (api.MaxCrmItems).
func (d *Dialog) changeCount() int {
	link, unlink, superior := d.diff()
	n := len(link) + len(unlink)
	if superior != nil {
		n++
	}
	return n
}

// Changed: over a saved message, the links or Private differ from the
// record. A new save is always a change.
func (d *Dialog) Changed() bool {
	if !d.editing {
		return true
	}
	return d.changeCount() > 0 || d.private != d.recordedPrivate
}

// CanSave is the Save button's state: a new save always (with no item
// checked the message is saved without links), an edit when it Changed.
func (d *Dialog) CanSave() bool { return d.Changed() && d.changeCount() <= api.MaxCrmItems }

// SaveParams is crm.save for the dialog's choice over the messages
// messageIDs of messageAccountID (those of the lookup): the items to
// link and, when Editing, to unlink, the new superior item and Private.
// false when the call would break a limit of docs/api.md §4.17: 1 to
// api.MaxCrmMessages messages (exactly one when Editing), at most
// api.MaxCrmItems items.
func (d *Dialog) SaveParams(messageAccountID api.AccountID, messageIDs []api.MessageID) (api.CrmSaveParams, bool) {
	n := len(messageIDs)
	if n == 0 || n > api.MaxCrmMessages || (d.editing && n != 1) {
		return api.CrmSaveParams{}, false
	}
	link, unlink, superior := d.diff()
	count := len(link) + len(unlink)
	if superior != nil {
		count++
	}
	if count > api.MaxCrmItems {
		return api.CrmSaveParams{}, false
	}
	if !d.editing {
		unlink = nil
	}
	return api.CrmSaveParams{
		AccountID:        d.accountID,
		MessageAccountID: messageAccountID,
		MessageIDs:       append([]api.MessageID(nil), messageIDs...),
		Link:             link,
		Unlink:           unlink,
		Superior:         superior,
		Private:          d.private,
	}, true
}

// SearchParams is crm.search for the dialog's search field as typed:
// false while the query is shorter than api.MinCrmQueryBytes or longer
// than api.MaxCrmQueryBytes after trimming (the field then searches
// nothing and the listed items stay).
func (d *Dialog) SearchParams(raw string) (api.CrmSearchParams, bool) {
	q := strings.TrimSpace(raw)
	if len(q) < api.MinCrmQueryBytes || len(q) > api.MaxCrmQueryBytes {
		return api.CrmSearchParams{}, false
	}
	return api.CrmSearchParams{AccountID: d.accountID, Query: q}, true
}
