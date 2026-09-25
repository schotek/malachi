// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// draftBuilder is a BuildDraft over the harness store: a plain message
// under a fresh Message-ID for every call, the subject the draft's.
func draftBuilder(h **harness) func(ctx context.Context, draftID string) (store.DraftUpload, error) {
	var n atomic.Int32
	return func(ctx context.Context, draftID string) (store.DraftUpload, error) {
		d, err := (*h).st.GetDraft(ctx, (*h).acc.ID, draftID)
		if err != nil {
			return store.DraftUpload{}, err
		}
		id := fmt.Sprintf("draft-%d-v%d@example.test", n.Add(1), d.Version)
		raw := fmt.Sprintf("From: me@example.test\r\nSubject: %s\r\nMessage-ID: <%s>\r\nDate: Mon, 01 Sep 2026 10:00:00 +0000\r\nContent-Type: text/plain\r\n\r\n%s\r\n",
			d.Subject, id, d.TextBody)
		return store.DraftUpload{DraftID: d.ID, Version: d.Version, RFCMessageID: id, Date: d.UpdatedAt, Raw: []byte(raw)}, nil
	}
}

// saveDraft stores a new version of d.
func (h *harness) saveDraft(d *store.Draft) {
	h.t.Helper()
	if err := h.st.SaveDraft(context.Background(), d, nil); err != nil {
		h.t.Fatal(err)
	}
}

// serverSubjects lists the subjects in a mailbox on the server.
func (h *harness) serverSubjects(mailbox string) []string {
	h.t.Helper()
	c := h.client()
	defer func() { c.Logout().Wait() }()
	if _, err := c.Select(mailbox, nil).Wait(); err != nil {
		h.t.Fatal(err)
	}
	msgs, err := c.Fetch(imap.SeqSetNum(1, 2, 3, 4, 5, 6, 7, 8, 9), &imap.FetchOptions{Envelope: true}).Collect()
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, m := range msgs {
		out = append(out, m.Envelope.Subject)
	}
	return out
}

// TestDraftCopyStoredAndReplaced: a saved draft is appended to the Drafts
// folder with \Draft, a new version replaces it (never two copies), the
// folder's pass brings the copy back, and deleting the draft deletes the
// copy — with UIDPLUS (APPENDUID) and without (the local row).
func TestDraftCopyStoredAndReplaced(t *testing.T) {
	for name, caps := range map[string]imap.CapSet{"uidplus": fullCaps, "rev1": rev1Caps} {
		t.Run(name, func(t *testing.T) {
			var h *harness
			h = newHarness(t, harnessOptions{caps: caps, buildDraft: draftBuilder(&h)})
			if err := h.user.Create("Drafts", nil); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			h.start()
			h.waitIdle(start)
			drafts := h.folder("drafts")

			d := store.Draft{AccountID: h.acc.ID, Subject: "first", TextBody: "one"}
			h.saveDraft(&d)
			h.syncer.Trigger(api.FolderID(drafts.ID), false)
			waitFor(t, "first copy fetched", func() bool {
				msgs := h.messages(drafts.ID)
				return len(msgs) == 1 && msgs[0].Subject == "first"
			})
			uids := h.serverUIDs("Drafts")
			if len(uids) != 1 {
				t.Fatalf("server drafts = %v", uids)
			}
			if fl := h.serverFlags("Drafts", uids[0]); !hasIMAPFlag(fl, imap.FlagDraft) || !hasIMAPFlag(fl, imap.FlagSeen) {
				t.Fatalf("server flags = %v", fl)
			}
			got, _ := h.st.GetDraft(context.Background(), h.acc.ID, d.ID)
			if got.SyncedVersion != 1 || got.Copy.RFCMessageID == "" {
				t.Fatalf("draft after upload = %+v", got)
			}
			if name == "uidplus" && got.Copy.UID != uids[0] {
				t.Fatalf("copy uid %d, server %v", got.Copy.UID, uids)
			}
			if n := h.notes.newMessages(); len(n) != 0 {
				t.Fatalf("draft copy notified: %+v", n)
			}

			d.Subject = "second"
			h.saveDraft(&d)
			h.syncer.Trigger(api.FolderID(drafts.ID), false)
			waitFor(t, "second copy replaced the first", func() bool {
				msgs := h.messages(drafts.ID)
				return len(msgs) == 1 && msgs[0].Subject == "second"
			})
			waitFor(t, "one copy on the server", func() bool {
				subjects := h.serverSubjects("Drafts")
				return len(subjects) == 1 && subjects[0] == "second"
			})

			if err := h.st.DeleteDraft(context.Background(), h.acc.ID, d.ID); err != nil {
				t.Fatal(err)
			}
			h.syncer.Trigger(api.FolderID(drafts.ID), false)
			waitFor(t, "copy deleted", func() bool { return len(h.serverUIDs("Drafts")) == 0 })
			if msgs := h.messages(drafts.ID); len(msgs) != 0 {
				t.Fatalf("local copy after delete: %+v", msgs)
			}
		})
	}
}

// TestDraftBuildFailureStaysLocal: a draft that cannot be built is
// recorded on the draft and retried later; the pass goes on.
func TestDraftBuildFailureStaysLocal(t *testing.T) {
	var h *harness
	h = newHarness(t, harnessOptions{buildDraft: func(context.Context, string) (store.DraftUpload, error) {
		return store.DraftUpload{}, errors.New("attachment file missing")
	}})
	if err := h.user.Create("Drafts", nil); err != nil {
		t.Fatal(err)
	}
	d := store.Draft{AccountID: h.acc.ID, Subject: "broken"}
	h.saveDraft(&d)
	start := time.Now()
	h.start()
	h.waitIdle(start)
	got, _ := h.st.GetDraft(context.Background(), h.acc.ID, d.ID)
	if got.SyncAttempts != 1 || got.SyncedVersion != 0 {
		t.Fatalf("draft after a failed build = %+v", got)
	}
	if uids := h.serverUIDs("Drafts"); len(uids) != 0 {
		t.Fatalf("server drafts = %v", uids)
	}
}

// TestDraftWithoutDraftsFolder: without a drafts folder nothing is
// uploaded and the draft stays due for when one appears.
func TestDraftWithoutDraftsFolder(t *testing.T) {
	var h *harness
	h = newHarness(t, harnessOptions{buildDraft: draftBuilder(&h)})
	d := store.Draft{AccountID: h.acc.ID, Subject: "local"}
	h.saveDraft(&d)
	start := time.Now()
	h.start()
	h.waitIdle(start)
	got, _ := h.st.GetDraft(context.Background(), h.acc.ID, d.ID)
	if got.SyncedVersion != 0 || got.SyncAttempts != 0 {
		t.Fatalf("draft = %+v", got)
	}
	if names := h.serverMailboxes(); len(names) != 1 {
		t.Fatalf("server mailboxes = %v", names)
	}
}

// TestGmailPermanentDeleteGoesThroughTrash: on Gmail an expunge only
// drops a label (the message stays in All Mail), so a permanent delete
// moves the message to the Trash and expunges it there.
func TestGmailPermanentDeleteGoesThroughTrash(t *testing.T) {
	h := newHarness(t, harnessOptions{gmail: true})
	if err := h.user.Create("Trash", nil); err != nil {
		t.Fatal(err)
	}
	h.append("INBOX", rawMessage("g1", "gone", "x"), time.Now())
	start := time.Now()
	h.start()
	h.waitIdle(start)
	inbox := h.folder("inbox")
	msgs := h.messages(inbox.ID)
	if len(msgs) != 1 {
		t.Fatalf("inbox = %+v", msgs)
	}
	if err := h.st.DeleteMessages(context.Background(), h.acc.ID, []string{msgs[0].ID}); err != nil {
		t.Fatal(err)
	}
	h.syncer.Trigger("", false)
	waitFor(t, "message gone from INBOX", func() bool { return len(h.serverUIDs("INBOX")) == 0 })
	if uids := h.serverUIDs("Trash"); len(uids) != 0 {
		t.Fatalf("copy left in the Trash: %v", uids)
	}
	// It went through the Trash, not a plain expunge.
	c := h.client()
	defer func() { c.Logout().Wait() }()
	st, err := c.Status("Trash", &imap.StatusOptions{UIDNext: true}).Wait()
	if err != nil {
		t.Fatal(err)
	}
	if st.UIDNext != 2 {
		t.Fatalf("Trash UIDNEXT = %d, the message never passed through", st.UIDNext)
	}
}
