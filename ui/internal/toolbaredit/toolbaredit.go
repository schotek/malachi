// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package toolbaredit is the toolbar editor's pure controller (decision
// T13, docs/plans/toolbar-customization.md §17), shared by the GTK and
// Windows editors in Preferences: the arrangement of one toolbar as
// sections with rows, the items the user may add to a section, moving a
// row by dragging (Drop) or with Ctrl+↑/↓ (MoveUp, MoveDown) only where
// toolbar.CanInsertFor allows, removing a row, adding a space or a
// flexible space, Restore Defaults, and the tokens the client writes to
// the toolbar's key (toolbar.StoredFor, so the items the client lacks
// survive). No widgets: the client draws Sections and calls the
// methods, writes Tokens after each change and calls Reload when the key
// changes (also from elsewhere).
package toolbaredit

import "github.com/schotek/malachi/ui/internal/toolbar"

// MaxTokens bounds what the editor lets the arrangement grow to (spaces
// mostly), below toolbar.MaxTokens so what it writes, with the items
// merged back, is read back whole.
const MaxTokens = toolbar.MaxTokens - 16

// Row is an entry of the editor's list.
type Row struct {
	// Index is the row's place in Layout, the argument of the moves.
	Index int
	ID    toolbar.ID
	// Fixed: the row can neither move nor go (the mode switch).
	Fixed bool
	// Space: a space or a flexible space.
	Space bool
	// Unavailable: its feature is missing, it keeps its place and is
	// hidden in the toolbar (toolbar.UnavailableSubtitle).
	Unavailable bool
	// CanMoveUp and CanMoveDown tell whether MoveUp and MoveDown move it.
	CanMoveUp, CanMoveDown bool
}

// Section is a section of the toolbar with its rows.
type Section struct {
	Section toolbar.Section
	Rows    []Row
}

// Editor edits one toolbar of one client.
type Editor struct {
	v      toolbar.Variant
	bar    toolbar.Bar
	f      toolbar.Features
	stored []string
	layout toolbar.Layout
}

// New is an editor of bar for the client v over the key's stored tokens
// and the features at hand.
func New(v toolbar.Variant, bar toolbar.Bar, stored []string, f toolbar.Features) *Editor {
	e := &Editor{v: v, bar: bar, f: f}
	e.Reload(stored)
	return e
}

// Bar is the toolbar edited.
func (e *Editor) Bar() toolbar.Bar { return e.bar }

// Reload reads the key again (after the client wrote Tokens, or a change
// from elsewhere).
func (e *Editor) Reload(stored []string) {
	e.stored = append([]string(nil), stored...)
	e.layout = toolbar.NormalizeFor(e.v, e.bar, stored)
}

// SetFeatures changes the features the rows are marked and the items
// offered by.
func (e *Editor) SetFeatures(f toolbar.Features) { e.f = f }

// Layout is the arrangement as the client's toolbar has it.
func (e *Editor) Layout() toolbar.Layout { return append(toolbar.Layout(nil), e.layout...) }

// Tokens are what the client writes to toolbar.Key(Bar()) for the
// arrangement.
func (e *Editor) Tokens() []string { return toolbar.StoredFor(e.v, e.bar, e.stored, e.layout) }

// Sections are the toolbar's sections with their rows (the separators
// are the sections' headings, not rows).
func (e *Editor) Sections() []Section {
	var out []Section
	for _, s := range toolbar.Sections(e.bar) {
		out = append(out, Section{Section: s})
	}
	cur := 0
	for i, id := range e.layout {
		if toolbar.IsSeparator(id) {
			s := toolbar.SectionAt(e.bar, e.layout, i)
			for k := range out {
				if out[k].Section == s {
					cur = k
				}
			}
			continue
		}
		r := Row{Index: i, ID: id, Space: toolbar.IsSpace(id)}
		if it, ok := toolbar.LookupFor(e.v, e.bar, id); ok {
			r.Fixed = it.Fixed
			r.Unavailable = it.Feature != "" && !e.f.Have[it.Feature]
		}
		_, r.CanMoveUp = e.target(i, -1)
		_, r.CanMoveDown = e.target(i, +1)
		out[cur].Rows = append(out[cur].Rows, r)
	}
	return out
}

// Available are the items the user may add to section s: what the
// client offers (toolbar.AllowedFor) that the toolbar does not have and
// may stand in s, in the catalog's order. The spaces are always
// addable (AddSpace).
func (e *Editor) Available(s toolbar.Section) []toolbar.ID {
	have := map[toolbar.ID]bool{}
	for _, id := range e.layout {
		have[id] = true
	}
	var out []toolbar.ID
	for _, id := range toolbar.AllowedFor(e.v, e.bar, e.f) {
		if !have[id] && toolbar.CanInsertFor(e.v, e.bar, id, s) {
			out = append(out, id)
		}
	}
	return out
}

// Add puts id at the end of section s; false when it may not stand
// there, is there already or the toolbar is full.
func (e *Editor) Add(id toolbar.ID, s toolbar.Section) bool {
	if toolbar.IsSpace(id) {
		return e.insert(id, e.sectionEnd(s))
	}
	for _, x := range e.Available(s) {
		if x == id {
			return e.insert(id, e.sectionEnd(s))
		}
	}
	return false
}

// AddSpace puts a space or a flexible space after the row at index i
// (at the start of the section, after its fixed items, when i is a
// separator, -1 for the first section).
func (e *Editor) AddSpace(kind toolbar.ID, i int) bool {
	if !toolbar.IsSpace(kind) || i < -1 || i >= len(e.layout) {
		return false
	}
	at := i + 1
	for at < len(e.layout) && e.fixed(e.layout[at]) {
		at++
	}
	return e.insert(kind, at)
}

// Remove takes the row at index i off the toolbar; false for a fixed
// item or a separator.
func (e *Editor) Remove(i int) bool {
	if i < 0 || i >= len(e.layout) {
		return false
	}
	id := e.layout[i]
	if toolbar.IsSeparator(id) || e.fixed(id) {
		return false
	}
	e.layout = append(e.layout[:i:i], e.layout[i+1:]...)
	e.normalize()
	return true
}

// MoveUp moves the row at index i one place up (Ctrl+↑): past the row
// above, or past the section's heading to the end of the section above
// when the item may stand there. It returns the row's new index.
func (e *Editor) MoveUp(i int) (int, bool) { return e.step(i, -1) }

// MoveDown moves the row at index i one place down (Ctrl+↓).
func (e *Editor) MoveDown(i int) (int, bool) { return e.step(i, +1) }

func (e *Editor) step(i, d int) (int, bool) {
	to, ok := e.target(i, d)
	if !ok {
		return i, false
	}
	return e.move(i, to), true
}

// target is the insertion index (in the layout without the row) that a
// step of the row at i in direction d gives.
func (e *Editor) target(i, d int) (int, bool) {
	if i < 0 || i >= len(e.layout) {
		return 0, false
	}
	id := e.layout[i]
	if toolbar.IsSeparator(id) || e.fixed(id) {
		return 0, false
	}
	// Without the row, the row above is at i-1 and the row below at i.
	to := i - 1
	if d > 0 {
		to = i + 1
	}
	if to < 0 || to > len(e.layout)-1 {
		return 0, false
	}
	if !e.CanDrop(i, to) {
		return 0, false
	}
	return to, true
}

// CanDrop reports whether the row at index from may be dropped at
// insertion index to, counted in the layout without that row (to = 0
// before the first token, len(Layout())-1 after the last).
func (e *Editor) CanDrop(from, to int) bool {
	if from < 0 || from >= len(e.layout) {
		return false
	}
	id := e.layout[from]
	if toolbar.IsSeparator(id) || e.fixed(id) {
		return false
	}
	rest := append(append(toolbar.Layout(nil), e.layout[:from]...), e.layout[from+1:]...)
	if to < 0 || to > len(rest) {
		return false
	}
	return e.canStand(rest, id, to)
}

// Drop moves the row at index from to insertion index to (as CanDrop)
// and returns its new index; a drop where it may not stand leaves the
// row where it was.
func (e *Editor) Drop(from, to int) (int, bool) {
	if !e.CanDrop(from, to) {
		return from, false
	}
	return e.move(from, to), true
}

// DropAtEnd is the insertion index (as CanDrop and Drop count it) at the
// end of section s for the row at index from: where a row dropped on the
// section's last line (its Add Item… row) lands; -1 for a section the
// toolbar does not have.
func (e *Editor) DropAtEnd(from int, s toolbar.Section) int {
	end := e.sectionEnd(s)
	if end < 0 {
		return -1
	}
	if from >= 0 && from < end {
		end--
	}
	return end
}

// Reset is Restore Defaults: the client's default arrangement and the
// empty list to write (another client's items in the key go
// too).
func (e *Editor) Reset() []string {
	e.stored = []string{}
	e.layout = toolbar.DefaultFor(e.v, e.bar)
	return []string{}
}

// IsDefault reports whether the arrangement is the client's default (to
// grey Restore Defaults out).
func (e *Editor) IsDefault() bool {
	return len(e.stored) == 0 && e.layout.Equal(toolbar.DefaultFor(e.v, e.bar))
}

func (e *Editor) fixed(id toolbar.ID) bool {
	it, ok := toolbar.LookupFor(e.v, e.bar, id)
	return ok && it.Fixed
}

// canStand reports whether id may be inserted into l at index at: the
// section there allows it and no fixed item would stand after it in
// that section.
func (e *Editor) canStand(l toolbar.Layout, id toolbar.ID, at int) bool {
	sec := toolbar.Sections(e.bar)[0]
	if at > 0 {
		sec = toolbar.SectionAt(e.bar, l, at-1)
	}
	if !toolbar.CanInsertFor(e.v, e.bar, id, sec) {
		return false
	}
	return at >= len(l) || toolbar.IsSeparator(l[at]) || !e.fixed(l[at])
}

func (e *Editor) insert(id toolbar.ID, at int) bool {
	if len(e.layout) >= MaxTokens || at < 0 || at > len(e.layout) || !e.canStand(e.layout, id, at) {
		return false
	}
	l := append(toolbar.Layout(nil), e.layout[:at]...)
	l = append(l, id)
	e.layout = append(l, e.layout[at:]...)
	e.normalize()
	return true
}

func (e *Editor) move(from, to int) int {
	id := e.layout[from]
	rest := append(append(toolbar.Layout(nil), e.layout[:from]...), e.layout[from+1:]...)
	l := append(toolbar.Layout(nil), rest[:to]...)
	l = append(l, id)
	e.layout = append(l, rest[to:]...)
	e.normalize()
	if toolbar.IsSpace(id) {
		return to
	}
	for i, x := range e.layout {
		if x == id {
			return i
		}
	}
	return to
}

// sectionEnd is the insertion index at the end of section s.
func (e *Editor) sectionEnd(s toolbar.Section) int {
	inside := toolbar.Sections(e.bar)[0] == s
	for i, id := range e.layout {
		if toolbar.IsSeparator(id) {
			if inside {
				return i
			}
			inside = id == toolbar.Separator(s)
		}
	}
	if inside {
		return len(e.layout)
	}
	return -1
}

// normalize keeps the arrangement what the client reads back; every
// change above is valid, so this changes nothing but guards the rules.
func (e *Editor) normalize() {
	e.layout = toolbar.NormalizeFor(e.v, e.bar, e.layout.Tokens())
}
