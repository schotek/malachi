// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// holdTurn takes the store's turn for background writers (store.Paced)
// until the returned function is called, and returns once it is held.
func holdTurn(t *testing.T, st *store.Store) (release func()) {
	t.Helper()
	held, free := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = st.Paced(context.Background(), func() error {
			close(held)
			<-free
			return nil
		})
	}()
	<-held
	var once bool
	return func() {
		if !once {
			once = true
			close(free)
			<-done
		}
	}
}

// A pass writes in the store's turn: while another background writer
// holds it, the pass stores nothing; once it is free, the pass goes on.
// A user's write (ApplyWrite) does not wait for the turn.
func TestPassWritesTakeTurns(t *testing.T) {
	h := newHarness(t)
	p := newFakeProvider(RemoteCalendar{RemoteID: "/cal/"})
	p.put("/cal/", timed("a", t0.Add(time.Hour), time.Hour))
	p.put("/cal/", timed("user", t0.Add(3*time.Hour), time.Hour)) // the server has the user's write too: the full pass keeps it
	a := caldavAccount("acc_1")
	h.setProvider(a.ID, p)
	h.run()
	release := holdTurn(t, h.st)
	defer release()
	h.sv.Start(a)
	eventually(t, "the pass reached the provider", func() bool { return p.callsOf("/cal/") == 1 })
	time.Sleep(100 * time.Millisecond)
	if n := len(occurrences(t, h.st, a.ID, t0, t0.Add(24*time.Hour))); n != 0 {
		t.Fatalf("%d occurrences stored while another background writer held the turn", n)
	}
	// ApplyWrite stores at once, turn or no turn.
	cal := calendarRows(t, h.st, a.ID)[0]
	done := make(chan error, 1)
	go func() {
		done <- h.sv.ApplyWrite(context.Background(), a, cal.ID, WriteResult{Objects: []Object{timed("user", t0.Add(3*time.Hour), time.Hour)}})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ApplyWrite waited for the background writers' turn")
	}
	if n := len(occurrences(t, h.st, a.ID, t0, t0.Add(24*time.Hour))); n != 1 {
		t.Fatalf("%d occurrences after ApplyWrite", n)
	}
	release()
	eventually(t, "the pass went on", idleWithSync(h.sv, a.ID))
	if n := len(occurrences(t, h.st, a.ID, t0, t0.Add(24*time.Hour))); n != 2 {
		t.Fatalf("%d occurrences after the pass", n)
	}
}

// storageError leaves an *api.Error and a cancelled context alone and
// makes storageError of anything else.
func TestStorageErrorWrapping(t *testing.T) {
	if storageError(nil) != nil {
		t.Error("nil wrapped")
	}
	ae := api.NewError(api.CodeConflict, "theirs")
	if got := storageError(fmt.Errorf("wrapped: %w", ae)); !errors.Is(got, ae) {
		t.Errorf("api error replaced: %v", got)
	}
	for _, c := range []error{context.Canceled, fmt.Errorf("x: %w", context.DeadlineExceeded)} {
		if got := storageError(c); got != c {
			t.Errorf("context error replaced: %v", got)
		}
	}
	got := storageError(errors.New("replace resource: database is locked (5) (SQLITE_BUSY)"))
	var out *api.Error
	if !errors.As(got, &out) || out.Code != api.CodeStorageError || !strings.Contains(out.Message, "database is locked") {
		t.Errorf("plain error: %v", got)
	}
}

// graphLikeProvider maps the sink's errors as the Graph provider does
// (graph.ToAPIError): an *api.Error passes, anything else is the network's.
type graphLikeProvider struct {
	*fakeProvider
	lock func(ctx context.Context) (release func(), err error) // held around the inner Sync
}

func (p graphLikeProvider) Sync(ctx context.Context, cal StoredCalendar, w Window, sink Sink) (SyncState, error) {
	if p.lock != nil {
		release, err := p.lock(ctx)
		if err != nil {
			return SyncState{}, err
		}
		defer release()
	}
	st, err := p.fakeProvider.Sync(ctx, cal, w, sink)
	if err == nil {
		return st, nil
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return SyncState{}, ae
	}
	return SyncState{}, api.NewError(api.CodeNetworkError, "graph: %s", err.Error())
}

// A store that refuses the pass's write (another process holds the write
// lock past busy_timeout, as on the owner's first start with 0020) is the
// calendar's storageError and error state — not offline / networkError,
// which the provider's mapping would make of the plain error; the
// provider is kept (an offline account's would be made again).
func TestStoreFailureIsAStorageError(t *testing.T) {
	h := newHarness(t)
	fp := newFakeProvider(RemoteCalendar{RemoteID: "/cal/"})
	fp.put("/cal/", timed("a", t0.Add(time.Hour), time.Hour))
	// A second connection to the same database, with the store's own
	// locking (BEGIN IMMEDIATE): its open transaction is the other writer.
	other, err := sql.Open("sqlite", "file:"+strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(h.st.Path())+"?_txlock=immediate&_pragma=busy_timeout(1000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	p := graphLikeProvider{fakeProvider: fp, lock: func(ctx context.Context) (func(), error) {
		tx, err := other.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		return func() { _ = tx.Rollback() }, nil
	}}
	// A caldav account: the daemon expands its objects, so the pass writes
	// rows (a Graph object without occurrences of its own stores nothing).
	// The mapping under test is the provider's, whatever the account.
	a := caldavAccount("acc_1")
	h.setVersioned(a.ID, p) // any Provider; the harness hands it out as is
	h.run()
	h.sv.Start(a)
	// The store waits busy_timeout (5 s) before it gives up.
	deadline := time.Now().Add(20 * time.Second)
	var s api.CalendarSyncState
	for {
		s, _ = h.sv.CalendarState(a.ID)
		if s.Status == api.CalendarSyncError || s.Status == api.CalendarSyncOffline || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s.Status != api.CalendarSyncError || s.Error == nil || s.Error.Code != api.CodeStorageError {
		t.Fatalf("state %+v (error %+v)", s, s.Error)
	}
	if !strings.Contains(s.Error.Message, "locked") {
		t.Errorf("message %q does not say the database was locked", s.Error.Message)
	}
	if h.madeCount(a.ID) != 1 {
		t.Errorf("provider made %d times; a storage error is not the network's", h.madeCount(a.ID))
	}
	for _, c := range calendarRows(t, h.st, a.ID) {
		if c.ErrorCode != int(api.CodeStorageError) {
			t.Errorf("calendar %s error %d", c.RemoteID, c.ErrorCode)
		}
	}
}
