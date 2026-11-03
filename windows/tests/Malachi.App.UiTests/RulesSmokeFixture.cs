// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The app of the mail rules' smoke tests (RulesSmokeTests): started once on
// an empty data folder with the invented rules (MALACHI_RULES_SAMPLES=1: the
// rules controllers talk to RulesSampleCaller, an in-memory daemon of the
// rules methods; docs/plans/rules.md, docs/windows-port.md §11.17) for all
// of its tests, and quit when they are done. What a test changes stays in
// memory.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>One app with the invented rules for the rules' smoke tests.</summary>
public sealed class RulesSmokeFixture : IAsyncLifetime
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
            session = AppSession.Start(new Dictionary<string, string> { ["MALACHI_RULES_SAMPLES"] = "1" });
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
