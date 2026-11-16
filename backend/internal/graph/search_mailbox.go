// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
)

// Search of the whole mailbox through the Microsoft Search API (POST
// /search/query with entityTypes message; docs/plans/server-search.md):
// the search Outlook runs, one request for every folder of the mailbox,
// ranked by the service, the window and the query's dates in KQL. The
// service answers at most mailboxSearchPage hits a page, from must start
// at 0, personal Microsoft accounts are not supported (the caller falls
// back to SearchServer, folder by folder), and the In-Place Archive is not
// searched (measured 2026-11-15: a query restricted to dates the archive
// alone covers answered nothing). Reads only.

const (
	// mailboxSearchPage is the most hits the service returns for messages
	// in one page.
	mailboxSearchPage = 25
	// mailboxSearchMaxPages bounds the pages one search reads (Limit is
	// api.MaxServerSearchResults = 200).
	mailboxSearchMaxPages = 8
)

// ErrMailboxSearchUnsupported says the service refused the Search API
// for this mailbox (a personal account, a tenant without it): the caller
// searches folder by folder instead.
var ErrMailboxSearchUnsupported = errors.New("graph: the mailbox search is not available")

// SearchMailbox searches the mailbox of an account through the Search
// API and returns what it found per folder, for the folders given (a hit
// in any other folder, one the store does not synchronise, is left out).
// The rows carry the immutable ids (translateIDs). A 400, 403 or 404 of
// the service is ErrMailboxSearchUnsupported.
func SearchMailbox(ctx context.Context, c *Client, accountID string, folders []store.Folder, q ServerSearch) ([]Found, error) {
	if q.Before.IsZero() || q.Limit <= 0 || len(folders) == 0 {
		return nil, nil
	}
	byMailbox := make(map[string]int, len(folders))
	for i, f := range folders {
		if f.Mailbox != "" {
			byMailbox[f.Mailbox] = i
		}
	}
	found := make([]Found, len(folders))
	for i, f := range folders {
		found[i].Folder = f
	}
	seen := map[string]bool{}
	kql := mailboxKQL(q)
	total, pages, hitsAll, otherFolder, dropped := 0, 0, 0, 0, 0
	defer func() {
		// What the service answered and what became rows, for the log
		// (never the query): a search that finds nothing is otherwise
		// indistinguishable from one whose hits were all left out.
		if c.log != nil && pages > 0 {
			c.log.Info("search on the server: mailbox search", "pages", pages, "hits", hitsAll, "rows", total,
				"otherFolders", otherFolder, "dropped", dropped)
		}
	}()
	for page := 0; page < mailboxSearchMaxPages && total < q.Limit; page++ {
		body := map[string]any{
			"requests": []map[string]any{{
				"entityTypes": []string{"message"},
				"query":       map[string]string{"queryString": kql},
				"from":        page * mailboxSearchPage,
				"size":        mailboxSearchPage,
				"fields":      strings.Split(messageSelect, ","),
			}},
		}
		var res struct {
			Value []struct {
				HitsContainers []struct {
					Hits []struct {
						HitID    string  `json:"hitId"`
						Resource message `json:"resource"`
					} `json:"hits"`
					MoreResultsAvailable bool `json:"moreResultsAvailable"`
				} `json:"hitsContainers"`
			} `json:"value"`
		}
		if err := c.Post(ctx, "search/query", body, &res); err != nil {
			var se *StatusError
			if errors.As(err, &se) && (se.Status == http.StatusBadRequest || se.Status == http.StatusForbidden || se.Status == http.StatusNotFound) {
				return nil, fmt.Errorf("%w: HTTP %d %s", ErrMailboxSearchUnsupported, se.Status, se.Code)
			}
			return nil, err
		}
		pages++
		more := false
		hits := 0
		for _, v := range res.Value {
			for _, hc := range v.HitsContainers {
				more = more || hc.MoreResultsAvailable
				for _, h := range hc.Hits {
					hits++
					hitsAll++
					m := h.Resource
					if m.ID == "" {
						m.ID = h.HitID
					}
					i, ok := byMailbox[m.ParentFolderID]
					if !ok {
						otherFolder++
						continue
					}
					if !validSearchID(m.ID) || m.Removed != nil || seen[m.ID] || !searchMatches(m, q) {
						dropped++
						continue
					}
					seen[m.ID] = true
					if total == q.Limit {
						found[i].Truncated = true
						continue
					}
					found[i].Messages = append(found[i].Messages, messageRow(accountID, folders[i], m))
					total++
				}
			}
		}
		if !more || hits == 0 {
			break
		}
		if page == mailboxSearchMaxPages-1 || total >= q.Limit {
			for i := range found {
				if len(found[i].Messages) > 0 {
					found[i].Truncated = true
				}
			}
		}
	}
	var out []Found
	for i := range found {
		if len(found[i].Messages) == 0 {
			continue
		}
		if err := translateIDs(ctx, c, &found[i]); err != nil {
			return out, err
		}
		out = append(out, found[i])
	}
	return out, nil
}

// mailboxKQL is the KQL of a search of the mailbox: the query's terms as
// searchExpr makes them, and the window's start and the query's dates as
// received restrictions (days, in the zone of the query; the service
// compares days), so that the service never spends its pages on mail the
// store holds. The flags (is:, has:) are applied to the hits
// (searchMatches). A query of filters alone is the dates alone.
func mailboxKQL(q ServerSearch) string {
	parts := []string{}
	if expr := searchTerms(q.Query, `"`); expr != "" {
		parts = append(parts, expr)
	}
	lo, hi := searchBounds(q)
	zone := q.Zone
	if zone == nil {
		zone = time.Local
	}
	// hi is exclusive; received< compares the day, and a day more on the
	// window's side is cut by searchMatches.
	parts = append(parts, "received<"+hi.In(zone).AddDate(0, 0, 1).Format("2006-01-02"))
	if !lo.IsZero() {
		parts = append(parts, "received>="+lo.In(zone).Format("2006-01-02"))
	}
	return strings.Join(parts, " ")
}
