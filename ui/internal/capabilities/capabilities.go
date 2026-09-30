// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package capabilities decides which message actions a client offers for
// an account (Account.Capabilities, docs/api.md): a mail account (imap,
// graph) can do everything, an issue-tracker account (jira) only what its
// capabilities list, for example comment instead of reply, and forward
// into a mail account. Seen and flagged work on every account and are not
// decided here. Which accounts write mail at all (the From list, New
// Message) is ComposeAccounts and CanComposeNew.
//
// The rules extend those of the message toolbar (ui/internal/window
// actions.go setMessageActionsSensitive): archive and junk need the
// account's role folder, and a queued message in the outbox keeps reply,
// forward and trash (which cancels the send). The package is pure and
// holds no text; macOS MalachiCore (Model/Capabilities.swift) and later
// Windows Malachi.Core port it one to one.
package capabilities

import "github.com/schotek/malachi/backend/pkg/api"

// Can reports whether account a has capability c. nil capabilities (a
// daemon from before capabilities) mean api.MailCapabilities; an empty
// list means none. (api.Account.Can; the Swift port is Account.can.)
func Can(a api.Account, c api.AccountCapability) bool {
	return a.Can(c)
}

// Situation is what the rules look at.
type Situation struct {
	// Account is the selected row's account; with nothing selected, the
	// account of the listed folder. The zero value (no folder listed) has
	// nil capabilities, the mail default.
	Account api.Account
	// Selected says a row is selected and the actions are on at all.
	Selected bool
	// Outbox says the row is a queued message in the account's outbox
	// (with nothing selected: the listed folder is the outbox).
	Outbox bool
	// Archive and Junk say the account has such a role folder and the row
	// is not in it already (window canMoveToRole).
	Archive, Junk bool
	// ComposeAccount says some enabled account can compose
	// (ForwardAccounts is not empty): the forward of an issue goes out
	// from a mail account.
	ComposeAccount bool
}

// Actions are the message actions, each true when offered.
type Actions struct {
	Reply, ReplyAll, Forward bool
	Move, Trash              bool
	Archive, Junk            bool
	// Comment says Reply writes a comment on the issue: a client labels
	// it "Comment" (jira ReplyLabel).
	Comment bool
}

// Supported are the actions the account offers at all, whatever is
// selected: a client hides the others (or disables them where it cannot
// hide), and labels Reply by Comment. Reply needs reply or comment,
// Forward needs forward and an account to send from (the account itself,
// or another enabled one that composes), Trash needs delete except in the
// outbox (cancelling a queued message), Move, Archive and Junk need move.
func Supported(s Situation) Actions {
	a := s.Account
	move := Can(a, api.CapabilityMove)
	return Actions{
		Reply:    Can(a, api.CapabilityReply) || Can(a, api.CapabilityComment),
		ReplyAll: Can(a, api.CapabilityReplyAll),
		Forward:  Can(a, api.CapabilityForward) && (Can(a, api.CapabilityCompose) || s.ComposeAccount),
		Move:     move,
		Trash:    Can(a, api.CapabilityDelete) || s.Outbox,
		Archive:  move,
		Junk:     move,
		Comment:  Can(a, api.CapabilityComment),
	}
}

// Available are the actions enabled now: the Supported ones while a row is
// selected, archive and junk only with the role folder, and for a queued
// message only reply, reply all, forward and trash (the daemon refuses
// moves in the outbox). Comment is Supported's, selected or not.
func Available(s Situation) Actions {
	sup := Supported(s)
	on := s.Selected
	return Actions{
		Reply:    on && sup.Reply,
		ReplyAll: on && sup.ReplyAll,
		Forward:  on && sup.Forward,
		Move:     on && sup.Move && !s.Outbox,
		Trash:    on && sup.Trash,
		Archive:  on && sup.Archive && !s.Outbox && s.Archive,
		Junk:     on && sup.Junk && !s.Outbox && s.Junk,
		Comment:  sup.Comment,
	}
}

// ForwardAccounts are the accounts a message can be forwarded from: the
// enabled ones that compose, in the given order.
func ForwardAccounts(accounts []api.Account) []api.Account {
	var out []api.Account
	for _, a := range accounts {
		if a.Enabled && Can(a, api.CapabilityCompose) {
			out = append(out, a)
		}
	}
	return out
}

// ForwardFrom picks the account a message of account from is forwarded
// from: from itself when it is enabled and composes, else the first of
// ForwardAccounts. false when there is none. When the result is not from,
// draft.create names from as DraftCreateParams.MessageAccountID.
func ForwardFrom(accounts []api.Account, from api.AccountID) (api.Account, bool) {
	list := ForwardAccounts(accounts)
	for _, a := range list {
		if a.ID == from {
			return a, true
		}
	}
	if len(list) == 0 {
		return api.Account{}, false
	}
	return list[0], true
}

// ComposeAccounts are the accounts a message is written from (the compose
// window's From list): those that compose, in the given order, paused ones
// included as before. An account that only comments (an issue tracker)
// writes in the comment mode of the window instead, pinned to itself.
func ComposeAccounts(accounts []api.Account) []api.Account {
	var out []api.Account
	for _, a := range accounts {
		if Can(a, api.CapabilityCompose) {
			out = append(out, a)
		}
	}
	return out
}

// CanComposeNew reports whether New Message is offered: while no account
// is known (the compose window writes from a placeholder identity until
// the list arrives) or when some account composes; never for issue-tracker
// accounts alone.
func CanComposeNew(accounts []api.Account) bool {
	return len(accounts) == 0 || len(ComposeAccounts(accounts)) > 0
}
