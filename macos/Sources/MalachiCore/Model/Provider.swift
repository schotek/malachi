// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// Providers whose sign-in belongs to GNOME Online Accounts
// (ui/internal/widget/provider.go), as account.linked names them
// (`LinkedProvider`). An account of theirs has no password to ask for and
// no servers of the user's to edit. Such accounts cannot be added on macOS,
// but a profile may carry one, so the classification is kept.

/// Which Online Accounts provider an account signs in through (provider.go
/// `AccountProvider`): Microsoft 365 for a Graph account, the oauth2
/// provider for an IMAP account with a GOA token, nil for a password
/// account or the daemon's own (reserved) OAuth2 flow.
public func accountProvider(_ cfg: AccountConfig) -> LinkedProvider? {
    if cfg.protocolKind == .graph {
        return .microsoft365
    }
    if let oauth2 = cfg.oauth2, oauth2.source == .goa {
        return LinkedProvider(rawValue: oauth2.provider.rawValue)
    }
    return nil
}

/// An account whose sign-in lives in GNOME Online Accounts (provider.go
/// `GOAOwned`).
public func goaOwned(_ cfg: AccountConfig) -> Bool {
    accountProvider(cfg) != nil
}

/// The provider's name as shown to the user (provider.go `ProviderName`).
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
