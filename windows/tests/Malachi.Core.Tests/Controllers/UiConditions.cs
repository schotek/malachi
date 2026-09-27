// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The counterpart of the waitUntil polls of macos/Tests/MalachiCoreTests
// (WizardControllerTests, MailPreferencesTests, MCPRegistrationTests) for
// the moments a test cannot wait for everything to settle: a call is held
// in the fake daemon or a bridge process on purpose, so IdleAsync would
// wait for ever. The recorder of a suite calls Changed after every event
// it records; a condition is evaluated on the UI thread, where the
// controller and the recorder live, never polled.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Tests.Fixtures;

namespace Malachi.Core.Tests.Controllers;

/// <summary>Conditions over what a controller reported, met as the reports arrive.</summary>
internal sealed class UiConditions
{
    private static readonly TimeSpan Timeout = TimeSpan.FromSeconds(10);

    private readonly List<(Func<bool> Condition, TaskCompletionSource Met)> waiting = [];

    /// <summary>Re-evaluates the conditions waited for; on the UI thread, after every report.</summary>
    public void Changed()
    {
        for (var i = waiting.Count - 1; i >= 0; i--)
        {
            var (condition, met) = waiting[i];
            if (condition())
            {
                waiting.RemoveAt(i);
                met.TrySetResult();
            }
        }
    }

    /// <summary>
    /// Completes once <paramref name="condition"/> holds on
    /// <paramref name="ui"/>'s thread: at once when it does already,
    /// otherwise after the report that makes it true; fails after 10 s.
    /// </summary>
    public async Task WhenAsync(TestUIContext ui, Func<bool> condition, string? what = null)
    {
        ArgumentNullException.ThrowIfNull(ui);
        var met = await ui.RunAsync(() =>
        {
            if (condition())
            {
                return Task.CompletedTask;
            }
            var signal = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            waiting.Add((condition, signal));
            return signal.Task;
        });
        try
        {
            await met.WaitAsync(Timeout);
        }
        catch (TimeoutException)
        {
            throw new TimeoutException(what ?? "the condition did not hold in time");
        }
        // Whatever the report that met it posted runs before the test goes on.
        await ui.DrainAsync();
    }
}
