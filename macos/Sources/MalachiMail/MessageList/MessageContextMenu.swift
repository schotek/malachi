// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The context menu of a message in the list (ui/internal/contextmenu
/// `MessageItems`; GTK context_menus.go `showMessageMenu`). A right click
/// selects the row first (`MessageListViewController`), so the menu acts on
/// the selection: its items are the window's own actions without a target,
/// which `MainWindowController` validates and titles as it does in the menu
/// bar (Comment, Unstar, Cancel Sending). Move To lists the account's
/// folders; Change Status and the Assistant are the menu bar's submenus,
/// made afresh (a menu has one supermenu), each kept alive by its item.
@MainActor
enum MessageContextMenu {
    /// The selectors of the plain items; Move To, Change Status and the
    /// Assistant are submenus.
    private static let actions: [ContextMenu.ItemID: Selector] = [
        .openMessage: Action.openMessage,
        .editDraft: Action.openMessage,
        .reply: Action.reply,
        .replyAll: Action.replyAll,
        .forward: Action.forward,
        .markRead: Action.markAsRead,
        .markUnread: Action.markAsUnread,
        .toggleFlag: Action.toggleFlag,
        .archive: Action.archive,
        .junk: Action.markAsJunk,
        .trash: Action.moveToTrash,
    ]

    /// The menu of the selected row; nil without one.
    static func make(state: AppState, mailbox: MailboxController, row: ListRow?, flags: ActionFlags) -> NSMenu? {
        guard let row, flags.on else { return nil }
        let s = row.message
        let transitions = IssueTransitionMenu(state: state) { IssueTransitionMenu.keyWindowSubject() }
        let model = ContextMenu.MessageState(
            flags: flags, draft: mailbox.model.inDrafts(s), changeStatus: transitions.isOffered,
            assistant: state.assistant.shown)
        let labels = ContextMenu.Labels(comment: flags.comment, flagged: flags.flagged, outbox: flags.outbox)
        let menu = NSMenu()
        for (i, group) in ContextMenu.messageItems(model).enumerated() {
            if i > 0 {
                menu.addItem(.separator())
            }
            for it in group {
                let title = mn(ContextMenu.label(it.id, labels))
                switch it.id {
                case .moveTo:
                    let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
                    item.submenu = moveToMenu(mailbox: mailbox, message: s)
                    item.isEnabled = it.enabled
                    menu.addItem(item)
                case .changeStatus:
                    menu.addItem(transitions.menuItem())
                case .assistant:
                    let assistant = AssistantMenu(state: state, includesUnread: false)
                    let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
                    item.submenu = assistant.menu
                    item.representedObject = assistant // its menu's delegate is weak
                    menu.addItem(item)
                default:
                    menu.addItem(NSMenuItem(title: title, action: actions[it.id], keyEquivalent: ""))
                }
            }
        }
        return menu
    }

    /// Move To: the folders of `s`'s account in the sidebar's order,
    /// indented by depth (`ContextMenu.moveTargets`); the current folder and
    /// the containers disabled.
    private static func moveToMenu(mailbox: MailboxController, message s: MessageSummary) -> NSMenu {
        let menu = NSMenu()
        menu.autoenablesItems = false
        guard let account = mailbox.model.account(s.accountId) else { return menu }
        let entries = folderTree(account, mailbox.model.folders[s.accountId] ?? [], CollapseState(), FavouriteState())
            .filter { !$0.header }
            .compactMap { e in e.folder.map { ContextMenu.Entry(folder: $0, depth: e.depth) } }
        for t in ContextMenu.moveTargets(entries, current: s.folderId) {
            let item = NSMenuItem(title: folderTitle(t.folder), action: Action.moveToFolder, keyEquivalent: "")
            item.representedObject = t.folder.id.rawValue
            item.indentationLevel = min(t.depth, 15)
            item.isEnabled = t.enabled
            menu.addItem(item)
        }
        return menu
    }
}
