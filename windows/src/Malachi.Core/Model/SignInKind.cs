// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/Provider.swift (SignInKind); GTK:
// ui/internal/signin/signin.go (Kind).

namespace Malachi.Core.Model;

/// <summary>
/// Where an account's sign-in lives, and so where it is repaired
/// (signin.Kind).
/// </summary>
public enum SignInKind
{
    /// <summary>
    /// A password (or app password) the user types; the servers are the
    /// user's to edit.
    /// </summary>
    Password,

    /// <summary>
    /// GNOME Online Accounts holds the sign-in; it is fixed there. No such
    /// account exists on Windows, but a profile may carry one.
    /// </summary>
    Goa,

    /// <summary>
    /// The daemon's own sign-in in the browser (source <c>daemon</c>); it is
    /// fixed by signing in again.
    /// </summary>
    OAuth,
}
