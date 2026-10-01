// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// backend/pkg/api re-declared in Swift: the method table of methods.go with
// the params and result type of each, the notification names, the limits.
// The contract is docs/api.md; the numbers and names here must follow it.

import Foundation

/// One RPC method: its wire name, the type of its params object, the type of
/// its result, and how long the client waits for it by default.
public protocol RPCMethod: Sendable {
    associatedtype Params: Encodable & Sendable
    associatedtype Result: Decodable & Sendable
    static var name: String { get }
    static var timeout: Duration { get }
}

extension RPCMethod {
    public static var timeout: Duration { RPCTimeouts.default }
}

/// Decodes from `{}`, for methods whose result carries nothing.
public struct EmptyResult: Codable, Sendable, Equatable {
    public init() {}
}

public enum API {
    /// api.ProtocolVersion. A daemon whose system.hello answer carries
    /// another value is refused before the key is read
    /// (`RPCClient.HandshakeError.protocolMismatch`); system.info reports
    /// the same value.
    public static let protocolVersion = 2

    /// The name of `system.info`, for the string-based `RPCClient.call`.
    public static let systemInfo = "system.info"

    // MARK: System

    public enum SystemInfo: RPCMethod {
        public typealias Params = EmptyParams
        public typealias Result = SystemInfoResult
        public static let name = "system.info"
        public static let timeout = RPCTimeouts.systemInfo
    }

    /// The first line of every connection (docs/api.md §1.4). `RPCClient`
    /// sends it itself in `connect()`, never through `call`, which refuses
    /// until the handshake is done.
    public enum SystemHello: RPCMethod {
        public typealias Params = SystemHelloParams
        public typealias Result = SystemHelloResult
        public static let name = "system.hello"
        public static let timeout = RPCTimeouts.handshake
    }

    /// The second line of every connection (docs/api.md §1.4), sent by
    /// `RPCClient.connect()` as well; once it is answered the connection is
    /// usable.
    public enum SystemAuthenticate: RPCMethod {
        public typealias Params = SystemAuthenticateParams
        public typealias Result = EmptyResult
        public static let name = "system.authenticate"
        public static let timeout = RPCTimeouts.handshake
    }

    /// How much disk the mail store uses; cheap, the settings window asks
    /// every few seconds while it is open.
    public enum SystemStorage: RPCMethod {
        public typealias Params = EmptyParams
        public typealias Result = SystemStorageResult
        public static let name = "system.storage"
    }

    // MARK: Accounts

    public enum AccountList: RPCMethod {
        public typealias Params = EmptyParams
        public typealias Result = AccountListResult
        public static let name = "account.list"
    }

    public enum AccountAdd: RPCMethod {
        public typealias Params = AccountAddParams
        public typealias Result = AccountAddResult
        public static let name = "account.add"
        public static let timeout = RPCTimeouts.save
    }

    public enum AccountRemove: RPCMethod {
        public typealias Params = AccountRemoveParams
        public typealias Result = EmptyResult
        public static let name = "account.remove"
    }

    public enum AccountSetEnabled: RPCMethod {
        public typealias Params = AccountSetEnabledParams
        public typealias Result = EmptyResult
        public static let name = "account.setEnabled"
    }

    public enum AccountUpdate: RPCMethod {
        public typealias Params = AccountUpdateParams
        public typealias Result = EmptyResult
        public static let name = "account.update"
        public static let timeout = RPCTimeouts.save
    }

    public enum AccountDiscover: RPCMethod {
        public typealias Params = AccountDiscoverParams
        public typealias Result = AccountDiscoverResult
        public static let name = "account.discover"
        public static let timeout = RPCTimeouts.discover
    }

    public enum AccountTest: RPCMethod {
        public typealias Params = AccountTestParams
        public typealias Result = AccountTestResult
        public static let name = "account.test"
        public static let timeout = RPCTimeouts.test
    }

    public enum AccountLinked: RPCMethod {
        public typealias Params = EmptyParams
        public typealias Result = AccountLinkedResult
        public static let name = "account.linked"
    }

    public enum AccountReorder: RPCMethod {
        public typealias Params = AccountReorderParams
        public typealias Result = EmptyResult
        public static let name = "account.reorder"
    }

    public enum AccountOAuthStart: RPCMethod {
        public typealias Params = AccountOAuthStartParams
        public typealias Result = AccountOAuthStartResult
        public static let name = "account.oauthStart"
        public static let timeout = RPCTimeouts.oauthStart
    }

    /// Blocks up to 60 s in the daemon and answers `pending`; call again.
    public enum AccountOAuthWait: RPCMethod {
        public typealias Params = AccountOAuthWaitParams
        public typealias Result = AccountOAuthWaitResult
        public static let name = "account.oauthWait"
        public static let timeout = RPCTimeouts.oauthWaitCall
    }

    public enum AccountOAuthCancel: RPCMethod {
        public typealias Params = AccountOAuthCancelParams
        public typealias Result = EmptyResult
        public static let name = "account.oauthCancel"
    }

    /// What a Jira site address is (cloud or Data Center); nothing is
    /// stored or authenticated.
    public enum AccountDetectSite: RPCMethod {
        public typealias Params = AccountDetectSiteParams
        public typealias Result = AccountDetectSiteResult
        public static let name = "account.detectSite"
        public static let timeout = RPCTimeouts.detectSite
    }

    /// The spaces and statuses a Jira token sees; doubles as its sign-in
    /// test.
    public enum AccountListSpaces: RPCMethod {
        public typealias Params = AccountListSpacesParams
        public typealias Result = AccountListSpacesResult
        public static let name = "account.listSpaces"
        public static let timeout = RPCTimeouts.listSpaces
    }

    // MARK: Folders

    public enum FolderList: RPCMethod {
        public typealias Params = FolderListParams
        public typealias Result = FolderListResult
        public static let name = "folder.list"
    }

    /// The daemon answers notImplemented so far.
    public enum FolderSubscribe: RPCMethod {
        public typealias Params = FolderSubscribeParams
        public typealias Result = EmptyResult
        public static let name = "folder.subscribe"
    }

    // MARK: Messages

    public enum MessageList: RPCMethod {
        public typealias Params = MessageListParams
        public typealias Result = MessageListResult
        public static let name = "message.list"
    }

    public enum MessageGet: RPCMethod {
        public typealias Params = MessageGetParams
        public typealias Result = MessageGetResult
        public static let name = "message.get"
    }

    /// `remote` even without an override: a stored `allow` or a known
    /// sender lets the daemon fetch the images before it answers.
    public enum MessageBody: RPCMethod {
        public typealias Params = MessageBodyParams
        public typealias Result = MessageBodyResult
        public static let name = "message.body"
        public static let timeout = RPCTimeouts.remote
    }

    public enum MessagePart: RPCMethod {
        public typealias Params = MessagePartParams
        public typealias Result = MessagePartResult
        public static let name = "message.part"
        public static let timeout = RPCTimeouts.part
    }

    public enum MessageEmbedded: RPCMethod {
        public typealias Params = MessageEmbeddedParams
        public typealias Result = MessageEmbeddedResult
        public static let name = "message.embedded"
        public static let timeout = RPCTimeouts.remote
    }

    /// Fetches the parts of a message kept on the server (and a body not
    /// downloaded yet); the daemon's budget is 4 minutes, one download per
    /// message shared by every caller.
    public enum MessageDownload: RPCMethod {
        public typealias Params = MessageDownloadParams
        public typealias Result = MessageDownloadResult
        public static let name = "message.download"
        public static let timeout = RPCTimeouts.download
    }

    /// Acts on the sender's unsubscribe offer; the daemon may verify the
    /// message and call the sender's server, 30 s in all.
    public enum MessageUnsubscribe: RPCMethod {
        public typealias Params = MessageUnsubscribeParams
        public typealias Result = MessageUnsubscribeResult
        public static let name = "message.unsubscribe"
        public static let timeout = RPCTimeouts.unsubscribe
    }

    public enum MessageFlag: RPCMethod {
        public typealias Params = MessageFlagParams
        public typealias Result = EmptyResult
        public static let name = "message.flag"
    }

    public enum MessageMove: RPCMethod {
        public typealias Params = MessageMoveParams
        public typealias Result = EmptyResult
        public static let name = "message.move"
    }

    public enum MessageDelete: RPCMethod {
        public typealias Params = MessageDeleteParams
        public typealias Result = EmptyResult
        public static let name = "message.delete"
    }

    public enum MessageSend: RPCMethod {
        public typealias Params = MessageSendParams
        public typealias Result = MessageSendResult
        public static let name = "message.send"
    }

    // MARK: Outbox

    public enum OutboxRetry: RPCMethod {
        public typealias Params = OutboxRetryParams
        public typealias Result = EmptyResult
        public static let name = "outbox.retry"
    }

    // MARK: Threads

    public enum ThreadList: RPCMethod {
        public typealias Params = ThreadListParams
        public typealias Result = ThreadListResult
        public static let name = "thread.list"
    }

    public enum ThreadGet: RPCMethod {
        public typealias Params = ThreadGetParams
        public typealias Result = ThreadGetResult
        public static let name = "thread.get"
    }

    // MARK: Drafts

    public enum DraftSave: RPCMethod {
        public typealias Params = DraftSaveParams
        public typealias Result = DraftSaveResult
        public static let name = "draft.save"
    }

    public enum DraftList: RPCMethod {
        public typealias Params = DraftListParams
        public typealias Result = DraftListResult
        public static let name = "draft.list"
    }

    public enum DraftDelete: RPCMethod {
        public typealias Params = DraftDeleteParams
        public typealias Result = EmptyResult
        public static let name = "draft.delete"
    }

    public enum DraftCreate: RPCMethod {
        public typealias Params = DraftCreateParams
        public typealias Result = DraftCreateResult
        public static let name = "draft.create"
        public static let timeout = RPCTimeouts.compose
    }

    public enum DraftOpen: RPCMethod {
        public typealias Params = DraftOpenParams
        public typealias Result = DraftOpenResult
        public static let name = "draft.open"
        public static let timeout = RPCTimeouts.compose
    }

    /// Pasted plain text rendered as HTML when it reads as Markdown.
    public enum DraftMarkdown: RPCMethod {
        public typealias Params = DraftMarkdownParams
        public typealias Result = DraftMarkdownResult
        public static let name = "draft.markdown"
    }

    // MARK: Attachments

    public enum AttachmentImport: RPCMethod {
        public typealias Params = AttachmentImportParams
        public typealias Result = AttachmentImportResult
        public static let name = "attachment.import"
    }

    public enum AttachmentRemove: RPCMethod {
        public typealias Params = AttachmentRemoveParams
        public typealias Result = EmptyResult
        public static let name = "attachment.remove"
    }

    public enum AttachmentGet: RPCMethod {
        public typealias Params = AttachmentGetParams
        public typealias Result = AttachmentGetResult
        public static let name = "attachment.get"
        public static let timeout = RPCTimeouts.part
    }

    // MARK: Search

    public enum SearchQuery: RPCMethod {
        public typealias Params = SearchQueryParams
        public typealias Result = SearchQueryResult
        public static let name = "search.query"
    }

    // MARK: Sync

    public enum SyncStatus: RPCMethod {
        public typealias Params = SyncStatusParams
        public typealias Result = SyncStatusResult
        public static let name = "sync.status"
    }

    public enum SyncTrigger: RPCMethod {
        public typealias Params = SyncTriggerParams
        public typealias Result = EmptyResult
        public static let name = "sync.trigger"
    }

    // MARK: Config

    public enum ConfigGet: RPCMethod {
        public typealias Params = EmptyParams
        public typealias Result = ConfigGetResult
        public static let name = "config.get"
    }

    public enum ConfigSet: RPCMethod {
        public typealias Params = ConfigSetParams
        public typealias Result = ConfigSetResult
        public static let name = "config.set"
    }

    // MARK: Known senders

    public enum SenderList: RPCMethod {
        public typealias Params = EmptyParams
        public typealias Result = SenderListResult
        public static let name = "sender.list"
    }

    public enum SenderAdd: RPCMethod {
        public typealias Params = SenderAddParams
        public typealias Result = EmptyResult
        public static let name = "sender.add"
    }

    public enum SenderRemove: RPCMethod {
        public typealias Params = SenderRemoveParams
        public typealias Result = EmptyResult
        public static let name = "sender.remove"
    }

    // MARK: Contacts

    public enum ContactSearch: RPCMethod {
        public typealias Params = ContactSearchParams
        public typealias Result = ContactSearchResult
        public static let name = "contact.search"
    }

    // MARK: Issues

    /// The status changes the site allows on the issue of a message
    /// (an account with `Capability.transition`).
    public enum IssueTransitions: RPCMethod {
        public typealias Params = IssueTransitionsParams
        public typealias Result = IssueTransitionsResult
        public static let name = "issue.transitions"
        public static let timeout = RPCTimeouts.transitions
    }

    /// Performs one of them; the daemon refreshes the issue before it
    /// answers.
    public enum IssueTransition: RPCMethod {
        public typealias Params = IssueTransitionParams
        public typealias Result = IssueTransitionResult
        public static let name = "issue.transition"
        public static let timeout = RPCTimeouts.transition
    }

    // MARK: Tables

    /// Every method type, in the order of methods.go.
    public static let methods: [any RPCMethod.Type] = [
        SystemInfo.self, SystemHello.self, SystemAuthenticate.self, SystemStorage.self,
        AccountList.self, AccountAdd.self, AccountRemove.self, AccountSetEnabled.self,
        AccountUpdate.self, AccountDiscover.self, AccountTest.self, AccountLinked.self,
        AccountReorder.self, AccountOAuthStart.self, AccountOAuthWait.self, AccountOAuthCancel.self,
        AccountDetectSite.self, AccountListSpaces.self,
        FolderList.self, FolderSubscribe.self,
        MessageList.self, MessageGet.self, MessageBody.self, MessagePart.self,
        MessageEmbedded.self, MessageDownload.self, MessageFlag.self, MessageMove.self, MessageDelete.self,
        MessageSend.self, MessageUnsubscribe.self,
        OutboxRetry.self,
        ThreadList.self, ThreadGet.self,
        DraftSave.self, DraftList.self, DraftDelete.self, DraftCreate.self, DraftOpen.self,
        DraftMarkdown.self,
        AttachmentImport.self, AttachmentRemove.self, AttachmentGet.self,
        SearchQuery.self,
        SyncStatus.self, SyncTrigger.self,
        ConfigGet.self, ConfigSet.self,
        SenderList.self, SenderAdd.self, SenderRemove.self,
        ContactSearch.self,
        IssueTransitions.self, IssueTransition.self,
    ]

    /// api.AllMethods: every callable method name.
    public static let allMethods: [String] = methods.map { $0.name }

    /// The notification names (api.Notify*).
    public enum Notify {
        public static let newMessage = "notify.newMessage"
        public static let syncState = "notify.syncState"
        public static let authRequired = "notify.authRequired"
        public static let accountsChanged = "notify.accountsChanged"
        /// `MessagesChangedNotification`: the folders it names are listed
        /// again, and what was cached of their messages let go
        /// (`MailboxController.handleMessagesChanged`).
        public static let messagesChanged = "notify.messagesChanged"
    }

    /// api.AllNotifications: every server-initiated notification name.
    public static let allNotifications: [String] = [
        Notify.newMessage, Notify.syncState, Notify.authRequired, Notify.accountsChanged, Notify.messagesChanged,
    ]

    /// The limits the daemon enforces (types.go constants), for pre-checks.
    public enum Limits {
        public static let defaultPageLimit = 50
        public static let maxPageLimit = 500
        /// The built RFC 5322 message (`message.send`).
        public static let maxOutgoingMessageBytes = 36 << 20
        /// `textBody` and `htmlBody`, each.
        public static let maxDraftBodyBytes = 1 << 20
        public static let maxDraftSubjectBytes = 1024
        /// to + cc + bcc.
        public static let maxDraftRecipients = 500
        public static let maxDraftAttachments = 100
        /// One file (`attachment.import`).
        public static let maxAttachmentBytes = 25 << 20
        /// The sum over a draft (`draft.save`).
        public static let maxDraftAttachmentBytes = 25 << 20
        /// Inline base64 payloads (`message.part`, `attachment.get`, `attachment.import` data).
        public static let maxAttachmentDataBytes = 16 << 20
        public static let maxThreadMessages = 500
        public static let maxThreadParticipants = 8
        public static let maxDraftAttributionBytes = 2048
        public static let maxDraftAttributionLines = 16
        /// The smallest non-zero `Preferences.syncIntervalSeconds`.
        public static let syncIntervalMin = 60
        public static let offlineDaysMax = 3650
        /// `Preferences.attachmentOfflineDays`: the largest number of days,
        /// and the value that keeps no large attachment locally.
        public static let attachmentOfflineDaysMax = 3650
        public static let attachmentOfflineNone = -1
        /// The decoded size from which an attachment may be kept on the
        /// server only; smaller parts are always stored.
        public static let largeAttachmentMinBytes = 100 << 10
        /// `messageIds` in message.flag, message.move and message.delete.
        public static let maxMessageIDsPerCall = 1000
        public static let defaultContactLimit = 10
        public static let maxContactLimit = 50
        public static let maxContactQueryBytes = 256
        /// search.query: the query's length, its terms and filters, and the
        /// matches `page.total` counts exactly (-1 beyond).
        public static let maxSearchQueryBytes = 1024
        public static let maxSearchTerms = 32
        public static let maxSearchTotal = 1000
        /// `JiraConfig`: spaces, closed statuses, entries of each string
        /// list, and bytes of each entry (a metadata filter's pattern).
        public static let maxJiraSpaces = 200
        public static let maxJiraStatuses = 64
        public static let maxJiraListEntries = 32
        public static let maxJiraPatternBytes = 512
        /// `JiraConfig.offlineDays`: the largest value, and what 0 means.
        public static let maxJiraOfflineDays = 365
        public static let defaultJiraOfflineDays = 30
        /// `issue.transitions`: at most this many transitions.
        public static let maxIssueTransitions = 100
    }
}

extension RPCClient {
    /// Performs one typed call. `timeout` nil takes the method's own
    /// (`RPCMethod.timeout`). A daemon error arrives as `RPCError`, a lost
    /// connection as `ClientError.disconnected`, silence as `.timeout`.
    public func call<M: RPCMethod>(_ method: M.Type, _ params: M.Params, timeout: Duration? = nil) async throws -> M.Result {
        try await call(M.name, params, timeout: timeout ?? M.timeout)
    }
}
