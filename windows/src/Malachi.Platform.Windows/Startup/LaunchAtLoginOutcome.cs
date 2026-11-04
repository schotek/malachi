// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/LoginItemService.swift (Outcome);
// GTK: ui/internal/window/preferences.go (bindLaunchAtLogin: the switch
// flips only when the portal granted the change, "Autostart was not
// granted" otherwise).

namespace Malachi.Platform.Windows.Startup;

/// <summary>What <see cref="LaunchAtLogin.Set"/> achieved, as Windows says afterwards.</summary>
public enum LaunchAtLoginOutcome
{
    /// <summary>The entry is now in the wanted state: flip the switch and the mirror key.</summary>
    Granted,

    /// <summary>
    /// Turning it on needs the user in Windows Settings → Apps → Startup,
    /// where it was turned off (<see cref="LaunchAtLoginStatus.DisabledByUser"/>;
    /// Swift <c>requiresApproval</c>): revert the switch, say "Autostart was
    /// not granted" and offer <see cref="LaunchAtLogin.OpenSystemSettings"/>.
    /// </summary>
    RequiresApproval,

    /// <summary>The call went through but Windows does not report the wanted state.</summary>
    NotGranted,
}
