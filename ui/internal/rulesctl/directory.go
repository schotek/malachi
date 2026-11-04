// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package rulesctl

import (
	"slices"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/integrations"
	"github.com/schotek/malachi/ui/internal/rules"
)

// Directory is the mail accounts and their folders, which the rows of the
// Rules page name and the editor offers (Apply to, Move To, Try It): read
// from the daemon (Load: account.list, then folder.list of every mail
// account) or handed over by a window that has them already (Seed). Rules
// work on mail accounts only (imap, graph).
type Directory struct {
	Opts Options

	listeners map[int]func()
	nextID    int
	accounts  []api.Account
	folders   map[api.AccountID][]api.Folder
	loaded    bool
	gen       int
	closed    bool
}

// NewDirectory is an empty directory.
func NewDirectory(opts Options) *Directory {
	return &Directory{Opts: opts, folders: map[api.AccountID][]api.Folder{}}
}

// Stop ends the directory: answers still on their way are dropped.
func (d *Directory) Stop() { d.closed = true }

// Listen calls f whenever the directory changed (the controller and the
// editors that share it each listen); cancel stops it.
func (d *Directory) Listen(f func()) (cancel func()) {
	if d.listeners == nil {
		d.listeners = map[int]func(){}
	}
	d.nextID++
	id := d.nextID
	d.listeners[id] = f
	return func() { delete(d.listeners, id) }
}

func (d *Directory) changed() {
	if d.closed {
		return
	}
	ids := make([]int, 0, len(d.listeners))
	for id := range d.listeners {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if f, ok := d.listeners[id]; ok {
			f()
		}
	}
}

// Loaded reports that the accounts are known (the folders may still be on
// their way).
func (d *Directory) Loaded() bool { return d.loaded }

// Seed fills the directory from what the caller has: all accounts (the
// mail ones are kept) and their folders.
func (d *Directory) Seed(accounts []api.Account, folders map[api.AccountID][]api.Folder) {
	d.gen++
	d.setAccounts(accounts)
	d.folders = make(map[api.AccountID][]api.Folder, len(d.accounts))
	for _, a := range d.accounts {
		d.folders[a.ID] = d.sorted(a, folders[a.ID])
	}
	d.loaded = true
	d.changed()
}

func (d *Directory) setAccounts(all []api.Account) {
	d.accounts = d.accounts[:0:0]
	for _, a := range all {
		if integrations.ShowsInAccounts(a) {
			d.accounts = append(d.accounts, a)
		}
	}
}

func (d *Directory) sorted(a api.Account, f []api.Folder) []api.Folder {
	f = append([]api.Folder(nil), f...)
	if d.Opts.SortFolders != nil {
		return d.Opts.SortFolders(a, f)
	}
	return f
}

// Load reads the accounts and then the folders of every mail account.
func (d *Directory) Load() {
	if d.closed {
		return
	}
	d.gen++
	gen := d.gen
	call(d.Opts, api.MethodAccountList, api.AccountListParams{}, 0, func(r *api.AccountListResult, e *api.Error) {
		if d.closed || gen != d.gen || e != nil {
			return
		}
		d.setAccounts(r.Accounts)
		d.loaded = true
		d.changed()
		for _, a := range d.accounts {
			a := a
			call(d.Opts, api.MethodFolderList, api.FolderListParams{AccountID: a.ID}, 0, func(f *api.FolderListResult, e *api.Error) {
				if d.closed || gen != d.gen || e != nil {
					return
				}
				d.folders[a.ID] = d.sorted(a, f.Folders)
				d.changed()
			})
		}
	})
}

// Accounts are the mail accounts in the daemon's order.
func (d *Directory) Accounts() []api.Account { return d.accounts }

// Account is the mail account with the id.
func (d *Directory) Account(id api.AccountID) (api.Account, bool) {
	for _, a := range d.accounts {
		if a.ID == id {
			return a, true
		}
	}
	return api.Account{}, false
}

// Folders are the account's folders as the sidebar orders them.
func (d *Directory) Folders(id api.AccountID) []api.Folder { return d.folders[id] }

// MoveFolders are the folders a Move To action of a rule for the account
// offers (rules.MoveFolders).
func (d *Directory) MoveFolders(id api.AccountID) []api.Folder {
	return rules.MoveFolders(d.folders[id])
}

// AccountName is the name of an account, "" when it is not a mail account
// of the directory.
func (d *Directory) AccountName(id api.AccountID) string {
	if a, ok := d.Account(id); ok {
		return d.Opts.accountName(a)
	}
	return ""
}

// FolderName is the name of a folder, "" when it is gone.
func (d *Directory) FolderName(account api.AccountID, id api.FolderID) string {
	for _, f := range d.folders[account] {
		if f.ID == id {
			return d.Opts.folderName(f)
		}
	}
	return ""
}

// Names are the names the Rules page shows for ids.
func (d *Directory) Names() rules.Names {
	return rules.Names{Account: d.AccountName, Folder: d.FolderName}
}
