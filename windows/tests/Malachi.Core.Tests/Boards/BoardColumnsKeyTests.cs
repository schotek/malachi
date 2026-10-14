// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/board/columns_key_test.go (TestSidewaysTarget):
// Board.SidewaysTarget, Left and Right in the Columns style.

using Xunit;
using static Malachi.Core.Boards.Board;

namespace Malachi.Core.Tests.Boards;

public sealed class BoardColumnsKeyTests
{
    public static TheoryData<string, int[], int, int, int, int, int, bool> Cases => new()
    {
        { "right keeps position", [3, 4, 2, 1], 0, 2, 1, 1, 2, true },
        { "right clamps to last", [3, 4, 2, 1], 1, 3, 1, 2, 1, true },
        { "left keeps position", [3, 4, 2, 1], 1, 1, -1, 0, 1, true },
        { "empty column is passed over", [3, 0, 0, 2], 0, 1, 1, 3, 1, true },
        { "empty column left", [2, 0, 3], 2, 2, -1, 0, 1, true },
        { "right edge", [3, 2], 1, 0, 1, 0, 0, false },
        { "left edge", [3, 2], 0, 0, -1, 0, 0, false },
        { "only empty beyond", [3, 0, 0], 0, 0, 1, 0, 0, false },
        { "nothing selected counts as first", [3, 2], 0, -1, 1, 1, 0, true },
        { "bad delta", [3, 2], 0, 0, 2, 0, 0, false },
        { "bad column", [3, 2], 5, 0, 1, 0, 0, false },
        { "no columns", [], 0, 0, 1, 0, 0, false },
    };

    [Theory]
    [MemberData(nameof(Cases))]
    public void SidewaysTargetCases(string name, int[] counts, int from, int pos, int delta, int col, int row, bool ok)
    {
        var got = SidewaysTarget(counts, from, pos, delta, out var c, out var r);
        Assert.True(got == ok, name);
        if (ok)
        {
            Assert.True(c == col && r == row, $"{name}: got ({c},{r}), want ({col},{row})");
        }
    }
}
