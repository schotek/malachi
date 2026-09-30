// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Issue-tracker (Jira) accounts: the `jira` block of an account
// (docs/api.md §3, §4.1 account.detectSite and account.listSpaces), the
// issue projection of a message and a thread (§3, §4.3, §4.4), comment
// drafts (§4.5) and notify.messagesChanged (§5); types.go, the Jira types.
// Everything that came from the site (summaries, names, statuses, titles)
// is hostile input like mail: display it as plain text.

import Foundation

/// api.SpaceRef: a Jira space (project) the account synchronises.
public struct SpaceRef: Codable, Sendable, Hashable {
    public var id: String
    public var key: String
    /// Display only; the daemon may refresh it.
    public var name: String?

    public init(id: String, key: String, name: String? = nil) {
        self.id = id
        self.key = key
        self.name = name
    }
}

/// api.StatusRef: an issue status by id, the name for display only.
public struct StatusRef: Codable, Sendable, Hashable {
    public var id: String
    public var name: String?

    public init(id: String, name: String? = nil) {
        self.id = id
        self.name = name
    }
}

/// api.JiraConfig: the `jira` block of an account of kind `jira` (its
/// `imap`, `smtp`, `graph` and `oauth2` are nil). The zero value of every
/// field is the default. Every Go field is here, so that an
/// `account.update` built from a listed account never drops one; the
/// encoding leaves out what Go's `omitempty` leaves out (nil and empty
/// lists), `siteUrl`, `deployment` and `spaces` are always written.
public struct JiraConfig: Codable, Sendable, Equatable {
    /// Normalised by the daemon: https (http only for loopback or a Data
    /// Center the user typed http for), no userinfo, query or fragment, no
    /// trailing slash; a Data Center may carry a context path.
    public var siteUrl: String
    public var deployment: JiraDeployment
    /// Cloud only: the tenant UUID; enables the gateway route (scoped tokens).
    public var cloudId: String?
    /// Cloud: the Atlassian e-mail for Basic auth (required); Data Center: nil.
    public var login: String?
    /// 1 to `API.Limits.maxJiraSpaces`.
    @NullAsEmpty public var spaces: [SpaceRef]
    /// The account's own retention window: issues updated within it are
    /// kept, open issues assigned to the user whatever their age. nil or 0
    /// = `API.Limits.defaultJiraOfflineDays`; at most
    /// `API.Limits.maxJiraOfflineDays`.
    public var offlineDays: Int?
    /// Only the issues the user reports, is assigned, watches or updated
    /// recently, instead of every issue of the spaces.
    public var onlyMine: Bool?
    /// Status and assignee changes are not listed.
    public var hideEvents: Bool?
    /// Empty = all three virtual folders are shown.
    @NullAsEmpty public var disabledFolders: [VirtualFolder]
    /// Empty = the statuses of category `done`.
    @NullAsEmpty public var closedStatuses: [StatusRef]
    /// nil = `sync`.
    public var notificationMail: NotificationMailMode?
    /// "addr@host" or "@host"; empty = "@<site host>" on cloud, none on
    /// Data Center.
    @NullAsEmpty public var notificationSenders: [String]
    /// Display names of bots whose comments relay someone else's, which
    /// the daemon re-attributes to the author they name. Empty = none.
    @NullAsEmpty public var botNames: [String]
    /// RE2 patterns, each matched against a whole trimmed line of a
    /// relayed comment; matching lines are removed.
    @NullAsEmpty public var metadataFilters: [String]
    /// Line prefixes that introduce the original author in a relayed comment.
    @NullAsEmpty public var authorPrefixes: [String]

    public init(
        siteUrl: String, deployment: JiraDeployment, cloudId: String? = nil, login: String? = nil,
        spaces: [SpaceRef] = [], offlineDays: Int? = nil, onlyMine: Bool? = nil, hideEvents: Bool? = nil,
        disabledFolders: [VirtualFolder] = [], closedStatuses: [StatusRef] = [],
        notificationMail: NotificationMailMode? = nil, notificationSenders: [String] = [], botNames: [String] = [],
        metadataFilters: [String] = [], authorPrefixes: [String] = []
    ) {
        self.siteUrl = siteUrl
        self.deployment = deployment
        self.cloudId = cloudId
        self.login = login
        self.spaces = spaces
        self.offlineDays = offlineDays
        self.onlyMine = onlyMine
        self.hideEvents = hideEvents
        self.disabledFolders = disabledFolders
        self.closedStatuses = closedStatuses
        self.notificationMail = notificationMail
        self.notificationSenders = notificationSenders
        self.botNames = botNames
        self.metadataFilters = metadataFilters
        self.authorPrefixes = authorPrefixes
    }

    private enum CodingKeys: String, CodingKey {
        case siteUrl, deployment, cloudId, login, spaces, offlineDays, onlyMine, hideEvents, disabledFolders
        case closedStatuses, notificationMail, notificationSenders, botNames, metadataFilters, authorPrefixes
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(siteUrl, forKey: .siteUrl)
        try c.encode(deployment, forKey: .deployment)
        try c.encodeIfPresent(cloudId, forKey: .cloudId)
        try c.encodeIfPresent(login, forKey: .login)
        try c.encode(spaces, forKey: .spaces)
        try c.encodeIfPresent(offlineDays, forKey: .offlineDays)
        try c.encodeIfPresent(onlyMine, forKey: .onlyMine)
        try c.encodeIfPresent(hideEvents, forKey: .hideEvents)
        try c.encodeUnlessEmpty(disabledFolders, forKey: .disabledFolders)
        try c.encodeUnlessEmpty(closedStatuses, forKey: .closedStatuses)
        try c.encodeIfPresent(notificationMail, forKey: .notificationMail)
        try c.encodeUnlessEmpty(notificationSenders, forKey: .notificationSenders)
        try c.encodeUnlessEmpty(botNames, forKey: .botNames)
        try c.encodeUnlessEmpty(metadataFilters, forKey: .metadataFilters)
        try c.encodeUnlessEmpty(authorPrefixes, forKey: .authorPrefixes)
    }
}

extension KeyedEncodingContainer {
    /// Go's `omitempty` on a slice: an empty list is left out.
    mutating func encodeUnlessEmpty<T: Encodable>(_ value: [T], forKey key: Key) throws {
        if !value.isEmpty {
            try encode(value, forKey: key)
        }
    }
}

/// api.IssueInfo: the issue a message or a thread of a jira account
/// belongs to. Every string is untrusted display text from the site.
public struct IssueInfo: Codable, Sendable, Equatable {
    public var key: String
    /// `<siteUrl>/browse/<key>`, http(s) only; still checked before opening.
    public var url: String
    public var summary: String
    public var status: String
    /// nil (or empty) when the daemon does not know it.
    public var statusCategory: IssueStatusCategory?
    public var type: String?
    public var priority: String?
    /// Display name; nil = unassigned.
    public var assignee: String?
    public var reporter: String?
    public var assignedToMe: Bool?
    public var watching: Bool?
    /// `[public, internal]` on a service-desk request, else empty.
    @NullAsEmpty public var commentVisibilities: [CommentVisibility]

    public init(
        key: String, url: String, summary: String, status: String, statusCategory: IssueStatusCategory? = nil,
        type: String? = nil, priority: String? = nil, assignee: String? = nil, reporter: String? = nil,
        assignedToMe: Bool? = nil, watching: Bool? = nil, commentVisibilities: [CommentVisibility] = []
    ) {
        self.key = key
        self.url = url
        self.summary = summary
        self.status = status
        self.statusCategory = statusCategory
        self.type = type
        self.priority = priority
        self.assignee = assignee
        self.reporter = reporter
        self.assignedToMe = assignedToMe
        self.watching = watching
        self.commentVisibilities = commentVisibilities
    }

    private enum CodingKeys: String, CodingKey {
        case key, url, summary, status, statusCategory, type, priority, assignee, reporter, assignedToMe, watching
        case commentVisibilities
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(key, forKey: .key)
        try c.encode(url, forKey: .url)
        try c.encode(summary, forKey: .summary)
        try c.encode(status, forKey: .status)
        try c.encodeIfPresent(statusCategory, forKey: .statusCategory)
        try c.encodeIfPresent(type, forKey: .type)
        try c.encodeIfPresent(priority, forKey: .priority)
        try c.encodeIfPresent(assignee, forKey: .assignee)
        try c.encodeIfPresent(reporter, forKey: .reporter)
        try c.encodeIfPresent(assignedToMe, forKey: .assignedToMe)
        try c.encodeIfPresent(watching, forKey: .watching)
        try c.encodeUnlessEmpty(commentVisibilities, forKey: .commentVisibilities)
    }
}

/// api.IssueChange: one field an event row changed; an empty side is nil.
public struct IssueChange: Codable, Sendable, Equatable {
    public var field: IssueField
    public var from: String?
    public var to: String?

    public init(field: IssueField, from: String? = nil, to: String? = nil) {
        self.field = field
        self.from = from
        self.to = to
    }
}

/// api.MessageIssue: `MessageSummary.issue`. Go embeds `IssueInfo`, so on
/// the wire its fields sit beside these; here they are `info`, and the
/// coding flattens (as `Message` does with its summary).
public struct MessageIssue: Codable, Sendable, Equatable {
    public var info: IssueInfo
    public var item: IssueItemKind
    /// A comment's visibility on a service-desk request; nil = public.
    public var visibility: CommentVisibility?
    /// Item `event` only: what changed. The UI builds the sentence from
    /// these; the body and snippet are language-neutral values.
    @NullAsEmpty public var changes: [IssueChange]
    /// The bot that relayed a re-attributed comment (display name).
    public var via: String?
    public var edited: Bool?
    /// The account's own user wrote the item on the site (never set with
    /// `via`: a relayed comment is someone else's).
    public var mine: Bool?

    public init(
        info: IssueInfo, item: IssueItemKind, visibility: CommentVisibility? = nil, changes: [IssueChange] = [],
        via: String? = nil, edited: Bool? = nil, mine: Bool? = nil
    ) {
        self.info = info
        self.item = item
        self.visibility = visibility
        self.changes = changes
        self.via = via
        self.edited = edited
        self.mine = mine
    }

    private enum CodingKeys: String, CodingKey {
        case item, visibility, changes, via, edited, mine
    }

    public init(from decoder: any Decoder) throws {
        info = try IssueInfo(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        item = try c.decode(IssueItemKind.self, forKey: .item)
        visibility = try c.decodeIfPresent(CommentVisibility.self, forKey: .visibility)
        changes = try c.decodeIfPresent([IssueChange].self, forKey: .changes) ?? []
        via = try c.decodeIfPresent(String.self, forKey: .via)
        edited = try c.decodeIfPresent(Bool.self, forKey: .edited)
        mine = try c.decodeIfPresent(Bool.self, forKey: .mine)
    }

    public func encode(to encoder: any Encoder) throws {
        try info.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(item, forKey: .item)
        try c.encodeIfPresent(visibility, forKey: .visibility)
        try c.encodeUnlessEmpty(changes, forKey: .changes)
        try c.encodeIfPresent(via, forKey: .via)
        try c.encodeIfPresent(edited, forKey: .edited)
        try c.encodeIfPresent(mine, forKey: .mine)
    }
}

/// api.AccountDetectSiteParams: "acme.atlassian.net" or a full URL; https
/// is assumed.
public struct AccountDetectSiteParams: Codable, Sendable, Equatable {
    public var url: String

    public init(url: String) {
        self.url = url
    }
}

/// api.AccountDetectSiteResult: what the address turned out to be. Nothing
/// is stored or authenticated.
public struct AccountDetectSiteResult: Codable, Sendable, Equatable {
    /// `jira`.
    public var kind: AccountKind
    /// Normalised; goes into `JiraConfig.siteUrl` as it is.
    public var siteUrl: String
    public var deployment: JiraDeployment
    public var cloudId: String?
    /// Untrusted display text.
    public var title: String?
    /// Untrusted display text.
    public var version: String?

    public init(
        kind: AccountKind, siteUrl: String, deployment: JiraDeployment, cloudId: String? = nil, title: String? = nil,
        version: String? = nil
    ) {
        self.kind = kind
        self.siteUrl = siteUrl
        self.deployment = deployment
        self.cloudId = cloudId
        self.title = title
        self.version = version
    }
}

/// api.AccountListSpacesParams: `config` of kind jira with its connection
/// fields (spaces may be empty). With `accountId` and no password the
/// stored token of that account is used. `counts` estimates the issues
/// updated within `config.jira.offlineDays`.
public struct AccountListSpacesParams: Codable, Sendable, Equatable {
    public var accountId: AccountID?
    public var config: AccountConfig
    public var credentials: Credentials
    public var counts: Bool?

    public init(accountId: AccountID? = nil, config: AccountConfig, credentials: Credentials = Credentials(), counts: Bool? = nil) {
        self.accountId = accountId
        self.config = config
        self.credentials = credentials
        self.counts = counts
    }
}

/// api.Space: a space (project) the token can see.
public struct Space: Codable, Sendable, Equatable {
    public var id: String
    public var key: String
    public var name: String
    /// A Jira Service Management project.
    public var serviceDesk: Bool?
    /// -1 = not counted.
    public var issues: Int

    public init(id: String, key: String, name: String, serviceDesk: Bool? = nil, issues: Int = -1) {
        self.id = id
        self.key = key
        self.name = name
        self.serviceDesk = serviceDesk
        self.issues = issues
    }
}

/// api.IssueStatus: a status of the site, for `JiraConfig.closedStatuses`.
public struct IssueStatus: Codable, Sendable, Equatable {
    public var id: String
    public var name: String
    public var category: IssueStatusCategory

    public init(id: String, name: String, category: IssueStatusCategory) {
        self.id = id
        self.name = name
        self.category = category
    }
}

/// api.SiteUser: the user the token belongs to. `email` may be hidden by
/// the site (Data Center, privacy settings).
public struct SiteUser: Codable, Sendable, Equatable {
    public var name: String
    public var email: String?

    public init(name: String, email: String? = nil) {
        self.name = name
        self.email = email
    }
}

/// api.AccountListSpacesResult: `spaces` in name order, at most 1000.
public struct AccountListSpacesResult: Codable, Sendable, Equatable {
    public var user: SiteUser
    @NullAsEmpty public var spaces: [Space]
    @NullAsEmpty public var statuses: [IssueStatus]

    public init(user: SiteUser, spaces: [Space], statuses: [IssueStatus] = []) {
        self.user = user
        self.spaces = spaces
        self.statuses = statuses
    }
}

/// api.DraftComment: `Draft.comment` of a comment draft on an issue. In
/// `draft.save` only `visibility` is read.
public struct DraftComment: Codable, Sendable, Equatable {
    public var issue: IssueInfo
    /// Empty = public; `internal` only when `issue.commentVisibilities`
    /// allows it.
    public var visibility: CommentVisibility

    public init(issue: IssueInfo, visibility: CommentVisibility = "") {
        self.issue = issue
        self.visibility = visibility
    }
}

/// api.MessagesChangedNotification (`notify.messagesChanged`): messages of
/// the account's folders changed without arriving or being deleted: hidden
/// or shown again (a Jira notification mail hidden in a mail account), or
/// rebuilt in place under their ids (a Jira account's items rendered with
/// other settings, a comment edited or re-attributed, an issue renamed).
/// Clients showing those folders drop what they cached of their messages
/// and list them again. `folderIds` empty = any folder of the account.
/// `DaemonNotification.messagesChanged` carries it.
public struct MessagesChangedNotification: Codable, Sendable, Equatable {
    public var accountId: AccountID
    @NullAsEmpty public var folderIds: [FolderID]

    public init(accountId: AccountID, folderIds: [FolderID] = []) {
        self.accountId = accountId
        self.folderIds = folderIds
    }
}
