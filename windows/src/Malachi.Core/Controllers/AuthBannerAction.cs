// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/SyncController.swift
// (SyncController.AuthBannerAction); GTK: ui/internal/window/sync.go
// (onAuthBannerButton, signInAgain). Swift's enum with associated values is
// a closed record hierarchy, compared by value.

using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

/// <summary>
/// What the sign-in banner's button does (window.go
/// <c>onAuthBannerButton</c>), by how the banner's account signs in.
/// </summary>
public abstract record AuthBannerAction
{
    // Only the cases below derive from it.
    private AuthBannerAction()
    {
    }

    /// <summary>A password account: the preferences, where it can be edited.</summary>
    public sealed record OpenPreferences : AuthBannerAction;

    /// <summary>
    /// A password account whose password is missing or was refused: its edit
    /// wizard, asking for the password.
    /// </summary>
    /// <param name="Account">The account to edit.</param>
    /// <param name="Reason">The notification's reason, for the wizard's banner.</param>
    public sealed record EditAccount(AccountId Account, ErrorCode Reason) : AuthBannerAction;

    /// <summary>
    /// GNOME Online Accounts holds the sign-in: its panel. There is none on
    /// Windows; the shell opens the preferences, as on macOS.
    /// </summary>
    public sealed record OpenOnlineAccounts : AuthBannerAction;

    /// <summary>
    /// The browser sign-in: a fresh session for the account
    /// (<see cref="SyncController.RequestSignInUrlAsync"/>), the notification's
    /// page when that fails.
    /// </summary>
    /// <param name="Account">The account to sign in again.</param>
    /// <param name="FallbackUrl">The notification's <c>authUrl</c>; null when it had none.</param>
    public sealed record SignInAgain(AccountId Account, string? FallbackUrl) : AuthBannerAction;
}
