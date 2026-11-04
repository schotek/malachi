// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The message list's selection of several rows (ui/internal/selection,
/// ported 1:1): what the window's actions allow for it (`combine`), that it
/// stays within one account (`restrict`), what it becomes when the list
/// changes under it (`reconcile`), and its texts. One row is the row's own
/// flags, unchanged.
public enum Selection {
    /// selection.MaxOpen: the most rows Open opens in windows at once.
    public static let maxOpen = 10
    /// selection.MaxForward: the most rows Forward takes into one message
    /// (api.MaxForwardMessages).
    public static let maxForward = 20

    /// selection.Combine over the window's flags of each selected row (one
    /// account). One row is that row's flags; several:
    /// - Mark as Read when any row is unread, Mark as Unread when any is
    ///   read; the star stars them all unless every row is starred, which
    ///   unstars them all (`flagged`: every row starred);
    /// - Archive and Junk when any row can go there; Trash, Move To and the
    ///   star only when every row allows them;
    /// - Forward forwards them all in one message, up to `maxForward`;
    ///   Open opens up to `maxOpen` windows;
    /// - Save to eWay-CRM when every row can be saved, up to
    ///   `API.Limits.maxCrmMessages` rows, as one save (never the
    ///   saved-state items);
    /// - Reply, Reply All, Load Images and Trust Sender are for one
    ///   message, and off;
    /// - in the outbox Trash cancels the sending of them all, and nothing
    ///   else goes.
    /// What the account offers (`unsupported`) and Comment are the first
    /// row's.
    public static func combine(_ rows: [ActionFlags]) -> ActionFlags {
        switch rows.count {
        case 0:
            return ActionFlags()
        case 1:
            var f = rows[0]
            f.count = 1
            f.open = f.on
            return f
        default:
            break
        }
        let n = rows.count
        var out = ActionFlags(
            count: n, open: n <= maxOpen, on: true, outbox: true, flagged: true, forward: n <= maxForward,
            star: true, trash: true, move: true, toggleFlag: true, comment: rows[0].comment,
            unsupported: rows[0].unsupported)
        out.crm = n <= API.Limits.maxCrmMessages
        for r in rows {
            out.outbox = out.outbox && r.outbox
            out.flagged = out.flagged && r.flagged
            out.forward = out.forward && r.forward
            out.star = out.star && r.star
            out.toggleFlag = out.toggleFlag && r.toggleFlag
            out.trash = out.trash && r.trash
            out.move = out.move && r.move
            out.crm = out.crm && r.crm
            out.archive = out.archive || r.archive
            out.junk = out.junk || r.junk
            out.markRead = out.markRead || r.markRead
            out.markUnread = out.markUnread || r.markUnread
        }
        if out.outbox {
            out.forward = false
            out.star = false
            out.toggleFlag = false
            out.move = false
            out.archive = false
            out.junk = false
            out.markRead = false
            out.markUnread = false
            out.crm = false
        }
        return out
    }

    /// selection.CombineAccounts: `combine` for the rows of a list that may
    /// hold several accounts (the All Inboxes list, `Unified`); `accounts`
    /// is the account of each row, index for index. When they are of more
    /// than one account the selection stays (decision U2) and the actions
    /// that run one call per account (read, star, Archive, Junk, Trash,
    /// Open, the Assistant) keep `combine`'s verdict, while those that need
    /// one account are off: Forward (one message from one account), Move To
    /// (the folder picker is one account's folders) and Save to eWay-CRM
    /// (one account's messages per save); `mixedAccounts` says so. Rows of
    /// one account are `combine`'s.
    public static func combineAccounts(_ rows: [ActionFlags], accounts: [AccountID]) -> ActionFlags {
        var out = combine(rows)
        guard rows.count >= 2, mixed(accounts) else { return out }
        out.mixedAccounts = true
        out.forward = false
        out.move = false
        out.crm = false
        return out
    }

    /// selection.mixed: more than one distinct account.
    private static func mixed(_ accounts: [AccountID]) -> Bool {
        guard let first = accounts.first else { return false }
        return accounts.dropFirst().contains { $0 != first }
    }

    /// selection.Mixed: whether the selected rows belong to more than one
    /// account.
    public static func mixed<K>(_ rows: [(key: K, account: AccountID)]) -> Bool {
        mixed(rows.map(\.account))
    }

    /// selection.FlagTarget: true stars the selected rows, false unstars
    /// them.
    public static func flagTarget(_ f: ActionFlags) -> Bool {
        !f.flagged
    }

    /// selection.Restrict: the selection within the account of `latest`
    /// (the row the user added last); rows in list order, which the result
    /// keeps. `latest` not among them: nothing changes.
    public static func restrict<K: Hashable>(_ rows: [(key: K, account: AccountID)], latest: K) -> [K] {
        guard let account = rows.first(where: { $0.key == latest })?.account else {
            return rows.map(\.key)
        }
        return rows.filter { $0.account == account }.map(\.key)
    }

    /// selection.RestrictIn: `restrict` in a list that may or may not span
    /// accounts: in the All Inboxes list (`crossAccount`) the selection may
    /// hold rows of every account and nothing leaves it (decision U2); in
    /// any other list it is `restrict`.
    public static func restrictIn<K: Hashable>(
        _ rows: [(key: K, account: AccountID)], latest: K, crossAccount: Bool
    ) -> [K] {
        crossAccount ? rows.map(\.key) : restrict(rows, latest: latest)
    }

    /// selection.Reconcile: each previous key resolved to what the list
    /// shows it as now (itself; a member row folded away as its
    /// conversation row) or gone, in order, without duplicates; when
    /// nothing is left and `neighbour` is given, the row it names.
    public static func reconcile<K: Hashable>(
        _ prev: [K], resolve: (K) -> K?, neighbour: (() -> K?)? = nil
    ) -> [K] {
        var seen = Set<K>()
        var out: [K] = []
        for k in prev {
            guard let now = resolve(k), !seen.contains(now) else { continue }
            seen.insert(now)
            out.append(now)
        }
        if out.isEmpty, !prev.isEmpty, let neighbour, let k = neighbour() {
            out.append(k)
        }
        return out
    }

    /// selection.SelectedText: the reading pane while `n` rows are
    /// selected.
    public static func selectedText(_ n: Int) -> String {
        // TRANSLATORS: The reading pane while several messages are selected in the list; %d is their number.
        L10n.N("%d message selected", "%d messages selected", n)
    }

    /// selection.CancelSendingQuestion: before Trash cancels the sending of
    /// `n` queued messages.
    public static func cancelSendingQuestion(_ n: Int) -> String {
        // TRANSLATORS: The question before the sending of several queued messages is cancelled; %d is their number.
        L10n.N("Cancel sending %d message?", "Cancel sending %d messages?", n)
    }
}

extension MailModel {
    /// The messages of several selected rows newest first, as the
    /// Assistant takes them (selection.go `newestFirstIDs`); one unknown
    /// here goes last.
    public func newestFirst(_ ids: [MessageID]) -> [MessageID] {
        ids.enumerated().sorted { a, b in
            let da = message(a.element)?.summary.date ?? .distantPast
            let db = message(b.element)?.summary.date ?? .distantPast
            return da != db ? da > db : a.offset < b.offset
        }.map(\.element)
    }
}
