// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Notifications, daemon to client (docs/api.md §5; types.go
// "Notifications"). Best-effort: a client that misses one resynchronises
// through sync.status, folder.list and message.list.

import Foundation

/// api.NewMessageNotification: one message that arrived after a folder's
/// initial synchronisation, once its body is stored.
public struct NewMessageNotification: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var folderId: FolderID
    public var message: MessageSummary

    public init(accountId: AccountID, folderId: FolderID, message: MessageSummary) {
        self.accountId = accountId
        self.folderId = folderId
        self.message = message
    }
}

/// api.SyncStateNotification.
public struct SyncStateNotification: Codable, Sendable, Equatable {
    public var state: SyncState

    public init(state: SyncState) {
        self.state = state
    }
}

/// api.AuthRequiredNotification: the daemon cannot proceed without the user
/// (wrong password, expired token, missing keyring). `message` is technical
/// English, not for display verbatim.
public struct AuthRequiredNotification: Codable, Sendable, Equatable {
    public var accountId: AccountID
    /// authRequired, authFailed or keyringError.
    public var reason: ErrorCode
    public var message: String
    /// Set for an OAuth2 flow the daemon runs itself (reserved).
    public var authUrl: String?

    public init(accountId: AccountID, reason: ErrorCode, message: String, authUrl: String? = nil) {
        self.accountId = accountId
        self.reason = reason
        self.message = message
        self.authUrl = authUrl
    }
}

/// api.AccountsChangedNotification: no payload; clients re-run account.list.
public struct AccountsChangedNotification: Codable, Sendable, Equatable {
    public init() {}
}

/// A decoded notification. `unknown` carries the method of one this client
/// does not know (a newer daemon); the raw notification is still available
/// from the transport for a caller that wants it.
public enum DaemonNotification: Sendable, Equatable {
    case newMessage(NewMessageNotification)
    case syncState(SyncState)
    case authRequired(AuthRequiredNotification)
    case accountsChanged
    case unknown(method: String)

    /// Decodes the params of a raw notification by its method. Throws when
    /// a known method carries params of the wrong shape.
    public init(_ raw: RPCNotification) throws {
        switch raw.method {
        case API.Notify.newMessage:
            self = .newMessage(try raw.params(NewMessageNotification.self))
        case API.Notify.syncState:
            self = .syncState(try raw.params(SyncStateNotification.self).state)
        case API.Notify.authRequired:
            self = .authRequired(try raw.params(AuthRequiredNotification.self))
        case API.Notify.accountsChanged:
            self = .accountsChanged
        default:
            self = .unknown(method: raw.method)
        }
    }
}
