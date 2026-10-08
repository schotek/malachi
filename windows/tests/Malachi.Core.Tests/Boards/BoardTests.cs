// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardTests.swift; GTK:
// ui/internal/board/mode_test.go (TestInitialMode, TestAllows,
// TestModeForRequest, TestViewsMail, TestModeTexts, and those Swift
// lacks: TestStyleOnShowRule, TestDefaultStyleNicks, TestStartMode,
// TestFilterOnShow, TestStyleNicks, TestStartDecision).

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Xunit;
using static Malachi.Core.Boards.Board;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardTests
{
    [Fact]
    public void InitialMode()
    {
        Assert.Equal(Mode.Mail, Board.InitialMode);
        Assert.Equal([0, 1], Array.ConvertAll(Enum.GetValues<Mode>(), m => (int)m));
    }

    [Fact]
    public void AllowsCommands()
    {
        (Command, Mode, bool)[] cases =
        [
            (Command.SwitchMode, Mode.Mail, true),
            (Command.NewMessage, Mode.Mail, true),
            (Command.CheckForNewMail, Mode.Mail, true),
            (Command.MailView, Mode.Mail, true),
            (Command.MessageAction, Mode.Mail, true),
            (Command.BoardView, Mode.Mail, false),
            (Command.SwitchMode, Mode.Board, true),
            (Command.NewMessage, Mode.Board, true),
            (Command.CheckForNewMail, Mode.Board, true),
            (Command.MailView, Mode.Board, false),
            (Command.MessageAction, Mode.Board, false),
            (Command.BoardView, Mode.Board, true),
        ];
        Assert.Equal(Enum.GetValues<Command>().Length * Enum.GetValues<Mode>().Length, cases.Length);
        foreach (var (command, mode, want) in cases)
        {
            Assert.True(Allows(command, mode) == want, $"Allows({command}, {mode})");
        }
    }

    [Fact]
    public void ModeForRequest()
    {
        (Request, Mode, Mode)[] cases =
        [
            (Request.ShowOutbox, Mode.Mail, Mode.Mail),
            (Request.ShowOutbox, Mode.Board, Mode.Mail),
            (Request.RevealAssistant, Mode.Mail, Mode.Mail),
            (Request.RevealAssistant, Mode.Board, Mode.Mail),
            (Request.OpenMessageWindow, Mode.Mail, Mode.Mail),
            (Request.OpenMessageWindow, Mode.Board, Mode.Board),
            (Request.Compose, Mode.Mail, Mode.Mail),
            (Request.Compose, Mode.Board, Mode.Board),
        ];
        Assert.Equal(Enum.GetValues<Request>().Length * Enum.GetValues<Mode>().Length, cases.Length);
        foreach (var (request, current, want) in cases)
        {
            Assert.True(ModeFor(request, current) == want, $"ModeFor({request}, {current})");
        }
    }

    [Theory]
    [InlineData(Mode.Mail, true, true)]
    [InlineData(Mode.Mail, false, false)]
    [InlineData(Mode.Board, true, false)]
    [InlineData(Mode.Board, false, false)]
    public void ViewsTheMail(Mode mode, bool key, bool want) => Assert.Equal(want, ViewsMail(mode, key));

    [Fact]
    public void ModeTexts()
    {
        var t = Texts();
        Assert.NotEmpty(t.Mail);
        Assert.NotEmpty(t.Board);
        Assert.NotEqual(t.Mail, t.Board);
        Assert.Equal("Mail", t.Mail);
        Assert.Equal("Board", t.Board);
    }

    [Fact]
    public void StyleOnShowRule()
    {
        foreach (var current in Enum.GetValues<BoardStyle>())
        {
            foreach (var last in Enum.GetValues<BoardStyle>())
            {
                foreach (var d in DefaultStyles)
                {
                    var want = d.IsLast ? last : d.Style;
                    Assert.Equal(want, StyleOnShow(d, last, current, pickedThisRun: false));
                    Assert.Equal(current, StyleOnShow(d, last, current, pickedThisRun: true));
                }
            }
        }
    }

    [Fact]
    public void DefaultStyleNicks()
    {
        Assert.Equal(["last", "list", "columns", "today"], DefaultStyles.Select(d => d.Nick));
        foreach (var d in DefaultStyles)
        {
            Assert.Equal(d, ParseDefaultStyle(d.Nick));
        }
        foreach (var junk in new[] { "", "List", "grid", null })
        {
            Assert.True(ParseDefaultStyle(junk).IsLast);
        }
        Assert.Equal(BoardStyle.List, ParseStyle("last"));
        Assert.Equal(["Last Used", "List", "Columns", "Today"], DefaultStyles.Select(Board.Text.DefaultStyleTitle));
        // The settings' nicks are the members' names in lower case, in the gschema's order.
        Assert.Equal(["list", "columns", "today", "last"], Enum.GetValues<DefaultStyle>().Select(d => d.Nick));
        Assert.Equal(["mail", "board", "last"], Enum.GetValues<StartChoice>().Select(c => c.Nick));
    }

    [Theory]
    [InlineData("mail", "board", true, Mode.Mail)]
    [InlineData("board", "mail", true, Mode.Board)]
    [InlineData("last", "board", true, Mode.Board)]
    [InlineData("last", "mail", true, Mode.Mail)]
    [InlineData("last", "", true, Mode.Mail)]
    [InlineData("last", "junk", true, Mode.Mail)]
    [InlineData("", "board", true, Mode.Mail)]
    [InlineData("junk", "board", true, Mode.Mail)]
    [InlineData("board", "board", false, Mode.Mail)]
    [InlineData("last", "board", false, Mode.Mail)]
    public void StartModeRule(string start, string last, bool enabled, Mode want) => Assert.Equal(want, StartMode(start, last, enabled));

    /// <summary>The start of a new window while the preferences may still come (Go TestStartDecision).</summary>
    [Theory]
    [InlineData(StartChoice.Mail, Mode.Board, false, true, false, false, 0, Mode.Mail, true)] // mail start decides at once
    [InlineData(StartChoice.Last, Mode.Mail, false, true, false, false, 0, Mode.Mail, true)] // last used with mail last decides at once
    [InlineData(StartChoice.Board, Mode.Mail, false, true, false, false, 1000, Mode.Mail, false)] // board start waits for prefs
    [InlineData(StartChoice.Last, Mode.Board, false, true, false, false, 4000, Mode.Mail, false)] // last used board waits for prefs
    [InlineData(StartChoice.Board, Mode.Mail, true, true, false, false, 1000, Mode.Board, true)] // board start, prefs on
    [InlineData(StartChoice.Last, Mode.Board, true, true, false, false, 0, Mode.Board, true)] // last used board, prefs on
    [InlineData(StartChoice.Board, Mode.Mail, true, false, false, false, 0, Mode.Mail, true)] // board start, board turned off
    [InlineData(StartChoice.Board, Mode.Mail, false, true, true, false, 0, Mode.Mail, true)] // user switched first
    [InlineData(StartChoice.Board, Mode.Mail, true, true, true, false, 0, Mode.Mail, true)] // user switched, prefs on too
    [InlineData(StartChoice.Last, Mode.Board, false, true, false, true, 1000, Mode.Mail, true)] // user acted in mail
    [InlineData(StartChoice.Board, Mode.Mail, true, true, false, true, 0, Mode.Mail, true)] // user acted in mail before prefs on
    [InlineData(StartChoice.Board, Mode.Mail, false, true, false, false, 5000, Mode.Mail, true)] // bound passed without prefs
    [InlineData(StartChoice.Last, Mode.Board, false, true, false, false, 60000, Mode.Mail, true)] // long after the bound
    [InlineData(StartChoice.Board, Mode.Mail, false, true, false, false, 4999, Mode.Mail, false)] // just under the bound
    public void StartDecisionRule(
        StartChoice start, Mode last, bool known, bool enabled, bool switched, bool acted, int waitedMs, Mode wantMode, bool wantDecided)
    {
        var (mode, decided) = StartDecision(start, last, known, enabled, switched, acted, TimeSpan.FromMilliseconds(waitedMs));
        Assert.Equal(wantMode, mode);
        Assert.Equal(wantDecided, decided);
    }

    [Fact]
    public void StartModeNicks()
    {
        foreach (var m in new[] { Mode.Mail, Mode.Board })
        {
            Assert.Equal(m, ParseMode(m.Nick));
        }
        Assert.Null(ParseMode("junk"));
        Assert.Equal(["mail", "board", "last"], StartModes.Select(s => s.Nick));
        Assert.Equal(["Mail", "Board", "Last Used"], StartModes.Select(Board.Text.StartModeTitle));
        foreach (var s in StartModes)
        {
            Assert.Equal(s, ParseStartChoice(s.Nick));
        }
    }

    [Fact]
    public void FilterOnShowRule()
    {
        AccountInfo[] accounts = [new(new AccountId("a"), "A"), new(new AccountId("b"), "B")];
        Assert.Equal("b", FilterOnShow("b", accounts));
        Assert.Equal("", FilterOnShow("c", accounts));
        Assert.Equal("", FilterOnShow("", accounts));
        Assert.Equal("", FilterOnShow("a", []));
    }

    [Fact]
    public void StyleNicks()
    {
        Assert.Equal(["list", "columns", "today"], Array.ConvertAll(Enum.GetValues<BoardStyle>(), s => s.Nick));
        foreach (var s in Enum.GetValues<BoardStyle>())
        {
            Assert.Equal(s, ParseStyle(s.Nick));
        }
        foreach (var junk in new[] { "", "List", "0", "1", "grid", " today", null })
        {
            Assert.Equal(BoardStyle.List, ParseStyle(junk));
        }
        Assert.Equal("list", ((BoardStyle)9).Nick);
    }
}
