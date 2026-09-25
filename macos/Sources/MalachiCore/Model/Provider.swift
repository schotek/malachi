// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// Providers whose accounts sign in with OAuth2 (ui/internal/signin and
// ui/internal/widget/provider.go), as account.linked names them
// (`LinkedProvider`): through GNOME Online Accounts, or through the
// daemon's own sign-in in the browser. An account of theirs has no
// password to ask for and no servers of the user's to edit. GNOME Online
// Accounts does not exist on macOS, but a profile may carry such an
// account, so the classification is kept.

/// Where an account's sign-in lives, and so where it is repaired
/// (signin.Kind, ui/internal/signin).
public enum SignInKind: Sendable, Hashable {
    /// A password (or app password) the user types; the servers are the
    /// user's to edit.
    case password
    /// GNOME Online Accounts holds the sign-in; it is fixed there.
    case goa
    /// The daemon's own sign-in in the browser (source daemon); it is fixed
    /// by signing in again.
    case oauth
}

/// signin.KindOf: a Graph account signs in through GNOME Online Accounts
/// when `graph.source` is goa and through the daemon's own sign-in
/// otherwise; an account with an `oauth2` block likewise by its source;
/// anything else with a password.
public func signInKind(_ cfg: AccountConfig) -> SignInKind {
    if cfg.protocolKind == .graph {
        return cfg.graph?.source == .goa ? .goa : .oauth
    }
    if let oauth2 = cfg.oauth2 {
        return oauth2.source == .goa ? .goa : .oauth
    }
    return .password
}

/// Which provider an account signs in with (signin.Provider, formerly
/// provider.go `AccountProvider`): Microsoft 365 for a Graph account, the
/// `oauth2` provider otherwise (`office365` is Microsoft 365), nil for a
/// password account or a provider this client does not know.
public func accountProvider(_ cfg: AccountConfig) -> LinkedProvider? {
    if cfg.protocolKind == .graph {
        return .microsoft365
    }
    switch cfg.oauth2?.provider {
    case .google?: return .google
    case .office365?: return .microsoft365
    default: return nil
    }
}

/// The provider's name as shown to the user (signin.ProviderName).
/// These are brand names and are not translated.
public func providerName(_ provider: LinkedProvider?) -> String {
    switch provider {
    case .microsoft365: return "Microsoft 365"
    case .google: return "Google"
    default: return ""
    }
}

/// The icon GNOME Online Accounts installs for the provider, nil for an
/// unknown one (provider.go `providerIconName`). The AppKit layer maps the
/// names to its own symbols.
public func providerIconName(_ provider: LinkedProvider?) -> String? {
    switch provider {
    case .microsoft365: return "goa-account-ms365-symbolic"
    case .google: return "goa-account-google-symbolic"
    default: return nil
    }
}

/// The provider's icon name, the generic mail icon otherwise (also for a
/// password account; provider.go `ProviderIcon`). The GTK version also
/// falls back when the theme lacks the icon; on macOS that decision is the
/// symbol mapping's.
public func providerIcon(_ provider: LinkedProvider?) -> String {
    providerIconName(provider) ?? "mail-unread-symbolic"
}
