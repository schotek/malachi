// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// Where the Change Status submenu (`IssueTransitionMenu.menuItem`) goes
/// besides the issue card's pill: the Message menu of the menu bar, acting
/// on the key window's message (`IssueTransitionHost`), and the More
/// Actions menu of the main window and of every message window, acting on
/// that window's and hidden there while it has nothing to act on. The
/// menus are built elsewhere (MainMenu, MainToolbar);
/// the items are added to them once they exist, so those builders stay as
/// they are.
@MainActor
enum ChangeStatusMenus {
    /// Message ▸ Change Status, after Forward.
    static func install(intoMenuBar bar: NSMenu?, state: AppState) {
        guard let bar, let message = bar.items.first(where: { $0.submenu?.items.contains { $0.action == Action.forward } == true })?.submenu,
              let forward = message.items.firstIndex(where: { $0.action == Action.forward }) else { return }
        let menu = IssueTransitionMenu(state: state) { IssueTransitionMenu.keyWindowSubject() }
        message.insertItem(menu.menuItem(), at: forward + 1)
    }

    /// More Actions ▸ Change Status of the window whose toolbar this is
    /// (`MainToolbar.ID.moreActions`), for the message `subject` names.
    static func install(intoToolbar toolbar: NSToolbar?, state: AppState, subject: @escaping @MainActor () -> IssueActionsController.Subject?) {
        guard let item = toolbar?.items.first(where: { $0.itemIdentifier == MainToolbar.ID.moreActions }) as? NSMenuToolbarItem
        else { return }
        let menu = IssueTransitionMenu(state: state, subject: subject)
        // Only for a message of an issue on an account that changes
        // statuses; for mail the menu ends as it did.
        let separator = NSMenuItem.separator()
        item.menu.addItem(separator)
        item.menu.addItem(menu.menuItem(hiddenWith: separator))
    }
}

extension MainWindowController: IssueTransitionHost {
    /// The selected row's message (a conversation row's newest folder
    /// member, as the other actions take it).
    var transitionSubject: IssueActionsController.Subject? {
        issueSubject(of: (messageActions as? MessageActionsController)?.list.selectedRow?.message)
    }
}

extension MessageWindowController: IssueTransitionHost {
    var transitionSubject: IssueActionsController.Subject? {
        messageView.transitionSubject ?? issueSubject(of: summary)
    }
}
