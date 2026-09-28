// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The preferences of a UI test's app are a registry key of the session's
// own (AppSession, MALACHI_SETTINGS_KEY), never the user's
// HKCU\Software\io.github.schotek.Malachi: what the session writes there
// before the start is what the app reads (window-maximized, which shows as
// the main window's state), and the key is gone once the session is
// disposed.

using System;
using System.Windows.Automation;
using Microsoft.Win32;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>The session's own preferences key.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class PreferencesTests
{
    [Fact]
    public void TheAppReadsThePreferencesOfTheSessionsOwnKey()
    {
        Assert.SkipWhen(UiEnvironment.SkipReason is not null, UiEnvironment.SkipReason ?? "");
        string key;
        using (var app = AppSession.Start(preferences: k => k.SetValue("window-maximized", 1, RegistryValueKind.DWord)))
        {
            key = app.SettingsKey;
            Assert.StartsWith(@"Software\" + AppSession.SettingsKeyPrefix, key, StringComparison.Ordinal);
            var window = (WindowPattern)app.MainWindow.GetCurrentPattern(WindowPattern.Pattern);
            Uia.WaitFor(
                () => window.Current.WindowVisualState == WindowVisualState.Maximized,
                "the main window maximised by the session's preferences");
        }
        using var left = Registry.CurrentUser.OpenSubKey(key);
        Assert.Null(left);
    }
}
