// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"testing"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// seedFolders stores folders for the account and returns them keyed by
// mailbox.
func seedFolders(t *testing.T, b *Backend, accountID string, folders []store.Folder) map[string]store.Folder {
	t.Helper()
	stored, _, err := b.store.UpsertFolders(context.Background(), accountID, folders)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]store.Folder, len(stored))
	for _, f := range stored {
		out[f.Mailbox] = f
	}
	return out
}

func TestFolderListFilterAndOrder(t *testing.T) {
	b, _ := newSyncBackend(t)
	ctx := context.Background()
	acc := seedAccount(t, b, "me@example.invalid")

	// Before the first sync: empty, not an error, and a non-nil slice.
	res, err := b.Folders().List(ctx, api.FolderListParams{AccountID: api.AccountID(acc)})
	if err != nil || res.Folders == nil || len(res.Folders) != 0 {
		t.Fatalf("empty account: %+v, %v", res, err)
	}

	seedFolders(t, b, acc, []store.Folder{
		{Mailbox: "Zeta", Name: "Zeta", Path: "Zeta", Subscribed: true, Selectable: true},
		{Mailbox: "Trash", Name: "Trash", Path: "Trash", Role: api.RoleTrash, Subscribed: false, Selectable: true},
		{Mailbox: "Alpha", Name: "Alpha", Path: "Alpha", Subscribed: true, Selectable: true},
		{Mailbox: "Alpha/Sub", ParentMailbox: "Alpha", Name: "Sub", Path: "Alpha/Sub", Subscribed: false, Selectable: true},
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Sent", Name: "Sent", Path: "Sent", Role: api.RoleSent, Subscribed: true, Selectable: true},
		{Mailbox: "Drafts", Name: "Drafts", Path: "Drafts", Role: api.RoleDrafts, Subscribed: true, Selectable: true},
		{Mailbox: "Container", Name: "Container", Path: "Container", Subscribed: true, Selectable: false},
		{Mailbox: "Junk", Name: "Junk", Path: "Junk", Role: api.RoleJunk, Subscribed: true, Selectable: true},
		{Mailbox: "Archive", Name: "Archive", Path: "Archive", Role: api.RoleArchive, Subscribed: true, Selectable: true},
	})

	paths := func(fs []api.Folder) []string {
		out := make([]string, 0, len(fs))
		for _, f := range fs {
			out = append(out, f.Path)
		}
		return out
	}
	equal := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	res, err = b.Folders().List(ctx, api.FolderListParams{AccountID: api.AccountID(acc)})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Inbox", "Drafts", "Sent", "Archive", "Junk", "Trash", "Alpha", "Container", "Zeta"}
	if got := paths(res.Folders); !equal(got, want) {
		t.Fatalf("subscribed list = %v, want %v", got, want)
	}

	res, err = b.Folders().List(ctx, api.FolderListParams{AccountID: api.AccountID(acc), IncludeUnsubscribed: true})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"Inbox", "Drafts", "Sent", "Archive", "Junk", "Trash", "Alpha", "Alpha/Sub", "Container", "Zeta"}
	if got := paths(res.Folders); !equal(got, want) {
		t.Fatalf("full list = %v, want %v", got, want)
	}

	var sub, alpha, container api.Folder
	for _, f := range res.Folders {
		switch f.Path {
		case "Alpha/Sub":
			sub = f
		case "Alpha":
			alpha = f
		case "Container":
			container = f
		}
	}
	if sub.ParentID != alpha.ID || sub.Name != "Sub" || sub.Subscribed || !sub.Selectable {
		t.Fatalf("sub = %+v (alpha id %s)", sub, alpha.ID)
	}
	if container.Selectable || container.Role != api.RoleNone || container.AccountID != api.AccountID(acc) {
		t.Fatalf("container = %+v", container)
	}
}

func TestFolderListErrors(t *testing.T) {
	b, _ := newSyncBackend(t)
	ctx := context.Background()
	if _, err := b.Folders().List(ctx, api.FolderListParams{}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("missing account: %v", err)
	}
	if _, err := b.Folders().List(ctx, api.FolderListParams{AccountID: "acc_nope"}); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("unknown account: %v", err)
	}
	if _, err := b.Folders().Subscribe(ctx, api.FolderSubscribeParams{AccountID: "acc_x", FolderID: "f_x"}); errCode(t, err) != api.CodeNotImplemented {
		t.Fatalf("subscribe: %v", err)
	}
}
