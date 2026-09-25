// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The pure part of ui/internal/accountwizard/linked.go: accounts whose
// sign-in belongs to GNOME Online Accounts. The daemon reports none on
// macOS, but the logic is kept so the wizard mirrors the GTK one.

import Foundation

/// accountwizard.linkedAccountID: the GNOME Online Accounts id an account
/// signs in with, nil when it has none yet (the sign-in hint of
/// account.discover).
public func linkedAccountID(_ cfg: AccountConfig) -> String? {
    if let graph = cfg.graph {
        return nonEmpty(graph.goaAccountId)
    }
    if let oauth2 = cfg.oauth2, oauth2.source == .goa {
        return nonEmpty(oauth2.goaAccountId)
    }
    return nil
}

/// accountwizard.withIdentity: the daemon-built account of a linked
/// sign-in with what the identity page adds: the display name, and an
/// account name derived from the address when the daemon left it at the
/// address itself.
public func withIdentity(_ config: AccountConfig, _ id: Identity) -> AccountConfig {
    var cfg = config
    let displayName = id.displayName.trimmingCharacters(in: .whitespacesAndNewlines)
    cfg.displayName = displayName.isEmpty ? nil : displayName
    let name = cfg.name.trimmingCharacters(in: .whitespacesAndNewlines)
    if name.isEmpty || name.caseInsensitiveCompare(cfg.email) == .orderedSame {
        cfg.name = suggestAccountName(cfg.email)
    }
    return cfg
}

/// accountwizard.LinkedMatch: the linked account with the address
/// (case-insensitively), if any.
public func linkedMatch(_ linked: [LinkedAccount], email: String) -> LinkedAccount? {
    let want = email.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    return linked.first { $0.email.lowercased() == want }
}

/// accountwizard.showGOAHint's description: for an address of the named
/// provider that the desktop is not signed in to yet; Microsoft 365 when
/// the provider has no name.
public func goaHintText(providerName: String?) -> String {
    var name = providerName ?? ""
    if name.isEmpty {
        // widget.ProviderName(widget.ProviderMicrosoft365): a brand name,
        // not translated.
        name = "Microsoft 365"
    }
    // TRANSLATORS: %s is a provider such as "Microsoft 365" or "Google".
    return L10n.T("This address belongs to a %s account. Add it under Settings → Online Accounts, then come back here.", name)
}

/// Go's "" sentinel as an Optional.
private func nonEmpty(_ s: String?) -> String? {
    guard let s, !s.isEmpty else { return nil }
    return s
}
