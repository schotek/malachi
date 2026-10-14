// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The app of BoardSmokeTests: started once on an empty data folder with the
// board's sample cases (MALACHI_BOARD_SAMPLES=1, read once at the start)
// for all of its tests, and quit when they are done. What a test changes
// stays in memory (InMemoryBoardSource), so each test uses cases of its own.
//
// The Claude registration is the session's own, so that the Preferences
// test sees both of its states whatever the machine has: USERPROFILE,
// APPDATA and LOCALAPPDATA point into an empty folder of the fixture
// (malachi-mcp reads ~/.claude.json and %APPDATA%\Claude there, the app
// Claude Desktop's package under %LOCALAPPDATA%\Packages), in which only
// ~/.claude exists: Claude Code is present and not registered, Claude
// Desktop absent (no "Restart Claude Desktop?"). The app's own data, socket
// and preferences have their variables already (AppSession). Open In is
// In App (assistant-target), so a registration shows the triage's group.

using System;
using System.Collections.Generic;
using System.IO;
using System.Threading.Tasks;
using System.Windows.Automation;
using Microsoft.Win32;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>One app with the sample board for <see cref="BoardSmokeTests"/>.</summary>
public sealed class BoardSmokeFixture : IAsyncLifetime
{
    private AppSession? session;
    private string? home;

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
            // Short, as the session's folder: nothing here lies under the socket.
            home = Path.Combine(Path.GetTempPath(), "muh-" + Guid.NewGuid().ToString("N")[..8]);
            var roaming = Path.Combine(home, "AppData", "Roaming");
            var local = Path.Combine(home, "AppData", "Local");
            Directory.CreateDirectory(roaming);
            Directory.CreateDirectory(local);
            Directory.CreateDirectory(Path.Combine(home, ".claude"));
            session = AppSession.Start(
                new Dictionary<string, string>
                {
                    ["MALACHI_BOARD_SAMPLES"] = "1",
                    ["USERPROFILE"] = home,
                    ["APPDATA"] = roaming,
                    ["LOCALAPPDATA"] = local,
                },
                preferences: k => k.SetValue("assistant-target", "app", RegistryValueKind.String));
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
        if (home is not null)
        {
            try
            {
                Directory.Delete(home, recursive: true);
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException)
            {
                // A file still held; the temporary folder keeps it.
            }
        }
        return ValueTask.CompletedTask;
    }
}
