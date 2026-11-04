// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package toolbar

import "strings"

// Variant is a client's difference from the catalog (decision T12,
// docs/plans/toolbar-customization.md §17): the items it lacks, the
// sections it lets an item stand in besides the catalog's, and its own
// default arrangement per toolbar, so that an untouched toolbar looks as
// the client's did before customization. The stored arrangement (the
// keys Key names) is shared by every client: what a client lacks is left
// out when it reads a layout (NormalizeFor) and kept when it writes one
// (StoredFor). The zero Variant is the catalog itself, macOS's; the
// functions without a variant use it.
type Variant struct {
	// Omit are the items of each toolbar the client has no widget for:
	// it never offers or shows them, and keeps them in what it stores.
	Omit map[Bar][]ID
	// Extra are sections an item may stand in besides the catalog's (the
	// home stays the catalog's).
	Extra map[Bar]map[ID][]Section
	// Default is the client's default arrangement of a toolbar, nil for
	// Default(bar) (without the omitted items).
	Default map[Bar]Layout
}

// GTK is the GTK client's variant (docs/plans/toolbar-customization-gtk.md
// §1 and §2): no message filter in a toolbar (the toggle group under the
// list's header) and no sidebar toggle in Mail and the Board's List (the
// navigation split views cannot hide their sidebar); Mail's search
// toggles the search bar over the list and may stand in the list's
// header; the defaults are the header bars of window.blp,
// board_page.blp and calendar_page.blp in screen order. Print is
// FeaturePrint, off until GTK prints. Do not modify.
var GTK = Variant{
	Omit: map[Bar][]ID{
		BarMail:      {Filter, ToggleSidebar},
		BarBoardList: {ToggleSidebar},
	},
	Extra: map[Bar]map[ID][]Section{
		BarMail: {Search: {SectionList}},
	},
	Default: map[Bar]Layout{
		BarMail: {
			Mode, NewMessage, FlexibleSpace,
			Separator(SectionList), Refresh, FlexibleSpace, Search,
			Separator(SectionMessage), Reply, ReplyAll, Forward, FlexibleSpace,
			Archive, Trash, Space, CRM, Space, Star, Assistant, MoreActions,
		},
		BarBoardList: {
			Mode, BoardStyle, FlexibleSpace,
			Separator(SectionList), BoardTriage, BoardAccount,
			Separator(SectionDetail), BoardDone, BoardRemind, BoardArchive, BoardTrash, FlexibleSpace, BoardReply,
		},
		BarBoardFlow: {
			Mode, BoardStyle, FlexibleSpace, BoardTriage, BoardAccount,
		},
		BarCalendar: {
			Mode,
			Separator(SectionMain), ToggleSidebar, CalendarToday, CalendarStep, CalendarView, FlexibleSpace,
			CalendarSearch, CalendarNewEvent, CalendarAssistant,
		},
	},
}

// Windows is the Windows client's variant (docs/plans/toolbar-customization.md
// §15, W.1, W.2, W.5): it has every item (the mode switch, Mail's search
// and filter and the Calendar's search stand in fixed places, the title
// bar and the row under the list's header, and count only by being in
// the layout); Mail's search may stand in the list section as GTK's
// does, so a Windows save keeps where GTK put it; the defaults are
// today's bars: the sidebar's row (New Message), the list's header
// (Check for New Mail; the filter row), MessageCommandBar, BoardPage.Bar,
// BoardDetailView's actions and CalendarBar. Print is FeaturePrint, off
// until Windows prints. Do not modify.
var Windows = Variant{
	Extra: map[Bar]map[ID][]Section{
		BarMail: {Search: {SectionList}},
	},
	Default: map[Bar]Layout{
		BarMail: {
			Mode, NewMessage,
			Separator(SectionList), Refresh, Filter,
			Separator(SectionMessage), Reply, ReplyAll, Forward, FlexibleSpace,
			Archive, Trash, Space, CRM, Space, Star, Assistant, MoreActions, Search,
		},
		BarBoardList: {
			Mode, BoardStyle, FlexibleSpace,
			Separator(SectionList), BoardAccount, BoardTriage,
			Separator(SectionDetail), BoardDone, BoardRemind, BoardArchive, BoardTrash, FlexibleSpace, BoardReply,
		},
		BarBoardFlow: {
			Mode, BoardStyle, FlexibleSpace, BoardAccount, BoardTriage,
		},
		BarCalendar: {
			Mode,
			Separator(SectionMain), ToggleSidebar, CalendarToday, CalendarStep, CalendarView, FlexibleSpace,
			CalendarSearch, CalendarNewEvent, CalendarAssistant,
		},
	},
}

// Omits reports whether the client has no id in bar (spaces and
// separators are never omitted).
func (v Variant) Omits(bar Bar, id ID) bool {
	for _, x := range v.Omit[bar] {
		if x == id {
			return true
		}
	}
	return false
}

// LookupFor is id's entry in bar's catalog for the client: false for an
// omitted item, the sections with the variant's extra ones.
func LookupFor(v Variant, bar Bar, id ID) (Item, bool) {
	it, ok := Lookup(bar, id)
	if !ok || v.Omits(bar, id) {
		return Item{}, false
	}
	if extra := v.Extra[bar][id]; len(extra) > 0 {
		secs := append([]Section(nil), it.Sections...)
		for _, s := range extra {
			if !it.allows(s) {
				secs = append(secs, s)
			}
		}
		it.Sections = secs
	}
	return it, true
}

// CatalogFor is what the client's bar can hold, in the catalog's order.
func CatalogFor(v Variant, bar Bar) []Item {
	var out []Item
	for _, it := range catalogs[bar] {
		if x, ok := LookupFor(v, bar, it.ID); ok {
			out = append(out, x)
		}
	}
	return out
}

// DefaultFor is the client's default arrangement of bar.
func DefaultFor(v Variant, bar Bar) Layout {
	base := defaults[bar]
	if d, ok := v.Default[bar]; ok && d != nil {
		base = d
	}
	var out Layout
	for _, id := range base {
		if !v.Omits(bar, id) {
			out = append(out, id)
		}
	}
	return out
}

// NormalizeFor makes the client's valid arrangement of bar from a stored
// token list, as Normalize: an empty list is the client's default; what
// the client omits is left out like an unknown id; a fixed item stands
// at the start of its home section in the order of the client's default.
func NormalizeFor(v Variant, bar Bar, stored []string) Layout {
	secs := sections[bar]
	if len(stored) == 0 || len(secs) == 0 {
		return DefaultFor(v, bar)
	}
	if len(stored) > MaxTokens {
		stored = stored[:MaxTokens]
	}
	index := make(map[Section]int, len(secs))
	for i, s := range secs {
		index[s] = i
	}
	parts := make([][]ID, len(secs))
	moved := make([][]ID, len(secs))
	seen := map[ID]bool{}
	cur := 0
	for _, tok := range stored {
		id := ID(tok)
		if IsSeparator(id) {
			if i, ok := index[Section(strings.TrimPrefix(tok, "|"))]; ok && i > cur {
				cur = i
			}
			continue
		}
		if IsSpace(id) {
			parts[cur] = append(parts[cur], id)
			continue
		}
		it, ok := LookupFor(v, bar, id)
		if !ok || seen[id] || it.Fixed {
			continue
		}
		seen[id] = true
		if it.allows(secs[cur]) {
			parts[cur] = append(parts[cur], id)
		} else {
			h := index[it.Home()]
			moved[h] = append(moved[h], id)
		}
	}
	def := DefaultFor(v, bar)
	var out Layout
	for i, s := range secs {
		if i > 0 {
			out = append(out, Separator(s))
		}
		for _, id := range def {
			if it, ok := LookupFor(v, bar, id); ok && it.Fixed && it.Home() == s {
				out = append(out, id)
			}
		}
		out = append(out, parts[i]...)
		out = append(out, moved[i]...)
	}
	return out
}

// CanInsertFor reports whether the user of the client may put id into
// section s of bar. Spaces go anywhere; separators, fixed and omitted
// items never.
func CanInsertFor(v Variant, bar Bar, id ID, s Section) bool {
	if IsSpace(id) {
		return true
	}
	if IsSeparator(id) {
		return false
	}
	it, ok := LookupFor(v, bar, id)
	return ok && !it.Fixed && it.allows(s)
}

// ImmovableFor are the client's items of bar the user can neither move
// nor remove, its separators included.
func ImmovableFor(v Variant, bar Bar) []ID {
	var out []ID
	for _, it := range CatalogFor(v, bar) {
		if it.Fixed {
			out = append(out, it.ID)
		}
	}
	for _, s := range sections[bar][1:] {
		out = append(out, Separator(s))
	}
	return out
}

// AllowedFor is what the client's editor offers: its catalog without the
// items whose feature is missing, in the catalog's order.
func AllowedFor(v Variant, bar Bar, f Features) []ID {
	var out []ID
	for _, it := range CatalogFor(v, bar) {
		if it.Feature == "" || f.Have[it.Feature] {
			out = append(out, it.ID)
		}
	}
	return out
}

// PresentFor is what the client's toolbar holds for l, as Present; an
// item the client omits is left out.
func PresentFor(v Variant, bar Bar, l Layout, f Features) []Shown {
	var out []Shown
	s := SectionMain
	if secs := sections[bar]; len(secs) > 0 {
		s = secs[0]
	}
	for _, id := range l {
		if IsSeparator(id) {
			s = Section(strings.TrimPrefix(string(id), "|"))
			if f.HiddenSections[s] || f.HiddenSeparators[id] {
				continue
			}
			out = append(out, Shown{ID: id})
			continue
		}
		if f.HiddenSections[s] || v.Omits(bar, id) {
			continue
		}
		hidden := false
		if it, ok := Lookup(bar, id); ok && it.Feature != "" {
			hidden = !f.Have[it.Feature]
		}
		out = append(out, Shown{ID: id, Hidden: hidden})
	}
	return out
}

// MissingActionsFor are the actions the client's More Actions menu adds
// because l does not show them, as MissingActions; an omitted action is
// none (the client has no command for it in a toolbar).
func MissingActionsFor(v Variant, bar Bar, l Layout, f Features) []ID {
	shown := map[ID]bool{}
	for _, e := range PresentFor(v, bar, l, f) {
		if !e.Hidden {
			shown[e.ID] = true
		}
	}
	var out []ID
	for _, it := range CatalogFor(v, bar) {
		if it.InMore && !shown[it.ID] && (it.Feature == "" || f.Have[it.Feature]) {
			out = append(out, it.ID)
		}
	}
	return out
}

// StoredFor is what the settings key keeps after the client's user
// arranged bar as l, the key holding stored before (T12): the client's
// arrangement with the items the client omits merged back from stored,
// so another client's choices about them survive. An empty stored list
// stands for Default(bar), the catalog's (macOS's) default, so a first
// edit on GTK keeps the items GTK lacks where the default had them.
//
// The merge: stored is read with the catalog and every client's extra
// sections (so GTK's search in the list section stays there); each item
// of it that the client omits goes after the nearest token before it in
// stored that the result has in the same section (an item or a
// separator; earlier merged items count), else at the end of that
// section. Unknown tokens (of another version)
// go as in Normalize.
//
// The key is emptied (the default, following later versions) only when
// l is the client's default and the omitted items in stored are the
// ones Default(bar) has (their places are given up). For the zero
// Variant this is Stored. Restore Defaults writes the empty list itself.
func StoredFor(v Variant, bar Bar, stored []string, l Layout) []string {
	edited := NormalizeFor(v, bar, l.Tokens())
	base := NormalizeFor(storage(), bar, stored)
	if edited.Equal(DefaultFor(v, bar)) && sameIDs(omitted(v, bar, base), omitted(v, bar, Default(bar))) {
		return []string{}
	}
	out := append(Layout(nil), edited...)
	have := map[ID]bool{}
	for _, id := range out {
		if !IsSpace(id) {
			have[id] = true
		}
	}
	for j, id := range base {
		if IsSpace(id) || IsSeparator(id) || !v.Omits(bar, id) {
			continue
		}
		sec := SectionAt(bar, base, j)
		at := -1
		for k := j - 1; k >= 0; k-- {
			y := base[k]
			if IsSpace(y) || !have[y] {
				continue
			}
			if i := indexOf(out, y); i >= 0 && SectionAt(bar, out, i) == sec {
				at = i + 1
				break
			}
		}
		if at < 0 {
			at = sectionEnd(bar, out, sec)
		}
		out = append(out, "")
		copy(out[at+1:], out[at:])
		out[at] = id
		have[id] = true
	}
	return out.Tokens()
}

// storage is how StoredFor reads the stored list: the catalog with the
// extra sections of every client.
func storage() Variant {
	extra := map[Bar]map[ID][]Section{}
	for _, v := range []Variant{GTK, Windows} {
		for bar, m := range v.Extra {
			if extra[bar] == nil {
				extra[bar] = map[ID][]Section{}
			}
			for id, secs := range m {
				for _, s := range secs {
					dup := false
					for _, x := range extra[bar][id] {
						dup = dup || x == s
					}
					if !dup {
						extra[bar][id] = append(extra[bar][id], s)
					}
				}
			}
		}
	}
	return Variant{Extra: extra}
}

// omitted are the items of l the client omits, in l's order.
func omitted(v Variant, bar Bar, l Layout) []ID {
	var out []ID
	for _, id := range l {
		if !IsSpace(id) && !IsSeparator(id) && v.Omits(bar, id) {
			out = append(out, id)
		}
	}
	return out
}

func sameIDs(a, b []ID) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[ID]bool, len(a))
	for _, id := range a {
		set[id] = true
	}
	for _, id := range b {
		if !set[id] {
			return false
		}
	}
	return true
}

func indexOf(l Layout, id ID) int {
	for i, x := range l {
		if x == id {
			return i
		}
	}
	return -1
}

// sectionEnd is the index just past the last token of section s in l
// (before the separator of the next section), len(l) for the last one.
func sectionEnd(bar Bar, l Layout, s Section) int {
	inside := false
	secs := sections[bar]
	if len(secs) > 0 && secs[0] == s {
		inside = true
	}
	for i, id := range l {
		if IsSeparator(id) {
			if inside {
				return i
			}
			if id == Separator(s) {
				inside = true
			}
		}
	}
	return len(l)
}
