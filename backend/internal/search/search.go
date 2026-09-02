// Package search provides full-text search over the local store using
// SQLite FTS5 (external-content table over the messages table, trigram or
// unicode61 tokenizer to be decided with real data).
//
// Query syntax exposed to users (subset, see docs/api.md): free text,
// quoted phrases, and field prefixes from:, to:, subject:, has:attachment,
// in:<folder>, before:/after:<date>. The user string is parsed here and
// compiled into a parameterised FTS5 MATCH expression; user input is never
// concatenated into SQL.
package search

import (
	"context"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Index is the search backend. TODO(phase-5): implement.
type Index interface {
	Query(ctx context.Context, p api.SearchQueryParams) (*api.SearchQueryResult, error)
	// Reindex rebuilds the index for one account (after tokenizer changes).
	Reindex(ctx context.Context, account api.AccountID) error
}
