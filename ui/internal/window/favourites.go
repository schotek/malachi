// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"sort"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
)

// Which folders the user pinned to the Favourites section at the top of the
// sidebar. Like the folds (collapse.go) this is pure presentation — the
// daemon knows nothing of it and no other client cares — so it lives in the
// UI's own GSettings, encoded with the same account/folder keys, and
// everything but the star button is plain Go for the package's tests.

// favouriteState is the set of pinned folders. The section lists them in
// tree order (sortFolders), so the set carries no order of its own.
type favouriteState map[folderKey]bool

// newFavouriteState returns an empty set.
func newFavouriteState() favouriteState { return make(favouriteState) }

// has reports whether k is pinned. Reading a nil state is fine.
func (f favouriteState) has(k folderKey) bool { return f[k] }

// set pins k or unpins it.
func (f favouriteState) set(k folderKey, on bool) {
	if on {
		f[k] = true
		return
	}
	delete(f, k)
}

// loadFavourites reads the stored set. Entries that do not parse are
// dropped: the list is data from disk, possibly written by another version.
func loadFavourites(s *settings.Store) favouriteState {
	f := newFavouriteState()
	for _, e := range s.FavouriteFolders() {
		if k, ok := decodeFolderKey(e); ok {
			f[k] = true
		}
	}
	return f
}

// save writes the set back, sorted so a no-op save does not look like a
// change to other windows, and without the entries of accounts that are
// gone. An empty account list means "not loaded yet", not "no accounts", and
// prunes nothing (as in collapseState.save). A folder that no longer resolves
// is kept: folder.list may have failed, the account may be switched off, and
// the pin is worth more than the stale entry costs.
func (f favouriteState) save(s *settings.Store, accounts []api.Account) {
	known := make(map[api.AccountID]bool, len(accounts))
	for _, a := range accounts {
		known[a.ID] = true
	}
	prune := len(accounts) > 0

	out := make([]string, 0, len(f))
	for k := range f {
		if prune && !known[k.Account] {
			continue
		}
		out = append(out, encodeFolderKey(k))
	}
	sort.Strings(out)
	s.SetFavouriteFolders(out)
}

// toggleFavourite pins a folder or unpins it, persists the change and
// redraws the sidebar at once. The selection and the message list are left
// alone: pinning is a way of arranging the sidebar, not of navigating.
func (w *Window) toggleFavourite(k folderKey) {
	w.model.favourites.set(k, !w.model.favourites.has(k))
	w.saveFavourites()
	w.rebuildFolderList()
}

// saveFavourites writes the set out without reacting to the change
// notification it causes in this window.
func (w *Window) saveFavourites() {
	w.savingFavourites = true
	w.model.favourites.save(w.settings, w.model.accounts)
	w.savingFavourites = false
}

// onFavouritesChanged re-reads the set after another window (or another
// process sharing the profile) pinned or unpinned something.
func (w *Window) onFavouritesChanged() {
	if w.savingFavourites {
		return
	}
	w.model.favourites = loadFavourites(w.settings)
	w.rebuildFolderList()
}

// newStar builds the pin button at the right end of a folder row: an
// outlined star, or a filled one once the folder is pinned. CSS keeps it out
// of sight until the pointer or the keyboard focus is on the row, the one
// exception being a filled star in the tree, which is what says the folder
// is pinned (.folder-star in internal/style). Like the fold arrow it takes
// the click itself, so pressing it does not select the row.
func newStar(starred bool) *gtk.Button {
	name, tip := "non-starred-symbolic", i18n.T("Add to Favourites")
	if starred {
		name, tip = "starred-symbolic", i18n.T("Remove from Favourites")
	}
	b := gtk.NewButtonFromIconName(name)
	b.AddCSSClass("flat")
	b.AddCSSClass("folder-star")
	if starred {
		b.AddCSSClass("starred")
	}
	b.SetVAlign(gtk.AlignCenter)
	// The rows are rebuilt on every click; moving the focus first would be
	// wasted. Tab still reaches the button.
	b.SetFocusOnClick(false)
	b.SetTooltipText(tip)
	return b
}
