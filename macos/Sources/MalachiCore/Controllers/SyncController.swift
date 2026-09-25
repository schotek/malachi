// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The sync status line and the sign-in banner of the main window
/// (ui/internal/window/sync.go), minus the widgets: the daemon owns the sync
/// state, this only mirrors the last sync.status / notify.syncState per
/// account into a footer line and a banner text.
///
/// `apply` records one state and refreshes the footer; who called it decides
/// what else follows (`MailboxController.handleSyncState` reloads folders and
/// the list, as the GTK `applySyncState` does). The 30 s fallback of
/// `triggerSync` lives here (`beginChecking`): the footer says "Checking for
/// new mail…" at once and the daemon's notify.syncState takes over, with the
/// timer as the guarantee that the spinner never sticks.
@MainActor
public final class SyncController {
    /// What the sidebar footer's sync line shows.
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

    /// The last state per account (sync.go `syncStates`).
    public private(set) var states: [AccountID: SyncState] = [:]
    /// The footer line last emitted.
    public private(set) var footer = FooterState(text: "", spinning: false)
    /// The account the sign-in banner is up for, nil while hidden.
    public private(set) var authBannerAccount: AccountID?
    /// What the banner's button does; nil while hidden.
    public private(set) var authBannerAction: AuthBannerAction?

    /// Called after every change of the footer line.
    public var onFooter: (@MainActor (FooterState) -> Void)?
    /// Called to show the sign-in banner (account, title, button label) or
    /// to hide it (all nil).
    public var onAuthBanner: (@MainActor (AccountID?, String?, String?) -> Void)?

    /// The accounts the footer counts (account.list order); the mailbox
    /// controller supplies its model's.
    public var accounts: @MainActor () -> [Account] = { [] }
    /// The display name of a folder, "" when unknown (sync.go
    /// `refreshSyncLabel`'s lookup).
    public var folderName: @MainActor (AccountID, FolderID) -> String = { _, _ in "" }

    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "sync")
    private var fallback: Task<Void, Never>?
    private var closed = false

    public init() {}

    /// Stops the fallback timer; nothing is emitted afterwards.
    public func close() {
        closed = true
        fallback?.cancel()
        fallback = nil
    }

    // MARK: States

    /// Records one account's state and refreshes the footer (the first half
    /// of sync.go `applySyncState`): the banner hides when its account left
    /// the sign-in state. Returns the state it replaced (nil for the first)
    /// and the new one, for the caller's `onSyncFinished` / `onOutboxChanged`.
    @discardableResult
    public func apply(_ s: SyncState) -> (prev: SyncState?, cur: SyncState) {
        let prev = states[s.accountId]
        states[s.accountId] = s
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
    /// is the expected answer from a daemon without a syncer: the footer
    /// says "Not syncing" and nothing else happens.
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
                self.setFooter(FooterState(text: L10n.T("Not syncing"), spinning: false))
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

    // MARK: Footer

    /// The footer line for the given states over `accounts` (sync.go
    /// `syncStatusText`, see `MalachiCore.syncStatusText`).
    public func footerState(accounts: [Account], folderName: ((AccountID, FolderID) -> String)?) -> FooterState {
        let (text, spinning) = syncStatusText(states, accounts, folderName: folderName)
        return FooterState(text: text, spinning: spinning)
    }

    /// Recomputes the footer from the cached states (sync.go
    /// `refreshSyncLabel`) and emits it. Enabled accounts without a cached
    /// state fall back to the state account.list reported, so the line is
    /// right before sync.status answered.
    public func refreshFooter() {
        let accounts = accounts()
        let lookup = folderName
        setFooter(footerState(accounts: accounts, folderName: { lookup($0, $1) }))
    }

    /// The footer of a refresh the user asked for (sync.go `triggerSync`):
    /// "Checking for new mail…" with the spinner at once, and a fallback
    /// timer that recomputes the line after `fallbackDelay` in case no
    /// notify.syncState follows. Calling it again restarts the timer.
    public func beginChecking() {
        setFooter(FooterState(text: L10n.T("Checking for new mail…"), spinning: true))
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

    // MARK: Sign-in banner

    /// The banner for a notify.authRequired (sync.go `showAuthRequired`):
    /// the sentence and the button label. `account` is the notified account
    /// when known; without it the id stands in for the name. For an account
    /// whose sign-in lives in GNOME Online Accounts the button opens that
    /// panel instead of the preferences (never the case on macOS, kept for
    /// the text table's parity); an account of the browser sign-in is
    /// signed in again from the button.
    public func authBanner(for n: AuthRequiredNotification, account: Account?) -> (title: String, button: String) {
        let name = account.map(accountRowTitle) ?? n.accountId.rawValue
        switch authBannerKind(n, account) {
        case .goa:
            return (goaAuthBannerText(n.reason, name), L10n.T("Open Online Accounts"))
        case .oauth:
            // TRANSLATORS: a button that signs in; the plain "Sign In" is a page title
            return (oauthAuthBannerText(n.reason, name), L10n.C("button", "Sign In"))
        case .password:
            return (authBannerText(n.reason, name), L10n.T("Open Preferences"))
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

/// The sidebar's connection line for a connection state (window.go
/// `showConnectionState` and `fetchSystemInfo`): the GTK icon name, which
/// the AppKit layer maps to a symbol, and the text. The controller reports a
/// connection only once system.info answered, so the plain "Connected" of
/// the GTK UI has no moment of its own here; the detailed line takes its
/// place at once. Stopping shows as unavailable: the window is on its way
/// out.
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
