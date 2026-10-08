// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The board's settings over their gschema keys: board-default-style (the
// nicks of Board.DefaultStyle, an unknown one read as Last Used, the
// default; Go board.ParseDefaultStyle), board-last-style (BoardStyle, an
// unknown one the List), board-start-mode (Board.StartChoice, an unknown
// one Mail), board-last-mode and board-account-filter (plain strings).

using Malachi.Core.Boards;
using Malachi.Core.Settings;
using Xunit;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardSettingsTests
{
    [Fact]
    public void DefaultStyleIsStoredAsItsNick()
    {
        var backend = new InMemorySettingsBackend();
        using var s = new SettingsStore(backend, null);
        Assert.Equal(Board.DefaultStyle.Last, s.BoardDefaultStyle);
        var fired = 0;
        s.OnChange(SettingsKey.BoardDefaultStyle, () => fired++);
        s.BoardDefaultStyle = Board.DefaultStyle.Today;
        Assert.True(backend.TryGetString("board-default-style", out var nick));
        Assert.Equal("today", nick);
        Assert.Equal(Board.DefaultStyle.Today, s.BoardDefaultStyle);
        s.BoardDefaultStyle = Board.DefaultStyle.Today; // no change, no handler
        s.BoardDefaultStyle = (Board.DefaultStyle)9; // outside the enum: ignored
        Assert.Equal(1, fired);
        s.BoardDefaultStyle = Board.DefaultStyle.Last;
        Assert.True(backend.TryGetString("board-default-style", out nick));
        Assert.Equal("last", nick);
        backend.SetString("board-default-style", "grid");
        Assert.Equal(Board.DefaultStyle.Last, s.BoardDefaultStyle);
        backend.SetString("board-default-style", "columns");
        Assert.Equal(Board.DefaultStyle.Columns, s.BoardDefaultStyle);
    }

    [Fact]
    public void LastStyleStartModeAndFilter()
    {
        var backend = new InMemorySettingsBackend();
        using var s = new SettingsStore(backend, null);
        Assert.Equal(BoardStyle.List, s.BoardLastStyle);
        Assert.Equal(Board.StartChoice.Mail, s.BoardStartMode);
        Assert.Equal("mail", s.BoardLastMode);
        Assert.Equal("", s.BoardAccountFilter);

        s.BoardLastStyle = BoardStyle.Columns;
        s.BoardStartMode = Board.StartChoice.Last;
        s.BoardLastMode = Board.Mode.Board.Nick;
        s.BoardAccountFilter = "acc_1";
        Assert.True(backend.TryGetString("board-last-style", out var style) && style == "columns");
        Assert.True(backend.TryGetString("board-start-mode", out var start) && start == "last");
        Assert.True(backend.TryGetString("board-last-mode", out var mode) && mode == "board");
        Assert.True(backend.TryGetString("board-account-filter", out var filter) && filter == "acc_1");
        Assert.Equal(Board.Mode.Board, Board.StartMode(s.BoardStartMode, s.BoardLastMode, boardEnabled: true));

        backend.SetString("board-start-mode", "grid");
        Assert.Equal(Board.StartChoice.Mail, s.BoardStartMode);
        backend.SetString("board-last-style", "last");
        Assert.Equal(BoardStyle.List, s.BoardLastStyle);
    }
}
