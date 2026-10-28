// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// DEVELOPMENT AID, not a feature (MALACHI_START, see
// MainWindowController+DevStart.swift): the steps of the assistant's
// scenes of docs/screenshots.md, taken over the demo claude of
// backend/cmd/malachi-devclaude (never the real Claude Code: the step
// runs whatever `assistant-claude-path` names). Nothing here runs without
// MALACHI_START and nothing of it is translated.
//
//   `assistant-unread`        Summarize Unread in This Folder, in the panel
//                             (the folder selected in the sidebar)
//   `assistant=<action>`      a message action on the list's selection, in the
//                             panel: summarize, tasks, draft-reply, ask
//   `assistant-say=<text>`    the words of the waiting action (Draft a Reply…,
//                             Ask About This Message…), or a free question
//   `assistant-new`           New Conversation: the panel emptied, following the
//                             selection again
//   `board-select=<text>`     selects the case whose subject contains the text
//   `triage`                  starts a manual triage run; the consent is given
//                             quietly (the preference keys and the daemon's
//                             `assistant` preference) when it is missing
//   `suggest-reply[=<text>]`  ✦ Suggest Reply (or Follow-up) for the selected
//                             case, with the instruction
//   `compose-new=<to>|<subject>|<text>`
//                             New Message prefilled; `\n` in the text is a
//                             paragraph break
//   `compose-rewrite=<preset>` the compose window's ✦ popover with the preset
//                             run: politer, shorter, fix, english
//   `reply`                   Reply to the selection (a compose window)
//   `compose-write=<text>`    the compose window's assistant bar: the
//                             instruction typed and Write pressed
//   `search-own=<words>`      the words put into the search field and
//                             converted by Search in Your Own Words
//   `search-clear`            the field emptied and the search ended
//   `prefs-ai[=<points>]`     Settings → AI, the window as tall as the screen,
//                             the page scrolled down by the points, left open
//   `wait=<seconds>`          nothing; the scheduler waits that long before the
//                             next step instead of the interval
//
// The steps print `MALACHI_START <step>: …` lines on stderr; what the
// assistant answers is in the window, never printed.
@MainActor
enum DevelopmentAssistant {
    static func step(_ step: String, in main: MainWindowController) -> Bool {
        let (name, value) = DevelopmentStart.split(step)
        switch name {
        case "assistant-unread":
            main.messageActions?.summarizeUnread()
            say("MALACHI_START assistant-unread: asked, panel shown \(main.state.assistant.panelShown)")
        case "assistant":
            ask(value, main)
        case "assistant-say":
            let sent = main.assistantPanel?.controller.submit(value) ?? false
            say("MALACHI_START assistant-say: sent \(sent)")
        case "assistant-new":
            main.assistantPanel?.controller.newConversation()
            say("MALACHI_START assistant-new: done")
        case "board-select":
            selectCase(value, main)
        case "triage":
            triage(main)
        case "suggest-reply":
            suggestReply(value, main)
        case "compose-new":
            composeNew(value, main)
        case "compose-rewrite":
            rewrite(value)
        case "reply":
            main.reply(nil)
            say("MALACHI_START reply: asked")
        case "compose-write":
            write(value)
        case "search-own":
            // The main window in front: a compose window may be.
            main.showMainWindow()
            main.developmentSearchInOwnWords(value)
            say("MALACHI_START search-own: asked for '\(value)'")
        case "search-clear":
            main.developmentClearSearch()
            say("MALACHI_START search-clear: done")
        case "prefs-ai":
            prefsAI(value, main)
        case "wait":
            break
        default:
            return false
        }
        return true
    }

    private static func say(_ line: String) {
        FileHandle.standardError.write(Data((line + "\n").utf8))
    }

    /// `prefs-ai[=<points>]`: Settings → AI, the window as tall as the
    /// screen allows (the page is long) and its page scrolled down by the
    /// points; the document's height is printed to choose them by.
    private static func prefsAI(_ value: String, _ main: MainWindowController) {
        let prefs = Integration.showPreferences(main.state)
        prefs.select(.ai)
        guard let w = prefs.window else {
            say("MALACHI_START prefs-ai: no Settings window")
            return
        }
        // After the tab's own sizing settled (the tab view controller
        // sizes the window to the page's preferred size as it selects).
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
            if let screen = w.screen ?? NSScreen.main {
                let visible = screen.visibleFrame
                var frame = w.frame
                frame.size.height = visible.height
                frame.origin.y = visible.minY
                // The tab view controller sizes the window to the page's
                // preferred size: that first, then the frame.
                let chrome = w.frame.height - (w.contentView?.frame.height ?? 0)
                prefs.aiPane.preferredContentSize = NSSize(width: PreferencesPaneViewController.paneWidth, height: visible.height - chrome)
                w.setFrame(frame, display: true)
            }
            let scroll = prefs.aiPane.scrollView
            scroll.layoutSubtreeIfNeeded()
            if let offset = Double(value), offset > 0 {
                scroll.contentView.scroll(to: NSPoint(x: 0, y: offset))
                scroll.reflectScrolledClipView(scroll.contentView)
            }
            let docHeight = scroll.documentView?.frame.height ?? 0
            say(String(format: "MALACHI_START prefs-ai: window %ld frame %@ document %.0f visible %.0f at %.0f",
                       w.windowNumber, NSStringFromRect(w.frame), docHeight, scroll.contentView.bounds.height, scroll.contentView.bounds.minY))
        }
    }

    private static func ask(_ value: String, _ main: MainWindowController) {
        guard let action = DevelopmentStart.assistantAction(value) else {
            say("MALACHI_START assistant: unknown action '\(value)' (summarize, tasks, draft-reply, ask)")
            return
        }
        main.messageActions?.askAssistant(action)
        say("MALACHI_START assistant: \(value) asked, panel shown \(main.state.assistant.panelShown)")
    }

    private static func selectCase(_ text: String, _ main: MainWindowController) {
        let needle = text.lowercased()
        let cases = main.board.source.snapshot.cases
        guard let c = cases.first(where: { $0.subject.lowercased().contains(needle) }) else {
            say("MALACHI_START board-select: no case with '\(text)' among \(cases.count)")
            return
        }
        main.board.select(c.id)
        say("MALACHI_START board-select: selected '\(c.subject)' (\(c.ruleReason))")
    }

    private static func triage(_ main: MainWindowController) {
        let triage = main.state.triage
        Task { @MainActor in
            if triage.needsConsent {
                let given = await triage.giveConsent(quiet: true)
                say("MALACHI_START triage: consent given \(given)")
            }
            let started = triage.start(.manual)
            say("MALACHI_START triage: started \(started), can run \(triage.canRun), signed in \(String(describing: triage.signedIn))")
        }
    }

    private static func suggestReply(_ instruction: String, _ main: MainWindowController) {
        guard let id = main.board.state.selection,
              let c = main.board.source.snapshot.cases.first(where: { $0.id == id }) else {
            say("MALACHI_START suggest-reply: no case selected")
            return
        }
        let started = main.state.boardReply.start(c, instruction: instruction)
        say("MALACHI_START suggest-reply: started \(started) for '\(c.subject)', can run \(main.state.boardReply.canRun)")
    }

    /// `to|subject|text`: New Message with the recipient ("Name <address>"
    /// or an address), the subject and the text as paragraphs.
    private static func composeNew(_ value: String, _ main: MainWindowController) {
        guard let fields = DevelopmentStart.composeFields(value) else {
            say("MALACHI_START compose-new: expected to|subject|text")
            return
        }
        let html = fields.paragraphs.map { "<p>" + escapeText($0) + "</p>" }.joined()
        main.state.hooks.openCompose?(ComposeParams(
            kind: .new, to: fields.to.map { [$0] } ?? [], subject: fields.subject, bodyHTML: html))
        say("MALACHI_START compose-new: opened, to \(fields.to == nil ? 0 : 1), subject '\(fields.subject)', \(fields.paragraphs.count) paragraphs")
    }

    /// The newest visible compose window of the application (the one the
    /// step before opened; which window is key is not reliable while
    /// another application is active).
    private static func composeWindow() -> ComposeWindowController? {
        NSApp.windows.filter { $0.isVisible && $0.windowController is ComposeWindowController }
            .max { $0.windowNumber < $1.windowNumber }?.windowController as? ComposeWindowController
    }

    private static func rewrite(_ preset: String) {
        guard let r = DevelopmentStart.rewrite(preset) else {
            say("MALACHI_START compose-rewrite: unknown preset '\(preset)' (politer, shorter, fix, english)")
            return
        }
        guard let wc = composeWindow() else {
            say("MALACHI_START compose-rewrite: no compose window")
            return
        }
        // The editor loads its document in its own time: the popover opens
        // once the editor reports the text (at most ten seconds).
        func open(_ tries: Int) {
            wc.editor.rewriteTarget(attribution: "") { target in
                let text = target?.text.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                if text.isEmpty, tries < 20 {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { open(tries + 1) }
                    return
                }
                wc.showAssistantRewrite(nil)
                DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) {
                    guard let vc = wc.developmentRewrite else {
                        say("MALACHI_START compose-rewrite: no popover (assistant available \(wc.state.assistant.canRunInApp))")
                        return
                    }
                    let started = vc.controller.start(rewrite: r, custom: "", passage: vc.target.text)
                    say("MALACHI_START compose-rewrite: \(preset) started \(started) over \(vc.target.text.count) characters, selected \(vc.target.selected), after \(tries) waits")
                }
            }
        }
        open(0)
    }

    private static func write(_ instruction: String) {
        guard let wc = composeWindow() else {
            say("MALACHI_START compose-write: no compose window")
            return
        }
        wc.pane.composeAssistantBar.developmentWrite(instruction)
        say("MALACHI_START compose-write: asked in '\(wc.window?.title ?? "")', bar hidden \(wc.pane.composeAssistantBar.isHidden)")
    }
}
