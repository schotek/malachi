// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
)

// AttendeeField is the event editor's Attendees row (docs/calendar.md
// §5.6): the compose window's recipient field (recipient_field.go, badges
// with ×, invalid ones red, the ui/internal/recipients rules) and its
// contact.search suggestions (suggest.go), unchanged, behind an exported
// face for the calendar window. The field keeps its own recipients.Tokens;
// the editor reads Resolved after every change (the same way the macOS
// editor feeds its controller).
type AttendeeField struct {
	f *recipientField
	s *suggestions
}

// AttendeeOptions are what the field needs from its window.
type AttendeeOptions struct {
	// RemoveText is the tooltip of a badge's × ("Remove").
	RemoveText string
	// Search runs contact.search for a token and answers on the main loop
	// (nil: no suggestions).
	Search func(query string, done func([]api.Contact, error))
	// Active reports whether the window is active: a focus loss while it is
	// in the background does not commit the typed text. Nil is true.
	Active func() bool
	// OnChanged reports a change of the value (a badge added or removed,
	// text typed).
	OnChanged func()
}

// NewAttendeeField builds the field into the builder's scrolled wrap box
// of the row; label names the entry.
func NewAttendeeField(scroll *gtk.ScrolledWindow, row *adw.WrapBox, label *gtk.Label, o AttendeeOptions) *AttendeeField {
	f := newRecipientField(scroll, row, label, o.RemoveText)
	a := &AttendeeField{f: f}
	if o.Search != nil {
		a.s = newFieldSuggestions(f, &suggestions{find: o.Search})
	}
	f.skipCommit = func() bool {
		if o.Active != nil && !o.Active() {
			return true
		}
		return a.s != nil && a.s.hover
	}
	f.changed = func() {
		if o.OnChanged != nil {
			o.OnChanged()
		}
	}
	f.typed = func() {
		if a.s != nil {
			a.s.onChanged()
		}
	}
	f.wireKeys() // after the completion's keys: they come first
	return a
}

// SetAddresses replaces the value with parsed addresses (not an edit).
func (a *AttendeeField) SetAddresses(list []api.Address) { a.f.SetAddresses(list) }

// Resolved is the attendees as if the typed text were committed, and the
// entries that are not addresses.
func (a *AttendeeField) Resolved() ([]api.Address, []string) { return a.f.resolved() }

// CommitPending makes the typed text a badge (before Save, so that an
// invalid entry shows red).
func (a *AttendeeField) CommitPending() { a.f.commitPending() }

// Entry is the field's text entry (focus, tests).
func (a *AttendeeField) Entry() *gtk.Entry { return a.f.entry }

// SuggestionsShown reports the suggestion popover on screen (Escape
// closes it before the window).
func (a *AttendeeField) SuggestionsShown() bool { return a.s != nil && a.s.popover.Visible() }

// Close stops the field; the window is going away.
func (a *AttendeeField) Close() {
	if a.s != nil {
		a.s.hide()
	}
	a.f.close()
}
