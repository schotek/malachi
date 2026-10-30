// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/settings"
)

func TestMessageAccelsAllowed(t *testing.T) {
	cases := []struct {
		mode         board.Mode
		typingAllows bool
		want         bool
	}{
		{board.ModeMail, true, true},
		{board.ModeMail, false, false},
		{board.ModeBoard, true, false},
		{board.ModeBoard, false, false},
		{board.ModeCalendar, true, false},
		{board.ModeCalendar, false, false},
	}
	for _, c := range cases {
		if got := messageAccelsAllowed(c.mode, c.typingAllows); got != c.want {
			t.Errorf("messageAccelsAllowed(%v, %v) = %v, want %v", c.mode, c.typingAllows, got, c.want)
		}
	}
}

func TestUndoAccelAllowed(t *testing.T) {
	cases := []struct {
		mode         board.Mode
		typingAllows bool
		want         bool
	}{
		{board.ModeMail, true, true},
		{board.ModeMail, false, false},
		{board.ModeBoard, true, true},
		{board.ModeBoard, false, false},
		{board.ModeCalendar, true, false},
	}
	for _, c := range cases {
		if got := undoAccelAllowed(c.mode, c.typingAllows); got != c.want {
			t.Errorf("undoAccelAllowed(%v, %v) = %v, want %v", c.mode, c.typingAllows, got, c.want)
		}
	}
}

func TestBoardStatusText(t *testing.T) {
	cases := []struct {
		name             string
		base, triageNote string
		want             string
	}{
		{"no triage note: the base line", "Up to date", "", "Up to date"},
		{"a triage note replaces the base line", "Up to date", "Triaging…", "Triaging…"},
		{"an empty base with a note still shows it", "", "Triaging…", "Triaging…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := boardStatusText(c.base, c.triageNote); got != c.want {
				t.Errorf("boardStatusText(%q, %q) = %q, want %q", c.base, c.triageNote, got, c.want)
			}
		})
	}
}

// The mode switches' toggles are named by the modes' nicks: every mode
// goes through a toggle's name and back.
func TestModeSwitchNamesRoundTrip(t *testing.T) {
	want := []string{"mail", "board", "calendar"}
	for i, m := range board.ModesWithCalendar {
		if m.Nick() != want[i] {
			t.Errorf("mode %d nick = %q, want %q", m, m.Nick(), want[i])
		}
		if got, ok := board.ParseMode(m.Nick()); !ok || got != m {
			t.Errorf("ParseMode(%q) = %v, %v", m.Nick(), got, ok)
		}
	}
	if got, ok := board.ParseMode(""); ok || got != board.ModeMail {
		t.Errorf("ParseMode(\"\") = %v, %v, want Mail, false", got, ok)
	}
}

func TestModeAccels(t *testing.T) {
	got := ModeAccels()
	want := map[string]string{
		"win.show-mail":     "<Control>1",
		"win.show-board":    "<Control>2",
		"win.show-calendar": "<Control>3",
	}
	if len(got) != len(want) {
		t.Fatalf("ModeAccels() = %v, want %v", got, want)
	}
	for a, k := range want {
		if got[a] != k {
			t.Errorf("ModeAccels()[%q] = %q, want %q", a, got[a], k)
		}
	}
}

// Open at Launch lists the Calendar before Last Used, and every value it
// writes is one the settings accept.
func TestBoardStartModeChoices(t *testing.T) {
	got := boardStartModeChoices()
	want := []settings.BoardStartMode{settings.BoardStartMail, settings.BoardStartBoard,
		settings.BoardStartCalendar, settings.BoardStartLast}
	if !slices.Equal(got, want) {
		t.Fatalf("boardStartModeChoices() = %v, want %v", got, want)
	}
	for _, c := range got {
		if !slices.Contains(settings.BoardStartModes, c) {
			t.Errorf("%q is not a value of board-start-mode", c)
		}
	}
}

// The window's start decision (decideStart over board.StartDecision) with
// the Calendar: it needs no daemon preferences, and the user's own switch
// or work in Mail keeps Mail.
func TestStartDecisionCalendar(t *testing.T) {
	cases := []struct {
		start            string
		last             string
		known, sw, acted bool
		want             board.Mode
	}{
		{"calendar", "mail", false, false, false, board.ModeCalendar},
		{"calendar", "board", true, false, false, board.ModeCalendar},
		{"last", "calendar", false, false, false, board.ModeCalendar},
		{"calendar", "mail", false, true, false, board.ModeMail},
		{"last", "calendar", false, false, true, board.ModeMail},
	}
	for _, c := range cases {
		last, _ := board.ParseMode(c.last)
		mode, decided := board.StartDecision(board.ParseStartChoice(c.start), last, c.known, true, c.sw, c.acted, time.Millisecond)
		if !decided || mode != c.want {
			t.Errorf("start %q last %q: (%v, %v), want (%v, true)", c.start, c.last, mode, decided, c.want)
		}
	}
}
