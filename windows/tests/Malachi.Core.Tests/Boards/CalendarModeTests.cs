// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarModeTests.swift (nicks,
// resolveStartMode, startDecisionCalendar, modeKeys,
// keyContextFollowsTheMode); GTK:
// ui/internal/board/mode_test.go (TestResolveStartMode,
// TestStartDecisionCalendar, and the Calendar's rows of TestInitialMode and
// TestStartMode; the rows of TestAllows, TestModeForRequest and
// TestViewsMail are BoardTests').
//
// The window's Calendar mode (docs/calendar.md D27). Swift's modeKeys read
// macOS key codes (modeKeyDigit, modeForKey); Windows switches the modes
// with ShortcutMap's Ctrl+1, Ctrl+2 and Ctrl+3, whose VirtualKey is the
// layout-independent key. keyContextFollowsTheMode is the port of keys.go's
// KeyContext (Cal.KeyContext, CalendarKeys.cs).

using System;
using System.Linq;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Malachi.Core.Presentation;
using Malachi.Core.Settings;
using Xunit;
using static Malachi.Core.Boards.Board;

namespace Malachi.Core.Tests.Boards;

public sealed class CalendarModeTests
{
    [Fact]
    public void Nicks()
    {
        Assert.Equal(2, (int)Mode.Calendar);
        Assert.Equal("calendar", Mode.Calendar.Nick);
        Assert.Equal(Mode.Calendar, ParseMode("calendar"));
        Assert.Null(ParseMode("Calendar"));
        Assert.Equal("calendar", StartChoice.Calendar.Nick);
        Assert.Equal(StartChoice.Calendar, ParseStartChoice("calendar"));
        // A nick is case sensitive.
        Assert.Equal(StartChoice.Mail, ParseStartChoice("Calendar"));
        Assert.Equal("Calendar", Texts().Name(Mode.Calendar));
        Assert.Equal("Calendar", Texts().Calendar);
        Assert.Equal("Mail", Texts().Name(Mode.Mail));
        Assert.Equal("Board", Texts().Name(Mode.Board));
        Assert.Equal("Calendar", Board.Text.CalendarName);
        Assert.Equal("Calendar", Board.Text.StartModeTitle(StartChoice.Calendar));
        foreach (var m in Enum.GetValues<Mode>())
        {
            Assert.Equal(m, ParseMode(m.Nick));
        }
    }

    [Theory]
    [InlineData(StartChoice.Mail, Mode.Calendar, true, Mode.Mail)]
    [InlineData(StartChoice.Board, Mode.Calendar, true, Mode.Board)]
    [InlineData(StartChoice.Calendar, Mode.Mail, true, Mode.Calendar)]
    [InlineData(StartChoice.Calendar, Mode.Mail, false, Mode.Mail)]
    [InlineData(StartChoice.Last, Mode.Calendar, true, Mode.Calendar)]
    [InlineData(StartChoice.Last, Mode.Calendar, false, Mode.Mail)]
    [InlineData(StartChoice.Last, Mode.Board, false, Mode.Board)]
    [InlineData(StartChoice.Last, Mode.Mail, false, Mode.Mail)]
    public void ResolveStartModeRule(StartChoice choice, Mode last, bool hasCalendar, Mode want) =>
        Assert.Equal(want, ResolveStartMode(choice, last, hasCalendar));

    [Theory]
    [InlineData(StartChoice.Calendar, Mode.Mail, false, false, Mode.Calendar)]
    [InlineData(StartChoice.Last, Mode.Calendar, false, false, Mode.Calendar)]
    [InlineData(StartChoice.Calendar, Mode.Mail, true, false, Mode.Mail)]
    [InlineData(StartChoice.Calendar, Mode.Mail, false, true, Mode.Mail)]
    public void StartDecisionCalendar(StartChoice start, Mode last, bool switched, bool acted, Mode want)
    {
        var (mode, decided) = StartDecision(start, last, prefsKnown: false, enabled: false, switched, acted, TimeSpan.Zero);
        Assert.Equal(want, mode);
        Assert.True(decided);
    }

    [Fact]
    public void TheCalendarDoesNotDependOnTheBoard()
    {
        Assert.Equal(Mode.Calendar, StartMode(StartChoice.Calendar, "mail", boardEnabled: false));
        Assert.Equal(Mode.Calendar, StartMode(StartChoice.Last, "calendar", boardEnabled: false));
        Assert.Equal(Mode.Mail, StartMode(StartChoice.Last, "board", boardEnabled: false));
        // A Calendar start settles at once, without the board's preferences.
        Assert.Equal((Mode.Calendar, true), StartDecision(StartChoice.Last, Mode.Calendar, false, true, false, false, TimeSpan.Zero));
    }

    /// <summary>Swift's modeKeys: ⌘1, ⌘2 and ⌘3 in every mode are Ctrl+1, Ctrl+2 and Ctrl+3 of the main window here.</summary>
    [Fact]
    public void ModeKeys()
    {
        var main = new ShortcutContext(WindowKind.Main);
        Assert.Equal(ShortcutCommand.ShowMail, ShortcutMap.Resolve(KeyChord.Ctrl(VirtualKey.Number1), main));
        Assert.Equal(ShortcutCommand.ShowBoard, ShortcutMap.Resolve(KeyChord.Ctrl(VirtualKey.Number2), main));
        Assert.Equal(ShortcutCommand.ShowCalendar, ShortcutMap.Resolve(KeyChord.Ctrl(VirtualKey.Number3), main));
        // A Ctrl chord: a text input does not lift it, nor does the ctrl-r setting.
        Assert.Equal(
            ShortcutCommand.ShowCalendar,
            ShortcutMap.Resolve(KeyChord.Ctrl(VirtualKey.Number3), main with { TextInputFocused = true, CtrlR = CtrlR.Refresh }));
        Assert.Null(ShortcutMap.Resolve(KeyChord.Ctrl(0x34), main));
        Assert.False(ShortcutMap.IsSingleKey(ShortcutCommand.ShowCalendar));
        Assert.Contains(KeyChord.Ctrl(VirtualKey.Number3), ShortcutMap.Chords(WindowKind.Main));
        foreach (var window in Enum.GetValues<WindowKind>().Where(w => w != WindowKind.Main))
        {
            Assert.Null(ShortcutMap.Resolve(KeyChord.Ctrl(VirtualKey.Number3), new ShortcutContext(window)));
            Assert.DoesNotContain(KeyChord.Ctrl(VirtualKey.Number3), ShortcutMap.Chords(window));
        }
        // The board's own keys know nothing of Ctrl+3.
        foreach (var mode in Enum.GetValues<Mode>())
        {
            Assert.Null(KeyFor('3', primary: true, other: false, inText: false, mode));
        }
    }

    /// <summary>
    /// Swift's keyContextFollowsTheMode: the calendar's key context follows
    /// the window's mode, and its Ctrl+3 is ShortcutMap's in every mode.
    /// </summary>
    [Fact]
    public void KeyContextFollowsTheMode()
    {
        Assert.True(new Cal.KeyContext(Mode.Calendar).CalendarMode);
        Assert.False(new Cal.KeyContext(Mode.Mail).CalendarMode);
        Assert.False(new Cal.KeyContext(Mode.Board, InText: true, Selected: true).CalendarMode);
        // Ctrl+T is the calendar's only in Calendar.
        var t = new Cal.Press('t', Primary: true);
        Assert.Equal(Cal.KeyAction.Today, Cal.KeyFor(t, new Cal.KeyContext(Mode.Calendar)));
        Assert.Null(Cal.KeyFor(t, new Cal.KeyContext(Mode.Mail)));
        var ctrl3 = Cal.Press.FromVirtualKey(VirtualKey.Number3, ctrl: true, shift: false, alt: false);
        foreach (var mode in Enum.GetValues<Mode>())
        {
            Assert.Equal(Cal.KeyAction.ShowCalendar, Cal.KeyFor(ctrl3, new Cal.KeyContext(mode, InText: true)));
        }
    }
}
