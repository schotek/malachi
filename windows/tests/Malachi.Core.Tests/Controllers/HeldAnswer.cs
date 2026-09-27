// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The counterpart of the handlers of macos/Tests/MalachiCoreTests that
// sleep before they answer (WizardControllerTests, MailPreferencesTests:
// Task.sleep of 200 ms to 10 s, so that another call overtakes or the
// controller closes meanwhile): the answer waits until the test releases
// it, so the order is the test's and no clock is involved.

using System;
using System.Threading;
using System.Threading.Tasks;

namespace Malachi.Core.Tests.Controllers;

/// <summary>An answer of a fake daemon's handler, held back until <see cref="Release"/>.</summary>
internal sealed class HeldAnswer
{
    private static readonly TimeSpan Timeout = TimeSpan.FromSeconds(10);

    private readonly TaskCompletionSource arrived = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private readonly TaskCompletionSource released = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private int arrivals;

    /// <summary>How many calls reached the hold.</summary>
    public int Arrivals => Volatile.Read(ref arrivals);

    /// <summary>In the handler: notes the call, then waits for <see cref="Release"/>.</summary>
    public Task WaitAsync()
    {
        Interlocked.Increment(ref arrivals);
        arrived.TrySetResult();
        return released.Task;
    }

    /// <summary>Completes once a call reached the hold; fails after 10 s.</summary>
    public Task ArrivedAsync() => arrived.Task.WaitAsync(Timeout);

    /// <summary>Lets every held call, and every later one, answer.</summary>
    public void Release() => released.TrySetResult();
}
