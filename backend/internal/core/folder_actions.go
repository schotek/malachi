// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The folder-wide actions of the clients' context menus (docs/api.md §4.2
// folder.markRead and folder.empty). Both work on what the daemon stores,
// the messages of the offlineDays window that the folder shows; messages
// older than that, kept on the server only, are left alone. They go the
// way of message.flag and message.delete, in batches of
// api.MaxMessageIDsPerCall, one store transaction and one queued
// operation per message each, so the syncer pushes them as it pushes the
// user's own: no operation of a new kind, no server-wide command.

// foldersRetries is how often a batch is listed again when a message of it
// went meanwhile (a pass moved or expunged it between the listing and the
// change, which the store refuses as a whole).
const foldersRetries = 3

// MarkRead marks every stored, unhidden unread message of the folder read.
// An outbox is refused; an issue-tracker account's flags are local and
// reach the copies in its other folders, as with message.flag.
func (s *folderService) MarkRead(ctx context.Context, p api.FolderMarkReadParams) (*api.FolderMarkReadResult, error) {
	a, f, err := s.b.requireFolder(ctx, p.AccountID, p.FolderID)
	if err != nil {
		return nil, err
	}
	if f.Role == api.RoleOutbox {
		return nil, api.NewError(api.CodeInvalidArgument, "an outbox has nothing to mark read")
	}
	issue := isIssueAccount(a)
	marked, err := s.b.inBatches(ctx, func() ([]string, error) {
		return s.b.store.FolderMessageIDs(ctx, f.ID, true, false)
	}, func(ids []string) error {
		if err := s.b.store.FlagMessages(ctx, a.ID, ids, []api.Flag{api.FlagSeen}, nil); err != nil {
			return err
		}
		if issue {
			return s.b.flagCopies(ctx, a.ID, ids)
		}
		return nil
	})
	if marked > 0 {
		if !issue {
			s.b.Supervisor.Trigger(a.ID, "", false)
		}
		// The copies of an issue tracker's messages are in its other
		// folders too: any folder of the account.
		folders := []string{f.ID}
		if issue {
			folders = nil
		}
		s.b.messagesChanged(map[string][]string{a.ID: folders})
	}
	if err != nil {
		return nil, err
	}
	return &api.FolderMarkReadResult{Marked: marked}, nil
}

// Empty deletes every stored message of a Trash or Junk folder for good,
// hidden ones too, as message.delete with permanent does.
func (s *folderService) Empty(ctx context.Context, p api.FolderEmptyParams) (*api.FolderEmptyResult, error) {
	a, f, err := s.b.requireFolder(ctx, p.AccountID, p.FolderID)
	if err != nil {
		return nil, err
	}
	if f.Role != api.RoleTrash && f.Role != api.RoleJunk {
		return nil, api.NewError(api.CodeInvalidArgument, "only a trash or junk folder can be emptied")
	}
	if err := requireCapability(a, api.CapabilityDelete); err != nil {
		return nil, err
	}
	deleted, err := s.b.inBatches(ctx, func() ([]string, error) {
		return s.b.store.FolderMessageIDs(ctx, f.ID, false, true)
	}, func(ids []string) error {
		dropped, err := s.b.store.DeleteMessages(ctx, a.ID, ids)
		if err != nil {
			return err
		}
		s.b.boardDraftsDropped(ctx, a.ID, dropped)
		return nil
	})
	if deleted > 0 {
		s.b.Supervisor.Trigger(a.ID, "", false)
		s.b.messagesChanged(map[string][]string{a.ID: {f.ID}})
	}
	if err != nil {
		return nil, err
	}
	return &api.FolderEmptyResult{Deleted: deleted}, nil
}

// requireFolder looks up the account and one of its folders.
func (b *Backend) requireFolder(ctx context.Context, accountID api.AccountID, folderID api.FolderID) (store.Account, store.Folder, error) {
	if folderID == "" {
		return store.Account{}, store.Folder{}, api.NewError(api.CodeInvalidArgument, "folderId is required")
	}
	a, err := b.requireAccount(ctx, string(accountID))
	if err != nil {
		return store.Account{}, store.Folder{}, err
	}
	f, err := b.store.GetFolder(ctx, a.ID, string(folderID))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.Account{}, store.Folder{}, api.NewError(api.CodeFolderNotFound, "unknown folder %q", folderID)
	case err != nil:
		return store.Account{}, store.Folder{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	return a, f, nil
}

// inBatches applies change to what list returns, api.MaxMessageIDsPerCall
// ids at a time, and returns how many ids it changed. A batch the store
// refuses because one of its messages went meanwhile is listed afresh
// (what was changed no longer qualifies), at most foldersRetries times.
// On any other failure the batches done stay done and the error is the
// API's.
func (b *Backend) inBatches(ctx context.Context, list func() ([]string, error), change func(ids []string) error) (int, error) {
	done := 0
	for attempt := 0; ; attempt++ {
		ids, err := list()
		if err != nil {
			return done, api.NewError(api.CodeStorageError, "%v", err)
		}
		gone := false
		for len(ids) > 0 {
			n := min(len(ids), api.MaxMessageIDsPerCall)
			err := change(ids[:n])
			if errors.Is(err, store.ErrNotFound) && attempt < foldersRetries {
				gone = true
				break
			}
			if err != nil {
				var apiErr *api.Error
				if errors.As(err, &apiErr) {
					return done, err
				}
				return done, mutationError(err)
			}
			done += n
			ids = ids[n:]
		}
		if !gone {
			return done, nil
		}
	}
}
