// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the pages of Windows Settings the app sends the user
// to where Windows keeps the decision (docs/windows-port.md §10): Default
// apps for mailto: (an app cannot make itself the default) and Startup apps
// for launch at login (a user's "off" there is never overwritten). The
// counterpart of SMAppService.openSystemSettingsLoginItems in
// macos/Sources/MalachiMail/App/LoginItemService.swift; GTK has none (the
// portal asks, GNOME Settings chooses the mail app).

using System;
using System.ComponentModel;
using System.Diagnostics;
using Malachi.Core;

namespace Malachi.Platform.Windows.Registration;

/// <summary>Opens pages of Windows Settings.</summary>
public static class SystemSettings
{
    /// <summary>
    /// Settings → Apps → Default apps, on Malachi Mail's own page: the
    /// value is the name under <c>HKCU\Software\RegisteredApplications</c>,
    /// URL-encoded (Windows 11 21H2 with the 2023-04 update and later).
    /// </summary>
    public static string DefaultAppsUri { get; } =
        "ms-settings:defaultapps?registeredAppUser=" + Uri.EscapeDataString(AppIdentity.DisplayName);

    /// <summary>Settings → Apps → Startup.</summary>
    public const string StartupAppsUri = "ms-settings:startupapps";

    /// <summary>
    /// Opens <paramref name="uri"/>, which must be an <c>ms-settings:</c>
    /// page, through the shell. False when Windows could not open it.
    /// </summary>
    public static bool Open(string uri)
    {
        ArgumentNullException.ThrowIfNull(uri);
        if (!uri.StartsWith("ms-settings:", StringComparison.OrdinalIgnoreCase))
        {
            throw new ArgumentException("not a Settings page", nameof(uri));
        }
        try
        {
            using var process = Process.Start(new ProcessStartInfo(uri) { UseShellExecute = true });
            return true;
        }
        catch (Win32Exception)
        {
            return false;
        }
    }
}
