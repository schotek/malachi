// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The status line, its popover and the sign-in banner (ui/internal/
// window/sync.go and status.go), the pure parts. The daemon owns the sync
// state; these only mirror the last sync.status / notify.syncState per
// account into text.

/// The status line for the given states (sync.go `syncStatusText`). Only
/// enabled accounts count; an account missing from `states` uses the state
/// embedded in its account.list entry. The most pressing state wins:
/// syncing (with the folder or account name and the progress when known),
/// then sign-in required, a changed certificate, a certificate problem
/// (`CertTrust.fromSyncState`: an offline or failed account whose server's
/// certificate was refused, instead of "Offline"), sending (the pending
/// outbox messages of every account added up; sending is not a sync
/// status, so it shows while the status is idle), messages that were not
/// sent (failed, added up the same way), error, offline, and finally "Up to
/// date" with the time of the newest last check. Sign-in required, error
/// and offline name the account when exactly one is in that state and more
/// than one is enabled; with a single account the name would say nothing,
/// with several it could not be one (the popover lists them). The
/// certificate states leave the name to the certificate banner. When every
/// account is paused the line says so; with no account at all it is empty.
/// `folderName` returns the display name of a folder or "" when unknown;
/// `now` is the moment the time of the last check is shown against.
public func syncStatusText(
    _ states: [AccountID: SyncState], _ accounts: [Account], folderName: ((AccountID, FolderID) -> String)?,
    now: Date
) -> (text: String, spinning: Bool) {
    var syncing: SyncState?
    var syncingName = ""
    var authRequired: [Account] = []
    var certChanged = false
    var certProblem = false
    var syncError: [Account] = []
    var offline: [Account] = []
    var enabled = 0
    var pending = 0
    var failed = 0
    var lastSync: Date?
    for a in accounts where a.enabled {
        enabled += 1
        let s = states[a.id] ?? a.state
        pending += s.pendingOutbox
        failed += s.failedOutbox
        if let t = s.lastSync, !t.isGoZero, lastSync.map({ t > $0 }) ?? true {
            lastSync = t
        }
        if let p = CertTrust.fromSyncState(s) {
            if p.category == .changed {
                certChanged = true
            } else {
                certProblem = true
            }
        }
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
            authRequired.append(a)
        case .error:
            syncError.append(a)
        case .offline:
            offline.append(a)
        default:
            break
        }
    }
    // The one account in a state, when naming it helps.
    let named: ([Account]) -> String? = { list in
        guard list.count == 1, enabled >= 2 else { return nil }
        return accountRowTitle(list[0])
    }
    if enabled == 0 {
        return accounts.isEmpty ? ("", false) : (L10n.T("Paused"), false)
    }
    if let syncing {
        if syncing.progress >= 0 {
            // TRANSLATORS: %s is a folder or account name, %d the progress in percent.
            return (L10n.T("Syncing %s… %d %%", syncingName, syncing.progress), true)
        }
        // TRANSLATORS: %s is a folder or account name.
        return (L10n.T("Syncing %s…", syncingName), true)
    }
    if !authRequired.isEmpty {
        if let name = named(authRequired) {
            // TRANSLATORS: status line; %s is an account name.
            return (L10n.T("Sign-in required: %s", name), false)
        }
        return (L10n.T("Sign-in required"), false)
    }
    if certChanged {
        return (certStatusText(.changed), false)
    }
    if certProblem {
        return (certStatusText(.certificate), false)
    }
    if pending > 0 {
        return (sendingText(pending), true)
    }
    if failed > 0 {
        return (notSentText(failed), false)
    }
    if !syncError.isEmpty {
        if let name = named(syncError) {
            // TRANSLATORS: status line; %s is an account name.
            return (L10n.T("Sync error: %s", name), false)
        }
        return (L10n.T("Sync error"), false)
    }
    if !offline.isEmpty {
        if let name = named(offline) {
            // TRANSLATORS: status line of an account that cannot reach its
            // server and keeps trying; %s is an account name.
            return (L10n.T("Offline: %s", name), false)
        }
        return (L10n.T("Offline, retrying"), false)
    }
    if let lastSync {
        // TRANSLATORS: status line; %s is the time of the last check for
        // new mail, e.g. "15:04", or its date when that was before today.
        return (L10n.T("Up to date · %s", formatDate(lastSync, now: now)), false)
    }
    return (L10n.T("Up to date"), false)
}

/// The status of `n` messages waiting in the outbox (sync.go `sendingText`:
/// the status line, an account's row in its popover).
public func sendingText(_ n: Int) -> String {
    // TRANSLATORS: %d is the number of messages waiting in the outbox.
    L10n.N("Sending %d message…", "Sending %d messages…", n)
}

/// The status of `n` messages in the outbox whose delivery failed (sync.go
/// `notSentText`: the status line, the popover's link to the outbox).
public func notSentText(_ n: Int) -> String {
    // TRANSLATORS: status line; %d is the number of messages in the outbox
    // whose sending failed.
    L10n.N("%d message not sent", "%d messages not sent", n)
}

// MARK: The status popover (status.go)

/// What the button of an account's row in the status popover does
/// (status.go `statusAction`).
public enum StatusAction: Sendable, Equatable {
    /// statusActionNone: nothing to offer (a paused account).
    case noAction
    /// sync.trigger for the account (an icon).
    case check
    /// sync.trigger for the account ("Try Again").
    case retry
    /// Signing in again, labelled by `authBannerButton`.
    case signIn
    /// The account assistant ("Edit Account…").
    case edit
}

/// One account's row in the status popover (status.go `accountStatus`).
public struct AccountStatus: Sendable, Equatable {
    public var account: AccountID
    /// `accountRowTitle`; plain text.
    public var title: String
    /// One whole sentence, never pieced together.
    public var detail: String
    public var action: StatusAction
    /// Where the account signs in: the label and route of `.signIn`.
    public var signIn: SignInKind
    /// `failedOutbox`; above zero the popover links to the outbox.
    public var failed: Int

    public init(
        account: AccountID, title: String, detail: String, action: StatusAction = .noAction,
        signIn: SignInKind = .password, failed: Int = 0
    ) {
        self.account = account
        self.title = title
        self.detail = detail
        self.action = action
        self.signIn = signIn
        self.failed = failed
    }
}

/// The status popover's content (status.go `accountStatuses`): one entry
/// per account, in account.list order, paused accounts included. Whether an
/// account is paused is decided by `enabled`, not by `states`: pausing
/// sends no notify.syncState, so `states` keeps what the account said
/// before. A state that came afterwards (an outbox change of the paused
/// account) says "disabled" and is used for its failed messages; otherwise
/// they come from the account.list entry, as does the state of an enabled
/// account missing from `states`. `folderName` and `now` are as for
/// `syncStatusText`.
public func accountStatuses(
    _ states: [AccountID: SyncState], _ accounts: [Account], folderName: ((AccountID, FolderID) -> String)?,
    now: Date
) -> [AccountStatus] {
    var out: [AccountStatus] = []
    out.reserveCapacity(accounts.count)
    for a in accounts {
        var st = AccountStatus(account: a.id, title: accountRowTitle(a), detail: "", signIn: signInKind(a.config))
        if !a.enabled {
            var s = a.state
            if let cached = states[a.id], cached.status == .disabled {
                s = cached
            }
            st.detail = L10n.T("Paused")
            st.failed = s.failedOutbox
            out.append(st)
            continue
        }
        let s = states[a.id] ?? a.state
        let d = accountDetail(a.id, s, folderName: folderName, now: now)
        st.detail = d.detail
        st.action = d.action
        st.failed = s.failedOutbox
        out.append(st)
    }
    return out
}

/// The sentence and the action for an enabled account in state `s`
/// (status.go `accountDetail`). The most pressing state wins: syncing (the
/// folder and progress when known, never the account's name, which is the
/// row's title), sign-in required, a refused or changed certificate (the
/// reason, and the account settings, where it can be trusted), sending,
/// error and offline (the reason when known, and a retry), and finally idle
/// with the time of the last check. Syncing and sending keep the check
/// button of idle: it must not vanish, taking the keyboard focus with it,
/// whenever a pass starts; only a paused account has no action.
public func accountDetail(
    _ acc: AccountID, _ s: SyncState, folderName: ((AccountID, FolderID) -> String)?, now: Date
) -> (detail: String, action: StatusAction) {
    switch s.status {
    case .syncing:
        var name = ""
        if let id = s.folderId, let folderName {
            name = folderName(acc, id)
        }
        if name.isEmpty {
            return (L10n.T("Syncing…"), .check)
        }
        if s.progress >= 0 {
            // TRANSLATORS: %s is a folder or account name, %d the progress in percent.
            return (L10n.T("Syncing %s… %d %%", name, s.progress), .check)
        }
        // TRANSLATORS: %s is a folder or account name.
        return (L10n.T("Syncing %s…", name), .check)
    case .authRequired:
        return (L10n.T("Sign-in required"), .signIn)
    default:
        break
    }
    if CertTrust.fromSyncState(s) != nil {
        return (endpointErrorText(s.error), .edit)
    }
    if s.pendingOutbox > 0 {
        return (sendingText(s.pendingOutbox), .check)
    }
    switch s.status {
    case .error:
        if let e = s.error {
            return (endpointErrorText(e), .retry)
        }
        return (L10n.T("Sync error"), .retry)
    case .offline:
        if let e = s.error {
            return (endpointErrorText(e), .retry)
        }
        return (L10n.T("Offline, retrying"), .retry)
    default:
        break
    }
    if let t = s.lastSync, !t.isGoZero {
        // TRANSLATORS: state of an account; %s is the time of its last
        // check for new mail, e.g. "15:04", or its date when that was
        // before today.
        return (L10n.T("Last synced %s", formatDate(t, now: now)), .check)
    }
    return (L10n.T("Up to date"), .check)
}

/// The label of the button that repairs an account (status.go
/// `statusButtonLabel`): "" when there is nothing to repair (checking for
/// new mail is an icon of its own). "_Edit Account…" carries a mnemonic.
public func statusButtonLabel(_ st: AccountStatus) -> String {
    switch st.action {
    case .retry:
        return L10n.T("Try Again")
    case .signIn:
        return authBannerButton(st.signIn)
    case .edit:
        return L10n.T("_Edit Account…")
    case .noAction, .check:
        return ""
    }
}

/// Whether the popover's rows, built for the accounts in `order`, still fit
/// `list` (status.go `sameAccounts`).
public func sameAccounts(_ order: [AccountID], _ list: [AccountStatus]) -> Bool {
    order == list.map(\.account)
}

/// The label of the button that repairs a sign-in (sync.go
/// `authBannerButton`), by how the account signs in: the Online Accounts
/// panel, the browser, or the preferences.
public func authBannerButton(_ kind: SignInKind) -> String {
    switch kind {
    case .goa:
        return L10n.T("Open Online Accounts")
    case .oauth:
        // TRANSLATORS: a button that signs in; the plain "Sign In" is a page title
        return L10n.C("button", "Sign In")
    case .password:
        return L10n.T("Open Preferences")
    }
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

/// The short status of an account whose server's certificate was refused
/// (sync.go `certStatusText`; Settings → Accounts, the status line).
public func certStatusText(_ c: CertTrust.Category) -> String {
    if c == .changed {
        // TRANSLATORS: account status (sidebar, Settings → Accounts)
        return L10n.T("Certificate changed")
    }
    // TRANSLATORS: account status (sidebar, Settings → Accounts)
    return L10n.T("Certificate problem")
}

/// The first enabled account, in account order, whose state is a
/// certificate problem (sync.go `certProblemAccount`,
/// `CertTrust.fromSyncState`); an account missing from `states` uses the
/// state of its account.list entry, as the status line does.
public func certProblemAccount(
    _ states: [AccountID: SyncState], _ accounts: [Account]
) -> (account: Account, problem: CertTrust.Problem)? {
    for a in accounts where a.enabled {
        if let p = CertTrust.fromSyncState(states[a.id] ?? a.state) {
            return (a, p)
        }
    }
    return nil
}

/// The certificate banner's sentence (sync.go `certBannerText`): changed
/// when the account pins another certificate, not trusted otherwise;
/// `account` is the account's display name.
public func certBannerText(_ c: CertTrust.Category, _ account: String) -> String {
    if c == .changed {
        // TRANSLATORS: banner; %s is an account name
        return L10n.T("The certificate of %s has changed", account)
    }
    // TRANSLATORS: banner; %s is an account name
    return L10n.T("The certificate of %s is not trusted", account)
}
