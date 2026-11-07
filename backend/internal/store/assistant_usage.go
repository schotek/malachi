// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"fmt"
	"regexp"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The token usage of the daemon's own assistant (migration 0027,
// docs/api.md §4.18): one row per ai account, local day and model, added
// to after every turn and read summed over models.

// dayRe is the form of a local day key.
var dayRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// ValidDay reports a "YYYY-MM-DD" day key.
func ValidDay(day string) bool { return dayRe.MatchString(day) }

// AddAssistantUsage adds one turn's tokens of model to the row of
// accountID and day (a local day, "2026-11-07"), counting the turn and,
// when newSession, the session. Counters are clamped to
// 0..api.MaxBoardUsageTokens; an unknown account is ErrNotFound.
func (s *Store) AddAssistantUsage(ctx context.Context, accountID, day, model string, u api.AssistantUsage, newSession bool) error {
	if !ValidDay(day) {
		return fmt.Errorf("add assistant usage: bad day %q", day)
	}
	if model == "" {
		model = "?"
	}
	c := func(n int64) int64 { return min(max(n, 0), api.MaxBoardUsageTokens) }
	sess := 0
	if newSession {
		sess = 1
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO assistant_usage (account_id, day, model, input_tokens, output_tokens,
			cache_read_input_tokens, cache_creation_input_tokens, sessions, turns, updated_at)
		SELECT id, ?, ?, ?, ?, ?, ?, ?, 1, ? FROM accounts WHERE id = ?
		ON CONFLICT (account_id, day, model) DO UPDATE SET
			input_tokens = min(input_tokens + excluded.input_tokens, ?),
			output_tokens = min(output_tokens + excluded.output_tokens, ?),
			cache_read_input_tokens = min(cache_read_input_tokens + excluded.cache_read_input_tokens, ?),
			cache_creation_input_tokens = min(cache_creation_input_tokens + excluded.cache_creation_input_tokens, ?),
			sessions = sessions + excluded.sessions, turns = turns + 1, updated_at = excluded.updated_at`,
		day, model, c(u.InputTokens), c(u.OutputTokens), c(u.CacheReadInputTokens), c(u.CacheCreationInputTokens), sess, nowStamp(), accountID,
		api.MaxBoardUsageTokens, api.MaxBoardUsageTokens, api.MaxBoardUsageTokens, api.MaxBoardUsageTokens)
	if err != nil {
		return fmt.Errorf("add assistant usage: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AssistantUsage returns the usage of accountID ("" = every account) on
// the days from sinceDay on (inclusive), summed over models, newest day
// first and accounts in id order within a day.
func (s *Store) AssistantUsage(ctx context.Context, accountID, sinceDay string) ([]api.AssistantUsageDay, error) {
	if !ValidDay(sinceDay) {
		return nil, fmt.Errorf("assistant usage: bad day %q", sinceDay)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT day, account_id, SUM(input_tokens), SUM(output_tokens),
			SUM(cache_read_input_tokens), SUM(cache_creation_input_tokens), SUM(sessions), SUM(turns)
		FROM assistant_usage WHERE day >= ? AND (? = '' OR account_id = ?)
		GROUP BY day, account_id ORDER BY day DESC, account_id`, sinceDay, accountID, accountID)
	if err != nil {
		return nil, fmt.Errorf("assistant usage: %w", err)
	}
	defer rows.Close()
	var out []api.AssistantUsageDay
	for rows.Next() {
		var d api.AssistantUsageDay
		var id string
		if err := rows.Scan(&d.Day, &id, &d.InputTokens, &d.OutputTokens, &d.CacheReadInputTokens,
			&d.CacheCreationInputTokens, &d.Sessions, &d.Turns); err != nil {
			return nil, fmt.Errorf("assistant usage: %w", err)
		}
		d.AccountID = api.AccountID(id)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("assistant usage: %w", err)
	}
	return out, nil
}

// PruneAssistantUsage deletes the rows of days before beforeDay and
// returns how many.
func (s *Store) PruneAssistantUsage(ctx context.Context, beforeDay string) (int, error) {
	if !ValidDay(beforeDay) {
		return 0, fmt.Errorf("prune assistant usage: bad day %q", beforeDay)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM assistant_usage WHERE day < ?`, beforeDay)
	if err != nil {
		return 0, fmt.Errorf("prune assistant usage: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
