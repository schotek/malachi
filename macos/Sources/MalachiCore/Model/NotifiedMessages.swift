// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// Bounds the desktop notifications the app remembers (ui/internal/window/
/// notified.go `notifiedMax`). The oldest one is withdrawn when a newer one
/// pushes it out: once forgotten it could never be withdrawn again.
public let notifiedMax = 50

/// The notification request identifier of message `id`'s notification
/// (notified.go `notificationID`).
public func notificationID(_ id: MessageID) -> String {
    "message-" + id.rawValue
}

/// One notified message and the folder it arrived in (notified.go
/// `notifiedEntry`).
public struct NotifiedEntry: Equatable, Sendable {
    public var id: MessageID
    public var key: FolderKey

    public init(id: MessageID, key: FolderKey) {
        self.id = id
        self.key = key
    }
}

/// The messages the app posted a desktop notification for that may still
/// show in Notification Center, oldest first, each with the folder it was
/// notified in (notified.go `notifiedSet`). Only messages in it are ever
/// withdrawn (`MailboxController.withdrawNotifications`), so the app never
/// withdraws blindly.
public struct NotifiedMessages: Sendable {
    public private(set) var entries: [NotifiedEntry] = []

    public init() {}

    /// Records the notification of message `id` in folder `k` as the
    /// newest; a message notified twice keeps one entry. Returns the
    /// messages pushed out beyond `notifiedMax`, oldest first, for the
    /// caller to withdraw.
    @discardableResult
    public mutating func add(_ id: MessageID, _ k: FolderKey) -> [MessageID] {
        drop { $0.id == id }
        entries.append(NotifiedEntry(id: id, key: k))
        let over = entries.count - notifiedMax
        guard over > 0 else { return [] }
        let evicted = entries.prefix(over).map(\.id)
        entries.removeFirst(over)
        return evicted
    }

    /// Forgets the given messages and returns those it held.
    @discardableResult
    public mutating func remove(_ ids: [MessageID]) -> [MessageID] {
        guard !entries.isEmpty else { return [] }
        let want = Set(ids)
        return drop { want.contains($0.id) }
    }

    /// Forgets the messages notified in folder `k` and returns them.
    @discardableResult
    public mutating func removeFolder(_ k: FolderKey) -> [MessageID] {
        drop { $0.key == k }
    }

    /// Forgets the messages of every account not in `keep` and returns
    /// them.
    @discardableResult
    public mutating func removeAccounts(except keep: Set<AccountID>) -> [MessageID] {
        drop { !keep.contains($0.key.account) }
    }

    /// The entries of account `acc`, oldest first.
    public func ofAccount(_ acc: AccountID) -> [NotifiedEntry] {
        entries.filter { $0.key.account == acc }
    }

    /// Removes the entries `match` selects and returns their messages, in
    /// order.
    @discardableResult
    private mutating func drop(_ match: (NotifiedEntry) -> Bool) -> [MessageID] {
        var kept: [NotifiedEntry] = []
        var removed: [MessageID] = []
        for e in entries {
            if match(e) {
                removed.append(e.id)
            } else {
                kept.append(e)
            }
        }
        entries = kept
        return removed
    }
}

/// Reads message.get's answer about a notified message (notified.go
/// `notificationOutdated`): true when the message was read or has left the
/// folder it was notified in, or when the daemon no longer has the message
/// or its account; false when it is still unread where it arrived; nil when
/// the answer tells nothing (no connection, a timeout, a storage error),
/// and the notification then stays.
public func notificationOutdated(_ e: NotifiedEntry, _ answer: Result<MessageSummary, any Error>) -> Bool? {
    switch answer {
    case .success(let m):
        let moved = !m.folderId.rawValue.isEmpty && m.folderId != e.key.folder
        return moved || hasFlag(m.flags, .seen)
    case .failure(let err):
        if let rpc = err as? RPCError, rpc.code == .messageNotFound || rpc.code == .accountNotFound {
            return true
        }
        return nil
    }
}
