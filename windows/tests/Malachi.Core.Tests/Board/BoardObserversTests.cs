// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/board/observers_test.go (TestObservers); the Swift
// BoardObservers has no test of its own.

using System.Collections.Generic;
using Malachi.Core.Board;
using Xunit;

namespace Malachi.Core.Tests.Board;

public sealed class BoardObserversTests
{
    [Fact]
    public void HandlersRunInOrderAndCancel()
    {
        var o = new BoardObservers();
        o.Notify(); // a new one works
        var log = new List<string>();
        BoardObserverToken? second = null;
        var first = o.Add(() =>
        {
            log.Add("first");
            second!.Cancel(); // another's token: not called after this
        });
        second = o.Add(() => log.Add("second"));
        BoardObserverToken? third = null;
        third = o.Add(() =>
        {
            log.Add("third");
            third!.Cancel(); // its own
        });
        o.Notify();
        Assert.Equal(["first", "third"], log);
        Assert.Equal(1, o.Count);
        log.Clear();
        o.Notify();
        Assert.Equal(["first"], log);
        first.Cancel();
        first.Cancel(); // twice is nothing
        log.Clear();
        o.Notify();
        Assert.Empty(log);
        // Added during a round: called from the next one.
        o.Add(() =>
        {
            log.Add("outer");
            o.Add(() => log.Add("inner"));
        });
        o.Notify();
        Assert.Equal(["outer"], log);
        log.Clear();
        o.Notify();
        Assert.Equal(["outer", "inner"], log);
    }
}
