// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The string enumerations of backend/pkg/api/types.go as extensible structs:
// a value a newer daemon adds decodes as itself instead of failing the whole
// result (docs/api.md §6: clients ignore what they do not know). The named
// constants are the values of the contract (protocol version 2).

import Foundation

/// A wire enum: a known set of string values that may grow.
public protocol WireEnum: StringWireValue {}

/// api.Security: transport security of an IMAP/SMTP endpoint.
public struct Security: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Implicit TLS (IMAPS 993 / SMTPS 465).
    public static let tls: Security = "tls"
    /// Upgrade on a plain port (143 / 587).
    public static let starttls: Security = "starttls"
    /// Plaintext; the daemon accepts it only for localhost.
    public static let none: Security = "none"
}

/// api.AuthMethod: how the daemon signs in to an endpoint.
public struct AuthMethod: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let password: AuthMethod = "password"
    public static let oauth2: AuthMethod = "oauth2"
}

/// api.OAuth2Source: who holds the sign-in of an oauth2 endpoint.
public struct OAuth2Source: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// GNOME Online Accounts; not available on macOS.
    public static let goa: OAuth2Source = "goa"
    /// The daemon's own sign-in (authorization code with PKCE,
    /// `account.oauthStart`); the refresh token is in the keyring.
    public static let daemon: OAuth2Source = "daemon"
}

/// api.OAuth2Provider* constants: whose OAuth2 account an endpoint uses.
public struct OAuth2Provider: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let google: OAuth2Provider = "google"
    public static let office365: OAuth2Provider = "office365"
    public static let custom: OAuth2Provider = "custom"
}

/// api.AccountKind: the protocol behind an account.
public struct AccountKind: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// IMAP for the mailbox, SMTP for sending. The default when absent.
    public static let imap: AccountKind = "imap"
    /// Microsoft 365 / Outlook.com through the Graph API.
    public static let graph: AccountKind = "graph"
}

/// api.GraphSource: who holds the OAuth2 session of a Graph account.
public struct GraphSource: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let goa: GraphSource = "goa"
    /// The daemon's own sign-in; the account's `oauth2` block says
    /// `{source: daemon, provider: office365}`.
    public static let daemon: GraphSource = "daemon"
}

/// api.DiscoverSource: where an `account.discover` suggestion came from,
/// from most to least trustworthy.
public struct DiscoverSource: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let goa: DiscoverSource = "goa"
    public static let ispdb: DiscoverSource = "ispdb"
    public static let autoconfig: DiscoverSource = "autoconfig"
    public static let srv: DiscoverSource = "srv"
    /// A known provider that signs in with OAuth2 (GNOME Online Accounts
    /// or the daemon's own sign-in).
    public static let provider: DiscoverSource = "provider"
    public static let guess: DiscoverSource = "guess"
    /// Nothing found; `config` is absent.
    public static let none: DiscoverSource = "none"
}

/// api.LinkedAccount.provider: the desktop service's account type.
public struct LinkedProvider: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let microsoft365: LinkedProvider = "microsoft365"
    public static let google: LinkedProvider = "google"
}

/// api.OAuthSessionStatus: what `account.oauthWait` reports of a sign-in.
public struct OAuthSessionStatus: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// The browser has not come back yet; call again.
    public static let pending: OAuthSessionStatus = "pending"
    /// Signed in; the result carries the config.
    public static let complete: OAuthSessionStatus = "complete"
}

/// api.FolderRole: the special-use classification of a folder.
public struct FolderRole: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let none: FolderRole = "none"
    public static let inbox: FolderRole = "inbox"
    public static let sent: FolderRole = "sent"
    public static let drafts: FolderRole = "drafts"
    public static let trash: FolderRole = "trash"
    public static let junk: FolderRole = "junk"
    public static let archive: FolderRole = "archive"
    public static let all: FolderRole = "all"
    /// The local-only queue of messages to send.
    public static let outbox: FolderRole = "outbox"
}

/// api.Flag: a message flag; the daemon maps them to IMAP flags.
public struct Flag: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let seen: Flag = "seen"
    public static let answered: Flag = "answered"
    public static let flagged: Flag = "flagged"
    public static let draft: Flag = "draft"
    public static let deleted: Flag = "deleted"
    public static let junk: Flag = "junk"
    public static let forwarded: Flag = "forwarded"
}

/// api.OutboxState: the delivery state of a queued message.
public struct OutboxState: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Waiting for the next attempt.
    public static let queued: OutboxState = "queued"
    /// An SMTP session is running.
    public static let sending: OutboxState = "sending"
    /// Delivered; the Sent copy is pending.
    public static let sent: OutboxState = "sent"
    /// Permanent failure; `outbox.retry` re-queues it.
    public static let failed: OutboxState = "failed"
}

/// api.SortOrder for message and thread lists.
public struct SortOrder: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let dateDesc: SortOrder = "dateDesc"
    public static let dateAsc: SortOrder = "dateAsc"
}

/// api.MessageFilter: the subset of a folder a listing shows.
public struct MessageFilter: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let all: MessageFilter = "all"
    /// Without the `seen` flag.
    public static let unread: MessageFilter = "unread"
    /// With the `flagged` flag.
    public static let flagged: MessageFilter = "flagged"
}

/// api.RemoteContentPolicy: how the daemon treats remote references when
/// producing a body. `knownSenders` is valid only as the stored preference;
/// a result reports `block` or `allow`, never `knownSenders`.
public struct RemoteContentPolicy: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Every remote reference stripped. The default.
    public static let block: RemoteContentPolicy = "block"
    /// `https:` images fetched by the daemon and inlined, after consent.
    public static let allow: RemoteContentPolicy = "allow"
    /// Resolved per message to `allow` when every sender is a known sender.
    public static let knownSenders: RemoteContentPolicy = "knownSenders"
}

/// api.BodyState: whether the daemon holds the message content.
public struct BodyState: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let fetched: BodyState = "fetched"
    public static let pending: BodyState = "pending"
    public static let tooBig: BodyState = "tooBig"
    public static let failed: BodyState = "failed"
}

/// api.ComposeMode: how `draft.create` pre-fills a draft.
public struct ComposeMode: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let new: ComposeMode = "new"
    public static let reply: ComposeMode = "reply"
    public static let replyAll: ComposeMode = "replyAll"
    public static let forward: ComposeMode = "forward"
}

/// api.QuoteForm: how much of the original a `draft.create` result quotes.
public struct QuoteForm: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let html: QuoteForm = "html"
    public static let text: QuoteForm = "text"
    public static let none: QuoteForm = "none"
}

/// api.SyncStatus: the coarse state of one account.
public struct SyncStatus: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let idle: SyncStatus = "idle"
    public static let syncing: SyncStatus = "syncing"
    public static let offline: SyncStatus = "offline"
    public static let authRequired: SyncStatus = "authRequired"
    public static let error: SyncStatus = "error"
    public static let disabled: SyncStatus = "disabled"
}

/// api.KnownSenderSource* constants: why an address is a known sender.
public struct KnownSenderSource: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// A recipient of mail the user sent.
    public static let sent: KnownSenderSource = "sent"
    /// An explicit decision of the user.
    public static let user: KnownSenderSource = "user"
}

/// api.ContactSource: where a recipient suggestion came from.
public struct ContactSource: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// A recipient of mail the user sent; never an incoming `From`.
    public static let sent: ContactSource = "sent"
    /// A system address book, read only.
    public static let addressBook: ContactSource = "addressBook"
}
