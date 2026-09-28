// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/WizardController.swift
// (WizardController.OAuthView); GTK: account_wizard.blp (oauth_stack:
// oauth_prompt, oauth_waiting, oauth_unavailable) and
// ui/internal/accountwizard/oauth.go (showOAuthStack). Swift's enum with
// associated values is a closed record hierarchy, compared by value.

namespace Malachi.Core.Controllers;

public sealed partial class WizardController
{
    /// <summary>What the browser sign-in page shows (the <c>oauth_stack</c> of the Blueprint).</summary>
    public abstract record OAuthView
    {
        // Only the cases below derive from it.
        private OAuthView()
        {
        }

        /// <summary>"Sign In Through Your Browser" with the provider's button.</summary>
        /// <param name="Description">The page's text.</param>
        /// <param name="SignInLabel">The button's label, without the mnemonic marker.</param>
        public sealed record Prompt(string Description, string SignInLabel) : OAuthView;

        /// <summary>The browser is open; the page continues by itself.</summary>
        public sealed record Waiting : OAuthView;

        /// <summary>No OAuth client for the provider; the app password is offered when the provider has one.</summary>
        /// <param name="Description">The page's text.</param>
        /// <param name="PasswordAlternative">Whether "Use an App Password Instead" is offered.</param>
        public sealed record Unavailable(string Description, bool PasswordAlternative) : OAuthView;
    }
}
