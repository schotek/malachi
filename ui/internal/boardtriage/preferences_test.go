// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardtriage

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

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

// Show the Board, the windows and the triage accounts go through the same
// write as every preference; windows the daemon would refuse never leave.
func TestPreferencesFieldSetters(t *testing.T) {
	loop := newTestLoop()
	d := newFakeDaemon(api.DefaultBoardPreferences())
	p := newPrefs(d, loop)
	loadNow(t, loop, p)
	wait := func(set func(done func(bool))) bool {
		t.Helper()
		var got []bool
		set(func(ok bool) { got = append(got, ok) })
		loop.runUntil(t, func() bool { return len(got) > 0 })
		return got[0]
	}
	if !wait(func(done func(bool)) { p.SetEnabled(false, done) }) || current(p).Enabled {
		t.Fatalf("enabled: %+v", current(p))
	}
	w := api.BoardWindows{Hot: 7, You: 365, Them: 1, Info: 2}
	if !wait(func(done func(bool)) {
		if !p.SetWindows(w, done) {
			t.Fatal("valid windows refused")
		}
	}) || current(p).Windows != w {
		t.Fatalf("windows: %+v", current(p))
	}
	sets := len(d.setList())
	for _, bad := range []api.BoardWindows{
		{Hot: 0, You: 1, Them: 1, Info: 1}, {Hot: 1, You: api.MaxBoardWindowDays + 1, Them: 1, Info: 1},
		{Hot: 1, You: 1, Them: -3, Info: 1}, {Hot: 1, You: 1, Them: 1, Info: 0},
	} {
		if ValidWindows(bad) || p.SetWindows(bad, func(bool) { t.Error("done called") }) {
			t.Errorf("%+v accepted", bad)
		}
	}
	if !ValidWindows(DefaultWindows) || DefaultWindows != api.DefaultBoardPreferences().Windows {
		t.Errorf("DefaultWindows %+v vs the contract's %+v", DefaultWindows, api.DefaultBoardPreferences().Windows)
	}
	loop.settle(t, 20*time.Millisecond)
	if len(d.setList()) != sets {
		t.Error("refused windows were written")
	}
	if !wait(func(done func(bool)) { p.SetTriageAccounts([]api.AccountID{"b", "a", "b", ""}, done) }) ||
		!slices.Equal(current(p).TriageAccounts, []api.AccountID{"b", "a"}) {
		t.Fatalf("accounts: %+v", current(p))
	}
	if !wait(func(done func(bool)) { p.SetTriageAccounts(nil, done) }) || len(current(p).TriageAccounts) != 0 {
		t.Fatalf("all accounts: %+v", current(p))
	}
	if last := d.setList()[len(d.setList())-1]; last.TriageAccounts == nil {
		t.Errorf("sent null accounts: %+v", last)
	}
}

func TestTriageAccountsChecklist(t *testing.T) {
	acct := func(id string, kind api.AccountKind, enabled bool) api.Account {
		return api.Account{ID: api.AccountID(id), Enabled: enabled, Config: api.AccountConfig{Kind: kind}}
	}
	accounts := []api.Account{
		acct("m1", "", true), acct("m2", api.AccountGraph, true), acct("j", api.AccountJira, true), acct("off", "", false),
	}
	ids := func(s ...string) []api.AccountID {
		out := []api.AccountID{}
		for _, x := range s {
			out = append(out, api.AccountID(x))
		}
		return out
	}
	// Nothing listed: every enabled mail account, no issue tracker, nothing disabled.
	for _, a := range accounts {
		want := a.ID == "m1" || a.ID == "m2"
		if got := TriageAccountChecked(nil, a); got != want {
			t.Errorf("default %s = %v", a.ID, got)
		}
	}
	if TriageAccountChecked(ids("off"), accounts[3]) {
		t.Error("a disabled account checked")
	}
	tests := []struct {
		name   string
		listed []api.AccountID
		id     string
		on     bool
		want   []api.AccountID
		ok     bool
	}{
		{"uncheck one of all", nil, "m2", false, ids("m1"), true},
		{"check the tracker", nil, "j", true, ids("m1", "m2", "j"), true},
		{"back to all is empty", ids("m1"), "m2", true, ids(), true},
		{"the last one stays", ids("m1"), "m1", false, ids("m1"), false},
		{"unknown account", nil, "x", true, nil, false},
		{"a disabled account cannot be toggled", ids("m1"), "off", true, ids("m1"), false},
		{"a disabled listed account stays listed", ids("off", "m1"), "m2", true, ids("m1", "m2", "off"), true},
		{"only the disabled one would be left", ids("off", "m1"), "m1", false, ids("off", "m1"), false},
	}
	for _, tt := range tests {
		got, ok := ToggleTriageAccount(tt.listed, accounts, api.AccountID(tt.id), tt.on)
		if ok != tt.ok || !slices.Equal(got, tt.want) {
			t.Errorf("%s: = %v, %v; want %v, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestTriageAccountsSubtitle(t *testing.T) {
	acct := func(id string, kind api.AccountKind, enabled bool) api.Account {
		return api.Account{ID: api.AccountID(id), Enabled: enabled, Config: api.AccountConfig{Kind: kind}}
	}
	accounts := []api.Account{acct("m1", "", true), acct("j", api.AccountJira, true), acct("off", "", false)}
	tests := []struct {
		name     string
		listed   []api.AccountID
		accounts []api.Account
		want     TriageAccountsCoverage
	}{
		{"nothing listed", nil, accounts, TriageAccountsAll},
		{"empty list", []api.AccountID{}, accounts, TriageAccountsAll},
		{"nothing listed, no accounts", nil, nil, TriageAccountsAll},
		{"one listed", []api.AccountID{"m1"}, accounts, TriageAccountsSome},
		{"issue tracker listed", []api.AccountID{"j"}, accounts, TriageAccountsSome},
		{"only removed accounts", []api.AccountID{"gone", "gone2"}, accounts, TriageAccountsNone},
		{"only a disabled account", []api.AccountID{"off"}, accounts, TriageAccountsNone},
		{"removed and present", []api.AccountID{"gone", "m1"}, accounts, TriageAccountsSome},
		{"listed, no accounts at all", []api.AccountID{"m1"}, nil, TriageAccountsNone},
	}
	for _, tt := range tests {
		if got := TriageAccountsSubtitle(tt.listed, tt.accounts); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
