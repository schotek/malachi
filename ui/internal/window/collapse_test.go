// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/settings"
)

// Folding parts of the sidebar away: which rows survive, what the badges
// then say, and what is written to and read back from the settings store.

// nestedFolders is one account whose tree is
//
//	INBOX
//	Work -> Work/Bugs -> Work/Bugs/Old
//	Zulu
//
// with unread counts that make a roll-up visible.
func nestedFolders() []api.Folder {
	return []api.Folder{
		{ID: "in", Path: "INBOX", Role: api.RoleInbox, Selectable: true, Unread: 1},
		{ID: "work", Path: "Work", Selectable: true, Unread: 2},
		{ID: "bugs", ParentID: "work", Path: "Work/Bugs", Selectable: true, Unread: 4},
		{ID: "old", ParentID: "bugs", Path: "Work/Bugs/Old", Selectable: true, Unread: 8},
		{ID: "zulu", Path: "Zulu", Selectable: true, Unread: 16},
	}
}

func nestedAccount() ([]api.Account, map[api.AccountID][]api.Folder) {
	return []api.Account{{ID: "a", Enabled: true}},
		map[api.AccountID][]api.Folder{"a": nestedFolders()}
}

// entryByID finds a row by folder ID.
func entryByID(entries []folderEntry, id api.FolderID) (folderEntry, bool) {
	for _, e := range entries {
		if !e.Header && e.Folder.ID == id {
			return e, true
		}
	}
	return folderEntry{}, false
}

func TestFolderTreeExpandedByDefault(t *testing.T) {
	accounts, folders := nestedAccount()
	entries := sortFolders(accounts, folders, newCollapseState())

	want := []string{"in@0", "work@0", "bugs@1", "old@2", "zulu@0"}
	if got := ids(entries); !equalIDs(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	work, _ := entryByID(entries, "work")
	if !work.HasChildren || work.Collapsed {
		t.Errorf("Work: HasChildren=%v Collapsed=%v, want true/false", work.HasChildren, work.Collapsed)
	}
	if work.Badge != 2 {
		t.Errorf("expanded Work badge = %d, want its own 2", work.Badge)
	}
	// Every row of a nested account reserves the arrow column so the titles
	// line up, including the leaves.
	for _, e := range entries {
		if !e.Nested {
			t.Errorf("%s: Nested = false, want true in a nested account", e.Folder.ID)
		}
	}
	zulu, _ := entryByID(entries, "zulu")
	if zulu.HasChildren {
		t.Error("Zulu has no children but claims to")
	}
}

func TestFolderTreeCollapsedHidesWholeSubtree(t *testing.T) {
	accounts, folders := nestedAccount()
	c := newCollapseState()
	c.setFolder(folderKey{Account: "a", Folder: "work"}, true)
	entries := sortFolders(accounts, folders, c)

	// Both the child and the grandchild go, not just the child.
	want := []string{"in@0", "work@0", "zulu@0"}
	if got := ids(entries); !equalIDs(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	work, _ := entryByID(entries, "work")
	if !work.Collapsed || !work.HasChildren {
		t.Errorf("Work: Collapsed=%v HasChildren=%v, want true/true", work.Collapsed, work.HasChildren)
	}
	// 2 of its own plus 4 and 8 from the two hidden descendants.
	if work.Badge != 14 {
		t.Errorf("collapsed Work badge = %d, want 14", work.Badge)
	}
	// Siblings are untouched.
	if in, _ := entryByID(entries, "in"); in.Badge != 1 {
		t.Errorf("Inbox badge = %d, want 1", in.Badge)
	}
}

func TestFolderTreeCollapsedInnerNode(t *testing.T) {
	accounts, folders := nestedAccount()
	c := newCollapseState()
	c.setFolder(folderKey{Account: "a", Folder: "bugs"}, true)
	entries := sortFolders(accounts, folders, c)

	want := []string{"in@0", "work@0", "bugs@1", "zulu@0"}
	if got := ids(entries); !equalIDs(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	bugs, _ := entryByID(entries, "bugs")
	if bugs.Badge != 12 {
		t.Errorf("collapsed Work/Bugs badge = %d, want 4+8", bugs.Badge)
	}
	// The expanded ancestor keeps counting only itself: its child is visible
	// and carries the rest.
	if work, _ := entryByID(entries, "work"); work.Badge != 2 {
		t.Errorf("expanded Work badge = %d, want 2", work.Badge)
	}
}

func TestFolderTreeCollapsingALeafDoesNothing(t *testing.T) {
	accounts, folders := nestedAccount()
	c := newCollapseState()
	// A stale entry for a folder that has no children any more must not
	// remove it from the list or change its badge.
	c.setFolder(folderKey{Account: "a", Folder: "zulu"}, true)
	entries := sortFolders(accounts, folders, c)

	want := []string{"in@0", "work@0", "bugs@1", "old@2", "zulu@0"}
	if got := ids(entries); !equalIDs(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	zulu, _ := entryByID(entries, "zulu")
	if zulu.Collapsed || zulu.Badge != 16 {
		t.Errorf("Zulu: Collapsed=%v Badge=%d, want false/16", zulu.Collapsed, zulu.Badge)
	}
}

func TestSortFoldersCollapsedAccount(t *testing.T) {
	accounts := []api.Account{{ID: "a", Enabled: true}, {ID: "b", Enabled: true}}
	folders := map[api.AccountID][]api.Folder{
		"a": nestedFolders(),
		"b": {{ID: "in-b", Path: "INBOX", Role: api.RoleInbox, Selectable: true}},
	}
	c := newCollapseState()
	c.setAccount("a", true)
	entries := sortFolders(accounts, folders, c)

	// The header stays, its whole tree goes, the other account is untouched.
	want := []string{"#a", "#b", "in-b@0"}
	if got := ids(entries); !equalIDs(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if !entries[0].Collapsed || !entries[0].HasChildren {
		t.Error("collapsed account header must be foldable and folded")
	}
	if entries[1].Collapsed {
		t.Error("the other account must stay expanded")
	}
}

func TestSortFoldersSingleAccountIgnoresAccountFold(t *testing.T) {
	// With one account there is no header, so there is nothing to click and
	// a stored fold must not blank the sidebar.
	accounts, folders := nestedAccount()
	c := newCollapseState()
	c.setAccount("a", true)

	if got := len(sortFolders(accounts, folders, c)); got != 5 {
		t.Fatalf("entries = %d, want all 5 folders", got)
	}
}

func TestFolderTreeFlatAccountReservesNoArrow(t *testing.T) {
	accounts := []api.Account{{ID: "a", Enabled: true}}
	folders := map[api.AccountID][]api.Folder{"a": {
		{ID: "in", Path: "INBOX", Role: api.RoleInbox, Selectable: true},
		{ID: "zulu", Path: "Zulu", Selectable: true},
	}}
	for _, e := range sortFolders(accounts, folders, newCollapseState()) {
		if e.Nested || e.HasChildren {
			t.Errorf("%s: Nested=%v HasChildren=%v, want false in a flat account",
				e.Folder.ID, e.Nested, e.HasChildren)
		}
	}
}

func TestFolderTreeCollapsedParentCycleStaysFlat(t *testing.T) {
	// Two folders pointing at each other are unreachable from any root; the
	// orphan sweep lists them flat and folding does not apply.
	accounts := []api.Account{{ID: "a", Enabled: true}}
	folders := map[api.AccountID][]api.Folder{"a": {
		{ID: "x", ParentID: "y", Path: "X/One", Selectable: true, Unread: 3},
		{ID: "y", ParentID: "x", Path: "X/Two", Selectable: true},
	}}
	c := newCollapseState()
	c.setFolder(folderKey{Account: "a", Folder: "x"}, true)
	entries := sortFolders(accounts, folders, c)

	if len(entries) != 2 {
		t.Fatalf("entries = %v, want both folders listed", ids(entries))
	}
	for _, e := range entries {
		if e.Collapsed {
			t.Errorf("%s: an unreachable folder must not be folded", e.Folder.ID)
		}
	}
	if x, _ := entryByID(entries, "x"); x.Badge != 3 {
		t.Errorf("X/One badge = %d, want its own 3", x.Badge)
	}
}

func TestRefreshBadgesFollowsUnread(t *testing.T) {
	accounts, folders := nestedAccount()
	m := &mailModel{accounts: accounts, folders: folders, collapsed: newCollapseState()}
	m.collapsed.setFolder(folderKey{Account: "a", Folder: "work"}, true)
	m.rebuildEntries()

	// A new message lands in the hidden grandchild: the visible ancestor's
	// badge has to move even though the folder itself has no row.
	m.adjustUnread(folderKey{Account: "a", Folder: "old"}, 1)
	work, _ := entryByID(m.entries, "work")
	if work.Badge != 15 {
		t.Errorf("Work badge = %d, want 15 after the hidden grandchild gained one", work.Badge)
	}
}

func TestCollapseStateRoundTrip(t *testing.T) {
	s := settings.NewMemory()
	c := newCollapseState()
	c.setFolder(folderKey{Account: "acc_1", Folder: "f_1"}, true)
	c.setFolder(folderKey{Account: "acc_2", Folder: "f_2"}, true)
	c.setAccount("acc_2", true)
	c.save(s, []api.Account{{ID: "acc_1"}, {ID: "acc_2"}})

	got := loadCollapse(s)
	if !got.folderCollapsed(folderKey{Account: "acc_1", Folder: "f_1"}) ||
		!got.folderCollapsed(folderKey{Account: "acc_2", Folder: "f_2"}) {
		t.Errorf("folders did not survive the round trip: %v", got.folders)
	}
	if !got.accountCollapsed("acc_2") || got.accountCollapsed("acc_1") {
		t.Errorf("accounts = %v, want only acc_2", got.accounts)
	}

	// Unfolding removes the entry rather than storing a false.
	got.setFolder(folderKey{Account: "acc_1", Folder: "f_1"}, false)
	got.save(s, []api.Account{{ID: "acc_1"}, {ID: "acc_2"}})
	if len(s.CollapsedFolders()) != 1 {
		t.Errorf("stored folders = %v, want one left", s.CollapsedFolders())
	}
}

func TestCollapseStatePrunesRemovedAccounts(t *testing.T) {
	s := settings.NewMemory()
	c := newCollapseState()
	c.setFolder(folderKey{Account: "acc_gone", Folder: "f_1"}, true)
	c.setFolder(folderKey{Account: "acc_1", Folder: "f_2"}, true)
	c.setAccount("acc_gone", true)

	c.save(s, []api.Account{{ID: "acc_1"}})
	if got := s.CollapsedFolders(); len(got) != 1 || got[0] != "acc_1/f_2" {
		t.Errorf("stored folders = %v, want only the surviving account's", got)
	}
	if got := s.CollapsedAccounts(); len(got) != 0 {
		t.Errorf("stored accounts = %v, want empty", got)
	}

	// An empty account list means "not loaded yet" and must prune nothing.
	c.save(s, nil)
	if got := s.CollapsedFolders(); len(got) != 2 {
		t.Errorf("stored folders = %v, want both kept when no accounts are known", got)
	}
}

func TestCollapseStateStoredSorted(t *testing.T) {
	// Map order is random; a stable stored value keeps a no-op save from
	// looking like a change to the other windows listening for it.
	s := settings.NewMemory()
	c := newCollapseState()
	for _, id := range []api.FolderID{"f_3", "f_1", "f_2"} {
		c.setFolder(folderKey{Account: "acc_1", Folder: id}, true)
	}
	c.save(s, []api.Account{{ID: "acc_1"}})

	want := []string{"acc_1/f_1", "acc_1/f_2", "acc_1/f_3"}
	got := s.CollapsedFolders()
	if len(got) != len(want) {
		t.Fatalf("stored = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stored = %v, want %v", got, want)
		}
	}
}

func TestDecodeFolderKeyRejectsJunk(t *testing.T) {
	for _, in := range []string{"", "/", "acc_1", "acc_1/", "/f_1"} {
		if _, ok := decodeFolderKey(in); ok {
			t.Errorf("decodeFolderKey(%q) accepted a malformed entry", in)
		}
	}
	k, ok := decodeFolderKey("acc_1/f_1")
	if !ok || k.Account != "acc_1" || k.Folder != "f_1" {
		t.Errorf("decodeFolderKey = %+v, %v", k, ok)
	}
}

func TestLoadCollapseSkipsJunk(t *testing.T) {
	s := settings.NewMemory()
	s.SetCollapsedFolders([]string{"acc_1/f_1", "nonsense", "", "acc_2/f_2"})
	s.SetCollapsedAccounts([]string{"acc_3", "  "})

	c := loadCollapse(s)
	if len(c.folders) != 2 {
		t.Errorf("folders = %v, want the two well-formed entries", c.folders)
	}
	if len(c.accounts) != 1 || !c.accountCollapsed("acc_3") {
		t.Errorf("accounts = %v, want only acc_3", c.accounts)
	}
}

func TestFolderListedIgnoresFolds(t *testing.T) {
	accounts, folders := nestedAccount()
	m := &mailModel{accounts: accounts, folders: folders, collapsed: newCollapseState()}
	m.collapsed.setFolder(folderKey{Account: "a", Folder: "work"}, true)
	m.rebuildEntries()

	// Folded out of sight is still listed, so the selection stays put.
	if !m.folderListed(folderKey{Account: "a", Folder: "old"}) {
		t.Error("a folder hidden by a fold must still count as listed")
	}
	if m.folderListed(folderKey{Account: "a", Folder: "nope"}) {
		t.Error("an unknown folder must not count as listed")
	}
	if m.folderListed(folderKey{}) {
		t.Error("the zero key must not count as listed")
	}

	// An empty outbox is hidden outright, and a selection there must move.
	m.folders["a"] = append(m.folders["a"],
		api.Folder{ID: "out", Path: "Outbox", Role: api.RoleOutbox, Selectable: true, Total: 0})
	if m.folderListed(folderKey{Account: "a", Folder: "out"}) {
		t.Error("an empty outbox is not listed")
	}

	m.accounts[0].Enabled = false
	if m.folderListed(folderKey{Account: "a", Folder: "in"}) {
		t.Error("a disabled account's folders are not listed")
	}
}
