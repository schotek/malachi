// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"testing"
	"time"
)

// The window's modes (macOS BoardTests.swift) and the default style
// (BoardControllerTests.swift styleOnShowRule, styleNicks).

func TestInitialMode(t *testing.T) {
	if InitialMode != ModeMail {
		t.Error("a window does not start in Mail")
	}
	eq(t, "Modes", Modes, []Mode{0, 1})
}

func TestAllows(t *testing.T) {
	cases := []struct {
		c    Command
		m    Mode
		want bool
	}{
		{CommandSwitchMode, ModeMail, true},
		{CommandNewMessage, ModeMail, true},
		{CommandCheckForNewMail, ModeMail, true},
		{CommandMailView, ModeMail, true},
		{CommandMessageAction, ModeMail, true},
		{CommandBoardView, ModeMail, false},
		{CommandSwitchMode, ModeBoard, true},
		{CommandNewMessage, ModeBoard, true},
		{CommandCheckForNewMail, ModeBoard, true},
		{CommandMailView, ModeBoard, false},
		{CommandMessageAction, ModeBoard, false},
		{CommandBoardView, ModeBoard, true},
	}
	if len(cases) != len(Commands)*len(Modes) {
		t.Fatalf("%d cases for %d commands in %d modes", len(cases), len(Commands), len(Modes))
	}
	for _, c := range cases {
		if got := Allows(c.c, c.m); got != c.want {
			t.Errorf("Allows(%d, %d) = %v", c.c, c.m, got)
		}
	}
}

func TestModeForRequest(t *testing.T) {
	cases := []struct {
		r             Request
		current, want Mode
	}{
		{RequestShowOutbox, ModeMail, ModeMail},
		{RequestShowOutbox, ModeBoard, ModeMail},
		{RequestRevealAssistant, ModeMail, ModeMail},
		{RequestRevealAssistant, ModeBoard, ModeMail},
		{RequestOpenMessageWindow, ModeMail, ModeMail},
		{RequestOpenMessageWindow, ModeBoard, ModeBoard},
		{RequestCompose, ModeMail, ModeMail},
		{RequestCompose, ModeBoard, ModeBoard},
	}
	if len(cases) != len(Requests)*len(Modes) {
		t.Fatalf("%d cases for %d requests in %d modes", len(cases), len(Requests), len(Modes))
	}
	for _, c := range cases {
		if got := ModeFor(c.r, c.current); got != c.want {
			t.Errorf("ModeFor(%d, %d) = %d", c.r, c.current, got)
		}
	}
}

func TestViewsMail(t *testing.T) {
	for _, c := range []struct {
		m        Mode
		key, out bool
	}{
		{ModeMail, true, true},
		{ModeMail, false, false},
		{ModeBoard, true, false},
		{ModeBoard, false, false},
	} {
		if got := ViewsMail(c.m, c.key); got != c.out {
			t.Errorf("ViewsMail(%d, %v) = %v", c.m, c.key, got)
		}
	}
}

func TestModeTexts(t *testing.T) {
	if Mail(tr) == "" || BoardName(tr) == "" || Mail(tr) == BoardName(tr) {
		t.Errorf("the switch's texts %q and %q", Mail(tr), BoardName(tr))
	}
}

func TestStyleOnShowRule(t *testing.T) {
	for _, current := range Styles {
		for _, last := range Styles {
			for _, d := range DefaultStyles {
				want := d.Style
				if d.Last {
					want = last
				}
				if got := StyleOnShow(d, last, current, false); got != want {
					t.Errorf("not picked: from %d, default %+v, last %d = %d", current, d, last, got)
				}
				if got := StyleOnShow(d, last, current, true); got != current {
					t.Errorf("picked: from %d, default %+v, last %d = %d", current, d, last, got)
				}
			}
		}
	}
}

func TestDefaultStyleNicks(t *testing.T) {
	var nicks []string
	for _, d := range DefaultStyles {
		nicks = append(nicks, d.Nick())
		if ParseDefaultStyle(d.Nick()) != d {
			t.Errorf("ParseDefaultStyle(%q) is not %+v", d.Nick(), d)
		}
	}
	eq(t, "nicks", nicks, []string{"last", "list", "columns", "today"})
	for _, junk := range []string{"", "List", "grid"} {
		if !ParseDefaultStyle(junk).Last {
			t.Errorf("ParseDefaultStyle(%q) is not Last Used", junk)
		}
	}
	if ParseStyle("last") != StyleList {
		t.Error(`ParseStyle("last") is not the List`)
	}
	var titles []string
	for _, d := range DefaultStyles {
		titles = append(titles, DefaultStyleTitle(d, tr))
	}
	eq(t, "titles", titles, []string{"Last Used", "List", "Columns", "Today"})
}

func TestStartMode(t *testing.T) {
	cases := []struct {
		start, last string
		enabled     bool
		want        Mode
	}{
		{"mail", "board", true, ModeMail},
		{"board", "mail", true, ModeBoard},
		{"last", "board", true, ModeBoard},
		{"last", "mail", true, ModeMail},
		{"last", "", true, ModeMail},
		{"last", "junk", true, ModeMail},
		{"", "board", true, ModeMail},
		{"junk", "board", true, ModeMail},
		{"board", "board", false, ModeMail},
		{"last", "board", false, ModeMail},
	}
	for _, c := range cases {
		if got := StartMode(c.start, c.last, c.enabled); got != c.want {
			t.Errorf("StartMode(%q, %q, %v) = %d, want %d", c.start, c.last, c.enabled, got, c.want)
		}
	}
	for _, m := range Modes {
		if got, ok := ParseMode(m.Nick()); !ok || got != m {
			t.Errorf("ParseMode(%q) = %d, %v", m.Nick(), got, ok)
		}
	}
	var nicks, titles []string
	for _, s := range StartModes {
		nicks = append(nicks, s.Nick())
		titles = append(titles, StartModeTitle(s, tr))
		if ParseStartChoice(s.Nick()) != s {
			t.Errorf("ParseStartChoice(%q)", s.Nick())
		}
	}
	eq(t, "start nicks", nicks, []string{"mail", "board", "last"})
	eq(t, "start titles", titles, []string{"Mail", "Board", "Last Used"})
}

func TestFilterOnShow(t *testing.T) {
	accounts := []AccountInfo{{ID: "a"}, {ID: "b"}}
	eq(t, "kept", FilterOnShow("b", accounts), "b")
	eq(t, "gone", FilterOnShow("c", accounts), "")
	eq(t, "none saved", FilterOnShow("", accounts), "")
	eq(t, "no accounts", FilterOnShow("a", nil), "")
}

func TestStyleNicks(t *testing.T) {
	var nicks []string
	for _, s := range Styles {
		nicks = append(nicks, s.Nick())
		if ParseStyle(s.Nick()) != s {
			t.Errorf("ParseStyle(%q) is not %d", s.Nick(), s)
		}
	}
	eq(t, "nicks", nicks, []string{"list", "columns", "today"})
	for _, junk := range []string{"", "List", "0", "1", "grid", " today"} {
		if ParseStyle(junk) != StyleList {
			t.Errorf("ParseStyle(%q) is not the List", junk)
		}
	}
}

// StartDecision (the start mode while the daemon's preferences may still
// be on their way; decision H3-4).
func TestStartDecision(t *testing.T) {
	const (
		no  = false
		yes = true
	)
	tests := []struct {
		name                      string
		start                     StartChoice
		last                      Mode
		known, enabled, sw, acted bool
		waited                    time.Duration
		wantMode                  Mode
		wantDecided               bool
	}{
		{"mail start decides at once", StartMail, ModeBoard, no, yes, no, no, 0, ModeMail, yes},
		{"last used with mail last decides at once", StartLast, ModeMail, no, yes, no, no, 0, ModeMail, yes},
		{"board start waits for prefs", StartBoard, ModeMail, no, yes, no, no, time.Second, ModeMail, no},
		{"last used board waits for prefs", StartLast, ModeBoard, no, yes, no, no, 4 * time.Second, ModeMail, no},
		{"board start, prefs on", StartBoard, ModeMail, yes, yes, no, no, time.Second, ModeBoard, yes},
		{"last used board, prefs on", StartLast, ModeBoard, yes, yes, no, no, 0, ModeBoard, yes},
		{"board start, board turned off", StartBoard, ModeMail, yes, no, no, no, 0, ModeMail, yes},
		{"user switched first", StartBoard, ModeMail, no, yes, yes, no, 0, ModeMail, yes},
		{"user switched, prefs on too", StartBoard, ModeMail, yes, yes, yes, no, 0, ModeMail, yes},
		{"user acted in mail", StartLast, ModeBoard, no, yes, no, yes, time.Second, ModeMail, yes},
		{"user acted in mail before prefs on", StartBoard, ModeMail, yes, yes, no, yes, 0, ModeMail, yes},
		{"bound passed without prefs", StartBoard, ModeMail, no, yes, no, no, StartWait, ModeMail, yes},
		{"long after the bound", StartLast, ModeBoard, no, yes, no, no, time.Minute, ModeMail, yes},
		{"just under the bound", StartBoard, ModeMail, no, yes, no, no, StartWait - time.Millisecond, ModeMail, no},
	}
	for _, tt := range tests {
		mode, decided := StartDecision(tt.start, tt.last, tt.known, tt.enabled, tt.sw, tt.acted, tt.waited)
		if mode != tt.wantMode || decided != tt.wantDecided {
			t.Errorf("%s: got (%v, %v), want (%v, %v)", tt.name, mode, decided, tt.wantMode, tt.wantDecided)
		}
	}
}

func TestNumberRowDigit(t *testing.T) {
	for code, want := range map[uint]rune{10: '1', 11: '2', 9: 0, 12: 0, 0: 0, 49: 0} {
		if got := NumberRowDigit(code); got != want {
			t.Errorf("NumberRowDigit(%d) = %q, want %q", code, got, want)
		}
	}
	// With the digit, Primary+1 is Show Mail on any layout.
	if a, ok := KeyFor(NumberRowDigit(10), true, false, false, ModeBoard, tr); !ok || a != KeyShowMail {
		t.Errorf("KeyFor(number row 1) = %v, %v", a, ok)
	}
}
