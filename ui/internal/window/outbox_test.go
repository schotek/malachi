// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestOutboxBannerText(t *testing.T) {
	cases := []struct {
		name          string
		info          *api.OutboxInfo
		title, button string
		shown         bool
	}{
		{"not in the outbox", nil, "", "", false},
		{"sent", &api.OutboxInfo{State: api.OutboxSent}, "", "", false},
		{"unknown state", &api.OutboxInfo{State: "bogus"}, "", "", false},
		{"queued", &api.OutboxInfo{State: api.OutboxQueued}, "Queued for sending", "", true},
		{"queued after a failure keeps the error quiet", &api.OutboxInfo{State: api.OutboxQueued, Attempts: 2, Error: &api.Error{Code: api.CodeNetworkError, Message: "dial"}}, "Queued for sending", "", true},
		{"sending", &api.OutboxInfo{State: api.OutboxSending, Attempts: 1}, "Sending…", "", true},
		{"failed with a known code", &api.OutboxInfo{State: api.OutboxFailed, Error: &api.Error{Code: api.CodeAuthFailed, Message: "535"}}, "Sending the message failed: the server rejected the user name or password", "Retry", true},
		{"failed with an unknown code", &api.OutboxInfo{State: api.OutboxFailed, Error: &api.Error{Code: api.CodeInternalError, Message: "boom"}}, "Sending the message failed", "Retry", true},
		{"failed without an error", &api.OutboxInfo{State: api.OutboxFailed}, "Sending the message failed", "Retry", true},
	}
	for _, c := range cases {
		title, button, shown := outboxBannerText(c.info)
		if title != c.title || button != c.button || shown != c.shown {
			t.Errorf("%s: got %q/%q/%v, want %q/%q/%v", c.name, title, button, shown, c.title, c.button, c.shown)
		}
	}
}

func TestInOutbox(t *testing.T) {
	m := mailModel{
		accounts: []api.Account{{ID: "a", Enabled: true}},
		folders: map[api.AccountID][]api.Folder{"a": {
			{ID: "in", Path: "INBOX", Role: api.RoleInbox, Selectable: true},
			{ID: "out", Path: "Outbox", Role: api.RoleOutbox, Selectable: true},
		}},
	}
	inbox := api.MessageSummary{ID: "1", AccountID: "a", FolderID: "in"}
	if m.inOutbox(inbox) {
		t.Error("inbox message reported in the outbox")
	}
	// The folder role alone is enough (a list summary from the outbox).
	byFolder := api.MessageSummary{ID: "2", AccountID: "a", FolderID: "out"}
	if !m.inOutbox(byFolder) {
		t.Error("message in the outbox folder not recognised")
	}
	// So is the delivery state alone (folders not loaded yet).
	byInfo := api.MessageSummary{ID: "3", AccountID: "a", FolderID: "gone", Outbox: &api.OutboxInfo{State: api.OutboxQueued}}
	if !m.inOutbox(byInfo) {
		t.Error("message with delivery state not recognised")
	}
	if (&mailModel{}).inOutbox(inbox) {
		t.Error("empty model reported the outbox")
	}
}

func TestTrashTooltip(t *testing.T) {
	if got := trashTooltip(true); got != "Cancel Sending" {
		t.Errorf("outbox: %q", got)
	}
	if got := trashTooltip(false); got != "Move to Trash" {
		t.Errorf("plain: %q", got)
	}
}
