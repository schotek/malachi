// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// What the right-click menus of the message list and of the sidebar
/// offer, in all three clients (ui/internal/contextmenu, ported 1:1): which
/// items, in which groups, and which of them are enabled. A right click on
/// a message selects it first, so the message menu acts on the selection
/// through the window's own actions; the sidebar's menu acts on the folder
/// or account clicked and leaves the selection alone. The views map the
/// item ids to their actions and put a separator between two groups.
public enum ContextMenu {
    /// contextmenu.ItemID (the Go ids "open", "edit" and "empty" are the
    /// case names here).
    public enum ItemID: String, Sendable, Hashable, CaseIterable {
        // The message menu.
        /// Opens the message in a window of its own, as a double click
        /// does; `edit` is the same for a message of the Drafts folder,
        /// which opens in the compose window.
        case openMessage
        case editDraft
        /// Comment on an issue tracker's account (`Labels.comment`).
        case reply
        case replyAll
        case forward
        /// Only when they apply; a conversation with read and unread
        /// members has both.
        case markRead
        case markUnread
        /// Star or Unstar (`Labels.flagged`).
        case toggleFlag
        /// The submenu of an issue's transitions.
        case changeStatus
        case archive
        /// The submenu of the account's folders (`moveTargets`).
        case moveTo
        case junk
        /// Move to Trash, or Cancel Sending in the outbox (`Labels.outbox`).
        case trash
        /// The submenu of the Assistant's actions on the message.
        case assistant
        // The sidebar's menus.
        /// Synchronises the folder clicked, or the account from its heading.
        case checkMail
        /// folder.markRead.
        case markAllRead
        /// folder.empty: Empty Trash or Empty Junk (`Labels.junk`), always
        /// after `emptyQuestion`.
        case emptyFolder
        /// Add to or Remove from Favourites (`Labels.favourite`).
        case favourite
        /// Expand or Collapse (`Labels.collapsed`).
        case fold
    }

    /// contextmenu.Item.
    public struct Item: Sendable, Equatable {
        public var id: ItemID
        public var enabled: Bool

        public init(_ id: ItemID, _ enabled: Bool) {
            self.id = id
            self.enabled = enabled
        }
    }

    /// contextmenu.MessageState: what the window knows of the message row
    /// clicked (by then the selected one).
    public struct MessageState: Sendable, Equatable {
        public var draft = false
        public var outbox = false
        /// What the account offers at all (`Capabilities.supported`, the
        /// outbox always offering Trash): the others are left out.
        public var offerReply = false, offerReplyAll = false, offerForward = false
        public var offerArchive = false, offerMove = false, offerJunk = false
        public var offerTrash = false
        /// What can be done now (the window's action rules): an offered item
        /// that cannot is disabled.
        public var reply = false, replyAll = false, forward = false
        public var markRead = false, markUnread = false
        public var toggleFlag = false
        public var archive = false, move = false, junk = false
        public var trash = false
        public var changeStatus = false
        public var assistant = false

        public init() {}

        /// The state of the window's action flags (`messageActionState`).
        public init(flags f: ActionFlags, draft: Bool, changeStatus: Bool, assistant: Bool) {
            self.draft = draft
            outbox = f.outbox
            offerReply = !f.unsupported.contains(.reply)
            offerReplyAll = !f.unsupported.contains(.replyAll)
            offerForward = !f.unsupported.contains(.forward)
            offerArchive = !f.unsupported.contains(.archive)
            offerMove = !f.unsupported.contains(.move)
            offerJunk = !f.unsupported.contains(.junk)
            offerTrash = !f.unsupported.contains(.trash)
            reply = f.reply
            replyAll = f.replyAll
            forward = f.forward
            markRead = f.markRead
            markUnread = f.markUnread
            toggleFlag = f.toggleFlag
            archive = f.archive
            move = f.move
            junk = f.junk
            trash = f.trash
            self.changeStatus = changeStatus
            self.assistant = assistant
        }
    }

    /// contextmenu.MessageItems: opening; answering; read, star and status;
    /// where the message goes; the Assistant. Empty groups are left out.
    public static func messageItems(_ s: MessageState) -> [[Item]] {
        let open = Item(s.draft ? .editDraft : .openMessage, true)
        var answer: [Item] = []
        if s.offerReply { answer.append(Item(.reply, s.reply)) }
        if s.offerReplyAll { answer.append(Item(.replyAll, s.replyAll)) }
        if s.offerForward { answer.append(Item(.forward, s.forward)) }
        var mark: [Item] = []
        if s.markRead { mark.append(Item(.markRead, true)) }
        if s.markUnread { mark.append(Item(.markUnread, true)) }
        mark.append(Item(.toggleFlag, s.toggleFlag))
        if s.changeStatus { mark.append(Item(.changeStatus, true)) }
        var move: [Item] = []
        if s.offerArchive { move.append(Item(.archive, s.archive)) }
        if s.offerMove { move.append(Item(.moveTo, s.move)) }
        if s.offerJunk { move.append(Item(.junk, s.junk)) }
        if s.offerTrash || s.outbox { move.append(Item(.trash, s.trash)) }
        let assistant = s.assistant ? [Item(.assistant, true)] : []
        return [[open], answer, mark, move, assistant].filter { !$0.isEmpty }
    }

    /// contextmenu.FolderState: what the sidebar knows of the folder
    /// clicked.
    public struct FolderState: Sendable, Equatable {
        public var selectable = false
        public var role: FolderRole = .none
        /// The folder's own counts, not the badge.
        public var unread = 0, total = 0
        public var canDelete = false
        public var foldable = false

        public init(selectable: Bool = false, role: FolderRole = .none, unread: Int = 0, total: Int = 0,
                    canDelete: Bool = false, foldable: Bool = false) {
            self.selectable = selectable
            self.role = role
            self.unread = unread
            self.total = total
            self.canDelete = canDelete
            self.foldable = foldable
        }
    }

    /// contextmenu.FolderItems: checking it; marking it read or emptying
    /// it; its place in Favourites and its folding.
    public static func folderItems(_ f: FolderState) -> [[Item]] {
        let holds = f.selectable && f.role != .outbox
        var check: [Item] = [], act: [Item] = [], place: [Item] = []
        if holds {
            check.append(Item(.checkMail, true))
            act.append(Item(.markAllRead, f.unread > 0))
        }
        if f.selectable, f.role == .trash || f.role == .junk, f.canDelete {
            act.append(Item(.emptyFolder, f.total > 0))
        }
        if f.selectable { place.append(Item(.favourite, true)) }
        if f.foldable { place.append(Item(.fold, true)) }
        return [check, act, place].filter { !$0.isEmpty }
    }

    /// contextmenu.AccountItems: checking the account, and its folding when
    /// the heading folds.
    public static func accountItems(foldable: Bool) -> [[Item]] {
        [[Item(.checkMail, true)], foldable ? [Item(.fold, true)] : []].filter { !$0.isEmpty }
    }

    /// contextmenu.Entry: a folder of the account in the sidebar's order.
    public struct Entry: Sendable, Equatable {
        public var folder: Folder
        public var depth: Int

        public init(folder: Folder, depth: Int) {
            self.folder = folder
            self.depth = depth
        }
    }

    /// contextmenu.Target: an entry of the Move To submenu.
    public struct Target: Sendable, Equatable {
        public var folder: Folder
        public var depth: Int
        public var enabled: Bool
    }

    /// contextmenu.MoveTargets: everything but the outbox, the Drafts, an
    /// \All folder and an issue tracker's views; the current folder and the
    /// containers disabled, and a container left out when none of its
    /// subfolders is listed.
    public static func moveTargets(_ entries: [Entry], current: FolderID) -> [Target] {
        var kept: [Target] = []
        for e in entries {
            let f = e.folder
            if f.id.rawValue.isEmpty || f.virtual != nil { continue }
            if f.role == .outbox || f.role == .drafts || f.role == .all { continue }
            kept.append(Target(folder: f, depth: e.depth, enabled: f.selectable && f.id != current))
        }
        var out: [Target] = []
        var next = -1
        for t in kept.reversed() {
            if !t.folder.selectable && next <= t.depth { continue }
            out.append(t)
            next = t.depth
        }
        return out.reversed()
    }
}
