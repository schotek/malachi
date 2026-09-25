// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"errors"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestInDrafts(t *testing.T) {
	m := mailModel{
		accounts: []api.Account{{ID: "a", Enabled: true}},
		folders: map[api.AccountID][]api.Folder{"a": {
			{ID: "in", Path: "INBOX", Role: api.RoleInbox, Selectable: true},
			{ID: "dr", Path: "Drafts", Role: api.RoleDrafts, Selectable: true},
		}},
	}
	if m.inDrafts(api.MessageSummary{ID: "1", AccountID: "a", FolderID: "in"}) {
		t.Error("inbox message reported as a draft")
	}
	if !m.inDrafts(api.MessageSummary{ID: "2", AccountID: "a", FolderID: "dr"}) {
		t.Error("message of the Drafts folder not recognised")
	}
	// The folder of another account with the same id is not this one.
	if m.inDrafts(api.MessageSummary{ID: "3", AccountID: "b", FolderID: "dr"}) {
		t.Error("draft of an unknown account")
	}
}

func TestDraftOpenErrors(t *testing.T) {
	if !draftOpenUnsupported(api.NewError(api.CodeMethodNotFound, "x")) || !draftOpenUnsupported(api.NewError(api.CodeNotImplemented, "x")) {
		t.Error("an old daemon is not recognised")
	}
	if draftOpenUnsupported(api.NewError(api.CodeUnavailable, "x")) || draftOpenUnsupported(errors.New("x")) {
		t.Error("an ordinary failure taken for an old daemon")
	}
	if got := draftOpenErrorText(api.NewError(api.CodeUnavailable, "not downloaded")); got != "The draft has not been downloaded yet; try again in a moment" {
		t.Errorf("unavailable = %q", got)
	}
	if got := draftOpenErrorText(api.NewError(api.CodeMessageNotFound, "gone")); got != "Opening the draft failed" {
		t.Errorf("not found = %q", got)
	}
	if got := skippedText(2); got != "2 attachments of the draft could not be opened" {
		t.Errorf("skipped = %q", got)
	}
}
