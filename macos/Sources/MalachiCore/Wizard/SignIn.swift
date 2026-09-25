// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/signin/signin.go: which way the wizard takes after
// account.discover. The daemon answers with a primary config and, for a
// Google or Microsoft 365 address, alternatives; the wizard follows the
// primary one and keeps the alternatives it can offer on the way.

import Foundation

/// signin.Discovery: the outcome of `classifyDiscovery`.
public struct Discovery: Sendable, Equatable {
    /// signin.Path*: the page the wizard continues on.
    public enum Path: Sendable, Hashable {
        /// Servers and a password: the connection test when `config` is
        /// set, the Servers page with a guess otherwise.
        case password
        /// Signed in through GNOME Online Accounts: the daemon's account is
        /// complete; test it.
        case goa
        /// An address of a GNOME Online Accounts provider the desktop is not
        /// signed in to yet: the hint page (with the browser as a way out
        /// when `oauthAlt` is set).
        case goaHint
        /// The daemon's own sign-in in the browser.
        case oauth
    }

    public var path: Path
    /// The primary config; nil on a failed or empty discovery.
    public var config: AccountConfig?
    /// The daemon's own sign-in offered from the GNOME Online Accounts hint.
    public var oauthAlt: AccountConfig?
    /// The IMAP/SMTP account with an app password (Google), offered when the
    /// browser sign-in is not configured.
    public var passwordAlt: AccountConfig?
    /// Whose account it is, for the texts (never the untrusted
    /// `providerName`).
    public var provider: LinkedProvider?

    public init(
        path: Path, config: AccountConfig? = nil, oauthAlt: AccountConfig? = nil,
        passwordAlt: AccountConfig? = nil, provider: LinkedProvider? = nil
    ) {
        self.path = path
        self.config = config
        self.oauthAlt = oauthAlt
        self.passwordAlt = passwordAlt
        self.provider = provider
    }
}

/// signin.ClassifyDiscovery: a GNOME Online Accounts config with an
/// account id goes to the test, one without to the hint (with the first
/// browser sign-in and the first app-password account among the
/// alternatives); a daemon config goes to the browser sign-in (with the
/// first app-password account); anything else, a failure included, is the
/// password path.
public func classifyDiscovery(_ outcome: Result<AccountDiscoverResult, any Error>) -> Discovery {
    guard case .success(let res) = outcome, let cfg = res.config else {
        return Discovery(path: .password)
    }
    let provider = accountProvider(cfg)
    switch signInKind(cfg) {
    case .goa:
        if linkedAccountID(cfg) != nil {
            return Discovery(path: .goa, config: cfg, provider: provider)
        }
        return Discovery(
            path: .goaHint, config: cfg, oauthAlt: res.alternatives.first { signInKind($0) == .oauth },
            passwordAlt: firstPasswordAlternative(res.alternatives), provider: provider
        )
    case .oauth:
        return Discovery(path: .oauth, config: cfg, passwordAlt: firstPasswordAlternative(res.alternatives), provider: provider)
    case .password:
        return Discovery(path: .password, config: cfg)
    }
}

/// The first alternative the password path can take: an IMAP account with
/// both endpoints and a password on each.
private func firstPasswordAlternative(_ alternatives: [AccountConfig]) -> AccountConfig? {
    alternatives.first { alt in
        guard signInKind(alt) == .password, alt.protocolKind == .imap,
              let imap = alt.imap, let smtp = alt.smtp else { return false }
        return imap.authMethod == .password && smtp.authMethod == .password
    }
}
