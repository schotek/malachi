// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// useDraftBuilder gives the harness a BuildDraft over its store: a plain
// message under a fresh Message-ID for every call.
func (h *harness) useDraftBuilder() {
	var n atomic.Int32
	h.buildDraft = func(ctx context.Context, draftID string) (store.DraftUpload, error) {
		d, err := h.st.GetDraft(ctx, h.acc.ID, draftID)
		if err != nil {
			return store.DraftUpload{}, err
		}
		id := fmt.Sprintf("draft-%d@contoso.invalid", n.Add(1))
		raw := fmt.Sprintf("From: me@contoso.invalid\r\nSubject: %s\r\nMessage-ID: <%s>\r\nContent-Type: text/plain\r\n\r\n%s\r\n",
			d.Subject, id, d.TextBody)
		return store.DraftUpload{DraftID: d.ID, Version: d.Version, RFCMessageID: id, Raw: []byte(raw)}, nil
	}
	h.newSyncer()
}

func (h *harness) saveDraft(d *store.Draft) {
	h.t.Helper()
	if err := h.st.SaveDraft(context.Background(), d, nil); err != nil {
		h.t.Fatal(err)
	}
}

func subjects(msgs []fakeMsg) string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.subject)
	}
	return strings.Join(out, ",")
}

// TestDraftCopyCreatedAndReplaced: a saved draft is created in Drafts
// from its MIME, a new version replaces it, a copy Outlook changed in
// place is kept next to the new one, and deleting the draft deletes its
// copy.
func TestDraftCopyCreatedAndReplaced(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	h.useDraftBuilder()
	start := time.Now()
	h.start()
	h.waitIdle(start)
	drafts := h.folder("drafts")

	d := store.Draft{AccountID: h.acc.ID, Subject: "first", TextBody: "one"}
	h.saveDraft(&d)
	h.pass(api.FolderID(drafts.ID), false)
	server := h.fake.drafts()
	if len(server) != 1 || server[0].subject != "first" || !server[0].isDraft {
		t.Fatalf("server drafts = %+v", server)
	}
	got, _ := h.st.GetDraft(context.Background(), h.acc.ID, d.ID)
	if got.SyncedVersion != 1 || got.Copy.RemoteID != server[0].id || got.Copy.FolderID != drafts.ID {
		t.Fatalf("draft after upload = %+v", got)
	}
	waitFor(t, "copy synchronised down", func() bool {
		msgs := h.messages(drafts.ID)
		return len(msgs) == 1 && msgs[0].RemoteID == server[0].id
	})

	d.Subject = "second"
	h.saveDraft(&d)
	h.pass(api.FolderID(drafts.ID), false)
	if s := subjects(h.fake.drafts()); s != "second" {
		t.Fatalf("server drafts after replace = %s", s)
	}
	waitFor(t, "local copy replaced", func() bool {
		msgs := h.messages(drafts.ID)
		return len(msgs) == 1 && msgs[0].Subject == "second"
	})

	// Changed in place elsewhere: both stay.
	current := h.fake.drafts()[0].id
	h.fake.edit(current)
	d.Subject = "third"
	h.saveDraft(&d)
	h.pass(api.FolderID(drafts.ID), false)
	if s := subjects(h.fake.drafts()); s != "second,third" {
		t.Fatalf("server drafts after an edit elsewhere = %s", s)
	}

	if err := h.st.DeleteDraft(context.Background(), h.acc.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	h.pass(api.FolderID(drafts.ID), false)
	if s := subjects(h.fake.drafts()); s != "second" {
		t.Fatalf("server drafts after delete = %s", s)
	}
}

// TestLocalDraftNeverCreated: a local draft (a board case's suggested
// reply) is never created in Drafts; a draft that becomes local while its
// upload runs has its new copy deleted in the same pass; a local draft
// that still records a copy (linked before migration 0018) loses it at the
// next pass.
func TestLocalDraftNeverCreated(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	h.useDraftBuilder()
	build := h.buildDraft
	var raced string
	h.buildDraft = func(ctx context.Context, id string) (store.DraftUpload, error) {
		up, err := build(ctx, id)
		if id == raced {
			if _, err := h.st.DB().Exec(`UPDATE drafts SET local = 1 WHERE id = ?`, id); err != nil {
				t.Error(err)
			}
		}
		return up, err
	}
	h.newSyncer()
	start := time.Now()
	h.start()
	h.waitIdle(start)
	drafts := h.folder("drafts")

	local := store.Draft{AccountID: h.acc.ID, Subject: "local", TextBody: "board", Local: true}
	h.saveDraft(&local)
	race := store.Draft{AccountID: h.acc.ID, Subject: "raced", TextBody: "linked mid-upload"}
	h.saveDraft(&race)
	raced = race.ID
	old := store.Draft{AccountID: h.acc.ID, Subject: "old", TextBody: "linked before 0018"}
	h.saveDraft(&old)
	h.pass(api.FolderID(drafts.ID), false)
	if s := subjects(h.fake.drafts()); s != "old" {
		t.Fatalf("server drafts = %s", s)
	}
	for _, id := range []string{local.ID, race.ID} {
		got, _ := h.st.GetDraft(context.Background(), h.acc.ID, id)
		if !got.Local || !got.Copy.IsZero() || got.SyncedVersion != 0 {
			t.Fatalf("local draft after the pass = %+v", got)
		}
	}
	h.strayAsMigration(old.ID)
	h.pass(api.FolderID(drafts.ID), false)
	if s := subjects(h.fake.drafts()); s != "" {
		t.Fatalf("server drafts after the next pass = %s", s)
	}
	if got, _ := h.st.GetDraft(context.Background(), h.acc.ID, old.ID); !got.Local || !got.Copy.IsZero() {
		t.Fatalf("old local draft = %+v", got)
	}
}

// strayAsMigration makes a draft local the way migrations 0018 and 0019 (and a link,
// for a Graph copy) does: local and edited, its copy moved to
// draft_stray_copies with the time it was stored.
func (h *harness) strayAsMigration(id string) {
	h.t.Helper()
	for _, q := range []string{
		`INSERT INTO draft_stray_copies (account_id, folder_id, uidvalidity, uid, remote_id, rfc_message_id, synced_at, recorded_at)
			SELECT account_id, server_folder_id, server_uidvalidity, server_uid, server_remote_id, rfc_message_id, synced_at, synced_at
			FROM drafts WHERE id = ?`,
		`UPDATE drafts SET local = 1, edited = 1, rfc_message_id = '', server_folder_id = '', server_uidvalidity = 0,
			server_uid = 0, server_remote_id = '', synced_version = 0 WHERE id = ?`,
	} {
		if _, err := h.st.DB().Exec(q, id); err != nil {
			h.t.Fatal(err)
		}
	}
}

// TestLocalDraftKeepsOutlookEdit (D4): a draft whose copy Outlook changed
// in place after the upload becomes local (linked to a board case): the
// pass asks the service, and the changed copy stays in Drafts as the
// user's own — not deleted, no longer tracked — while the draft stays
// local and is never uploaded.
func TestLocalDraftKeepsOutlookEdit(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	h.useDraftBuilder()
	start := time.Now()
	h.start()
	h.waitIdle(start)
	drafts := h.folder("drafts")

	d := store.Draft{AccountID: h.acc.ID, Subject: "started here", TextBody: "one"}
	h.saveDraft(&d)
	h.pass(api.FolderID(drafts.ID), false)
	server := h.fake.drafts()
	if len(server) != 1 {
		t.Fatalf("server drafts = %+v", server)
	}
	h.fake.edit(server[0].id) // finished in Outlook on the web
	h.strayAsMigration(d.ID)
	h.pass(api.FolderID(drafts.ID), false)
	if s := subjects(h.fake.drafts()); s != "started here" {
		t.Fatalf("server drafts after the pass = %q: the copy changed in Outlook was deleted", s)
	}
	var strays int
	if err := h.st.DB().QueryRow(`SELECT COUNT(*) FROM draft_stray_copies`).Scan(&strays); err != nil || strays != 0 {
		t.Fatalf("stray copies after the check: %d %v", strays, err)
	}
	if got, _ := h.st.GetDraft(context.Background(), h.acc.ID, d.ID); !got.Local || !got.Copy.IsZero() {
		t.Fatalf("draft after the pass = %+v", got)
	}
	h.pass(api.FolderID(drafts.ID), false)
	if s := subjects(h.fake.drafts()); s != "started here" {
		t.Fatalf("server drafts after another pass = %q", s)
	}
}

// TestStrayCopyFailureDoesNotStopThePass: a storage error while deleting
// stray draft copies is logged, and the pass goes on (the draft uploads).
func TestStrayCopyFailureDoesNotStopThePass(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	h.useDraftBuilder()
	start := time.Now()
	h.start()
	h.waitIdle(start)
	drafts := h.folder("drafts")
	if _, err := h.st.DB().Exec(`DROP TABLE draft_stray_copies`); err != nil {
		t.Fatal(err)
	}
	d := store.Draft{AccountID: h.acc.ID, Subject: "still uploaded", TextBody: "one"}
	h.saveDraft(&d)
	h.pass(api.FolderID(drafts.ID), false)
	if s := subjects(h.fake.drafts()); s != "still uploaded" {
		t.Fatalf("server drafts = %q", s)
	}
}
