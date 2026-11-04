// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

// Event search over cal_events_fts (migration 0020): title, location,
// notes and people (the organiser's and attendees' names and addresses),
// folded by the unicode61 tokenizer (case and diacritics, so "novak"
// finds "Nováková"); every word of the query is a prefix.

// MaxEventSearchTotal caps SearchEvents' total (api event.search total).
const MaxEventSearchTotal = 1000

// maxEventSearchWords bounds the words of a query that count.
const maxEventSearchWords = 16

// EventSearch is what SearchEvents looks for: Query (the user's words),
// within AccountIDs and CalendarIDs when set, leaving out
// ExcludeAccountIDs; Limit 0 = MaxEventSearchTotal.
type EventSearch struct {
	Query             string
	AccountIDs        []string
	CalendarIDs       []string
	ExcludeAccountIDs []string
	Limit             int
}

// EventSearchMatch is the FTS5 expression of a query: each word (a run of
// letters and digits) quoted as a prefix term, all of them required; ""
// when the query has no word.
func EventSearchMatch(query string) string {
	words := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) })
	if len(words) > maxEventSearchWords {
		words = words[:maxEventSearchWords]
	}
	terms := make([]string, len(words))
	for i, w := range words {
		terms[i] = `"` + w + `"*`
	}
	return strings.Join(terms, " ")
}

// SearchEvents returns the ids of the events that match q.Query, one per
// event: a match in an override counts for its master (when stored), so a
// series is one id. Ordered by id (the caller orders by occurrence, see
// RepresentativeOccurrences); total counts the matching events up to
// MaxEventSearchTotal. A query without a word matches nothing.
func (s *Store) SearchEvents(ctx context.Context, q EventSearch) (ids []string, total int, err error) {
	match := EventSearchMatch(q.Query)
	if match == "" {
		return nil, 0, nil
	}
	limit := q.Limit
	if limit <= 0 || limit > MaxEventSearchTotal {
		limit = MaxEventSearchTotal
	}
	where := ` WHERE cal_events_fts MATCH ?`
	args := []any{match}
	if len(q.AccountIDs) > 0 {
		where += ` AND e.account_id IN (` + inPlaceholders(len(q.AccountIDs)) + `)`
		args = append(args, toAny(q.AccountIDs)...)
	}
	if len(q.CalendarIDs) > 0 {
		where += ` AND e.calendar_id IN (` + inPlaceholders(len(q.CalendarIDs)) + `)`
		args = append(args, toAny(q.CalendarIDs)...)
	}
	if len(q.ExcludeAccountIDs) > 0 {
		where += ` AND e.account_id NOT IN (` + inPlaceholders(len(q.ExcludeAccountIDs)) + `)`
		args = append(args, toAny(q.ExcludeAccountIDs)...)
	}
	args = append(args, MaxEventSearchTotal)
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT
			CASE WHEN e.kind = 'override' AND e.master_id != ''
			      AND EXISTS (SELECT 1 FROM cal_events m WHERE m.id = e.master_id) THEN e.master_id ELSE e.id END AS root
		FROM cal_events_fts
		JOIN cal_events_fts_docs d ON d.docid = cal_events_fts.rowid
		JOIN cal_events e ON e.id = d.event_id`+where+`
		ORDER BY root LIMIT ?`, args...)
	if err != nil {
		if msg := err.Error(); strings.Contains(msg, "fts5") || strings.Contains(msg, "unterminated string") {
			return nil, 0, ErrSearchRejected
		}
		return nil, 0, fmt.Errorf("search events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, 0, fmt.Errorf("search events: %w", err)
		}
		total++
		if len(ids) < limit {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("search events: %w", err)
	}
	return ids, total, nil
}
