// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The Triage control and the status strip (`Board.triageView`) and the
// triage's texts (BoardTriageText.swift).

private let t0 = Date(timeIntervalSince1970: 1_790_848_800)

private func inputs(
    shown: Bool = true, claudeFound: Bool = true, bridge: Bool = true, signedIn: Bool? = true,
    needsConsent: Bool = false, assistantOn: Bool = true, state: Board.TriageState = .idle, lastRun: Board.Run? = nil,
    autoTriage: Bool = false, pause: Board.AutoTriagePause? = nil, backendFailed: Bool = false,
    annotatedToday: Int? = nil, boardPhase: Board.Phase? = nil, signingIn: Bool = false, queue: Int? = nil
) -> Board.TriageViewInputs {
    Board.TriageViewInputs(
        shown: shown, claudeFound: claudeFound, bridge: bridge, signedIn: signedIn, needsConsent: needsConsent,
        assistantOn: assistantOn, state: state, lastRun: lastRun, autoTriage: autoTriage, pause: pause,
        backendFailed: backendFailed, annotatedToday: annotatedToday, boardPhase: boardPhase, signingIn: signingIn,
        queue: queue, now: t0)
}

@MainActor
@Suite struct BoardTriageViewTests {
    @Test func control() {
        let rows: [(String, Board.TriageViewInputs, Board.TriageControl, String, Bool)] = [
            ("ready", inputs(), .triage, "✦ Triage", true),
            ("sign-in not known", inputs(signedIn: nil), .triage, "✦ Triage", true),
            ("needs consent: still Triage", inputs(needsConsent: true), .triage, "✦ Triage", true),
            ("the assistant off", inputs(shown: false), .hidden, "", false),
            ("no Claude Code", inputs(claudeFound: false), .getClaudeCode, Assistant.signInTexts().getClaudeCode, true),
            ("no bridge", inputs(bridge: false), .unavailable, "✦ Triage", false),
            ("no preferences from the daemon", inputs(backendFailed: true), .unavailable, "✦ Triage", false),
            ("running wins over no preferences", inputs(state: .starting(.manual), backendFailed: true), .stop,
             Assistant.panelTexts().stop, true),
            ("signed out", inputs(signedIn: false), .signIn, Assistant.signInTexts().signIn, true),
            ("running", inputs(state: .running(.manual, done: 1, total: 3)), .stop, Assistant.panelTexts().stop, true),
            ("starting", inputs(state: .starting(.automatic)), .stop, Assistant.panelTexts().stop, true),
            ("running wins over a sign-in lost meanwhile",
             inputs(signedIn: false, state: .running(.automatic, done: 0, total: 1)), .stop, Assistant.panelTexts().stop, true),
        ]
        for (name, i, control, title, enabled) in rows {
            let v = Board.triageView(i)
            #expect(v.control == control, "\(name)")
            #expect(v.title == title, "\(name)")
            #expect(v.enabled == enabled, "\(name)")
            #expect(v.needsConsent == i.needsConsent, "\(name)")
        }
        #expect(Board.triageView(inputs(bridge: false)).toolTip == "the Malachi Mail tools are not available to the assistant")
    }

    @Test func progressAndResult() {
        var v = Board.triageView(inputs(state: .running(.manual, done: 2, total: 12)))
        #expect(v.running && v.progress == "Triaging… 2 of 12" && v.result == "")
        #expect(v.statusLine == "The assistant is triaging the board… 2 of 12")
        // More accepted than expected (the queue grew) never shows more.
        v = Board.triageView(inputs(state: .running(.manual, done: 5, total: 3)))
        #expect(v.progress == "Triaging… 3 of 3")
        v = Board.triageView(inputs(state: .starting(.manual)))
        #expect(v.progress == "Starting the triage…" && v.statusLine == "The assistant is triaging the board…")
        v = Board.triageView(inputs(state: .finished(.manual, annotated: 4, at: t0)))
        #expect(!v.running && v.progress == "" && v.result == "Triage finished: 4 conversations refined.")
        #expect(Board.triageView(inputs(state: .finished(.manual, annotated: 1, at: t0))).result
            == "Triage finished: 1 conversation refined.")
        #expect(Board.triageView(inputs(state: .finished(.automatic, annotated: 0, at: t0))).result
            == "Triage finished. No conversation needed new notes.")
        #expect(Board.triageView(inputs(state: .failed(.manual, .cancelled, at: t0))).result == "Triage stopped.")
        #expect(Board.triageView(inputs(state: .failed(.manual, .notSignedIn, at: t0))).result
            == "Triage failed: Claude Code is not signed in.")
        // Every failure has its words.
        for f in Board.TriageFailure.allCases {
            #expect(!Board.Text.triageFailure(f).isEmpty, "\(f)")
        }
    }

    @Test func statusLine() {
        #expect(Board.triageView(inputs(assistantOn: false)).statusLine == "Sorted by the daemon’s rules · assistant off")
        #expect(Board.triageView(inputs()).statusLine == "Triaged by rules · not refined by the assistant yet")
        let run = Board.Run(model: "claude-code", date: t0.addingTimeInterval(-5 * 60), trigger: "auto")
        #expect(Board.triageView(inputs(lastRun: run)).statusLine == "Triaged by rules · refined by the assistant 5 minutes ago")
        // Without the in-app assistant: no "assistant off" at all; notes
        // another client wrote still have their line.
        #expect(Board.triageView(inputs(shown: false, assistantOn: false)).statusLine == "")
        #expect(Board.triageView(inputs(shown: false, lastRun: run)).statusLine
            == "Triaged by rules · refined by the assistant 5 minutes ago")
    }

    /// Whether the view names a time that goes stale.
    @Test func relativeTimeFlag() {
        let run = Board.Run(model: "claude-code", date: t0.addingTimeInterval(-5 * 60), trigger: "auto")
        #expect(Board.triageView(inputs(lastRun: run)).relativeTime)
        #expect(!Board.triageView(inputs()).relativeTime)
        #expect(!Board.triageView(inputs(assistantOn: false, lastRun: run)).relativeTime)
        #expect(!Board.triageView(inputs(state: .running(.manual, done: 1, total: 2), lastRun: run)).relativeTime)
        let until = t0.addingTimeInterval(3600)
        #expect(Board.triageView(inputs(autoTriage: true, pause: .failed(.timeout, until: until))).relativeTime)
        #expect(!Board.triageView(inputs(autoTriage: true, pause: .signedOut)).relativeTime)
    }

    /// Today's automatic count, while automatic triage is on.
    @Test func today() {
        var v = Board.triageView(inputs(autoTriage: true, annotatedToday: 1))
        #expect(v.annotatedToday == 1 && v.todayLine == "1 conversation triaged automatically today")
        v = Board.triageView(inputs(autoTriage: true, annotatedToday: 12))
        #expect(v.todayLine == "12 conversations triaged automatically today")
        v = Board.triageView(inputs(autoTriage: false, annotatedToday: 12))
        #expect(v.annotatedToday == nil && v.todayLine == "")
        v = Board.triageView(inputs(autoTriage: true))
        #expect(v.annotatedToday == nil && v.todayLine == "")
    }

    @Test func refusedNotes() {
        #expect(Board.triageView(inputs(state: .finished(.manual, annotated: 3, refused: 2, at: t0))).result
            == "Triage finished: 3 conversations refined. The board refused 2 of the assistant’s notes.")
        #expect(Board.triageView(inputs(state: .failed(.automatic, .notesRefused, at: t0))).result
            == "Triage failed: the board refused the assistant’s notes.")
        #expect(Board.Text.autoTriagePaused(.failed(.noProgress, until: t0.addingTimeInterval(3600)), now: t0)
            == "Automatic triage paused: the assistant added no notes · next try in 1 hour")
    }

    @Test func paused() {
        let until = t0.addingTimeInterval(3600)
        #expect(Board.triageView(inputs(autoTriage: true, pause: .failed(.timeout, until: until))).paused
            == "Automatic triage paused: it took too long · next try in 1 hour")
        #expect(Board.triageView(inputs(autoTriage: true, pause: .signedOut)).paused
            == "Automatic triage paused: Claude Code is not signed in")
        #expect(Board.triageView(inputs(autoTriage: true, pause: .noConsent)).paused
            == "Automatic triage paused: sending mail to the assistant is not allowed")
        #expect(Board.triageView(inputs(autoTriage: true, pause: .unavailable)).paused
            == "Automatic triage paused: the assistant cannot run")
        // Not with automatic triage off, nor while a run works, nor with the
        // assistant hidden.
        #expect(Board.triageView(inputs(autoTriage: false, pause: .signedOut)).paused == "")
        #expect(Board.triageView(inputs(state: .starting(.manual), autoTriage: true, pause: .signedOut)).paused == "")
        #expect(Board.triageView(inputs(shown: false, autoTriage: true, pause: .signedOut)).paused == "")
    }

    @Test func relativeTimes() {
        let past: [(TimeInterval, String)] = [
            (0, "just now"), (59, "just now"), (60, "1 minute ago"), (5 * 60, "5 minutes ago"), (3600, "1 hour ago"),
            (5 * 3600, "5 hours ago"), (86400, "yesterday"), (3 * 86400, "3 days ago"), (-30, "just now"),
        ]
        for (ago, want) in past {
            #expect(Board.Text.relativeTime(t0.addingTimeInterval(-ago), now: t0) == want, "\(ago)")
        }
        let future: [(TimeInterval, String)] = [
            (0, "now"), (90, "in 1 minute"), (20 * 60, "in 20 minutes"), (3600, "in 1 hour"), (4 * 3600, "in 4 hours"),
            (86400, "tomorrow"), (2 * 86400, "in 2 days"),
        ]
        for (ahead, want) in future {
            #expect(Board.Text.relativeFuture(t0.addingTimeInterval(ahead), now: t0) == want, "\(ahead)")
        }
    }

    @Test func runErrors() {
        #expect(Board.TriageFailure.cancelled.runError == .cancelled)
        #expect(Board.TriageFailure.timeout.runError == .timeout)
        #expect(Board.TriageFailure.notSignedIn.runError == .signedOut)
        for f in [Board.TriageFailure.notFound, .toolsMissing, .declined, .assistantOff, .backend, .stopped, .nothingToDo,
                  .notesRefused, .noProgress] {
            #expect(f.runError == .failed, "\(f)")
        }
        #expect(Board.TriageTrigger.manual.wire == .manual && Board.TriageTrigger.automatic.wire == .auto)
    }

    /// A board the daemon has turned off, or does not have (an older
    /// daemon), offers no triage: one rule for the toolbar and Settings.
    @Test func boardGone() {
        let run = Board.Run(model: "claude-code", date: t0.addingTimeInterval(-5 * 60), trigger: "auto")
        for phase in [Board.Phase.off, .unsupported] {
            let v = Board.triageView(inputs(lastRun: run, autoTriage: true, pause: .signedOut, annotatedToday: 3,
                                            boardPhase: phase))
            #expect(v.control == .hidden && !v.offered && !v.enabled && v.title == "", "\(phase)")
            #expect(v.statusLine == "" && v.paused == "" && v.todayLine == "" && !v.relativeTime, "\(phase)")
            // A run under way keeps its Stop.
            #expect(Board.triageView(inputs(state: .starting(.manual), boardPhase: phase)).control == .stop, "\(phase)")
        }
        for phase in [Board.Phase.ready, .preparing, .loading, .failed, .unavailable] {
            #expect(Board.triageView(inputs(boardPhase: phase)).control == .triage, "\(phase)")
        }
        #expect(Board.triageView(inputs()).offered)
        #expect(!Board.triageView(inputs(shown: false)).offered)
    }

    /// While the application's sign-in waits for the browser, the control
    /// cannot start a second one and says why.
    @Test func signingIn() {
        let waiting = Assistant.signInTexts().waiting
        var v = Board.triageView(inputs(signedIn: false, signingIn: true))
        #expect(v.control == .signIn && !v.enabled && v.signingIn)
        #expect(v.title == Assistant.signInTexts().signIn && v.toolTip == waiting)
        // Also while a sign-in started elsewhere replaces a known one.
        v = Board.triageView(inputs(signedIn: true, signingIn: true))
        #expect(v.control == .signIn && !v.enabled)
        // Without it: Sign In… can be clicked.
        v = Board.triageView(inputs(signedIn: false))
        #expect(v.control == .signIn && v.enabled && !v.signingIn)
        // A run, a missing Claude Code or bridge come first.
        #expect(Board.triageView(inputs(state: .starting(.manual), signingIn: true)).control == .stop)
        #expect(Board.triageView(inputs(claudeFound: false, signingIn: true)).control == .getClaudeCode)
        #expect(Board.triageView(inputs(bridge: false, signingIn: true)).control == .unavailable)
        #expect(Board.triageView(inputs(shown: false, signingIn: true)).control == .hidden)
        #expect(Board.triageStripText(Board.triageView(inputs(signedIn: false, signingIn: true)), mode: .board,
                                      phase: .ready) == waiting)
    }

    /// The status strip's note by the window's mode and the board's phase.
    @Test func stripText() {
        let run = Board.Run(model: "claude-code", date: t0.addingTimeInterval(-5 * 60), trigger: "auto")
        let idle = Board.triageView(inputs(lastRun: run))
        let running = Board.triageView(inputs(state: .running(.manual, done: 1, total: 4)))
        let starting = Board.triageView(inputs(state: .starting(.automatic)))
        let paused = Board.triageView(inputs(lastRun: run, autoTriage: true, pause: .noConsent))
        let line = "Triaged by rules · refined by the assistant 5 minutes ago"
        #expect(Board.triageStripText(idle, mode: .mail, phase: .ready) == "")
        #expect(Board.triageStripText(running, mode: .mail, phase: .ready) == "Triaging… 1 of 4")
        #expect(Board.triageStripText(idle, mode: .board, phase: .ready) == line)
        #expect(Board.triageStripText(running, mode: .board, phase: .ready) == "Triaging… 1 of 4")
        #expect(Board.triageStripText(starting, mode: .board, phase: .ready) == "Starting the triage…")
        #expect(Board.triageStripText(paused, mode: .board, phase: .ready)
            == "Automatic triage paused: sending mail to the assistant is not allowed")
        for phase in [Board.Phase.off, .unsupported] {
            #expect(Board.triageStripText(idle, mode: .board, phase: phase) == "", "\(phase)")
            #expect(Board.triageStripText(paused, mode: .board, phase: phase) == "", "\(phase)")
            #expect(Board.triageStripText(running, mode: .board, phase: phase) == "Triaging… 1 of 4", "\(phase)")
        }
    }

    /// The conversations that wait for the assistant, after the line they
    /// belong to.
    @Test func waiting() {
        let run = Board.Run(model: "claude-code", date: t0.addingTimeInterval(-5 * 60), trigger: "auto")
        let refined = "Triaged by rules · refined by the assistant 5 minutes ago"
        let notYet = "Triaged by rules · not refined by the assistant yet"
        // Unknown or empty: nothing added.
        for q in [nil, 0] as [Int?] {
            let v = Board.triageView(inputs(lastRun: run, queue: q))
            #expect(v.waiting == "" && v.statusLine == refined, "\(String(describing: q))")
        }
        // Idle, refined or not yet, in the strip and in Settings.
        var v = Board.triageView(inputs(lastRun: run, queue: 1))
        #expect(v.waiting == "1 conversation waits for the assistant")
        #expect(v.statusLine == refined + " · 1 conversation waits for the assistant")
        #expect(Board.triageStripText(v, mode: .board, phase: .ready) == v.statusLine)
        #expect(Board.triageStripText(v, mode: .mail, phase: .ready) == "")
        v = Board.triageView(inputs(queue: 40))
        #expect(v.statusLine == notYet + " · 40 conversations wait for the assistant")
        v = Board.triageView(inputs(lastRun: run, autoTriage: true, annotatedToday: 2, queue: 3))
        #expect(Board.triageSettingsStatus(v)
            == refined + " · 3 conversations wait for the assistant · 2 conversations triaged automatically today")
        // After a run.
        v = Board.triageView(inputs(state: .finished(.manual, annotated: 5, at: t0), lastRun: run, queue: 7))
        #expect(v.statusLine == refined + " · 7 conversations wait for the assistant")
        // Paused, in the strip and in Settings.
        let until = t0.addingTimeInterval(3600)
        v = Board.triageView(inputs(lastRun: run, autoTriage: true, pause: .failed(.timeout, until: until), queue: 12))
        let paused = "Automatic triage paused: it took too long · next try in 1 hour · 12 conversations wait for the assistant"
        #expect(v.paused == paused)
        #expect(Board.triageStripText(v, mode: .board, phase: .ready) == paused)
        #expect(Board.triageSettingsStatus(v) == paused)
        v = Board.triageView(inputs(autoTriage: true, pause: .signedOut, queue: 2))
        #expect(v.paused == "Automatic triage paused: Claude Code is not signed in · 2 conversations wait for the assistant")
        // The board's notes off, or no board: no count.
        v = Board.triageView(inputs(assistantOn: false, queue: 5))
        #expect(v.waiting == "" && v.statusLine == "Sorted by the daemon’s rules · assistant off")
        v = Board.triageView(inputs(shown: false, assistantOn: false, queue: 5))
        #expect(v.waiting == "" && v.statusLine == "")
        for phase in [Board.Phase.off, .unsupported] {
            v = Board.triageView(inputs(lastRun: run, boardPhase: phase, queue: 5))
            #expect(v.waiting == "" && v.statusLine == "", "\(phase)")
        }
        // A run: only when more waits than it still has to do.
        v = Board.triageView(inputs(state: .running(.manual, done: 3, total: 5), queue: 40))
        #expect(v.progress == "Triaging… 3 of 5 · 40 conversations wait for the assistant")
        #expect(v.statusLine == "The assistant is triaging the board… 3 of 5 · 40 conversations wait for the assistant")
        #expect(Board.triageStripText(v, mode: .board, phase: .ready) == v.progress)
        #expect(Board.triageStripText(v, mode: .mail, phase: .ready) == v.progress)
        v = Board.triageView(inputs(state: .running(.manual, done: 3, total: 5), queue: 2))
        #expect(v.waiting == "" && v.progress == "Triaging… 3 of 5")
        v = Board.triageView(inputs(state: .running(.manual, done: 0, total: 0), queue: 9))
        #expect(v.waiting == "" && v.progress == "Triaging…")
        v = Board.triageView(inputs(state: .starting(.manual), queue: 9))
        #expect(v.waiting == "" && v.progress == "Starting the triage…")
    }

    /// The count picks its own plural form (the Czech catalogue's three).
    @Test func waitingPlurals() {
        #expect(Board.Text.triageWaiting(1) == "1 conversation waits for the assistant")
        #expect(Board.Text.triageWaiting(2) == "2 conversations wait for the assistant")
        #expect(Board.Text.triageWaiting(0) == "0 conversations wait for the assistant")
    }

    /// Settings' Board group: its status row and its description.
    @Test func settingsTexts() {
        let run = Board.Run(model: "claude-code", date: t0.addingTimeInterval(-5 * 60), trigger: "auto")
        let line = "Triaged by rules · refined by the assistant 5 minutes ago"
        #expect(Board.triageSettingsStatus(Board.triageView(inputs(lastRun: run))) == line)
        #expect(Board.triageSettingsStatus(Board.triageView(inputs(lastRun: run, autoTriage: true, annotatedToday: 1)))
            == line + " · 1 conversation triaged automatically today")
        #expect(Board.triageSettingsStatus(Board.triageView(inputs(lastRun: run, autoTriage: true, pause: .signedOut,
                                                                    annotatedToday: 1)))
            == "Automatic triage paused: Claude Code is not signed in")
        #expect(Board.triageSettingsStatus(Board.triageView(inputs(shown: false, assistantOn: false))) == "")

        let rows: [(String, Board.TriageViewInputs, String)] = [
            ("ready", inputs(), ""),
            ("running", inputs(state: .starting(.manual)), ""),
            ("no Claude Code", inputs(claudeFound: false),
             "The triage runs your Claude Code, which was not found on this computer. The Claude Code row above offers to get it."),
            ("signed out", inputs(signedIn: false),
             "Claude Code is not signed in. The Claude Code row above offers to sign in."),
            ("signing in", inputs(signedIn: false, signingIn: true), Assistant.signInTexts().waiting),
            ("no bridge", inputs(bridge: false),
             "The Malachi Mail tools are not available to the assistant, so the board cannot be triaged."),
            ("no preferences", inputs(backendFailed: true),
             "The mail backend did not answer with the board’s settings, so they cannot be changed now."),
        ]
        for (name, i, want) in rows {
            #expect(Board.triageSettingsDescription(Board.triageView(i)) == want, "\(name)")
        }
        let v = Board.triageView(inputs(backendFailed: true))
        #expect(v.unavailable == .backend && Board.triageView(inputs(bridge: false)).unavailable == .toolsMissing)
        #expect(Board.triageView(inputs()).unavailable == nil)
    }

    @Test func settingsChoices() {
        let minutes: [(Int, String)] = [(1, "1 minute"), (15, "15 minutes"), (60, "1 hour"), (90, "90 minutes"),
                                        (180, "3 hours")]
        for (m, want) in minutes {
            #expect(Board.Text.triageInterval(minutes: m) == want, "\(m)")
        }
        #expect(Board.Text.triageDailyCap(60) == "Up to 60" && Board.Text.triageDailyCap(0) == "None")
        // The switch's subtitle says what the sheet says.
        let sub = Board.Text.triageSettingsConsentSubtitle
        let body = Board.Text.triageConsentBody
        for words in ["Anthropic", "through your Claude Code", "cannot send, move or delete mail",
                      "write replies, which stay on the board"] {
            #expect(sub.contains(words) && body.contains(words), "\(words)")
        }
        // Suggested replies stay on the board, out of the Drafts folder.
        #expect(!sub.contains("reply drafts") && !body.contains("reply drafts"))
    }

    /// Settings' row of the tokens of the last 24 hours: shown with the
    /// Board group once a board.list said what they were; the sum grouped
    /// for the locale, the split and the runs as detail, "None" without.
    @Test func usageRow() {
        let en = Locale(identifier: "en_US")
        func view(_ i: Board.TriageViewInputs) -> Board.TriageView { Board.triageView(i) }
        var i = inputs()
        #expect(!view(i).usageShown, "not known before a board.list")
        i.usageKnown = true
        i.locale = en
        #expect(view(i).usageShown && view(i).usageValue == "None" && view(i).usageDetail.isEmpty)
        #expect(view(i).usageToolTip == "Counts only the triage runs Malachi Mail started, not those of other assistants")
        i.usage24h = BoardUsageTotal(
            inputTokens: 1200, outputTokens: 340, cacheCreationInputTokens: 5, cacheReadInputTokens: 1_234_567, runs: 1)
        #expect(view(i).usageValue == "1,236,112")
        #expect(view(i).usageDetail
            == "Input 1,200 · output 340 · written to cache 5 · read from cache 1,234,567\nFrom 1 triage run")
        i.usage24h?.runs = 3
        #expect(view(i).usageDetail.hasSuffix("\nFrom 3 triage runs"))
        // Not offered: no row.
        i.shown = false
        #expect(!view(i).usageShown)
        // Grouped for the locale; a sum beyond Int64 stops there.
        let cs = Board.triageUsageTexts(BoardUsageTotal(inputTokens: 1_234_567, runs: 1), locale: Locale(identifier: "cs_CZ"))
        #expect(cs.value.filter(\.isNumber) == "1234567" && cs.value != "1234567")
        let huge = Board.triageUsageTexts(
            BoardUsageTotal(inputTokens: Int64.max, outputTokens: Int64.max, cacheReadInputTokens: -5, runs: 0), locale: en)
        #expect(huge.value == "9,223,372,036,854,775,807")
        #expect(huge.detail.contains("read from cache 0\nFrom 1 triage run"))
    }
}
