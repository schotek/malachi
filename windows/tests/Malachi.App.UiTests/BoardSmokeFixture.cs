// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The app of BoardSmokeTests: started once on an empty data folder with the
// board's sample cases (MALACHI_BOARD_SAMPLES=1, read once at the start)
// for all of its tests, and quit when they are done. What a test changes
// stays in memory (InMemoryBoardSource), so each test uses cases of its own.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>One app with the sample board for <see cref="BoardSmokeTests"/>.</summary>
public sealed class BoardSmokeFixture : IAsyncLifetime
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
            session = AppSession.Start(new Dictionary<string, string> { ["MALACHI_BOARD_SAMPLES"] = "1" });
        }
        return ValueTask.CompletedTask;
    }

    /// <inheritdoc/>
    public ValueTask DisposeAsync()
    {
        // Back in Mail, where the session's Quit finds the primary menu (the
        // board hides the sidebar that holds it).
        try
        {
            if (session is { HasExited: false } s && Uia.TryFind(Uia.WindowWith(s.ProcessId, "ModeMail"), "ModeMail") is { } mail
                && ((TogglePattern)mail.GetCurrentPattern(TogglePattern.Pattern)).Current.ToggleState != ToggleState.On)
            {
                Uia.Toggle(mail);
                Uia.Find(s.MainWindow, "MainMenuButton");
            }
        }
        catch (Exception e) when (e is TimeoutException or ElementNotAvailableException or InvalidOperationException)
        {
            // The session's Dispose kills what it cannot quit.
        }
        session?.Dispose();
        return ValueTask.CompletedTask;
    }
}
