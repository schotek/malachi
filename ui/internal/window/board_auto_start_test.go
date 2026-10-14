// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import "testing"

func TestBoardAutoStartAfterPreferencesArrive(t *testing.T) {
	wanted, made := false, false
	var pending []func()
	starts := 0
	s := &boardAutoStart{
		wanted: func() bool { return wanted && !made },
		post:   func(f func()) { pending = append(pending, f) },
	}
	s.start = func() {
		starts++
		// Source startup may notify triage before the page is installed.
		s.changed()
		made = true
	}
	s.changed()
	if len(pending) != 0 {
		t.Fatal("started before preferences and availability arrived")
	}
	wanted = true
	s.changed()
	s.changed()
	if len(pending) != 1 || starts != 0 {
		t.Fatalf("notifications must queue one start: %d callbacks, %d starts", len(pending), starts)
	}
	pending[0]()
	s.changed()
	if len(pending) != 1 || starts != 1 {
		t.Fatalf("existing board must not start again: %d callbacks, %d starts", len(pending), starts)
	}
}

func TestBoardAutoStartRechecksAvailability(t *testing.T) {
	wanted := true
	var pending func()
	starts := 0
	s := &boardAutoStart{
		wanted: func() bool { return wanted },
		post:   func(f func()) { pending = f },
		start:  func() { starts++ },
	}
	s.changed()
	wanted = false
	pending()
	if starts != 0 {
		t.Fatal("started after automatic triage was disabled")
	}
	wanted = true
	s.changed()
	pending()
	if starts != 1 {
		t.Fatal("did not retry when automatic triage became available again")
	}
}
