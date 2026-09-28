// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/SyncController.swift
// (SyncController.SignInURL); GTK: ui/internal/window/sync.go
// (signInInBrowser's two outcomes).

namespace Malachi.Core.Controllers;

/// <summary>The outcome of <see cref="SyncController.RequestSignInUrlAsync"/>.</summary>
public abstract record SignInUrl
{
    // Only the cases below derive from it.
    private SignInUrl()
    {
    }

    /// <summary>Open this page in the browser.</summary>
    /// <param name="Url">An https address.</param>
    public sealed record Open(string Url) : SignInUrl;

    /// <summary>Nothing to open; the toast.</summary>
    /// <param name="Text">The sentence to show.</param>
    public sealed record Failed(string Text) : SignInUrl;
}
