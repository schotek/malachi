// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardTriageViewTests.swift: the
// Triage control and the status strip (Board.TriageViewOf) and the
// triage's texts (BoardTriageText.cs); Go: ui/internal/boardtriage
// triage_test.go (the view's cases). The ChatGPT case at the end is a
// Windows addition (the Codex provider's branches are Swift's). Board.Text
// has an alias: a bare Text is the namespace Malachi.Core.Text.

using System;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Boards;
using Xunit;
using static Malachi.Core.Boards.Board;
using BoardText = Malachi.Core.Boards.Board.Text;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardTriageViewTests
{
    private static readonly DateTimeOffset T0 = DateTimeOffset.FromUnixTimeSeconds(1_790_848_800);

    private static TriageViewInputs Inputs(
        bool shown = true, bool claudeFound = true, bool bridge = true, bool? signedIn = true, bool needsConsent = false,
        bool assistantOn = true, TriageState? state = null, Run? lastRun = null, bool autoTriage = false,
        AutoTriagePause? pause = null, bool backendFailed = false, int? annotatedToday = null, Phase? boardPhase = null,
        bool signingIn = false, int? queue = null) => new()
        {
            Shown = shown,
            ClaudeFound = claudeFound,
            Bridge = bridge,
            SignedIn = signedIn,
            NeedsConsent = needsConsent,
            AssistantOn = assistantOn,
            State = state ?? new TriageState.Idle(),
            LastRun = lastRun,
            AutoTriage = autoTriage,
            Pause = pause,
            BackendFailed = backendFailed,
            AnnotatedToday = annotatedToday,
            BoardPhase = boardPhase,
            SigningIn = signingIn,
            Queue = queue,
            Now = T0,
            TimeZone = TimeZoneInfo.Utc,
        };

    private static Run FiveMinutesAgo => new() { Model = "claude-code", Date = T0.AddMinutes(-5), Trigger = "auto" };

    [Fact]
    public void Control()
    {
        (string Name, TriageViewInputs I, TriageControl Control, string Title, bool Enabled)[] rows =
        [
            ("ready", Inputs(), TriageControl.Triage, "✦ Triage", true),
            ("sign-in not known", Inputs(signedIn: null), TriageControl.Triage, "✦ Triage", true),
            ("needs consent: still Triage", Inputs(needsConsent: true), TriageControl.Triage, "✦ Triage", true),
            ("the assistant off", Inputs(shown: false), TriageControl.Hidden, "", false),
            ("no Claude Code", Inputs(claudeFound: false), TriageControl.GetClaudeCode, Assistant.SignInTexts().GetClaudeCode, true),
            ("no bridge", Inputs(bridge: false), TriageControl.Unavailable, "✦ Triage", false),
            ("no preferences from the daemon", Inputs(backendFailed: true), TriageControl.Unavailable, "✦ Triage", false),
            ("running wins over no preferences", Inputs(state: new TriageState.Starting(TriageTrigger.Manual), backendFailed: true),
                TriageControl.Stop, Assistant.PanelTexts().Stop, true),
            ("signed out", Inputs(signedIn: false), TriageControl.SignIn, Assistant.SignInTexts().SignIn, true),
            ("running", Inputs(state: new TriageState.Running(TriageTrigger.Manual, 1, 3)), TriageControl.Stop, Assistant.PanelTexts().Stop, true),
            ("starting", Inputs(state: new TriageState.Starting(TriageTrigger.Automatic)), TriageControl.Stop, Assistant.PanelTexts().Stop, true),
            ("running wins over a sign-in lost meanwhile",
                Inputs(signedIn: false, state: new TriageState.Running(TriageTrigger.Automatic, 0, 1)), TriageControl.Stop,
                Assistant.PanelTexts().Stop, true),
        ];
        foreach (var (name, i, control, title, enabled) in rows)
        {
            var v = TriageViewOf(i);
            Assert.True(v.Control == control, name);
            Assert.True(v.Title == title, name);
            Assert.True(v.Enabled == enabled, name);
            Assert.True(v.NeedsConsent == i.NeedsConsent, name);
        }
        Assert.Equal("the Malachi Mail tools are not available to the assistant", TriageViewOf(Inputs(bridge: false)).ToolTip);
    }

    [Fact]
    public void ProgressAndResult()
    {
        var v = TriageViewOf(Inputs(state: new TriageState.Running(TriageTrigger.Manual, 2, 12)));
        Assert.True(v.Running && v.Progress == "Triaging… 2 of 12" && v.Result == "");
        Assert.Equal("The assistant is triaging the board… 2 of 12", v.StatusLine);
        // More accepted than expected (the queue grew) never shows more.
        v = TriageViewOf(Inputs(state: new TriageState.Running(TriageTrigger.Manual, 5, 3)));
        Assert.Equal("Triaging… 3 of 3", v.Progress);
        v = TriageViewOf(Inputs(state: new TriageState.Starting(TriageTrigger.Manual)));
        Assert.True(v.Progress == "Starting the triage…" && v.StatusLine == "The assistant is triaging the board…");
        v = TriageViewOf(Inputs(state: new TriageState.Finished(TriageTrigger.Manual, 4, 0, T0)));
        Assert.True(!v.Running && v.Progress == "" && v.Result == "Triage finished: 4 conversations refined.");
        Assert.Equal(
            "Triage finished: 1 conversation refined.",
            TriageViewOf(Inputs(state: new TriageState.Finished(TriageTrigger.Manual, 1, 0, T0))).Result);
        Assert.Equal(
            "Triage finished. No conversation needed new notes.",
            TriageViewOf(Inputs(state: new TriageState.Finished(TriageTrigger.Automatic, 0, 0, T0))).Result);
        Assert.Equal(
            "Triage stopped.", TriageViewOf(Inputs(state: new TriageState.Failed(TriageTrigger.Manual, TriageFailure.Cancelled, T0))).Result);
        Assert.Equal(
            "Triage failed: Claude Code is not signed in.",
            TriageViewOf(Inputs(state: new TriageState.Failed(TriageTrigger.Manual, TriageFailure.NotSignedIn, T0))).Result);
        // Every failure has its words.
        foreach (var f in TriageFailures)
        {
            Assert.True(BoardText.TriageFailureText(f).Length > 0, f.ToString());
        }
    }

    [Fact]
    public void StatusLine()
    {
        Assert.Equal("Sorted by the daemon’s rules · assistant off", TriageViewOf(Inputs(assistantOn: false)).StatusLine);
        Assert.Equal("Triaged by rules · not refined by the assistant yet", TriageViewOf(Inputs()).StatusLine);
        Assert.Equal(
            "Triaged by rules · refined by the assistant 5 minutes ago", TriageViewOf(Inputs(lastRun: FiveMinutesAgo)).StatusLine);
        // Without the in-app assistant: no "assistant off" at all; notes
        // another client wrote still have their line.
        Assert.Equal("", TriageViewOf(Inputs(shown: false, assistantOn: false)).StatusLine);
        Assert.Equal(
            "Triaged by rules · refined by the assistant 5 minutes ago",
            TriageViewOf(Inputs(shown: false, lastRun: FiveMinutesAgo)).StatusLine);
    }

    /// <summary>Whether the view names a time that goes stale.</summary>
    [Fact]
    public void RelativeTimeFlag()
    {
        Assert.True(TriageViewOf(Inputs(lastRun: FiveMinutesAgo)).RelativeTime);
        Assert.False(TriageViewOf(Inputs()).RelativeTime);
        Assert.False(TriageViewOf(Inputs(assistantOn: false, lastRun: FiveMinutesAgo)).RelativeTime);
        Assert.False(TriageViewOf(Inputs(state: new TriageState.Running(TriageTrigger.Manual, 1, 2), lastRun: FiveMinutesAgo)).RelativeTime);
        var until = T0.AddHours(1);
        Assert.True(TriageViewOf(Inputs(autoTriage: true, pause: new AutoTriagePause.Failed(TriageFailure.Timeout, until))).RelativeTime);
        Assert.False(TriageViewOf(Inputs(autoTriage: true, pause: new AutoTriagePause.SignedOut())).RelativeTime);
    }

    /// <summary>Today's automatic count, while automatic triage is on.</summary>
    [Fact]
    public void Today()
    {
        var v = TriageViewOf(Inputs(autoTriage: true, annotatedToday: 1));
        Assert.True(v.AnnotatedToday == 1 && v.TodayLine == "1 conversation triaged automatically today");
        v = TriageViewOf(Inputs(autoTriage: true, annotatedToday: 12));
        Assert.Equal("12 conversations triaged automatically today", v.TodayLine);
        v = TriageViewOf(Inputs(autoTriage: false, annotatedToday: 12));
        Assert.True(v.AnnotatedToday is null && v.TodayLine == "");
        v = TriageViewOf(Inputs(autoTriage: true));
        Assert.True(v.AnnotatedToday is null && v.TodayLine == "");
    }

    [Fact]
    public void RefusedNotes()
    {
        Assert.Equal(
            "Triage finished: 3 conversations refined. The board refused 2 of the assistant’s notes.",
            TriageViewOf(Inputs(state: new TriageState.Finished(TriageTrigger.Manual, 3, 2, T0))).Result);
        Assert.Equal(
            "Triage failed: the board refused the assistant’s notes.",
            TriageViewOf(Inputs(state: new TriageState.Failed(TriageTrigger.Automatic, TriageFailure.NotesRefused, T0))).Result);
        Assert.Equal(
            "Automatic triage paused: the assistant added no notes · next try in 1 hour",
            BoardText.AutoTriagePaused(new AutoTriagePause.Failed(TriageFailure.NoProgress, T0.AddHours(1)), T0));
    }

    [Fact]
    public void Paused()
    {
        var until = T0.AddHours(1);
        Assert.Equal(
            "Automatic triage paused: it took too long · next try in 1 hour",
            TriageViewOf(Inputs(autoTriage: true, pause: new AutoTriagePause.Failed(TriageFailure.Timeout, until))).Paused);
        Assert.Equal(
            "Automatic triage paused: Claude Code is not signed in",
            TriageViewOf(Inputs(autoTriage: true, pause: new AutoTriagePause.SignedOut())).Paused);
        Assert.Equal(
            "Automatic triage paused: sending mail to the assistant is not allowed",
            TriageViewOf(Inputs(autoTriage: true, pause: new AutoTriagePause.NoConsent())).Paused);
        Assert.Equal(
            "Automatic triage paused: the assistant cannot run",
            TriageViewOf(Inputs(autoTriage: true, pause: new AutoTriagePause.Unavailable())).Paused);
        // Not with automatic triage off, nor while a run works, nor with the
        // assistant hidden.
        Assert.Equal("", TriageViewOf(Inputs(autoTriage: false, pause: new AutoTriagePause.SignedOut())).Paused);
        Assert.Equal(
            "",
            TriageViewOf(Inputs(state: new TriageState.Starting(TriageTrigger.Manual), autoTriage: true, pause: new AutoTriagePause.SignedOut())).Paused);
        Assert.Equal("", TriageViewOf(Inputs(shown: false, autoTriage: true, pause: new AutoTriagePause.SignedOut())).Paused);
    }

    [Theory]
    [InlineData(0, "just now")]
    [InlineData(59, "just now")]
    [InlineData(60, "1 minute ago")]
    [InlineData(5 * 60, "5 minutes ago")]
    [InlineData(3600, "1 hour ago")]
    [InlineData(5 * 3600, "5 hours ago")]
    [InlineData(86400, "yesterday")]
    [InlineData(3 * 86400, "3 days ago")]
    [InlineData(-30, "just now")]
    public void RelativeTimes(int ago, string want) => Assert.Equal(want, BoardText.RelativeTime(T0.AddSeconds(-ago), T0, TimeZoneInfo.Utc));

    [Theory]
    [InlineData(0, "now")]
    [InlineData(90, "in 1 minute")]
    [InlineData(20 * 60, "in 20 minutes")]
    [InlineData(3600, "in 1 hour")]
    [InlineData(4 * 3600, "in 4 hours")]
    [InlineData(86400, "tomorrow")]
    [InlineData(2 * 86400, "in 2 days")]
    public void RelativeFutures(int ahead, string want) => Assert.Equal(want, BoardText.RelativeFuture(T0.AddSeconds(ahead), T0, TimeZoneInfo.Utc));

    /// <summary>Past a day, "yesterday" and "tomorrow" are calendar days in the zone, not 24 to 48 hours.</summary>
    [Fact]
    public void RelativeByCalendarDay()
    {
        var utc = TimeZoneInfo.Utc;
        var now = new DateTimeOffset(2026, 10, 2, 0, 30, 0, TimeSpan.Zero);
        var late = new DateTimeOffset(2026, 10, 2, 23, 30, 0, TimeSpan.Zero);
        Assert.Equal("2 days ago", BoardText.RelativeTime(now.AddHours(-25), now, utc)); // the day before yesterday
        Assert.Equal("yesterday", BoardText.RelativeTime(late.AddHours(-47), late, utc));
        Assert.Equal("in 2 days", BoardText.RelativeFuture(late.AddHours(25), late, utc)); // the day after tomorrow
        Assert.Equal("tomorrow", BoardText.RelativeFuture(now.AddHours(47), now, utc));
    }

    [Fact]
    public void RunErrors()
    {
        Assert.Equal(BoardRunError.Cancelled, TriageFailure.Cancelled.RunError.Value);
        Assert.Equal(BoardRunError.Timeout, TriageFailure.Timeout.RunError.Value);
        Assert.Equal(BoardRunError.SignedOut, TriageFailure.NotSignedIn.RunError.Value);
        foreach (var f in new[]
        {
            TriageFailure.NotFound, TriageFailure.ToolsMissing, TriageFailure.Declined, TriageFailure.AssistantOff, TriageFailure.Backend,
            TriageFailure.Stopped, TriageFailure.NothingToDo, TriageFailure.NotesRefused, TriageFailure.NoProgress,
            TriageFailure.Limit,
        })
        {
            Assert.True(f.RunError == BoardRunError.Failed, f.ToString());
        }
        Assert.True(TriageTrigger.Manual.Wire == BoardTrigger.Manual && TriageTrigger.Automatic.Wire == BoardTrigger.Auto);
    }

    /// <summary>A board the daemon has turned off, or does not have, offers no triage: one rule for the toolbar and Settings.</summary>
    [Fact]
    public void BoardGone()
    {
        foreach (var phase in new[] { Phase.Off, Phase.Unsupported })
        {
            var v = TriageViewOf(Inputs(
                lastRun: FiveMinutesAgo, autoTriage: true, pause: new AutoTriagePause.SignedOut(), annotatedToday: 3, boardPhase: phase));
            Assert.True(v.Control == TriageControl.Hidden && !v.Offered && !v.Enabled && v.Title == "", phase.ToString());
            Assert.True(v.StatusLine == "" && v.Paused == "" && v.TodayLine == "" && !v.RelativeTime, phase.ToString());
            // A run under way keeps its Stop.
            Assert.Equal(TriageControl.Stop, TriageViewOf(Inputs(state: new TriageState.Starting(TriageTrigger.Manual), boardPhase: phase)).Control);
        }
        foreach (var phase in new[] { Phase.Ready, Phase.Preparing, Phase.Loading, Phase.Failed, Phase.Unavailable })
        {
            Assert.Equal(TriageControl.Triage, TriageViewOf(Inputs(boardPhase: phase)).Control);
        }
        Assert.True(TriageViewOf(Inputs()).Offered);
        Assert.False(TriageViewOf(Inputs(shown: false)).Offered);
    }

    /// <summary>While the application's sign-in waits for the browser, the control cannot start a second one and says why.</summary>
    [Fact]
    public void SigningIn()
    {
        var waiting = Assistant.SignInTexts().Waiting;
        var v = TriageViewOf(Inputs(signedIn: false, signingIn: true));
        Assert.True(v.Control == TriageControl.SignIn && !v.Enabled && v.SigningIn);
        Assert.True(v.Title == Assistant.SignInTexts().SignIn && v.ToolTip == waiting);
        // Also while a sign-in started elsewhere replaces a known one.
        v = TriageViewOf(Inputs(signedIn: true, signingIn: true));
        Assert.True(v.Control == TriageControl.SignIn && !v.Enabled);
        // Without it: Sign In… can be clicked.
        v = TriageViewOf(Inputs(signedIn: false));
        Assert.True(v.Control == TriageControl.SignIn && v.Enabled && !v.SigningIn);
        // A run, a missing Claude Code or bridge come first.
        Assert.Equal(TriageControl.Stop, TriageViewOf(Inputs(state: new TriageState.Starting(TriageTrigger.Manual), signingIn: true)).Control);
        Assert.Equal(TriageControl.GetClaudeCode, TriageViewOf(Inputs(claudeFound: false, signingIn: true)).Control);
        Assert.Equal(TriageControl.Unavailable, TriageViewOf(Inputs(bridge: false, signingIn: true)).Control);
        Assert.Equal(TriageControl.Hidden, TriageViewOf(Inputs(shown: false, signingIn: true)).Control);
        Assert.Equal(waiting, TriageStripText(TriageViewOf(Inputs(signedIn: false, signingIn: true)), Mode.Board, Phase.Ready));
    }

    /// <summary>The status strip's note by the window's mode and the board's phase.</summary>
    [Fact]
    public void StripText()
    {
        var idle = TriageViewOf(Inputs(lastRun: FiveMinutesAgo));
        var running = TriageViewOf(Inputs(state: new TriageState.Running(TriageTrigger.Manual, 1, 4)));
        var starting = TriageViewOf(Inputs(state: new TriageState.Starting(TriageTrigger.Automatic)));
        var paused = TriageViewOf(Inputs(lastRun: FiveMinutesAgo, autoTriage: true, pause: new AutoTriagePause.NoConsent()));
        const string line = "Triaged by rules · refined by the assistant 5 minutes ago";
        Assert.Equal("", TriageStripText(idle, Mode.Mail, Phase.Ready));
        Assert.Equal("Triaging… 1 of 4", TriageStripText(running, Mode.Mail, Phase.Ready));
        Assert.Equal(line, TriageStripText(idle, Mode.Board, Phase.Ready));
        Assert.Equal("Triaging… 1 of 4", TriageStripText(running, Mode.Board, Phase.Ready));
        Assert.Equal("Starting the triage…", TriageStripText(starting, Mode.Board, Phase.Ready));
        Assert.Equal("Automatic triage paused: sending mail to the assistant is not allowed", TriageStripText(paused, Mode.Board, Phase.Ready));
        foreach (var phase in new[] { Phase.Off, Phase.Unsupported })
        {
            Assert.Equal("", TriageStripText(idle, Mode.Board, phase));
            Assert.Equal("", TriageStripText(paused, Mode.Board, phase));
            Assert.Equal("Triaging… 1 of 4", TriageStripText(running, Mode.Board, phase));
        }
    }

    /// <summary>The conversations that wait for the assistant, after the line they belong to.</summary>
    [Fact]
    public void Waiting()
    {
        const string refined = "Triaged by rules · refined by the assistant 5 minutes ago";
        const string notYet = "Triaged by rules · not refined by the assistant yet";
        // Unknown or empty: nothing added.
        foreach (var q in new int?[] { null, 0 })
        {
            var e = TriageViewOf(Inputs(lastRun: FiveMinutesAgo, queue: q));
            Assert.True(e.Waiting == "" && e.StatusLine == refined, q?.ToString(CultureInfo.InvariantCulture) ?? "null");
        }
        // Idle, refined or not yet, in the strip and in Settings.
        var v = TriageViewOf(Inputs(lastRun: FiveMinutesAgo, queue: 1));
        Assert.Equal("1 conversation waits for the assistant", v.Waiting);
        Assert.Equal(refined + " · 1 conversation waits for the assistant", v.StatusLine);
        Assert.Equal(v.StatusLine, TriageStripText(v, Mode.Board, Phase.Ready));
        Assert.Equal("", TriageStripText(v, Mode.Mail, Phase.Ready));
        v = TriageViewOf(Inputs(queue: 40));
        Assert.Equal(notYet + " · 40 conversations wait for the assistant", v.StatusLine);
        v = TriageViewOf(Inputs(lastRun: FiveMinutesAgo, autoTriage: true, annotatedToday: 2, queue: 3));
        Assert.Equal(
            refined + " · 3 conversations wait for the assistant · 2 conversations triaged automatically today", TriageSettingsStatus(v));
        // After a run.
        v = TriageViewOf(Inputs(state: new TriageState.Finished(TriageTrigger.Manual, 5, 0, T0), lastRun: FiveMinutesAgo, queue: 7));
        Assert.Equal(refined + " · 7 conversations wait for the assistant", v.StatusLine);
        // Paused, in the strip and in Settings.
        var until = T0.AddHours(1);
        v = TriageViewOf(Inputs(
            lastRun: FiveMinutesAgo, autoTriage: true, pause: new AutoTriagePause.Failed(TriageFailure.Timeout, until), queue: 12));
        const string paused = "Automatic triage paused: it took too long · next try in 1 hour · 12 conversations wait for the assistant";
        Assert.Equal(paused, v.Paused);
        Assert.Equal(paused, TriageStripText(v, Mode.Board, Phase.Ready));
        Assert.Equal(paused, TriageSettingsStatus(v));
        v = TriageViewOf(Inputs(autoTriage: true, pause: new AutoTriagePause.SignedOut(), queue: 2));
        Assert.Equal("Automatic triage paused: Claude Code is not signed in · 2 conversations wait for the assistant", v.Paused);
        // The board's notes off, or no board: no count.
        v = TriageViewOf(Inputs(assistantOn: false, queue: 5));
        Assert.True(v.Waiting == "" && v.StatusLine == "Sorted by the daemon’s rules · assistant off");
        v = TriageViewOf(Inputs(shown: false, assistantOn: false, queue: 5));
        Assert.True(v.Waiting == "" && v.StatusLine == "");
        foreach (var phase in new[] { Phase.Off, Phase.Unsupported })
        {
            v = TriageViewOf(Inputs(lastRun: FiveMinutesAgo, boardPhase: phase, queue: 5));
            Assert.True(v.Waiting == "" && v.StatusLine == "", phase.ToString());
        }
        // A run: only when more waits than it still has to do.
        v = TriageViewOf(Inputs(state: new TriageState.Running(TriageTrigger.Manual, 3, 5), queue: 40));
        Assert.Equal("Triaging… 3 of 5 · 40 conversations wait for the assistant", v.Progress);
        Assert.Equal("The assistant is triaging the board… 3 of 5 · 40 conversations wait for the assistant", v.StatusLine);
        Assert.Equal(v.Progress, TriageStripText(v, Mode.Board, Phase.Ready));
        Assert.Equal(v.Progress, TriageStripText(v, Mode.Mail, Phase.Ready));
        v = TriageViewOf(Inputs(state: new TriageState.Running(TriageTrigger.Manual, 3, 5), queue: 2));
        Assert.True(v.Waiting == "" && v.Progress == "Triaging… 3 of 5");
        v = TriageViewOf(Inputs(state: new TriageState.Running(TriageTrigger.Manual, 0, 0), queue: 9));
        Assert.True(v.Waiting == "" && v.Progress == "Triaging…");
        v = TriageViewOf(Inputs(state: new TriageState.Starting(TriageTrigger.Manual), queue: 9));
        Assert.True(v.Waiting == "" && v.Progress == "Starting the triage…");
    }

    /// <summary>The count picks its own plural form.</summary>
    [Fact]
    public void WaitingPlurals()
    {
        Assert.Equal("1 conversation waits for the assistant", BoardText.TriageWaiting(1));
        Assert.Equal("2 conversations wait for the assistant", BoardText.TriageWaiting(2));
        Assert.Equal("0 conversations wait for the assistant", BoardText.TriageWaiting(0));
    }

    /// <summary>Settings' Board group: its status row and its description.</summary>
    [Fact]
    public void SettingsTexts()
    {
        const string line = "Triaged by rules · refined by the assistant 5 minutes ago";
        Assert.Equal(line, TriageSettingsStatus(TriageViewOf(Inputs(lastRun: FiveMinutesAgo))));
        Assert.Equal(
            line + " · 1 conversation triaged automatically today",
            TriageSettingsStatus(TriageViewOf(Inputs(lastRun: FiveMinutesAgo, autoTriage: true, annotatedToday: 1))));
        Assert.Equal(
            "Automatic triage paused: Claude Code is not signed in",
            TriageSettingsStatus(TriageViewOf(Inputs(
                lastRun: FiveMinutesAgo, autoTriage: true, pause: new AutoTriagePause.SignedOut(), annotatedToday: 1))));
        Assert.Equal("", TriageSettingsStatus(TriageViewOf(Inputs(shown: false, assistantOn: false))));

        (string Name, TriageViewInputs I, string Want)[] rows =
        [
            ("ready", Inputs(), ""),
            ("running", Inputs(state: new TriageState.Starting(TriageTrigger.Manual)), ""),
            ("no Claude Code", Inputs(claudeFound: false),
                "The triage runs your Claude Code, which was not found on this computer. The Claude Code row above offers to get it."),
            ("signed out", Inputs(signedIn: false), "Claude Code is not signed in. The Claude Code row above offers to sign in."),
            ("signing in", Inputs(signedIn: false, signingIn: true), Assistant.SignInTexts().Waiting),
            ("no bridge", Inputs(bridge: false), "The Malachi Mail tools are not available to the assistant, so the board cannot be triaged."),
            ("no preferences", Inputs(backendFailed: true),
                "The mail backend did not answer with the board’s settings, so they cannot be changed now."),
        ];
        foreach (var (name, i, want) in rows)
        {
            Assert.True(TriageSettingsDescription(TriageViewOf(i)) == want, name);
        }
        Assert.True(TriageViewOf(Inputs(backendFailed: true)).Unavailable == TriageFailure.Backend
            && TriageViewOf(Inputs(bridge: false)).Unavailable == TriageFailure.ToolsMissing);
        Assert.Null(TriageViewOf(Inputs()).Unavailable);
    }

    [Fact]
    public void SettingsChoices()
    {
        foreach (var (m, want) in new[] { (1, "1 minute"), (15, "15 minutes"), (60, "1 hour"), (90, "90 minutes"), (180, "3 hours") })
        {
            Assert.Equal(want, BoardText.TriageInterval(m));
        }
        Assert.True(BoardText.TriageDailyCap(60) == "Up to 60" && BoardText.TriageDailyCap(0) == "None");
        // The switch's subtitle says what the sheet says.
        var sub = BoardText.TriageSettingsConsentSubtitle;
        var body = BoardText.TriageConsentBody;
        foreach (var words in new[]
        {
            "Anthropic", "through your Claude Code", "cannot send, move or delete mail", "write replies, which stay on the board",
        })
        {
            Assert.True(sub.Contains(words, StringComparison.Ordinal) && body.Contains(words, StringComparison.Ordinal), words);
        }
        // Suggested replies stay on the board, out of the Drafts folder.
        Assert.False(sub.Contains("reply drafts", StringComparison.Ordinal) || body.Contains("reply drafts", StringComparison.Ordinal));
    }

    /// <summary>
    /// Settings' row of the tokens of the last 24 hours: shown with the Board
    /// group once a board.list said what they were; the sum grouped for the
    /// locale, the split and the runs as detail, "None" without.
    /// </summary>
    [Fact]
    public void UsageRow()
    {
        var en = CultureInfo.GetCultureInfo("en-US");
        var i = Inputs();
        Assert.False(TriageViewOf(i).UsageShown, "not known before a board.list");
        i = i with { UsageKnown = true, Locale = en };
        var v = TriageViewOf(i);
        Assert.True(v.UsageShown && v.UsageValue == "None" && v.UsageDetail.Length == 0);
        Assert.Equal("Counts only the triage runs Malachi Mail started, not those of other assistants", v.UsageToolTip);
        i = i with
        {
            Usage24h = new BoardUsageTotal
            {
                InputTokens = 1200,
                OutputTokens = 340,
                CacheCreationInputTokens = 5,
                CacheReadInputTokens = 1_234_567,
                Runs = 1,
            },
        };
        Assert.Equal("1,236,112", TriageViewOf(i).UsageValue);
        Assert.Equal(
            "Input 1,200 · output 340 · written to cache 5 · read from cache 1,234,567\nFrom 1 triage run", TriageViewOf(i).UsageDetail);
        i = i with { Usage24h = i.Usage24h! with { Runs = 3 } };
        Assert.EndsWith("\nFrom 3 triage runs", TriageViewOf(i).UsageDetail, StringComparison.Ordinal);
        // A run that reported only part of its tokens: "at least".
        Assert.Equal("at least 1,236,112", TriageViewOf(i with { Usage24h = i.Usage24h! with { LowerBound = true } }).UsageValue);
        // Not offered: no row.
        Assert.False(TriageViewOf(i with { Shown = false }).UsageShown);
        // Grouped for the locale; a sum beyond Int64 stops there.
        var cs = TriageUsageTexts(
            new BoardUsageTotal { InputTokens = 1_234_567, OutputTokens = 0, CacheCreationInputTokens = 0, CacheReadInputTokens = 0, Runs = 1 },
            CultureInfo.GetCultureInfo("cs-CZ"));
        Assert.True(new string([.. cs.Value.Where(char.IsDigit)]) == "1234567" && cs.Value != "1234567", cs.Value);
        var huge = TriageUsageTexts(
            new BoardUsageTotal
            {
                InputTokens = long.MaxValue,
                OutputTokens = long.MaxValue,
                CacheCreationInputTokens = 0,
                CacheReadInputTokens = -5,
                Runs = 0,
            },
            en);
        Assert.Equal("9,223,372,036,854,775,807", huge.Value);
        Assert.Contains("read from cache 0\nFrom 1 triage run", huge.Detail, StringComparison.Ordinal);
    }

    /// <summary>Windows addition: the Codex provider's words where Swift has them.</summary>
    [Fact]
    public void ChatGptProvider()
    {
        var i = Inputs(claudeFound: false) with { Provider = AssistantProviderID.ChatGpt };
        var v = TriageViewOf(i);
        Assert.True(v.Control == TriageControl.GetClaudeCode && v.Title == "Get Codex…");
        Assert.Equal("Codex was not found. Choose a native Codex executable.", v.ToolTip);
        Assert.Equal("Codex was not found. Choose a native Codex executable.", TriageSettingsDescription(v));
        v = TriageViewOf(Inputs(signedIn: false) with { Provider = AssistantProviderID.ChatGpt });
        Assert.True(v.Title == "Continue with ChatGPT" && v.ToolTip == "Reconnect to ChatGPT");
        v = TriageViewOf(Inputs(state: new TriageState.Failed(TriageTrigger.Manual, TriageFailure.NotSignedIn, T0)) with
        {
            Provider = AssistantProviderID.ChatGpt,
        });
        Assert.Equal("Triage failed: Reconnect to ChatGPT.", v.Result);
        v = TriageViewOf(Inputs(autoTriage: true, pause: new AutoTriagePause.SignedOut()) with { Provider = AssistantProviderID.ChatGpt });
        Assert.Equal("Automatic triage paused: Reconnect to ChatGPT", v.Paused);
        v = TriageViewOf(Inputs(signedIn: false, signingIn: true) with { Provider = AssistantProviderID.ChatGpt });
        Assert.Equal("Connecting…", TriageStripText(v, Mode.Board, Phase.Ready));
    }
}
