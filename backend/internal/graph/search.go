// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/search"
	"github.com/schotek/malachi/backend/internal/store"
)

// Search on the service (search.server): the mail older than the
// offlineDays window, which the store does not hold. Reads only.

const (
	// searchPageSize is the $top of one page of a search.
	searchPageSize = 100
	// searchMaxPages bounds the pages one folder's search reads: $search
	// cannot be combined with a filter, so the window is applied here to
	// what the service returns (at most 1000 results, newest first).
	searchMaxPages = 10
	// translateBatch is the most ids one translateExchangeIds call takes
	// (the service's cap is 1,000).
	translateBatch = 1000
	// maxSearchIDBytes caps an id the service returns before it is stored.
	maxSearchIDBytes = 1024
)

// ServerSearch is one search of an account's folders.
type ServerSearch struct {
	// Query is the parsed query; its In is the caller's (the folders are
	// chosen by it).
	Query search.Query
	// Zone is where the days of before: and after: start.
	Zone *time.Location
	// Before is the start of the offlineDays window: only messages
	// received before it are found. Zero finds nothing (no window).
	Before time.Time
	// Limit is the most messages returned per folder, newest first.
	Limit int
}

// Found is what a search found in one folder: rows with their headers
// only (BodyNone), newest first, ready for the store. Truncated says that
// the folder may hold more matches.
type Found struct {
	Folder    store.Folder
	Messages  []*store.Message
	Truncated bool
	// Renamed maps the REST id the search answered for a message to the
	// immutable id its row carries (RemoteID). A store that holds the
	// message under the REST id (a search before the translation) renames
	// that row (store.RenameRemoteIDs).
	Renamed map[string]string
}

// SearchServer searches the folders of an account through Microsoft Graph
// and returns what it found per folder. A folder the service no longer
// has is skipped. A query with words goes to $search, in the service's
// own matching; one of filters alone to a $filter over the folder.
func SearchServer(ctx context.Context, c *Client, accountID string, folders []store.Folder, q ServerSearch) ([]Found, error) {
	if q.Before.IsZero() || q.Limit <= 0 {
		return nil, nil
	}
	var out []Found
	for _, f := range folders {
		if f.Mailbox == "" {
			continue
		}
		found := Found{Folder: f}
		seen := map[string]bool{} // a page may repeat a message of an earlier one
		next, pages := searchURL(f, q), 0
		for next != "" && len(found.Messages) < q.Limit {
			if pages == searchMaxPages {
				found.Truncated = true
				break
			}
			var pg page[message]
			err := c.Get(ctx, next, &pg)
			if IsNotFound(err) {
				break
			}
			if err != nil {
				return out, err
			}
			pages++
			for _, m := range pg.Value {
				if !validSearchID(m.ID) || m.Removed != nil || seen[m.ID] || !searchMatches(m, q) {
					continue
				}
				seen[m.ID] = true
				if len(found.Messages) == q.Limit {
					found.Truncated = true
					break
				}
				found.Messages = append(found.Messages, messageRow(accountID, f, m))
			}
			next = pg.NextLink
		}
		if next != "" && len(found.Messages) == q.Limit {
			found.Truncated = true // pages the search did not read
		}
		if len(found.Messages) > 0 {
			if err := translateIDs(ctx, c, &found); err != nil {
				return out, err
			}
			sort.SliceStable(found.Messages, func(i, j int) bool {
				return found.Messages[i].InternalDate.After(found.Messages[j].InternalDate)
			})
			out = append(out, found)
		}
	}
	return out, nil
}

// translateIDs gives the found rows the immutable ids the sync keys
// messages by. $search answers with the service's default REST ids
// whatever the Prefer header asks (measured 2026-11-15 against Microsoft
// 365 and Outlook.com), while the delta sync and a $filter listing answer
// with immutable ids: a row keyed by the REST id would be a second row of
// a message the store has, or will have once the window grows, and would
// go stale when the message moves. translateExchangeIds converts them
// (restId → restImmutableEntryId, at most translateBatch per call). The
// translation is best effort: when the service refuses it, or leaves an
// id out, the row keeps its REST id, which every request of the service
// accepts, and the refusal is logged with its code only. A cancelled
// context ends the search with its error.
func translateIDs(ctx context.Context, c *Client, found *Found) error {
	ids := make([]string, 0, len(found.Messages))
	for _, m := range found.Messages {
		ids = append(ids, m.RemoteID)
	}
	immutable := make(map[string]string, len(ids))
	for start := 0; start < len(ids); start += translateBatch {
		batch := ids[start:min(start+translateBatch, len(ids))]
		var res struct {
			Value []struct {
				SourceID string `json:"sourceId"`
				TargetID string `json:"targetId"`
			} `json:"value"`
		}
		body := map[string]any{
			"inputIds":     batch,
			"sourceIdType": "restId",
			"targetIdType": "restImmutableEntryId",
		}
		if err := c.Post(ctx, "me/translateExchangeIds", body, &res); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if c.log != nil {
				c.log.Warn("search: ids not translated to immutable ids", "code", ToAPIError(err).Code, "ids", len(batch))
			}
			return nil
		}
		for _, v := range res.Value {
			if v.SourceID != "" && validSearchID(v.TargetID) {
				immutable[v.SourceID] = v.TargetID
			}
		}
	}
	missing := 0
	for _, m := range found.Messages {
		to, ok := immutable[m.RemoteID]
		switch {
		case !ok:
			missing++
			continue
		case to == m.RemoteID:
			continue
		}
		if found.Renamed == nil {
			found.Renamed = map[string]string{}
		}
		found.Renamed[m.RemoteID] = to
		m.RemoteID = to
	}
	if missing > 0 && c.log != nil {
		c.log.Debug("search: ids left untranslated by the service", "ids", missing, "of", len(ids))
	}
	return nil
}

// validSearchID accepts an id the service returned for a message: printable
// ASCII of a bounded length, as the ids of the service are.
func validSearchID(s string) bool {
	if s == "" || len(s) > maxSearchIDBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// searchURL is the first request of a folder's search.
func searchURL(f store.Folder, q ServerSearch) string {
	v := url.Values{}
	v.Set("$select", messageSelect)
	v.Set("$top", strconv.Itoa(searchPageSize))
	if expr := searchExpr(q.Query); expr != "" {
		v.Set("$search", `"`+expr+`"`)
	} else {
		lo, hi := searchBounds(q)
		filter := []string{"receivedDateTime lt " + hi.UTC().Format(time.RFC3339)}
		if !lo.IsZero() {
			filter = append(filter, "receivedDateTime ge "+lo.UTC().Format(time.RFC3339))
		}
		if q.Query.Unread {
			filter = append(filter, "isRead eq false")
		}
		if q.Query.HasAttachment {
			filter = append(filter, "hasAttachments eq true")
		}
		v.Set("$filter", strings.Join(filter, " and "))
		v.Set("$orderby", "receivedDateTime desc")
	}
	return "me/mailFolders/" + url.PathEscape(f.Mailbox) + "/messages?" + v.Encode()
}

// searchExpr is the KQL of the query's terms, "" without any. Every term
// is a quoted phrase, with its property for from:, to: and subject:, so
// that nothing typed is an operator or a property of KQL; a quote or a
// backslash inside a term is dropped.
func searchExpr(q search.Query) string { return searchTerms(q, `\"`) }

// searchTerms is the terms of a query as quoted phrases, with quote
// around each: `\"` inside the $search string of OData, `"` in the KQL of
// the Search API.
func searchTerms(q search.Query, quote string) string {
	var parts []string
	for _, t := range q.Terms {
		text := strings.TrimSpace(strings.Map(func(r rune) rune {
			if r == '"' || r == '\\' {
				return -1
			}
			return r
		}, t.Text))
		if text == "" {
			continue
		}
		phrase := quote + text + quote
		switch t.Field {
		case search.FieldSubject:
			phrase = "subject:" + phrase
		case search.FieldSender:
			phrase = "from:" + phrase
		case search.FieldRecipients:
			phrase = "recipients:" + phrase
		}
		parts = append(parts, phrase)
	}
	return strings.Join(parts, " ")
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

// searchMatches applies the filters to a listed message: $search takes
// none of them, and what a $filter was asked is checked again.
func searchMatches(m message, q ServerSearch) bool {
	lo, hi := searchBounds(q)
	at := m.ReceivedDateTime
	switch {
	case m.IsDraft:
		return false
	case at.IsZero() || !at.Before(hi):
		return false
	case !lo.IsZero() && at.Before(lo):
		return false
	case q.Query.Unread && m.IsRead:
		return false
	case q.Query.Flagged && (m.Flag == nil || m.Flag.FlagStatus != "flagged"):
		return false
	case q.Query.HasAttachment && !m.HasAttachments:
		return false
	}
	return true
}
