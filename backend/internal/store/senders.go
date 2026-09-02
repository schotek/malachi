package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// KnownSender is one row of the remote-content allow-list.
type KnownSender struct {
	Address string
	Source  string // "sent" | "user"
	AddedAt time.Time
}

// timeLayout matches the strftime default used by the migrations.
const timeLayout = "2006-01-02T15:04:05.000Z"

// NormalizeAddress is how addresses are stored and compared: trimmed and
// lower-cased. Callers validate syntax before storing.
func NormalizeAddress(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// AddKnownSender records address with the given source. Adding an address
// that already exists is a no-op (the original source is kept).
func (s *Store) AddKnownSender(ctx context.Context, address, source string) error {
	address = NormalizeAddress(address)
	if address == "" {
		return fmt.Errorf("add known sender: empty address")
	}
	// ON CONFLICT DO NOTHING only swallows the duplicate-address case; the
	// CHECK on source still fails loudly (INSERT OR IGNORE would hide it).
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO known_senders (address, source) VALUES (?, ?)
		 ON CONFLICT(address) DO NOTHING`, address, source)
	if err != nil {
		return fmt.Errorf("add known sender: %w", err)
	}
	return nil
}

// RemoveKnownSender deletes address; removing an unknown address is not an
// error.
func (s *Store) RemoveKnownSender(ctx context.Context, address string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM known_senders WHERE address = ?`, NormalizeAddress(address))
	if err != nil {
		return fmt.Errorf("remove known sender: %w", err)
	}
	return nil
}

// IsKnownSender reports whether address is on the allow-list.
func (s *Store) IsKnownSender(ctx context.Context, address string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM known_senders WHERE address = ?`, NormalizeAddress(address)).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("lookup known sender: %w", err)
	}
	return n > 0, nil
}

// ListKnownSenders returns the allow-list ordered by address.
func (s *Store) ListKnownSenders(ctx context.Context) ([]KnownSender, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT address, source, added_at FROM known_senders ORDER BY address`)
	if err != nil {
		return nil, fmt.Errorf("list known senders: %w", err)
	}
	defer rows.Close()

	var out []KnownSender
	for rows.Next() {
		var ks KnownSender
		var added string
		if err := rows.Scan(&ks.Address, &ks.Source, &added); err != nil {
			return nil, fmt.Errorf("scan known sender: %w", err)
		}
		if t, err := time.Parse(timeLayout, added); err == nil {
			ks.AddedAt = t
		}
		out = append(out, ks)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list known senders: %w", err)
	}
	return out, nil
}
