// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Assistant menu (ui/internal/assistant; there is no Blueprint yet,
/// the GTK widgets follow this port): the four message actions on what the
/// window shows (`Assistant.messageActions`), Summarize Unread in This
/// Folder (the main window's only), "Open In" with Claude Desktop and
/// Claude Code as a choice, and, while the chosen app cannot run the
/// message actions (no handler, or the bridge not registered in its
/// client; the other app is never used instead), a disabled item that says
/// why (`Assistant.problem`) above "Set Up the Assistant…".
///
/// One instance fills one menu, the toolbar button of the main window or
/// of a message window or the Message menu's submenu. The items are built
/// once: an `NSMenuToolbarItem` shows its menu through a pull-down button,
/// which takes the menu's first item for its title and hides it, so a menu
/// rebuilt while it opens loses Summarize there. Each opening only shows
/// or hides the problem and "Set Up the Assistant…" (and sets the
/// problem's title) from the last known state of `AssistantController`,
/// which it asks to refresh at the same time: the handlers at once, the
/// bridge's status in the background for the next opening. Every item
/// goes through the
/// responder chain: the window controllers perform and validate the
/// message actions and Summarize Unread for what they show, the
/// application delegate the choice of the target (the item's check mark is
/// the `assistant-target` preference) and the set-up.
@MainActor
final class AssistantMenu: NSObject, NSMenuDelegate {
    let state: AppState
    /// Whether the menu offers Summarize Unread in This Folder.
    let includesUnread: Bool
    let menu: NSMenu
    /// The separator, the problem and "Set Up the Assistant…", shown while
    /// the chosen target cannot run the message actions.
    private var setUpItems: [NSMenuItem] = []
    /// Why the chosen target cannot run the message actions: disabled,
    /// information only.
    private let problemItem = NSMenuItem()

    init(state: AppState, includesUnread: Bool) {
        self.state = state
        self.includesUnread = includesUnread
        menu = NSMenu(title: Assistant.texts().assistant)
        super.init()
        menu.delegate = self
        populate()
        updateSetUp()
    }

    /// The message action behind an item's tag.
    static func action(tag: Int?) -> Assistant.Action? {
        guard let tag, Assistant.messageActions.indices.contains(tag) else { return nil }
        return Assistant.messageActions[tag]
    }

    /// The target behind an item's tag.
    static func target(tag: Int?) -> Assistant.Target? {
        guard let tag, AssistantController.targets.indices.contains(tag) else { return nil }
        return AssistantController.targets[tag]
    }

    // MARK: NSMenuDelegate

    func menuNeedsUpdate(_ menu: NSMenu) {
        state.assistant.refresh()
        updateSetUp()
    }

    /// The items have no key equivalents: AppKit need not fill the menu
    /// (and ask the bridge) while it looks for one.
    func menuHasKeyEquivalent(
        _ menu: NSMenu, for event: NSEvent, target: AutoreleasingUnsafeMutablePointer<AnyObject?>,
        action: UnsafeMutablePointer<Selector?>
    ) -> Bool {
        false
    }

    // MARK: Items

    /// Builds the items, once.
    private func populate() {
        let texts = Assistant.texts()
        for (i, a) in Assistant.messageActions.enumerated() {
            menu.addItem(item(Assistant.label(a), Action.askAssistant, tag: i))
        }
        if includesUnread {
            menu.addItem(.separator())
            menu.addItem(item(Assistant.label(.unread), Action.summarizeUnread))
        }
        menu.addItem(.separator())
        menu.addItem(.sectionHeader(title: texts.openIn))
        for (i, t) in AssistantController.targets.enumerated() {
            menu.addItem(item(Assistant.targetName(t), Action.setAssistantTarget, tag: i))
        }
        // No action: AppKit keeps it disabled.
        problemItem.isEnabled = false
        setUpItems = [.separator(), problemItem, item(texts.setUp, Action.setUpAssistant)]
        setUpItems.forEach { menu.addItem($0) }
    }

    /// Shows why the chosen target cannot run the message actions, and
    /// "Set Up the Assistant…", while it cannot.
    private func updateSetUp() {
        let (target, ok) = state.assistant.pick(needsBridge: true)
        setUpItems.forEach { $0.isHidden = ok }
        if !ok {
            problemItem.title = state.assistant.problem(target)
        }
    }

    private func item(_ title: String, _ action: Selector, tag: Int = 0) -> NSMenuItem {
        let it = NSMenuItem(title: title, action: action, keyEquivalent: "")
        it.tag = tag
        return it
    }
}
