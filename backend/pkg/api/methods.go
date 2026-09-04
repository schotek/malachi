// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

// Method names (client → backend). Grouped by service, matching docs/api.md.
const (
	// System.
	MethodSystemInfo = "system.info"

	// Accounts.
	MethodAccountList       = "account.list"
	MethodAccountAdd        = "account.add"
	MethodAccountRemove     = "account.remove"
	MethodAccountSetEnabled = "account.setEnabled"
	MethodAccountUpdate     = "account.update"
	MethodAccountDiscover   = "account.discover"
	MethodAccountTest       = "account.test"

	// Folders.
	MethodFolderList      = "folder.list"
	MethodFolderSubscribe = "folder.subscribe"

	// Messages.
	MethodMessageList   = "message.list"
	MethodMessageGet    = "message.get"
	MethodMessageBody   = "message.body"
	MethodMessageFlag   = "message.flag"
	MethodMessageMove   = "message.move"
	MethodMessageDelete = "message.delete"
	MethodMessageSend   = "message.send"

	// Threads.
	MethodThreadList = "thread.list"
	MethodThreadGet  = "thread.get"

	// Drafts.
	MethodDraftSave   = "draft.save"
	MethodDraftList   = "draft.list"
	MethodDraftDelete = "draft.delete"
	MethodDraftCreate = "draft.create"

	// Attachments (compose-side store).
	MethodAttachmentImport = "attachment.import"
	MethodAttachmentRemove = "attachment.remove"

	// Search.
	MethodSearchQuery = "search.query"

	// Sync.
	MethodSyncStatus  = "sync.status"
	MethodSyncTrigger = "sync.trigger"

	// Config (daemon-owned preferences).
	MethodConfigGet = "config.get"
	MethodConfigSet = "config.set"

	// Known senders (remote-content allow-list).
	MethodSenderList   = "sender.list"
	MethodSenderAdd    = "sender.add"
	MethodSenderRemove = "sender.remove"
)

// Notification names (backend → client, no reply expected).
const (
	NotifyNewMessage      = "notify.newMessage"
	NotifySyncState       = "notify.syncState"
	NotifyAuthRequired    = "notify.authRequired"
	NotifyAccountsChanged = "notify.accountsChanged"
)

// AllMethods lists every callable method. The RPC server uses it to register
// stubs and tests use it to check docs/api.md coverage.
var AllMethods = []string{
	MethodSystemInfo,
	MethodAccountList, MethodAccountAdd, MethodAccountRemove, MethodAccountSetEnabled,
	MethodAccountUpdate, MethodAccountDiscover, MethodAccountTest,
	MethodFolderList, MethodFolderSubscribe,
	MethodMessageList, MethodMessageGet, MethodMessageBody,
	MethodMessageFlag, MethodMessageMove, MethodMessageDelete, MethodMessageSend,
	MethodThreadList, MethodThreadGet,
	MethodDraftSave, MethodDraftList, MethodDraftDelete, MethodDraftCreate,
	MethodAttachmentImport, MethodAttachmentRemove,
	MethodSearchQuery,
	MethodSyncStatus, MethodSyncTrigger,
	MethodConfigGet, MethodConfigSet,
	MethodSenderList, MethodSenderAdd, MethodSenderRemove,
}

// AllNotifications lists every server-initiated notification.
var AllNotifications = []string{
	NotifyNewMessage, NotifySyncState, NotifyAuthRequired, NotifyAccountsChanged,
}
