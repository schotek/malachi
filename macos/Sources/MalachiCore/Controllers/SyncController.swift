// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The sync status line, the sign-in banner and the certificate banner of
/// the main window (ui/internal/window/sync.go and status.go), minus the
/// widgets: the daemon owns the sync state, this only mirrors the last
/// sync.status / notify.syncState per account into a status line, the rows
/// of its popover (`accountStatuses`) and banner texts.
///
/// The line comes in two halves, as in GTK's `refreshSyncLabel`: `footer`
/// is the sync state of every account (`syncStatusText`), and `line` is what
/// the status bar shows, the footer with the connection to the daemon put
/// over it (`statusLineFor`).
///
/// `apply` records one state and refreshes the line; who called it decides
/// what else follows (`MailboxController.handleSyncState` reloads folders and
/// the list, as the GTK `applySyncState` does). The 30 s fallback of
/// `triggerSync` lives here (`beginChecking`): the line says "Checking for
/// new mail…" at once and the daemon's notify.syncState takes over, with the
/// timer as the guarantee that the spinner never sticks. A second timer
/// (`startRefreshing`) redraws the line every minute, so the time of the
/// last check it names becomes a date once the day is over.
@MainActor
public final class SyncController {
    /// The sync half of the status line (`syncStatusText`, or "Checking for
    /// new mail…").
    public struct FooterState: Equatable, Sendable {
        public var text: String
        public var spinning: Bool

        public init(text: String, spinning: Bool) {
            self.text = text
            self.spinning = spinning
        }
    }

    /// What the sign-in banner's button does (window.go
    /// `onAuthBannerButton`), by how the banner's account signs in.
    public enum AuthBannerAction: Sendable, Equatable {
        /// A password account: the preferences, where it can be edited.
        case openPreferences
        /// A password account whose password is missing or was refused:
        /// its edit wizard, asking for the password (`reason` is the
        /// notification's, for the wizard's banner).
        case editAccount(AccountID, reason: ErrorCode)
        /// GNOME Online Accounts holds the sign-in: its panel (there is none
        /// on macOS; the shell opens the preferences).
        case openOnlineAccounts
        /// The browser sign-in: a fresh session for the account
        /// (`requestSignInURL`), the notification's page when that fails.
        case signInAgain(AccountID, fallbackURL: String?)
    }

    /// The outcome of `requestSignInURL`.
    public enum SignInURL: Sendable, Equatable {
        /// Open this page in the browser.
        case open(String)
        /// Nothing to open; the toast.
        case failed(String)
    }

    /// How long the spinner started by `beginChecking` stays on when no
    /// notify.syncState follows (sync.go `syncFallbackSeconds`).
    public static let fallbackDelay: Duration = .seconds(30)

    /// The fallback in use; tests shorten it.
    public var fallbackDelay: Duration = SyncController.fallbackDelay

    /// How often `startRefreshing` redraws the line without a state change
    /// (sync.go `statusRefreshSeconds`).
    public static let refreshInterval: Duration = .seconds(60)

    /// The moment the time of the last check is shown against; tests pin it.
    public var now: @MainActor () -> Date = { Date() }

    /// The last state per account (sync.go `syncStates`).
    public private(set) var states: [AccountID: SyncState] = [:]
    /// The sync half of the line last emitted.
    public private(set) var footer = FooterState(text: "", spinning: false)
    /// What the status bar shows, last emitted: `footer` under the
    /// connection (`statusLineFor`). Until the connection reports anything
    /// the first attempt is underway: "Connecting to backend…".
    public private(set) var line = statusLineFor(ConnView(state: .connecting), text: "", spinning: false)
    /// The connection as the line knows it (status.go `connView`).
    public private(set) var connection = ConnView(state: .connecting)
    /// The account the sign-in banner is up for, nil while hidden.
    public private(set) var authBannerAccount: AccountID?
    /// What the banner's button does; nil while hidden.
    public private(set) var authBannerAction: AuthBannerAction?
    /// The account the certificate banner is up for, nil while hidden.
    public private(set) var certBannerAccount: AccountID?
    /// The certificate banner's title last emitted.
    private var certBannerTitle: String?

    /// Called after every change of the footer line.
    public var onFooter: (@MainActor (FooterState) -> Void)?
    /// Called after every change of the status line, and whenever the rows
    /// of the popover may have changed with it (sync.go `refreshSyncLabel`
    /// refreshes an open popover from the same place).
    public var onStatusLine: (@MainActor (StatusLine) -> Void)?
    /// Called to show the sign-in banner (account, title, button label) or
    /// to hide it (all nil).
    public var onAuthBanner: (@MainActor (AccountID?, String?, String?) -> Void)?
    /// Called to show the certificate banner (account, title, button label
    /// without its mnemonic) or to hide it (all nil). Its button edits the
    /// account (the wizard in edit mode).
    public var onCertBanner: (@MainActor (AccountID?, String?, String?) -> Void)?

    /// The accounts the line counts and the popover lists (account.list
    /// order); the mailbox controller supplies its model's.
    public var accounts: @MainActor () -> [Account] = { [] }
    /// The display name of a folder, "" when unknown (sync.go
    /// `refreshSyncLabel`'s lookup).
    public var folderName: @MainActor (AccountID, FolderID) -> String = { _, _ in "" }

    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "sync")
    private var fallback: Task<Void, Never>?
    private var refresher: Task<Void, Never>?
    private var closed = false

    public init() {}

    /// Stops the timers; nothing is emitted afterwards.
    public func close() {
        closed = true
        fallback?.cancel()
        fallback = nil
        refresher?.cancel()
        refresher = nil
    }

    /// Redraws the line every `interval` without a state change (window.go
    /// `New`, the `statusRefreshSeconds` timeout): it names the time of the
    /// last check ("Up to date · 15:04"), which a day later has to be a
    /// date. Calling it again restarts the timer.
    public func startRefreshing(every interval: Duration = SyncController.refreshInterval) {
        refresher?.cancel()
        refresher = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: interval)
                guard let self, !Task.isCancelled, !self.closed else { return }
                self.refreshFooter()
            }
        }
    }

    // MARK: States

    /// Records one account's state and refreshes the line (the first half
    /// of sync.go `applySyncState`): a failed sync.status no longer holds
    /// the line, and the banner hides when its account left the sign-in
    /// state. Returns the state it replaced (nil for the first) and the new
    /// one, for the caller's `onSyncFinished` / `onOutboxChanged`.
    @discardableResult
    public func apply(_ s: SyncState) -> (prev: SyncState?, cur: SyncState) {
        let prev = states[s.accountId]
        states[s.accountId] = s
        connection.syncFailed = false
        refreshFooter()
        if s.accountId == authBannerAccount, s.status != .authRequired {
            hideAuthBanner()
        }
        return (prev, s)
    }

    /// The cached state of an account, if any arrived.
    public func state(of acc: AccountID) -> SyncState? {
        states[acc]
    }

    /// Runs sync.status and hands every state to `apply` (sync.go
    /// `loadSyncStatus`); `each` replaces that step for a caller that does
    /// more per state (`MailboxController.handleSyncState`). notImplemented
    /// is the expected answer from a daemon without a syncer: the line says
    /// "Not syncing" until a state arrives after all (`statusLineFor`), and
    /// nothing else happens.
    public func loadSyncStatus(client: RPCClient, each: (@MainActor (SyncState) -> Void)? = nil) {
        Task { [weak self] in
            let outcome: Result<SyncStatusResult, any Error>
            do {
                outcome = .success(try await client.call(API.SyncStatus.self, SyncStatusParams()))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed else { return }
            switch outcome {
            case .failure(let err):
                self.log.debug("sync.status: \(String(describing: err), privacy: .public)")
                self.connection.syncFailed = true
                self.refreshFooter()
            case .success(let res):
                for s in res.accounts {
                    if let each {
                        each(s)
                    } else {
                        self.apply(s)
                    }
                }
            }
        }
    }

    // MARK: Status line

    /// The footer line for the given states over `accounts` (sync.go
    /// `syncStatusText`, see `MalachiCore.syncStatusText`), against `now`.
    public func footerState(accounts: [Account], folderName: ((AccountID, FolderID) -> String)?) -> FooterState {
        let (text, spinning) = syncStatusText(states, accounts, folderName: folderName, now: now())
        return FooterState(text: text, spinning: spinning)
    }

    /// Recomputes the line from the cached states and the connection
    /// (sync.go `refreshSyncLabel`) and emits it, and with it the
    /// certificate banner. Enabled accounts without a cached state fall
    /// back to the state account.list reported, so the line is right before
    /// sync.status answered.
    public func refreshFooter() {
        let accounts = accounts()
        let lookup = folderName
        let f = footerState(accounts: accounts, folderName: { lookup($0, $1) })
        setFooter(f)
        setLine(statusLineFor(connection, text: f.text, spinning: f.spinning))
        refreshCertBanner(accounts)
    }

    /// The connection changed (window.go `showConnectionState`): what
    /// sync.status said is forgotten with it, and the line follows.
    public func setConnection(_ state: ConnectionController.ConnectionState) {
        connection = ConnView(state: state)
        refreshFooter()
    }

    /// The popover's rows for the cached states (status.go
    /// `refreshStatusPopover`'s `accountStatuses`), over `accounts` and
    /// against `now`.
    public func accountStatuses() -> [AccountStatus] {
        let lookup = folderName
        return MalachiCore.accountStatuses(states, accounts(), folderName: { lookup($0, $1) }, now: now())
    }

    /// The line of a refresh the user asked for (sync.go `startSync`):
    /// "Checking for new mail…" with the spinner at once, and a fallback
    /// timer that recomputes the line after `fallbackDelay` in case no
    /// notify.syncState follows. Calling it again restarts the timer. As in
    /// GTK only the text and the spinner change: the connection's icon and
    /// the rest of the line stay as they are.
    public func beginChecking() {
        let text = L10n.T("Checking for new mail…")
        setFooter(FooterState(text: text, spinning: true))
        var l = line
        l.text = text
        l.spinning = true
        setLine(l)
        fallback?.cancel()
        let delay = fallbackDelay
        fallback = Task { [weak self] in
            try? await Task.sleep(for: delay)
            guard let self, !Task.isCancelled, !self.closed else { return }
            self.fallback = nil
            self.refreshFooter()
        }
    }

    private func setFooter(_ f: FooterState) {
        guard !closed else { return }
        footer = f
        onFooter?(f)
    }

    private func setLine(_ l: StatusLine) {
        guard !closed else { return }
        line = l
        onStatusLine?(l)
    }

    // MARK: Certificate banner

    /// The certificate banner (sync.go `refreshCertBanner`): up for the
    /// first enabled account, in account order, whose server's certificate
    /// was refused or has changed (`certProblemAccount`), hidden when there
    /// is none. Emits only a change.
    private func refreshCertBanner(_ accounts: [Account]) {
        guard !closed else { return }
        guard let found = certProblemAccount(states, accounts) else {
            guard certBannerAccount != nil else { return }
            certBannerAccount = nil
            certBannerTitle = nil
            onCertBanner?(nil, nil, nil)
            return
        }
        let a = found.account
        let title = certBannerText(found.problem.category, accountRowTitle(a))
        guard certBannerAccount != a.id || certBannerTitle != title else { return }
        certBannerAccount = a.id
        certBannerTitle = title
        // TRANSLATORS: banner button
        onCertBanner?(a.id, title, withoutMnemonic(L10n.T("_Edit Account…")))
    }

    // MARK: Sign-in banner

    /// The banner for a notify.authRequired (sync.go `showAuthRequired`):
    /// the sentence and the button label. `account` is the notified account
    /// when known; without it the id stands in for the name. For an account
    /// whose sign-in lives in GNOME Online Accounts the button opens that
    /// panel instead of the preferences (never the case on macOS, kept for
    /// the text table's parity); an account of the browser sign-in is
    /// signed in again from the button; a password account whose password
    /// is missing or was refused says so and edits the account.
    public func authBanner(for n: AuthRequiredNotification, account: Account?) -> (title: String, button: String) {
        let name = account.map(accountRowTitle) ?? n.accountId.rawValue
        let kind = authBannerKind(n, account)
        // The banner's button shows no mnemonic.
        let button = withoutMnemonic(authBannerButton(kind, n.reason))
        switch kind {
        case .goa:
            return (goaAuthBannerText(n.reason, name), button)
        case .oauth:
            return (oauthAuthBannerText(n.reason, name), button)
        case .password:
            return (authBannerText(n.reason, name), button)
        }
    }

    /// The button's action for the notified account (sync.go
    /// `showAuthRequired`'s kind).
    public func authBannerAction(for n: AuthRequiredNotification, account: Account?) -> AuthBannerAction {
        switch authBannerKind(n, account) {
        case .goa:
            return .openOnlineAccounts
        case .oauth:
            let url = n.authUrl ?? ""
            return .signInAgain(n.accountId, fallbackURL: url.isEmpty ? nil : url)
        case .password:
            if editsPassword(.password, n.reason) {
                return .editAccount(n.accountId, reason: n.reason)
            }
            return .openPreferences
        }
    }

    /// How the notified account signs in: by its config, or, for an account
    /// not listed yet, the daemon's own sign-in when the notification
    /// carries a URL (only that sign-in has one).
    private func authBannerKind(_ n: AuthRequiredNotification, _ account: Account?) -> SignInKind {
        if let account {
            return signInKind(account.config)
        }
        return (n.authUrl ?? "").isEmpty ? .password : .oauth
    }

    /// Reveals the banner for the notified account. The authUrl of an
    /// account of the browser sign-in is kept as the fallback of its
    /// button; it is never logged.
    public func showAuthRequired(_ n: AuthRequiredNotification, account: Account?) {
        let action = authBannerAction(for: n, account: account)
        log.debug("auth required: account \(n.accountId.rawValue, privacy: .public) reason \(n.reason.name, privacy: .public) authUrl \(n.authUrl != nil)")
        authBannerAccount = n.accountId
        authBannerAction = action
        let (title, button) = authBanner(for: n, account: account)
        onAuthBanner?(n.accountId, title, button)
    }

    /// Hides the banner and forgets its account (sync.go `hideAuthBanner`).
    public func hideAuthBanner() {
        let wasShown = authBannerAccount != nil
        authBannerAccount = nil
        authBannerAction = nil
        if wasShown {
            onAuthBanner?(nil, nil, nil)
        }
    }

    /// The banner's Sign In for an account of the browser sign-in (sync.go
    /// `signInInBrowser`): a fresh page from account.oauthStart with the
    /// account's id (the daemon hands back the session it is already
    /// waiting on), or the notification's authUrl when the daemon cannot
    /// answer; only an https address is opened. The daemon completes the
    /// sign-in by itself and the banner goes with the next notify.syncState.
    public func requestSignInURL(client: RPCClient, accountId: AccountID, fallbackURL: String?) async -> SignInURL {
        var url = ""
        var failure: (any Error)?
        do {
            url = try await client.call(
                API.AccountOAuthStart.self, AccountOAuthStartParams(accountId: accountId, browserPage: browserPage()),
                timeout: RPCTimeouts.oauthStart
            ).authUrl
        } catch {
            log.warning("account.oauthStart for \(accountId.rawValue, privacy: .public): \(String(describing: error), privacy: .public)")
            failure = error
            url = fallbackURL ?? ""
        }
        if url.isEmpty {
            return .failed(rpcErrorText(L10n.T("Starting the sign-in"), failure))
        }
        guard isBrowserURL(url) else {
            log.warning("sign-in address refused: not https")
            return .failed(refusedBrowserURLText())
        }
        return .open(url)
    }
}

/// The connection's sentence for a connection state (window.go
/// `showConnectionState` and `fetchSystemInfo`, the texts of status.go
/// `statusLineFor`): the GTK icon name, which the AppKit layer maps to a
/// symbol, and the text. Without a connection the status line says it;
/// once connected the sentence is the foot of the status popover. Stopping
/// shows as unavailable: the window is on its way out.
public func connectionStatusLine(_ state: ConnectionController.ConnectionState) -> (icon: String, text: String) {
    switch state {
    case .connecting:
        return ("network-idle-symbolic", L10n.T("Connecting to backend…"))
    case .connected(let info):
        return ("network-transmit-receive-symbolic", L10n.T("Connected to malachid %s (pid %d)", info.version, info.pid))
    case .protocolMismatch(let daemon):
        return ("network-transmit-receive-symbolic", L10n.T("Protocol mismatch: UI %d, backend %d", API.protocolVersion, daemon))
    case .infoFailed:
        return ("network-transmit-receive-symbolic", L10n.T("Connected, but system.info failed"))
    case .unavailable, .stopping:
        return ("network-offline-symbolic", L10n.T("Backend unavailable"))
    }
}

/// What the status line knows of the daemon connection (status.go
/// `connView`). The connection controller folds system.info into its state
/// (connected with the answer, its failure, a mismatching protocol), so
/// beside it only the failure of sync.status is kept. GTK has one more
/// moment, connected with system.info still on its way; here the
/// controller reports the connection only once system.info answered.
public struct ConnView: Sendable, Equatable {
    public var state: ConnectionController.ConnectionState
    /// sync.status failed; the next state from the daemon clears it, and a
    /// change of the connection forgets it.
    public var syncFailed: Bool

    public init(state: ConnectionController.ConnectionState, syncFailed: Bool = false) {
        self.state = state
        self.syncFailed = syncFailed
    }
}

/// What the status bar shows (status.go `statusLine`): the line, whether
/// the spinner turns, the connection icon (a GTK name, "" for none),
/// whether the line can be clicked, and the foot of its popover ("" for
/// none).
public struct StatusLine: Sendable, Equatable {
    public var text: String
    public var spinning: Bool
    public var icon: String
    public var active: Bool
    public var daemon: String

    public init(text: String, spinning: Bool = false, icon: String = "", active: Bool = false, daemon: String = "") {
        self.text = text
        self.spinning = spinning
        self.icon = icon
        self.active = active
        self.daemon = daemon
    }
}

/// Puts the connection over the sync state (status.go `statusLineFor`;
/// `text` and `spinning` from `syncStatusText`). Without a connection the
/// line says so, with an icon, and cannot be clicked: there is no account
/// state to show. A daemon of another protocol version, or a failed
/// sync.status, takes the line over as well. An empty line (no account at
/// all) cannot be clicked either. The popover's foot names the daemon
/// (`connectionStatusLine`), or that system.info failed; it is empty while
/// the line says the protocols do not match.
public func statusLineFor(_ c: ConnView, text: String, spinning: Bool) -> StatusLine {
    switch c.state {
    case .connecting, .unavailable, .stopping:
        let conn = connectionStatusLine(c.state)
        return StatusLine(text: conn.text, icon: conn.icon)
    case .protocolMismatch:
        return StatusLine(text: connectionStatusLine(c.state).text, active: true)
    case .connected, .infoFailed:
        var line = StatusLine(text: text, spinning: spinning, daemon: connectionStatusLine(c.state).text)
        if c.syncFailed {
            line.text = L10n.T("Not syncing")
            line.spinning = false
        }
        // An empty line (no account at all) is no button to tab to.
        line.active = !line.text.isEmpty
        return line
    }
}
