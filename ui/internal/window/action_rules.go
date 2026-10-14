// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/capabilities"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/selection"
)

// The pure rules of the per-message actions (actions.go
// setMessageActionsSensitive applies them to the widgets): which actions
// the selected row allows, as far as its account offers them
// (ui/internal/capabilities: a Jira account has no Reply, Forward or Trash
// of its own, comments instead of replying and forwards from a mail
// account). macOS MalachiCore ports them as ActionRules.swift.

// actionState is what the per-message buttons and actions allow for the
// selected row.
type actionState struct {
	// count: how many rows are selected (selection.go; several combine
	// the rows' states, selection.Combine); open: Open in New Window can
	// open them.
	count int
	open  bool
	// on: a row is selected; outbox: it is a queued message; flagged: the
	// star shows it (a conversation row: any member is flagged).
	on, outbox, flagged bool

	// move: the row can go to another folder of its account (Move To in
	// its context menu).
	reply, replyAll, forward, star, trash, archive, junk, move bool
	markRead, markUnread, toggleFlag, loadImages, trustSender  bool
	// changeStatus: the row's account changes the status of the row's
	// issue (jira.CanTransition, a message of an issue).
	changeStatus bool

	// comment: Reply writes a comment on the issue (the account has the
	// comment capability), labelled "Comment" (jira.ReplyLabel).
	comment bool
	// supported are the actions the account offers at all, whatever is
	// selected; with nothing selected, those of the listed folder's
	// account. A client hides the others where it can.
	supported capabilities.Actions
}

// messageActionState is actionState for row (on false, or no row: nothing
// selected). Archive and junk need the account's role folder and the row
// not in it already; mark read and unread follow the seen flag, a
// conversation row being read when every member is and flagged when any
// is. A queued message of the outbox keeps only reply, forward and trash
// (which cancels the send; the daemon refuses flags and moves). Reply,
// Reply All, Forward, Trash, Archive and Junk need the account's
// capabilities as well (capabilities.Available). With nothing selected
// everything is off, and supported and comment are those of the listed
// folder's account (none listed: the mail default), so that the toolbar of
// a Jira folder does not show Reply before a row is chosen.
func (m *mailModel) messageActionState(row listRow, on bool) actionState {
	if !on {
		return m.idleActionState()
	}
	s := row.Message
	outbox := m.inOutbox(s)
	flagged := hasFlag(s.Flags, api.FlagFlagged)
	seen := hasFlag(s.Flags, api.FlagSeen)
	unread := !seen
	if row.Thread {
		flagged = hasFlag(row.Summary.Flags, api.FlagFlagged)
		unread = row.Summary.UnreadCount > 0
		seen = row.Summary.UnreadCount < row.Summary.MessageCount
	}
	acc, _ := m.account(s.AccountID)
	sit := capabilities.Situation{
		Account:        acc,
		Selected:       true,
		Outbox:         outbox,
		Archive:        m.canMoveToRole(s, api.RoleArchive),
		Junk:           m.canMoveToRole(s, api.RoleJunk),
		ComposeAccount: len(capabilities.ForwardAccounts(m.accounts)) > 0,
	}
	sup := capabilities.Supported(sit)
	avail := capabilities.Available(sit)
	return actionState{
		count:        1,
		open:         true,
		on:           true,
		outbox:       outbox,
		flagged:      flagged,
		reply:        avail.Reply,
		replyAll:     avail.ReplyAll,
		forward:      avail.Forward,
		star:         !outbox,
		trash:        avail.Trash,
		archive:      avail.Archive,
		junk:         avail.Junk,
		move:         avail.Move,
		markRead:     !outbox && unread,
		markUnread:   !outbox && seen,
		toggleFlag:   !outbox,
		loadImages:   true,
		trustSender:  !outbox,
		changeStatus: s.Issue != nil && jira.CanTransition(acc),
		comment:      sup.Comment,
		supported:    sup,
	}
}

// idleActionState is messageActionState with nothing selected.
func (m *mailModel) idleActionState() actionState {
	k := m.listFolder
	acc, _ := m.account(k.Account)
	sit := capabilities.Situation{
		Account:        acc,
		Outbox:         k.Folder != "" && m.folderRole(k) == api.RoleOutbox,
		ComposeAccount: len(capabilities.ForwardAccounts(m.accounts)) > 0,
	}
	sup := capabilities.Supported(sit)
	return actionState{comment: sup.Comment, supported: sup}
}

// canMoveToRole reports whether s can go to its account's role folder:
// the folder exists and s is not in it.
func (m *mailModel) canMoveToRole(s api.MessageSummary, role api.FolderRole) bool {
	f, ok := m.folderByRole(s.AccountID, role)
	return ok && f.ID != s.FolderID
}

// flags is the state as the selection rules take it.
func (s actionState) flags() selection.Flags {
	return selection.Flags{
		Count: s.count, On: s.on, Outbox: s.outbox, Flagged: s.flagged,
		Reply: s.reply, ReplyAll: s.replyAll, Forward: s.forward,
		Star: s.star, ToggleFlag: s.toggleFlag,
		Trash: s.trash, Archive: s.archive, Junk: s.junk, Move: s.move,
		MarkRead: s.markRead, MarkUnread: s.markUnread,
		LoadImages: s.loadImages, TrustSender: s.trustSender,
		ChangeStatus: s.changeStatus, Open: s.open, Comment: s.comment,
	}
}

// with is s with the selection's flags f; the account's offers stay s's
// (a selection is of one account).
func (s actionState) with(f selection.Flags) actionState {
	s.count, s.open, s.on, s.outbox, s.flagged = f.Count, f.Open, f.On, f.Outbox, f.Flagged
	s.reply, s.replyAll, s.forward = f.Reply, f.ReplyAll, f.Forward
	s.star, s.toggleFlag = f.Star, f.ToggleFlag
	s.trash, s.archive, s.junk, s.move = f.Trash, f.Archive, f.Junk, f.Move
	s.markRead, s.markUnread = f.MarkRead, f.MarkUnread
	s.loadImages, s.trustSender, s.changeStatus = f.LoadImages, f.TrustSender, f.ChangeStatus
	return s
}
