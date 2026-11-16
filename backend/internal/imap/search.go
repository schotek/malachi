// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"sort"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/schotek/malachi/backend/internal/search"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Search on the server (search.server): the mail older than the
// offlineDays window, which the store does not hold. It runs on a
// connection of its own, apart from the account's syncer and its IDLE,
// opens every mailbox read-only and changes nothing on the server.

// searchFetchCap bounds the envelopes one folder's search fetches when the
// server's SEARCH cannot be trusted with dates and the window is applied
// here.
const searchFetchCap = 2000

// ServerSearch is one search of an account's folders.
type ServerSearch struct {
	// Query is the parsed query; its In is the caller's (the folders are
	// chosen by it) and HasAttachment is applied to what the server finds.
	Query search.Query
	// Zone is where the days of before: and after: start.
	Zone *time.Location
	// Before is the start of the offlineDays window: only messages that
	// arrived before it are found. Zero finds nothing (no window).
	Before time.Time
	// Limit is the most messages returned per folder, newest first.
	Limit int
}

// Found is what a search found in one folder: rows with their envelopes
// only (BodyNone), newest first, ready for the store. Truncated says that
// the folder holds more matches than Limit.
type Found struct {
	Folder    store.Folder
	Messages  []*store.Message
	Truncated bool
}

// SearchServer searches the folders of an account on its IMAP server and
// returns what it found per folder. A folder the server refuses, or whose
// UIDVALIDITY is not the stored one (its UIDs would name other rows; the
// next sync pass resets it), is skipped. The error is the connection's or
// the login's *api.Error; a cancelled ctx ends the search with it.
func SearchServer(ctx context.Context, cfg api.ServerConfig, secret, accountID string, folders []store.Folder, q ServerSearch) ([]Found, error) {
	if q.Before.IsZero() || q.Limit <= 0 || len(folders) == 0 {
		return nil, nil
	}
	sess, err := openSession(ctx, cfg, secret, nil)
	if err != nil {
		return nil, err
	}
	var out []Found
	noDates := false // the server refused a SEARCH with dates
	for _, f := range folders {
		if f.Mailbox == "" || !f.Selectable || f.UIDValidity == 0 {
			continue
		}
		var sel *imap.SelectData
		err := sess.do(ctx, commandTimeout, func() error {
			var err error
			sel, err = sess.Select(f.Mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
			return err
		})
		switch {
		case isNo(err):
			continue
		case err != nil:
			sess.Close()
			return out, err
		case sel.UIDValidity != f.UIDValidity:
			continue
		}
		uids, dated, err := searchFolder(ctx, sess, q, &noDates)
		switch {
		case isNo(err) || isStatusError(err):
			continue
		case err != nil:
			sess.Close()
			return out, err
		}
		found, err := fetchFound(ctx, sess, accountID, f, uids, q, dated)
		if err != nil {
			sess.Close()
			return out, err
		}
		if len(found.Messages) > 0 {
			out = append(out, found)
		}
	}
	sess.logout()
	return out, nil
}

// searchFolder runs the UID SEARCH of the selected mailbox and returns
// the UIDs newest first. dated says that the server applied the dates; a
// server that refuses a SEARCH with dates is asked without them from then
// on (noDates) and the caller applies them to what it fetches.
func searchFolder(ctx context.Context, sess *session, q ServerSearch, noDates *bool) (uids []uint32, dated bool, err error) {
	run := func(c *imap.SearchCriteria) error {
		return sess.do(ctx, commandTimeout, func() error {
			data, err := sess.UIDSearch(c, nil).Wait()
			if err != nil {
				return err
			}
			set, _ := data.All.(imap.UIDSet)
			uids, err = uidsFromSet(set)
			return err
		})
	}
	if !*noDates {
		err = run(searchCriteria(q, true))
		switch {
		case err == nil:
			dated = true
		case isNo(err) || isStatusError(err):
			*noDates = true
		default:
			return nil, false, err
		}
	}
	if !dated {
		if err = run(searchCriteria(q, false)); err != nil {
			return nil, false, err
		}
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
	return uids, dated, nil
}

// searchCriteria is the SEARCH of a query: every term and filter must
// hold. A free word is looked for in the whole message (TEXT), from: and
// subject: in their header, to: in To, Cc or Bcc. With dates the window's
// start and the query's before: and after: go to the server too.
func searchCriteria(q ServerSearch, dates bool) *imap.SearchCriteria {
	c := &imap.SearchCriteria{}
	for _, t := range q.Query.Terms {
		switch t.Field {
		case search.FieldSubject:
			c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: t.Text})
		case search.FieldSender:
			c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: t.Text})
		case search.FieldRecipients:
			header := func(key string) imap.SearchCriteria {
				return imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: key, Value: t.Text}}}
			}
			ccOrBcc := imap.SearchCriteria{Or: [][2]imap.SearchCriteria{{header("Cc"), header("Bcc")}}}
			c.Or = append(c.Or, [2]imap.SearchCriteria{header("To"), ccOrBcc})
		default:
			c.Text = append(c.Text, t.Text)
		}
	}
	if q.Query.Unread {
		c.NotFlag = append(c.NotFlag, imap.FlagSeen)
	}
	if q.Query.Flagged {
		c.Flag = append(c.Flag, imap.FlagFlagged)
	}
	if dates {
		// SEARCH compares days in the server's zone: a day more on either
		// side, and matches() decides.
		lo, hi := searchBounds(q)
		c.Before = hi.AddDate(0, 0, 1)
		if !lo.IsZero() {
			c.Since = lo.AddDate(0, 0, -1)
		}
	}
	return c
}

// searchBounds is the range of arrival times a search finds: from the
// query's after: (zero = none) up to, exclusively, the window's start or
// the query's before:, whichever is earlier.
func searchBounds(q ServerSearch) (lo, hi time.Time) {
	zone := q.Zone
	if zone == nil {
		zone = time.Local
	}
	hi = q.Before
	if b := q.Query.Before; b != nil {
		if t := b.Start(zone); t.Before(hi) {
			hi = t
		}
	}
	if a := q.Query.After; a != nil {
		lo = a.Start(zone)
	}
	return lo, hi
}

// matches applies to a fetched row what the server was not asked, or not
// trusted with: the range of arrival times and has:attachment.
func matches(m *store.Message, q ServerSearch) bool {
	lo, hi := searchBounds(q)
	at := m.InternalDate
	if at.IsZero() {
		at = m.Date
	}
	switch {
	case at.IsZero() || !at.Before(hi):
		return false
	case !lo.IsZero() && at.Before(lo):
		return false
	case q.Query.HasAttachment && !m.HasAttachments:
		return false
	}
	return true
}

// fetchFound fetches the envelopes of the found UIDs, newest first, until
// Limit rows match. Only UIDs that were asked for are taken from the
// answer. When the server applied the dates, at most a few batches beyond
// Limit are fetched; when it did not, at most searchFetchCap envelopes.
func fetchFound(ctx context.Context, sess *session, accountID string, f store.Folder, uids []uint32, q ServerSearch, dated bool) (Found, error) {
	found := Found{Folder: f}
	budget := searchFetchCap
	if dated {
		budget = min(budget, q.Limit+envelopeBatch)
	}
	if len(uids) > budget {
		uids, found.Truncated = uids[:budget], true
	}
	for start := 0; start < len(uids) && len(found.Messages) < q.Limit; start += envelopeBatch {
		batch := uids[start:min(start+envelopeBatch, len(uids))]
		asked := make(map[uint32]bool, len(batch))
		ascending := append([]uint32(nil), batch...)
		sort.Slice(ascending, func(i, j int) bool { return ascending[i] < ascending[j] })
		for _, u := range batch {
			asked[u] = true
		}
		var msgs []*store.Message
		err := sess.do(ctx, commandTimeout, func() error {
			cmd := sess.Fetch(uidSet(ascending), &envelopeOptions)
			for md := cmd.Next(); md != nil; md = cmd.Next() {
				m := messageFromFetch(md, accountID, f)
				if asked[m.UID] {
					asked[m.UID] = false // once
					msgs = append(msgs, m)
				}
			}
			return cmd.Close()
		})
		if err != nil {
			return found, err
		}
		sort.Slice(msgs, func(i, j int) bool { return msgs[i].UID > msgs[j].UID })
		for _, m := range msgs {
			if !matches(m, q) {
				continue
			}
			if len(found.Messages) == q.Limit {
				found.Truncated = true
				break
			}
			found.Messages = append(found.Messages, m)
		}
	}
	return found, nil
}
