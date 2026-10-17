// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The background loops of a first start — the board's evaluation, the
// invitation scan — write in the store's turn for background writers
// (store.Paced), so that they take turns with each other and leave the
// write lock free between turns.

// holdTurn takes the store's turn until the returned function is called,
// and returns once it is held.
func holdTurn(t *testing.T, st *store.Store) (release func()) {
	t.Helper()
	held, free, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_ = st.Paced(context.Background(), func() error {
			close(held)
			<-free
			return nil
		})
	}()
	<-held
	released := false
	return func() {
		if !released {
			released = true
			close(free)
			<-done
		}
	}
}

func TestBoardDrainTakesTurns(t *testing.T) {
	x := newBoardBox(t)
	x.put(bmail{folder: x.inbox, thread: "t_1", rfc: "m1", from: boardAlice, to: []api.Address{boardMe}, text: "Hello?"})
	if n, err := x.b.store.CountBoardDirty(x.ctx); err != nil || n == 0 {
		t.Fatalf("dirty %d %v", n, err)
	}
	release := holdTurn(t, x.b.store)
	defer release()
	done := make(chan error, 1)
	go func() { done <- x.b.drainBoard(x.ctx) }()
	select {
	case err := <-done:
		t.Fatalf("the drain ran (%v) while another background writer held the turn", err)
	case <-time.After(150 * time.Millisecond):
	}
	if n, _ := x.b.store.CountBoardDirty(x.ctx); n == 0 {
		t.Fatal("the drain evaluated threads while another background writer held the turn")
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the drain did not go on once the turn was free")
	}
	if _, cases := x.list(); len(cases) != 1 {
		t.Fatalf("cases %+v", cases)
	}
}

func TestInvitationScanTakesTurns(t *testing.T) {
	x := newInvBox(t)
	id := x.store(x.inbox, readMIME(t, "invite-outlook.eml")).ID // no Stored hook: mail from before 0020
	release := holdTurn(t, x.b.store)
	defer release()
	done := make(chan error, 1)
	go func() { done <- x.b.backfillInvitations(x.ctx) }()
	select {
	case err := <-done:
		t.Fatalf("the scan finished (%v) while another background writer held the turn", err)
	case <-time.After(150 * time.Millisecond):
	}
	if _, err := x.b.store.GetInvitation(x.ctx, id); err == nil {
		t.Fatal("the scan wrote a row while another background writer held the turn")
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the scan did not go on once the turn was free")
	}
	if _, err := x.b.store.GetInvitation(x.ctx, id); err != nil {
		t.Fatalf("row after the scan: %v", err)
	}
}
