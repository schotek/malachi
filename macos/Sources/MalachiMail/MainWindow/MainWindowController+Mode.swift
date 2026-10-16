// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// The main window's modes (MalachiCore `Board`): Mail shows the panes
// under the mail toolbar, Board a page in their place under toolbars of
// its own (`BoardToolbar`, one for the List and one for Columns and Today),
// Calendar the calendar page under its toolbar (`CalendarToolbar`,
// MainWindowController+Calendar); the status bar stays. The switch is the first item of every toolbar
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
    /// leaves the shown mode's views before they hide (each mode keeps what
    /// had it: `savedResponder`, `boardResponder`, the calendar's) and comes
    /// back to what had it in the new one, else to its main view
    /// (`mailFocusFallback`, the board's table, the calendar's view).
    func setMode(_ newMode: Board.Mode) {
        // A refused change (a field that keeps the keyboard) leaves the
        // switches showing the mode the window is in.
        defer { syncModeSwitches() }
        guard newMode != mode, let window else { return }
        let owner = Self.focusOwner(window.firstResponder)
        // Before the views leave the window: AppKit would hand the
        // keyboard to the next key view (the status bar's button), and
        // Space would press it; a field ends its editing.
        guard window.makeFirstResponder(nil) else { return }
        switch mode {
        case .mail:
            savedResponder = owner
            // A Show in Mail still waiting selects nothing in the hidden
            // panes.
            boardMail.leftMail?()
        case .board:
            boardResponder = owner
            boardExtras.gate?.isOpen = false
        case .calendar:
            calendarExtras.responder = owner
            // The assistant panel goes back to the mail's split view (D28).
            returnAssistantToMail()
        }
        mode = newMode
        modeSwitched()
        switch newMode {
        case .board:
            // The first entry of the run opens the default style (Settings
            // → General → Board), later ones the user's last.
            board.boardWillShow()
            // The selected case's conversation may be fetched again: only
            // now, so that the case the style just chosen shows is the one
            // asked for, and once.
            boardExtras.gate?.isOpen = true
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
        case .calendar:
            showCalendar(in: window)
        case .mail:
            content.setMode(.mail)
            split.messageContainer.setOverlay(toasts)
            window.toolbar = mailToolbar
            // The panes came back into the window: the sidebar separator
            // finds their split view again (`rebindSidebarSeparator`).
            BoardToolbar.rebindSidebarSeparator(in: mailToolbar)
            BoardToolbar.keepSidebarToggle(in: mailToolbar)
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
        updateCalendarStatus()
    }

    /// Every switch is remembered (`board-last-mode`, for Open at Launch's
    /// Last Used) and settles the mode the window opens in.
    private func modeSwitched() {
        state.settings.boardLastModeNick = mode.nick
        boardExtras.startDecided = true
    }

    // MARK: Open at Launch and Show the Board

    /// Whether the board is on (the daemon's `enabled`; on until the
    /// daemon said otherwise).
    var boardEnabled: Bool {
        state.boardPreferences.preferences?.enabled ?? true
    }

    /// Opens the window in the mode Open at Launch asks for
    /// (`Settings.boardModeOnLaunch`) and keeps the mode switch and View ▸
    /// Show Board with Show the Board, as the GTK window's
    /// `setupModeMemory`: the Board waits for the daemon's word that the
    /// board is on, unless the user switched first, and only until the
    /// user's first click or key press in the window's Mail or
    /// `Board.startWait` after launch, whichever comes first
    /// (`Board.startDecision`); turned off, the switch goes (on macOS 14 its
    /// Board segment is only disabled: a toolbar item hides from macOS 15)
    /// and the window shows Mail. Called once, by the application's
    /// integration, after the window is wired.
    func setupModeMemory() {
        guard boardExtras.prefsTokens.isEmpty else { return }
        let settings = state.settings
        let prefs = state.boardPreferences
        let setUp = Date()
        let decide = { [weak self] in
            guard let self, !self.boardExtras.startDecided else { return }
            let d = Board.startDecision(
                start: settings.boardStartMode, lastMode: settings.boardLastMode, prefsKnown: prefs.stored != nil,
                enabled: self.boardEnabled, userSwitched: self.mode != .mail,
                userInteracted: self.boardExtras.interacted, waited: Date().timeIntervalSince(setUp))
            guard d.decided else { return }
            self.boardExtras.startDecided = true
            if d.mode != .mail, self.mode == .mail {
                self.setMode(d.mode)
            }
        }
        boardExtras.prefsTokens = [
            prefs.observe { [weak self] in self?.boardEnabledChanged() },
            prefs.observeLoaded { decide() },
        ]
        boardEnabledChanged()
        decide()
        guard !boardExtras.startDecided else { return }
        // The first click or key press in the window decides; so does the
        // bound.
        boardExtras.interactionMonitor = NSEvent.addLocalMonitorForEvents(
            matching: [.leftMouseDown, .rightMouseDown, .keyDown]
        ) { [weak self] event in
            MainActor.assumeIsolated {
                guard let self, let window = self.window, event.window === window, self.mode == .mail,
                      !self.boardExtras.interacted
                else { return }
                // A click counts inside the Mail content only (not the
                // toolbar: its mode switch is the user's switch); a key
                // anywhere in the window.
                if event.type != .keyDown, !window.contentLayoutRect.contains(event.locationInWindow) {
                    return
                }
                self.boardExtras.interacted = true
                decide()
                self.boardExtras.endInteractionWatch()
            }
            return event
        }
        Task { @MainActor [weak self] in
            try? await Task.sleep(for: .seconds(Board.startWait))
            decide()
            self?.boardExtras.endInteractionWatch()
        }
    }

    /// Show the Board changed (or may have): the switches follow (their
    /// Board segment is disabled while the board is off, the switch stays
    /// for Mail and Calendar), and a board turned off gives the window back
    /// to Mail.
    private func boardEnabledChanged() {
        let on = boardEnabled
        window?.toolbar?.validateVisibleItems()
        if !on, mode == .board {
            setMode(.mail)
        }
    }

    /// The board's part of a menu item's validation: Show Board while the
    /// board is off, and the board's keys (⌘1, ⌘2, and E, ⌫, D, R, which act
    /// only while the board shows and nothing takes typing:
    /// `Board.keyFor`); Done's title follows the case. False refuses the
    /// item; true leaves it to the rest of the validation.
    func validateBoardMenuItem(_ item: NSMenuItem, _ action: Selector, typing: Bool) -> Bool {
        switch action {
        case Action.setWindowMode:
            if item.tag == Board.Mode.board.rawValue, !boardEnabled {
                return false
            }
        case Action.boardDone:
            item.title = mode == .board && board.view.detail?.isDone == true ? Board.Text.notDone : Board.Text.done
        default:
            break
        }
        let mods = item.keyEquivalentModifierMask.intersection([.command, .shift, .option, .control])
        // ⌘1, ⌘2, ⌘3 work in every mode (`Board.modeForKey`).
        if action == Action.setWindowMode, let key = item.keyEquivalent.first, mods == .command {
            return Board.modeForKey(key, in: mode) != nil
        }
        guard var key = item.keyEquivalent.first, mods.subtracting(.command).isEmpty,
              [Action.setWindowMode, Action.boardDone, Action.boardRemind, Action.boardArchive, Action.boardTrash]
                .contains(action)
        else { return true }
        if key == "\u{8}" {
            key = Board.deleteCharacter // ⌫ as a key equivalent
        }
        let inText = typing || (mode == .board && boardPage.replyHost.keyboardInLivePane() != nil)
        return Board.keyFor(key, primary: mods.contains(.command), other: false, inText: inText, mode: mode) != nil
    }

    /// Whether the user looks at the selected folder (`Board.viewsMail`):
    /// a key window showing the board does not count.
    var viewsMail: Bool {
        Board.viewsMail(mode, windowIsKey: window?.isKeyWindow ?? false)
    }

    /// The window's title and subtitle for the mode: the folder's name and
    /// counts in Mail, the board's name and its scope ("All Accounts · 23
    /// cases", `Board.View.subtitle`) in Board, the shown range and its
    /// week or count in Calendar (`CalendarController.title`/`subtitle`).
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
        case .calendar:
            window.title = calendarController.title
            window.subtitle = calendarController.subtitle
        }
        let hidden = mode == .board && boardToolbarsMade && window.toolbar === boardToolbars.listToolbar
        let visibility: NSWindow.TitleVisibility = hidden ? .hidden : .visible
        if window.titleVisibility != visibility {
            window.titleVisibility = visibility
        }
    }

    /// ⌘1, ⌘2 and ⌘3 by the key, not by the character the layout types
    /// there (`Board.modeKeyDigit`): on Czech QWERTZ those keys type "+",
    /// "ě" and "š" also with ⌘, so the View menu's key equivalents alone would not
    /// match. A local monitor sees the key before the window and the menu;
    /// a layout whose ⌘ map types the digit is left to the menu item (its
    /// highlight). The monitor holds the controller weakly and acts only for
    /// this window, without a sheet or a modal window up.
    func installModeKeys() -> Any? {
        NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            // The monitor's closure is called on the main thread by AppKit.
            let consumed = MainActor.assumeIsolated {
                self?.handleModeKey(event) ?? false
            }
            return consumed ? nil : event
        }
    }

    private func handleModeKey(_ event: NSEvent) -> Bool {
        guard let window, event.window === window, window.attachedSheet == nil, NSApp.modalWindow == nil else {
            return false
        }
        let mods = event.modifierFlags.intersection([.command, .shift, .option, .control])
        guard mods == .command, let digit = Board.modeKeyDigit(event.keyCode),
              event.charactersIgnoringModifiers != String(digit),
              let target = Board.modeForKey(digit, in: mode)
        else { return false }
        if target == .board, !boardEnabled {
            return false
        }
        setMode(target)
        return true
    }

    /// The toolbar's switch (the selected segment) and View ▸ Mail, Board,
    /// Calendar (the item's tag).
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
        case Action.setBoardStyle, Action.boardDone, Action.boardRemind, Action.boardArchive, Action.boardTrash,
             Action.boardReply, Action.boardTriage:
            return .boardView
        case Action.newMessage:
            return .newMessage
        case CalendarAction.newEvent:
            return .newEvent
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

/// What the Board mode keeps beside the window's board controller.
@MainActor
final class BoardWindowExtras {
    /// The board controller's source (`BoardMessagesGate`), once made.
    var gate: BoardMessagesGate?
    /// Open at Launch has nothing more to do: decided, or the user
    /// switched first.
    var startDecided = false
    /// The user clicked or typed in the window before Open at Launch
    /// decided (`Board.startDecision`'s `userInteracted`).
    var interacted = false
    /// Watches for that first interaction until it came or the bound.
    var interactionMonitor: Any?

    func endInteractionWatch() {
        if let interactionMonitor {
            NSEvent.removeMonitor(interactionMonitor)
        }
        interactionMonitor = nil
    }
    /// The board preferences observed (`setupModeMemory`).
    var prefsTokens: [BoardObserverToken] = []
    /// The account filter as last seen, to save only its changes.
    var account = Board.AccountFilter.all
}

/// The Mail/Board/Calendar switch of the toolbars: three segments with
/// icons (a text would not fit the sidebar's section at its 200 pt minimum
/// next to the window's buttons and the sidebar toggle), the labels as
/// tooltips and accessibility descriptions. A segment's index is the mode's
/// raw value; the keys are View ▸ Show Mail (⌘1), Show Board (⌘2) and Show
/// Calendar (⌘3).
@MainActor
enum ModeSwitch {
    static func item(_ id: NSToolbarItem.Identifier, selected: Board.Mode = Board.initialMode) -> NSToolbarItemGroup {
        let t = Board.texts()
        let modes = Board.Mode.allCases
        let labels = modes.map(t.name)
        let images = modes.map { m in
            Icon.symbol(symbol(m), size: .toolbar, description: labels[m.rawValue])
        }
        let group = NSToolbarItemGroup(
            itemIdentifier: id, images: images, selectionMode: .selectOne, labels: labels, target: nil,
            action: Action.setWindowMode)
        group.controlRepresentation = .expanded
        group.selectedIndex = selected.rawValue
        tagSegments(group, titles: labels, action: Action.setWindowMode)
        return group
    }

    /// The SF Symbol of `mode`'s segment.
    static func symbol(_ mode: Board.Mode) -> String {
        switch mode {
        case .mail: "envelope"
        case .board: "square.grid.2x2"
        case .calendar: "calendar"
        }
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
