// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"errors"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/board"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The adapter between the store's board layer and the rules
// (internal/board): DrainBoard hands each dirty thread to the decider,
// which turns the store's BoardThread into the rules' Thread, evaluates
// it and returns the store's BoardVerdict.
//
// What the adapter decides itself:
//   - "mine" is the store's Mine (a row in a folder of role sent or
//     outbox); a twin of such a row (TwinOfMine) shares its Message-ID, so
//     the rules merge it with the mine copy. The From header never makes a
//     member mine.
//   - The identity of a mail account is its own address and the senders
//     of its sent folders, with the user's known correspondents across
//     every enabled mail account, and the addresses of every account of
//     the user for notes to self (Identity.WithSelf; boardIdentity), read
//     before the batch:
//     the decider runs inside the store's write transaction and must not
//     call the store. For a jira thread the user's id is the thread's Me.
//   - Members carry Reply-To; an issue's members carry the site's time of
//     their item (store.BoardMember.ItemAt) as their date, never the date
//     a bot's relayed text claims.
//   - Text is read lazily: the thread is evaluated without any first, and
//     only when its newest member that counts is the user's are the texts
//     the rules read loaded (board.Verdict.TextMembers) and the thread
//     evaluated again; the own text of such a member that has HTML comes from the
//     cache of board_owntext.go.
//   - The input key handed to clients and checked by board.annotate and
//     board.commit is the store's (BoardCase.InputKey, derived again inside
//     the annotate transaction).

// boardAccount is what the decider knows of an account, read before a
// batch.
type boardAccount struct {
	identity   board.Identity // mail; a jira account's is built per thread
	addresses  []string       // the account's address and its senders, lower case
	jira       bool
	closed     map[string]bool // jira: the ids of the closed statuses
	canArchive bool            // the move capability and a selectable folder of role archive
}

// boardSnippetBytes caps the snippet a case keeps.
const boardSnippetBytes = 300

// boardDecider returns the decider over the accounts read for this batch;
// the own texts it lacks are added to wants.
func (b *Backend) boardDecider(accounts map[string]*boardAccount, now time.Time, wants *boardWants) store.BoardDecider {
	return func(t *store.BoardThread) (store.BoardVerdict, error) {
		acc := accounts[t.AccountID]
		if acc == nil {
			// The account is gone (its rows go with it): no case.
			return store.BoardVerdict{RulesVersion: board.RulesVersion}, nil
		}
		th := boardThreadOf(t, acc)
		if acc.jira && th.Issue == nil {
			// An issue's rows without the issue (deleted on the site, or
			// not stored yet): no case.
			return store.BoardVerdict{RulesVersion: board.RulesVersion}, nil
		}
		id := acc.identity
		if acc.jira {
			id = board.NewIdentity(t.Me, acc.addresses, nil)
		}
		v := board.Evaluate(th, id, now)
		retried := b.board.own.takeRetry(t.AccountID + "\x00" + t.ThreadID)
		if !v.Pending && !acc.jira {
			if need := v.TextMembers(); len(need) > 0 {
				ready, err := b.boardFillTexts(t, &th, need, retried, wants)
				if err != nil {
					return store.BoardVerdict{}, err
				}
				if !ready {
					return store.BoardVerdict{Skip: true}, nil
				}
				v = board.Evaluate(th, id, now)
			}
		}
		if v.Pending {
			// A relevant member waits for the bulk classification;
			// classifying it marks the thread dirty again.
			return store.BoardVerdict{Skip: true}, nil
		}
		out := store.BoardVerdict{
			State: v.State, Reason: v.Reason, RulesVersion: board.RulesVersion,
		}
		if v.Count == 0 {
			return out, nil
		}
		out.Subject, out.Person, out.Date = v.Subject, v.Person, v.Date
		out.Unread, out.HasAttachments, out.MessageCount = v.Unread, v.HasAttachments, v.Count
		out.ReplyMessageID, out.ReplyFolderID, out.LatestMessageID = string(v.ReplyID), string(v.ReplyFolderID), string(v.LatestID)
		for _, m := range t.Members {
			if m.ID == string(v.LatestID) {
				out.Snippet = cleanSnippet(m.Snippet)
			}
		}
		if acc.canArchive {
			for _, m := range v.Members {
				if m.FolderRole == api.RoleInbox {
					out.CanArchive = true
					break
				}
			}
		}
		// What the store compares to reopen a done case; a message the case
		// had when it was marked done is passed over (board.Verdict.NewestInbound).
		var doneAt time.Time
		var seen func(string) bool
		if t.Case != nil {
			doneAt, seen = t.Case.DoneAt, t.Case.SeenAtDone
		}
		out.NewestInboundStored, out.NewestInboundDate, out.NewestInboundMessageID = v.NewestInbound(doneAt, seen)
		return out, nil
	}
}

// boardFillTexts sets the text of the members need names in th, and the
// own text of those with HTML from the cache. ready is false when an own
// text is not cached yet and the thread has not waited for it before: it
// is asked for (wants) and the thread left as it is. A thread that did
// wait (retried) is judged with what there is.
func (b *Backend) boardFillTexts(t *store.BoardThread, th *board.Thread, need []api.MessageID, retried bool, wants *boardWants) (bool, error) {
	at := make(map[api.MessageID]int, len(th.Members))
	for i, m := range th.Members {
		at[m.ID] = i
	}
	var missing []boardOwnKey
	for _, id := range need {
		i, ok := at[id]
		if !ok {
			continue
		}
		m := &th.Members[i]
		text, err := t.Text(string(id))
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return false, err
		}
		m.Text = text
		if store.BodyState(m.BodyState) != store.BodyFetched {
			continue
		}
		k := boardOwnKey{account: t.AccountID, id: string(id), state: store.BodyFetched}
		if own, ok := b.board.own.get(k); ok {
			m.OwnText, m.OwnTextSet = own.text, own.html
		} else {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 && !retried {
		wants.add(t.AccountID, t.ThreadID, missing)
		return false, nil
	}
	return true, nil
}

// boardThreadOf converts the store's thread for the rules (without any
// text: boardFillTexts adds what the rules read).
func boardThreadOf(t *store.BoardThread, acc *boardAccount) board.Thread {
	var th board.Thread
	if acc.jira && t.Issue != nil {
		is := &board.Issue{
			StatusCategory: t.Issue.StatusCategory,
			Closed:         acc.closed[t.Issue.StatusID],
			AssigneeID:     t.Issue.AssigneeID,
			ReporterID:     t.Issue.ReporterID,
			Watching:       t.Issue.Watching,
		}
		if t.Me != "" {
			for _, it := range t.Items {
				if it.AuthorID == t.Me && it.Kind != api.IssueItemEvent {
					is.Commented = true
					break
				}
			}
		}
		th.Issue = is
	}
	th.Members = make([]board.Member, 0, len(t.Members))
	for _, m := range t.Members {
		bm := board.Member{
			ID:             api.MessageID(m.ID),
			FolderID:       api.FolderID(m.FolderID),
			FolderRole:     m.Role,
			Mine:           m.Mine,
			MessageID:      m.RFCMessageID,
			InReplyTo:      m.InReplyTo,
			References:     m.References,
			ReplyTo:        m.ReplyTo,
			To:             m.To,
			Cc:             m.CC,
			Bcc:            m.BCC,
			Subject:        m.Subject,
			Date:           m.Date,
			InternalDate:   m.InternalDate,
			StoredAt:       m.StoredAt,
			Flagged:        m.Flagged,
			Unread:         m.Unread,
			HasAttachments: m.HasAttachments,
			HasMessagePart: m.HasRFC822(),
			BodyState:      string(m.BodyState),
			Bulk:           m.Bulk,
			Importance:     headerValue(m.Headers, "Importance"),
			XPriority:      headerValue(m.Headers, "X-Priority"),
			IssueKind:      m.ItemKind,
			AuthorID:       m.ItemAuthorID,
		}
		if m.ItemKind != "" && !m.ItemAt.IsZero() {
			// The site's time of the item: a comment a bot relayed has the
			// date its text claims as Date and InternalDate.
			bm.Date, bm.InternalDate = m.ItemAt, m.ItemAt
		}
		if len(m.From) > 0 {
			bm.From = m.From[0]
		}
		th.Members = append(th.Members, bm)
	}
	return th
}

// headerValue reads a curated header, whatever the case of its name.
func headerValue(h map[string]string, name string) string {
	if v, ok := h[name]; ok {
		return v
	}
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// cleanSnippet makes a stored snippet one clean line of at most
// boardSnippetBytes.
func cleanSnippet(s string) string {
	return store.CutUTF8(strings.Join(strings.Fields(board.CleanText(store.CutUTF8(s, 4*boardSnippetBytes))), " "), boardSnippetBytes)
}
