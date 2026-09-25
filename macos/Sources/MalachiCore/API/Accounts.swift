// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Accounts (docs/api.md §4.1; types.go "Accounts").

import Foundation

/// api.Page: which slice of a list to return. `cursor` is opaque and passed
/// back unchanged; nil means from the beginning. `limit` nil is the server
/// default (50), clamped to 500.
public struct Page: Codable, Sendable, Equatable {
    public var cursor: String?
    public var limit: Int?

    public init(cursor: String? = nil, limit: Int? = nil) {
        self.cursor = cursor
        self.limit = limit
    }
}

/// api.PageInfo: comes with every list. `nextCursor` is nil on the last
/// page; `total` is -1 when the daemon cannot cheaply compute it.
public struct PageInfo: Codable, Sendable, Equatable {
    public var nextCursor: String?
    public var total: Int

    public init(nextCursor: String? = nil, total: Int) {
        self.nextCursor = nextCursor
        self.total = total
    }
}

/// api.SystemInfoResult: the answer of `system.info` (docs/api.md §4.0).
public struct SystemInfoResult: Codable, Sendable, Equatable {
    public var version: String
    public var protocolVersion: Int
    public var pid: Int
    public var storePath: String

    public init(version: String, protocolVersion: Int, pid: Int, storePath: String) {
        self.version = version
        self.protocolVersion = protocolVersion
        self.pid = pid
        self.storePath = storePath
    }
}

/// The name the skeleton used for the `system.info` result.
public typealias SystemInfo = SystemInfoResult

/// api.ServerConfig: one endpoint (IMAP or SMTP).
public struct ServerConfig: Codable, Sendable, Equatable {
    public var host: String
    public var port: Int
    public var security: Security
    public var username: String
    public var authMethod: AuthMethod

    public init(host: String, port: Int, security: Security, username: String, authMethod: AuthMethod) {
        self.host = host
        self.port = port
        self.security = security
        self.username = username
        self.authMethod = authMethod
    }
}

/// api.OAuth2Config: present when an endpoint uses `oauth2`, or on a Graph
/// account whose token comes from the daemon's own sign-in. With `source`
/// goa only `provider` and `goaAccountId` are set; with `source` daemon
/// `provider` (google on IMAP, office365 on Graph) and optionally
/// `clientId`/`tenantId`. Without `source` (provider custom) it is reserved
/// and not implemented.
public struct OAuth2Config: Codable, Sendable, Equatable {
    public var source: OAuth2Source?
    public var goaAccountId: String?
    public var provider: OAuth2Provider
    public var clientId: String?
    public var tenantId: String?
    public var authUrl: String?
    public var tokenUrl: String?
    public var scopes: [String]?

    public init(
        source: OAuth2Source? = nil, goaAccountId: String? = nil, provider: OAuth2Provider,
        clientId: String? = nil, tenantId: String? = nil, authUrl: String? = nil, tokenUrl: String? = nil,
        scopes: [String]? = nil
    ) {
        self.source = source
        self.goaAccountId = goaAccountId
        self.provider = provider
        self.clientId = clientId
        self.tenantId = tenantId
        self.authUrl = authUrl
        self.tokenUrl = tokenUrl
        self.scopes = scopes
    }
}

/// api.GraphConfig: present only for a `graph` account. `goaAccountId`
/// only with source goa; a daemon account carries an `oauth2` block too.
public struct GraphConfig: Codable, Sendable, Equatable {
    public var source: GraphSource
    public var goaAccountId: String?

    public init(source: GraphSource, goaAccountId: String? = nil) {
        self.source = source
        self.goaAccountId = goaAccountId
    }
}

/// api.AccountConfig: the non-secret part of an account. Secrets travel only
/// in `Credentials` at add/test time and are never returned by any method.
/// `imap` and `smtp` are set for an IMAP account and absent for Graph; `graph`
/// the other way round.
public struct AccountConfig: Codable, Sendable, Equatable {
    /// Display name of the account.
    public var name: String
    /// Primary address.
    public var email: String
    public var displayName: String?
    /// nil means `imap`; see `protocolKind`.
    public var kind: AccountKind?
    public var imap: ServerConfig?
    public var smtp: ServerConfig?
    public var oauth2: OAuth2Config?
    public var graph: GraphConfig?
    /// nil = the daemon's default.
    public var syncIntervalSeconds: Int?

    public init(
        name: String, email: String, displayName: String? = nil, kind: AccountKind? = nil,
        imap: ServerConfig? = nil, smtp: ServerConfig? = nil, oauth2: OAuth2Config? = nil,
        graph: GraphConfig? = nil, syncIntervalSeconds: Int? = nil
    ) {
        self.name = name
        self.email = email
        self.displayName = displayName
        self.kind = kind
        self.imap = imap
        self.smtp = smtp
        self.oauth2 = oauth2
        self.graph = graph
        self.syncIntervalSeconds = syncIntervalSeconds
    }

    /// api.AccountConfig.Protocol: `kind` with the empty default resolved.
    public var protocolKind: AccountKind { kind ?? .imap }
}

/// api.Credentials: secrets for `account.add`, `account.update` and
/// `account.test`. Write-only: a password for password endpoints, the id of
/// a completed `account.oauthStart` session for a daemon sign-in (its tokens
/// never leave the daemon), nothing for a GNOME Online Accounts account.
/// Printing one (a log line, an assertion, a debugger) shows whether a
/// field is set, never its value.
public struct Credentials: Codable, Sendable, Equatable, CustomStringConvertible, CustomDebugStringConvertible {
    public var password: String?
    /// Consumed by `account.add` / `account.update`; `account.test` only
    /// reads it.
    public var oauthSession: String?

    public init(password: String? = nil, oauthSession: String? = nil) {
        self.password = password
        self.oauthSession = oauthSession
    }

    public var description: String {
        "Credentials(password: \(password == nil ? "nil" : "<redacted>"), oauthSession: \(oauthSession == nil ? "nil" : "<set>"))"
    }

    public var debugDescription: String { description }
}

/// api.Account: what `account.list` returns, config plus derived state.
public struct Account: Codable, Sendable, Equatable {
    public var id: AccountID
    public var config: AccountConfig
    public var enabled: Bool
    public var state: SyncState

    public init(id: AccountID, config: AccountConfig, enabled: Bool, state: SyncState) {
        self.id = id
        self.config = config
        self.enabled = enabled
        self.state = state
    }
}

/// api.AccountListResult.
public struct AccountListResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var accounts: [Account]

    public init(accounts: [Account]) {
        self.accounts = accounts
    }
}

/// api.AccountAddParams.
public struct AccountAddParams: Codable, Sendable, Equatable {
    public var config: AccountConfig
    public var credentials: Credentials

    public init(config: AccountConfig, credentials: Credentials = Credentials()) {
        self.config = config
        self.credentials = credentials
    }
}

/// api.AccountAddResult.
public struct AccountAddResult: Codable, Sendable, Equatable {
    public var accountId: AccountID

    public init(accountId: AccountID) {
        self.accountId = accountId
    }
}

/// api.AccountRemoveParams. `deleteLocalData` also purges the account's
/// drafts and attachments; the mail cache always goes.
public struct AccountRemoveParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var deleteLocalData: Bool

    public init(accountId: AccountID, deleteLocalData: Bool) {
        self.accountId = accountId
        self.deleteLocalData = deleteLocalData
    }
}

/// api.AccountSetEnabledParams: pause (false) or resume (true) an account.
public struct AccountSetEnabledParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var enabled: Bool

    public init(accountId: AccountID, enabled: Bool) {
        self.accountId = accountId
        self.enabled = enabled
    }
}

/// api.AccountReorderParams: the listed accounts take the head in this
/// order; the rest keep their relative order behind them.
public struct AccountReorderParams: Codable, Sendable, Equatable {
    public var accountIds: [AccountID]

    public init(accountIds: [AccountID]) {
        self.accountIds = accountIds
    }
}

/// api.AccountDiscoverParams: only the domain leaves the machine for ISPDB
/// and DNS; the provider's own autoconfig URL receives the address.
public struct AccountDiscoverParams: Codable, Sendable, Equatable {
    public var email: String

    public init(email: String) {
        self.email = email
    }
}

/// api.AccountDiscoverResult: a suggestion only; nothing is stored or
/// authenticated. `config` is absent for source `none`. With `provider` it
/// is the primary way to add the address: the GNOME Online Accounts hint
/// (without `goaAccountId`, does not pass `account.add`) where GNOME Online
/// Accounts runs, the daemon's own sign-in (source daemon) elsewhere.
/// `alternatives` are further ways, in the daemon's order of preference:
/// the own sign-in when it is not `config`, and for Google an IMAP/SMTP
/// account with an app password.
public struct AccountDiscoverResult: Codable, Sendable, Equatable {
    public var config: AccountConfig?
    public var source: DiscoverSource
    /// Display-only, untrusted text.
    public var providerName: String?
    @NullAsEmpty public var alternatives: [AccountConfig]

    public init(
        config: AccountConfig? = nil, source: DiscoverSource, providerName: String? = nil,
        alternatives: [AccountConfig] = []
    ) {
        self.config = config
        self.source = source
        self.providerName = providerName
        self.alternatives = alternatives
    }
}

/// api.LinkedAccount: an account another desktop service is signed in to.
/// `name` and `email` are untrusted text from the service.
public struct LinkedAccount: Codable, Sendable, Equatable {
    public var provider: LinkedProvider
    public var email: String
    public var name: String?
    public var goaAccountId: String
    /// A Malachi account with that address exists already.
    public var configured: Bool
    /// The service wants the user to sign in again.
    public var attentionNeeded: Bool
    /// The account to add, complete; passes `account.add` without credentials.
    public var config: AccountConfig?

    public init(
        provider: LinkedProvider, email: String, name: String? = nil, goaAccountId: String,
        configured: Bool, attentionNeeded: Bool, config: AccountConfig? = nil
    ) {
        self.provider = provider
        self.email = email
        self.name = name
        self.goaAccountId = goaAccountId
        self.configured = configured
        self.attentionNeeded = attentionNeeded
        self.config = config
    }
}

/// api.AccountLinkedResult. Empty, not an error, without GNOME Online
/// Accounts (always on macOS).
public struct AccountLinkedResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var accounts: [LinkedAccount]

    public init(accounts: [LinkedAccount]) {
        self.accounts = accounts
    }
}

/// api.OAuthBrowserPage: the texts of the page the browser shows after the
/// provider redirects back to the daemon, in the user's language. Plain
/// text, at most 200 characters each, escaped by the daemon.
public struct OAuthBrowserPage: Codable, Sendable, Equatable {
    public var successTitle: String?
    public var successText: String?
    public var failureTitle: String?
    public var failureText: String?

    public init(successTitle: String? = nil, successText: String? = nil, failureTitle: String? = nil, failureText: String? = nil) {
        self.successTitle = successTitle
        self.successText = successText
        self.failureTitle = failureTitle
        self.failureText = failureText
    }
}

/// api.AccountOAuthStartParams: the daemon's own sign-in for a new account
/// (`config`, source daemon) or to sign an existing daemon account in again
/// (`accountId`); exactly one is set.
public struct AccountOAuthStartParams: Codable, Sendable, Equatable {
    public var accountId: AccountID?
    public var config: AccountConfig?
    public var browserPage: OAuthBrowserPage?

    public init(accountId: AccountID? = nil, config: AccountConfig? = nil, browserPage: OAuthBrowserPage? = nil) {
        self.accountId = accountId
        self.config = config
        self.browserPage = browserPage
    }
}

/// api.AccountOAuthStartResult: the UI opens `authUrl` in the browser; the
/// daemon listens for the redirect on 127.0.0.1 until `expiresAt`.
public struct AccountOAuthStartResult: Codable, Sendable, Equatable {
    public var sessionId: String
    public var authUrl: String
    public var expiresAt: Date

    public init(sessionId: String, authUrl: String, expiresAt: Date) {
        self.sessionId = sessionId
        self.authUrl = authUrl
        self.expiresAt = expiresAt
    }
}

/// api.AccountOAuthWaitParams.
public struct AccountOAuthWaitParams: Codable, Sendable, Equatable {
    public var sessionId: String

    public init(sessionId: String) {
        self.sessionId = sessionId
    }
}

/// api.AccountOAuthWaitResult: with `complete`, `config` is the account to
/// pass to account.test / account.add with `credentials.oauthSession` (for
/// a re-sign-in, the account's own).
public struct AccountOAuthWaitResult: Codable, Sendable, Equatable {
    public var status: OAuthSessionStatus
    public var config: AccountConfig?

    public init(status: OAuthSessionStatus, config: AccountConfig? = nil) {
        self.status = status
        self.config = config
    }
}

/// api.AccountOAuthCancelParams.
public struct AccountOAuthCancelParams: Codable, Sendable, Equatable {
    public var sessionId: String

    public init(sessionId: String) {
        self.sessionId = sessionId
    }
}

/// api.AccountUpdateParams: replaces the configuration; an empty password
/// keeps the stored one; `enabled` is not touched.
public struct AccountUpdateParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var config: AccountConfig
    public var credentials: Credentials

    public init(accountId: AccountID, config: AccountConfig, credentials: Credentials = Credentials()) {
        self.accountId = accountId
        self.config = config
        self.credentials = credentials
    }
}

/// api.AccountTestParams: with `accountId` and no password, the stored
/// password of that account is used; for a daemon sign-in the token of
/// `credentials.oauthSession` (read, not consumed) or, with `accountId`,
/// the stored sign-in.
public struct AccountTestParams: Codable, Sendable, Equatable {
    public var accountId: AccountID?
    public var config: AccountConfig
    public var credentials: Credentials

    public init(accountId: AccountID? = nil, config: AccountConfig, credentials: Credentials = Credentials()) {
        self.accountId = accountId
        self.config = config
        self.credentials = credentials
    }
}

/// api.EndpointTestResult: one endpoint's outcome; `error` set on failure.
public struct EndpointTestResult: Codable, Sendable, Equatable {
    public var ok: Bool
    public var error: RPCError?
    public var capabilities: [String]?
    public var latencyMs: Int

    public init(ok: Bool, error: RPCError? = nil, capabilities: [String]? = nil, latencyMs: Int) {
        self.ok = ok
        self.error = error
        self.capabilities = capabilities
        self.latencyMs = latencyMs
    }
}

/// api.AccountTestResult: `imap` and `smtp` for an IMAP account, `graph`
/// for a Graph account.
public struct AccountTestResult: Codable, Sendable, Equatable {
    public var imap: EndpointTestResult?
    public var smtp: EndpointTestResult?
    public var graph: EndpointTestResult?

    public init(imap: EndpointTestResult? = nil, smtp: EndpointTestResult? = nil, graph: EndpointTestResult? = nil) {
        self.imap = imap
        self.smtp = smtp
        self.graph = graph
    }
}
