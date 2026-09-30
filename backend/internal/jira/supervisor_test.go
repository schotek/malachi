// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// runningSupervisor starts a supervisor over the harness's store and site
// with the account started, and waits for its first pass.
func (h *harness) runningSupervisor(tokenCalls *int, mu *sync.Mutex) *Supervisor {
	h.t.Helper()
	sv := NewSupervisor(SupervisorDeps{
		Store: h.st,
		Token: func(context.Context, string) (string, error) {
			mu.Lock()
			*tokenCalls++
			mu.Unlock()
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.token, h.tokenErr
		},
		Notifier: h.notes,
		HTTP:     h.f.HTTPClient(),
		Now:      h.clock.now,
		Prefs:    func() SyncPrefs { return SyncPrefs{} }, // manual: passes only when asked
		Stored:   func(context.Context, string, string, ingest.Policy) {},
		Backoff:  func(int) time.Duration { return 20 * time.Millisecond },
		Sleep:    func(context.Context, time.Duration) error { return nil },
	})
	sv.Start(h.acc) // before Run: queued
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sv.Run(ctx); close(done) }()
	h.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(waitTimeout):
			h.t.Error("supervisor did not stop")
		}
	})
	h.waitSupervisor(sv, func(st api.SyncState) bool { return st.Status == api.SyncIdle && st.LastSync != nil })
	return sv
}

func (h *harness) waitSupervisor(sv *Supervisor, ok func(api.SyncState) bool) api.SyncState {
	h.t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if st, running := sv.State(h.acc.ID); running && ok(st) {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	st, _ := sv.State(h.acc.ID)
	h.t.Fatalf("supervisor state stayed %+v", st)
	return api.SyncState{}
}

func TestSupervisorRefreshIssue(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h := newHarness(t, mode)
		newScene(h)
		var mu sync.Mutex
		calls := 0
		sv := h.runningSupervisor(&calls, &mu)
		ctx := context.Background()
		if !h.hasFolder(spaceBox("10000")) {
			t.Fatal("the first pass made no folders")
		}

		// An issue the store has never seen, by key: fetched at once.
		h.clock.advance(time.Minute)
		fresh := h.f.AddIssue("ITSD", "Hlášeno e-mailem", func(is *jiratest.Issue) { is.Reporter = h.f.Petr })
		if err := sv.RefreshIssueWait(ctx, h.acc.ID, fresh.Key, waitTimeout); err != nil {
			t.Fatal(err)
		}
		if _, ok := h.issue(fresh.ID); !ok {
			t.Fatal("the refreshed issue is not stored")
		}
		// A known one, by key, whatever its stamp.
		h.clock.advance(time.Minute)
		h.f.SetWatching(fresh.ID, true) // no updated change
		if err := sv.RefreshIssueWait(ctx, h.acc.ID, fresh.Key, waitTimeout); err != nil {
			t.Fatal(err)
		}
		if is, _ := h.issue(fresh.ID); !is.Watching {
			t.Fatal("a forced refresh did not refresh")
		}
		// A key of an unselected space is fetched and dropped.
		mob := h.f.AddIssue("MOB", "Jinde")
		if err := sv.RefreshIssueWait(ctx, h.acc.ID, mob.Key, waitTimeout); err != nil {
			t.Fatal(err)
		}
		if _, ok := h.issue(mob.ID); ok {
			t.Fatal("an issue of an unselected space was stored")
		}

		if sv.TriggerIssue(h.acc.ID, "not a key") || sv.TriggerIssue("acc_nobody", fresh.Key) {
			t.Fatal("a bad trigger was accepted")
		}
		if !sv.TriggerIssue(h.acc.ID, fresh.Key) {
			t.Fatal("a trigger was refused")
		}
		if err := sv.RefreshIssueWait(ctx, "acc_nobody", fresh.Key, time.Second); ToAPIError(err).Code != api.CodeUnavailable {
			t.Fatalf("unknown account: %v", err)
		}
		if err := sv.RefreshIssueWait(ctx, h.acc.ID, "ITSD 1", time.Second); ToAPIError(err).Code != api.CodeInvalidArgument {
			t.Fatalf("bad key: %v", err)
		}
		short, cancel := context.WithCancel(ctx)
		cancel()
		if err := sv.RefreshIssueWait(short, h.acc.ID, fresh.Key, waitTimeout); ToAPIError(err).Code != api.CodeCancelled {
			t.Fatalf("cancelled: %v", err)
		}

		// The token was read once and kept.
		mu.Lock()
		n := calls
		mu.Unlock()
		if n != 1 {
			t.Fatalf("the keyring was asked %d times", n)
		}
		if states := sv.States(); len(states) != 1 || states[0].AccountID != api.AccountID(h.acc.ID) {
			t.Fatalf("states = %+v", states)
		}
		// A full trigger runs.
		if !sv.Trigger(h.acc.ID, "", true) || sv.Trigger("acc_nobody", "", false) {
			t.Fatal("trigger result")
		}
		sv.Stop(h.acc.ID)
		if _, ok := sv.State(h.acc.ID); ok {
			t.Fatal("a stopped account has a state")
		}
	})
}

func TestSupervisorTokenRefused(t *testing.T) {
	h := newHarness(t, jiratest.Cloud)
	var mu sync.Mutex
	calls := 0
	sv := h.runningSupervisor(&calls, &mu)
	// The token is revoked on the site: the pass fails with authFailed,
	// the cached token is dropped and the keyring asked again.
	h.f.Set(func(f *jiratest.Server) { f.Token = "rotated" })
	sv.Trigger(h.acc.ID, "", false)
	st := h.waitSupervisor(sv, func(st api.SyncState) bool { return st.Status == api.SyncAuthRequired })
	if st.Error == nil || st.Error.Code != api.CodeAuthFailed {
		t.Fatalf("state = %+v", st)
	}
	// The user stores the new token (account.update restarts the syncer).
	h.mu.Lock()
	h.token = "rotated"
	h.mu.Unlock()
	sv.Restart(h.acc)
	h.waitSupervisor(sv, func(st api.SyncState) bool { return st.Status == api.SyncIdle && st.LastSync != nil })
	mu.Lock()
	defer mu.Unlock()
	if calls < 2 {
		t.Fatalf("the keyring was asked %d times", calls)
	}
	if n := h.notes.authCount(); n != 1 {
		t.Fatalf("authRequired sent %d times", n)
	}
}

func TestSupervisorBadConfiguration(t *testing.T) {
	h := newHarness(t, jiratest.Cloud)
	h.acc.Config.Jira.CloudID = "not-a-uuid"
	sv := NewSupervisor(SupervisorDeps{Store: h.st, HTTP: h.f.HTTPClient()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sv.Run(ctx)
	sv.Start(h.acc)
	st := h.waitSupervisor(sv, func(st api.SyncState) bool { return st.Status == api.SyncError })
	if st.Error == nil || st.Error.Code != api.CodeInvalidArgument {
		t.Fatalf("state = %+v", st)
	}
	acc := h.acc
	acc.Config.Jira = nil
	if _, _, err := sv.FetchMessage(ctx, acc, store.Message{}); ToAPIError(err).Code != api.CodeInvalidArgument {
		t.Fatalf("fetch without configuration: %v", err)
	}
}
