// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"slices"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The unified listing (docs/api.md §4.3 and §4.4, "Unified listing";
// docs/architecture.md §7): message.list and thread.list with `unified`
// set to a folder role list the folders of that role of every enabled
// mail account in one page. Only api.UnifiedRoles are accepted.

// isMailAccount: an account of a mail kind (imap or graph), the only ones
// a unified listing covers; never jira, caldav or eway.
func isMailAccount(a store.Account) bool {
	switch a.Config.Protocol() {
	case api.AccountIMAP, api.AccountGraph:
		return true
	}
	return false
}

// checkUnified validates the unified parameters of a listing: no account
// or folder beside them, and a role of api.UnifiedRoles.
func checkUnified(role api.FolderRole, accountID api.AccountID, folderID api.FolderID) error {
	if accountID != "" || folderID != "" {
		return api.NewError(api.CodeInvalidArgument, "unified excludes accountId and folderId")
	}
	if !slices.Contains(api.UnifiedRoles, role) {
		return api.NewError(api.CodeInvalidArgument, "unified must be one of %v", api.UnifiedRoles)
	}
	return nil
}

// unifiedFolders is the scope of a unified listing: the synced, non-virtual
// folders of the role of every enabled mail account, in the order of the
// accounts. accounts maps the id of each account in scope.
func (b *Backend) unifiedFolders(ctx context.Context, role api.FolderRole) (refs []store.FolderRef, accounts map[string]store.Account, err error) {
	all, err := b.store.ListAccounts(ctx)
	if err != nil {
		return nil, nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	accounts = map[string]store.Account{}
	for _, a := range all {
		if !a.Enabled || !isMailAccount(a) {
			continue
		}
		folders, err := b.store.ListFolders(ctx, a.ID)
		if err != nil {
			return nil, nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		for _, f := range folders {
			if f.Role == role && !f.Unsynced && f.Virtual == "" {
				refs = append(refs, store.FolderRef{AccountID: a.ID, FolderID: f.ID})
				accounts[a.ID] = a
			}
		}
	}
	return refs, accounts, nil
}

// byAccount groups the indexes of a page by the account of each row, in
// the order the accounts first appear: decorations run per account and
// write back index for index, so the page keeps its order.
func byAccount(n int, accountOf func(i int) string) (order []string, groups map[string][]int) {
	groups = map[string][]int{}
	for i := 0; i < n; i++ {
		id := accountOf(i)
		if _, ok := groups[id]; !ok {
			order = append(order, id)
		}
		groups[id] = append(groups[id], i)
	}
	return order, groups
}

// unified is the unified branch of message.list.
func (s *messageService) unified(ctx context.Context, p api.MessageListParams, limit int, sortOrder api.SortOrder, listFilter api.MessageFilter) (*api.MessageListResult, error) {
	refs, accounts, err := s.b.unifiedFolders(ctx, p.Unified)
	if err != nil {
		return nil, err
	}
	items, next, total, err := s.b.store.ListMessagesIn(ctx, refs, p.Page.Cursor, limit, sortOrder, listFilter)
	if err := listError(err, ""); err != nil {
		return nil, err
	}
	out := make([]api.MessageSummary, 0, len(items))
	for _, m := range items {
		out = append(out, toAPISummary(m))
	}
	// No outbox information: the unified roles never include the outbox.
	order, groups := byAccount(len(items), func(i int) string { return items[i].AccountID })
	for _, id := range order {
		idx := groups[id]
		rows := make([]store.Message, len(idx))
		sums := make([]api.MessageSummary, len(idx))
		for k, i := range idx {
			rows[k], sums[k] = items[i], out[i]
		}
		if err := s.b.decorateRows(ctx, accounts[id], rows, sums); err != nil {
			return nil, err
		}
		for k, i := range idx {
			out[i] = sums[k]
		}
	}
	return &api.MessageListResult{Messages: out, Page: api.PageInfo{NextCursor: next, Total: total}}, nil
}

// unified is the unified branch of thread.list. SentCount and DraftCount
// of each row are relative to the folder of its account the row is of,
// as thread.list of that folder has them.
func (s *threadService) unified(ctx context.Context, p api.ThreadListParams, limit int, sortOrder api.SortOrder, listFilter api.MessageFilter) (*api.ThreadListResult, error) {
	refs, accounts, err := s.b.unifiedFolders(ctx, p.Unified)
	if err != nil {
		return nil, err
	}
	items, next, total, err := s.b.store.ListThreadsIn(ctx, refs, p.Page.Cursor, limit, sortOrder, listFilter)
	if err := listError(err, ""); err != nil {
		return nil, err
	}
	out := make([]api.ThreadSummary, 0, len(items))
	for _, r := range items {
		out = append(out, toAPIThread(r))
	}
	order, groups := byAccount(len(items), func(i int) string { return items[i].AccountID })
	for _, id := range order {
		idx := groups[id]
		rows := make([]store.Message, len(idx))
		sums := make([]api.ThreadSummary, len(idx))
		for k, i := range idx {
			rows[k], sums[k] = items[i].Latest, out[i]
		}
		if err := s.b.decorateThreads(ctx, accounts[id], rows, sums); err != nil {
			return nil, err
		}
		for k, i := range idx {
			out[i] = sums[k]
		}
	}
	return &api.ThreadListResult{Threads: out, Page: api.PageInfo{NextCursor: next, Total: total}}, nil
}

// listError maps a store error of a listing; folderID names the folder of
// a single-folder listing ("" for a unified one, which has no unknown
// folder).
func listError(err error, folderID api.FolderID) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrBadCursor):
		return api.NewError(api.CodeInvalidArgument, "invalid page cursor")
	case folderID != "" && errors.Is(err, store.ErrNotFound):
		return api.NewError(api.CodeFolderNotFound, "unknown folder %q", folderID)
	default:
		return api.NewError(api.CodeStorageError, "%v", err)
	}
}
