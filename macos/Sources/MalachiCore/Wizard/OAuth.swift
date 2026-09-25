// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The texts and rules of the browser sign-in (ui/internal/accountwizard/
// oauth.go and ui/internal/signin): the prompt, the page without a
// configured client, the errors of account.oauthStart / account.oauthWait,
// the page the browser shows and the addresses that may be opened. The
// provider is named by the constants of `providerName`, never by the
// untrusted `providerName` of account.discover.

import Foundation

/// The name the sign-in texts use for a provider (oauth.go
/// `providerLabel`): its brand name, or for a provider this client does
/// not know the address's domain.
public func oauthProviderLabel(_ provider: LinkedProvider?, email: String) -> String {
    let name = providerName(provider)
    if !name.isEmpty {
        return name
    }
    return suggestAccountName(email)
}

/// The prompt page's description (oauth.go `showOAuthPrompt`); `name` is
/// `oauthProviderLabel`'s.
public func oauthPromptText(_ name: String) -> String {
    // TRANSLATORS: %s is a provider such as "Google" or "Microsoft 365".
    L10n.T("Your browser will open so you can sign in to %s. Malachi Mail never sees your password; it only receives permission to read and send your mail.", name)
}

/// The prompt page's button, with its mnemonic marker (oauth.go
/// `showOAuthPrompt`).
public func oauthSignInLabel(_ name: String) -> String {
    // TRANSLATORS: %s is a provider such as "Google" or "Microsoft 365".
    L10n.T("_Sign In with %s", name)
}

/// The description of the page shown when the daemon has no OAuth client
/// for the provider (oauth.go `showOAuthUnavailable`).
public func oauthUnavailableText(_ name: String) -> String {
    // TRANSLATORS: %s is a provider such as "Google" or "Microsoft 365".
    L10n.T("No OAuth client is configured for %s on this computer. Add a client ID to the mail backend's configuration and try again.", name)
}

/// The toast for a sign-in that ended without an account (oauth.go
/// `oauthErrorText` over signin.ClassifyFailure): the user or the provider
/// said no, the session or the call ran out of time, the browser signed in
/// to another mailbox, or the call failed.
public func oauthErrorText(_ name: String, _ error: any Error) -> String {
    if let e = error as? RPCError {
        switch e.code {
        case .cancelled:
            return L10n.T("The sign-in was cancelled")
        case .authFailed:
            // TRANSLATORS: %s is a provider such as "Google" or "Microsoft 365".
            return L10n.T("The sign-in with %s was refused", name)
        case .serverTimeout:
            return L10n.T("The sign-in took too long; try again")
        case .invalidArgument:
            if let who = signedInAs(e) {
                // TRANSLATORS: %s is an e-mail address.
                return L10n.T("The browser signed in to %s, not to this address", who)
            }
        default:
            break
        }
    }
    // The client's own deadline (the GTK client's context.DeadlineExceeded).
    if let e = error as? RPCClient.ClientError, case .timeout = e {
        return L10n.T("The sign-in took too long; try again")
    }
    if error is CancellationError {
        return L10n.T("The sign-in took too long; try again")
    }
    return rpcErrorText(L10n.T("Signing in"), error)
}

/// signin.signedInAsOf: the mailbox the browser signed in to, from an
/// invalidArgument's `data.signedInAs`; nil unless it is a plausible
/// address (trimmed, at most 254 bytes, no control characters and no
/// invisible format characters such as U+202E, which reverses what
/// follows).
func signedInAs(_ e: RPCError) -> String? {
    guard let raw = e.data?["signedInAs"]?.stringValue else { return nil }
    // strings.TrimSpace: only White_Space goes. Foundation's
    // `.whitespacesAndNewlines` would also trim a zero-width space, which
    // the GTK client rejects.
    let scalars = raw.unicodeScalars
    guard let first = scalars.firstIndex(where: { !$0.properties.isWhitespace }),
          let last = scalars.lastIndex(where: { !$0.properties.isWhitespace }) else { return nil }
    let s = String(scalars[first...last])
    guard s.utf8.count <= 254, !s.unicodeScalars.contains(where: isHiddenScalar) else { return nil }
    return s
}

/// signin.isHiddenRune: a control or format character (categories Cc, Cf).
private func isHiddenScalar(_ c: Unicode.Scalar) -> Bool {
    switch c.properties.generalCategory {
    case .control, .format: return true
    default: return false
    }
}

/// The page the browser shows once the provider sent it back to the daemon
/// (oauth.go `BrowserPage`), in the user's language; every
/// account.oauthStart passes it.
public func browserPage() -> OAuthBrowserPage {
    OAuthBrowserPage(
        // TRANSLATORS: title of the page the browser shows
        successTitle: L10n.T("Signed in"),
        // TRANSLATORS: page the browser shows
        successText: L10n.T("You can close this tab and return to Malachi Mail."),
        // TRANSLATORS: title of the page the browser shows
        failureTitle: L10n.T("Sign-in failed"),
        // TRANSLATORS: page the browser shows
        failureText: L10n.T("Return to Malachi Mail and try again.")
    )
}

/// signin.BrowserURL: only an https address with a host and without user
/// information, as the daemon builds them, is opened in the browser.
public func isBrowserURL(_ s: String) -> Bool {
    guard let c = URLComponents(string: s), c.scheme?.lowercased() == "https",
          let host = c.host, !host.isEmpty, c.user == nil, c.password == nil else { return false }
    return true
}

/// The toast for a sign-in address that is refused before the browser
/// sees it (oauth.go `launch`).
public func refusedBrowserURLText() -> String {
    // TRANSLATORS: %s is a technical error message.
    L10n.T("The link could not be opened: %s", "not an https address") // macOS-only string (the technical detail, as in GTK)
}

/// Whether a connection test of a browser sign-in account failed at the
/// sign-in (signin.TestNeedsSignIn, with the Classify outcome): an endpoint
/// rejected the token (authFailed) or the daemon has no valid sign-in
/// (authRequired), or the call itself said so. The results then offer
/// "Sign In Again".
public func isSignInProblem(_ outcome: Result<AccountTestResult, any Error>) -> Bool {
    switch outcome {
    case .failure(let error):
        guard let e = error as? RPCError else { return false }
        return e.code == .authFailed || e.code == .authRequired
    case .success(let res):
        if classify(res) == .authFailed {
            return true
        }
        return [res.imap, res.smtp, res.graph].contains { $0?.error?.code == .authRequired }
    }
}
