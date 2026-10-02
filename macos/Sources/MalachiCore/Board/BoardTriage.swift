// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The board's triage run as the UI sees it: the run's state
// (`BoardTriageController`), why one failed, why automatic triage pauses
// (`BoardAutoTriageScheduler`), and the view model of the Triage control
// and the status strip (`Board.triageView`). Pure; the texts are
// `Board.Text`'s. A run's outcome is only counts and classes: neither the
// model's words nor mail text ever get here.
//
// Swift-first: the GTK port follows with the board.

import Foundation

extension Board {
    /// Why a triage run brought nothing, or ended early.
    public enum TriageFailure: Sendable, Equatable, CaseIterable {
        /// Claude Code is signed out, or the API refused its sign-in.
        case notSignedIn
        /// Claude Code was not found on this computer.
        case notFound
        /// The bridge is not beside the application, or Claude Code did not
        /// report it connected.
        case toolsMissing
        /// The run took longer than `Assistant.triageTimeout`.
        case timeout
        /// The user stopped it.
        case cancelled
        /// The user declined the consent question.
        case declined
        /// The assistant is off (Settings → AI, or not registered).
        case assistantOff
        /// The mail backend refused or did not answer (board.runStart, the
        /// preferences).
        case backend
        /// Claude Code ended badly (its result was an error, it exited).
        case stopped
        /// The queue was empty: nothing waited for the assistant.
        case nothingToDo
        /// The run ended with no note accepted and at least one refused
        /// (annotate_case results that were errors: a conflict, a quote
        /// not found, an invalid argument).
        case notesRefused
        /// An automatic run ended with no note accepted and none refused
        /// although its queue was known to have cases: the assistant did
        /// not annotate. Counts for the back-off, so that such a run does
        /// not repeat at every interval.
        case noProgress

        /// The class `board.runEnd` records (`BoardRunError`).
        public var runError: BoardRunError {
            switch self {
            case .cancelled: return .cancelled
            case .timeout: return .timeout
            case .notSignedIn: return .signedOut
            default: return .failed
            }
        }
    }

    /// Where the app's triage run is.
    public enum TriageState: Sendable, Equatable {
        /// No run since the application started.
        case idle
        /// Checking what it needs (consent, Claude Code, the sign-in) and
        /// starting the run.
        case starting(TriageTrigger)
        /// Claude Code works: `done` cases annotated of `total` expected.
        case running(TriageTrigger, done: Int, total: Int)
        /// It ended well, with `annotated` cases annotated and `refused`
        /// notes the board refused (annotate_case results that were errors).
        case finished(TriageTrigger, annotated: Int, refused: Int = 0, at: Date)
        case failed(TriageTrigger, TriageFailure, at: Date)

        /// A run is starting or running.
        public var isActive: Bool {
            switch self {
            case .starting, .running: return true
            case .idle, .finished, .failed: return false
            }
        }

        public var trigger: TriageTrigger? {
            switch self {
            case .idle: return nil
            case .starting(let t), .running(let t, _, _), .finished(let t, _, _, _), .failed(let t, _, _): return t
            }
        }
    }

    /// Why automatic triage does not run although it is on (the status
    /// strip's "Automatic triage paused: …").
    public enum AutoTriagePause: Sendable, Equatable {
        /// The last automatic runs failed: the next try waits (`until`).
        case failed(TriageFailure, until: Date)
        case signedOut
        case unavailable
        case noConsent
    }

    /// What the Triage control is.
    public enum TriageControl: Sendable, Equatable {
        /// No control: the assistant is off.
        case hidden
        /// Claude Code is missing: the control offers *Get Claude Code…*
        /// (`Assistant.installURL` in the browser).
        case getClaudeCode
        /// Claude Code is signed out: *Sign In…* (`ClaudeCodeLocator.startSignIn`).
        case signIn
        /// The bridge is missing, or the daemon does not answer the board's
        /// preferences: shown, insensitive, `toolTip` says why.
        case unavailable
        /// Ready: a click starts a manual run (which asks for consent
        /// first when `needsConsent`).
        case triage
        /// A run is under way: a click stops it.
        case stop
    }

    /// What `triageView` reads.
    public struct TriageViewInputs: Sendable, Equatable {
        public var provider: AssistantProviderID = .claude
        /// The assistant is shown (Settings → AI, registered).
        public var shown: Bool
        public var claudeFound: Bool
        /// `malachi-mcp` is beside the application.
        public var bridge: Bool
        public var signedIn: Bool?
        /// The triage would ask for consent first.
        public var needsConsent: Bool
        /// The board's assistant preference is on (its notes count).
        public var assistantOn: Bool
        public var state: TriageState
        /// The daemon's last run (`board.list` `triage.lastRun`).
        public var lastRun: Run?
        public var autoTriage: Bool
        public var pause: AutoTriagePause?
        /// The daemon did not answer the board's preferences, and none are
        /// known: a run could neither take a consent nor start.
        public var backendFailed: Bool
        /// Cases automatic runs annotated today; nil when not known for
        /// today.
        public var annotatedToday: Int?
        /// The board's phase as far as it decides whether triage exists:
        /// `.off` (turned off in the daemon) and `.unsupported` (an older
        /// daemon without the board) hide the control; nil and any other
        /// phase do not.
        public var boardPhase: Phase?
        /// Claude Code's sign-in in the browser is under way (the
        /// application's one, whoever started it).
        public var signingIn: Bool
        /// A board.list has said what the triage used in the last 24 hours
        /// (`usage24h`, nil when no run reported any).
        public var usageKnown: Bool
        public var usage24h: BoardUsageTotal?
        /// Cases waiting for the assistant (`board.list` `triage.queue`,
        /// uncapped); nil while not known or the board's notes are off.
        public var queue: Int?
        /// How numbers are grouped.
        public var locale: Locale
        public var now: Date

        public init(
            shown: Bool, claudeFound: Bool, bridge: Bool, signedIn: Bool?, needsConsent: Bool, assistantOn: Bool,
            state: TriageState, lastRun: Run?, autoTriage: Bool, pause: AutoTriagePause?, backendFailed: Bool = false,
            annotatedToday: Int? = nil, boardPhase: Phase? = nil, signingIn: Bool = false, usageKnown: Bool = false,
            usage24h: BoardUsageTotal? = nil, queue: Int? = nil, locale: Locale = .current, now: Date, provider: AssistantProviderID = .claude
        ) {
            self.provider = provider
            self.shown = shown
            self.claudeFound = claudeFound
            self.bridge = bridge
            self.signedIn = signedIn
            self.needsConsent = needsConsent
            self.assistantOn = assistantOn
            self.state = state
            self.lastRun = lastRun
            self.autoTriage = autoTriage
            self.pause = pause
            self.backendFailed = backendFailed
            self.annotatedToday = annotatedToday
            self.boardPhase = boardPhase
            self.signingIn = signingIn
            self.usageKnown = usageKnown
            self.usage24h = usage24h
            self.queue = queue
            self.locale = locale
            self.now = now
        }
    }

    /// The Triage control and the status strip.
    public struct TriageView: Sendable, Equatable {
        public var provider: AssistantProviderID = .claude
        /// `.hidden` exactly when triage is not offered: the toolbar item
        /// and Settings' Board group follow this one rule (`offered`).
        public var control: TriageControl
        /// The control's title ("" when hidden).
        public var title: String
        public var enabled: Bool
        public var toolTip: String
        /// A click asks for consent first.
        public var needsConsent: Bool
        public var running: Bool
        /// "Triaging… 3 of 12" while a run works; "" otherwise.
        public var progress: String
        /// The strip's line: who sorted the board and when the assistant
        /// last refined it, or the run's progress. "" while triage is not
        /// offered (`control == .hidden`) and the board's notes are off:
        /// a user without the in-app assistant gets no "assistant off".
        public var statusLine: String
        /// The line names a time relative to `now` ("5 minutes ago",
        /// "next try in 1 hour"), which goes stale as time passes.
        public var relativeTime: Bool
        /// How the app's last run ended ("" before one, and while one runs).
        public var result: String
        /// "Automatic triage paused: …", or "". Both it and `statusLine`
        /// end with `waiting` after " · " when that is not "".
        public var paused: String
        /// "3 conversations wait for the assistant" while the queue is known
        /// and not empty (and, during a run, has cases beyond what the run
        /// still has to do), else "". Already part of `statusLine`,
        /// `paused` and `progress`.
        public var waiting: String
        /// Cases automatic runs annotated today while automatic triage is
        /// on and the count is known for today; nil otherwise.
        public var annotatedToday: Int?
        /// `annotatedToday` as "12 conversations triaged automatically
        /// today", or "".
        public var todayLine: String
        /// Why the control is `.unavailable` (`.toolsMissing`, `.backend`);
        /// nil otherwise.
        public var unavailable: TriageFailure?
        /// Claude Code's sign-in waits for the browser: the control is
        /// Sign In…, insensitive, and says so.
        public var signingIn: Bool
        /// Settings' row of the tokens of the last 24 hours shows: triage
        /// is offered and a board.list said what they were.
        public var usageShown: Bool
        /// That row's value: the four counters added up, grouped for the
        /// locale, or "None".
        public var usageValue: String
        /// That row's detail: the tokens by kind on one line, the runs they
        /// come from on the next; "" without usage.
        public var usageDetail: String
        /// That row's tooltip: only this application's runs count.
        public var usageToolTip: String

        /// Triage is offered: the toolbar shows its control and Settings
        /// its Board group.
        public var offered: Bool { control != .hidden }
    }

    /// The Triage control and the status strip for `i`.
    public static func triageView(_ i: TriageViewInputs) -> TriageView {
        var v = TriageView(
            control: .triage, title: Text.triage, enabled: true, toolTip: Text.triageToolTip,
            needsConsent: i.needsConsent, running: i.state.isActive, progress: "", statusLine: "", relativeTime: false,
            result: "", paused: "", waiting: "", annotatedToday: nil, todayLine: "", unavailable: nil, signingIn: false,
            usageShown: false, usageValue: "", usageDetail: "", usageToolTip: "")
        v.provider = i.provider
        // A board the daemon does not have, or has turned off, has nothing
        // to triage; a run under way keeps its Stop (losing the board
        // stops it anyway).
        let boardGone = i.boardPhase == .off || i.boardPhase == .unsupported
        if !i.shown || (boardGone && !i.state.isActive) {
            v.control = .hidden
            v.title = ""
            v.enabled = false
            v.toolTip = ""
        } else if i.state.isActive {
            v.control = .stop
            v.title = Assistant.panelTexts().stop
            v.toolTip = Text.triageStopToolTip
        } else if !i.claudeFound {
            v.control = .getClaudeCode
            v.title = i.provider == .chatgpt ? L10n.T("Get Codex…") : Assistant.signInTexts().getClaudeCode
            v.toolTip = i.provider == .chatgpt ? L10n.T("Codex was not found. Choose a native Codex executable.") : Text.triageNeedsClaudeCode
        } else if !i.bridge {
            v.control = .unavailable
            v.enabled = false
            v.toolTip = Text.triageFailure(.toolsMissing)
            v.unavailable = .toolsMissing
        } else if i.backendFailed {
            v.control = .unavailable
            v.enabled = false
            v.toolTip = Text.triageFailure(.backend)
            v.unavailable = .backend
        } else if i.signingIn {
            // One sign-in for the application: a click must not start a
            // second one (which would end the first as cancelled).
            v.control = .signIn
            v.title = i.provider == .chatgpt ? L10n.T("Continue with ChatGPT") : Assistant.signInTexts().signIn
            v.enabled = false
            v.toolTip = i.provider == .chatgpt ? L10n.T("Connecting…") : Assistant.signInTexts().waiting
            v.signingIn = true
        } else if i.signedIn == false {
            v.control = .signIn
            v.title = i.provider == .chatgpt ? L10n.T("Continue with ChatGPT") : Assistant.signInTexts().signIn
            v.toolTip = i.provider == .chatgpt ? L10n.T("Reconnect to ChatGPT") : Text.triageNeedsSignIn
        }
        switch i.state {
        case .starting:
            v.progress = Text.triageStarting
        case .running(_, let done, let total):
            v.progress = Text.triageProgress(done: done, total: total)
        case .finished(_, let n, let refused, _):
            v.result = Text.triageFinished(n, refused: refused)
        case .failed(_, let f, _):
            if i.provider == .chatgpt, f == .notFound || f == .notSignedIn {
                v.result = L10n.T("Triage failed: %s.", f == .notFound ? L10n.T("Codex was not found. Choose a native Codex executable.") : L10n.T("Reconnect to ChatGPT"))
            } else { v.result = f == .cancelled ? Text.triageStopped : Text.triageFailed(f) }
        case .idle:
            break
        }
        if case .running(_, let done, let total) = i.state {
            v.statusLine = Text.triageRunningLine(done: done, total: total)
        } else if i.state.isActive {
            v.statusLine = Text.triageRunningLine(done: 0, total: 0)
        } else if v.control == .hidden && boardGone {
            // No board: no line about it.
        } else if i.shown || i.assistantOn {
            // Notes another client wrote (Claude Code with the bridge's
            // triage tier) still count without the in-app assistant.
            v.statusLine = Text.triageStatusLine(assistantOn: i.assistantOn, lastRun: i.lastRun, now: i.now)
            if i.assistantOn, let run = i.lastRun, !run.running {
                v.relativeTime = true
            }
        }
        if v.offered, i.autoTriage, !i.state.isActive, let p = i.pause {
            v.paused = i.provider == .chatgpt && p == .signedOut
                ? L10n.T("Automatic triage paused: %s", L10n.T("Reconnect to ChatGPT")) : Text.autoTriagePaused(p, now: i.now)
            if case .failed = p {
                v.relativeTime = true
            }
        }
        // The queue, after the line it belongs to. During a run only when
        // something waits beyond what the run still has to do.
        if let q = i.queue, q > 0, i.assistantOn {
            switch i.state {
            case .running(_, let done, let total) where total > 0 && q > total - min(done, total):
                v.waiting = Text.triageWaiting(q)
                v.progress = joinedNote(v.progress, v.waiting)
                v.statusLine = joinedNote(v.statusLine, v.waiting)
            case .running, .starting:
                break
            case .idle, .finished, .failed:
                if !v.statusLine.isEmpty || !v.paused.isEmpty {
                    v.waiting = Text.triageWaiting(q)
                    v.statusLine = joinedNote(v.statusLine, v.waiting)
                    v.paused = joinedNote(v.paused, v.waiting)
                }
            }
        }
        if v.offered, i.autoTriage, let n = i.annotatedToday {
            v.annotatedToday = n
            v.todayLine = Text.triagedToday(n)
        }
        if v.offered, i.usageKnown {
            (v.usageValue, v.usageDetail) = triageUsageTexts(i.usage24h, locale: i.locale)
            v.usageShown = true
            v.usageToolTip = Text.triageUsageToolTip
        }
        return v
    }

    /// `line` and `note` joined as the status line joins its notes (" · "),
    /// or `line` alone when either is "".
    static func joinedNote(_ line: String, _ note: String) -> String {
        line.isEmpty || note.isEmpty ? line : line + " · " + note
    }

    /// The value and the detail of Settings' row of the tokens of the last
    /// 24 hours for `u`: the sum of the four counters (saturating) and the
    /// split with the runs; "None" and "" without usage.
    public static func triageUsageTexts(_ u: BoardUsageTotal?, locale: Locale) -> (value: String, detail: String) {
        guard let u else { return (Text.triageUsageNone, "") }
        let parts = [u.inputTokens, u.outputTokens, u.cacheCreationInputTokens, u.cacheReadInputTokens].map { max($0, 0) }
        var total: Int64 = 0
        for p in parts {
            let (sum, overflow) = total.addingReportingOverflow(p)
            total = overflow ? Int64.max : sum
        }
        let n = { Text.triageTokens($0, locale: locale) }
        let split = Text.triageUsageSplit(
            input: n(parts[0]), output: n(parts[1]), cacheWrite: n(parts[2]), cacheRead: n(parts[3]))
        return (n(total), [split, Text.triageUsageRuns(max(u.runs, 1))].joined(separator: "\n"))
    }

    /// The status strip's triage note for the window in `mode` with the
    /// board in `phase`: in Mail only a run's progress; in Board a run's
    /// progress, else the sign-in waiting for the browser, else why
    /// automatic triage pauses, else the status line; nothing in Board
    /// while the board is off or the daemon has none, unless a run works.
    public static func triageStripText(_ v: TriageView, mode: Mode, phase: Phase) -> String {
        switch mode {
        case .mail:
            return v.running ? v.progress : ""
        case .board:
            if v.running {
                return v.progress.isEmpty ? v.statusLine : v.progress
            }
            if phase == .off || phase == .unsupported {
                return ""
            }
            if v.signingIn {
                return v.provider == .chatgpt ? L10n.T("Connecting…") : Assistant.signInTexts().waiting
            }
            return v.paused.isEmpty ? v.statusLine : v.paused
        }
    }

    /// Settings' status row of the Board group: why automatic triage
    /// pauses, else the status line with today's automatic count.
    public static func triageSettingsStatus(_ v: TriageView) -> String {
        if !v.paused.isEmpty {
            return v.paused
        }
        return [v.statusLine, v.todayLine].filter { !$0.isEmpty }.joined(separator: " · ")
    }

    /// The description of Settings' Board group: why triage cannot run
    /// now, in whole sentences, or "" when it can.
    public static func triageSettingsDescription(_ v: TriageView) -> String {
        switch v.control {
        case .getClaudeCode:
            return v.provider == .chatgpt ? L10n.T("Codex was not found. Choose a native Codex executable.") : Text.triageSettingsNeedsClaudeCode
        case .signIn:
            return v.provider == .chatgpt ? (v.signingIn ? L10n.T("Connecting…") : L10n.T("Reconnect to ChatGPT")) : (v.signingIn ? Assistant.signInTexts().waiting : Text.triageSettingsNeedsSignIn)
        case .unavailable:
            return v.unavailable == .backend ? Text.triageSettingsNoBackend : Text.triageSettingsNoTools
        case .hidden, .triage, .stop:
            return ""
        }
    }
}
