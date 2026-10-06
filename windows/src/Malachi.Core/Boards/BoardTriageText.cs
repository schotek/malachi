// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardTriageText.swift; Go:
// ui/internal/board/triage.go, which holds the msgids.
//
// The texts of the board's triage run: its control, progress, outcome,
// the status strip, automatic triage's pause, the consent sheet and
// Settings → AI's Board group. Every text goes through L10n with the GTK
// msgid as the key. The buttons that exist already (Stop, Sign In…, Get
// Claude Code…, Allow, Cancel) are the assistant panel's. Swift's
// triageFailure(_:) is Go's TriageFailureText: a member of Board.Text
// named like the enum would hide it.

using System;
using System.Globalization;
using Malachi.Core.I18n;

namespace Malachi.Core.Boards;

public static partial class Board
{
    public static partial class Text
    {
        /// <summary>The Triage button's tooltip.</summary>
        public static string TriageToolTip =>
            L10n.T("Ask the assistant to read the conversations that wait for it and add notes");

        /// <summary>The button's tooltip while a run works.</summary>
        public static string TriageStopToolTip => L10n.T("Stop the assistant’s triage");

        /// <summary>The button's tooltip without Claude Code.</summary>
        public static string TriageNeedsClaudeCode =>
            L10n.T("The triage runs your Claude Code, which was not found on this Mac");

        /// <summary>The button's tooltip while Claude Code is signed out (the assistant panel's msgid).</summary>
        public static string TriageNeedsSignIn => L10n.T("Claude Code is not signed in");

        /// <summary>While the run starts.</summary>
        public static string TriageStarting => L10n.T("Starting the triage…");

        /// <summary>"Triaging… 3 of 12" ("Triaging…" without a total).</summary>
        public static string TriageProgress(int done, int total) =>
            total > 0 ? L10n.T("Triaging… %d of %d", Math.Min(done, total), total) : L10n.T("Triaging…");

        /// <summary>The strip while a run works (without the counts while it starts).</summary>
        public static string TriageRunningLine(int done, int total) =>
            total > 0
                ? L10n.T("The assistant is triaging the board… %d of %d", Math.Min(done, total), total)
                : L10n.T("The assistant is triaging the board…");

        /// <summary>How a run ended well; <paramref name="refused"/> notes the board refused, as a second sentence.</summary>
        public static string TriageFinished(int n, int refused = 0)
        {
            var done = n < 1
                ? L10n.T("Triage finished. No conversation needed new notes.")
                : L10n.N("Triage finished: %d conversation refined.", "Triage finished: %d conversations refined.", n);
            return refused <= 0 ? done : done + " " + TriageRefused(refused);
        }

        /// <summary>"The board refused 1 of the assistant’s notes." after a finished run's line.</summary>
        public static string TriageRefused(int n) =>
            L10n.N("The board refused %d of the assistant’s notes.", "The board refused %d of the assistant’s notes.", n);

        /// <summary>Cases automatic runs triaged today, for Settings' status row.</summary>
        public static string TriagedToday(int n) =>
            L10n.N("%d conversation triaged automatically today", "%d conversations triaged automatically today", n);

        /// <summary>Cases the assistant has not triaged yet, a segment of the status line after its " · ".</summary>
        public static string TriageWaiting(int n) =>
            L10n.N("%d conversation waits for the assistant", "%d conversations wait for the assistant", n);

        /// <summary>The outcome of a run the user stopped.</summary>
        public static string TriageStopped => L10n.T("Triage stopped.");

        /// <summary>How a run failed: "Triage failed: Claude Code is not signed in."</summary>
        public static string TriageFailed(TriageFailure f) => L10n.T("Triage failed: %s.", TriageFailureText(f));

        /// <summary>Why a run failed, inside a sentence (Swift <c>triageFailure</c>).</summary>
        public static string TriageFailureText(TriageFailure f) => f switch
        {
            TriageFailure.NotSignedIn => TriageNeedsSignIn,
            TriageFailure.NotFound => L10n.T("Claude Code was not found"),
            TriageFailure.ToolsMissing => L10n.T("the Malachi Mail tools are not available to the assistant"),
            TriageFailure.Timeout => L10n.T("it took too long"),
            TriageFailure.Cancelled => L10n.T("it was stopped"),
            TriageFailure.Declined => L10n.T("sending mail to the assistant was not allowed"),
            TriageFailure.AssistantOff => L10n.T("the assistant is off"),
            TriageFailure.Backend => L10n.T("the mail backend did not answer"),
            TriageFailure.Stopped => L10n.T("the assistant stopped"),
            TriageFailure.NothingToDo => L10n.T("no conversation waits for the assistant"),
            TriageFailure.NotesRefused => L10n.T("the board refused the assistant’s notes"),
            TriageFailure.NoProgress => L10n.T("the assistant added no notes"),
            _ => "",
        };

        /// <summary>The status strip when no run works: who sorted the board, and when the assistant last refined it.</summary>
        public static string TriageStatusLine(bool assistantOn, Run? lastRun, DateTimeOffset now)
        {
            if (!assistantOn)
            {
                return AssistantOffLine;
            }
            if (lastRun is null || lastRun.Running)
            {
                return L10n.T("Triaged by rules · not refined by the assistant yet");
            }
            return L10n.T("Triaged by rules · refined by the assistant %s", RelativeTime(lastRun.Date, now));
        }

        /// <summary>"Automatic triage paused: …".</summary>
        public static string AutoTriagePaused(AutoTriagePause p, DateTimeOffset now)
        {
            ArgumentNullException.ThrowIfNull(p);
            if (p is AutoTriagePause.Failed f)
            {
                return L10n.T(
                    "Automatic triage paused: %s · next try %s", TriageFailureText(f.Failure), RelativeFuture(f.Until, now));
            }
            var why = p switch
            {
                AutoTriagePause.SignedOut => TriageNeedsSignIn,
                AutoTriagePause.Unavailable => L10n.T("the assistant cannot run"),
                _ => L10n.T("sending mail to the assistant is not allowed"),
            };
            return L10n.T("Automatic triage paused: %s", why);
        }

        /// <summary>The consent sheet before the first triage (its buttons are the panel's Allow and Cancel).</summary>
        public static string TriageConsentHeading => L10n.T("Let the Assistant Triage the Board?");

        /// <summary>The consent sheet's body.</summary>
        public static string TriageConsentBody =>
            L10n.T("The assistant reads the conversations on the board that need notes and sends their text to Anthropic through your Claude Code, under your Claude account. It adds titles, summaries, tasks, deadlines and suggested replies to the board, and a triage you start yourself may also write replies, which stay on the board in Malachi Mail, not in your Drafts folder, until you send them. It cannot send, move or delete mail, and messages may contain instructions from their senders that it is told not to follow. You can turn this off in Settings.");

        // Settings → AI, the Board group.

        /// <summary>The consent switch's row.</summary>
        public static string TriageSettingsConsent => L10n.T("Let the assistant refine the board");

        /// <summary>The consent switch's subtitle: what the sheet says.</summary>
        public static string TriageSettingsConsentSubtitle =>
            L10n.T("Sends the newest messages of conversations that need sorting to Anthropic through your Claude Code. It cannot send, move or delete mail; a triage you start yourself may write replies, which stay on the board until you send them.");

        /// <summary>The row of automatic runs.</summary>
        public static string TriageSettingsAutomatic => L10n.T("Triage new mail automatically");

        /// <summary>The row of the interval between automatic runs; its value is <see cref="TriageInterval"/>.</summary>
        public static string TriageSettingsInterval => L10n.T("At most every");

        /// <summary>The row of the daily cap; its value is <see cref="TriageDailyCap"/>.</summary>
        public static string TriageSettingsDaily => L10n.T("Conversations a day");

        /// <summary>The group's description without Claude Code.</summary>
        public static string TriageSettingsNeedsClaudeCode =>
            L10n.T("The triage runs your Claude Code, which was not found on this Mac. The Claude Code row above offers to get it.");

        /// <summary>The group's description while Claude Code is signed out.</summary>
        public static string TriageSettingsNeedsSignIn =>
            L10n.T("Claude Code is not signed in. The Claude Code row above offers to sign in.");

        /// <summary>The group's description without the bridge.</summary>
        public static string TriageSettingsNoTools =>
            L10n.T("The Malachi Mail tools are not available to the assistant, so the board cannot be triaged.");

        /// <summary>The group's description without the board's preferences.</summary>
        public static string TriageSettingsNoBackend =>
            L10n.T("The mail backend did not answer with the board’s settings, so they cannot be changed now.");

        /// <summary>A value of "At most every": "15 minutes", "1 hour", "3 hours".</summary>
        public static string TriageInterval(int minutes) =>
            minutes >= 60 && minutes % 60 == 0
                ? L10n.N("%d hour", "%d hours", minutes / 60)
                : L10n.N("%d minute", "%d minutes", minutes);

        /// <summary>A value of "Conversations a day": "Up to 60"; 0 or less is "None".</summary>
        public static string TriageDailyCap(int cases) =>
            cases <= 0 ? L10n.C("daily cap", "None") : L10n.T("Up to %d", cases);

        /// <summary>The row of the tokens triage runs used in the last 24 hours.</summary>
        public static string TriageSettingsUsage => L10n.T("Tokens in the Last 24 Hours");

        /// <summary>That row's value when no run of the last 24 hours reported tokens.</summary>
        public static string TriageUsageNone => L10n.C("token usage", "None");

        /// <summary>That row's first line of detail: the tokens by kind, each formatted by <see cref="TriageTokens"/>.</summary>
        public static string TriageUsageSplit(string input, string output, string cacheWrite, string cacheRead) =>
            L10n.T("Input %s · output %s · written to cache %s · read from cache %s", input, output, cacheWrite, cacheRead);

        /// <summary>That row's second line of detail: how many triage runs the tokens come from.</summary>
        public static string TriageUsageRuns(int runs) => L10n.N("From %d triage run", "From %d triage runs", runs);

        /// <summary>That row's tooltip: whose runs count.</summary>
        public static string TriageUsageToolTip =>
            L10n.T("Counts only the triage runs Malachi Mail started, not those of other assistants");

        /// <summary>A number of tokens as a whole number grouped for <paramref name="locale"/> ("1,234,567"; "1 234 567" in Czech).</summary>
        public static string TriageTokens(long n, CultureInfo locale) => n.ToString("N0", locale);

        /// <summary>"just now", "5 minutes ago", "2 hours ago", "yesterday", "3 days ago".</summary>
        public static string RelativeTime(DateTimeOffset date, DateTimeOffset now)
        {
            var s = Math.Max(0, (long)(now - date).TotalSeconds);
            return s switch
            {
                < 60 => L10n.T("just now"),
                < 3600 => L10n.N("%d minute ago", "%d minutes ago", s / 60),
                < 86400 => L10n.N("%d hour ago", "%d hours ago", s / 3600),
                < 172_800 => L10n.T("yesterday"),
                _ => L10n.N("%d day ago", "%d days ago", s / 86400),
            };
        }

        /// <summary>"now", "in 5 minutes", "in 2 hours", "tomorrow", "in 3 days".</summary>
        public static string RelativeFuture(DateTimeOffset date, DateTimeOffset now)
        {
            var s = Math.Max(0, (long)(date - now).TotalSeconds);
            return s switch
            {
                < 60 => L10n.T("now"),
                < 3600 => L10n.N("in %d minute", "in %d minutes", s / 60),
                < 86400 => L10n.N("in %d hour", "in %d hours", s / 3600),
                < 172_800 => L10n.T("tomorrow"),
                _ => L10n.N("in %d day", "in %d days", s / 86400),
            };
        }
    }
}
