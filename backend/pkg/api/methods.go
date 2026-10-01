// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

// Method names (client → backend). Grouped by service, matching docs/api.md.
const (
	// System. system.hello and system.authenticate are the connection
	// handshake (docs/api.md §1.4): the transport (internal/rpc) answers
	// them, not Backend.
	MethodSystemInfo         = "system.info"
	MethodSystemHello        = "system.hello"
	MethodSystemAuthenticate = "system.authenticate"
	MethodSystemStorage      = "system.storage"

	// Accounts.
	MethodAccountList        = "account.list"
	MethodAccountAdd         = "account.add"
	MethodAccountRemove      = "account.remove"
	MethodAccountSetEnabled  = "account.setEnabled"
	MethodAccountUpdate      = "account.update"
	MethodAccountDiscover    = "account.discover"
	MethodAccountTest        = "account.test"
	MethodAccountLinked      = "account.linked"
	MethodAccountReorder     = "account.reorder"
	MethodAccountOAuthStart  = "account.oauthStart"
	MethodAccountOAuthWait   = "account.oauthWait"
	MethodAccountOAuthCancel = "account.oauthCancel"
	MethodAccountDetectSite  = "account.detectSite"
	MethodAccountListSpaces  = "account.listSpaces"

	// Folders.
	MethodFolderList      = "folder.list"
	MethodFolderSubscribe = "folder.subscribe"

	// Messages.
	MethodMessageList     = "message.list"
	MethodMessageGet      = "message.get"
	MethodMessageBody     = "message.body"
	MethodMessagePart     = "message.part"
	MethodMessageEmbedded = "message.embedded"
	MethodMessageDownload = "message.download"
	MethodMessageFlag     = "message.flag"
	MethodMessageMove     = "message.move"
	MethodMessageDelete   = "message.delete"
	MethodMessageSend     = "message.send"
	// MethodMessageUnsubscribe acts on the sender's unsubscribe offer.
	MethodMessageUnsubscribe = "message.unsubscribe"

	// Outbox.
	MethodOutboxRetry = "outbox.retry"

	// Threads.
	MethodThreadList = "thread.list"
	MethodThreadGet  = "thread.get"

	// Drafts.
	MethodDraftSave     = "draft.save"
	MethodDraftList     = "draft.list"
	MethodDraftDelete   = "draft.delete"
	MethodDraftCreate   = "draft.create"
	MethodDraftOpen     = "draft.open"
	MethodDraftMarkdown = "draft.markdown"

	// Attachments (compose-side store).
	MethodAttachmentImport = "attachment.import"
	MethodAttachmentRemove = "attachment.remove"
	MethodAttachmentGet    = "attachment.get"

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

	// Contacts (recipient completion).
	MethodContactSearch = "contact.search"

	// Issues (the issues of an issue-tracker account).
	MethodIssueTransitions = "issue.transitions"
	MethodIssueTransition  = "issue.transition"
)

// Notification names (backend → client, no reply expected).
const (
	NotifyNewMessage      = "notify.newMessage"
	NotifySyncState       = "notify.syncState"
	NotifyAuthRequired    = "notify.authRequired"
	NotifyAccountsChanged = "notify.accountsChanged"
	NotifyMessagesChanged = "notify.messagesChanged"
)

// AllMethods lists every callable method. The RPC server uses it to register
// stubs and tests use it to check docs/api.md coverage.
var AllMethods = []string{
	MethodSystemInfo, MethodSystemHello, MethodSystemAuthenticate, MethodSystemStorage,
	MethodAccountList, MethodAccountAdd, MethodAccountRemove, MethodAccountSetEnabled,
	MethodAccountUpdate, MethodAccountDiscover, MethodAccountTest, MethodAccountLinked,
	MethodAccountReorder, MethodAccountOAuthStart, MethodAccountOAuthWait, MethodAccountOAuthCancel,
	MethodAccountDetectSite, MethodAccountListSpaces,
	MethodFolderList, MethodFolderSubscribe,
	MethodMessageList, MethodMessageGet, MethodMessageBody, MethodMessagePart,
	MethodMessageEmbedded, MethodMessageDownload, MethodMessageFlag, MethodMessageMove, MethodMessageDelete,
	MethodMessageSend, MethodMessageUnsubscribe,
	MethodOutboxRetry,
	MethodThreadList, MethodThreadGet,
	MethodDraftSave, MethodDraftList, MethodDraftDelete, MethodDraftCreate, MethodDraftOpen,
	MethodDraftMarkdown,
	MethodAttachmentImport, MethodAttachmentRemove, MethodAttachmentGet,
	MethodSearchQuery,
	MethodSyncStatus, MethodSyncTrigger,
	MethodConfigGet, MethodConfigSet,
	MethodSenderList, MethodSenderAdd, MethodSenderRemove,
	MethodContactSearch,
	MethodIssueTransitions, MethodIssueTransition,
}

// AllNotifications lists every server-initiated notification.
var AllNotifications = []string{
	NotifyNewMessage, NotifySyncState, NotifyAuthRequired, NotifyAccountsChanged, NotifyMessagesChanged,
}
