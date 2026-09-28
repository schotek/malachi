// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/WizardController.swift
// (WizardController.OAuthState); GTK: ui/internal/accountwizard/oauth.go
// (oauthState). Swift mutates the struct in place; here a change is a
// `with` expression that replaces the controller's OAuth.

using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

public sealed partial class WizardController
{
    /// <summary>The browser sign-in the page is about (oauth.go <c>oauthState</c>).</summary>
    public sealed record OAuthState
    {
        /// <summary>Whose account it is.</summary>
        public LinkedProvider? Provider { get; init; }

        /// <summary>
        /// The provider's name for the texts (<c>OAuth.OAuthProviderLabel</c>):
        /// a constant, never the daemon's <c>providerName</c>.
        /// </summary>
        public required string Name { get; init; }

        /// <summary>
        /// The new account to sign in (source daemon, from <c>account.discover</c>);
        /// null when editing, where the account's id is signed in again.
        /// </summary>
        public AccountConfig? Config { get; init; }

        /// <summary>
        /// The app-password account offered when no client is configured;
        /// null when the provider has none (Microsoft 365).
        /// </summary>
        public AccountConfig? PasswordAlt { get; init; }

        /// <summary>The session <c>account.oauthStart</c> opened; null before and after.</summary>
        public string? SessionId { get; init; }

        /// <summary>The provider's page for the session, to open again.</summary>
        public string? AuthUrl { get; init; }

        /// <summary>The session completed: its id goes into the credentials.</summary>
        public bool Complete { get; init; }
    }
}
