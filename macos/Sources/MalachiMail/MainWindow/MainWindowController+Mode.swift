// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// The main window's two modes (MalachiCore `Board`): Mail shows the panes
// under the mail toolbar, Board a page in their place under toolbars of
// its own (`BoardToolbar`, one for the List and one for Columns and Today);
// the status bar stays. The switch is the first item of every toolbar
// (`ModeSwitch`), right after the window's buttons, and the View menu has
// Mail and Board. The board takes the keyboard on entry and gets back what
// had it there last (MainWindowController+Board). The panes leave the
// window while the board shows (`MainContentViewController.setMode`) and
// the mail toolbar is kept, so the way back finds the folder, the
// selection, the scroll positions, the reader, the search and the
// assistant's transcript as they were. Swift-first: the GTK window
// follows with ui/internal/board.

extension MainWindowController {
    /// Shows `newMode`; the current one changes nothing. The keyboard
    /// leaves the panes before they hide and comes back to what had it
    /// (`savedResponder`), else to the list (`mailFocusFallback`).
    func setMode(_ newMode: Board.Mode) {
        // A refused change (a field that keeps the keyboard) leaves the
        // switches showing the mode the window is in.
        defer { syncModeSwitches() }
        guard newMode != mode, let window else { return }
        switch newMode {
        case .board:
            let owner = Self.focusOwner(window.firstResponder)
            // Before the panes leave the window: AppKit would hand the
            // keyboard to the next key view (the status bar's button), and
            // Space would press it.
            guard window.makeFirstResponder(nil) else { return }
            savedResponder = owner
            mode = .board
            // A Show in Mail still waiting selects nothing in the hidden
            // panes.
            boardMail.leftMail?()
            // The first entry of the run opens the default style (Settings
            // → General → Board), later ones the user's last.
            board.boardWillShow()
            // The daemon's board is listed from the first entry on.
            startBoard()
            // The page first: the List's toolbar follows its split view.
            content.setMode(.board, page: boardPage)
            // A List shown for the first time decides its folds from the
            // window's width before the next layout: its panes' minimums
            // must not hold the window wider.
            boardPage.shownList?.willResize(toWidth: window.contentLayoutRect.width)
            boardPage.setOverlay(toasts)
            installBoardToolbar(force: true)
            // The dates may have moved on since the board was built (a new
            // day); the controller reports only what really changed. A
            // board that could not be listed is asked for again.
            board.boardShown()
            boardToolbars.update()
            restoreBoardFocus(in: window)
        case .mail:
            // The board's keyboard owner, for the next time it shows; then
            // the board lets the keyboard go before the page leaves the
            // window (a field ends its editing).
            let owner = Self.focusOwner(window.firstResponder)
            guard window.makeFirstResponder(nil) else { return }
            boardResponder = owner
            mode = .mail
            content.setMode(.mail)
            split.messageContainer.setOverlay(toasts)
            window.toolbar = mailToolbar
            // The panes came back into the window: the sidebar separator
            // finds their split view again (`rebindSidebarSeparator`).
            BoardToolbar.rebindSidebarSeparator(in: mailToolbar)
            // What changed while the toolbar was away (stars, hidden items).
            mailToolbar.validateVisibleItems()
            restoreFocus(in: window)
            // The user looks at the folder again (`viewsMail`): its
            // notifications go, as when the window becomes key.
            if window.isKeyWindow {
                onBecomeKey?()
            }
        }
        applyTitle()
        updateTriageStrip()
    }

    /// Whether the user looks at the selected folder (`Board.viewsMail`):
    /// a key window showing the board does not count.
    var viewsMail: Bool {
        Board.viewsMail(mode, windowIsKey: window?.isKeyWindow ?? false)
    }

    /// The window's title and subtitle for the mode: the folder's name and
    /// counts in Mail, the board's name and its scope ("All Accounts · 23
    /// cases", `Board.View.subtitle`) in Board.
    /// The List shows no title in its toolbar (its sections are the
    /// columns'); the window keeps its title for the Window menu.
    func applyTitle() {
        guard let window else { return }
        switch mode {
        case .mail:
            window.title = folderTitle.isEmpty ? MainMenu.appName : folderTitle
            window.subtitle = folderSubtitle
        case .board:
            window.title = Board.texts().board
            window.subtitle = board.view.subtitle
        }
        let hidden = mode == .board && boardToolbarsMade && window.toolbar === boardToolbars.listToolbar
        let visibility: NSWindow.TitleVisibility = hidden ? .hidden : .visible
        if window.titleVisibility != visibility {
            window.titleVisibility = visibility
        }
    }

    /// The toolbar's switch (the selected segment) and View ▸ Mail, Board
    /// (the item's tag).
    @objc func setWindowMode(_ sender: Any?) {
        guard let index = Self.chosenIndex(sender), let m = Board.Mode(rawValue: index) else {
            syncModeSwitches()
            return
        }
        setMode(m)
    }

    /// The `Board.Command` a selector stands for, nil for an action the
    /// mode does not restrict (the application's, the Edit menu's).
    /// Swift-first and AppKit-only: Go names its actions, not selectors;
    /// built like `ActionPresentation.kind`.
    static func command(_ action: Selector) -> Board.Command? {
        switch action {
        case Action.setWindowMode:
            return .switchMode
        case Action.setBoardStyle, Action.boardDone, Action.boardRemind, Action.boardArchive, Action.boardReply,
             Action.boardTriage:
            return .boardView
        case Action.newMessage:
            return .newMessage
        case Action.checkForNewMail:
            return .checkForNewMail
        case Action.toggleSidebar, Action.setMessageFilter, Action.findMessages, Action.toggleMessageList, Action.toggleInspector,
             Action.loadImages, Action.trustSender:
            return .mailView
        case Action.reply, Action.replyAll, Action.forward, Action.markAsRead, Action.markAsUnread,
             Action.toggleFlag, Action.archive, Action.markAsJunk, Action.moveToTrash, Action.askAssistant,
             Action.summarizeUnread:
            return .messageAction
        default:
            return nil
        }
    }

    /// The index a switch's sender chose: a menu item's or a segment's
    /// tag (the toolbar's overflow menu), a group's or a segmented
    /// control's selected segment. Shared by the mode and the style.
    static func chosenIndex(_ sender: Any?) -> Int? {
        switch sender {
        case let item as NSMenuItem: return item.tag
        case let group as NSToolbarItemGroup: return group.selectedIndex
        case let item as NSToolbarItem: return item.tag
        case let control as NSSegmentedControl: return control.selectedSegment
        default: return nil
        }
    }

    // MARK: Focus

    /// What has the keyboard, as far as it can be given back: a text field
    /// rather than the field editor it edits in, nil for the window itself.
    static func focusOwner(_ responder: NSResponder?) -> NSResponder? {
        if let editor = responder as? NSTextView, editor.isFieldEditor {
            return editor.delegate as? NSResponder
        }
        return responder is NSView ? responder : nil
    }

    /// Gives the keyboard back to what had it in Mail when it is still in
    /// this window and showing; otherwise to the list.
    private func restoreFocus(in window: NSWindow) {
        let saved = savedResponder as? NSView
        savedResponder = nil
        if let saved, saved.window === window, !saved.isHiddenOrHasHiddenAncestor, window.makeFirstResponder(saved) {
            return
        }
        mailFocusFallback?()
    }

    private func syncModeSwitches() {
        for item in modeSwitches where item.selectedIndex != mode.rawValue {
            item.selectedIndex = mode.rawValue
        }
    }
}

/// The Mail/Board switch of the toolbars: two segments with icons (a text
/// would not fit the sidebar's section at its 200 pt minimum next to the
/// window's buttons and the sidebar toggle), the labels as tooltips and
/// accessibility descriptions. A segment's index is the mode's raw value.
/// No key equivalent yet.
@MainActor
enum ModeSwitch {
    static func item(_ id: NSToolbarItem.Identifier, selected: Board.Mode = Board.initialMode) -> NSToolbarItemGroup {
        let t = Board.texts()
        let modes = Board.Mode.allCases
        let labels = modes.map { $0 == .mail ? t.mail : t.board }
        let images = modes.map { m in
            Icon.symbol(m == .mail ? "envelope" : "square.grid.2x2", size: .toolbar, description: labels[m.rawValue])
        }
        let group = NSToolbarItemGroup(
            itemIdentifier: id, images: images, selectionMode: .selectOne, labels: labels, target: nil,
            action: Action.setWindowMode)
        group.controlRepresentation = .expanded
        group.selectedIndex = selected.rawValue
        tagSegments(group, titles: labels, action: Action.setWindowMode)
        return group
    }

    /// Gives each segment of `group` its index as tag, its title as
    /// tooltip and a tagged menu form, so a group that ends up in the
    /// toolbar's overflow menu still chooses by index
    /// (`MainWindowController.chosenIndex`); the window's validation checks
    /// the current one, as in the View menu.
    static func tagSegments(_ group: NSToolbarItemGroup, titles: [String], action: Selector) {
        for (index, (sub, title)) in zip(group.subitems, titles).enumerated() {
            sub.tag = index
            sub.toolTip = title
            let form = NSMenuItem(title: title, action: action, keyEquivalent: "")
            form.tag = index
            sub.menuFormRepresentation = form
        }
    }
}
