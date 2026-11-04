// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Sync (docs/api.md §3 SyncState, §4.7; types.go "Sync").

import Foundation

/// api.SyncState: the live state of one account's syncer (also
/// `Account.state` and the payload of `notify.syncState`).
public struct SyncState: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var status: SyncStatus
    /// Set while a specific folder is being synchronised.
    public var folderId: FolderID?
    /// 0–100 within the current pass, -1 otherwise.
    public var progress: Int
    /// End of the last successful pass; nil before the first.
    public var lastSync: Date?
    /// The last failure, cleared by the next successful pass.
    public var error: RPCError?
    /// Outbox messages in state queued or sending.
    public var pendingOutbox: Int
    /// Outbox messages in state failed: delivery gave up and they wait for
    /// outbox.retry or a delete. They never count in `pendingOutbox`.
    public var failedOutbox: Int

    public init(
        accountId: AccountID, status: SyncStatus, folderId: FolderID? = nil, progress: Int = -1,
        lastSync: Date? = nil, error: RPCError? = nil, pendingOutbox: Int = 0, failedOutbox: Int = 0
    ) {
        self.accountId = accountId
        self.status = status
        self.folderId = folderId
        self.progress = progress
        self.lastSync = lastSync
        self.error = error
        self.pendingOutbox = pendingOutbox
        self.failedOutbox = failedOutbox
    }

    private enum CodingKeys: String, CodingKey {
        case accountId, status, folderId, progress, lastSync, error, pendingOutbox, failedOutbox
    }

    /// The synthesised decoding would refuse a state without
    /// `failedOutbox` (a daemon from before the field); it is 0 then.
    /// Everything else decodes as before. The encoding stays synthesised
    /// and always writes the key.
    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        accountId = try c.decode(AccountID.self, forKey: .accountId)
        status = try c.decode(SyncStatus.self, forKey: .status)
        folderId = try c.decodeIfPresent(FolderID.self, forKey: .folderId)
        progress = try c.decode(Int.self, forKey: .progress)
        lastSync = try c.decodeIfPresent(Date.self, forKey: .lastSync)
        error = try c.decodeIfPresent(RPCError.self, forKey: .error)
        pendingOutbox = try c.decode(Int.self, forKey: .pendingOutbox)
        failedOutbox = try c.decodeIfPresent(Int.self, forKey: .failedOutbox) ?? 0
    }
}

/// api.SyncStatusParams. `accountId` nil = every account.
public struct SyncStatusParams: Codable, Sendable, Equatable {
    public var accountId: AccountID?

    public init(accountId: AccountID? = nil) {
        self.accountId = accountId
    }
}

/// api.SyncStatusResult: in `account.list` order, paused accounts included.
public struct SyncStatusResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var accounts: [SyncState]

    public init(accounts: [SyncState]) {
        self.accounts = accounts
    }
}

/// api.SyncTriggerParams. `accountId` nil = all, `folderId` nil = the whole
/// account, `full` forces a complete pass instead of an incremental one.
public struct SyncTriggerParams: Codable, Sendable, Equatable {
    public var accountId: AccountID?
    public var folderId: FolderID?
    public var full: Bool?

    public init(accountId: AccountID? = nil, folderId: FolderID? = nil, full: Bool? = nil) {
        self.accountId = accountId
        self.folderId = folderId
        self.full = full
    }
}
