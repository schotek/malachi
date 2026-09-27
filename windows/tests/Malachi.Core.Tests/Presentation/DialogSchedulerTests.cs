// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// DialogScheduler (Core/Presentation): one ContentDialog at a time per
// window (docs/windows-port.md §11.4), in the order they were asked, a
// failed one not blocking the next.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class DialogSchedulerTests
{
    [Fact]
    public async Task DialogsRunOneAfterTheOtherInOrder()
    {
        var dialogs = new DialogScheduler();
        var log = new List<string>();
        var firstAnswer = new TaskCompletionSource<bool>();
        var first = dialogs.Enqueue(async () =>
        {
            log.Add("show 1");
            var answer = await firstAnswer.Task;
            log.Add("end 1");
            return answer;
        });
        var second = dialogs.Enqueue(() =>
        {
            log.Add("show 2");
            return Task.FromResult("two");
        });
        Assert.True(dialogs.IsBusy);
        Assert.Equal(2, dialogs.Count);
        Assert.Equal(["show 1"], log);
        Assert.False(second.IsCompleted);

        firstAnswer.SetResult(true);
        Assert.True(await first);
        Assert.Equal("two", await second);
        Assert.Equal(["show 1", "end 1", "show 2"], log);
        Assert.False(dialogs.IsBusy);
    }

    [Fact]
    public async Task AFailedDialogDoesNotBlockTheNext()
    {
        var dialogs = new DialogScheduler();
        var failed = dialogs.Enqueue<int>(() => throw new InvalidOperationException("dialog broke"));
        var next = dialogs.Enqueue(() => Task.FromResult(7));
        await Assert.ThrowsAsync<InvalidOperationException>(() => failed);
        Assert.Equal(7, await next);
        Assert.Equal(0, dialogs.Count);
    }
}
