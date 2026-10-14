// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package selection is the message list's selection of several rows, in
// all three clients: what the window's actions allow for it (Combine),
// that it stays within one account (Restrict), what it becomes when the
// list changes under it (Reconcile), and its texts. One row is the row's
// own flags, unchanged: everything here is about two or more. A row
// stands for one message or, folded, for a conversation's members in the
// folder, as before. Pure logic, no widgets; ported 1:1 to macOS
// (MalachiCore/Selection) and Windows (Malachi.Core/Selection).
package selection

import "github.com/schotek/malachi/backend/pkg/api"

const (
	// MaxOpen is the most rows Open opens in windows of their own at once.
	MaxOpen = 10
	// MaxForward is the most rows Forward takes into one message
	// (api.MaxForwardMessages).
	MaxForward = api.MaxForwardMessages
)

// Flags is what the window's actions allow: for one row the action rules'
// own (GTK actionState, MalachiCore and Malachi.Core ActionFlags), for
// several Combine's. Count is how many rows (0: none).
type Flags struct {
	Count int
	// On: something is selected. Outbox: queued messages. Flagged: the
	// star shows it (several: every row is starred).
	On, Outbox, Flagged bool

	Reply, ReplyAll, Forward bool
	Star, ToggleFlag         bool
	Trash, Archive, Junk     bool
	Move                     bool
	MarkRead, MarkUnread     bool
	LoadImages, TrustSender  bool
	ChangeStatus             bool
	// Open: Open in New Window (several: at most MaxOpen rows).
	Open bool
	// Comment: Reply writes a comment (the account's capability).
	Comment bool
}

// Combine is what the window's actions allow for rows, the flags of each
// selected row (one account; a client never passes rows of two). One row
// is that row's flags; several:
//   - Mark as Read when any row is unread, Mark as Unread when any is
//     read; the star stars them all unless every row is starred, which
//     unstars them all (Flagged: every row starred);
//   - Archive and Junk when any row can go there (those already there
//     stay); Trash, Move To and the star only when every row allows them;
//   - Forward forwards them all in one message, up to MaxForward rows;
//     Open opens up to MaxOpen windows;
//   - Reply, Reply All, Change Status, Load Images and Trust Sender are
//     for one message, and off;
//   - in the outbox Trash cancels the sending of them all, and nothing
//     else goes.
func Combine(rows []Flags) Flags {
	switch len(rows) {
	case 0:
		return Flags{}
	case 1:
		f := rows[0]
		f.Count = 1
		f.Open = f.On
		return f
	}
	n := len(rows)
	out := Flags{Count: n, On: true, Outbox: true, Flagged: true, Comment: rows[0].Comment,
		Forward: n <= MaxForward, Star: true, ToggleFlag: true, Trash: true, Move: true, Open: n <= MaxOpen}
	for _, r := range rows {
		out.Outbox = out.Outbox && r.Outbox
		out.Flagged = out.Flagged && r.Flagged
		out.Forward = out.Forward && r.Forward
		out.Star = out.Star && r.Star
		out.ToggleFlag = out.ToggleFlag && r.ToggleFlag
		out.Trash = out.Trash && r.Trash
		out.Move = out.Move && r.Move
		out.Archive = out.Archive || r.Archive
		out.Junk = out.Junk || r.Junk
		out.MarkRead = out.MarkRead || r.MarkRead
		out.MarkUnread = out.MarkUnread || r.MarkUnread
	}
	if out.Outbox {
		out.Forward, out.Star, out.ToggleFlag, out.Move = false, false, false, false
		out.Archive, out.Junk, out.MarkRead, out.MarkUnread = false, false, false, false
	}
	return out
}

// FlagTarget is what the star does to the selected rows: true stars them,
// false unstars them (Flags.Flagged of Combine: every row starred).
func FlagTarget(f Flags) bool { return !f.Flagged }

// Row is a selected row's key and the account of what it stands for.
type Row[K comparable] struct {
	Key     K
	Account api.AccountID
}

// Restrict keeps the selection within one account, latest's (the row the
// user added last): the rows of another account leave it. rows are the
// selected rows in list order; the result keeps that order. latest not
// among rows: nothing changes.
func Restrict[K comparable](rows []Row[K], latest K) []K {
	var account api.AccountID
	found := false
	for _, r := range rows {
		if r.Key == latest {
			account, found = r.Account, true
			break
		}
	}
	out := make([]K, 0, len(rows))
	for _, r := range rows {
		if !found || r.Account == account {
			out = append(out, r.Key)
		}
	}
	return out
}

// Reconcile is the selection after the list changed under it (a reload,
// a page, a conversation folded, rows removed): each previous key in
// order resolved to what the list shows it as now (itself; a member row
// folded away as its conversation row) or gone, without duplicates. When
// nothing is left and neighbour is given, the row it names (the one now
// where the first selected row was) is selected instead; it may say none.
func Reconcile[K comparable](prev []K, resolve func(K) (K, bool), neighbour func() (K, bool)) []K {
	seen := make(map[K]bool, len(prev))
	var out []K
	for _, k := range prev {
		now, ok := resolve(k)
		if !ok || seen[now] {
			continue
		}
		seen[now] = true
		out = append(out, now)
	}
	if len(out) == 0 && len(prev) > 0 && neighbour != nil {
		if k, ok := neighbour(); ok {
			out = append(out, k)
		}
	}
	return out
}
