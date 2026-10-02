// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardtriage

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
)

// The board's preferences against the fake daemon: loading, optimistic
// writes, their order and their revert (macOS
// BoardPreferencesControllerTests).

func newPrefs(d *fakeDaemon, loop *testLoop) *Preferences {
	return NewPreferences(PreferencesConfig{Caller: d, Loop: loop, Log: discardLog(), Translator: tr})
}

// loadNow loads and waits for the answer.
func loadNow(t *testing.T, loop *testLoop, p *Preferences) bool {
	t.Helper()
	var got []bool
	p.Load(func(ok bool) { got = append(got, ok) })
	loop.runUntil(t, func() bool { return len(got) > 0 })
	return got[0]
}

// update writes and waits for the answer.
func update(t *testing.T, loop *testLoop, p *Preferences, quiet bool, change func(*api.BoardPreferences)) bool {
	t.Helper()
	var got []bool
	p.Update(quiet, change, func(ok bool) { got = append(got, ok) })
	loop.runUntil(t, func() bool { return len(got) > 0 })
	return got[0]
}

func current(p *Preferences) *api.BoardPreferences {
	c, ok := p.Current()
	if !ok {
		return nil
	}
	return &c
}

func TestPreferencesLoad(t *testing.T) {
	loop := newTestLoop()
	want := prefsWith(func(p *api.BoardPreferences) { p.Assistant, p.AutoTriage, p.AutoTriageMinutes = true, true, 60 })
	d := newFakeDaemon(want)
	p := newPrefs(d, loop)
	reports := 0
	p.Observe(func() { reports++ })
	if _, ok := p.Current(); ok {
		t.Fatal("known before a load")
	}
	if !loadNow(t, loop, p) || !samePrefs(current(p), &want) || reports != 1 {
		t.Errorf("loaded %+v, reports %d", current(p), reports)
	}
	// The same answer again reports nothing.
	if !loadNow(t, loop, p) || reports != 1 {
		t.Errorf("again: reports %d", reports)
	}
}

// A write shows at once, sends the whole object, and the answer stands.
func TestPreferencesOptimisticWrite(t *testing.T) {
	loop := newTestLoop()
	d := newFakeDaemon(api.DefaultBoardPreferences())
	p := newPrefs(d, loop)
	loadNow(t, loop, p)
	d.hold(api.MethodBoardSetPreferences, true)
	var results []bool
	p.Update(false, func(p *api.BoardPreferences) { p.AutoTriage = true }, func(ok bool) { results = append(results, ok) })
	if c := current(p); c == nil || !c.AutoTriage {
		t.Errorf("not at once: %+v", c)
	}
	loop.runUntil(t, func() bool { return d.waiting(api.MethodBoardSetPreferences) == 1 })
	d.hold(api.MethodBoardSetPreferences, false)
	loop.runUntil(t, func() bool { return len(results) == 1 && p.Idle() })
	want := prefsWith(func(p *api.BoardPreferences) { p.AutoTriage = true })
	if !slices.Equal(results, []bool{true}) || !samePrefList(d.setList(), []api.BoardPreferences{want}) || !samePrefs(current(p), &want) {
		t.Errorf("results %v, sets %+v, current %+v", results, d.setList(), current(p))
	}
}

// A refused write is taken back and says why.
func TestPreferencesRefusedWriteIsTakenBack(t *testing.T) {
	loop := newTestLoop()
	d := newFakeDaemon(api.DefaultBoardPreferences())
	p := newPrefs(d, loop)
	var errs []string
	p.OnError = func(s string) { errs = append(errs, s) }
	loadNow(t, loop, p)
	d.failSet(&api.Error{Code: api.CodeInvalidArgument, Message: "autoTriageMinutes out of range"})
	d.hold(api.MethodBoardSetPreferences, true)
	reports := 0
	p.Observe(func() { reports++ })
	var results []bool
	p.Update(false, func(p *api.BoardPreferences) { p.AutoTriageMinutes = 3 }, func(ok bool) { results = append(results, ok) })
	if c := current(p); c == nil || c.AutoTriageMinutes != 3 {
		t.Errorf("not at once: %+v", c)
	}
	loop.runUntil(t, func() bool { return d.waiting(api.MethodBoardSetPreferences) == 1 })
	d.hold(api.MethodBoardSetPreferences, false)
	loop.runUntil(t, func() bool { return len(results) == 1 })
	def := api.DefaultBoardPreferences()
	if results[0] || !samePrefs(current(p), &def) || reports != 2 {
		t.Errorf("result %v, current %+v, reports %d", results[0], current(p), reports)
	}
	if !slices.Equal(errs, []string{"Changing the board’s settings failed: the board did not accept it."}) {
		t.Errorf("errors %q", errs)
	}
}

// Writes go one after another, each with what the earlier left.
func TestPreferencesWritesInOrder(t *testing.T) {
	loop := newTestLoop()
	d := newFakeDaemon(api.DefaultBoardPreferences())
	p := newPrefs(d, loop)
	loadNow(t, loop, p)
	d.hold(api.MethodBoardSetPreferences, true)
	p.Update(false, func(p *api.BoardPreferences) { p.AutoTriage = true }, nil)
	p.Update(false, func(p *api.BoardPreferences) { p.AutoTriageDailyCases = 10 }, nil)
	both := prefsWith(func(p *api.BoardPreferences) { p.AutoTriage, p.AutoTriageDailyCases = true, 10 })
	if !samePrefs(current(p), &both) {
		t.Errorf("at once: %+v", current(p))
	}
	loop.runUntil(t, func() bool { return d.waiting(api.MethodBoardSetPreferences) == 1 })
	d.hold(api.MethodBoardSetPreferences, false)
	loop.runUntil(t, p.Idle)
	first := prefsWith(func(p *api.BoardPreferences) { p.AutoTriage = true })
	if !samePrefList(d.setList(), []api.BoardPreferences{first, both}) || !samePrefs(current(p), &both) {
		t.Errorf("sets %+v, current %+v", d.setList(), current(p))
	}
}

// A write before the first load loads first; a load that fails takes the
// write back.
func TestPreferencesWriteBeforeLoad(t *testing.T) {
	loop := newTestLoop()
	stored := prefsWith(func(p *api.BoardPreferences) { p.Windows.Hot = 7 })
	d := newFakeDaemon(stored)
	p := newPrefs(d, loop)
	if !update(t, loop, p, false, func(p *api.BoardPreferences) { p.Assistant = true }) {
		t.Fatal("the write failed")
	}
	want := prefsWith(func(p *api.BoardPreferences) { p.Windows.Hot, p.Assistant = 7, true })
	if d.getCount() != 1 || !samePrefList(d.setList(), []api.BoardPreferences{want}) || !samePrefs(current(p), &want) {
		t.Errorf("gets %d, sets %+v, current %+v", d.getCount(), d.setList(), current(p))
	}

	other := newPrefs(d, loop)
	var errs []string
	other.OnError = func(s string) { errs = append(errs, s) }
	d.failGet(&api.Error{Code: api.CodeStorageError, Message: "disk"})
	if update(t, loop, other, false, func(p *api.BoardPreferences) { p.Assistant = false }) {
		t.Error("a write without preferences stored")
	}
	if _, ok := other.Current(); ok {
		t.Error("preferences without a load")
	}
	if !slices.Equal(errs, []string{"Changing the board’s settings failed: the mail backend could not save it."}) || len(d.setList()) != 1 {
		t.Errorf("errors %q, sets %d", errs, len(d.setList()))
	}
}

// A write reads the preferences afresh and lays only its own change over
// them: what another client changed since the last load stays.
func TestPreferencesWriteKeepsAnotherClientsChange(t *testing.T) {
	loop := newTestLoop()
	d := newFakeDaemon(api.DefaultBoardPreferences())
	p := newPrefs(d, loop)
	loadNow(t, loop, p)
	// Another client changes the interval after this one loaded.
	d.setPrefs(prefsWith(func(p *api.BoardPreferences) { p.AutoTriageMinutes = 60 }))
	if !update(t, loop, p, false, func(p *api.BoardPreferences) { p.AutoTriage = true }) {
		t.Fatal("the write failed")
	}
	want := prefsWith(func(p *api.BoardPreferences) { p.AutoTriage, p.AutoTriageMinutes = true, 60 })
	if !samePrefList(d.setList(), []api.BoardPreferences{want}) || !samePrefs(current(p), &want) {
		t.Errorf("sets %+v, current %+v", d.setList(), current(p))
	}
	// A fresh read that fails: the write goes on with the last one.
	d.failGet(&api.Error{Code: api.CodeStorageError, Message: "disk"})
	if !update(t, loop, p, false, func(p *api.BoardPreferences) { p.AutoTriageDailyCases = 20 }) {
		t.Fatal("the second write failed")
	}
	last := prefsWith(func(p *api.BoardPreferences) {
		p.AutoTriage, p.AutoTriageMinutes, p.AutoTriageDailyCases = true, 60, 20
	})
	if sets := d.setList(); !samePrefs(&sets[len(sets)-1], &last) {
		t.Errorf("last set %+v", sets[len(sets)-1])
	}
}

// A quiet write that fails says nothing through OnError; the result still
// tells.
func TestPreferencesQuietWrite(t *testing.T) {
	loop := newTestLoop()
	d := newFakeDaemon(api.DefaultBoardPreferences())
	p := newPrefs(d, loop)
	var errs []string
	p.OnError = func(s string) { errs = append(errs, s) }
	loadNow(t, loop, p)
	d.failSet(&api.Error{Code: api.CodeStorageError, Message: "disk"})
	if update(t, loop, p, true, func(p *api.BoardPreferences) { p.Assistant = true }) || len(errs) != 0 {
		t.Errorf("quiet: errors %q", errs)
	}
	if update(t, loop, p, false, func(p *api.BoardPreferences) { p.Assistant = true }) || len(errs) != 1 {
		t.Errorf("loud: errors %q", errs)
	}
}

// A write whose done starts another runs them one after another.
func TestPreferencesWriteFromDone(t *testing.T) {
	loop := newTestLoop()
	d := newFakeDaemon(api.DefaultBoardPreferences())
	p := newPrefs(d, loop)
	loadNow(t, loop, p)
	var results []bool
	p.Update(false, func(p *api.BoardPreferences) { p.AutoTriage = true }, func(ok bool) {
		results = append(results, ok)
		p.Update(false, func(p *api.BoardPreferences) { p.AutoTriageDailyCases = 20 }, func(ok bool) { results = append(results, ok) })
	})
	loop.runUntil(t, func() bool { return len(results) == 2 && p.Idle() })
	if !slices.Equal(results, []bool{true, true}) || len(d.setList()) != 2 {
		t.Errorf("results %v, sets %+v", results, d.setList())
	}
}

// LastLoadFailed follows the loads; ObserveLoaded reports each answer.
func TestPreferencesLoadState(t *testing.T) {
	loop := newTestLoop()
	d := newFakeDaemon(api.DefaultBoardPreferences())
	p := newPrefs(d, loop)
	loads := 0
	p.ObserveLoaded(func() { loads++ })
	d.failGet(&api.Error{Code: api.CodeStorageError, Message: "disk"})
	if loadNow(t, loop, p) || !p.LastLoadFailed() || loads != 0 {
		t.Errorf("failed load: failed %v, loads %d", p.LastLoadFailed(), loads)
	}
	if _, ok := p.Stored(); ok {
		t.Error("stored after a failed load")
	}
	d.failGet(nil)
	def := api.DefaultBoardPreferences()
	if s, ok := p.Stored(); ok || !loadNow(t, loop, p) || p.LastLoadFailed() || loads != 1 {
		t.Errorf("load: stored %+v, failed %v, loads %d", s, p.LastLoadFailed(), loads)
	}
	if s, ok := p.Stored(); !ok || !samePrefs(&s, &def) {
		t.Errorf("stored %+v", s)
	}
	// A lost connection drops the reply on its way; the preferences stay.
	var got []bool
	p.Load(func(ok bool) { got = append(got, ok) })
	p.ConnectionChanged(false)
	loop.runUntil(t, func() bool { return len(got) == 1 })
	if got[0] || !p.Idle() {
		t.Errorf("overtaken load: %v, idle %v", got[0], p.Idle())
	}
	if _, ok := p.Current(); !ok {
		t.Error("the preferences went with the connection")
	}
}

func TestWhyOf(t *testing.T) {
	disconnected := errors.New("not connected to malachid")
	isDown := func(err error) bool { return errors.Is(err, disconnected) }
	rows := []struct {
		err  error
		want board.Why
	}{
		{nil, board.WhyNone},
		{&api.Error{Code: api.CodeCaseNotFound}, board.WhyCaseGone},
		{&api.Error{Code: api.CodeInvalidArgument}, board.WhyNotAccepted},
		{&api.Error{Code: api.CodeMethodNotFound}, board.WhyNoBoard},
		{&api.Error{Code: api.CodeNotImplemented}, board.WhyNoBoard},
		{&api.Error{Code: api.CodeStorageError}, board.WhyNotSaved},
		{&api.Error{Code: api.CodeDraftNotFound}, board.WhyDraftGone},
		{&api.Error{Code: api.CodeConflict}, board.WhyNone},
		{fmt.Errorf("call: %w", &api.Error{Code: api.CodeStorageError}), board.WhyNotSaved},
		{context.DeadlineExceeded, board.WhyTimeout},
		{disconnected, board.WhyBackendDown},
		{errors.New("write: broken pipe"), board.WhyNone},
	}
	for _, r := range rows {
		if got := WhyOf(r.err, isDown); got != r.want {
			t.Errorf("WhyOf(%v) = %d, want %d", r.err, got, r.want)
		}
	}
	if WhyOf(disconnected, nil) != board.WhyNone {
		t.Error("without a hook")
	}
}
