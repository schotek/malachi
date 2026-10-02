// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import "testing"

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
		for _, d := range Styles {
			if got := StyleOnShow(current, d, true); got != d {
				t.Errorf("first show from %d with default %d = %d", current, d, got)
			}
			if got := StyleOnShow(current, d, false); got != current {
				t.Errorf("later show from %d with default %d = %d", current, d, got)
			}
		}
	}
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
