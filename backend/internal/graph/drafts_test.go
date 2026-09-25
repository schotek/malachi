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
