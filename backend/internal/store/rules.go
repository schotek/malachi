// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The mail rules (migration 0025, docs/api.md §4.21). The store keeps
// what internal/rules checked; it does not judge a rule itself.

var (
	// ErrRuleLimit: a new rule would be more than api.MaxRules.
	ErrRuleLimit = errors.New("store: too many rules")
	// ErrRuleOrder: a reorder does not name every rule exactly once.
	ErrRuleOrder = errors.New("store: the order does not name every rule once")
)

// Rule is a stored rule with what only the daemon needs: when it was last
// enabled (the automatic run leaves older mail alone).
type Rule struct {
	api.Rule
	EnabledAt time.Time
}

const ruleColumns = `id, position, name, enabled, account_id, match, conditions, actions, stop,
	problem, created_at, updated_at, enabled_at, applied, last_applied_at, skipped`

func scanRule(row scanner) (Rule, error) {
	var r Rule
	var position, created, updated, enabledAt int64
	var enabled, stop int
	var account sql.NullString
	var lastApplied sql.NullInt64
	var conds, acts, match, problem string
	if err := row.Scan(&r.ID, &position, &r.Name, &enabled, &account, &match, &conds, &acts, &stop,
		&problem, &created, &updated, &enabledAt, &r.Applied, &lastApplied, &r.Skipped); err != nil {
		return Rule{}, err
	}
	r.Enabled, r.Stop = enabled != 0, stop != 0
	r.AccountID = api.AccountID(account.String)
	r.Match = api.RuleMatch(match)
	r.Problem = api.RuleProblem(problem)
	if err := json.Unmarshal([]byte(conds), &r.Conditions); err != nil {
		return Rule{}, fmt.Errorf("decode conditions of rule %s: %w", r.ID, err)
	}
	if err := json.Unmarshal([]byte(acts), &r.Actions); err != nil {
		return Rule{}, fmt.Errorf("decode actions of rule %s: %w", r.ID, err)
	}
	if r.Conditions == nil {
		r.Conditions = []api.RuleCondition{}
	}
	if r.Actions == nil {
		r.Actions = []api.RuleAction{}
	}
	for _, a := range r.Actions {
		if a.Kind.IsOutgoing() {
			r.Outgoing = true
		}
	}
	r.CreatedAt, r.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	if enabledAt > 0 {
		r.EnabledAt = time.Unix(enabledAt, 0).UTC()
	}
	if lastApplied.Valid {
		t := time.Unix(lastApplied.Int64, 0).UTC()
		r.LastAppliedAt = &t
	}
	return r, nil
}

type ruleQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func listRules(ctx context.Context, q ruleQueryer) ([]Rule, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+ruleColumns+` FROM rules ORDER BY position, id`)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, fmt.Errorf("list rules: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	return out, nil
}

// ListRules returns every rule in its order.
func (s *Store) ListRules(ctx context.Context) ([]Rule, error) {
	return listRules(ctx, s.db)
}

// GetRule returns one rule; ErrNotFound when there is none.
func (s *Store) GetRule(ctx context.Context, id string) (Rule, error) {
	r, err := scanRule(s.db.QueryRowContext(ctx, `SELECT `+ruleColumns+` FROM rules WHERE id = ?`, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Rule{}, ErrNotFound
	case err != nil:
		return Rule{}, fmt.Errorf("get rule: %w", err)
	}
	return r, nil
}

func getRuleTx(ctx context.Context, tx *sql.Tx, id string) (Rule, error) {
	r, err := scanRule(tx.QueryRowContext(ctx, `SELECT `+ruleColumns+` FROM rules WHERE id = ?`, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Rule{}, ErrNotFound
	case err != nil:
		return Rule{}, fmt.Errorf("get rule: %w", err)
	}
	return r, nil
}

func encodeRuleInput(in api.RuleInput) (conds, acts string, account any, err error) {
	c := in.Conditions
	if c == nil {
		c = []api.RuleCondition{}
	}
	a := in.Actions
	if a == nil {
		a = []api.RuleAction{}
	}
	cb, err := json.Marshal(c)
	if err != nil {
		return "", "", nil, fmt.Errorf("encode conditions: %w", err)
	}
	ab, err := json.Marshal(a)
	if err != nil {
		return "", "", nil, fmt.Errorf("encode actions: %w", err)
	}
	if in.AccountID != "" {
		account = string(in.AccountID)
	}
	return string(cb), string(ab), account, nil
}

// CreateRule adds a rule at the end of the order; ErrRuleLimit when there
// are api.MaxRules already.
func (s *Store) CreateRule(ctx context.Context, in api.RuleInput, now time.Time) (Rule, error) {
	conds, acts, account, err := encodeRuleInput(in)
	if err != nil {
		return Rule{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Rule{}, fmt.Errorf("create rule: %w", err)
	}
	defer tx.Rollback()
	var n int
	var maxPos sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), MAX(position) FROM rules`).Scan(&n, &maxPos); err != nil {
		return Rule{}, fmt.Errorf("create rule: %w", err)
	}
	if n >= api.MaxRules {
		return Rule{}, ErrRuleLimit
	}
	id := newID("r_")
	at := now.Unix()
	var enabledAt int64
	if in.Enabled {
		enabledAt = at
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO rules (id, position, name, enabled, account_id, match,
		conditions, actions, stop, problem, created_at, updated_at, enabled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?)`,
		id, maxPos.Int64+1, in.Name, boolInt(in.Enabled), account, string(in.Match), conds, acts,
		boolInt(in.Stop), at, at, enabledAt); err != nil {
		return Rule{}, fmt.Errorf("create rule: %w", err)
	}
	r, err := getRuleTx(ctx, tx, id)
	if err != nil {
		return Rule{}, err
	}
	if err := tx.Commit(); err != nil {
		return Rule{}, fmt.Errorf("create rule: %w", err)
	}
	return r, nil
}

// UpdateRule replaces a rule's input; check sees the stored rule first
// and its error aborts the update (returned as it is). The problem is
// cleared; enabling a rule that was not enabled sets EnabledAt to now.
// ErrNotFound when there is no such rule.
func (s *Store) UpdateRule(ctx context.Context, id string, in api.RuleInput, now time.Time, check func(old Rule) error) (Rule, error) {
	conds, acts, account, err := encodeRuleInput(in)
	if err != nil {
		return Rule{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Rule{}, fmt.Errorf("update rule: %w", err)
	}
	defer tx.Rollback()
	old, err := getRuleTx(ctx, tx, id)
	if err != nil {
		return Rule{}, err
	}
	if check != nil {
		if err := check(old); err != nil {
			return Rule{}, err
		}
	}
	enabledAt := int64(0)
	if !old.EnabledAt.IsZero() {
		enabledAt = old.EnabledAt.Unix()
	}
	if in.Enabled && !old.Enabled {
		enabledAt = now.Unix()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rules SET name = ?, enabled = ?, account_id = ?, match = ?,
		conditions = ?, actions = ?, stop = ?, problem = '', updated_at = ?, enabled_at = ? WHERE id = ?`,
		in.Name, boolInt(in.Enabled), account, string(in.Match), conds, acts, boolInt(in.Stop),
		now.Unix(), enabledAt, id); err != nil {
		return Rule{}, fmt.Errorf("update rule: %w", err)
	}
	r, err := getRuleTx(ctx, tx, id)
	if err != nil {
		return Rule{}, err
	}
	if err := tx.Commit(); err != nil {
		return Rule{}, fmt.Errorf("update rule: %w", err)
	}
	return r, nil
}

// DeleteRule removes a rule and its ledgers; ErrNotFound when there is
// none.
func (s *Store) DeleteRule(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	for _, q := range []string{`DELETE FROM rule_replies WHERE rule_id = ?`, `DELETE FROM rule_outgoing WHERE rule_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("delete rule: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	return nil
}

// ReorderRules sets the order: ids must name every rule exactly once
// (ErrRuleOrder otherwise). It returns the rules in the new order.
func (s *Store) ReorderRules(ctx context.Context, ids []string) ([]Rule, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reorder rules: %w", err)
	}
	defer tx.Rollback()
	current, err := listRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	if len(ids) != len(current) {
		return nil, ErrRuleOrder
	}
	known := make(map[string]bool, len(current))
	for _, r := range current {
		known[r.ID] = true
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !known[id] || seen[id] {
			return nil, ErrRuleOrder
		}
		seen[id] = true
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE rules SET position = ? WHERE id = ?`, i+1, id); err != nil {
			return nil, fmt.Errorf("reorder rules: %w", err)
		}
	}
	out, err := listRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("reorder rules: %w", err)
	}
	return out, nil
}

// DisableRule disables an enabled rule with a problem; false when it was
// not enabled (or is gone).
func (s *Store) DisableRule(ctx context.Context, id string, problem api.RuleProblem, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE rules SET enabled = 0, problem = ?, updated_at = ?
		WHERE id = ? AND enabled = 1`, string(problem), now.Unix(), id)
	if err != nil {
		return false, fmt.Errorf("disable rule: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// NoteRulesApplied counts messages the rules acted on (rule id → how
// many) and sets their last application.
func (s *Store) NoteRulesApplied(ctx context.Context, counts map[string]int, at time.Time) error {
	if len(counts) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("note rules applied: %w", err)
	}
	defer tx.Rollback()
	for id, n := range counts {
		if n <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE rules SET applied = applied + ?,
			last_applied_at = MAX(COALESCE(last_applied_at, 0), ?) WHERE id = ?`, n, at.Unix(), id); err != nil {
			return fmt.Errorf("note rules applied: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("note rules applied: %w", err)
	}
	return nil
}

// NoteRuleSkipped counts an outgoing action the protections refused.
func (s *Store) NoteRuleSkipped(ctx context.Context, id string, n int) error {
	if n <= 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE rules SET skipped = skipped + ? WHERE id = ?`, n, id); err != nil {
		return fmt.Errorf("note rule skipped: %w", err)
	}
	return nil
}

// ClaimRuleSeen records that the message key of the account went through
// the automatic run; false when it had already (R3). Atomic: of two
// callers with the same key one gets true.
func (s *Store) ClaimRuleSeen(ctx context.Context, accountID, key string, at time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO rule_seen (account_id, rfc_message_id, seen_at) VALUES (?, ?, ?)
		ON CONFLICT (account_id, rfc_message_id) DO NOTHING`, accountID, key, at.Unix())
	if err != nil {
		return false, fmt.Errorf("claim rule seen: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RuleBodyText is what a body condition reads of a stored message: the
// first maxChars characters of its plain text, and whether the body is
// stored (ErrNotFound when the message is gone).
func (s *Store) RuleBodyText(ctx context.Context, accountID, id string, maxChars int) (string, bool, error) {
	var text, state string
	err := s.db.QueryRowContext(ctx, `SELECT substr(text_body, 1, ?), body_state FROM messages WHERE id = ? AND account_id = ?`,
		maxChars, id, accountID).Scan(&text, &state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, ErrNotFound
	case err != nil:
		return "", false, fmt.Errorf("rule body text: %w", err)
	}
	return text, BodyState(state) == BodyFetched, nil
}

// RuleMessageKey is the key of rule_seen for a message: its Message-ID,
// else its id.
func RuleMessageKey(m Message) string {
	if id := strings.TrimSpace(m.RFCMessageID); id != "" {
		return id
	}
	return "id:" + m.ID
}

// OutgoingVerdict is what ReserveRuleOutgoing decided.
type OutgoingVerdict int

const (
	// OutgoingAllowed: the message may be queued; it is counted.
	OutgoingAllowed OutgoingVerdict = iota
	// OutgoingHourly: the rule sent its per-hour limit already.
	OutgoingHourly
	// OutgoingReplied: the rule answered this sender within the interval.
	OutgoingReplied
)

// ReserveRuleOutgoing decides, in one transaction, whether a rule may
// send one more message now: fewer than perHour in the hour before now,
// and for an automatic reply (sender not empty) no reply to that sender
// within interval. An allowed message is recorded at once, so two callers
// never both get the last slot.
func (s *Store) ReserveRuleOutgoing(ctx context.Context, ruleID, sender string, now time.Time, perHour int, interval time.Duration) (OutgoingVerdict, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("reserve rule outgoing: %w", err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM rule_outgoing WHERE rule_id = ? AND sent_at > ?`,
		ruleID, now.Add(-time.Hour).Unix()).Scan(&n); err != nil {
		return 0, fmt.Errorf("reserve rule outgoing: %w", err)
	}
	if n >= perHour {
		return OutgoingHourly, nil
	}
	if sender != "" {
		var last int64
		err := tx.QueryRowContext(ctx, `SELECT sent_at FROM rule_replies WHERE rule_id = ? AND sender = ?`, ruleID, sender).Scan(&last)
		switch {
		case err == nil:
			if now.Unix()-last < int64(interval/time.Second) {
				return OutgoingReplied, nil
			}
		case !errors.Is(err, sql.ErrNoRows):
			return 0, fmt.Errorf("reserve rule outgoing: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO rule_replies (rule_id, sender, sent_at) VALUES (?, ?, ?)
			ON CONFLICT (rule_id, sender) DO UPDATE SET sent_at = excluded.sent_at`, ruleID, sender, now.Unix()); err != nil {
			return 0, fmt.Errorf("reserve rule outgoing: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO rule_outgoing (rule_id, sent_at) VALUES (?, ?)`, ruleID, now.Unix()); err != nil {
		return 0, fmt.Errorf("reserve rule outgoing: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("reserve rule outgoing: %w", err)
	}
	return OutgoingAllowed, nil
}

// PruneRuleLedgers forgets what no rule needs any more: queued messages
// older than an hour, replies older than replyInterval, seen keys older
// than seenAge, and rows of rules that are gone.
func (s *Store) PruneRuleLedgers(ctx context.Context, now time.Time, replyInterval, seenAge time.Duration) error {
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM rule_outgoing WHERE sent_at <= ? OR rule_id NOT IN (SELECT id FROM rules)`, []any{now.Add(-time.Hour).Unix()}},
		{`DELETE FROM rule_replies WHERE sent_at <= ? OR rule_id NOT IN (SELECT id FROM rules)`, []any{now.Add(-replyInterval).Unix()}},
		{`DELETE FROM rule_seen WHERE seen_at <= ?`, []any{now.Add(-seenAge).Unix()}},
	} {
		if _, err := s.db.ExecContext(ctx, q.sql, q.args...); err != nil {
			return fmt.Errorf("prune rule ledgers: %w", err)
		}
	}
	return nil
}

// deleteRulesOfAccountTx removes the account's rules with their ledgers
// and its seen keys (DeleteAccount).
func deleteRulesOfAccountTx(ctx context.Context, tx *sql.Tx, accountID string) error {
	for _, q := range []string{
		`DELETE FROM rule_replies WHERE rule_id IN (SELECT id FROM rules WHERE account_id = ?)`,
		`DELETE FROM rule_outgoing WHERE rule_id IN (SELECT id FROM rules WHERE account_id = ?)`,
		`DELETE FROM rules WHERE account_id = ?`,
		`DELETE FROM rule_seen WHERE account_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, accountID); err != nil {
			return fmt.Errorf("delete account rules: %w", err)
		}
	}
	return nil
}
