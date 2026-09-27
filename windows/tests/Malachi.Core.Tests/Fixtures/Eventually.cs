// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the eventually/waitUntil helpers of
// macos/Tests/MalachiCoreTests (RPCClientTests.swift, HandshakeTests.swift):
// for the transport tests, where the thing waited for is another thread's
// (a socket's) doing. The controller tests use Quiescence instead.

using System;
using System.Threading.Tasks;

namespace Malachi.Core.Tests.Fixtures;

internal static class Eventually
{
    /// <summary>Polls <paramref name="condition"/> until it holds, for at most <paramref name="timeout"/> (5 s).</summary>
    public static async Task Holds(Func<bool> condition, TimeSpan? timeout = null, string? what = null)
    {
        var deadline = DateTime.UtcNow + (timeout ?? TimeSpan.FromSeconds(5));
        while (!condition())
        {
            if (DateTime.UtcNow > deadline)
            {
                throw new TimeoutException(what ?? "the condition did not hold in time");
            }
            await Task.Delay(10);
        }
    }
}
