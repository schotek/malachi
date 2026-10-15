// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// draftsRecorder records notify.draftsChanged (and is an api.Notifier that
// ignores the rest).
type draftsRecorder struct {
	mu     sync.Mutex
	events []api.DraftsChangedNotification
}

func (*draftsRecorder) NewMessage(api.NewMessageNotification)           {}
func (*draftsRecorder) SyncState(api.SyncStateNotification)             {}
func (*draftsRecorder) AuthRequired(api.AuthRequiredNotification)       {}
func (*draftsRecorder) AccountsChanged(api.AccountsChangedNotification) {}
func (*draftsRecorder) MessagesChanged(api.MessagesChangedNotification) {}
func (r *draftsRecorder) DraftsChanged(n api.DraftsChangedNotification) {
	r.mu.Lock()
	r.events = append(r.events, n)
	r.mu.Unlock()
}

var _ api.DraftsNotifier = (*draftsRecorder)(nil)

// wait returns the notifications once one came, within two seconds.
func (r *draftsRecorder) wait(t *testing.T) []api.DraftsChangedNotification {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		r.mu.Lock()
		out := r.events
		if len(out) > 0 {
			r.events = nil
			r.mu.Unlock()
			return out
		}
		r.mu.Unlock()
	}
	t.Fatal("no notify.draftsChanged")
	return nil
}

// The user's unsent reply: counted on its thread's row, returned on
// request, never a member, announced when saved and deleted.
func TestThreadDrafts(t *testing.T) {
	m := seedThreadedMailbox(t)
	ctx := context.Background()
	rec := &draftsRecorder{}
	m.b.SetNotifier(rec)
	svc := m.b.Threads()

	saved, err := m.b.Drafts().Save(ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: m.acc, Subject: "Re: Lunch",
		TextBody: "On my way", InReplyTo: m.a3}})
	if err != nil {
		t.Fatal(err)
	}
	if ev := rec.wait(t); len(ev) != 1 || ev[0].AccountID != m.acc || len(ev[0].ThreadIDs) != 1 || ev[0].ThreadIDs[0] != "t_a" {
		t.Errorf("after save: %+v", ev)
	}
	// A forward replies to nothing: no conversation shows it.
	if _, err := m.b.Drafts().Save(ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: m.acc, Subject: "Fwd: Report",
		TextBody: "fyi", Forwarding: m.b1}}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.List(ctx, api.ThreadListParams{AccountID: m.acc, FolderID: m.inbox})
	if err != nil {
		t.Fatal(err)
	}
	for _, th := range res.Threads {
		want := 0
		if th.ID == "t_a" {
			want = 1
		}
		if th.DraftCount != want {
			t.Errorf("%s: draftCount %d, want %d", th.ID, th.DraftCount, want)
		}
	}

	got, err := svc.Get(ctx, api.ThreadGetParams{AccountID: m.acc, ThreadID: "t_a", FolderID: m.inbox, WithDrafts: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Thread.DraftCount != 1 || got.Thread.MessageCount != 2 || len(got.Messages) != 2 || len(got.Drafts) != 1 ||
		got.Drafts[0].ID != saved.DraftID || got.Drafts[0].TextBody != "On my way" || got.Drafts[0].InReplyTo != m.a3 ||
		got.Drafts[0].Version != saved.Version || got.Drafts[0].Comment != nil {
		t.Fatalf("with drafts = %+v", got)
	}
	without, err := svc.Get(ctx, api.ThreadGetParams{AccountID: m.acc, ThreadID: "t_a", FolderID: m.inbox})
	if err != nil || without.Drafts != nil || without.Thread.DraftCount != 1 {
		t.Fatalf("without drafts = %+v, %v", without, err)
	}
	all, err := svc.Get(ctx, api.ThreadGetParams{AccountID: m.acc, ThreadID: "t_a", WithDrafts: true})
	if err != nil || all.Drafts != nil || all.Thread.DraftCount != 0 {
		t.Fatalf("account-wide = %+v, %v", all, err)
	}
	// The sent folder shows the conversation's drafts too.
	inSent, err := svc.Get(ctx, api.ThreadGetParams{AccountID: m.acc, ThreadID: "t_a", FolderID: m.sent, WithDrafts: true})
	if err != nil || len(inSent.Drafts) != 1 || inSent.Thread.DraftCount != 1 {
		t.Fatalf("in Sent = %+v, %v", inSent, err)
	}
	none, err := svc.Get(ctx, api.ThreadGetParams{AccountID: m.acc, ThreadID: "t_b", FolderID: m.inbox, WithDrafts: true})
	if err != nil || none.Drafts != nil {
		t.Fatalf("no draft = %+v, %v", none, err)
	}
	if b, err := json.Marshal(none); err != nil || strings.Contains(string(b), `"drafts"`) {
		t.Errorf("json = %s, %v", b, err)
	}

	if _, err := m.b.Drafts().Delete(ctx, api.DraftDeleteParams{AccountID: m.acc, DraftID: saved.DraftID}); err != nil {
		t.Fatal(err)
	}
	if ev := rec.wait(t); len(ev) != 1 || len(ev[0].ThreadIDs) != 1 || ev[0].ThreadIDs[0] != "t_a" {
		t.Errorf("after delete: %+v", ev)
	}
	if after, err := svc.Get(ctx, api.ThreadGetParams{AccountID: m.acc, ThreadID: "t_a", FolderID: m.inbox, WithDrafts: true}); err != nil ||
		after.Drafts != nil || after.Thread.DraftCount != 0 {
		t.Fatalf("after delete = %+v, %v", after, err)
	}
}

// What gathers within the window goes out once an account; a draft of a
// message no longer stored names no thread (any thread of the account).
func TestDraftsChangedCoalesces(t *testing.T) {
	m := seedThreadedMailbox(t)
	rec := &draftsRecorder{}
	m.b.SetNotifier(rec)
	m.b.draftsChanged(string(m.acc), "t_b")
	m.b.draftsChanged(string(m.acc), "t_a")
	m.b.draftsChanged("acc_other")
	m.b.draftChanged(context.Background(), string(m.acc), "") // a forward: nothing
	time.Sleep(draftsNotifyCoalesce + 100*time.Millisecond)
	ev := rec.wait(t)
	if len(ev) != 2 || ev[0].AccountID != m.acc || strings.Join(threadStrings(ev[0].ThreadIDs), ",") != "t_a,t_b" ||
		ev[1].AccountID != "acc_other" || ev[1].ThreadIDs != nil {
		t.Errorf("events = %+v", ev)
	}
	m.b.draftChanged(context.Background(), string(m.acc), "m_gone")
	if ev := rec.wait(t); len(ev) != 1 || ev[0].ThreadIDs != nil {
		t.Errorf("unknown message: %+v", ev)
	}
	m.b.stopDraftNotify()
	m.b.draftsChanged(string(m.acc), "t_a")
	time.Sleep(draftsNotifyCoalesce + 100*time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.events) != 0 {
		t.Errorf("after stop: %+v", rec.events)
	}
}

func threadStrings(ids []api.ThreadID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}
