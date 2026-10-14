// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardTriage.swift; Go:
// ui/internal/boardtriage/triage.go (State, Control, ViewInputs, View,
// ViewOf, UsageTexts, StripText, SettingsStatus, SettingsDescription) and
// ui/internal/board/triage.go (TriageFailure, Pause).
//
// The board's triage run as the UI sees it: the run's state, why one
// failed, why automatic triage pauses, and the view model of the Triage
// control and the status strip. Pure; the texts are Board.Text's. A run's
// outcome is only counts and classes: neither the model's words nor mail
// text ever get here. The ChatGPT branches are Swift's (the Windows client
// has the Codex provider; Go's view has no provider). Swift's
// Board.triageView and Board.TriageView share a name, which C# does not
// allow in one class: the function is Go's ViewOf, TriageViewOf. Swift's
// TriageState.trigger is TriggeredBy, beside the cases' own Trigger.

using System;
using System.Globalization;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.I18n;

namespace Malachi.Core.Boards;

public static partial class Board
{
    /// <summary>Why a triage run brought nothing, or ended early (Go <c>board.TriageFailure</c>).</summary>
    public enum TriageFailure
    {
        /// <summary>Claude Code is signed out, or the API refused its sign-in.</summary>
        NotSignedIn,

        /// <summary>Claude Code was not found on this computer.</summary>
        NotFound,

        /// <summary>The bridge is not beside the application, or Claude Code did not report it connected.</summary>
        ToolsMissing,

        /// <summary>The run took longer than the triage's timeout.</summary>
        Timeout,

        /// <summary>The user stopped it.</summary>
        Cancelled,

        /// <summary>The user declined the consent question.</summary>
        Declined,

        /// <summary>The assistant is off (Settings → AI, or not registered).</summary>
        AssistantOff,

        /// <summary>The mail backend refused or did not answer (board.runStart, the preferences).</summary>
        Backend,

        /// <summary>Claude Code ended badly (its result was an error, it exited).</summary>
        Stopped,

        /// <summary>The queue was empty: nothing waited for the assistant.</summary>
        NothingToDo,

        /// <summary>
        /// The run ended with no note accepted and at least one refused
        /// (annotate_case results that were errors).
        /// </summary>
        NotesRefused,

        /// <summary>
        /// An automatic run ended with no note accepted and none refused
        /// although its queue was known to have cases: the assistant did not
        /// annotate. Counts for the back-off.
        /// </summary>
        NoProgress,

        /// <summary>
        /// The assistant's usage limit (of the user's plan) was reached; the
        /// run ends as failed and the back-off doubles once.
        /// </summary>
        Limit,
    }

    /// <summary>The control the Triage button is.</summary>
    public enum TriageControl
    {
        /// <summary>No control: the assistant is off.</summary>
        Hidden,

        /// <summary>Claude Code is missing: the control offers Get Claude Code…</summary>
        GetClaudeCode,

        /// <summary>Claude Code is signed out: Sign In…</summary>
        SignIn,

        /// <summary>
        /// The bridge is missing, or the daemon does not answer the board's
        /// preferences: shown, insensitive, the tooltip says why.
        /// </summary>
        Unavailable,

        /// <summary>Ready: a click starts a manual run (which asks for consent first when it needs it).</summary>
        Triage,

        /// <summary>A run is under way: a click stops it.</summary>
        Stop,
    }

    /// <summary>Every <see cref="TriageFailure"/> (Swift <c>allCases</c>, Go <c>TriageFailures</c>).</summary>
    public static ReadOnlySpan<TriageFailure> TriageFailures =>
    [
        TriageFailure.NotSignedIn, TriageFailure.NotFound, TriageFailure.ToolsMissing, TriageFailure.Timeout,
        TriageFailure.Cancelled, TriageFailure.Declined, TriageFailure.AssistantOff, TriageFailure.Backend,
        TriageFailure.Stopped, TriageFailure.NothingToDo, TriageFailure.NotesRefused, TriageFailure.NoProgress,
        TriageFailure.Limit,
    ];

    /// <summary>Where the application's triage run is.</summary>
    public abstract record TriageState
    {
        // Only the cases below derive from it.
        private TriageState()
        {
        }

        /// <summary>A run is starting or running.</summary>
        public bool IsActive => this is Starting or Running;

        /// <summary>Who started the run; null while idle (Swift <c>trigger</c>).</summary>
        public TriageTrigger? TriggeredBy => this switch
        {
            Starting s => s.Trigger,
            Running r => r.Trigger,
            Finished f => f.Trigger,
            Failed f => f.Trigger,
            _ => null,
        };

        /// <summary>No run since the application started.</summary>
        public sealed record Idle : TriageState;

        /// <summary>Checking what it needs (consent, Claude Code, the sign-in) and starting the run.</summary>
        /// <param name="Trigger">Who started it.</param>
        public sealed record Starting(TriageTrigger Trigger) : TriageState;

        /// <summary>Claude Code works: <paramref name="Done"/> cases annotated of <paramref name="Total"/> expected.</summary>
        /// <param name="Trigger">Who started it.</param>
        /// <param name="Done">Cases annotated.</param>
        /// <param name="Total">Cases expected.</param>
        public sealed record Running(TriageTrigger Trigger, int Done, int Total) : TriageState;

        /// <summary>
        /// It ended well, with <paramref name="Annotated"/> cases annotated and
        /// <paramref name="Refused"/> notes the board refused.
        /// </summary>
        /// <param name="Trigger">Who started it.</param>
        /// <param name="Annotated">Cases annotated.</param>
        /// <param name="Refused">Notes refused.</param>
        /// <param name="At">When.</param>
        public sealed record Finished(TriageTrigger Trigger, int Annotated, int Refused, DateTimeOffset At) : TriageState;

        /// <summary>It failed.</summary>
        /// <param name="Trigger">Who started it.</param>
        /// <param name="Failure">Why.</param>
        /// <param name="At">When.</param>
        public sealed record Failed(TriageTrigger Trigger, TriageFailure Failure, DateTimeOffset At) : TriageState;
    }

    /// <summary>Why automatic triage does not run although it is on (the status strip's "Automatic triage paused: …").</summary>
    public abstract record AutoTriagePause
    {
        // Only the cases below derive from it.
        private AutoTriagePause()
        {
        }

        /// <summary>The last automatic runs failed: the next try waits.</summary>
        /// <param name="Failure">Why the last one failed.</param>
        /// <param name="Until">When the next try comes.</param>
        public sealed record Failed(TriageFailure Failure, DateTimeOffset Until) : AutoTriagePause;

        /// <summary>Claude Code is signed out.</summary>
        public sealed record SignedOut : AutoTriagePause;

        /// <summary>The assistant cannot run.</summary>
        public sealed record Unavailable : AutoTriagePause;

        /// <summary>A consent is missing.</summary>
        public sealed record NoConsent : AutoTriagePause;
    }

    /// <summary>What <see cref="TriageViewOf"/> reads.</summary>
    public sealed record TriageViewInputs
    {
        /// <summary>The in-app provider.</summary>
        public AssistantProviderID Provider { get; init; } = AssistantProviderID.Claude;

        /// <summary>The assistant is shown (Settings → AI, registered).</summary>
        public required bool Shown { get; init; }

        /// <summary>Claude Code (or Codex) was found.</summary>
        public required bool ClaudeFound { get; init; }

        /// <summary><c>malachi-mcp</c> is beside the application.</summary>
        public required bool Bridge { get; init; }

        /// <summary>Whether Claude Code is signed in; null when not known.</summary>
        public required bool? SignedIn { get; init; }

        /// <summary>The triage would ask for consent first.</summary>
        public required bool NeedsConsent { get; init; }

        /// <summary>The board's assistant preference is on (its notes count).</summary>
        public required bool AssistantOn { get; init; }

        /// <summary>The application's run.</summary>
        public required TriageState State { get; init; }

        /// <summary>The daemon's last run (<c>board.list</c> <c>triage.lastRun</c>).</summary>
        public required Run? LastRun { get; init; }

        /// <summary>The autoTriage preference.</summary>
        public required bool AutoTriage { get; init; }

        /// <summary>Why automatic triage pauses; null when it does not.</summary>
        public required AutoTriagePause? Pause { get; init; }

        /// <summary>
        /// The daemon did not answer the board's preferences, and none are
        /// known: a run could neither take a consent nor start.
        /// </summary>
        public bool BackendFailed { get; init; }

        /// <summary>Cases automatic runs annotated today; null when not known for today.</summary>
        public int? AnnotatedToday { get; init; }

        /// <summary>
        /// The board's phase as far as it decides whether triage exists:
        /// <see cref="Phase.Off"/> and <see cref="Phase.Unsupported"/> hide
        /// the control; null and any other phase do not.
        /// </summary>
        public Phase? BoardPhase { get; init; }

        /// <summary>Claude Code's sign-in in the browser is under way (the application's one, whoever started it).</summary>
        public bool SigningIn { get; init; }

        /// <summary>A board.list has said what the triage used in the last 24 hours.</summary>
        public bool UsageKnown { get; init; }

        /// <summary><c>usage24h</c>; null when no run reported any.</summary>
        public BoardUsageTotal? Usage24h { get; init; }

        /// <summary>Cases waiting for the assistant; null while not known or the board's notes are off.</summary>
        public int? Queue { get; init; }

        /// <summary>How numbers are grouped (Swift <c>locale</c>); the current culture when null.</summary>
        public CultureInfo? Locale { get; init; }

        /// <summary>Now.</summary>
        public required DateTimeOffset Now { get; init; }

        /// <summary>The zone of the calendar days of "yesterday" and "tomorrow"; the local one when null.</summary>
        public TimeZoneInfo? TimeZone { get; init; }
    }

    /// <summary>The Triage control and the status strip.</summary>
    public sealed record TriageView
    {
        /// <summary>The in-app provider.</summary>
        public AssistantProviderID Provider { get; init; } = AssistantProviderID.Claude;

        /// <summary>
        /// <see cref="TriageControl.Hidden"/> exactly when triage is not
        /// offered: the toolbar item and Settings' Board group follow this one
        /// rule (<see cref="Offered"/>).
        /// </summary>
        public TriageControl Control { get; init; }

        /// <summary>The control's title ("" when hidden).</summary>
        public string Title { get; init; } = "";

        /// <summary>The control can be clicked.</summary>
        public bool Enabled { get; init; }

        /// <summary>The control's tooltip.</summary>
        public string ToolTip { get; init; } = "";

        /// <summary>A click asks for consent first.</summary>
        public bool NeedsConsent { get; init; }

        /// <summary>A run is starting or running.</summary>
        public bool Running { get; init; }

        /// <summary>"Triaging… 3 of 12" while a run works; "" otherwise.</summary>
        public string Progress { get; init; } = "";

        /// <summary>
        /// The strip's line: who sorted the board and when the assistant last
        /// refined it, or the run's progress. "" while triage is not offered
        /// and the board's notes are off.
        /// </summary>
        public string StatusLine { get; init; } = "";

        /// <summary>The line names a time relative to now, which goes stale as time passes.</summary>
        public bool RelativeTime { get; init; }

        /// <summary>How the app's last run ended ("" before one, and while one runs).</summary>
        public string Result { get; init; } = "";

        /// <summary>
        /// "Automatic triage paused: …", or "". Both it and
        /// <see cref="StatusLine"/> end with <see cref="Waiting"/> after " · "
        /// when that is not "".
        /// </summary>
        public string Paused { get; init; } = "";

        /// <summary>
        /// "3 conversations wait for the assistant" while the queue is known
        /// and not empty (and, during a run, has cases beyond what the run
        /// still has to do), else "". Already part of the lines.
        /// </summary>
        public string Waiting { get; init; } = "";

        /// <summary>Cases automatic runs annotated today while automatic triage is on and the count is known; null otherwise.</summary>
        public int? AnnotatedToday { get; init; }

        /// <summary><see cref="AnnotatedToday"/> as a line, or "".</summary>
        public string TodayLine { get; init; } = "";

        /// <summary>Why the control is <see cref="TriageControl.Unavailable"/>; null otherwise.</summary>
        public TriageFailure? Unavailable { get; init; }

        /// <summary>Claude Code's sign-in waits for the browser: the control is Sign In…, insensitive, and says so.</summary>
        public bool SigningIn { get; init; }

        /// <summary>Settings' row of the tokens of the last 24 hours shows.</summary>
        public bool UsageShown { get; init; }

        /// <summary>That row's value: the four counters added up, grouped for the locale, or "None".</summary>
        public string UsageValue { get; init; } = "";

        /// <summary>That row's detail: the tokens by kind on one line, the runs on the next; "" without usage.</summary>
        public string UsageDetail { get; init; } = "";

        /// <summary>That row's tooltip: only this application's runs count.</summary>
        public string UsageToolTip { get; init; } = "";

        /// <summary>Triage is offered: the toolbar shows its control and Settings its Board group.</summary>
        public bool Offered => Control != TriageControl.Hidden;
    }

    /// <summary>The Triage control and the status strip for <paramref name="i"/> (Swift <c>triageView</c>, Go <c>ViewOf</c>).</summary>
    public static TriageView TriageViewOf(TriageViewInputs i)
    {
        ArgumentNullException.ThrowIfNull(i);
        var chatGpt = i.Provider == AssistantProviderID.ChatGpt;
        var active = i.State.IsActive;
        var control = TriageControl.Triage;
        var title = Text.Triage;
        var enabled = true;
        var toolTip = Text.TriageToolTip;
        TriageFailure? unavailable = null;
        var signingIn = false;
        // A board the daemon does not have, or has turned off, has nothing
        // to triage; a run under way keeps its Stop (losing the board stops
        // it anyway).
        var boardGone = i.BoardPhase is Phase.Off or Phase.Unsupported;
        if (!i.Shown || (boardGone && !active))
        {
            control = TriageControl.Hidden;
            title = "";
            enabled = false;
            toolTip = "";
        }
        else if (active)
        {
            control = TriageControl.Stop;
            title = Assistant.PanelTexts().Stop;
            toolTip = Text.TriageStopToolTip;
        }
        else if (!i.ClaudeFound)
        {
            control = TriageControl.GetClaudeCode;
            title = chatGpt ? L10n.T("Get Codex…") : Assistant.SignInTexts().GetClaudeCode;
            toolTip = chatGpt ? L10n.T("Codex was not found. Choose a native Codex executable.") : Text.TriageNeedsClaudeCode;
        }
        else if (!i.Bridge)
        {
            control = TriageControl.Unavailable;
            enabled = false;
            toolTip = Text.TriageFailureText(TriageFailure.ToolsMissing);
            unavailable = TriageFailure.ToolsMissing;
        }
        else if (i.BackendFailed)
        {
            control = TriageControl.Unavailable;
            enabled = false;
            toolTip = Text.TriageFailureText(TriageFailure.Backend);
            unavailable = TriageFailure.Backend;
        }
        else if (i.SigningIn)
        {
            // One sign-in for the application: a click must not start a
            // second one (which would end the first as cancelled).
            control = TriageControl.SignIn;
            title = chatGpt ? L10n.T("Continue with ChatGPT") : Assistant.SignInTexts().SignIn;
            enabled = false;
            toolTip = chatGpt ? L10n.T("Connecting…") : Assistant.SignInTexts().Waiting;
            signingIn = true;
        }
        else if (i.SignedIn == false)
        {
            control = TriageControl.SignIn;
            title = chatGpt ? L10n.T("Continue with ChatGPT") : Assistant.SignInTexts().SignIn;
            toolTip = chatGpt ? L10n.T("Reconnect to ChatGPT") : Text.TriageNeedsSignIn;
        }

        var progress = "";
        var result = "";
        switch (i.State)
        {
            case TriageState.Starting:
                progress = Text.TriageStarting;
                break;
            case TriageState.Running r:
                progress = Text.TriageProgress(r.Done, r.Total);
                break;
            case TriageState.Finished f:
                result = Text.TriageFinished(f.Annotated, f.Refused);
                break;
            case TriageState.Failed { Failure: var f }:
                if (chatGpt && f is TriageFailure.NotFound or TriageFailure.NotSignedIn)
                {
                    result = L10n.T(
                        "Triage failed: %s.",
                        f == TriageFailure.NotFound ? L10n.T("Codex was not found. Choose a native Codex executable.") : L10n.T("Reconnect to ChatGPT"));
                }
                else
                {
                    result = f == TriageFailure.Cancelled ? Text.TriageStopped : Text.TriageFailed(f);
                }
                break;
        }

        var statusLine = "";
        var relativeTime = false;
        if (i.State is TriageState.Running running)
        {
            statusLine = Text.TriageRunningLine(running.Done, running.Total);
        }
        else if (active)
        {
            statusLine = Text.TriageRunningLine(0, 0);
        }
        else if (control == TriageControl.Hidden && boardGone)
        {
            // No board: no line about it.
        }
        else if (i.Shown || i.AssistantOn)
        {
            // Notes another client wrote (Claude Code with the bridge's
            // triage tier) still count without the in-app assistant.
            statusLine = Text.TriageStatusLine(i.AssistantOn, i.LastRun, i.Now, i.TimeZone);
            if (i.AssistantOn && i.LastRun is { Running: false })
            {
                relativeTime = true;
            }
        }

        var offered = control != TriageControl.Hidden;
        var paused = "";
        if (offered && i.AutoTriage && !active && i.Pause is { } p)
        {
            paused = chatGpt && p is AutoTriagePause.SignedOut
                ? L10n.T("Automatic triage paused: %s", L10n.T("Reconnect to ChatGPT"))
                : Text.AutoTriagePaused(p, i.Now, i.TimeZone);
            if (p is AutoTriagePause.Failed)
            {
                relativeTime = true;
            }
        }

        // The queue, after the line it belongs to. During a run only when
        // something waits beyond what the run still has to do.
        var waiting = "";
        if (i.Queue is > 0 and var q && i.AssistantOn)
        {
            switch (i.State)
            {
                case TriageState.Running r when r.Total > 0 && q > r.Total - Math.Min(r.Done, r.Total):
                    waiting = Text.TriageWaiting(q);
                    progress = JoinedNote(progress, waiting);
                    statusLine = JoinedNote(statusLine, waiting);
                    break;
                case TriageState.Running or TriageState.Starting:
                    break;
                default:
                    if (statusLine.Length > 0 || paused.Length > 0)
                    {
                        waiting = Text.TriageWaiting(q);
                        statusLine = JoinedNote(statusLine, waiting);
                        paused = JoinedNote(paused, waiting);
                    }
                    break;
            }
        }

        int? annotatedToday = null;
        var todayLine = "";
        if (offered && i.AutoTriage && i.AnnotatedToday is { } n)
        {
            annotatedToday = n;
            todayLine = Text.TriagedToday(n);
        }

        var usageShown = false;
        var usageValue = "";
        var usageDetail = "";
        var usageToolTip = "";
        if (offered && i.UsageKnown)
        {
            (usageValue, usageDetail) = TriageUsageTexts(i.Usage24h, i.Locale ?? CultureInfo.CurrentCulture);
            usageShown = true;
            usageToolTip = Text.TriageUsageToolTip;
        }

        return new TriageView
        {
            Provider = i.Provider,
            Control = control,
            Title = title,
            Enabled = enabled,
            ToolTip = toolTip,
            NeedsConsent = i.NeedsConsent,
            Running = active,
            Progress = progress,
            StatusLine = statusLine,
            RelativeTime = relativeTime,
            Result = result,
            Paused = paused,
            Waiting = waiting,
            AnnotatedToday = annotatedToday,
            TodayLine = todayLine,
            Unavailable = unavailable,
            SigningIn = signingIn,
            UsageShown = usageShown,
            UsageValue = usageValue,
            UsageDetail = usageDetail,
            UsageToolTip = usageToolTip,
        };
    }

    /// <summary><paramref name="line"/> and <paramref name="note"/> joined as the status line joins its notes (" · "), or the line alone when either is "".</summary>
    public static string JoinedNote(string line, string note)
    {
        ArgumentNullException.ThrowIfNull(line);
        ArgumentNullException.ThrowIfNull(note);
        return line.Length == 0 || note.Length == 0 ? line : line + " · " + note;
    }

    /// <summary>
    /// The value and the detail of Settings' row of the tokens of the last
    /// 24 hours for <paramref name="u"/>: the sum of the four counters
    /// (negative ones as 0, saturating) and the split with the runs (at least
    /// 1); "None" and "" without usage.
    /// </summary>
    public static (string Value, string Detail) TriageUsageTexts(BoardUsageTotal? u, CultureInfo locale)
    {
        ArgumentNullException.ThrowIfNull(locale);
        if (u is null)
        {
            return (Text.TriageUsageNone, "");
        }
        long[] parts =
        [
            Math.Max(u.InputTokens, 0), Math.Max(u.OutputTokens, 0),
            Math.Max(u.CacheCreationInputTokens, 0), Math.Max(u.CacheReadInputTokens, 0),
        ];
        long total = 0;
        foreach (var part in parts)
        {
            total = total > long.MaxValue - part ? long.MaxValue : total + part;
        }
        var split = Text.TriageUsageSplit(
            Text.TriageTokens(parts[0], locale), Text.TriageTokens(parts[1], locale),
            Text.TriageTokens(parts[2], locale), Text.TriageTokens(parts[3], locale));
        return (Text.UsageText(Text.TriageTokens(total, locale), u.LowerBound ?? false), split + "\n" + Text.TriageUsageRuns(Math.Max(u.Runs, 1)));
    }

    /// <summary>
    /// The status strip's triage note for the window in <paramref name="mode"/>
    /// with the board in <paramref name="phase"/>: in Mail only a run's
    /// progress; in Board a run's progress, else the sign-in waiting for the
    /// browser, else why automatic triage pauses, else the status line;
    /// nothing in Board while the board is off or the daemon has none,
    /// unless a run works.
    /// </summary>
    public static string TriageStripText(TriageView v, Mode mode, Phase phase)
    {
        ArgumentNullException.ThrowIfNull(v);
        if (mode == Mode.Mail)
        {
            return v.Running ? v.Progress : "";
        }
        if (v.Running)
        {
            return v.Progress.Length == 0 ? v.StatusLine : v.Progress;
        }
        if (phase is Phase.Off or Phase.Unsupported)
        {
            return "";
        }
        if (v.SigningIn)
        {
            return v.Provider == AssistantProviderID.ChatGpt ? L10n.T("Connecting…") : Assistant.SignInTexts().Waiting;
        }
        return v.Paused.Length == 0 ? v.StatusLine : v.Paused;
    }

    /// <summary>Settings' status row of the Board group: why automatic triage pauses, else the status line with today's automatic count.</summary>
    public static string TriageSettingsStatus(TriageView v)
    {
        ArgumentNullException.ThrowIfNull(v);
        if (v.Paused.Length > 0)
        {
            return v.Paused;
        }
        if (v.StatusLine.Length == 0 || v.TodayLine.Length == 0)
        {
            return v.StatusLine + v.TodayLine;
        }
        return v.StatusLine + " · " + v.TodayLine;
    }

    /// <summary>The description of Settings' Board group: why triage cannot run now, in whole sentences, or "" when it can.</summary>
    public static string TriageSettingsDescription(TriageView v)
    {
        ArgumentNullException.ThrowIfNull(v);
        var chatGpt = v.Provider == AssistantProviderID.ChatGpt;
        return v.Control switch
        {
            TriageControl.GetClaudeCode => chatGpt
                ? L10n.T("Codex was not found. Choose a native Codex executable.")
                : Text.TriageSettingsNeedsClaudeCode,
            TriageControl.SignIn => chatGpt
                ? (v.SigningIn ? L10n.T("Connecting…") : L10n.T("Reconnect to ChatGPT"))
                : (v.SigningIn ? Assistant.SignInTexts().Waiting : Text.TriageSettingsNeedsSignIn),
            TriageControl.Unavailable => v.Unavailable == TriageFailure.Backend ? Text.TriageSettingsNoBackend : Text.TriageSettingsNoTools,
            _ => "",
        };
    }
}
