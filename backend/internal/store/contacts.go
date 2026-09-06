// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// CollectedAddress is a recipient of mail the user sent, kept for recipient
// completion (collected_addresses, migration 0009).
type CollectedAddress struct {
	Address   string
	Name      string
	FirstUsed time.Time
	LastUsed  time.Time
	Uses      int
}

// maxCollectedNameBytes caps a stored display name; a longer one is
// dropped rather than cut mid-rune.
const maxCollectedNameBytes = 256

// defaultCollectedLimit is SearchCollectedAddresses' limit for limit <= 0.
const defaultCollectedLimit = 20

// TouchCollectedAddresses records one use of each address at the given
// time. Addresses are normalised; ones that do not parse are skipped, not
// an error, and an address listed twice in one call counts once. A
// non-empty name from a use at least as recent as the stored one replaces
// the name; an empty name never does. Uses may arrive out of order (the
// backfill walks a Sent folder newest first): first_used and last_used
// stay the extremes.
func (s *Store) TouchCollectedAddresses(ctx context.Context, addrs []api.Address, at time.Time) error {
	if len(addrs) == 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("touch collected addresses: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO collected_addresses (address, name, name_lc, first_used, last_used, uses)
		VALUES (?, ?, ?, ?, ?, 1)
		ON CONFLICT(address) DO UPDATE SET
			uses       = uses + 1,
			name       = CASE WHEN excluded.name <> '' AND excluded.last_used >= last_used THEN excluded.name ELSE name END,
			name_lc    = CASE WHEN excluded.name <> '' AND excluded.last_used >= last_used THEN excluded.name_lc ELSE name_lc END,
			first_used = MIN(first_used, excluded.first_used),
			last_used  = MAX(last_used, excluded.last_used)`)
	if err != nil {
		return fmt.Errorf("touch collected addresses: %w", err)
	}
	defer stmt.Close()

	when := stamp(at)
	seen := make(map[string]bool, len(addrs))
	for _, a := range addrs {
		address, ok := collectableAddress(a.Address)
		if !ok || seen[address] {
			continue
		}
		seen[address] = true
		name := cleanCollectedName(a.Name)
		if _, err := stmt.ExecContext(ctx, address, name, strings.ToLower(name), when, when); err != nil {
			return fmt.Errorf("touch collected address: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("touch collected addresses: %w", err)
	}
	return nil
}

// SearchCollectedAddresses returns up to limit addresses whose address or
// folded name contains q, most recently used first. An empty q matches
// nothing.
func (s *Store) SearchCollectedAddresses(ctx context.Context, q string, limit int) ([]CollectedAddress, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultCollectedLimit
	}
	pattern := "%" + escapeLike(q) + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT address, name, first_used, last_used, uses FROM collected_addresses
		WHERE address LIKE ? ESCAPE '\' OR name_lc LIKE ? ESCAPE '\'
		ORDER BY last_used DESC, address LIMIT ?`, pattern, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("search collected addresses: %w", err)
	}
	defer rows.Close()

	var out []CollectedAddress
	for rows.Next() {
		var c CollectedAddress
		var first, last string
		if err := rows.Scan(&c.Address, &c.Name, &first, &last, &c.Uses); err != nil {
			return nil, fmt.Errorf("scan collected address: %w", err)
		}
		c.FirstUsed, c.LastUsed = parseStamp(first), parseStamp(last)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search collected addresses: %w", err)
	}
	return out, nil
}

// collectableAddress normalises a bare address and reports whether it is
// one worth storing: it must parse as an address on its own, so a mailbox
// with a display name or a stray comment is not smuggled into the column.
func collectableAddress(s string) (string, bool) {
	s = NormalizeAddress(s)
	if s == "" {
		return "", false
	}
	parsed, err := mail.ParseAddress(s)
	if err != nil || parsed.Address != s {
		return "", false
	}
	return s, true
}

// cleanCollectedName keeps a display name only when it is short, valid
// UTF-8 and free of control characters; anything else is stored as empty
// rather than repaired.
func cleanCollectedName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxCollectedNameBytes || !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return ""
	}
	return s
}

// escapeLike makes s literal inside a LIKE pattern that uses '\' as its
// ESCAPE character.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
