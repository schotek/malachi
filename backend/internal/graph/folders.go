// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"net/url"
	"strings"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// wellKnown maps Graph's well-known folder names to roles. The ids are
// resolved once per session: they never change for a mailbox.
var wellKnown = []struct {
	name string
	role api.FolderRole
}{
	{"inbox", api.RoleInbox},
	{"sentitems", api.RoleSent},
	{"drafts", api.RoleDrafts},
	{"deleteditems", api.RoleTrash},
	{"junkemail", api.RoleJunk},
	{"archive", api.RoleArchive},
}

// storedRoles rebuilds the role map from the folders a previous pass
// stored: folders.mailbox is the Graph id and folders.role the role
// resolveRoles assigned it. nil when the account has no folders yet (the
// caller then asks the service), and a full pass re-resolves anyway, which
// is what picks up a mailbox whose well-known folders were re-assigned.
func (s *Syncer) storedRoles(ctx context.Context) (map[string]api.FolderRole, error) {
	folders, err := s.deps.Store.ListFolders(ctx, s.account.ID)
	if err != nil {
		return nil, storageError(err)
	}
	roles := make(map[string]api.FolderRole, len(wellKnown))
	for _, f := range folders {
		// The outbox is local (no Graph id) and has no well-known folder.
		if f.Mailbox == "" || f.Role == "" || f.Role == api.RoleNone || f.Role == api.RoleOutbox {
			continue
		}
		roles[f.Mailbox] = f.Role
	}
	if len(roles) == 0 {
		return nil, nil
	}
	return roles, nil
}

// folderSelect is the $select of folder listings.
const folderSelect = "id,displayName,parentFolderId,childFolderCount,totalItemCount,unreadItemCount,isHidden"

// maxFolders bounds the hierarchy walk: a hostile or absurd mailbox must
// not turn one pass into thousands of requests.
const maxFolders = 2000

// resolveRoles fetches the ids of the well-known folders. A missing one
// (no archive, say) is simply absent.
func resolveRoles(ctx context.Context, c *Client) (map[string]api.FolderRole, error) {
	roles := make(map[string]api.FolderRole, len(wellKnown))
	for _, wk := range wellKnown {
		var f mailFolder
		err := c.Get(ctx, "me/mailFolders/"+wk.name+"?$select=id", &f)
		switch {
		case IsNotFound(err):
			continue
		case err != nil:
			return nil, err
		}
		if f.ID != "" {
			roles[f.ID] = wk.role
		}
	}
	return roles, nil
}

// listFolders walks the whole hierarchy (top level, then child folders of
// every folder that has some) and returns the store's folder batch in
// store.SortSyncOrder. Mailbox is the Graph id, Path the "/"-joined display
// names, ParentMailbox the parent's id when the parent is in the batch.
// Hidden folders are skipped.
func listFolders(ctx context.Context, c *Client, roles map[string]api.FolderRole) ([]store.Folder, error) {
	var out []store.Folder
	paths := map[string]string{} // id → display path
	var walk func(parentPath, listURL string) error
	walk = func(parentPath, listURL string) error {
		for next := listURL; next != ""; {
			var pg page[mailFolder]
			if err := c.Get(ctx, next, &pg); err != nil {
				return err
			}
			for _, f := range pg.Value {
				if f.ID == "" || f.IsHidden {
					continue
				}
				if len(out) >= maxFolders {
					return api.NewError(api.CodeServerError, "graph: more than %d folders", maxFolders)
				}
				name := cleanName(f.DisplayName)
				path := name
				if parentPath != "" {
					path = parentPath + "/" + name
				}
				paths[f.ID] = path
				sf := store.Folder{
					Mailbox: f.ID, Delimiter: "/", Name: name, Path: path,
					Role: roles[f.ID], Subscribed: true, Selectable: true,
					ServerMessages: f.TotalItemCount, ServerUnseen: f.UnreadItemCount,
				}
				if _, known := paths[f.ParentFolderID]; known {
					sf.ParentMailbox = f.ParentFolderID
				}
				out = append(out, sf)
				if f.ChildFolderCount > 0 {
					if err := walk(path, "me/mailFolders/"+url.PathEscape(f.ID)+"/childFolders?$top=250&$select="+folderSelect); err != nil {
						return err
					}
				}
			}
			next = pg.NextLink
		}
		return nil
	}
	if err := walk("", "me/mailFolders?$top=250&$select="+folderSelect); err != nil {
		return nil, err
	}
	// After the walk: ParentMailbox depends on traversal order, the batch
	// order does not (UpsertFolders resolves parents across the batch).
	store.SortSyncOrder(out)
	return out, nil
}

// cleanName makes a display name safe for the store and the UI: no
// control characters, no path separators, never empty.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if s == "" {
		return "?"
	}
	return s
}
