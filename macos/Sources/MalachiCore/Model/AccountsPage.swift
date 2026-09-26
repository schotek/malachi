// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The pure parts of the Accounts page of the preferences
// (ui/internal/window/accounts_page.go, accounts_reorder.go).

/// The account name, or the address when unnamed (accounts_page.go
/// `accountRowTitle`). Unlike `accountLabel` nothing is trimmed: this is
/// the name as the user typed it.
public func accountRowTitle(_ a: Account) -> String {
    if !a.config.name.isEmpty {
        return a.config.name
    }
    return a.config.email
}

/// The short status shown next to the switch; empty for the unremarkable
/// idle state (accounts_page.go `accountStatusText`). A refused server
/// certificate (`CertTrust.fromSyncState`) says so instead of "Offline";
/// an offline or failed account with an error says why
/// (`endpointErrorText`).
public func accountStatusText(_ state: SyncState) -> String {
    if let p = CertTrust.fromSyncState(state) {
        return certStatusText(p.category)
    }
    switch state.status {
    case .disabled: return L10n.T("Paused")
    case .syncing: return L10n.T("Syncing…")
    case .offline:
        if let e = state.error {
            // TRANSLATORS: account status in Settings → Accounts, %s says why
            return L10n.T("Offline: %s", endpointErrorText(e))
        }
        return L10n.T("Offline")
    case .authRequired: return L10n.T("Sign-in required")
    case .error:
        if let e = state.error {
            // TRANSLATORS: account status in Settings → Accounts, %s says why
            return L10n.T("Error: %s", endpointErrorText(e))
        }
        return L10n.T("Error")
    default: return ""
    }
}

/// The row offers "Sign In…" (accounts_page.go `accountRow`): an account
/// of the browser sign-in that needs one.
public func accountRowOffersSignIn(_ a: Account) -> Bool {
    a.state.status == .authRequired && signInKind(a.config) == .oauth
}

/// The slot a row dragged from `from` takes when it is dropped on the row
/// at `target` (accounts_reorder.go `insertIndex`): before that row when the
/// pointer is in its upper half, after it otherwise. The result is an index
/// in the list with the dragged row already taken out, so `from` means "no
/// change".
public func insertIndex(from: Int, target: Int, above: Bool) -> Int {
    var to = target
    if !above {
        to += 1
    }
    if from < to {
        to -= 1
    }
    return to
}

/// `accounts` with the entry at `from` moved to `to`; out-of-range or equal
/// positions return the list unchanged (accounts_reorder.go `moveAccount`).
public func moveAccount(_ accounts: [Account], from: Int, to: Int) -> [Account] {
    guard from != to, from >= 0, to >= 0, from < accounts.count, to < accounts.count else {
        return accounts
    }
    var out = accounts
    let moved = out.remove(at: from)
    out.insert(moved, at: to)
    return out
}
