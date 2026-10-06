// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardUnstarRuleTests.swift; GTK:
// ui/internal/board/unstar_test.go (TestCanUnstarAgreesWithTheDetail).
// Board.CanUnstar (the context menu's Unstar) agrees with the
// detail's CanUnstar.

using Malachi.Core.Api;
using Xunit;
using static Malachi.Core.Board.Board;
using B = Malachi.Core.Board.Board;
using F = Malachi.Core.Tests.Board.BoardFixture;

namespace Malachi.Core.Tests.Board;

public sealed class BoardUnstarRuleTests
{
    [Fact]
    public void AgreesWithTheDetail()
    {
        foreach (var reason in new BoardReason[] { BoardReason.HotFlagged, BoardReason.HotImportant, BoardReason.YouAddressed })
        {
            foreach (var done in new[] { false, true })
            {
                foreach (var user in new State?[] { null, State.Info })
                {
                    var c = F.Mk("c1", State.Hot, user: user, done: done) with { RuleReason = reason };
                    var d = Assert.IsType<Detail>(F.View([c], configure: v => done ? v with { Filter = Filter.Done } : v).Detail);
                    Assert.True(CanUnstar(c) == d.CanUnstar, $"{reason.Value} done {done} user {user}");
                    Assert.Equal(reason.Value == BoardReason.HotFlagged && !done, CanUnstar(c));
                }
            }
        }
    }
}
