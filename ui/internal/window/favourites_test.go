// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/settings"
)

// The Favourites section and the pin state behind it. Pure Go, like the
// folds: the section is entries, the state a set persisted in settings.

func pinned(keys ...folderKey) favouriteState {
	f := newFavouriteState()
	for _, k := range keys {
		f.set(k, true)
	}
	return f
}

func TestFavouriteSectionFirstInTreeOrder(t *testing.T) {
	accounts, folders := testAccounts()
	f := pinned(
		folderKey{Account: "acc2", Folder: "in2"},
		folderKey{Account: "acc1", Folder: "zeta"},
		folderKey{Account: "acc1", Folder: "deep"},
		folderKey{Account: "acc1", Folder: "inbox"},
	)
	entries := sortFolders(accounts, folders, newCollapseState(), f)

	// Accounts in list order, then role, then path: not the order pinned.
	section := []string{"#favourites", "*inbox@0", "*deep@0", "*zeta@0", "*in2@0"}
	plain := ids(sortFolders(accounts, folders, newCollapseState(), newFavouriteState()))
	if got := ids(entries); !equalIDs(got, append(section, plain...)) {
		t.Fatalf("entries = %v, want %v", got, append(section, plain...))
	}
	for _, e := range entries[1:5] {
		if e.Depth != 0 || e.HasChildren || e.Nested || e.Collapsed || !e.Starred || !e.Favourite {
			t.Errorf("section row %s = %+v: want depth 0, no children, starred", e.Folder.ID, e)
		}
		if e.Account.ID == "" {
			t.Errorf("section row %s carries no account", e.Folder.ID)
		}
	}
	if entries[0].Account.ID != "" || !entries[0].Header {
		t.Errorf("section heading = %+v", entries[0])
	}

	// The tree rows of pinned folders are starred, the others are not, and
	// none of them belongs to the section.
	starred := map[api.FolderID]bool{}
	for _, e := range entries[5:] {
		if e.Header {
			continue
		}
		if e.Favourite {
			t.Errorf("tree row %s marked as section row", e.Folder.ID)
		}
		starred[e.Folder.ID] = e.Starred
	}
	if !starred["inbox"] || !starred["deep"] || !starred["zeta"] || !starred["in2"] || starred["trash"] || starred["alpha"] {
		t.Errorf("starred tree rows = %v", starred)
	}
}

func TestFavouriteSectionGivesSingleAccountAHeader(t *testing.T) {
	accounts, folders := nestedAccount()
	f := pinned(folderKey{Account: "a", Folder: "bugs"})

	want := []string{"#favourites", "*bugs@0", "#a", "in@0", "work@0", "bugs@1", "old@2", "zulu@0"}
	if got := ids(sortFolders(accounts, folders, newCollapseState(), f)); !equalIDs(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}

	// The section row shows the folder's own count even while the tree row
	// is collapsed and rolls its children up.
	c := newCollapseState()
	c.setFolder(folderKey{Account: "a", Folder: "bugs"}, true)
	entries := sortFolders(accounts, folders, c, f)
	if entries[1].Badge != 4 || entries[1].Collapsed {
		t.Errorf("section row = %+v, want badge 4, not collapsed", entries[1])
	}
	if tree, _ := entryByID(entries[2:], "bugs"); tree.Badge != 12 || !tree.Collapsed {
		t.Errorf("tree row = %+v, want badge 12, collapsed", tree)
	}

	// With a heading the single account can be folded; the section stays.
	c.setAccount("a", true)
	want = []string{"#favourites", "*bugs@0", "#a"}
	if got := ids(sortFolders(accounts, folders, c, f)); !equalIDs(got, want) {
		t.Errorf("folded = %v, want %v", got, want)
	}
}

func TestFavouriteSectionSkipsWhatCannotShow(t *testing.T) {
	accounts, folders := testAccounts()
	folders["acc1"] = append(folders["acc1"],
		api.Folder{ID: "out", Path: "Outbox", Role: api.RoleOutbox, Selectable: true, Total: 0})
	f := pinned(
		folderKey{Account: "acc1", Folder: "container"}, // cannot be opened
		folderKey{Account: "acc3", Folder: "in3"},       // account disabled
		folderKey{Account: "acc1", Folder: "gone"},      // renamed on the server
		folderKey{Account: "acc1", Folder: "out"},       // empty outbox is hidden
	)
	plain := ids(sortFolders(accounts, folders, newCollapseState(), newFavouriteState()))
	if got := ids(sortFolders(accounts, folders, newCollapseState(), f)); !equalIDs(got, plain) {
		t.Errorf("entries = %v, want no section: %v", got, plain)
	}

	// An outbox with something in it is a folder like any other.
	folders["acc1"][len(folders["acc1"])-1].Total = 1
	got := ids(sortFolders(accounts, folders, newCollapseState(), f))
	if len(got) < 2 || got[0] != "#favourites" || got[1] != "*out@0" {
		t.Errorf("entries = %v, want the outbox pinned", got)
	}
}

func TestFavouriteBadgesFollowUnread(t *testing.T) {
	accounts, folders := nestedAccount()
	m := &mailModel{accounts: accounts, folders: folders, collapsed: newCollapseState(),
		favourites: pinned(folderKey{Account: "a", Folder: "zulu"})}
	m.rebuildEntries()
	m.adjustUnread(folderKey{Account: "a", Folder: "zulu"}, -6)

	rows := 0
	for _, e := range m.entries {
		if !e.Header && e.Folder.ID == "zulu" {
			rows++
			if e.Badge != 10 || e.Folder.Unread != 10 {
				t.Errorf("zulu row (favourite=%v) badge %d, unread %d, want 10", e.Favourite, e.Badge, e.Folder.Unread)
			}
		}
	}
	if rows != 2 {
		t.Errorf("zulu has %d rows, want the section's and the tree's", rows)
	}
}

func TestInitialFolderPrefersTheTree(t *testing.T) {
	accounts, folders := testAccounts()
	m := &mailModel{accounts: accounts, folders: folders, collapsed: newCollapseState(),
		favourites: pinned(folderKey{Account: "acc2", Folder: "in2"})}
	m.rebuildEntries()
	if k, ok := m.initialFolder(); !ok || k != (folderKey{Account: "acc1", Folder: "inbox"}) {
		t.Errorf("initialFolder = %+v %v, want the first account's Inbox", k, ok)
	}

	// Every account folded away: only the section is left to choose from.
	m.collapsed.setAccount("acc1", true)
	m.collapsed.setAccount("acc2", true)
	m.rebuildEntries()
	if k, ok := m.initialFolder(); !ok || k != (folderKey{Account: "acc2", Folder: "in2"}) {
		t.Errorf("initialFolder = %+v %v, want the pinned Inbox", k, ok)
	}
}

func TestFavouriteStateRoundTrip(t *testing.T) {
	s := settings.NewMemory()
	f := pinned(folderKey{Account: "acc_2", Folder: "f_2"}, folderKey{Account: "acc_1", Folder: "f_1"})
	f.save(s, []api.Account{{ID: "acc_1"}, {ID: "acc_2"}})

	// Stored sorted, so a no-op save is not a change for other windows.
	if got := s.FavouriteFolders(); len(got) != 2 || got[0] != "acc_1/f_1" || got[1] != "acc_2/f_2" {
		t.Fatalf("stored = %v", got)
	}
	got := loadFavourites(s)
	if len(got) != 2 || !got.has(folderKey{Account: "acc_1", Folder: "f_1"}) || !got.has(folderKey{Account: "acc_2", Folder: "f_2"}) {
		t.Errorf("loaded = %v", got)
	}

	// Unpinning removes the entry rather than storing a false; pinning twice
	// changes nothing.
	got.set(folderKey{Account: "acc_1", Folder: "f_1"}, false)
	got.set(folderKey{Account: "acc_2", Folder: "f_2"}, true)
	got.save(s, []api.Account{{ID: "acc_1"}, {ID: "acc_2"}})
	if stored := s.FavouriteFolders(); len(stored) != 1 || stored[0] != "acc_2/f_2" {
		t.Errorf("stored after unpin = %v", stored)
	}
}

func TestFavouriteStatePrunesRemovedAccounts(t *testing.T) {
	s := settings.NewMemory()
	f := pinned(folderKey{Account: "acc_gone", Folder: "f_1"}, folderKey{Account: "acc_1", Folder: "f_2"})

	f.save(s, []api.Account{{ID: "acc_1"}})
	if got := s.FavouriteFolders(); len(got) != 1 || got[0] != "acc_1/f_2" {
		t.Errorf("stored = %v, want only the surviving account's", got)
	}
	// An empty account list means "not loaded yet" and must prune nothing.
	f.save(s, nil)
	if got := s.FavouriteFolders(); len(got) != 2 {
		t.Errorf("stored = %v, want both kept when no accounts are known", got)
	}
}

func TestLoadFavouritesSkipsJunk(t *testing.T) {
	s := settings.NewMemory()
	s.SetFavouriteFolders([]string{"acc_1/f_1", "", "/", "acc", "acc_1/", "acc_1/f_1", "acc_2/f_2"})
	f := loadFavourites(s)
	if len(f) != 2 || !f.has(folderKey{Account: "acc_1", Folder: "f_1"}) || !f.has(folderKey{Account: "acc_2", Folder: "f_2"}) {
		t.Errorf("loaded = %v, want the two well-formed entries once each", f)
	}

	// A model without a state (the zero value) reads as nothing pinned.
	var none favouriteState
	if none.has(folderKey{Account: "acc_1", Folder: "f_1"}) {
		t.Error("nil state reports a pin")
	}
}
