// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// A window whose key state names the message the Message menu's Change
/// Status acts on: the main window (the list's selection) and a message
/// window (its message). `IssueTransitionMenu.keyWindowSubject` asks the
/// key window's controller.
@MainActor
protocol IssueTransitionHost: AnyObject {
    /// The issue of the message on display or selected, nil for a mail
    /// message or nothing.
    var transitionSubject: IssueActionsController.Subject? { get }
}

/// The subject of a message: its issue when it has one.
@MainActor
func issueSubject(of s: MessageSummary?) -> IssueActionsController.Subject? {
    guard let s, s.issue != nil else { return nil }
    return IssueActionsController.Subject(accountId: s.accountId, messageId: s.id)
}

/// The Change Status menu (`Jira.changeStatusLabel`; ui/internal/jira
/// transitions.go): the status pill of the issue card pops it up, and the
/// Message menu and the More Actions menus carry it as a submenu
/// (`menuItem`). The items are built when the menu opens: "Loading…" while
/// issue.transitions runs (`IssueActionsController.loadTransitions`), then
/// one item per transition, a transition that needs fields in Jira
/// disabled with the hint as its subtitle, the site's refusal or the
/// failure as one disabled item; choosing an item performs it
/// (`IssueActionsController.perform`, which toasts and refreshes the
/// cards). Closing the menu drops a late answer. Every title is text from
/// the site set on the item as it is, never markup.
///
/// `subject` names the message when the menu opens: the card's own, or
/// the key window's (`keyWindowSubject`). The controller comes from
/// `AppState.Hooks.issueActions`; without it, or without the capability,
/// the menu item validates disabled (the menu bar's, as its other items
/// do) or is hidden with its separator (More Actions: window.blp
/// `hidden-when: "action-disabled"`), and the pill is a plain label.
@MainActor
final class IssueTransitionMenu: NSObject, NSMenuDelegate, NSMenuItemValidation {
    let menu = NSMenu()

    private let state: AppState
    private let subject: @MainActor () -> IssueActionsController.Subject?
    /// What the open menu shows: the subject it was loaded for and the
    /// issue issue.transitions named (the spinner's key when an item is
    /// chosen).
    private var loadedFor: IssueActionsController.Subject?
    private var loadedIssue: IssueInfo?

    init(state: AppState, subject: @escaping @MainActor () -> IssueActionsController.Subject?) {
        self.state = state
        self.subject = subject
        super.init()
        menu.autoenablesItems = false
        menu.delegate = self
    }

    /// The message of the key window (`IssueTransitionHost`), for the
    /// menu bar.
    static func keyWindowSubject() -> IssueActionsController.Subject? {
        (NSApp.keyWindow?.windowController as? any IssueTransitionHost)?.transitionSubject
    }

    private var controller: IssueActionsController? {
        state.hooks.issueActions?()
    }

    /// Whether the menu has anything to offer right now: a subject on an
    /// account with the capability.
    var isOffered: Bool {
        guard let s = subject(), let c = controller else { return false }
        return c.canTransition(s.accountId)
    }

    /// The separator in front of the item in a menu that hides both while
    /// there is nothing to offer (`menuItem(hiddenWith:)`).
    private weak var separator: NSMenuItem?
    private var hidesWhenUnavailable = false

    /// The "Change Status" item with this menu as its submenu, validated by
    /// `isOffered`. The item keeps this object alive (its `representedObject`;
    /// a menu's delegate and an item's target are weak).
    func menuItem() -> NSMenuItem {
        let item = NSMenuItem(title: Jira.changeStatusLabel(), action: #selector(changeStatus(_:)), keyEquivalent: "")
        item.target = self
        item.submenu = menu
        item.representedObject = self
        return item
    }

    /// The item for a menu that shows it only where it can act (More
    /// Actions): it and `separator`, the one in front of it, are hidden
    /// while there is nothing to offer, and start out hidden.
    func menuItem(hiddenWith separator: NSMenuItem) -> NSMenuItem {
        let item = menuItem()
        hidesWhenUnavailable = true
        self.separator = separator
        item.isHidden = true
        separator.isHidden = true
        return item
    }

    /// The submenu's parent item never fires (the submenu opens instead);
    /// the selector exists for the validation.
    @objc private func changeStatus(_ sender: Any?) {}

    func validateMenuItem(_ item: NSMenuItem) -> Bool {
        if item.action == #selector(changeStatus(_:)) {
            let offered = isOffered
            if hidesWhenUnavailable {
                item.isHidden = !offered
                separator?.isHidden = !offered
            }
            return offered
        }
        return item.isEnabled
    }

    /// Pops the menu up under `view` (the status pill).
    func popUp(under view: NSView) {
        menu.popUp(positioning: nil, at: NSPoint(x: 0, y: 0), in: view)
    }

    // MARK: NSMenuDelegate

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        loadedFor = nil
        loadedIssue = nil
        guard let s = subject(), let c = controller, c.canTransition(s.accountId) else {
            menu.addItem(disabled(Jira.noTransitions()))
            return
        }
        menu.addItem(disabled(Jira.transitionsLoading()))
        c.loadTransitions(s) { [weak self, weak menu] outcome in
            guard let self, let menu else { return }
            menu.removeAllItems()
            switch outcome {
            case .success(let loaded):
                self.loadedFor = s
                self.loadedIssue = loaded.issue
                if loaded.items.isEmpty {
                    menu.addItem(self.disabled(Jira.noTransitions()))
                }
                let busy = c.isBusy(account: s.accountId, key: loaded.issue.key)
                for item in loaded.items {
                    menu.addItem(self.item(item, busy: busy))
                }
            case .failure(let err):
                menu.addItem(self.disabled(rpcErrorText(Jira.loadTransitionsAction(), err)))
            }
        }
    }

    func menuDidClose(_ menu: NSMenu) {
        controller?.cancelLoad()
    }

    /// No key equivalents: without this AppKit would populate the menu
    /// (`menuNeedsUpdate`, a request to the site) to look for one at every
    /// key press.
    func menuHasKeyEquivalent(
        _ menu: NSMenu, for event: NSEvent, target: AutoreleasingUnsafeMutablePointer<AnyObject?>,
        action: UnsafeMutablePointer<Selector?>
    ) -> Bool {
        false
    }

    // MARK: Items

    private func disabled(_ title: String) -> NSMenuItem {
        let it = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        it.isEnabled = false
        return it
    }

    private func item(_ t: Jira.TransitionItem, busy: Bool) -> NSMenuItem {
        let it = NSMenuItem(title: t.title, action: #selector(choose(_:)), keyEquivalent: "")
        it.target = self
        it.representedObject = Choice(item: t)
        it.isEnabled = t.enabled && !busy
        // The hint says why the item is disabled; an enabled item shows
        // the status it leads to when that is not its name. Before macOS
        // 14.4 a menu item has no subtitle: the tooltip carries it.
        let subtitle = t.enabled ? t.subtitle : t.hint
        if !subtitle.isEmpty {
            if #available(macOS 14.4, *) {
                it.subtitle = subtitle
            } else {
                it.toolTip = subtitle
            }
        }
        if !t.hint.isEmpty {
            it.toolTip = t.hint
        }
        return it
    }

    /// A menu item's transition (`representedObject`).
    private final class Choice: NSObject {
        let item: Jira.TransitionItem

        init(item: Jira.TransitionItem) {
            self.item = item
        }
    }

    @objc private func choose(_ sender: Any?) {
        guard let choice = (sender as? NSMenuItem)?.representedObject as? Choice,
              let s = loadedFor, let issue = loadedIssue, let c = controller else { return }
        c.perform(s, choice.item, issue: issue)
    }
}
