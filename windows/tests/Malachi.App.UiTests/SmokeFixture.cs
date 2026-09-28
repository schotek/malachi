// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The app of SmokeTests: started once on an empty data folder (no account)
// for all of its tests, each of which leaves only the main window open, and
// quit when they are done.

using System;
using System.Threading.Tasks;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>One app for <see cref="SmokeTests"/>.</summary>
public sealed class SmokeFixture : IAsyncLifetime
{
    private AppSession? session;

    /// <summary>Why the tests cannot run here; null when the app runs.</summary>
    public string? SkipReason { get; private set; }

    /// <summary>The app.</summary>
    internal AppSession Session => session ?? throw new InvalidOperationException(SkipReason ?? "the app did not start");

    /// <inheritdoc/>
    public ValueTask InitializeAsync()
    {
        SkipReason = UiEnvironment.SkipReason;
        if (SkipReason is null)
        {
            session = AppSession.Start();
        }
        return ValueTask.CompletedTask;
    }

    /// <inheritdoc/>
    public ValueTask DisposeAsync()
    {
        session?.Dispose();
        return ValueTask.CompletedTask;
    }
}
