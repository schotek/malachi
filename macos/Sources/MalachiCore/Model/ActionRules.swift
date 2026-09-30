// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The pure rules of the per-message actions (ui/internal/window/actions.go):
// the flag pair of a toggle and which actions the selected row allows, as
// far as its account offers them (`Capabilities`: a Jira account has no
// Reply, Forward or Trash of its own).

/// The message.flag set / clear pair that turns `f` on or off (actions.go
/// `flagChange`). The absent half is nil, as the wire omits it.
public func flagChange(_ f: Flag, on: Bool) -> (set: [Flag]?, clear: [Flag]?) {
    on ? ([f], nil) : (nil, [f])
}

/// A message action that an account may not offer at all
/// (`Capabilities.supported`): the client hides it where it can and
/// disables it otherwise. Seen and flagged work on every account and are
/// not among them; `move` has no control on the Mac yet.
public enum MessageActionKind: Sendable, Hashable, CaseIterable {
    case reply, replyAll, forward, trash, move, archive, junk
}

/// What the per-message header buttons and actions allow for the selected
/// row (actions.go `setMessageActionsSensitive`).
public struct ActionFlags: Sendable, Equatable {
    /// Something is selected and the actions are on at all.
    public var on: Bool
    /// The row is a queued outgoing message: the trash button cancels the
    /// send (`trashTooltip(outbox:)`), and flags and moves are refused.
    public var outbox: Bool
    /// The state the star button shows: a conversation row is flagged when
    /// any member is.
    public var flagged: Bool

    public var reply: Bool
    public var replyAll: Bool
    public var forward: Bool
    /// The star button.
    public var star: Bool
    public var trash: Bool
    public var archive: Bool
    public var junk: Bool
    public var markRead: Bool
    public var markUnread: Bool
    public var toggleFlag: Bool
    public var loadImages: Bool
    public var trustSender: Bool
    /// Reply writes a comment on the issue (the account has the comment
    /// capability): the client labels it "Comment" (`Jira.replyLabel`).
    public var comment: Bool
    /// The actions the account does not offer at all, whatever is selected
    /// (`Capabilities.supported`); with nothing selected, those of the
    /// listed folder's account. Empty for a mail account.
    public var unsupported: Set<MessageActionKind>

    public init(
        on: Bool = false, outbox: Bool = false, flagged: Bool = false, reply: Bool = false, replyAll: Bool = false,
        forward: Bool = false, star: Bool = false, trash: Bool = false, archive: Bool = false, junk: Bool = false,
        markRead: Bool = false, markUnread: Bool = false, toggleFlag: Bool = false, loadImages: Bool = false,
        trustSender: Bool = false, comment: Bool = false, unsupported: Set<MessageActionKind> = []
    ) {
        self.on = on
        self.outbox = outbox
        self.flagged = flagged
        self.reply = reply
        self.replyAll = replyAll
        self.forward = forward
        self.star = star
        self.trash = trash
        self.archive = archive
        self.junk = junk
        self.markRead = markRead
        self.markUnread = markUnread
        self.toggleFlag = toggleFlag
        self.loadImages = loadImages
        self.trustSender = trustSender
        self.comment = comment
        self.unsupported = unsupported
    }

    /// Everything off (nothing selected).
    public static let none = ActionFlags()
}

/// The actions the selected row allows (actions.go
/// `setMessageActionsSensitive`): archive and junk only when the account
/// has such a folder and the message is not in it already, mark read /
/// unread according to the seen flag; the star shows the flagged state. A
/// conversation row is read when every member is, flagged when any is. An
/// outbox message keeps only reply, forward and trash (which cancels the
/// send; the daemon refuses flags and moves). Reply, Reply All, Forward,
/// Trash, Archive and Junk need the account's capabilities as well
/// (`Capabilities.available`: a Jira account has none of them in M1, and
/// comments instead of replies later); what the account lacks altogether
/// is `unsupported`. With `on` false (or no row) everything is off, and
/// `unsupported` and `comment` are those of the listed folder's account.
public func messageActionState(_ row: ListRow?, on: Bool = true, model: MailModel) -> ActionFlags {
    guard on, let row else {
        return idleActionState(model)
    }
    let s = row.message
    let outbox = model.inOutbox(s)
    var flagged = hasFlag(s.flags, .flagged)
    var seen = hasFlag(s.flags, .seen)
    var unread = !seen
    if row.thread, let summary = row.summary {
        flagged = hasFlag(summary.flags, .flagged)
        unread = summary.unreadCount > 0
        seen = summary.unreadCount < summary.messageCount
    }
    let situation = Capabilities.Situation(
        account: model.account(s.accountId), selected: true, outbox: outbox,
        archive: model.canMoveToRole(s, .archive), junk: model.canMoveToRole(s, .junk),
        composeAccount: !Capabilities.forwardAccounts(model.accounts).isEmpty
    )
    let supported = Capabilities.supported(situation)
    let available = Capabilities.available(situation)
    return ActionFlags(
        on: true, outbox: outbox, flagged: flagged,
        reply: available.reply, replyAll: available.replyAll, forward: available.forward,
        star: !outbox,
        trash: available.trash,
        archive: available.archive,
        junk: available.junk,
        markRead: !outbox && unread,
        markUnread: !outbox && seen,
        toggleFlag: !outbox,
        loadImages: true,
        trustSender: !outbox,
        comment: supported.comment,
        unsupported: unsupportedActions(supported)
    )
}

/// The flags with nothing selected: every action off, and what the listed
/// folder's account offers at all (none listed: the mail default), so the
/// toolbar of a Jira folder does not show Reply before a row is chosen.
private func idleActionState(_ model: MailModel) -> ActionFlags {
    let k = model.listFolder
    let situation = Capabilities.Situation(
        account: k.flatMap { model.account($0.account) }, selected: false,
        outbox: k.map { model.folderRole($0) == .outbox } ?? false,
        composeAccount: !Capabilities.forwardAccounts(model.accounts).isEmpty
    )
    let supported = Capabilities.supported(situation)
    return ActionFlags(comment: supported.comment, unsupported: unsupportedActions(supported))
}

/// The actions `supported` leaves out.
private func unsupportedActions(_ supported: Capabilities.Actions) -> Set<MessageActionKind> {
    var out = Set<MessageActionKind>()
    let offered: [(MessageActionKind, Bool)] = [
        (.reply, supported.reply), (.replyAll, supported.replyAll), (.forward, supported.forward),
        (.trash, supported.trash), (.move, supported.move), (.archive, supported.archive), (.junk, supported.junk),
    ]
    for (kind, on) in offered where !on {
        out.insert(kind)
    }
    return out
}
