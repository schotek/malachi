// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardTests.swift; GTK:
// ui/internal/board/mode_test.go (TestInitialMode, TestAllows,
// TestModeForRequest, TestViewsMail, TestModeTexts, and the two Swift
// lacks: TestStyleOnShowRule, TestStyleNicks).

using System;
using Malachi.Core.Board;
using Xunit;
using static Malachi.Core.Board.Board;
using B = Malachi.Core.Board.Board;

namespace Malachi.Core.Tests.Board;

public sealed class BoardTests
{
    [Fact]
    public void InitialMode()
    {
        Assert.Equal(Mode.Mail, B.InitialMode);
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
            foreach (var d in Enum.GetValues<BoardStyle>())
            {
                Assert.Equal(d, StyleOnShow(current, d, firstShow: true));
                Assert.Equal(current, StyleOnShow(current, d, firstShow: false));
            }
        }
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
