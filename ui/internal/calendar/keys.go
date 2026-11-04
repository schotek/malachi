// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"cmp"
	"slices"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
)

// The calendar's keys, the part that needs no widgets: which shortcuts
// there are, with their texts for the shortcuts window and the menus,
// what a key does in which context, where Escape goes, and how the arrows
// move the selection. Every client binds the same keys with its own
// modifiers: Primary is ⌘ on macOS and Ctrl on Linux and Windows; Alt is
// ⌥ on macOS and Shift elsewhere; Control is ⌃ on macOS and Shift
// elsewhere (Ctrl+Alt is AltGr on Windows and on many Linux layouts).

// KeyAction is what a key of the calendar does.
type KeyAction int

// The actions.
const (
	// KeyShowCalendar switches the main window to the Calendar
	// (Primary+3, in every mode; Primary+1 and 2 are board.KeyFor's).
	KeyShowCalendar KeyAction = iota
	// KeyToday shows today (Primary+T).
	KeyToday
	// KeyPrevious and KeyNext are ‹ and › (Primary+← and →).
	KeyPrevious
	KeyNext
	// KeyDayView … KeyAgendaView switch the view (Primary+Control+1–4).
	KeyDayView
	KeyWeekView
	KeyMonthView
	KeyAgendaView
	// KeyNewEvent opens the editor (Primary+Alt+N; Primary+N stays New
	// Message).
	KeyNewEvent
	// KeyOpenDetail opens the selected event's detail (Return).
	KeyOpenDetail
	// KeyRemove deletes or removes the selected event, with its question
	// (⌫ / Delete).
	KeyRemove
	// KeyEscape is Escape (EscapeFor says what it does).
	KeyEscape
	// KeySelectPrevious and KeySelectNext move the selection among the
	// occurrences in time order (← ↑ and → ↓).
	KeySelectPrevious
	KeySelectNext
)

// Special is a key without a character.
type Special int

// The special keys.
const (
	SpecialNone Special = iota
	SpecialLeft
	SpecialRight
	SpecialUp
	SpecialDown
	SpecialReturn
	// SpecialDelete is ⌫ on macOS, Delete (and Backspace) elsewhere.
	SpecialDelete
	SpecialEscape
)

// Key is a shortcut of the calendar.
type Key struct {
	Action KeyAction
	// Rune is the key, lower case ('t', 'n', '1'…'4'), or 0 with Special.
	Rune    rune
	Special Special
	// The modifiers (see the package's note on what they are where).
	Primary, Alt, Control bool
	// CalendarOnly: only in Calendar mode. Mode keys work everywhere.
	CalendarOnly bool
	// OutsideText: not while the keyboard is in a text field (the search
	// field, an editor).
	OutsideText bool
	// Title is the shortcut's text in the shortcuts window and menus.
	Title string
}

// Press is a key as a client received it. For the digits the client
// passes the physical number-row key (board.KeyFor's rule). macOS passes
// ⌥ as Alt and ⌃ as Control; Linux and Windows set ShiftModifier and pass
// Shift, which then stands for a key's Alt or Control (their own Alt
// key is never a calendar modifier: Alt and Control stay false).
type Press struct {
	Rune                         rune
	Special                      Special
	Primary, Alt, Control, Shift bool
	// ShiftModifier: Shift stands for Alt and Control (Linux, Windows).
	ShiftModifier bool
}

// matches reports a press of k's key with k's modifiers.
func (p Press) matches(k Key, r rune) bool {
	if k.Rune != r || k.Special != p.Special || k.Primary != p.Primary {
		return false
	}
	if p.ShiftModifier {
		return !p.Alt && !p.Control && (k.Alt || k.Control) == p.Shift
	}
	return !p.Shift && k.Alt == p.Alt && k.Control == p.Control
}

// KeyContext is the state a key acts in.
type KeyContext struct {
	Mode board.Mode
	// InText: the keyboard is in a text field.
	InText bool
	// Selected: an occurrence is selected.
	Selected bool
}

// CalendarKeys are the calendar's shortcuts in the shortcuts window's
// order.
func CalendarKeys(tr Translator) []Key {
	return []Key{
		{Action: KeyShowCalendar, Rune: '3', Primary: true, Title: ShowCalendarKey(tr)},
		{Action: KeyToday, Rune: 't', Primary: true, CalendarOnly: true, Title: Today(tr)},
		{Action: KeyPrevious, Special: SpecialLeft, Primary: true, CalendarOnly: true, Title: Previous(tr)},
		{Action: KeyNext, Special: SpecialRight, Primary: true, CalendarOnly: true, Title: Next(tr)},
		{Action: KeyDayView, Rune: '1', Primary: true, Control: true, CalendarOnly: true, Title: ViewTitle(ViewDay, tr)},
		{Action: KeyWeekView, Rune: '2', Primary: true, Control: true, CalendarOnly: true, Title: ViewTitle(ViewWeek, tr)},
		{Action: KeyMonthView, Rune: '3', Primary: true, Control: true, CalendarOnly: true, Title: ViewTitle(ViewMonth, tr)},
		{Action: KeyAgendaView, Rune: '4', Primary: true, Control: true, CalendarOnly: true, Title: ViewTitle(ViewAgenda, tr)},
		{Action: KeyNewEvent, Rune: 'n', Primary: true, Alt: true, Title: NewEvent(tr)},
		{Action: KeyOpenDetail, Special: SpecialReturn, CalendarOnly: true, OutsideText: true, Title: OpenDetailKey(tr)},
		{Action: KeyRemove, Special: SpecialDelete, CalendarOnly: true, OutsideText: true, Title: RemoveKey(tr)},
		{Action: KeyEscape, Special: SpecialEscape, CalendarOnly: true},
		{Action: KeySelectPrevious, Special: SpecialLeft, CalendarOnly: true, OutsideText: true, Title: SelectPreviousKey(tr)},
		{Action: KeySelectPrevious, Special: SpecialUp, CalendarOnly: true, OutsideText: true, Title: SelectPreviousKey(tr)},
		{Action: KeySelectNext, Special: SpecialRight, CalendarOnly: true, OutsideText: true, Title: SelectNextKey(tr)},
		{Action: KeySelectNext, Special: SpecialDown, CalendarOnly: true, OutsideText: true, Title: SelectNextKey(tr)},
	}
}

// KeysGroup heads the calendar's group in the shortcuts window.
func KeysGroup(tr Translator) string { return ModeName(tr) }

// ShowCalendarKey is the shortcuts window's text of Primary+3.
func ShowCalendarKey(tr Translator) string {
	// TRANSLATORS: the shortcuts window: the key that shows the calendar in
	// the main window.
	return tr.T("Show Calendar")
}

// OpenDetailKey is the shortcuts window's text of Return.
func OpenDetailKey(tr Translator) string { return tr.T("Show Event Details") }

// RemoveKey is the shortcuts window's text of ⌫.
func RemoveKey(tr Translator) string { return tr.T("Delete or Remove Event") }

// SelectPreviousKey is the shortcuts window's text of ← and ↑.
func SelectPreviousKey(tr Translator) string { return tr.T("Select Previous Event") }

// SelectNextKey is the shortcuts window's text of → and ↓.
func SelectNextKey(tr Translator) string { return tr.T("Select Next Event") }

// needsSelection are the actions that act on the selected occurrence.
func needsSelection(a KeyAction) bool { return a == KeyOpenDetail || a == KeyRemove }

// KeyFor is the action of a key press in ctx; false when the key is not
// the calendar's there. A key that acts on the selection needs one.
func KeyFor(p Press, ctx KeyContext, tr Translator) (KeyAction, bool) {
	r := p.Rune
	if r >= 'A' && r <= 'Z' {
		r += 'a' - 'A'
	}
	for _, k := range CalendarKeys(tr) {
		if !p.matches(k, r) {
			continue
		}
		if k.CalendarOnly && ctx.Mode != board.ModeCalendar || k.OutsideText && ctx.InText ||
			needsSelection(k.Action) && !ctx.Selected {
			return 0, false
		}
		return k.Action, true
	}
	return 0, false
}

// EscapeTarget is what Escape does in Calendar mode.
type EscapeTarget int

// The targets.
const (
	// EscapeNothing: nothing is open.
	EscapeNothing EscapeTarget = iota
	// EscapeClosePopover closes the detail popover (or another popup).
	EscapeClosePopover
	// EscapeClearSearch clears the search field and returns to the view.
	EscapeClearSearch
)

// EscapeFor is where Escape goes: an open popover closes first, then the
// search is cleared.
func EscapeFor(popoverOpen, searching bool) EscapeTarget {
	switch {
	case popoverOpen:
		return EscapeClosePopover
	case searching:
		return EscapeClearSearch
	}
	return EscapeNothing
}

// SelectionOrder is the order the arrows walk: the shown occurrences'
// keys, all-day first on their day, by start, then end, then Key;
// duplicates (an occurrence in two cells) once.
func SelectionOrder(occurrences []api.Occurrence, loc *time.Location) []string {
	type entry struct {
		key        string
		day        time.Time
		allDay     bool
		start, end time.Time
	}
	var es []entry
	seen := map[string]bool{}
	for _, o := range occurrences {
		k := OccurrenceKey(o)
		s, e, ok := Span(o, loc)
		if !ok || seen[k] {
			continue
		}
		seen[k] = true
		es = append(es, entry{k, StartOfDay(s, loc), o.AllDay, s, e})
	}
	slices.SortStableFunc(es, func(a, b entry) int {
		if c := a.day.Compare(b.day); c != 0 {
			return c
		}
		if a.allDay != b.allDay {
			if a.allDay {
				return -1
			}
			return 1
		}
		return cmp.Or(a.start.Compare(b.start), a.end.Compare(b.end), cmp.Compare(a.key, b.key))
	})
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.key
	}
	return out
}

// MoveSelection is the key after current in order by delta (±1),
// clamped to the ends; with no current (or one not in order) the first
// for a step forward and the last for a step back; "" for an empty
// order.
func MoveSelection(order []string, current string, delta int) string {
	if len(order) == 0 {
		return ""
	}
	i := slices.Index(order, current)
	if i < 0 {
		if delta < 0 {
			return order[len(order)-1]
		}
		return order[0]
	}
	return order[min(max(i+delta, 0), len(order)-1)]
}
