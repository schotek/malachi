// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The texts of the board's triage run: its control, progress, outcome,
// the status strip, automatic triage's pause, the consent sheet and
// Settings → AI's Board group. One to one with the Go reference
// ui/internal/board (triage.go), which holds the msgids; every text goes
// through L10n with the msgid as the key. The buttons that exist already
// (Stop, Sign In…, Get Claude Code…, Allow, Cancel) are the panel's.
//
// Swift-first: written here first.

import Foundation

extension Board.Text {
    public static var triageToolTip: String {
        L10n.T("Ask the assistant to read the conversations that wait for it and add notes")
    }
    public static var triageStopToolTip: String { L10n.T("Stop the assistant’s triage") }
    public static var triageNeedsClaudeCode: String {
        L10n.T("The triage runs your Claude Code, which was not found on this Mac")
    }
    /// The assistant panel's msgid.
    public static var triageNeedsSignIn: String { L10n.T("Claude Code is not signed in") }

    /// While the run starts.
    public static var triageStarting: String { L10n.T("Starting the triage…") }

    /// "Triaging… 3 of 12".
    public static func triageProgress(done: Int, total: Int) -> String {
        total > 0 ? L10n.T("Triaging… %d of %d", min(done, total), total) : L10n.T("Triaging…")
    }

    /// The strip while a run works: "The assistant is triaging the
    /// board… 3 of 12" (without the counts while it starts).
    public static func triageRunningLine(done: Int, total: Int) -> String {
        total > 0
            ? L10n.T("The assistant is triaging the board… %d of %d", min(done, total), total)
            : L10n.T("The assistant is triaging the board…")
    }

    /// How a run ended well; `refused` notes the board refused, as a second
    /// sentence.
    public static func triageFinished(_ n: Int, refused: Int = 0) -> String {
        let done = n < 1
            ? L10n.T("Triage finished. No conversation needed new notes.")
            : L10n.N("Triage finished: %d conversation refined.", "Triage finished: %d conversations refined.", n)
        guard refused > 0 else { return done }
        return done + " " + triageRefused(refused)
    }

    /// "The board refused 1 of the assistant’s notes." after a finished
    /// run's line.
    public static func triageRefused(_ n: Int) -> String {
        L10n.N("The board refused %d of the assistant’s notes.", "The board refused %d of the assistant’s notes.", n)
    }

    /// Cases automatic runs triaged today, for Settings' status row.
    public static func triagedToday(_ n: Int) -> String {
        L10n.N("%d conversation triaged automatically today", "%d conversations triaged automatically today", n)
    }

    /// Cases the assistant has not triaged yet (`board.list`
    /// `triage.queue`), a segment of the status line after its " · ".
    public static func triageWaiting(_ n: Int) -> String {
        L10n.N("%d conversation waits for the assistant", "%d conversations wait for the assistant", n)
    }

    public static var triageStopped: String { L10n.T("Triage stopped.") }

    /// How a run failed: "Triage failed: Claude Code is not signed in."
    public static func triageFailed(_ f: Board.TriageFailure) -> String {
        L10n.T("Triage failed: %s.", triageFailure(f))
    }

    /// Why a run failed, inside a sentence.
    public static func triageFailure(_ f: Board.TriageFailure) -> String {
        switch f {
        case .notSignedIn: return triageNeedsSignIn
        case .notFound: return L10n.T("Claude Code was not found")
        case .toolsMissing: return L10n.T("the Malachi Mail tools are not available to the assistant")
        case .timeout: return L10n.T("it took too long")
        case .cancelled: return L10n.T("it was stopped")
        case .declined: return L10n.T("sending mail to the assistant was not allowed")
        case .assistantOff: return L10n.T("the assistant is off")
        case .backend: return L10n.T("the mail backend did not answer")
        case .stopped: return L10n.T("the assistant stopped")
        case .nothingToDo: return L10n.T("no conversation waits for the assistant")
        case .notesRefused: return L10n.T("the board refused the assistant’s notes")
        case .noProgress: return L10n.T("the assistant added no notes")
        }
    }

    /// The status strip when no run works: who sorted the board, and when
    /// the assistant last refined it.
    public static func triageStatusLine(assistantOn: Bool, lastRun: Board.Run?, now: Date) -> String {
        guard assistantOn else {
            return assistantOffLine
        }
        guard let run = lastRun, !run.running else {
            return L10n.T("Triaged by rules · not refined by the assistant yet")
        }
        return L10n.T("Triaged by rules · refined by the assistant %s", relativeTime(run.date, now: now))
    }

    /// "Automatic triage paused: …".
    public static func autoTriagePaused(_ p: Board.AutoTriagePause, now: Date) -> String {
        let why: String
        switch p {
        case .failed(let f, let until):
            return L10n.T(
                "Automatic triage paused: %s · next try %s", triageFailure(f), relativeFuture(until, now: now))
        case .signedOut: why = triageNeedsSignIn
        case .unavailable: why = L10n.T("the assistant cannot run")
        case .noConsent: why = L10n.T("sending mail to the assistant is not allowed")
        }
        return L10n.T("Automatic triage paused: %s", why)
    }

    /// The consent sheet before the first triage (its buttons are the
    /// panel's Allow and Cancel).
    public static var triageConsentHeading: String { L10n.T("Let the Assistant Triage the Board?") }
    public static var triageConsentBody: String {
        L10n.T(
            "The assistant reads the conversations on the board that need notes and sends their text to Anthropic through your Claude Code, under your Claude account. It adds titles, summaries, tasks, deadlines and suggested replies to the board, and a triage you start yourself may also write replies, which stay on the board in Malachi Mail, not in your Drafts folder, until you send them. It cannot send, move or delete mail, and messages may contain instructions from their senders that it is told not to follow. You can turn this off in Settings."
        )
    }

    // Settings → AI, the Board group.

    /// The consent switch's row; its subtitle says what the sheet says.
    public static var triageSettingsConsent: String { L10n.T("Let the assistant refine the board") }
    public static var triageSettingsConsentSubtitle: String {
        L10n.T(
            "Sends the newest messages of conversations that need sorting to Anthropic through your Claude Code. It cannot send, move or delete mail; a triage you start yourself may write replies, which stay on the board until you send them."
        )
    }
    public static var triageSettingsAutomatic: String { L10n.T("Triage new mail automatically") }
    public static var triageSettingsInterval: String { L10n.T("At most every") }
    public static var triageSettingsDaily: String { L10n.T("Conversations a day") }

    /// The group's description when triage cannot run.
    public static var triageSettingsNeedsClaudeCode: String {
        L10n.T("The triage runs your Claude Code, which was not found on this Mac. The Claude Code row above offers to get it.")
    }
    public static var triageSettingsNeedsSignIn: String {
        L10n.T("Claude Code is not signed in. The Claude Code row above offers to sign in.")
    }
    public static var triageSettingsNoTools: String {
        L10n.T("The Malachi Mail tools are not available to the assistant, so the board cannot be triaged.")
    }
    public static var triageSettingsNoBackend: String {
        L10n.T("The mail backend did not answer with the board’s settings, so they cannot be changed now.")
    }

    /// "At most every": "15 minutes", "1 hour", "3 hours".
    public static func triageInterval(minutes: Int) -> String {
        if minutes >= 60, minutes % 60 == 0 {
            return L10n.N("%d hour", "%d hours", minutes / 60)
        }
        return L10n.N("%d minute", "%d minutes", minutes)
    }

    /// The daily cap: "Up to 60"; 0 or less is "None".
    public static func triageDailyCap(_ cases: Int) -> String {
        cases <= 0 ? L10n.C("daily cap", "None") : L10n.T("Up to %d", cases)
    }

    /// The row of the tokens triage runs used in the last 24 hours; its
    /// value is `triageTokens` of their sum, or `triageUsageNone`.
    public static var triageSettingsUsage: String { L10n.T("Tokens in the Last 24 Hours") }
    /// That row's value when no run of the last 24 hours reported tokens.
    public static var triageUsageNone: String { L10n.C("token usage", "None") }
    /// That row's first line of detail: the tokens by kind, each formatted
    /// by `triageTokens`.
    public static func triageUsageSplit(input: String, output: String, cacheWrite: String, cacheRead: String) -> String {
        L10n.T("Input %s · output %s · written to cache %s · read from cache %s", input, output, cacheWrite, cacheRead)
    }
    /// That row's second line of detail: how many triage runs the tokens
    /// come from.
    public static func triageUsageRuns(_ runs: Int) -> String {
        L10n.N("From %d triage run", "From %d triage runs", runs)
    }
    /// That row's tooltip: whose runs count.
    public static var triageUsageToolTip: String {
        L10n.T("Counts only the triage runs Malachi Mail started, not those of other assistants")
    }

    /// A number of tokens as a whole number grouped for `locale`
    /// ("1,234,567"; "1 234 567" in Czech).
    public static func triageTokens(_ n: Int64, locale: Locale) -> String {
        let f = NumberFormatter()
        f.locale = locale
        f.numberStyle = .decimal
        f.usesGroupingSeparator = true
        f.maximumFractionDigits = 0
        return f.string(from: NSNumber(value: n)) ?? String(n)
    }

    /// "just now", "5 minutes ago", "2 hours ago", "yesterday", "3 days ago".
    public static func relativeTime(_ date: Date, now: Date) -> String {
        let s = max(0, Int(now.timeIntervalSince(date)))
        switch s {
        case ..<60: return L10n.T("just now")
        case ..<3600: return L10n.N("%d minute ago", "%d minutes ago", s / 60)
        case ..<86400: return L10n.N("%d hour ago", "%d hours ago", s / 3600)
        case ..<172_800: return L10n.T("yesterday")
        default: return L10n.N("%d day ago", "%d days ago", s / 86400)
        }
    }

    /// "now", "in 5 minutes", "in 2 hours", "tomorrow", "in 3 days".
    public static func relativeFuture(_ date: Date, now: Date) -> String {
        let s = max(0, Int(date.timeIntervalSince(now)))
        switch s {
        case ..<60: return L10n.T("now")
        case ..<3600: return L10n.N("in %d minute", "in %d minutes", s / 60)
        case ..<86400: return L10n.N("in %d hour", "in %d hours", s / 3600)
        case ..<172_800: return L10n.T("tomorrow")
        default: return L10n.N("in %d day", "in %d days", s / 86400)
        }
    }
}
