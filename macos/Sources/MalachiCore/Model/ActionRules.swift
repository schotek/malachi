// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The pure rules of the per-message actions (ui/internal/window/actions.go):
// the flag pair of a toggle and which actions the selected row allows.

/// The message.flag set / clear pair that turns `f` on or off (actions.go
/// `flagChange`). The absent half is nil, as the wire omits it.
public func flagChange(_ f: Flag, on: Bool) -> (set: [Flag]?, clear: [Flag]?) {
    on ? ([f], nil) : (nil, [f])
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

    public init(
        on: Bool = false, outbox: Bool = false, flagged: Bool = false, reply: Bool = false, replyAll: Bool = false,
        forward: Bool = false, star: Bool = false, trash: Bool = false, archive: Bool = false, junk: Bool = false,
        markRead: Bool = false, markUnread: Bool = false, toggleFlag: Bool = false, loadImages: Bool = false,
        trustSender: Bool = false
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
/// send; the daemon refuses flags and moves). With `on` false (or no row)
/// everything is off.
public func messageActionState(_ row: ListRow?, on: Bool = true, model: MailModel) -> ActionFlags {
    guard on, let row else {
        return .none
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
    return ActionFlags(
        on: true, outbox: outbox, flagged: flagged,
        reply: true, replyAll: true, forward: true,
        star: !outbox,
        trash: true,
        archive: !outbox && model.canMoveToRole(s, .archive),
        junk: !outbox && model.canMoveToRole(s, .junk),
        markRead: !outbox && unread,
        markUnread: !outbox && seen,
        toggleFlag: !outbox,
        loadImages: true,
        trustSender: !outbox
    )
}
