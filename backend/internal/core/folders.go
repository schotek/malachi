// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"sort"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

type folderService struct{ b *Backend }

// List returns the account's folders as learned from the last LIST
// (docs/api.md §4.2): unsubscribed folders only on request, role folders
// always; role folders first in a fixed order, then the rest by path.
// Before the first sync the list is empty, not an error.
func (s *folderService) List(ctx context.Context, p api.FolderListParams) (*api.FolderListResult, error) {
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	folders, err := s.b.store.ListFolders(ctx, a.ID)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	kept := make([]store.Folder, 0, len(folders))
	for _, f := range folders {
		if f.Subscribed || p.IncludeUnsubscribed || isRoleFolder(f.Role) {
			kept = append(kept, f)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		ri, rj := roleRank(kept[i].Role), roleRank(kept[j].Role)
		if ri != rj {
			return ri < rj
		}
		if kept[i].Path != kept[j].Path {
			return kept[i].Path < kept[j].Path
		}
		return kept[i].ID < kept[j].ID
	})
	out := make([]api.Folder, 0, len(kept))
	for _, f := range kept {
		out = append(out, toAPIFolder(f))
	}
	return &api.FolderListResult{Folders: out}, nil
}

// Subscribe is not implemented: the sync engine walks every selectable
// folder and a local-only flag would be overwritten by the next LIST.
func (s *folderService) Subscribe(context.Context, api.FolderSubscribeParams) (*api.FolderSubscribeResult, error) {
	return nil, api.ErrNotImplemented
}

// isRoleFolder says whether a folder is always listed.
func isRoleFolder(r api.FolderRole) bool {
	return r != "" && r != api.RoleNone
}

// roleRank orders the special folders; everything else sorts after them by
// path.
func roleRank(r api.FolderRole) int {
	switch r {
	case api.RoleInbox:
		return 0
	case api.RoleDrafts:
		return 1
	case api.RoleSent:
		return 2
	case api.RoleArchive:
		return 3
	case api.RoleJunk:
		return 4
	case api.RoleTrash:
		return 5
	case api.RoleOutbox:
		return 6
	default:
		return 7
	}
}

func toAPIFolder(f store.Folder) api.Folder {
	role := f.Role
	if role == "" {
		role = api.RoleNone
	}
	return api.Folder{
		ID:         api.FolderID(f.ID),
		AccountID:  api.AccountID(f.AccountID),
		ParentID:   api.FolderID(f.ParentID),
		Name:       f.Name,
		Path:       f.Path,
		Role:       role,
		Subscribed: f.Subscribed,
		Selectable: f.Selectable,
		Unread:     f.Unread,
		Total:      f.Total,
	}
}
