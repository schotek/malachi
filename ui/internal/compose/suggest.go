// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"strings"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// Recipient completion: a popover under each recipient row offering what
// contact.search returns for the address token under the caret. The
// backend ranks and merges; this file finds the token, asks, and inserts
// the answer. The popover never takes the focus, so typing goes on in the
// row; the arrow keys, Return, Tab and Escape are read off the row itself.

const (
	// suggestMinChars is how many characters a token needs before the
	// backend is asked: a directory book searches on its server.
	suggestMinChars = 2
	// suggestDebounce is how long typing must pause before a search, in
	// milliseconds.
	suggestDebounce = 150
	// suggestLimit is how many suggestions are asked for and shown.
	suggestLimit = 8
)

// suggestions is the completion state of one recipient row.
type suggestions struct {
	w       *Window
	entry   *gtk.Entry
	popover *gtk.Popover
	list    *gtk.ListBox

	contacts []api.Contact // what the rows show, in order
	gen      uint64        // guards replies of a search the text has outrun
	timer    glib.SourceHandle
	suppress bool // accept's SetText must not start a search of its own
}

// newSuggestions attaches completion to a recipient row.
func newSuggestions(w *Window, entry *gtk.Entry) *suggestions {
	s := &suggestions{w: w, entry: entry}
	s.popover = gtk.NewPopover()
	s.popover.SetParent(entry)
	s.popover.SetAutohide(false)
	s.popover.SetHasArrow(false)
	s.popover.SetPosition(gtk.PosBottom)
	s.popover.SetCanFocus(false)
	s.popover.AddCSSClass("recipient-suggestions")

	s.list = gtk.NewListBox()
	s.list.SetSelectionMode(gtk.SelectionSingle)
	s.list.SetActivateOnSingleClick(true)
	s.list.SetCanFocus(false)
	s.list.ConnectRowActivated(func(row *gtk.ListBoxRow) { s.accept(row.Index()) })
	s.popover.SetChild(s.list)

	// Capture phase, so the row's own handling of Return and Tab (and
	// the window's bubble-phase Escape) see the keys only when the
	// popover is not there to take them.
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(s.onKey)
	entry.AddController(keys)
	focus := gtk.NewEventControllerFocus()
	focus.ConnectLeave(s.hide)
	entry.AddController(focus)
	return s
}

// onChanged runs on every edit of the row: it finds the token under the
// caret and, after a pause in typing, asks for suggestions.
func (s *suggestions) onChanged() {
	if s.suppress {
		return
	}
	s.cancelTimer()
	_, _, token := tokenAt(s.entry.Text(), s.entry.Position())
	if utf8.RuneCountInString(token) < suggestMinChars {
		s.hide()
		return
	}
	s.timer = glib.TimeoutAdd(suggestDebounce, func() bool {
		s.timer = 0
		s.search(token)
		return false
	})
}

// search asks the backend for token and shows the answer, unless the row
// has moved on meanwhile. A failure is logged, never shown: completion
// is a convenience, and the row still takes what is typed.
func (s *suggestions) search(token string) {
	s.gen++
	gen := s.gen
	account := s.w.account().ID
	s.w.rpc(func() (any, error) {
		var res api.ContactSearchResult
		err := s.w.m.client.Call(s.w.ctx(), api.MethodContactSearch,
			api.ContactSearchParams{AccountID: account, Query: token, Limit: suggestLimit}, &res)
		return res, err
	}, func(v any, err error) {
		if gen != s.gen {
			return
		}
		if err != nil {
			s.w.log.Debug("contact.search", "err", err)
			s.hide()
			return
		}
		if _, _, now := tokenAt(s.entry.Text(), s.entry.Position()); now != token {
			return
		}
		s.show(v.(api.ContactSearchResult).Contacts)
	})
}

// show fills the popover and pops it up the width of the row, with the
// first suggestion selected so Return takes it at once.
func (s *suggestions) show(contacts []api.Contact) {
	s.list.RemoveAll()
	s.contacts = contacts
	if len(contacts) == 0 {
		s.hide()
		return
	}
	for _, c := range contacts {
		s.list.Append(suggestionRow(c))
	}
	s.list.SelectRow(s.list.RowAtIndex(0))
	width, height := s.entry.AllocatedWidth(), s.entry.AllocatedHeight()
	rect := gdk.NewRectangle(0, 0, width, height)
	s.popover.SetPointingTo(&rect)
	s.popover.SetSizeRequest(width, -1)
	if !s.popover.Visible() {
		s.popover.Popup()
	}
}

// hide closes the popover and forgets any search in flight.
func (s *suggestions) hide() {
	s.cancelTimer()
	s.gen++
	if s.popover.Visible() {
		s.popover.Popdown()
	}
}

func (s *suggestions) cancelTimer() {
	if s.timer != 0 {
		glib.SourceRemove(s.timer)
		s.timer = 0
	}
}

// onKey drives the popover from the row while it is shown; everything
// else, and every key while it is hidden, goes on to the row.
func (s *suggestions) onKey(keyval, _ uint, state gdk.ModifierType) bool {
	if !s.popover.Visible() || state&(gdk.ControlMask|gdk.AltMask) != 0 {
		return false
	}
	switch keyval {
	case gdk.KEY_Down:
		s.move(1)
		return true
	case gdk.KEY_Up:
		s.move(-1)
		return true
	case gdk.KEY_Return, gdk.KEY_KP_Enter, gdk.KEY_Tab:
		if row := s.list.SelectedRow(); row != nil {
			s.accept(row.Index())
			return true
		}
		return false
	case gdk.KEY_Escape:
		s.hide()
		return true
	}
	return false
}

// move steps the selection, wrapping around.
func (s *suggestions) move(delta int) {
	n := len(s.contacts)
	if n == 0 {
		return
	}
	i := 0
	if row := s.list.SelectedRow(); row != nil {
		i = row.Index()
	}
	i = ((i+delta)%n + n) % n
	s.list.SelectRow(s.list.RowAtIndex(i))
}

// accept replaces the token under the caret with the chosen contact and
// leaves the caret after the separator, ready for the next recipient.
func (s *suggestions) accept(i int) {
	if i < 0 || i >= len(s.contacts) {
		return
	}
	c := s.contacts[i]
	text := s.entry.Text()
	start, end, _ := tokenAt(text, s.entry.Position())
	newText, caret := replaceToken(text, start, end, api.Address{Name: c.Name, Address: c.Address})
	s.hide()
	s.suppress = true
	s.entry.SetText(newText)
	s.entry.SetPosition(caret)
	s.suppress = false
}

// suggestionRow builds one row: the source icon, the name over the
// address (or the address alone). Labels never interpret markup.
func suggestionRow(c api.Contact) *gtk.ListBoxRow {
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	icon := gtk.NewImageFromIconName(suggestionIcon(c))
	icon.SetVAlign(gtk.AlignCenter)
	box.Append(icon)

	text := gtk.NewBox(gtk.OrientationVertical, 0)
	text.SetHExpand(true)
	primary := gtk.NewLabel(c.Address)
	if c.Name != "" {
		primary.SetLabel(c.Name)
	}
	primary.SetUseMarkup(false)
	primary.SetXAlign(0)
	primary.SetEllipsize(pango.EllipsizeEnd)
	text.Append(primary)
	if c.Name != "" {
		secondary := gtk.NewLabel(c.Address)
		secondary.SetUseMarkup(false)
		secondary.SetXAlign(0)
		secondary.SetEllipsize(pango.EllipsizeEnd)
		secondary.AddCSSClass("dim-label")
		secondary.AddCSSClass("caption")
		text.Append(secondary)
	}
	box.Append(text)

	row := gtk.NewListBoxRow()
	row.SetChild(box)
	row.SetCanFocus(false)
	row.SetTooltipText(suggestionTooltip(c))
	return row
}

// suggestionIcon names the icon for a suggestion's source.
func suggestionIcon(c api.Contact) string {
	if c.Source == api.ContactSourceAddressBook {
		return "x-office-address-book-symbolic"
	}
	return "document-open-recent-symbolic"
}

// suggestionTooltip says where a suggestion comes from: the address
// book's own name when it has one.
func suggestionTooltip(c api.Contact) string {
	if c.Source == api.ContactSourceAddressBook {
		if c.Book != "" {
			return c.Book
		}
		// TRANSLATORS: tooltip of a recipient suggestion that came from a
		// system address book whose name is unknown.
		return i18n.T("Address book")
	}
	// TRANSLATORS: tooltip of a recipient suggestion that is an address
	// the user has written to before.
	return i18n.T("Recently used")
}

// tokenAt finds the address token the caret is in: the run between the
// separators splitAddressRanges honours, without the spaces around it.
// caret is a character offset (what gtk.Editable reports); start and end
// are byte offsets into text. A caret past the text is the last token.
func tokenAt(text string, caret int) (start, end int, token string) {
	pos := byteOffset(text, caret)
	for _, r := range splitAddressRanges(text) {
		if pos < r.start || pos > r.end {
			continue
		}
		s, e := r.start, r.end
		for s < e && (text[s] == ' ' || text[s] == '\t') {
			s++
		}
		for e > s && (text[e-1] == ' ' || text[e-1] == '\t') {
			e--
		}
		return s, e, text[s:e]
	}
	return len(text), len(text), ""
}

// replaceToken swaps text[start:end] for the formatted address. A
// separator and a space follow unless one is already there (spaces the
// token had before it are dropped), and the caret (a character offset)
// lands after the inserted address.
func replaceToken(text string, start, end int, a api.Address) (string, int) {
	insert := FormatAddressList([]api.Address{a})
	rest := strings.TrimLeft(text[end:], " \t")
	if !strings.HasPrefix(rest, ",") && !strings.HasPrefix(rest, ";") {
		insert += ", "
	}
	head := text[:start] + insert
	return head + rest, utf8.RuneCountInString(head)
}

// byteOffset converts a character offset to a byte offset, clamped to the
// text.
func byteOffset(text string, chars int) int {
	if chars <= 0 {
		return 0
	}
	i := 0
	for n := 0; n < chars; n++ {
		if i >= len(text) {
			return len(text)
		}
		_, size := utf8.DecodeRuneInString(text[i:])
		i += size
	}
	return i
}
