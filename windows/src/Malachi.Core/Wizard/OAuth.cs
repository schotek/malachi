// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/OAuth.swift; GTK:
// ui/internal/accountwizard/oauth.go (providerLabel, showOAuthPrompt,
// showOAuthUnavailable, oauthErrorText, BrowserPage, launch) and
// ui/internal/signin (BrowserURL, TestNeedsSignIn).
//
// The texts and rules of the browser sign-in: the prompt, the page without
// a configured client, the failures of account.oauthStart and
// account.oauthWait, the page the browser shows, and the addresses that may
// be opened. The provider is named by Provider.ProviderName, never by the
// untrusted providerName of account.discover.

using System;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Text;

namespace Malachi.Core.Wizard;

/// <summary>The browser sign-in's texts and checks.</summary>
public static class OAuth
{
    /// <summary>
    /// oauth.go providerLabel: the name the sign-in texts use for a provider:
    /// its brand name, or for a provider this client does not know the
    /// address's domain.
    /// </summary>
    public static string OAuthProviderLabel(LinkedProvider? provider, string email)
    {
        ArgumentNullException.ThrowIfNull(email);
        var name = Provider.ProviderName(provider);
        return name.Length > 0 ? name : Fields.SuggestAccountName(email);
    }

    /// <summary>The prompt page's description (oauth.go showOAuthPrompt); <paramref name="name"/> is <see cref="OAuthProviderLabel"/>'s.</summary>
    public static string OAuthPromptText(string name) =>
        // TRANSLATORS: %s is a provider such as "Google" or "Microsoft 365".
        L10n.T("Your browser will open so you can sign in to %s. Malachi Mail never sees your password; it only receives permission to read and send your mail.", name);

    /// <summary>The prompt page's button, with its mnemonic marker (oauth.go showOAuthPrompt).</summary>
    public static string OAuthSignInLabel(string name) =>
        // TRANSLATORS: %s is a provider such as "Google" or "Microsoft 365".
        L10n.T("_Sign In with %s", name);

    /// <summary>
    /// The description of the page shown when the daemon has no OAuth client
    /// for the provider (oauth.go showOAuthUnavailable).
    /// </summary>
    public static string OAuthUnavailableText(string name) =>
        // TRANSLATORS: %s is a provider such as "Google" or "Microsoft 365".
        L10n.T("No OAuth client is configured for %s on this computer. Add a client ID to the mail backend's configuration and try again.", name);

    /// <summary>
    /// oauth.go oauthErrorText over signin.ClassifyFailure: the toast for a
    /// sign-in that ended without an account: the user or the provider said
    /// no, the session or the call ran out of time, the browser signed in to
    /// another mailbox, or the call failed.
    /// </summary>
    public static string OAuthErrorText(string name, Exception? error)
    {
        ArgumentNullException.ThrowIfNull(name);
        var (failure, signedInAs) = SignIn.ClassifyFailure(error);
        switch (failure)
        {
            case SignIn.Failure.Cancelled:
                return L10n.T("The sign-in was cancelled");
            case SignIn.Failure.Refused:
                // TRANSLATORS: %s is a provider such as "Google" or "Microsoft 365".
                return L10n.T("The sign-in with %s was refused", name);
            case SignIn.Failure.Timeout:
                return L10n.T("The sign-in took too long; try again");
            case SignIn.Failure.WrongAccount:
                // TRANSLATORS: %s is an e-mail address.
                return L10n.T("The browser signed in to %s, not to this address", signedInAs);
            default:
                // TRANSLATORS: progressive form for the RPC error text
                return RpcErrorText.Text(L10n.T("Signing in"), error);
        }
    }

    /// <summary>
    /// oauth.go BrowserPage: the page the browser shows once the provider sent
    /// it back to the daemon, in the user's language; every
    /// <c>account.oauthStart</c> passes it.
    /// </summary>
    public static OAuthBrowserPage BrowserPage() => new()
    {
        // TRANSLATORS: title of the page the browser shows
        SuccessTitle = L10n.T("Signed in"),
        // TRANSLATORS: page the browser shows
        SuccessText = L10n.T("You can close this tab and return to Malachi Mail."),
        // TRANSLATORS: title of the page the browser shows
        FailureTitle = L10n.T("Sign-in failed"),
        // TRANSLATORS: page the browser shows
        FailureText = L10n.T("Return to Malachi Mail and try again."),
    };

    /// <summary>signin.BrowserURL (Swift <c>isBrowserURL</c>): see <see cref="SignIn.BrowserUrl"/>.</summary>
    public static bool IsBrowserUrl(string s) => SignIn.BrowserUrl(s);

    /// <summary>
    /// The toast for a sign-in address that is refused before the browser
    /// sees it (oauth.go launch, widget.LaunchErrorText of errNotHTTPS).
    /// </summary>
    public static string RefusedBrowserUrlText() =>
        // TRANSLATORS: %s is a technical error message.
        L10n.T("The link could not be opened: %s", "not an https address"); // Windows-only string (the technical detail, as in GTK)

    /// <summary>
    /// Whether a connection test of a browser sign-in account failed at the
    /// sign-in (signin.TestNeedsSignIn): an endpoint rejected the token
    /// (authFailed) or the daemon has no valid sign-in (authRequired), or the
    /// call itself said so. The results then offer "Sign In Again".
    /// </summary>
    public static bool IsSignInProblem(AccountTestResult? result, Exception? error) => SignIn.TestNeedsSignIn(result, error);
}
