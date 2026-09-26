// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The outbox (ui/internal/window/outbox.go), the pure parts: messages
// queued for sending live in the account's outbox folder, carry their
// delivery state in `MessageSummary.outbox` and are counted by
// `SyncState.pendingOutbox` (queued or sending) and `failedOutbox`. The
// daemon owns the queue: nothing here decides when or whether a message
// goes out.

/// The banner for a delivery state (outbox.go `outboxBannerText`): the
/// title, the button label ("" for no button) and whether the banner shows
/// at all. A message that is not in the outbox, or already delivered, has
/// none.
public func outboxBannerText(_ o: OutboxInfo?) -> (title: String, button: String, shown: Bool) {
    guard let o else {
        return ("", "", false)
    }
    switch o.state {
    case .queued:
        return (L10n.T("Queued for sending"), "", true)
    case .sending:
        return (L10n.T("Sending…"), "", true)
    case .failed:
        return (rpcErrorText(L10n.T("Sending the message"), o.error), L10n.T("Retry"), true)
    default:
        return ("", "", false)
    }
}

/// The trash button's tooltip: for an outbox message the button cancels the
/// send instead (outbox.go `trashTooltip`).
public func trashTooltip(outbox: Bool) -> String {
    outbox ? L10n.T("Cancel Sending") : L10n.T("Move to Trash")
}

/// The bookkeeping of outbox.go `trackOutbox`: after every folder.list of
/// an account, a shrink of the outbox folder that the user did not cause by
/// cancelling means messages were delivered and their copy filed in Sent
/// (the daemon removes an outbox message only then, or right after delivery
/// when the account has no Sent folder; a failed send keeps it). Those get
/// a toast. A cancel is used up only by a shrink it explains: a reload that
/// lands between counting it and the daemon's delete leaves it for the
/// reload that sees the drop.
public struct OutboxTracker: Sendable, Equatable {
    /// The outbox total last seen per account.
    private var seen: [AccountID: Int]
    /// Removals the user asked for since the last look, not deliveries.
    private var cancelled: [AccountID: Int]

    public init() {
        seen = [:]
        cancelled = [:]
    }

    /// Records the outbox folder's current `total` (0 when the account has
    /// no outbox folder) and returns how many messages were delivered since
    /// the last call, 0 on the first look or when nothing left.
    public mutating func track(_ acc: AccountID, total: Int) -> Int {
        let previous = seen[acc]
        seen[acc] = total
        guard let previous else {
            return 0
        }
        let shrink = max(previous - total, 0)
        let explained = min(cancelled[acc] ?? 0, shrink)
        cancelled[acc] = (cancelled[acc] ?? 0) - explained
        return shrink - explained
    }

    /// Notes that the user removed one outbox message of `acc` (cancel
    /// sending), so the next shrink is not counted as a delivery.
    public mutating func noteCancelled(_ acc: AccountID) {
        cancelled[acc, default: 0] += 1
    }

    /// Takes one `noteCancelled` of `acc` back: the daemon refused the
    /// removal (outbox.go `cancelSendFrom`). Never below zero, since a
    /// folder reload in between may have used the note up already.
    public mutating func noteCancelFailed(_ acc: AccountID) {
        if let n = cancelled[acc], n > 0 {
            cancelled[acc] = n - 1
        }
    }
}
