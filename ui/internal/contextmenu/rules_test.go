// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contextmenu

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Create Rule from Message… is a group of its own before the Assistant,
// for one message outside the outbox and the drafts only.
func TestMessageItemsRule(t *testing.T) {
	base := MessageState{OfferRule: true, ToggleFlag: true, Assistant: true}
	if got := show(MessageItems(base)); got != "open | toggleFlag | createRule | assistant" {
		t.Errorf("one message: %s", got)
	}
	several := base
	several.Count = 2
	several.Open = true
	if got := show(MessageItems(several)); got != "open | toggleFlag | assistant" {
		t.Errorf("several: %s", got)
	}
	draft := base
	draft.Draft = true
	if got := show(MessageItems(draft)); got != "edit | toggleFlag | assistant" {
		t.Errorf("draft: %s", got)
	}
	outbox := base
	outbox.Outbox = true
	if got := show(MessageItems(outbox)); got != "open | toggleFlag | trash- | assistant" {
		t.Errorf("outbox: %s", got)
	}
	off := base
	off.OfferRule = false
	if got := show(MessageItems(off)); got != "open | toggleFlag | assistant" {
		t.Errorf("not offered: %s", got)
	}
}

// Apply Rules… is in the acting group of a mail folder, disabled without
// a rule for the account or without messages.
func TestFolderItemsRules(t *testing.T) {
	cases := []struct {
		name string
		f    FolderState
		want string
	}{
		{"inbox with rules", FolderState{Selectable: true, Role: api.RoleInbox, Total: 5, OfferRules: true, Rules: true},
			"checkMail | markAllRead- applyRules | favourite"},
		{"no rule for the account", FolderState{Selectable: true, Role: api.RoleInbox, Total: 5, OfferRules: true},
			"checkMail | markAllRead- applyRules- | favourite"},
		{"empty folder", FolderState{Selectable: true, Role: api.RoleNone, OfferRules: true, Rules: true},
			"checkMail | markAllRead- applyRules- | favourite"},
		{"outbox", FolderState{Selectable: true, Role: api.RoleOutbox, Total: 1, OfferRules: true, Rules: true},
			"favourite"},
		{"drafts", FolderState{Selectable: true, Role: api.RoleDrafts, Total: 1, OfferRules: true, Rules: true},
			"checkMail | markAllRead- | favourite"},
		{"issue tracker", FolderState{Selectable: true, Role: api.RoleNone, Total: 1, Rules: true},
			"checkMail | markAllRead- | favourite"},
	}
	for _, c := range cases {
		if got := show(FolderItems(c.f)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRulesLabels(t *testing.T) {
	if got := Label(identity{}, CreateRule, Labels{}); got != "Create Rule from Message…" {
		t.Errorf("create: %q", got)
	}
	if got := Label(identity{}, ApplyRules, Labels{}); got != "Apply Rules…" {
		t.Errorf("apply: %q", got)
	}
}
