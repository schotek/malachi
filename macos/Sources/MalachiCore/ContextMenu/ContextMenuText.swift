// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The texts of the context menus (ui/internal/contextmenu text.go), with
// the GTK msgids as keys.

extension ContextMenu {
    /// contextmenu.Labels: the states that choose an item's label.
    public struct Labels: Sendable, Equatable {
        public var comment = false
        public var flagged = false
        public var outbox = false
        public var junk = false
        public var favourite = false
        public var collapsed = false
        /// Open opens, and Save to eWay-CRM saves, this many rows (2 or
        /// more).
        public var count = 0

        public init(comment: Bool = false, flagged: Bool = false, outbox: Bool = false, junk: Bool = false,
                    favourite: Bool = false, collapsed: Bool = false, count: Int = 0) {
            self.comment = comment
            self.flagged = flagged
            self.outbox = outbox
            self.junk = junk
            self.favourite = favourite
            self.collapsed = collapsed
            self.count = count
        }
    }

    /// contextmenu.Label: an item's text, some with a mnemonic underscore
    /// (`mn` takes it off).
    public static func label(_ id: ItemID, _ l: Labels = Labels()) -> String {
        switch id {
        case .openMessage:
            l.count > 1
                // TRANSLATORS: In the menu of several selected messages: opens each in a window of its own; %d is their number.
                ? L10n.N("Open %d Message in a New Window", "Open %d Messages in New Windows", l.count)
                // TRANSLATORS: In the menu of a message in the list: opens it in a window of its own.
                : L10n.T("Open in New Window")
        case .editDraft: L10n.T("Edit")
        case .reply: l.comment ? L10n.T("Comment") : L10n.T("Reply")
        case .replyAll: L10n.T("Reply All")
        case .forward: L10n.T("Forward")
        case .markRead: L10n.T("Mark as _Read")
        case .markUnread: L10n.T("Mark as _Unread")
        case .toggleFlag: l.flagged ? L10n.T("Unstar") : L10n.T("Star")
        case .changeStatus: L10n.T("Change Status")
        case .archive: L10n.T("Archive")
        case .moveTo: L10n.T("Move To")
        case .junk: L10n.T("Mark as Junk")
        case .trash: l.outbox ? L10n.T("Cancel Sending") : L10n.T("Move to Trash")
        case .assistant: L10n.T("Assistant")
        case .crmSave: CRM.saveMenuLabel(l.count)
        case .crmOpen: CRM.menuLabel(.open)
        case .crmEditLinks: CRM.menuLabel(.editLinks)
        case .crmRemove: CRM.menuLabel(.remove)
        case .checkMail: L10n.T("Check for New Mail")
        case .markAllRead:
            // TRANSLATORS: In the menu of a folder in the sidebar.
            L10n.T("Mark All as Read")
        case .emptyFolder:
            // TRANSLATORS: In the menu of the Trash or Junk folder: deletes its messages for good.
            l.junk ? L10n.T("Empty Junk") : L10n.T("Empty Trash")
        case .favourite: l.favourite ? L10n.T("Remove from Favourites") : L10n.T("Add to Favourites")
        case .fold: l.collapsed ? L10n.T("Expand") : L10n.T("Collapse")
        }
    }

    /// contextmenu.Question: a confirmation's heading, text and the label
    /// of the button that goes ahead.
    public struct Question: Sendable, Equatable {
        public var heading: String
        public var body: String
        public var confirm: String
    }

    /// contextmenu.EmptyQuestion: before Empty Trash or Empty Junk deletes
    /// the `count` messages the folder shows, for good; older messages kept
    /// on the server only stay there.
    public static func emptyQuestion(junk: Bool, count n: Int) -> Question {
        Question(
            heading: junk ? L10n.T("Empty the Junk Folder?") : L10n.T("Empty the Trash?"),
            // TRANSLATORS: %d is the number of messages the folder shows.
            body: L10n.N("%d message will be deleted for good. Older messages that are only on the mail server stay there.",
                         "%d messages will be deleted for good. Older messages that are only on the mail server stay there.", n),
            confirm: L10n.T("_Empty")
        )
    }

    /// contextmenu.MovingText: Move To in an error toast.
    public static func movingText(_ n: Int) -> String {
        n == 1 ? L10n.T("Moving the message") : L10n.N("Moving %d message", "Moving %d messages", n)
    }

    /// contextmenu.MarkingReadText: Mark All as Read in an error toast.
    public static func markingReadText() -> String {
        L10n.T("Marking the folder as read")
    }

    /// contextmenu.EmptyingText: Empty Trash or Empty Junk in an error toast.
    public static func emptyingText(junk: Bool) -> String {
        junk ? L10n.T("Emptying the Junk folder") : L10n.T("Emptying the Trash")
    }
}
