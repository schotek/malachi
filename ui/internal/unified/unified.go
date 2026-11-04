// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package unified is the "All Inboxes" item of the sidebar in all three
// clients: the inboxes of every enabled mail account in one message list
// (docs/plans/unified-inbox.md, decisions U1–U4; docs/api.md §4.3 and §4.4,
// "Unified listing"). It says when the item is shown (Shown), which
// folders it covers (Inboxes, Covers, CoversAny) and its badge (Unread,
// Total), builds the listing's parameters (MessageParams, ThreadParams),
// names its sidebar key (Key) and splits an action on rows of several
// accounts into one call per account (GroupByAccount). Its texts are in
// text.go. Pure logic, no widgets; ported 1:1 to macOS
// (MalachiCore/Model/Unified.swift) and Windows
// (Malachi.Core/Model/Unified.cs).
//
// The scope is the daemon's (core/unified.go): folders of role inbox
// that are synced and not virtual, of the enabled accounts of kind imap
// or graph, never jira, caldav or eway. The client mirrors it only to
// know which notifications and counts belong to the list; the rows come
// from the daemon.
package unified

import "github.com/schotek/malachi/backend/pkg/api"

// Role is the folder role the item lists (api.UnifiedRoles); the API
// takes it as `unified`.
const Role = api.RoleInbox

// MinAccounts is how many mail accounts the item needs to be shown: with
// one, its inbox is the same list.
const MinAccounts = 2

// KeyFolder is the folder of the item's sidebar key; its account is "".
// A real folder id never starts with "@" and no real key has an empty
// account, so the key cannot collide. It is never stored in
// favourite-folders or collapsed-folders and cannot be pinned.
const KeyFolder api.FolderID = "@unified:inbox"

// FolderKey is the sidebar's (account, folder) pair as every client has
// it (GTK folderKey, MalachiCore FolderKey, Malachi.Core FolderKey).
type FolderKey struct {
	Account api.AccountID
	Folder  api.FolderID
}

// Key is the sidebar key of the All Inboxes item.
var Key = FolderKey{Account: "", Folder: KeyFolder}

// IsUnified says whether k is the All Inboxes item's key.
func (k FolderKey) IsUnified() bool { return IsKey(k.Account, k.Folder) }

// IsKey is IsUnified over the pair, for a client whose key type is its own.
func IsKey(account api.AccountID, folder api.FolderID) bool {
	return account == "" && folder == KeyFolder
}

// IsMailAccount: an account the item covers when enabled, of kind imap
// or graph (an empty kind is imap).
func IsMailAccount(a api.Account) bool {
	switch a.Config.Protocol() {
	case api.AccountIMAP, api.AccountGraph:
		return true
	}
	return false
}

// Accounts is the ids of the accounts the item covers, the enabled mail
// accounts, in the order given (account.list's, which is the sidebar's).
func Accounts(accounts []api.Account) []api.AccountID {
	var out []api.AccountID
	for _, a := range accounts {
		if a.Enabled && IsMailAccount(a) {
			out = append(out, a.ID)
		}
	}
	return out
}

// Shown says whether the sidebar has the item: at least MinAccounts
// accounts it covers and the preference unified-inbox (setting) on.
func Shown(accounts []api.Account, setting bool) bool {
	return setting && len(Accounts(accounts)) >= MinAccounts
}

// IsInbox says whether f is a folder the item lists: role inbox, synced
// and not a view of an issue tracker.
func IsInbox(f api.Folder) bool {
	return f.Role == Role && f.Synced && f.Virtual == ""
}

// Inboxes is the folders the item lists: those IsInbox of each account
// Accounts covers, in the accounts' order and then the order of folders
// (folder.list's of each account, keyed by account).
func Inboxes(accounts []api.Account, folders map[api.AccountID][]api.Folder) []api.Folder {
	var out []api.Folder
	for _, id := range Accounts(accounts) {
		for _, f := range folders[id] {
			if IsInbox(f) {
				out = append(out, f)
			}
		}
	}
	return out
}

// Unread is the item's badge: the unread count of the inboxes (Inboxes).
func Unread(inboxes []api.Folder) int {
	n := 0
	for _, f := range inboxes {
		n += f.Unread
	}
	return n
}

// Total is the number of messages of the inboxes (Inboxes), for the
// list's subtitle ("%d unread of %d", the existing msgid).
func Total(inboxes []api.Folder) int {
	n := 0
	for _, f := range inboxes {
		n += f.Total
	}
	return n
}

// Covers says whether a change of folderID of accountID (notify.newMessage,
// a folder's sync finishing, an undone move into it) belongs to the
// list shown: the list is the item (listIsUnified) and the folder is one
// of inboxes (Inboxes).
func Covers(listIsUnified bool, accountID api.AccountID, folderID api.FolderID, inboxes []api.Folder) bool {
	if !listIsUnified {
		return false
	}
	for _, f := range inboxes {
		if f.AccountID == accountID && f.ID == folderID {
			return true
		}
	}
	return false
}

// CoversAny is Covers for a notification naming several folders of one
// account (notify.messagesChanged): any of them is covered; no folder at
// all means any folder of the account, covered when the account has an
// inbox among inboxes.
func CoversAny(listIsUnified bool, accountID api.AccountID, folderIDs []api.FolderID, inboxes []api.Folder) bool {
	if !listIsUnified {
		return false
	}
	if len(folderIDs) == 0 {
		for _, f := range inboxes {
			if f.AccountID == accountID {
				return true
			}
		}
		return false
	}
	for _, id := range folderIDs {
		if Covers(true, accountID, id, inboxes) {
			return true
		}
	}
	return false
}

// MessageParams is message.list of the item: newest first, the default
// page size, the filter of the list and the cursor of the next page (""
// for the first). The cursor must be one a unified listing returned.
func MessageParams(filter api.MessageFilter, cursor string) api.MessageListParams {
	return api.MessageListParams{
		Unified: Role,
		Page:    api.Page{Limit: api.DefaultPageLimit, Cursor: cursor},
		Sort:    api.SortDateDesc,
		Filter:  filter,
	}
}

// ThreadParams is thread.list of the item, as MessageParams. A row's
// conversation is then read with thread.get of the row's account
// (ThreadSummary.AccountID) and folder (Latest.FolderID).
func ThreadParams(filter api.MessageFilter, cursor string) api.ThreadListParams {
	return api.ThreadListParams{
		Unified: Role,
		Page:    api.Page{Limit: api.DefaultPageLimit, Cursor: cursor},
		Sort:    api.SortDateDesc,
		Filter:  filter,
	}
}

// Group is the items of one account, in their order.
type Group[T any] struct {
	Account api.AccountID
	Items   []T
}

// GroupByAccount splits items (selected rows, message ids with their
// account) by account, the accounts in the order they first appear and
// the items of each in their order: an action on a selection of the
// item runs one call per group (every message method takes one account).
func GroupByAccount[T any](items []T, account func(T) api.AccountID) []Group[T] {
	var out []Group[T]
	index := map[api.AccountID]int{}
	for _, it := range items {
		a := account(it)
		i, ok := index[a]
		if !ok {
			i = len(out)
			index[a] = i
			out = append(out, Group[T]{Account: a})
		}
		out[i].Items = append(out[i].Items, it)
	}
	return out
}

// Mixed says whether items belong to more than one account (a selection
// of the item across accounts, decision U2).
func Mixed[T any](items []T, account func(T) api.AccountID) bool {
	for i := 1; i < len(items); i++ {
		if account(items[i]) != account(items[0]) {
			return true
		}
	}
	return false
}
