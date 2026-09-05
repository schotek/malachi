// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"sort"
	"strings"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/settings"
)

// Which parts of the folder sidebar the user folded away. This is pure
// presentation: it changes nothing about the mail and no other client cares,
// so it lives in the UI's own GSettings rather than in the daemon (see
// docs/architecture.md on where preferences belong).
//
// Everything here is plain Go so the package's tests can exercise it without
// a display.

// collapseSep joins an account id and a folder id into one stored entry. The
// ids are generated tokens ("acc_" / "f_" plus 32 hex characters, see
// backend/internal/store/ids.go), so the separator cannot occur inside one.
const collapseSep = "/"

// collapseState is the set of collapsed nodes. A missing entry means
// expanded, which is what a fresh profile gets.
type collapseState struct {
	folders  map[folderKey]bool
	accounts map[api.AccountID]bool
}

// newCollapseState returns an empty state (everything expanded).
func newCollapseState() collapseState {
	return collapseState{
		folders:  make(map[folderKey]bool),
		accounts: make(map[api.AccountID]bool),
	}
}

// folderCollapsed reports whether the folder hides its children.
func (c collapseState) folderCollapsed(k folderKey) bool { return c.folders[k] }

// accountCollapsed reports whether the account hides its folders.
func (c collapseState) accountCollapsed(id api.AccountID) bool { return c.accounts[id] }

// setFolder folds k away or unfolds it.
func (c collapseState) setFolder(k folderKey, collapsed bool) {
	if collapsed {
		c.folders[k] = true
		return
	}
	delete(c.folders, k)
}

// setAccount folds an account's whole tree away or unfolds it.
func (c collapseState) setAccount(id api.AccountID, collapsed bool) {
	if collapsed {
		c.accounts[id] = true
		return
	}
	delete(c.accounts, id)
}

// loadCollapse reads the stored state. Entries that do not parse are
// dropped: the list is data from disk, possibly written by another version.
func loadCollapse(s *settings.Store) collapseState {
	c := newCollapseState()
	for _, e := range s.CollapsedFolders() {
		if k, ok := decodeFolderKey(e); ok {
			c.folders[k] = true
		}
	}
	for _, e := range s.CollapsedAccounts() {
		if id := strings.TrimSpace(e); id != "" {
			c.accounts[api.AccountID(id)] = true
		}
	}
	return c
}

// save writes the state back, dropping entries of accounts that are gone.
// Folders are kept even when their account currently has no folder list:
// folder.list may simply have failed or the account may be switched off, and
// losing the tree's shape over that would be worse than a stale entry.
func (c collapseState) save(s *settings.Store, accounts []api.Account) {
	known := make(map[api.AccountID]bool, len(accounts))
	for _, a := range accounts {
		known[a.ID] = true
	}
	// An empty account list means "not loaded yet", not "no accounts": never
	// prune against it.
	prune := len(accounts) > 0

	folders := make([]string, 0, len(c.folders))
	for k := range c.folders {
		if prune && !known[k.Account] {
			continue
		}
		folders = append(folders, encodeFolderKey(k))
	}
	accountIDs := make([]string, 0, len(c.accounts))
	for id := range c.accounts {
		if prune && !known[id] {
			continue
		}
		accountIDs = append(accountIDs, string(id))
	}
	// Map iteration is random; sort so the stored value is stable and a
	// no-op save does not look like a change to other windows.
	sort.Strings(folders)
	sort.Strings(accountIDs)

	s.SetCollapsedFolders(folders)
	s.SetCollapsedAccounts(accountIDs)
}

// toggleFolder folds a folder's children away, or brings them back, and
// persists the change. The selected folder and the message list are left
// alone on purpose: folding is a way of looking at the sidebar, not a way of
// navigating (rebuildFolderList keeps a hidden selection).
func (w *Window) toggleFolder(k folderKey) {
	w.model.collapsed.setFolder(k, !w.model.collapsed.folderCollapsed(k))
	w.saveCollapse()
	w.rebuildFolderList()
}

// toggleAccount folds a whole account's tree away, or brings it back.
func (w *Window) toggleAccount(id api.AccountID) {
	w.model.collapsed.setAccount(id, !w.model.collapsed.accountCollapsed(id))
	w.saveCollapse()
	w.rebuildFolderList()
}

// saveCollapse writes the state out without reacting to the change
// notification it causes in this window.
func (w *Window) saveCollapse() {
	w.savingCollapse = true
	w.model.collapsed.save(w.settings, w.model.accounts)
	w.savingCollapse = false
}

// onCollapseChanged re-reads the state after another window (or another
// process sharing the profile) folded something.
func (w *Window) onCollapseChanged() {
	if w.savingCollapse {
		return
	}
	w.model.collapsed = loadCollapse(w.settings)
	w.rebuildFolderList()
}

// addFolderShortcuts binds Left and Right on a focused folder row to folding
// and unfolding it, the convention GTK's own tree views follow. Both return
// false when there is nothing to do, so the key still reaches the list for
// its normal navigation.
func (w *Window) addFolderShortcuts(r *folderRow, k folderKey, e folderEntry) {
	if !e.HasChildren {
		return
	}
	sc := gtk.NewShortcutController()
	sc.SetScope(gtk.ShortcutScopeLocal)
	for _, s := range []struct {
		trigger  string
		collapse bool
	}{{"Left", true}, {"Right", false}} {
		collapse := s.collapse
		sc.AddShortcut(gtk.NewShortcut(
			gtk.NewShortcutTriggerParseString(s.trigger),
			gtk.NewCallbackAction(func(gtk.Widgetter, *glib.Variant) bool {
				if w.model.collapsed.folderCollapsed(k) == collapse {
					return false
				}
				w.toggleFolder(k)
				return true
			}),
		))
	}
	r.AddController(sc)
}

// encodeFolderKey renders a key as one stored entry.
func encodeFolderKey(k folderKey) string {
	return string(k.Account) + collapseSep + string(k.Folder)
}

// decodeFolderKey parses one stored entry. It reports false for anything
// that is not two non-empty halves.
func decodeFolderKey(e string) (folderKey, bool) {
	acc, folder, ok := strings.Cut(e, collapseSep)
	if !ok || acc == "" || folder == "" {
		return folderKey{}, false
	}
	return folderKey{Account: api.AccountID(acc), Folder: api.FolderID(folder)}, true
}
