// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The menus of a case: the context menu of a row or card, and the menu of
/// the detail's state pill. The items call the controller (`setState`)
/// and `BoardActions` (done, remind, archive, unstar, reply, Show in
/// Mail), as the toolbar and the panel's bar do; the controller reports
/// the change through its `onChange`, so the menus do nothing else.
@MainActor
enum BoardCaseMenu {
    /// Move To ▸ (the four states, a checkmark on the current one), Mark
    /// as Done or Move Back to Board, Remind Me ▸, Archive, Unstar (only for
    /// a case on the board because of a star, `Board.canUnstar`); Reply
    /// (the inline editor of a suggested reply, else a compose window),
    /// Show in Mail. A done case has no
    /// Move To: a new state would not take it out of Done (`setState` does
    /// not reopen), so the item is there, disabled.
    static func contextMenu(for id: Board.CaseID, actions: BoardActions) -> NSMenu {
        let controller = actions.controller
        let menu = NSMenu()
        menu.autoenablesItems = false
        let current = currentState(of: id, controller)
        let isDone = isDone(id, controller)
        let move = NSMenuItem(title: Board.Text.moveTo, action: nil, keyEquivalent: "")
        if current != nil, !isDone {
            move.submenu = stateMenu(for: id, controller: controller)
        } else {
            move.isEnabled = false
        }
        menu.addItem(move)
        menu.addItem(.separator())
        menu.addItem(BoardMenuAction.item(isDone ? Board.Text.notDone : Board.Text.markAsDone) {
            actions.toggleDone(id)
        })
        let remind = NSMenuItem(title: Board.Text.remindMe, action: nil, keyEquivalent: "")
        remind.submenu = actions.remindMenu(for: id)
        menu.addItem(remind)
        let archive = BoardMenuAction.item(Board.Text.archive) {
            actions.archive(id)
        }
        archive.isEnabled = actions.canArchive(id)
        menu.addItem(archive)
        if actions.canUnstar(id) {
            menu.addItem(BoardMenuAction.item(Board.Text.unstar) {
                actions.unstar(id)
            })
        }
        menu.addItem(.separator())
        let reply = BoardMenuAction.item(Board.Text.reply) {
            actions.reply(id)
        }
        reply.isEnabled = actions.canReply(id)
        menu.addItem(reply)
        let show = BoardMenuAction.item(Board.Text.showInMail) {
            actions.showInMail(id)
        }
        show.isEnabled = actions.canShowInMail(id)
        menu.addItem(show)
        return menu
    }

    /// The four states with a checkmark on the case's current one; for the
    /// state pill's menu and the Move To submenu.
    static func stateMenu(for id: Board.CaseID, controller: BoardController) -> NSMenu {
        let menu = NSMenu()
        let current = currentState(of: id, controller)
        for s in Board.State.allCases {
            let item = BoardMenuAction.item(Board.Text.stateName(s)) {
                controller.setState(s, of: id)
            }
            item.state = s == current ? .on : .off
            menu.addItem(item)
        }
        return menu
    }

    private static func currentState(of id: Board.CaseID, _ controller: BoardController) -> Board.State? {
        let snapshot = controller.source.snapshot
        guard let c = snapshot.cases.first(where: { $0.id == id }) else { return nil }
        return Board.state(of: c, annotated: snapshot.annotated)
    }

    private static func isDone(_ id: Board.CaseID, _ controller: BoardController) -> Bool {
        controller.source.snapshot.cases.first(where: { $0.id == id })?.done ?? false
    }
}

/// An item's action as a closure. The item keeps the object alive through
/// `representedObject` (an item's target is weak).
@MainActor
final class BoardMenuAction: NSObject {
    private let run: @MainActor () -> Void

    private init(_ run: @escaping @MainActor () -> Void) {
        self.run = run
    }

    static func item(_ title: String, _ run: @escaping @MainActor () -> Void) -> NSMenuItem {
        let action = BoardMenuAction(run)
        let item = NSMenuItem(title: title, action: #selector(fire(_:)), keyEquivalent: "")
        item.target = action
        item.representedObject = action
        return item
    }

    @objc private func fire(_ sender: Any?) {
        run()
    }
}
