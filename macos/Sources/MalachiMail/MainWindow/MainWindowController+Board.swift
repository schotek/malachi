// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// The Board mode's part of the window controller: the style (List,
// Columns, Today) from the toolbar's segments and the View menu, their
// checkmarks, the toolbar (the List's or the other one) and subtitle
// following the board, the detail's actions of the List's toolbar, the
// daemon's board source (started on the first entry, fed by the
// notification fan-out, stopped at quit), the case actions' hooks into
// the mail (Reply, Show in Mail) and the inline reply editor's
// environment, and the board's keyboard owner kept for the way back.
// Swift-first, like `Board`.

extension MainWindowController {
    /// The toolbar's style segments (the selected one) and View ▸ As List,
    /// As Columns, Today (the item's tag): `Board.Style`'s raw value.
    @objc func setBoardStyle(_ sender: Any?) {
        // A new style reaches the toolbar through `boardDidChange`; the
        // same one again (or a refused one) changes nothing in the
        // controller, but the segment must still show the current one.
        guard mode == .board, let index = Self.chosenIndex(sender), let style = Board.Style(rawValue: index),
              style != board.state.style
        else {
            boardToolbars.update()
            return
        }
        board.setStyle(style)
    }

    /// View ▸ As List, As Columns, Today: checked for the board's style
    /// while the board shows (in Mail they are disabled and unchecked, and
    /// the board is not made for them).
    func validateBoardStyle(_ item: NSMenuItem) {
        item.state = mode == .board && board.state.style.rawValue == item.tag ? .on : .off
    }

    /// After every change the board page applied: the toolbar and the
    /// subtitle follow, and what the user chose is remembered for the next
    /// launch (`board-last-style` once the user picked a style in this run,
    /// `board-account-filter` whenever the account changed).
    func boardDidChange(_ changes: BoardController.Changes = []) {
        rememberBoardView(changes)
        boardToolbars.update()
        if mode == .board {
            installBoardToolbar(force: false)
            applyTitle()
            // The board off or back on, the samples' own line.
            updateTriageStrip()
        }
    }

    private func rememberBoardView(_ changes: BoardController.Changes) {
        let settings = state.settings
        if changes.contains(.style), board.pickedStyle, settings.boardLastStyle != board.state.style {
            settings.boardLastStyle = board.state.style
        }
        let account = board.state.account
        if changes.contains(.filters), account != boardExtras.account {
            boardExtras.account = account
            switch account {
            case .all: settings.boardAccountFilter = ""
            case .account(let id): settings.boardAccountFilter = id.rawValue
            }
        }
    }

    /// Puts the toolbar for what the board shows into the window (the
    /// List's while the List shows cases); `force` re-binds its sidebar
    /// separator even when it is the window's already (the board coming
    /// back from Mail, whose split view was in the window meanwhile).
    func installBoardToolbar(force: Bool) {
        guard let window else { return }
        let wanted = boardToolbars.toolbar(for: board.state.style, empty: board.view.isEmpty)
        guard force || window.toolbar !== wanted else { return }
        if window.toolbar !== wanted {
            window.toolbar = wanted
        }
        BoardToolbar.rebindSidebarSeparator(in: wanted)
        if force, wanted === boardToolbars.listToolbar {
            boardToolbars.rebindDetailSeparator()
        }
        BoardToolbar.keepSidebarToggle(in: wanted)
        if wanted === boardToolbars.listToolbar {
            boardPage.shownList?.toolbarInstalled()
        }
        applyTitle()
    }

    // MARK: The detail's actions (the List's toolbar)

    /// Done, or Move Back to Board for a done case: the selected case.
    @objc func boardDone(_ sender: Any?) {
        guard mode == .board, let d = board.view.detail else { return }
        boardActions.toggleDone(d.id)
    }

    /// Remind…: the presets' menu under the toolbar item's view when it
    /// shows one, else (the item in the overflow menu, the View menu) at
    /// the pointer.
    @objc func boardRemind(_ sender: Any?) {
        guard mode == .board, let d = board.view.detail, let window, let content = window.contentView else { return }
        let menu = boardActions.remindMenu(for: d.id)
        if let item = sender as? NSToolbarItem, let anchor = item.view, anchor.window === window,
           !anchor.isHiddenOrHasHiddenAncestor
        {
            BoardActions.popUp(menu, under: anchor)
            return
        }
        // The menu item and its key R: under the panel's Remind… (or its
        // state pill), else under the List toolbar's Remind….
        let toolbarAnchor = window.toolbar?.items.first { $0.itemIdentifier == BoardToolbar.ID.boardRemind }?.view
        for anchor in [boardPage.remindAnchor, toolbarAnchor].compactMap({ $0 })
            where anchor.window === window && !anchor.isHiddenOrHasHiddenAncestor
        {
            BoardActions.popUp(menu, under: anchor)
            return
        }
        let at = content.convert(window.mouseLocationOutsideOfEventStream, from: nil)
        menu.popUp(positioning: nil, at: at, in: content)
    }

    @objc func boardArchive(_ sender: Any?) {
        guard mode == .board, let d = board.view.detail else { return }
        boardActions.archive(d.id)
    }

    /// Reply: the suggested reply's inline editor takes the keyboard, or a
    /// compose window answers (`BoardActions.reply`).
    @objc func boardReply(_ sender: Any?) {
        guard mode == .board, let d = board.view.detail else { return }
        boardActions.reply(d.id)
    }

    // MARK: The source

    /// What the board's actions need of the mail, installed by the
    /// application (Integration+Board): Reply (a comment on an issue
    /// tracker's account), Show in Mail's selection of the message
    /// (`folder` nil: the message's own folder, looked up; `found` runs
    /// before its row is selected), `leftMail` when the window leaves Mail
    /// (a Show in Mail still waiting is dropped), and the compose manager's
    /// controller, whose account list the inline reply editor shares.
    struct BoardMail {
        var reply: (@MainActor (AccountID, MessageID) -> Void)?
        var composer: ComposeController?
        var reveal: (@MainActor (
            _ account: AccountID, _ message: MessageID, _ folder: FolderID?, _ thread: ThreadID?,
            _ found: @escaping @MainActor () -> Void
        ) -> Void)?
        var leftMail: (@MainActor () -> Void)?
    }

    /// Starts the daemon's board the first time the board shows; it keeps
    /// running from then on.
    func startBoard() {
        guard !boardStarted, let source = boardSource else { return }
        boardStarted = true
        source.start()
    }

    /// Subscribes the board's source to the notification fan-out: the
    /// board's changes, the accounts' (names and badges), the connection
    /// (a reconnect lists everything again, a lost one shows the last
    /// board as unavailable). A source not started yet hears nothing. It
    /// stops when the application quits.
    func wireBoardSource() {
        let hub = state.notifications
        boardTokens = [
            hub.addBoardChanged { [weak self] n in
                self?.startedBoardSource?.boardChanged(n)
            },
            hub.addAccountsChanged { [weak self] in
                self?.startedBoardSource?.accountsChanged()
            },
            hub.addConnectionState { [weak self] s in
                let connected: Bool
                switch s {
                case .connected, .infoFailed: connected = true
                case .connecting, .protocolMismatch, .unavailable, .stopping: connected = false
                }
                self?.startedBoardSource?.connectionChanged(connected: connected)
            },
        ]
        NotificationCenter.default.addObserver(
            forName: NSApplication.willTerminateNotification, object: nil, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.startedBoardSource?.stop()
            }
        }
    }

    /// The daemon's source once started; nil before and with the samples.
    var startedBoardSource: DaemonBoardSource? {
        boardStarted ? boardSource : nil
    }

    func makeBoardActions() -> BoardActions {
        let actions = BoardActions(controller: board, samples: boardSource == nil)
        if boardSource != nil {
            actions.suggestion = state.boardReply
        }
        actions.onReply = { [weak self] account, target in
            self?.boardMail.reply?(account, target.message)
        }
        // The inline reply editor's panes (BoardReplyEditorHost).
        actions.inlineReply = BoardReplyEditorHost.Environment(state: state) { [weak self] in
            self?.boardMail.composer
        }
        actions.onShowInMail = { [weak self] c in
            self?.showInMail(c)
        }
        // As the compose window asks before it discards a message.
        let alerts = state.alerts
        actions.confirmDiscard = { [weak self] in
            await alerts.confirmDestructive(
                on: self?.window, heading: L10n.T("Discard this message?"), body: "", confirmLabel: L10n.T("_Discard"))
        }
        return actions
    }

    /// Show in Mail: the window goes back to Mail and the list selects the
    /// case's message in its folder (the reply target, else the newest
    /// message), or the message opens in its own window when the list
    /// cannot show it.
    private func showInMail(_ c: Board.Case) {
        guard let message = c.reply?.message ?? c.latestMessage, let reveal = boardMail.reveal else { return }
        setMode(.mail)
        // A refused change (a field that keeps the keyboard) leaves Board.
        guard mode == .mail else { return }
        reveal(c.account, message, c.reply?.folder, c.thread) { [weak self] in
            // A list folded by a narrow window unfolds for the row it
            // selects, as for the Outbox; not for the message's own window.
            guard let self, self.mode == .mail, self.split.isListCollapsed else { return }
            self.split.toggleMessageList(nil)
        }
    }

    /// Whether a detail action can act: in the board, on a selected case.
    var boardHasDetail: Bool {
        mode == .board && board.view.detail != nil
    }

    /// Gives the keyboard to the board as it shows: back to what had it
    /// when the board was left, when that is still in this window and
    /// showing; otherwise to the current style's table.
    func restoreBoardFocus(in window: NSWindow) {
        let saved = boardResponder as? NSView
        boardResponder = nil
        if let saved, saved.window === window, !saved.isHiddenOrHasHiddenAncestor, window.makeFirstResponder(saved) {
            return
        }
        boardPage.focusContent()
    }
}
