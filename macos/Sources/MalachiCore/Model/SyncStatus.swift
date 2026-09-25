// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The sidebar status line and the sign-in banner (ui/internal/window/
// sync.go), the pure parts. The daemon owns the sync state; these only
// mirror the last sync.status / notify.syncState per account into text.

/// The sidebar status line for the given states (sync.go `syncStatusText`).
/// Only enabled accounts count; an account missing from `states` uses the
/// state embedded in its account.list entry. The most pressing state wins:
/// syncing (with the folder or account name and the progress when known),
/// then sign-in required, sending (the pending outbox messages of every
/// account added up; sending is not a sync status, so it shows while the
/// status is idle), error, offline, and finally "Up to date". With no
/// enabled account the line is empty. `folderName` returns the display name
/// of a folder or "" when unknown.
public func syncStatusText(
    _ states: [AccountID: SyncState], _ accounts: [Account], folderName: ((AccountID, FolderID) -> String)?
) -> (text: String, spinning: Bool) {
    var syncing: SyncState?
    var syncingName = ""
    var authRequired = false
    var syncError = false
    var offline = false
    var enabled = 0
    var pending = 0
    for a in accounts where a.enabled {
        enabled += 1
        let s = states[a.id] ?? a.state
        pending += s.pendingOutbox
        switch s.status {
        case .syncing:
            if syncing == nil {
                syncing = s
                if let folderID = s.folderId, let folderName {
                    syncingName = folderName(a.id, folderID)
                }
                if syncingName.isEmpty {
                    syncingName = accountRowTitle(a)
                }
            }
        case .authRequired:
            authRequired = true
        case .error:
            syncError = true
        case .offline:
            offline = true
        default:
            break
        }
    }
    if enabled == 0 {
        return ("", false)
    }
    if let syncing {
        if syncing.progress >= 0 {
            // TRANSLATORS: %s is a folder or account name, %d the progress in percent.
            return (L10n.T("Syncing %s… %d %%", syncingName, syncing.progress), true)
        }
        // TRANSLATORS: %s is a folder or account name.
        return (L10n.T("Syncing %s…", syncingName), true)
    }
    if authRequired {
        return (L10n.T("Sign-in required"), false)
    }
    if pending > 0 {
        // TRANSLATORS: %d is the number of messages waiting in the outbox.
        return (L10n.N("Sending %d message…", "Sending %d messages…", pending), true)
    }
    if syncError {
        return (L10n.T("Sync error"), false)
    }
    if offline {
        return (L10n.T("Offline, retrying"), false)
    }
    return (L10n.T("Up to date"), false)
}

/// The banner sentence for a notify.authRequired reason (sync.go
/// `authBannerText`); `account` is the account's display name.
public func authBannerText(_ reason: ErrorCode, _ account: String) -> String {
    switch reason {
    case .authRequired, .authFailed:
        // TRANSLATORS: %s is an account name.
        return L10n.T("Sign in to %s again", account)
    case .keyringError:
        // TRANSLATORS: %s is an account name.
        return L10n.T("The system keyring is unavailable; %s cannot sign in", account)
    default:
        // TRANSLATORS: %s is an account name.
        return L10n.T("%s needs attention", account)
    }
}

/// `authBannerText` for an account whose sign-in belongs to GNOME Online
/// Accounts (sync.go `goaAuthBannerText`). Such accounts do not exist on
/// macOS; kept for parity of the text table.
public func goaAuthBannerText(_ reason: ErrorCode, _ account: String) -> String {
    if reason == .unavailable {
        // TRANSLATORS: %s is an account name.
        return L10n.T("GNOME Online Accounts is not available; %s cannot sign in", account)
    }
    // TRANSLATORS: %s is an account name.
    return L10n.T("Sign in to %s again in Settings → Online Accounts", account)
}

/// `authBannerText` for an account of the daemon's own sign-in (sync.go
/// `oauthAuthBannerText`): whatever the provider refused, signing in again
/// in the browser is the repair, unless the keyring that keeps the sign-in
/// is what failed.
public func oauthAuthBannerText(_ reason: ErrorCode, _ account: String) -> String {
    if reason == .keyringError {
        return authBannerText(reason, account)
    }
    // TRANSLATORS: %s is an account name.
    return L10n.T("Sign in to %s again in your browser", account)
}
