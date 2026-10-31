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
	MethodFolderMarkRead  = "folder.markRead"
	MethodFolderEmpty     = "folder.empty"

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
	MethodDraftGet      = "draft.get"

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

	// Board (docs/api.md §4.13).
	MethodBoardList           = "board.list"
	MethodBoardGet            = "board.get"
	MethodBoardSetState       = "board.setState"
	MethodBoardSetDone        = "board.setDone"
	MethodBoardRemind         = "board.remind"
	MethodBoardArchive        = "board.archive"
	MethodBoardTrash          = "board.trash"
	MethodBoardUnflag         = "board.unflag"
	MethodBoardDiscardDraft   = "board.discardDraft"
	MethodBoardSetDraft       = "board.setDraft"
	MethodBoardQueue          = "board.queue"
	MethodBoardAnnotate       = "board.annotate"
	MethodBoardCommit         = "board.commit"
	MethodBoardSetCommitment  = "board.setCommitment"
	MethodBoardPreferences    = "board.preferences"
	MethodBoardSetPreferences = "board.setPreferences"
	MethodBoardRunStart       = "board.runStart"
	MethodBoardRunEnd         = "board.runEnd"

	// Calendar accounts (docs/api.md §4.1).
	MethodAccountDiscoverCalDAV      = "account.discoverCalDAV"
	MethodAccountListCalDAVCalendars = "account.listCalDAVCalendars"

	// Calendars (docs/api.md §4.14).
	MethodCalendarList           = "calendar.list"
	MethodCalendarUpdate         = "calendar.update"
	MethodCalendarSync           = "calendar.sync"
	MethodCalendarPreferences    = "calendar.preferences"
	MethodCalendarSetPreferences = "calendar.setPreferences"
	MethodCalendarFreeBusy       = "calendar.freeBusy"
	MethodCalendarFindFreeTime   = "calendar.findFreeTime"

	// Events (docs/api.md §4.15).
	MethodEventList         = "event.list"
	MethodEventGet          = "event.get"
	MethodEventSearch       = "event.search"
	MethodEventCreate       = "event.create"
	MethodEventUpdate       = "event.update"
	MethodEventDelete       = "event.delete"
	MethodEventRespond      = "event.respond"
	MethodEventSnoozeAlert  = "event.snoozeAlert"
	MethodEventDismissAlert = "event.dismissAlert"

	// Invitations (docs/api.md §4.16).
	MethodInvitationList        = "invitation.list"
	MethodInvitationRespond     = "invitation.respond"
	MethodInvitationApplyCancel = "invitation.applyCancel"

	// CRM accounts (docs/api.md §4.1).
	MethodAccountDetectEway = "account.detectEway"

	// CRM (docs/api.md §4.17).
	MethodCrmLookup = "crm.lookup"
	MethodCrmSearch = "crm.search"
	MethodCrmSave   = "crm.save"
	MethodCrmRemove = "crm.remove"

	// Assistant profile (docs/api.md §4.18).
	MethodAssistantProfileGet     = "assistant.profileGet"
	MethodAssistantProfileSet     = "assistant.profileSet"
	MethodAssistantProfilePropose = "assistant.profilePropose"
	MethodAssistantProfileResolve = "assistant.profileResolve"

	// Address books and contacts (docs/api.md §4.19).
	MethodAddressBookList = "addressBook.list"
	MethodAddressBookSet  = "addressBook.set"
	MethodContactList     = "contact.list"
	MethodContactGet      = "contact.get"
	MethodContactLookup   = "contact.lookup"
	MethodContactCreate   = "contact.create"
	MethodContactUpdate   = "contact.update"
	MethodContactDelete   = "contact.delete"
	MethodContactPhoto    = "contact.photo"
)

// Notification names (backend → client, no reply expected).
const (
	NotifyNewMessage      = "notify.newMessage"
	NotifySyncState       = "notify.syncState"
	NotifyAuthRequired    = "notify.authRequired"
	NotifyAccountsChanged = "notify.accountsChanged"
	NotifyMessagesChanged = "notify.messagesChanged"
	NotifyBoardChanged    = "notify.boardChanged"
	NotifyDraftsChanged   = "notify.draftsChanged"

	NotifyCalendarChanged    = "notify.calendarChanged"
	NotifyCalendarState      = "notify.calendarState"
	NotifyEventAlert         = "notify.eventAlert"
	NotifyInvitationsChanged = "notify.invitationsChanged"

	NotifyAssistantProfileChanged = "notify.assistantProfileChanged"

	NotifyContactsChanged = "notify.contactsChanged"
)

// AllMethods lists every callable method. The RPC server uses it to register
// stubs and tests use it to check docs/api.md coverage.
var AllMethods = []string{
	MethodSystemInfo, MethodSystemHello, MethodSystemAuthenticate, MethodSystemStorage,
	MethodAccountList, MethodAccountAdd, MethodAccountRemove, MethodAccountSetEnabled,
	MethodAccountUpdate, MethodAccountDiscover, MethodAccountTest, MethodAccountLinked,
	MethodAccountReorder, MethodAccountOAuthStart, MethodAccountOAuthWait, MethodAccountOAuthCancel,
	MethodAccountDetectSite, MethodAccountListSpaces,
	MethodFolderList, MethodFolderSubscribe, MethodFolderMarkRead, MethodFolderEmpty,
	MethodMessageList, MethodMessageGet, MethodMessageBody, MethodMessagePart,
	MethodMessageEmbedded, MethodMessageDownload, MethodMessageFlag, MethodMessageMove, MethodMessageDelete,
	MethodMessageSend, MethodMessageUnsubscribe,
	MethodOutboxRetry,
	MethodThreadList, MethodThreadGet,
	MethodDraftSave, MethodDraftList, MethodDraftDelete, MethodDraftCreate, MethodDraftOpen,
	MethodDraftMarkdown, MethodDraftGet,
	MethodAttachmentImport, MethodAttachmentRemove, MethodAttachmentGet,
	MethodSearchQuery,
	MethodSyncStatus, MethodSyncTrigger,
	MethodConfigGet, MethodConfigSet,
	MethodSenderList, MethodSenderAdd, MethodSenderRemove,
	MethodContactSearch,
	MethodIssueTransitions, MethodIssueTransition,
	MethodBoardList, MethodBoardGet, MethodBoardSetState, MethodBoardSetDone, MethodBoardRemind,
	MethodBoardArchive, MethodBoardTrash, MethodBoardUnflag, MethodBoardDiscardDraft, MethodBoardSetDraft, MethodBoardQueue, MethodBoardAnnotate,
	MethodBoardCommit, MethodBoardSetCommitment, MethodBoardPreferences, MethodBoardSetPreferences,
	MethodBoardRunStart, MethodBoardRunEnd,
	MethodAccountDiscoverCalDAV, MethodAccountListCalDAVCalendars,
	MethodCalendarList, MethodCalendarUpdate, MethodCalendarSync, MethodCalendarPreferences,
	MethodCalendarSetPreferences, MethodCalendarFreeBusy, MethodCalendarFindFreeTime,
	MethodEventList, MethodEventGet, MethodEventSearch, MethodEventCreate, MethodEventUpdate,
	MethodEventDelete, MethodEventRespond, MethodEventSnoozeAlert, MethodEventDismissAlert,
	MethodInvitationList, MethodInvitationRespond, MethodInvitationApplyCancel,
	MethodAccountDetectEway,
	MethodCrmLookup, MethodCrmSearch, MethodCrmSave, MethodCrmRemove,
	MethodAssistantProfileGet, MethodAssistantProfileSet, MethodAssistantProfilePropose, MethodAssistantProfileResolve,
	MethodAddressBookList, MethodAddressBookSet,
	MethodContactList, MethodContactGet, MethodContactLookup,
	MethodContactCreate, MethodContactUpdate, MethodContactDelete,
	MethodContactPhoto,
}

// AllNotifications lists every server-initiated notification.
var AllNotifications = []string{
	NotifyNewMessage, NotifySyncState, NotifyAuthRequired, NotifyAccountsChanged, NotifyMessagesChanged,
	NotifyBoardChanged, NotifyDraftsChanged,
	NotifyCalendarChanged, NotifyCalendarState, NotifyEventAlert, NotifyInvitationsChanged,
	NotifyAssistantProfileChanged,
	NotifyContactsChanged,
}
