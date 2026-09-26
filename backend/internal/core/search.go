// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/search"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// searchExcerptRunes caps a result's excerpt.
const searchExcerptRunes = 200

// searchTimeZone is where the days of before: and after: start; tests pin
// it.
var searchTimeZone = time.Local

// searchExcludedRoles are left out of a search that names no folder
// (neither folderId nor in:): deleted and junk mail would crowd the
// results.
var searchExcludedRoles = []api.FolderRole{api.RoleTrash, api.RoleJunk}

// searchRoleNames are the in: keywords that name a folder by its role.
var searchRoleNames = map[string]api.FolderRole{
	"inbox":   api.RoleInbox,
	"sent":    api.RoleSent,
	"drafts":  api.RoleDrafts,
	"trash":   api.RoleTrash,
	"junk":    api.RoleJunk,
	"spam":    api.RoleJunk,
	"archive": api.RoleArchive,
	"outbox":  api.RoleOutbox,
}

type searchService struct{ b *Backend }

// Query searches the local store (docs/api.md, search.query): the
// messages within the offlineDays window, newest first. The query is what
// the user typed and is never logged.
func (s *searchService) Query(ctx context.Context, p api.SearchQueryParams) (*api.SearchQueryResult, error) {
	text := strings.TrimSpace(p.Query)
	switch {
	case text == "":
		return nil, api.NewError(api.CodeInvalidArgument, "query is required")
	case len(p.Query) > api.MaxSearchQueryBytes:
		return nil, api.NewError(api.CodeInvalidArgument, "query is longer than %d bytes", api.MaxSearchQueryBytes)
	case p.FolderID != "" && p.AccountID == "":
		return nil, api.NewError(api.CodeInvalidArgument, "folderId needs accountId")
	}
	q := search.Parse(text)
	if q.Size() > api.MaxSearchTerms {
		return nil, api.NewError(api.CodeInvalidArgument, "query has more than %d terms and filters", api.MaxSearchTerms)
	}
	limit := p.Page.Limit
	if limit <= 0 {
		limit = api.DefaultPageLimit
	}
	if limit > api.MaxPageLimit {
		limit = api.MaxPageLimit
	}

	f := store.SearchFilter{Match: q.MatchExpr(), Unread: q.Unread, Flagged: q.Flagged, Attachments: q.HasAttachment}
	if q.After != nil {
		f.After = q.After.Start(searchTimeZone)
	}
	if q.Before != nil {
		f.Before = q.Before.Start(searchTimeZone)
	}
	var accounts []store.Account // in scope, for in:
	if p.AccountID != "" {
		a, err := s.b.requireAccount(ctx, string(p.AccountID))
		if err != nil {
			return nil, err
		}
		f.AccountID, accounts = a.ID, []store.Account{a}
		if p.FolderID != "" {
			_, err := s.b.store.GetFolder(ctx, a.ID, string(p.FolderID))
			switch {
			case errors.Is(err, store.ErrNotFound):
				return nil, api.NewError(api.CodeFolderNotFound, "unknown folder %q", p.FolderID)
			case err != nil:
				return nil, api.NewError(api.CodeStorageError, "%v", err)
			}
			f.FolderIDs = []string{string(p.FolderID)}
		}
	} else {
		all, err := s.b.store.ListAccounts(ctx)
		if err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		for _, a := range all {
			if a.Enabled {
				accounts = append(accounts, a)
			}
		}
	}

	empty := &api.SearchQueryResult{Results: []api.SearchResult{}, Page: api.PageInfo{Total: 0}}
	if q.Empty() {
		return empty, nil // only words without letters or digits
	}
	switch {
	case len(q.In) > 0:
		ids, err := s.resolveIn(ctx, accounts, q.In)
		if err != nil {
			return nil, err
		}
		if len(f.FolderIDs) > 0 {
			ids = slices.DeleteFunc(ids, func(id string) bool { return id != f.FolderIDs[0] })
		}
		if len(ids) == 0 {
			return empty, nil
		}
		f.FolderIDs = ids // naming a folder reaches Trash and Junk too
	case p.FolderID == "":
		f.ExcludeRoles = searchExcludedRoles
	}

	rows, next, total, err := s.b.store.SearchMessages(ctx, f, p.Page.Cursor, limit)
	switch {
	case errors.Is(err, store.ErrBadCursor):
		return nil, api.NewError(api.CodeInvalidArgument, "invalid page cursor")
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	results := make([]api.SearchResult, 0, len(rows))
	for _, r := range rows {
		sum := toAPISummary(r.Message)
		res := api.SearchResult{Message: sum, Snippet: sum.Snippet}
		if excerpt, ranges, ok := search.Excerpt(r.Text, q, searchExcerptRunes); ok {
			res.Snippet, res.Ranges = excerpt, ranges
		}
		results = append(results, res)
	}
	if err := s.b.attachResultOutbox(ctx, results); err != nil {
		return nil, err
	}
	return &api.SearchQueryResult{Results: results, Page: api.PageInfo{NextCursor: next, Total: total}}, nil
}

// resolveIn turns in: names into folder ids of the accounts in scope: a
// role keyword (inbox, sent, drafts, trash, junk or spam, archive, outbox)
// names the folders of that role, anything else a folder whose path or
// name it is, compared case-insensitively. Several in: filters mean any
// of their folders.
func (s *searchService) resolveIn(ctx context.Context, accounts []store.Account, names []string) ([]string, error) {
	var ids []string
	for _, a := range accounts {
		folders, err := s.b.store.ListFolders(ctx, a.ID)
		if err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		for _, f := range folders {
			for _, name := range names {
				role, isRole := searchRoleNames[strings.ToLower(name)]
				if (isRole && f.Role == role) || strings.EqualFold(f.Path, name) || strings.EqualFold(f.Name, name) {
					ids = append(ids, f.ID)
					break
				}
			}
		}
	}
	return ids, nil
}

// attachResultOutbox fills the outbox state of the results that are
// queued messages, account by account.
func (b *Backend) attachResultOutbox(ctx context.Context, results []api.SearchResult) error {
	byAccount := map[api.AccountID][]int{}
	for i, r := range results {
		byAccount[r.Message.AccountID] = append(byAccount[r.Message.AccountID], i)
	}
	for acc, idx := range byAccount {
		list := make([]api.MessageSummary, len(idx))
		for k, i := range idx {
			list[k] = results[i].Message
		}
		if err := b.attachOutboxEntries(ctx, string(acc), list); err != nil {
			return err
		}
		for k, i := range idx {
			results[i].Message.Outbox = list[k].Outbox
		}
	}
	return nil
}
