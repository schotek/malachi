// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The board's service fixes after the review of 2026-10-08 (H1).

// storedAt writes the stored-at stamp of a message, so that a test does
// not hang on the wall clock moving on between two calls.
func (x *boardBox) storedAt(id string, at time.Time) {
	x.t.Helper()
	if _, err := x.b.store.DB().ExecContext(x.ctx, `UPDATE messages SET created_at = ? WHERE id = ?`,
		at.UTC().Format("2006-01-02T15:04:05.000Z"), id); err != nil {
		x.t.Fatal(err)
	}
}

// An archive folder the daemon does not synchronise (Gmail's All Mail)
// takes the messages off the local store: board.archive archives and
// marks the case done, but names nothing moved, so no client offers an
// Undo that could not move them back (H1-1). The synchronised folder
// is TestBoardArchiveMoved.
func TestBoardArchiveUnsyncedNoUndo(t *testing.T) {
	x := newBoardBox(t)
	if _, err := x.b.store.DB().ExecContext(x.ctx, `UPDATE folders SET unsynced = 1 WHERE id = ?`, x.archive.ID); err != nil {
		t.Fatal(err)
	}
	a1 := x.put(bmail{folder: x.inbox, thread: "t_a", rfc: "a1", from: boardAlice, to: []api.Address{boardMe}, text: "Hi"})
	x.drain()
	c := x.caseOf("t_a")
	if !c.CanArchive {
		t.Fatalf("canArchive: %+v", c)
	}
	res, err := x.svc.Archive(x.ctx, api.BoardArchiveParams{CaseID: c.ID})
	if err != nil || res.NoArchive || res.Archived != 1 || res.Moved != nil || res.Case.Visibility != api.BoardDone {
		t.Fatalf("archive: %+v %v", res, err)
	}
	if _, err := x.b.store.GetMessage(x.ctx, x.acc, a1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("still stored locally: %v", err)
	}
}

// An account removed after it was named in triageAccounts neither
// refuses the next write of the preferences nor is listed again; a
// malformed id is refused; a list whose accounts are all gone stays, so
// that triage reads no account rather than every one (H1-2).
func TestBoardPreferencesRemovedTriageAccount(t *testing.T) {
	x := newBoardBox(t)
	other := seedAccount(t, x.b, "other@example.invalid")
	p, err := x.svc.Preferences(x.ctx, api.BoardPreferencesParams{})
	if err != nil {
		t.Fatal(err)
	}
	prefs := p.Preferences
	prefs.TriageAccounts = []api.AccountID{api.AccountID(x.acc), api.AccountID(other), api.AccountID(other)}
	set, err := x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: prefs})
	if err != nil || !slices.Equal(set.Preferences.TriageAccounts, []api.AccountID{api.AccountID(x.acc), api.AccountID(other)}) {
		t.Fatalf("set: %+v %v", set, err)
	}
	if err := x.b.store.DeleteAccount(x.ctx, other, true); err != nil {
		t.Fatal(err)
	}
	// Read: the removed account is gone.
	p, err = x.svc.Preferences(x.ctx, api.BoardPreferencesParams{})
	if err != nil || !slices.Equal(p.Preferences.TriageAccounts, []api.AccountID{api.AccountID(x.acc)}) {
		t.Fatalf("read: %+v %v", p, err)
	}
	// A client that still holds the old list writes another preference.
	prefs.Windows.Info = 7
	set, err = x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: prefs})
	if err != nil || set.Preferences.Windows.Info != 7 ||
		!slices.Equal(set.Preferences.TriageAccounts, []api.AccountID{api.AccountID(x.acc)}) {
		t.Fatalf("write after the removal: %+v %v", set, err)
	}
	// Malformed ids are refused.
	for _, bad := range []api.AccountID{"", "acc 1", "acc\n", api.AccountID(make([]byte, maxBoardAccountIDLen+1))} {
		q := prefs
		q.TriageAccounts = []api.AccountID{api.AccountID(x.acc), bad}
		if _, err := x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: q}); !isCode(err, api.CodeInvalidArgument) {
			t.Fatalf("malformed %q: %v", bad, err)
		}
	}
	// Every named account gone: the list stays and names none to triage.
	q := prefs
	q.TriageAccounts = []api.AccountID{api.AccountID(other)}
	set, err = x.svc.SetPreferences(x.ctx, api.BoardSetPreferencesParams{Preferences: q})
	if err != nil || !slices.Equal(set.Preferences.TriageAccounts, []api.AccountID{api.AccountID(other)}) {
		t.Fatalf("all gone: %+v %v", set, err)
	}
	stored, err := x.b.boardPrefs(x.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if accs, err := x.b.triageAccounts(x.ctx, stored); err != nil || len(accs) != 0 {
		t.Fatalf("triage reads %d accounts: %v", len(accs), err)
	}
}

// isCode reports an api.Error of the code.
func isCode(err error, code api.ErrorCode) bool {
	var e *api.Error
	return errors.As(err, &e) && e.Code == code
}

// A refused unflag leaves the mark of a remind that came due: only an
// unflag that changed the flags ends it (H1-7).
func TestBoardUnflagRefusedKeepsReminded(t *testing.T) {
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_f", rfc: "f1", from: boardAlice, to: []api.Address{boardMe}, flagged: true, text: "Sign it"})
	x.drain()
	c := x.caseOf("t_f")
	if c.RuleReason != api.BoardReasonHotFlagged {
		t.Fatalf("case: %s", c.RuleReason)
	}
	until := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	if _, err := x.svc.Remind(x.ctx, api.BoardRemindParams{CaseID: c.ID, Until: &until}); err != nil {
		t.Fatal(err)
	}
	x.b.board.now = func() time.Time { return until.Add(time.Minute) }
	t.Cleanup(func() { x.b.board.now = time.Now })
	x.b.clearDueReminds(x.ctx)
	if got := x.caseOf("t_f"); got.RemindedAt == nil {
		t.Fatalf("not reminded: %+v", got)
	}
	if _, err := x.b.store.DB().ExecContext(x.ctx, `CREATE TRIGGER refuse_flag BEFORE UPDATE OF flags ON messages
		BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := x.svc.Unflag(x.ctx, api.BoardUnflagParams{CaseID: c.ID}); err == nil {
		t.Fatal("unflag went through the refusal")
	}
	if got := x.caseOf("t_f"); got.RemindedAt == nil {
		t.Fatalf("a refused unflag ended the reminded mark: %+v", got)
	}
	if _, err := x.b.store.DB().ExecContext(x.ctx, `DROP TRIGGER refuse_flag`); err != nil {
		t.Fatal(err)
	}
	res, err := x.svc.Unflag(x.ctx, api.BoardUnflagParams{CaseID: c.ID})
	if err != nil || res.Unflagged != 1 || res.Case.RemindedAt != nil {
		t.Fatalf("unflag: %+v %v", res, err)
	}
}

// A copy of a message the case already had (another client moved it,
// the daemon stored it anew) does not wake a remind, though it was
// stored after the remind was set: the adapter measures from when the
// remind was set and passes over the Message-IDs seen then (H1-11). New
// mail does wake it.
func TestBoardRemindNotWokenByMovedCopy(t *testing.T) {
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_r", rfc: "r1", from: boardAlice, to: []api.Address{boardMe}, at: 40 * time.Hour, text: "Hi"})
	x.drain()
	c := x.caseOf("t_r")
	until := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	if _, err := x.svc.Remind(x.ctx, api.BoardRemindParams{CaseID: c.ID, Until: &until}); err != nil {
		t.Fatal(err)
	}
	sc, err := x.b.store.GetBoardCase(x.ctx, string(c.ID))
	if err != nil {
		t.Fatal(err)
	}
	set := sc.RemindSetAt()
	if set.IsZero() {
		t.Fatal("no time the remind was set")
	}
	cp := x.put(bmail{folder: x.archive, thread: "t_r", rfc: "r1", from: boardAlice, to: []api.Address{boardMe}, at: 40 * time.Hour, text: "Hi"})
	x.storedAt(cp, set.Add(time.Second))
	x.drain()
	if got := x.caseOf("t_r"); got.Visibility != api.BoardSnoozed || got.RemindAt == nil {
		t.Fatalf("woken by a copy: %+v", got)
	}
	r2 := x.put(bmail{folder: x.inbox, thread: "t_r", rfc: "r2", inReplyTo: "r1", from: boardAlice, to: []api.Address{boardMe},
		at: 47 * time.Hour, text: "Any news?"})
	x.storedAt(r2, set.Add(2*time.Second))
	x.drain()
	if got := x.caseOf("t_r"); got.Visibility != api.BoardLive || got.RemindAt != nil || got.RemindedAt != nil {
		t.Fatalf("not woken by new mail: %+v", got)
	}
}
