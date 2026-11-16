// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: launch at login as Preferences and the start of the
// app see it, whichever way Windows keeps it: the Run value of an
// unpackaged app (LaunchAtLogin) or the startup task of an MSIX package
// (PackagedLaunchAtLogin). The counterpart of LoginItemService.swift's
// status, set and openSystemSettings.

using Malachi.Core.Settings;

namespace Malachi.Platform.Windows.Startup;

/// <summary>Launch at login of this app for the current user.</summary>
/// <remarks>
/// Every member asks Windows afresh and throws what Windows throws as
/// <see cref="System.UnauthorizedAccessException"/>,
/// <see cref="System.IO.IOException"/> or
/// <see cref="System.Security.SecurityException"/>; the caller shows it.
/// </remarks>
public interface ILaunchAtLogin
{
    /// <summary>What Windows will do at sign-in.</summary>
    LaunchAtLoginStatus Status { get; }

    /// <summary>Whether the app starts at sign-in.</summary>
    bool IsEnabled { get; }

    /// <summary>
    /// Turns it on or off and reports what Windows says afterwards. A
    /// switch the user or a policy turned off is never overwritten.
    /// </summary>
    LaunchAtLoginOutcome SetEnabled(bool enabled);

    /// <summary>
    /// Sets the <c>launch-at-login</c> key to <see cref="IsEnabled"/>, which
    /// it only mirrors, and returns it. Call it on the UI thread.
    /// </summary>
    bool MirrorInto(SettingsStore settings);

    /// <summary>Opens Settings → Apps → Startup; false when Windows could not.</summary>
    bool OpenSystemSettings();
}
