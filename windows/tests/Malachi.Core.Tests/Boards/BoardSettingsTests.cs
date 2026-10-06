// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// SettingsStore.BoardDefaultStyle over the gschema key board-default-style:
// the nicks of BoardStyle, an unknown one read as the List (Go
// board.ParseStyle, Swift Board.parseStyle).

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
        Assert.Equal(BoardStyle.List, s.BoardDefaultStyle);
        var fired = 0;
        s.OnChange(SettingsKey.BoardDefaultStyle, () => fired++);
        s.BoardDefaultStyle = BoardStyle.Today;
        Assert.True(backend.TryGetString("board-default-style", out var nick));
        Assert.Equal("today", nick);
        Assert.Equal(BoardStyle.Today, s.BoardDefaultStyle);
        s.BoardDefaultStyle = BoardStyle.Today; // no change, no handler
        s.BoardDefaultStyle = (BoardStyle)9; // outside the enum: ignored
        Assert.Equal(1, fired);
        backend.SetString("board-default-style", "grid");
        Assert.Equal(BoardStyle.List, s.BoardDefaultStyle);
        backend.SetString("board-default-style", "columns");
        Assert.Equal(BoardStyle.Columns, s.BoardDefaultStyle);
    }
}
