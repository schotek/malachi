// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"strings"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/thread"
	"github.com/schotek/malachi/backend/pkg/api"
)

// threadService is docs/api.md §4.4: conversations computed per account by
// the store as messages arrive, listed per folder.
type threadService struct{ b *Backend }

// List pages through the conversations with a member in the folder; every
// aggregate covers the members in that folder.
func (s *threadService) List(ctx context.Context, p api.ThreadListParams) (*api.ThreadListResult, error) {
	if p.AccountID == "" || p.FolderID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and folderId are required")
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	limit := p.Page.Limit
	if limit <= 0 {
		limit = api.DefaultPageLimit
	}
	if limit > api.MaxPageLimit {
		limit = api.MaxPageLimit
	}
	sortOrder := p.Sort
	switch sortOrder {
	case "":
		sortOrder = api.SortDateDesc
	case api.SortDateDesc, api.SortDateAsc:
	default:
		return nil, api.NewError(api.CodeInvalidArgument, "sort must be dateDesc or dateAsc")
	}
	listFilter := p.Filter
	switch listFilter {
	case "":
		listFilter = api.FilterAll
	case api.FilterAll, api.FilterUnread, api.FilterFlagged:
	default:
		return nil, api.NewError(api.CodeInvalidArgument, "filter must be all, unread or flagged")
	}
	items, next, total, err := s.b.store.ListThreads(ctx, a.ID, string(p.FolderID), p.Page.Cursor, limit, sortOrder, listFilter)
	switch {
	case errors.Is(err, store.ErrBadCursor):
		return nil, api.NewError(api.CodeInvalidArgument, "invalid page cursor")
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeFolderNotFound, "unknown folder %q", p.FolderID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	out := make([]api.ThreadSummary, 0, len(items))
	latest := make([]api.MessageSummary, 0, len(items))
	for _, r := range items {
		t := toAPIThread(r)
		out = append(out, t)
		latest = append(latest, t.Latest)
	}
	// The outbox decoration writes into the slice it is given.
	if err := s.b.attachOutboxInfo(ctx, a.ID, string(p.FolderID), latest); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Latest = latest[i]
	}
	return &api.ThreadListResult{Threads: out, Page: api.PageInfo{NextCursor: next, Total: total}}, nil
}

// Get returns one conversation with its members, in the folder given or
// across the account.
func (s *threadService) Get(ctx context.Context, p api.ThreadGetParams) (*api.ThreadGetResult, error) {
	if p.AccountID == "" || p.ThreadID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and threadId are required")
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	folderID := string(p.FolderID)
	if folderID != "" {
		if _, err := s.b.store.GetFolder(ctx, a.ID, folderID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, api.NewError(api.CodeFolderNotFound, "unknown folder %q", p.FolderID)
			}
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
	}
	row, err := s.b.store.GetThread(ctx, a.ID, string(p.ThreadID), folderID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeThreadNotFound, "unknown thread %q", p.ThreadID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	members, err := s.b.store.ThreadMessages(ctx, a.ID, string(p.ThreadID), folderID, api.MaxThreadMessages)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeThreadNotFound, "unknown thread %q", p.ThreadID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	summary := toAPIThread(row)
	// Members plus the summary's newest one, decorated together: a queued
	// reply in the outbox folder is a member like any other.
	all := make([]api.MessageSummary, 0, len(members)+1)
	for _, m := range members {
		all = append(all, toAPISummary(m))
	}
	all = append(all, summary.Latest)
	if folderID != "" {
		err = s.b.attachOutboxInfo(ctx, a.ID, folderID, all)
	} else {
		err = s.b.attachOutboxEntries(ctx, a.ID, all)
	}
	if err != nil {
		return nil, err
	}
	summary.Latest = all[len(all)-1]
	return &api.ThreadGetResult{Thread: summary, Messages: all[:len(all)-1]}, nil
}

// toAPIThread maps a store row; the subject loses its reply markers and
// falls back to the raw one when nothing else is left.
func toAPIThread(r store.ThreadRow) api.ThreadSummary {
	subject := thread.NormalizeSubject(r.Latest.Subject)
	if subject == "" {
		subject = strings.TrimSpace(r.Latest.Subject)
	}
	folders := make([]api.FolderID, 0, len(r.FolderIDs))
	for _, f := range r.FolderIDs {
		folders = append(folders, api.FolderID(f))
	}
	return api.ThreadSummary{
		ID:             api.ThreadID(r.ID),
		AccountID:      api.AccountID(r.AccountID),
		Subject:        subject,
		Participants:   nonNilAddresses(r.Participants),
		MessageCount:   r.MessageCount,
		UnreadCount:    r.UnreadCount,
		LatestDate:     r.Latest.Date,
		Latest:         toAPISummary(r.Latest),
		Snippet:        r.Latest.Snippet,
		Flags:          nonNilFlags(r.Flags),
		HasAttachments: r.HasAttachments,
		FolderIDs:      folders,
	}
}
