// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"

	"github.com/schotek/malachi/backend/internal/graph"
	"github.com/schotek/malachi/backend/internal/store"
)

// graphServerSearch searches a Microsoft 365 account on the service: the
// whole account through the Search API (graph.SearchMailbox, the search
// Outlook runs, one request for every folder), which a personal account
// or a tenant without it refuses (graph.ErrMailboxSearchUnsupported) and
// which then, like a search of one folder or of the folders in: names,
// goes folder by folder through $search (graph.SearchServer). The refusal
// is logged once per search with its code only.
func (b *Backend) graphServerSearch(ctx context.Context, c *graph.Client, accountID string, folders []store.Folder, q graph.ServerSearch, whole bool) ([]graph.Found, error) {
	if whole {
		found, err := graph.SearchMailbox(ctx, c, accountID, folders, q)
		if !errors.Is(err, graph.ErrMailboxSearchUnsupported) {
			return found, err
		}
		b.log.Info("search on the server: the mailbox search is not available, searching the folders", "account", accountID, "err", err.Error())
	}
	return graph.SearchServer(ctx, c, accountID, folders, q)
}
