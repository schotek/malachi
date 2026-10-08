// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// The board's triage in the main window (docs/mcp.md "Triage of the board";
// the run is the application's `BoardTriageController`, AppState): the
// board toolbars' Triage item, the status strip's triage line, the toast
// of a manual run's end, and what the run needs of this window's board
// source (its snapshots, a list again after a run).
//
// - Triage: what `triage.view.control` offers — hidden, Get Claude Code…,
//   Sign In…, unavailable (insensitive, the tooltip says why), ✦ Triage,
//   Stop while a run works — with the view's title and tooltip; validated
//   like the board's other items (`Action.boardTriage`, Board mode only).
//   Whether it shows at all is Core's one rule (`TriageView.offered`, which
//   Settings' Board group follows too). The second click of a double
//   click is ignored, so a double click starts one run and does not stop
//   it. With MALACHI_BOARD_SAMPLES it stays the placeholder it was: shown
//   with the samples' assistant on, and a click only says "not in this
//   preview".
// - The strip (`StatusBarViewController.setNote`): Core's
//   `Board.triageStripText` for the mode and the board's phase. The sync
//   line keeps its place: the note gives way first. Core publishes the
//   view again while it names a relative time, so the window keeps no
//   clock of its own.
// - Toasts: a manual run's result or failure (not a declined consent: the
//   user just said no). Automatic runs never toast; the strip says enough.
// - The board source starts at launch, not on the first entry into Board,
//   while the triage wants its data (`wantsBoardData`: automatic triage on,
//   consented to and able to run): the schedule learns the queue only from
//   its snapshots. It starts too when the triage asks the board to list
//   (`relistBoard`: Settings → AI's Board group, a run's end).
//
// Nothing a run's assistant wrote is shown here: only the Core view's
// counts and classes. Swift-first, like `Board`.

extension MainWindowController {
    func wireTriage() {
        let triage = state.triage
        if let source = boardSource {
            source.onSnapshot = { [weak triage] s in
                triage?.boardChanged(s)
            }
            // Settings → AI's Board group asks too (its status row, the
            // tokens of the last 24 hours): a board not listed yet starts
            // now and lists, without the Board ever shown.
            triage.onRefresh = { [weak self] in
                self?.relistBoard()
            }
            state.boardReply.onRefresh = { [weak self] in
                self?.relistBoard()
            }
        }
        triageTokens = [triage.observe { [weak self] in self?.triageChanged() }]
        triageSeenActive = triage.state.isActive ? triage.state.trigger : nil
        triageChanged()
    }

    /// Anything the triage view shows changed: the board source may have
    /// to start, the toolbar item and the strip follow, and a manual run
    /// that ended says how.
    private func triageChanged() {
        startBoardForAutoTriage()
        if boardToolbarsMade {
            boardToolbars.update()
            if mode == .board {
                window?.toolbar?.validateVisibleItems()
            }
        }
        toastTriageEnd()
        updateTriageStrip()
    }

    /// Lists the daemon's board again, starting it first when nothing
    /// has (the start lists it).
    private func relistBoard() {
        guard boardSource != nil else { return }
        if boardStarted {
            startedBoardSource?.refresh()
        } else {
            startBoard()
        }
    }

    /// The triage wants the board's data (`wantsBoardData`): the daemon's
    /// board is listed from now on, so its snapshots reach the triage
    /// (`startBoard`; it keeps running until the application quits, as
    /// after an entry into Board). Otherwise the board starts on its first
    /// entry.
    private func startBoardForAutoTriage() {
        guard !boardStarted, boardSource != nil, state.triage.wantsBoardData else { return }
        startBoard()
    }

    /// The end of a manual run this window saw active: its result, or why
    /// it failed, as a toast.
    private func toastTriageEnd() {
        let s = state.triage.state
        if s.isActive {
            triageSeenActive = s.trigger
            return
        }
        guard let was = triageSeenActive else { return }
        triageSeenActive = nil
        guard was == .manual else { return }
        if case .failed(_, .declined, _) = s {
            return
        }
        let text = state.triage.view.result
        if !text.isEmpty {
            toasts.show(text)
        }
    }

    // MARK: The strip

    /// Puts `triageStripText` into the status bar.
    func updateTriageStrip() {
        (content.statusBar as? StatusBarViewController)?.setNote(triageStripText)
    }

    /// The strip's triage line for the mode (see the file's comment).
    var triageStripText: String {
        // The samples' own line: the daemon's triage is not theirs.
        if mode == .board, boardSource == nil {
            return board.view.statusLine
        }
        return Board.triageStripText(state.triage.view, mode: mode, phase: board.view.phase)
    }

    // MARK: The toolbar item

    /// What the board toolbars' Triage shows.
    var triageToolbarItem: BoardToolbar.TriageItem {
        guard boardSource != nil else {
            // The samples: the placeholder, shown while their assistant is on.
            let v = board.view
            return BoardToolbar.TriageItem(
                shown: v.assistantOn && v.phase != .off, title: Board.Text.triage, toolTip: Board.Text.triage)
        }
        let v = state.triage.view
        // Core's rule: hidden without the assistant In App, and for a
        // board that is off or that the daemon does not have.
        guard v.offered else { return .hidden }
        return BoardToolbar.TriageItem(shown: true, title: v.title, toolTip: v.toolTip)
    }

    /// Whether Triage can be clicked (the window's validation).
    var triageClickable: Bool {
        guard mode == .board, triageToolbarItem.shown else { return false }
        return boardSource == nil || state.triage.view.enabled
    }

    /// The board toolbars' Triage (`Action.boardTriage`): what the view
    /// offers. A run without consent asks with the sheet on this window
    /// first (the triage's `consent`, AppState).
    @objc func boardTriage(_ sender: Any?) {
        guard mode == .board else { return }
        // The second click of a double click: the first one acted (and
        // made Triage a Stop under the pointer). A later click, however
        // soon, is a click of its own (`clickCount` 1).
        if let e = NSApp.currentEvent, [.leftMouseDown, .leftMouseUp].contains(e.type), e.clickCount > 1 {
            return
        }
        guard boardSource != nil else {
            // The samples are not the daemon's: nothing to triage.
            toasts.show(Board.Text.later)
            return
        }
        let triage = state.triage
        switch triage.view.control {
        case .triage:
            triage.start(.manual)
        case .stop:
            triage.cancel()
        case .getClaudeCode:
            openInBrowser(state.settings.assistantProvider == .chatgpt ? "https://developers.openai.com/codex/cli" : Assistant.installURL) { [weak self] text in
                self?.toasts.show(text)
            }
        case .signIn:
            triageSignIn()
        case .hidden, .unavailable:
            break
        }
    }

    /// Sign In…: Claude Code's own sign-in in the browser (the
    /// application's locator, as the panel and Settings run it); the
    /// triage asks the sign-in again by itself when it ends. A failure or
    /// a timeout is a toast, as Settings says it.
    private func triageSignIn() {
        if state.settings.assistantProvider == .chatgpt {
            guard !state.chatGPT.connecting else { return }
            Task { @MainActor [weak self] in
                guard let self else { return }
                do { try await self.state.chatGPT.signIn() }
                catch { self.toasts.show(L10n.T("Could not connect to ChatGPT.")) }
            }
            return
        }
        // The application has one sign-in: a second would end the first
        // (the control is insensitive meanwhile; this is the last guard).
        guard !state.claudeCode.signingIn else { return }
        let run = state.claudeCode.startSignIn()
        Task { @MainActor [weak self] in
            let result = await run.result
            guard let self else { return }
            switch result {
            case .failed(let reason):
                self.toasts.show(Assistant.signInFailedText(reason))
            case .timedOut:
                self.toasts.show(Assistant.signInTexts().timedOut)
            case .done, .cancelled, .notFound:
                break
            }
        }
    }

    // MARK: Development aid

    /// The triage's lines for `MALACHI_START`'s dump: the view's control,
    /// each board toolbar's Triage item, the strip.
    var triageDevelopmentLines: [String] {
        let v = state.triage.view
        var lines = ["  triage control \(v.control) title '\(v.title)' enabled \(v.enabled) running \(v.running)"
            + " needsConsent \(v.needsConsent) state \(state.triage.state) clickable \(triageClickable)"]
        if boardToolbarsMade {
            for item in boardToolbars.triageItems {
                lines.append("  triage item '\(item.title)' enabled \(item.isEnabled)"
                    + " toolTip '\(item.toolTip ?? "")' in \(item.toolbar?.identifier ?? "none")")
            }
        }
        if let bar = content.statusBar as? StatusBarViewController {
            let t = bar.shownTexts
            lines.append("  strip line '\(t.line)' note '\(t.note)'")
        }
        lines.append("  board source started \(boardStarted)")
        return lines
    }
}
