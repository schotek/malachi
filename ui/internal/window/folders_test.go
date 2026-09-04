// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The sidebar layout the window relies on: rebuildFolderList appends one
// row per entry and the row-selected handler maps row.Index() back onto
// model.entries, so ordering, depth and header placement must be exact.

func ids(entries []folderEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Header {
			out = append(out, "#"+string(e.Account.ID))
			continue
		}
		out = append(out, fmt.Sprintf("%s@%d", e.Folder.ID, e.Depth))
	}
	return out
}

func equalIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestSortFoldersHeadersOnlyForEnabled(t *testing.T) {
	accounts := []api.Account{
		{ID: "a", Enabled: true},
		{ID: "b", Enabled: false},
	}
	folders := map[api.AccountID][]api.Folder{
		"a": {{ID: "in", Path: "INBOX", Role: api.RoleInbox, Selectable: true}},
		"b": {{ID: "in-b", Path: "INBOX", Role: api.RoleInbox, Selectable: true}},
	}
	// One enabled account: no header, the disabled one is absent entirely.
	if got := ids(sortFolders(accounts, folders)); !equalIDs(got, []string{"in@0"}) {
		t.Errorf("single enabled: %v", got)
	}
	accounts[1].Enabled = true
	want := []string{"#a", "in@0", "#b", "in-b@0"}
	if got := ids(sortFolders(accounts, folders)); !equalIDs(got, want) {
		t.Errorf("two enabled: %v", got)
	}
}

func TestSortFoldersEmptyAccountKeepsHeader(t *testing.T) {
	// An account before its first sync lists no folders; its header still
	// appears so the user sees the account is there.
	accounts := []api.Account{{ID: "a", Enabled: true}, {ID: "b", Enabled: true}}
	folders := map[api.AccountID][]api.Folder{
		"b": {{ID: "in-b", Path: "INBOX", Role: api.RoleInbox, Selectable: true}},
	}
	want := []string{"#a", "#b", "in-b@0"}
	if got := ids(sortFolders(accounts, folders)); !equalIDs(got, want) {
		t.Errorf("got %v", got)
	}
	m := mailModel{accounts: accounts, folders: folders}
	m.rebuildEntries()
	if k, ok := m.initialFolder(); !ok || k != (folderKey{Account: "b", Folder: "in-b"}) {
		t.Errorf("initialFolder skipped the header: %+v %v", k, ok)
	}
}

func TestSortFoldersOrdering(t *testing.T) {
	accounts := []api.Account{{ID: "a", Enabled: true}}
	folders := map[api.AccountID][]api.Folder{"a": {
		{ID: "b", Path: "beta", Selectable: true},
		{ID: "A", Path: "Alpha", Selectable: true},
		{ID: "sent", Path: "Sent", Role: api.RoleSent, Selectable: true},
		{ID: "all", Path: "All Mail", Role: api.RoleAll, Selectable: true},
		{ID: "junk", Path: "Junk", Role: api.RoleJunk, Selectable: true},
		{ID: "in", Path: "zzz/INBOX", Role: api.RoleInbox, Selectable: true},
		{ID: "drafts", Path: "Drafts", Role: api.RoleDrafts, Selectable: true},
		{ID: "arch", Path: "Archive", Role: api.RoleArchive, Selectable: true},
		{ID: "trash", Path: "Trash", Role: api.RoleTrash, Selectable: true},
		// Total: an empty outbox is not listed (visibleFolders).
		{ID: "out", Path: "Outbox", Role: api.RoleOutbox, Selectable: true, Total: 1},
	}}
	// Roles in rank order regardless of path, then plain folders by
	// case-insensitive path.
	want := []string{"in@0", "drafts@0", "sent@0", "arch@0", "junk@0", "trash@0", "out@0", "all@0", "A@0", "b@0"}
	if got := ids(sortFolders(accounts, folders)); !equalIDs(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestSortFoldersChildrenFollowParent(t *testing.T) {
	accounts := []api.Account{{ID: "a", Enabled: true}}
	folders := map[api.AccountID][]api.Folder{"a": {
		// Children listed before their parent and out of order.
		{ID: "p-b", Path: "Projects/b", ParentID: "p", Selectable: true},
		{ID: "p-a-x", Path: "Projects/a/x", ParentID: "p-a", Selectable: true},
		{ID: "p-a", Path: "Projects/a", ParentID: "p", Selectable: true},
		{ID: "p", Path: "Projects", Selectable: false},
		{ID: "in", Path: "INBOX", Role: api.RoleInbox, Selectable: true},
		// A child with a role sorts before its plain siblings.
		{ID: "p-junk", Path: "Projects/zz", ParentID: "p", Role: api.RoleJunk, Selectable: true},
	}}
	want := []string{"in@0", "p@0", "p-junk@1", "p-a@1", "p-a-x@2", "p-b@1"}
	if got := ids(sortFolders(accounts, folders)); !equalIDs(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestSortFoldersDepthCap(t *testing.T) {
	// A chain deeper than maxFolderDepth: the part below the cap is listed
	// flat afterwards, indented by its path, and nothing is lost.
	const n = maxFolderDepth + 5
	list := make([]api.Folder, 0, n)
	path := ""
	for i := 0; i < n; i++ {
		if path != "" {
			path += "/"
		}
		path += fmt.Sprint("f", i)
		f := api.Folder{ID: api.FolderID(fmt.Sprint("f", i)), Path: path, Selectable: true}
		if i > 0 {
			f.ParentID = api.FolderID(fmt.Sprint("f", i-1))
		}
		list = append(list, f)
	}
	entries := sortFolders([]api.Account{{ID: "a", Enabled: true}}, map[api.AccountID][]api.Folder{"a": list})
	if len(entries) != n {
		t.Fatalf("got %d entries, want %d", len(entries), n)
	}
	seen := map[api.FolderID]bool{}
	for i, e := range entries {
		if seen[e.Folder.ID] {
			t.Errorf("entry %d duplicated", i)
		}
		seen[e.Folder.ID] = true
		if e.Depth < 0 || e.Depth > n {
			t.Errorf("entry %d depth %d out of range", i, e.Depth)
		}
	}
	if entries[0].Folder.ID != "f0" || entries[0].Depth != 0 || entries[maxFolderDepth].Depth != maxFolderDepth {
		t.Errorf("tree head wrong: %v", ids(entries[:3]))
	}
	if e := entries[maxFolderDepth+1]; e.Folder.ID != "f33" || e.Depth != 33 {
		t.Errorf("first orphan: %+v", e)
	}
}

func TestSortFoldersIgnoresFoldersOfUnknownAccounts(t *testing.T) {
	accounts := []api.Account{{ID: "a", Enabled: true}}
	folders := map[api.AccountID][]api.Folder{
		"a":     {{ID: "in", Path: "INBOX", Role: api.RoleInbox, Selectable: true}},
		"ghost": {{ID: "g", Path: "INBOX", Role: api.RoleInbox, Selectable: true}},
	}
	if got := ids(sortFolders(accounts, folders)); !equalIDs(got, []string{"in@0"}) {
		t.Errorf("got %v", got)
	}
}
