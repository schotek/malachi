// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The drafts of a conversation read in a folder: the drafts replying to a
// member in any folder, not a forward and not a board case's suggested
// reply; none in a Drafts folder, the outbox or account-wide.
func TestThreadDraftMembers(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)
	draftsFolder := seedFolder(t, s, "acc", "Drafts", api.RoleDrafts)
	outbox, err := s.OutboxFolder(ctx, "acc")
	if err != nil {
		t.Fatal(err)
	}
	a := boardMail(t, s, inbox, 1, "a", "")
	r := linked(t, s, sent, 2, "r", "a", "a")
	copyOf := linked(t, s, draftsFolder, 3, "dcopy", "r", "a", "r") // an uploaded draft
	tid := sameThread(t, s, a, r, copyOf)
	other := linked(t, s, inbox, 4, "o", "")

	save := func(d *Draft) *Draft {
		t.Helper()
		d.AccountID = "acc"
		if err := s.SaveDraft(ctx, d, nil); err != nil {
			t.Fatal(err)
		}
		return d
	}
	toA := save(&Draft{Subject: "Re: a", TextBody: "one", InReplyTo: a.ID})
	toR := save(&Draft{Subject: "Re: r", TextBody: "two", InReplyTo: r.ID}) // a member in Sent
	save(&Draft{Subject: "Fwd: a", TextBody: "fwd", Forwarding: a.ID})
	save(&Draft{Subject: "new", TextBody: "new"})
	save(&Draft{Subject: "Re: gone", TextBody: "gone", InReplyTo: "m_nope"})
	toOther := save(&Draft{Subject: "Re: o", TextBody: "other", InReplyTo: other.ID})
	suggested := save(&Draft{Subject: "Re: a", TextBody: "suggested", InReplyTo: a.ID})
	drainAll(t, s, boardNow, testDecider)
	if _, err := s.SetBoardDraft(ctx, caseOf(t, s, "acc", tid).ID, suggested.ID); err != nil {
		t.Fatal(err)
	}

	items, _, _, err := s.ListThreads(ctx, "acc", inbox.ID, "", 0, "", "")
	if err != nil || len(items) != 2 {
		t.Fatalf("inbox threads: %+v %v", items, err)
	}
	counts := map[string]int{}
	for _, it := range items {
		counts[it.ID] = it.DraftCount
	}
	if counts[tid] != 2 || counts[threadOf(t, s, other)] != 1 {
		t.Errorf("draft counts = %v", counts)
	}
	got, err := s.ThreadDrafts(ctx, "acc", tid, inbox.ID, 0)
	if err != nil || len(got) != 2 {
		t.Fatalf("thread drafts = %+v %v", got, err)
	}
	ids := map[string]bool{got[0].ID: true, got[1].ID: true}
	if !ids[toA.ID] || !ids[toR.ID] || got[0].TextBody == "" {
		t.Errorf("thread drafts = %+v", got)
	}
	if !got[0].UpdatedAt.After(got[1].UpdatedAt) && !got[0].UpdatedAt.Equal(got[1].UpdatedAt) {
		t.Errorf("not newest first: %v %v", got[0].UpdatedAt, got[1].UpdatedAt)
	}
	if capped, err := s.ThreadDrafts(ctx, "acc", tid, inbox.ID, 1); err != nil || len(capped) != 1 {
		t.Errorf("capped thread drafts = %+v %v", capped, err)
	}
	row, err := s.GetThread(ctx, "acc", tid, inbox.ID)
	if err != nil || row.DraftCount != 2 || row.MessageCount != 1 {
		t.Fatalf("thread in the inbox: %+v %v", row, err)
	}
	// The sent folder is an ordinary folder for drafts.
	if row, err := s.GetThread(ctx, "acc", tid, sent.ID); err != nil || row.DraftCount != 2 {
		t.Errorf("thread in Sent: %+v %v", row, err)
	}
	// The Drafts folder (the copy is its member) and account-wide: none.
	if row, err := s.GetThread(ctx, "acc", tid, draftsFolder.ID); err != nil || row.DraftCount != 0 {
		t.Errorf("thread in Drafts: %+v %v", row, err)
	}
	if got, err := s.ThreadDrafts(ctx, "acc", tid, draftsFolder.ID, 0); err != nil || len(got) != 0 {
		t.Errorf("drafts in Drafts: %+v %v", got, err)
	}
	if ok, err := s.draftScope(ctx, "acc", outbox.ID); err != nil || ok {
		t.Errorf("outbox scope: %v %v", ok, err)
	}
	if row, err := s.GetThread(ctx, "acc", tid, ""); err != nil || row.DraftCount != 0 {
		t.Errorf("account-wide: %+v %v", row, err)
	}
	if got, err := s.ThreadDrafts(ctx, "acc", "t_nope", inbox.ID, 0); err != nil || len(got) != 0 {
		t.Errorf("unknown thread: %+v %v", got, err)
	}
	if _, err := s.ThreadDrafts(ctx, "acc", tid, "f_nope", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown folder: %v", err)
	}

	if got, err := s.DraftThread(ctx, "acc", toOther.InReplyTo); err != nil || got != threadOf(t, s, other) {
		t.Errorf("draft thread = %q %v", got, err)
	}
	for _, id := range []string{"", "m_nope"} {
		if got, err := s.DraftThread(ctx, "acc", id); err != nil || got != "" {
			t.Errorf("draft thread of %q = %q %v", id, got, err)
		}
	}

	// A virtual folder (an issue tracker's view) shows its threads' drafts.
	views, _, err := s.UpsertFolders(ctx, "jira", []Folder{
		{Mailbox: "view:assignedToMe", Name: "Mine", Path: "Mine", Virtual: api.VirtualAssignedToMe, Selectable: true, Subscribed: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.draftScope(ctx, "jira", views[0].ID); err != nil || !ok {
		t.Errorf("virtual folder scope: %v %v", ok, err)
	}
}
