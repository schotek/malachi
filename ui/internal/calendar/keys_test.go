// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
)

func TestKeyFor(t *testing.T) {
	cal := KeyContext{Mode: board.ModeCalendar}
	sel := KeyContext{Mode: board.ModeCalendar, Selected: true}
	mail := KeyContext{Mode: board.ModeMail}
	tests := []struct {
		name string
		p    Press
		ctx  KeyContext
		want KeyAction
		ok   bool
	}{
		{"⌘3 in Mail", Press{Rune: '3', Primary: true}, mail, KeyShowCalendar, true},
		{"⌘T", Press{Rune: 't', Primary: true}, cal, KeyToday, true},
		{"⌘T upper case", Press{Rune: 'T', Primary: true}, cal, KeyToday, true},
		{"⌘T in Mail", Press{Rune: 't', Primary: true}, mail, 0, false},
		{"⌘←", Press{Special: SpecialLeft, Primary: true}, cal, KeyPrevious, true},
		{"⌘→", Press{Special: SpecialRight, Primary: true}, cal, KeyNext, true},
		{"⌘→ in the search field", Press{Special: SpecialRight, Primary: true}, KeyContext{Mode: board.ModeCalendar, InText: true}, KeyNext, true},
		{"⌃⌘1", Press{Rune: '1', Primary: true, Control: true}, cal, KeyDayView, true},
		{"⌃⌘2", Press{Rune: '2', Primary: true, Control: true}, cal, KeyWeekView, true},
		{"⌃⌘3", Press{Rune: '3', Primary: true, Control: true}, cal, KeyMonthView, true},
		{"⌃⌘4", Press{Rune: '4', Primary: true, Control: true}, cal, KeyAgendaView, true},
		{"⌃⌘4 in Mail", Press{Rune: '4', Primary: true, Control: true}, mail, 0, false},
		{"Ctrl+Shift+3 elsewhere", Press{Rune: '3', Primary: true, Shift: true, ShiftModifier: true}, cal, KeyMonthView, true},
		{"⇧⌘3 on macOS", Press{Rune: '3', Primary: true, Shift: true}, cal, 0, false},
		{"⌥⌘N", Press{Rune: 'n', Primary: true, Alt: true}, mail, KeyNewEvent, true},
		{"Ctrl+Shift+N elsewhere", Press{Rune: 'n', Primary: true, Shift: true, ShiftModifier: true}, mail, KeyNewEvent, true},
		{"Ctrl+Alt+N elsewhere", Press{Rune: 'n', Primary: true, Alt: true, ShiftModifier: true}, mail, 0, false},
		{"⌘N is New Message", Press{Rune: 'n', Primary: true}, cal, 0, false},
		{"Return with a selection", Press{Special: SpecialReturn}, sel, KeyOpenDetail, true},
		{"Return without one", Press{Special: SpecialReturn}, cal, 0, false},
		{"⌫ with a selection", Press{Special: SpecialDelete}, sel, KeyRemove, true},
		{"⌫ in text", Press{Special: SpecialDelete}, KeyContext{Mode: board.ModeCalendar, InText: true, Selected: true}, 0, false},
		{"Escape", Press{Special: SpecialEscape}, cal, KeyEscape, true},
		{"↑", Press{Special: SpecialUp}, cal, KeySelectPrevious, true},
		{"←", Press{Special: SpecialLeft}, cal, KeySelectPrevious, true},
		{"↓", Press{Special: SpecialDown}, cal, KeySelectNext, true},
		{"→ in text", Press{Special: SpecialRight}, KeyContext{Mode: board.ModeCalendar, InText: true}, 0, false},
		{"x", Press{Rune: 'x'}, cal, 0, false},
	}
	for _, tt := range tests {
		got, ok := KeyFor(tt.p, tt.ctx, en{})
		if ok != tt.ok || ok && got != tt.want {
			t.Errorf("%s: %v %v, want %v %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
	for _, k := range CalendarKeys(en{}) {
		if k.Title == "" && k.Action != KeyEscape {
			t.Errorf("key %v has no title", k.Action)
		}
	}
}

func TestEscapeFor(t *testing.T) {
	tests := []struct {
		popover, search bool
		want            EscapeTarget
	}{{true, true, EscapeClosePopover}, {true, false, EscapeClosePopover}, {false, true, EscapeClearSearch}, {false, false, EscapeNothing}}
	for _, tt := range tests {
		if got := EscapeFor(tt.popover, tt.search); got != tt.want {
			t.Errorf("EscapeFor(%v, %v) = %v", tt.popover, tt.search, got)
		}
	}
}

func TestSelectionOrder(t *testing.T) {
	loc := prague(t)
	occ := []api.Occurrence{
		timed("b", at(loc, 2026, 10, 14, 10, 0), at(loc, 2026, 10, 14, 11, 0)),
		timed("a", at(loc, 2026, 10, 14, 9, 0), at(loc, 2026, 10, 14, 10, 0)),
		allDayOcc("allday", at(loc, 2026, 10, 14, 0, 0), 1),
		timed("c", at(loc, 2026, 10, 13, 18, 0), at(loc, 2026, 10, 13, 19, 0)),
		timed("a", at(loc, 2026, 10, 14, 9, 0), at(loc, 2026, 10, 14, 10, 0)),
		{EventID: "broken"},
	}
	order := SelectionOrder(occ, loc)
	want := []string{"c|", "allday|", "a|", "b|"}
	if !slices.Equal(order, want) {
		t.Errorf("order %v, want %v", order, want)
	}
	moves := []struct {
		cur   string
		delta int
		want  string
	}{{"a|", 1, "b|"}, {"a|", -1, "allday|"}, {"b|", 1, "b|"}, {"c|", -1, "c|"}, {"", 1, "c|"}, {"", -1, "b|"}, {"gone", 1, "c|"}}
	for _, tt := range moves {
		if got := MoveSelection(order, tt.cur, tt.delta); got != tt.want {
			t.Errorf("MoveSelection(%q, %d) = %q, want %q", tt.cur, tt.delta, got, tt.want)
		}
	}
	if MoveSelection(nil, "a", 1) != "" {
		t.Error("an empty order")
	}
	_ = time.Second
}
