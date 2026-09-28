// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the arguments of macos/Sources/MalachiCore/Controllers/
// SyncController.swift's onAuthBanner and onCertBanner callbacks
// (AccountID?, String?, String?); GTK: ui/internal/window/sync.go
// (showAuthRequired, hideAuthBanner, refreshCertBanner). Windows-only
// type: a C# event carries one argument.

using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

/// <summary>
/// A banner of the main window shown for an account, or hidden
/// (<see cref="Hidden"/>: every member null).
/// </summary>
/// <param name="Account">The account the banner is up for; null when hidden.</param>
/// <param name="Title">The sentence; null when hidden.</param>
/// <param name="Button">The button's label without its mnemonic; null when hidden.</param>
public readonly record struct SyncBanner(AccountId? Account, string? Title, string? Button)
{
    /// <summary>The banner hidden (Swift's three nils).</summary>
    public static SyncBanner Hidden => default;

    /// <summary>Whether the banner is hidden.</summary>
    public bool IsHidden => Account is null && Title is null && Button is null;
}
