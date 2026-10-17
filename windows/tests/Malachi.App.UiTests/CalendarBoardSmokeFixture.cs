// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The app of the invitation and Today-strip smoke tests
// (CalendarInvitationSmokeTests): CalendarSmokeFixture's session with the
// board's sample cases as well (MALACHI_CALENDAR_SAMPLES=1 and
// MALACHI_BOARD_SAMPLES=1, both read once at the start), because the Board
// shows its status page in place of the Today style while it has no case,
// and the Today strip lives in that style. As the other fixtures: the Claude
// registration is the session's own (USERPROFILE, APPDATA and LOCALAPPDATA in
// an empty folder with only ~/.claude), Open In is In App (assistant-target),
// and the session is quit when its tests are done.

using System;
using System.Collections.Generic;
using System.IO;
using System.Threading.Tasks;
using System.Windows.Automation;
using Microsoft.Win32;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>One app with the calendar's and the board's sample data.</summary>
public sealed class CalendarBoardSmokeFixture : IAsyncLifetime
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
            home = Path.Combine(Path.GetTempPath(), "mui-" + Guid.NewGuid().ToString("N")[..8]);
            var roaming = Path.Combine(home, "AppData", "Roaming");
            var local = Path.Combine(home, "AppData", "Local");
            Directory.CreateDirectory(roaming);
            Directory.CreateDirectory(local);
            Directory.CreateDirectory(Path.Combine(home, ".claude"));
            session = AppSession.Start(
                new Dictionary<string, string>
                {
                    ["MALACHI_CALENDAR_SAMPLES"] = "1",
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
        // Board and the Calendar hide the sidebar that holds it).
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
