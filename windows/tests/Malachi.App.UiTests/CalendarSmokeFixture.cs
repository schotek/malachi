// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The app of the calendar's smoke tests (CalendarSmokeTests and the
// classes of the calendar's other views): started once on an empty data
// folder with the calendar's sample data (MALACHI_CALENDAR_SAMPLES=1, read
// once at the start: every window's calendar source is the in-memory one
// over CalendarSamples, docs/calendar.md §5.10) for all of its tests, and
// quit when they are done. What a test changes stays in memory; writes say
// "the preview cannot do that".
//
// As BoardSmokeFixture: the Claude registration is the session's own
// (USERPROFILE, APPDATA and LOCALAPPDATA point into an empty folder of the
// fixture with only ~/.claude in it), so nothing of the machine's leaks in,
// and Open In is In App (assistant-target), so the calendar's ✦ can show
// once the Assistant is. The app's own data, socket and preferences have
// their variables already (AppSession).

using System;
using System.Collections.Generic;
using System.IO;
using System.Threading.Tasks;
using System.Windows.Automation;
using Microsoft.Win32;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>One app with the calendar's sample data for the calendar's smoke tests.</summary>
public sealed class CalendarSmokeFixture : IAsyncLifetime
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
            home = Path.Combine(Path.GetTempPath(), "muc-" + Guid.NewGuid().ToString("N")[..8]);
            var roaming = Path.Combine(home, "AppData", "Roaming");
            var local = Path.Combine(home, "AppData", "Local");
            Directory.CreateDirectory(roaming);
            Directory.CreateDirectory(local);
            Directory.CreateDirectory(Path.Combine(home, ".claude"));
            session = AppSession.Start(
                new Dictionary<string, string>
                {
                    ["MALACHI_CALENDAR_SAMPLES"] = "1",
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
        // calendar hides the sidebar that holds it).
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
