// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/SignIn.swift (Discovery); GTK:
// ui/internal/signin/signin.go (Discovery). Swift's nested Discovery.Path is
// SignIn.Path (Go's signin.Path): C# allows no nested type named like a
// member of the same type.

using Malachi.Core.Api;

namespace Malachi.Core.Wizard;

/// <summary>
/// signin.Discovery: what <see cref="SignIn.ClassifyDiscovery"/> decided.
/// Records are immutable, so nothing here aliases the result it came from.
/// </summary>
public sealed record Discovery
{
    /// <summary>The page the wizard continues on.</summary>
    public required SignIn.Path Path { get; init; }

    /// <summary>The primary config; null on a failed or empty discovery.</summary>
    public AccountConfig? Config { get; init; }

    /// <summary>The daemon's own sign-in offered from the GNOME Online Accounts hint.</summary>
    public AccountConfig? OAuthAlt { get; init; }

    /// <summary>
    /// The IMAP/SMTP account with an app password (Google), offered when the
    /// browser sign-in is not configured.
    /// </summary>
    public AccountConfig? PasswordAlt { get; init; }

    /// <summary>Whose account it is, for the texts (never the untrusted <c>providerName</c>).</summary>
    public LinkedProvider? Provider { get; init; }
}
