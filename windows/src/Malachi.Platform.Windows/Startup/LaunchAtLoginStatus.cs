// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: what Windows will do at sign-in, the counterpart of
// SMAppService.Status in macos/Sources/MalachiMail/App/LoginItemService.swift
// (notRegistered, enabled, requiresApproval) and of the Background portal's
// answer behind ui/internal/background.

namespace Malachi.Platform.Windows.Startup;

/// <summary>Whether Malachi Mail starts when the user signs in.</summary>
public enum LaunchAtLoginStatus
{
    /// <summary>No Run entry: it does not start.</summary>
    NotRegistered,

    /// <summary>A Run entry that Windows starts.</summary>
    Enabled,

    /// <summary>
    /// A Run entry the user turned off in Windows Settings or Task Manager:
    /// it does not start, and only the user can turn it on again there (the
    /// app never overwrites that choice).
    /// </summary>
    DisabledByUser,

    /// <summary>
    /// The startup task of an MSIX package that the organisation's policy
    /// turned off: it does not start and nothing the user or the app does
    /// turns it on. The unpackaged app's Run value never has this state.
    /// </summary>
    DisabledByPolicy,
}
