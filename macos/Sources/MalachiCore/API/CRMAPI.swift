// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// CRM accounts and saving mail to a CRM (docs/api.md §4.1
// account.detectEway, §4.17 crm; backend/pkg/api/crm.go; the design is
// docs/eway.md). The only CRM is eWay-CRM (`AccountKind.eway`). Every name
// and detail of a CRM item is untrusted display text: show it with
// `stringValue` / plain text only. Every URL is built by the daemon from
// the account's stored service URL and an item GUID of fixed shape, never
// taken from the CRM.

import Foundation

// MARK: Account

/// api.EwayConfig: the `eway` block of an account of kind `eway` (its
/// password is `Credentials.password`).
public struct EwayConfig: Codable, Sendable, Equatable {
    /// Normalised by `account.detectEway`: https, no user info, query or
    /// fragment, no trailing `/API.svc` and no trailing slash.
    public var serviceUrl: String
    /// The eWay-CRM user name the password belongs to.
    public var username: String
    /// Set by the daemon after the first sign-in; clients leave it as they
    /// got it.
    public var userGuid: String?
    /// Later messages of a conversation the user saved are saved by the
    /// daemon too, with the same links (docs/eway.md D22). Always sent;
    /// the assistant sends true for a new account; absent reads false.
    public var followThreads: Bool

    public init(serviceUrl: String, username: String, userGuid: String? = nil, followThreads: Bool = false) {
        self.serviceUrl = serviceUrl
        self.username = username
        self.userGuid = userGuid
        self.followThreads = followThreads
    }

    private enum CodingKeys: String, CodingKey {
        case serviceUrl, username, userGuid, followThreads
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        serviceUrl = try c.decode(String.self, forKey: .serviceUrl)
        username = try c.decodeIfPresent(String.self, forKey: .username) ?? ""
        userGuid = try c.decodeIfPresent(String.self, forKey: .userGuid)
        followThreads = try c.decodeIfPresent(Bool.self, forKey: .followThreads) ?? false
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(serviceUrl, forKey: .serviceUrl)
        try c.encode(username, forKey: .username)
        if let userGuid, !userGuid.isEmpty {
            try c.encode(userGuid, forKey: .userGuid)
        }
        try c.encode(followThreads, forKey: .followThreads)
    }
}

// MARK: Items

/// api.CrmItemKind: what a saved message can be linked to (an open enum).
public struct CrmItemKind: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let contact: CrmItemKind = "contact"
    public static let company: CrmItemKind = "company"
    /// eWay-CRM's Leads.
    public static let deal: CrmItemKind = "deal"
    /// eWay-CRM's Projects.
    public static let project: CrmItemKind = "project"
}

/// api.CrmReason: why crm.lookup or crm.search offers an item.
public struct CrmReason: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let address: CrmReason = "address"
    public static let domain: CrmReason = "domain"
    public static let contact: CrmReason = "contact"
    public static let company: CrmReason = "company"
    public static let search: CrmReason = "search"
    public static let saved: CrmReason = "saved"
}

/// api.CrmItem: one item of the CRM.
public struct CrmItem: Codable, Sendable, Equatable {
    public var kind: CrmItemKind
    public var guid: String
    /// Untrusted display text.
    public var name: String
    /// Untrusted display text (a contact's e-mail, a deal's state).
    public var detail: String?
    public var reason: CrmReason?
    /// The saved message's superior item; in suggestions the one deal or
    /// project the dialog checks as superior in advance.
    @DefaultFalse public var superior: Bool
    /// Built by the daemon; opened without a question.
    public var url: String

    public init(
        kind: CrmItemKind, guid: String, name: String, detail: String? = nil, reason: CrmReason? = nil,
        superior: Bool = false, url: String = ""
    ) {
        self.kind = kind
        self.guid = guid
        self.name = name
        self.detail = detail
        self.reason = reason
        self.superior = superior
        self.url = url
    }

    private enum CodingKeys: String, CodingKey {
        case kind, guid, name, detail, reason, superior, url
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        kind = try c.decode(CrmItemKind.self, forKey: .kind)
        guid = try c.decode(String.self, forKey: .guid)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        detail = try c.decodeIfPresent(String.self, forKey: .detail)
        reason = try c.decodeIfPresent(CrmReason.self, forKey: .reason)
        _superior = try c.decode(DefaultFalse.self, forKey: .superior)
        url = try c.decodeIfPresent(String.self, forKey: .url) ?? ""
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(kind, forKey: .kind)
        try c.encode(guid, forKey: .guid)
        try c.encode(name, forKey: .name)
        if let detail, !detail.isEmpty {
            try c.encode(detail, forKey: .detail)
        }
        if let reason, !reason.rawValue.isEmpty {
            try c.encode(reason, forKey: .reason)
        }
        try c.encode(_superior, forKey: .superior)
        try c.encode(url, forKey: .url)
    }
}

/// api.CrmItemRef: an item named in crm.save.
public struct CrmItemRef: Codable, Sendable, Hashable {
    public var kind: CrmItemKind
    public var guid: String

    public init(kind: CrmItemKind, guid: String) {
        self.kind = kind
        self.guid = guid
    }
}

/// api.CrmRecord: a message saved in a CRM account.
public struct CrmRecord: Codable, Sendable, Equatable {
    /// The eway account.
    public var accountId: AccountID
    public var itemGuid: String
    public var savedAt: Date
    public var checkedAt: Date
    public var `private`: Bool
    /// The daemon saved the message by itself, following a saved
    /// conversation (`EwayConfig.followThreads`, docs/eway.md D22).
    @DefaultFalse public var auto: Bool
    @NullAsEmpty public var items: [CrmItem]
    public var url: String

    public init(
        accountId: AccountID, itemGuid: String, savedAt: Date = .goZero, checkedAt: Date = .goZero,
        private isPrivate: Bool = false, auto: Bool = false, items: [CrmItem] = [], url: String = ""
    ) {
        self.accountId = accountId
        self.itemGuid = itemGuid
        self.savedAt = savedAt
        self.checkedAt = checkedAt
        self.private = isPrivate
        self.auto = auto
        self.items = items
        self.url = url
    }
}

// MARK: account.detectEway (§4.1)

/// api.AccountDetectEwayParams: what the user typed; nothing is stored or
/// authenticated.
public struct AccountDetectEwayParams: Codable, Sendable, Equatable {
    public var serviceUrl: String

    public init(serviceUrl: String) {
        self.serviceUrl = serviceUrl
    }
}

/// api.AccountDetectEwayResult.
public struct AccountDetectEwayResult: Codable, Sendable, Equatable {
    /// Normalised, as `EwayConfig.serviceUrl`.
    public var serviceUrl: String
    /// The instance signs in through Microsoft Entra, which this version
    /// cannot do.
    public var microsoftSignIn: Bool
    /// Untrusted display text.
    public var version: String?

    public init(serviceUrl: String, microsoftSignIn: Bool = false, version: String? = nil) {
        self.serviceUrl = serviceUrl
        self.microsoftSignIn = microsoftSignIn
        self.version = version
    }
}

// MARK: Error data

/// api.CrmSignInReason: why an instance cannot be signed in to.
public struct CrmSignInReason: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Microsoft Entra sign-in.
    public static let microsoft: CrmSignInReason = "microsoft"
    /// The plan has no API access.
    public static let plan: CrmSignInReason = "plan"
}

/// api.CrmSignInUnsupportedData: `error.data` of `crmSignInUnsupported`.
public struct CrmSignInUnsupportedData: Codable, Sendable, Equatable {
    public var reason: CrmSignInReason

    public init(reason: CrmSignInReason) {
        self.reason = reason
    }
}

/// api.MessagesNotDownloadedData: `error.data` of `partNotDownloaded` from
/// crm.save, the messages not whole on this device.
public struct MessagesNotDownloadedData: Codable, Sendable, Equatable {
    @NullAsEmpty public var messageIds: [MessageID]

    public init(messageIds: [MessageID]) {
        self.messageIds = messageIds
    }
}

extension RPCError {
    /// The data of a `crmSignInUnsupported` error; nil for any other code
    /// or data of another shape.
    public var crmSignInUnsupported: CrmSignInUnsupportedData? {
        guard code == .crmSignInUnsupported else { return nil }
        return crmData(CrmSignInUnsupportedData.self)
    }

    /// The messages of a `partNotDownloaded` from crm.save; nil for any
    /// other code or data of another shape.
    public var messagesNotDownloaded: MessagesNotDownloadedData? {
        guard code == .partNotDownloaded else { return nil }
        return crmData(MessagesNotDownloadedData.self)
    }

    private func crmData<T: Decodable>(_ type: T.Type) -> T? {
        guard let data, data.objectValue != nil, let raw = try? JSONCoding.encoder().encode(data) else { return nil }
        return try? JSONCoding.decoder().decode(type, from: raw)
    }
}

// MARK: crm.* (§4.17)

/// api.CrmLookupParams. `accountId` nil = the first enabled eway account.
public struct CrmLookupParams: Codable, Sendable, Equatable {
    public var accountId: AccountID?
    public var messageAccountId: AccountID
    /// 1 to `API.Limits.maxCrmMessages`.
    public var messageIds: [MessageID]

    public init(accountId: AccountID? = nil, messageAccountId: AccountID, messageIds: [MessageID]) {
        self.accountId = accountId
        self.messageAccountId = messageAccountId
        self.messageIds = messageIds
    }
}

/// api.CrmSavedMessage: `record` nil = not saved in that account.
public struct CrmSavedMessage: Codable, Sendable, Equatable {
    public var messageId: MessageID
    public var record: CrmRecord?

    public init(messageId: MessageID, record: CrmRecord? = nil) {
        self.messageId = messageId
        self.record = record
    }

    private enum CodingKeys: String, CodingKey {
        case messageId, record
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(messageId, forKey: .messageId)
        // Always sent, null when not saved (as Go writes it).
        try c.encode(record, forKey: .record)
    }
}

/// api.CrmLookupResult.
public struct CrmLookupResult: Codable, Sendable, Equatable {
    public var accountId: AccountID
    @NullAsEmpty public var saved: [CrmSavedMessage]
    @NullAsEmpty public var suggestions: [CrmItem]
    @DefaultFalse public var partial: Bool

    public init(accountId: AccountID, saved: [CrmSavedMessage] = [], suggestions: [CrmItem] = [], partial: Bool = false) {
        self.accountId = accountId
        self.saved = saved
        self.suggestions = suggestions
        self.partial = partial
    }
}

/// api.CrmSearchParams. `kinds` empty = all.
public struct CrmSearchParams: Codable, Sendable, Equatable {
    public var accountId: AccountID?
    public var query: String
    public var kinds: [CrmItemKind]?

    public init(accountId: AccountID? = nil, query: String, kinds: [CrmItemKind]? = nil) {
        self.accountId = accountId
        self.query = query
        self.kinds = kinds
    }
}

/// api.CrmSearchResult.
public struct CrmSearchResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var items: [CrmItem]
    @DefaultFalse public var truncated: Bool

    public init(items: [CrmItem] = [], truncated: Bool = false) {
        self.items = items
        self.truncated = truncated
    }
}

/// api.CrmSaveParams: `unlink` only with one message, already saved;
/// `superior` nil adds none.
public struct CrmSaveParams: Codable, Sendable, Equatable {
    public var accountId: AccountID?
    public var messageAccountId: AccountID
    public var messageIds: [MessageID]
    public var link: [CrmItemRef]?
    public var unlink: [CrmItemRef]?
    public var superior: CrmItemRef?
    public var `private`: Bool?

    public init(
        accountId: AccountID? = nil, messageAccountId: AccountID, messageIds: [MessageID],
        link: [CrmItemRef]? = nil, unlink: [CrmItemRef]? = nil, superior: CrmItemRef? = nil, private isPrivate: Bool? = nil
    ) {
        self.accountId = accountId
        self.messageAccountId = messageAccountId
        self.messageIds = messageIds
        self.link = link
        self.unlink = unlink
        self.superior = superior
        self.private = isPrivate
    }
}

/// api.CrmSaveOutcome: `record` on success, `error` otherwise.
public struct CrmSaveOutcome: Codable, Sendable, Equatable {
    public var messageId: MessageID
    public var record: CrmRecord?
    @DefaultFalse public var created: Bool
    public var error: RPCError?

    public init(messageId: MessageID, record: CrmRecord? = nil, created: Bool = false, error: RPCError? = nil) {
        self.messageId = messageId
        self.record = record
        self.created = created
        self.error = error
    }
}

/// api.CrmSaveResult: one outcome per message, in order.
public struct CrmSaveResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var results: [CrmSaveOutcome]

    public init(results: [CrmSaveOutcome] = []) {
        self.results = results
    }
}

/// api.CrmRemoveParams.
public struct CrmRemoveParams: Codable, Sendable, Equatable {
    public var accountId: AccountID?
    public var messageAccountId: AccountID
    public var messageId: MessageID

    public init(accountId: AccountID? = nil, messageAccountId: AccountID, messageId: MessageID) {
        self.accountId = accountId
        self.messageAccountId = messageAccountId
        self.messageId = messageId
    }
}

// MARK: Methods

extension API {
    /// What an eWay-CRM service address is (one anonymous question, 15 s).
    public enum AccountDetectEway: RPCMethod {
        public typealias Params = AccountDetectEwayParams
        public typealias Result = AccountDetectEwayResult
        public static let name = "account.detectEway"
        public static let timeout = RPCTimeouts.detectSite
    }

    /// The daemon's budget is 15 s for the CRM's answers.
    public enum CrmLookup: RPCMethod {
        public typealias Params = CrmLookupParams
        public typealias Result = CrmLookupResult
        public static let name = "crm.lookup"
        public static let timeout = RPCTimeouts.crmLookup
    }

    public enum CrmSearch: RPCMethod {
        public typealias Params = CrmSearchParams
        public typealias Result = CrmSearchResult
        public static let name = "crm.search"
        public static let timeout = RPCTimeouts.crmLookup
    }

    /// Uploads up to 20 messages one after the other (2 min each at most).
    public enum CrmSave: RPCMethod {
        public typealias Params = CrmSaveParams
        public typealias Result = CrmSaveResult
        public static let name = "crm.save"
        public static let timeout = RPCTimeouts.crmSave
    }

    public enum CrmRemove: RPCMethod {
        public typealias Params = CrmRemoveParams
        public typealias Result = EmptyResult
        public static let name = "crm.remove"
        public static let timeout = RPCTimeouts.remote
    }

    /// The CRM's methods, in the order of methods.go.
    static let crmMethods: [any RPCMethod.Type] = [
        AccountDetectEway.self, CrmLookup.self, CrmSearch.self, CrmSave.self, CrmRemove.self,
    ]
}

// MARK: Limits

extension API.Limits {
    /// docs/eway.md D16 (pkg/api/crm.go).
    public static let maxCrmMessages = 20
    public static let maxCrmItems = 50
    public static let maxCrmSuggestions = 50
    public static let maxCrmSearchResults = 50
    public static let minCrmQueryBytes = 2
    public static let maxCrmQueryBytes = 200
    public static let maxCrmLookupAddresses = 20
    public static let maxCrmNoteBytes = 32 << 10
    public static let maxCrmNameBytes = 256
    public static let maxCrmDetailBytes = 512
}
