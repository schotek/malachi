// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The flow of the "Add Account" wizard, the counterpart of
/// ui/internal/accountwizard/wizard.go, linked.go and oauth.go with the
/// widgets replaced by callbacks. It owns the page stack (`pages`, mirroring
/// `AdwNavigationView`), the identity and server fields the UI reports
/// into it, the RPC calls with their `closed`/`op` guards, and every
/// user-facing string of the flow; the AppKit layer only shows what the
/// callbacks deliver and feeds the fields back.
///
/// Every callback runs on the main actor. Wire them, then call `start()`.
@MainActor
public final class WizardController {
    /// The navigation page tags of account_wizard.blp.
    public enum WizardPage: Sendable, Hashable, CaseIterable {
        case identity
        case servers
        /// The sign-in hint for an address of a GNOME Online Accounts provider.
        case goa
        /// The daemon's own sign-in in the browser.
        case oauth
        case testing

        /// The order pages appear in, for the direction of a transition.
        public var rank: Int {
            switch self {
            case .identity: return 0
            case .servers: return 1
            case .goa: return 2
            case .oauth: return 3
            case .testing: return 4
            }
        }
    }

    /// The identity fields the UI can focus.
    public enum IdentityField: Sendable, Hashable {
        case email
        case password
    }

    /// Which buttons the testing page offers (wizard.go `showButtons`).
    public struct WizardButtons: Sendable, Equatable {
        public var edit: Bool
        public var retry: Bool
        public var addAnyway: Bool
        public var add: Bool

        public init(edit: Bool = false, retry: Bool = false, addAnyway: Bool = false, add: Bool = false) {
            self.edit = edit
            self.retry = retry
            self.addAnyway = addAnyway
            self.add = add
        }

        /// Nothing shown, while a call runs (wizard.go `hideButtons`).
        public static let none = WizardButtons()
    }

    /// One endpoint row of the results: a GTK icon name (mapped to a
    /// symbol by the UI), the subtitle and whether the row offers "Trust
    /// Certificate…" (`trustCertificate`).
    public struct EndpointRow: Sendable, Equatable {
        public var icon: String
        public var text: String
        public var trust: Bool

        public init(icon: String, text: String, trust: Bool = false) {
            self.icon = icon
            self.text = text
            self.trust = trust
        }
    }

    /// The results page of the connection test. A nil row is hidden: an
    /// IMAP account shows `imap` and `smtp`, a Graph account `graph`.
    public struct ResultsView: Sendable, Equatable {
        /// A GTK icon name: emblem-ok-symbolic or dialog-warning-symbolic.
        public var icon: String
        public var title: String
        public var description: String?
        public var imap: EndpointRow?
        public var smtp: EndpointRow?
        public var graph: EndpointRow?
        public var buttons: WizardButtons

        public init(
            icon: String, title: String, description: String? = nil, imap: EndpointRow? = nil,
            smtp: EndpointRow? = nil, graph: EndpointRow? = nil, buttons: WizardButtons
        ) {
            self.icon = icon
            self.title = title
            self.description = description
            self.imap = imap
            self.smtp = smtp
            self.graph = graph
            self.buttons = buttons
        }
    }

    /// What the testing page shows (the `testing_stack` of the Blueprint).
    public enum TestingView: Sendable, Equatable {
        case progress(title: String)
        case results(ResultsView)
    }

    /// What the browser sign-in page shows (the `oauth_stack` of the
    /// Blueprint).
    public enum OAuthView: Sendable, Equatable {
        /// "Sign In Through Your Browser" with the provider's button (its
        /// label without the mnemonic marker).
        case prompt(description: String, signInLabel: String)
        /// The browser is open; the page continues by itself.
        case waiting
        /// No OAuth client for the provider; the app password is offered
        /// when the provider has one.
        case unavailable(description: String, passwordAlternative: Bool)
    }

    /// The browser sign-in the page is about (oauth.go `oauthState`).
    public struct OAuthState: Sendable, Equatable {
        /// Whose account it is.
        public var provider: LinkedProvider?
        /// The provider's name for the texts (`oauthProviderLabel`): a
        /// constant, never the daemon's providerName.
        public var name: String
        /// The new account to sign in (source daemon, from account.discover);
        /// nil when editing, where the account's id is signed in again.
        public var config: AccountConfig?
        /// The app-password account offered when no client is configured;
        /// nil when the provider has none (Microsoft 365).
        public var passwordAlt: AccountConfig?
        /// The session account.oauthStart opened; nil before and after.
        public var sessionId: String?
        /// The provider's page for the session, to open again.
        public var authUrl: String?
        /// The session completed: its id goes into the credentials.
        public var complete = false

        public init(
            provider: LinkedProvider?, name: String, config: AccountConfig?, passwordAlt: AccountConfig? = nil,
            sessionId: String? = nil, authUrl: String? = nil, complete: Bool = false
        ) {
            self.provider = provider
            self.name = name
            self.config = config
            self.passwordAlt = passwordAlt
            self.sessionId = sessionId
            self.authUrl = authUrl
            self.complete = complete
        }
    }

    // MARK: Outputs

    /// The page stack changed; the last page is the visible one.
    public var onPages: (@MainActor ([WizardPage]) -> Void)?
    /// An RPC started or finished; the editable pages follow it.
    public var onBusy: (@MainActor (Bool) -> Void)?
    /// Which identity fields to flag and the banner to show (nil hides it).
    public var onIdentityProblems: (@MainActor (IdentityProblems, _ banner: String?) -> Void)?
    /// Which server rows to flag.
    public var onServerProblems: (@MainActor (ServerProblems) -> Void)?
    /// The identity fields were set from here (a linked account); show them.
    public var onIdentity: (@MainActor (Identity) -> Void)?
    /// Fill the Servers page without triggering the port logic.
    public var onApplyConfig: (@MainActor (AccountConfig) -> Void)?
    /// The accounts signed in elsewhere on the desktop (always delivered;
    /// the macOS UI keeps the group hidden).
    public var onLinked: (@MainActor ([LinkedAccount]) -> Void)?
    /// The text of the sign-in hint page, and whether it offers the
    /// browser sign-in instead ("Use the Browser Instead").
    public var onGOAHint: (@MainActor (_ text: String, _ browser: Bool) -> Void)?
    /// The browser sign-in page's content.
    public var onOAuth: (@MainActor (OAuthView) -> Void)?
    /// account.oauthStart runs (true) or answered (false): only the
    /// prompt's Sign In button waits for it (oauth.go sets `oauth_sign_in`
    /// insensitive); the other pages and Back stay usable.
    public var onOAuthStarting: (@MainActor (Bool) -> Void)?
    /// Open the provider's sign-in page in the user's browser.
    public var onOpenURL: (@MainActor (String) -> Void)?
    /// The testing page's content.
    public var onTesting: (@MainActor (TestingView) -> Void)?
    /// Move the keyboard focus to an identity field.
    public var onFocus: (@MainActor (IdentityField) -> Void)?
    /// Show a toast inside the wizard.
    public var onToast: (@MainActor (String) -> Void)?
    /// The account was stored; the UI closes the wizard.
    public var onDone: (@MainActor (AccountID, AccountConfig) -> Void)?
    /// The certificates pinned to the endpoints changed: their fingerprints
    /// as shown (`CertTrust.formatFingerprint`), "" for none. The Servers
    /// page shows a "Pinned Certificate" row per pin.
    public var onPins: (@MainActor (_ imap: String, _ smtp: String) -> Void)?
    /// Asks "Trust This Certificate?"; true when the user confirmed. Without
    /// it nothing is ever pinned.
    public var onConfirmTrust: (@MainActor (TrustPrompt) async -> Bool)?

    // MARK: State

    public let client: RPCClient
    /// The account being changed; nil when adding a new one.
    public let editing: Account?
    /// The wizard only signs `editing` in again (NewEditSignIn): it opens
    /// on the browser sign-in and ends with account.update.
    public let signInMode: Bool

    public private(set) var identity = Identity()
    public private(set) var accountName = ""
    public private(set) var imap = ServerFields(port: 993, security: .tls)
    public private(set) var smtp = ServerFields(port: 587, security: .starttls)
    public private(set) var linked: [LinkedAccount] = []
    /// Set for an account whose sign-in lives in GNOME Online Accounts or
    /// comes from the browser sign-in: the daemon built it, there is no
    /// password and the servers are not the user's to edit. nil is the
    /// password path.
    public private(set) var linkedCfg: AccountConfig?
    /// The browser sign-in, from the moment its prompt is shown.
    public private(set) var oauth: OAuthState?
    /// What the browser sign-in page shows now.
    public private(set) var oauthView: OAuthView?
    public private(set) var lastOutcome: Outcome = .failed
    public private(set) var pages: [WizardPage] = [.identity]
    public private(set) var closed = false
    /// Bumped per RPC so stale replies bail out.
    public private(set) var op = 0
    public private(set) var busy = false
    /// account.oauthStart is under way (`onOAuthStarting`).
    public private(set) var oauthStarting = false

    private var lastResults: ResultsView?
    /// The endpoint configuration each pin was set for (certtrust
    /// `pinned`): the fields carry its pin while their host and port match
    /// (`CertTrust.keepPin`); Forget drops it.
    private var pinned: [Endpoint: ServerConfig] = [:]
    /// The pins last reported through `onPins`.
    private var shownPins: (imap: String, smtp: String) = ("", "")
    /// What the last results offer to trust, per endpoint: the tested
    /// configuration and the problem with its certificate.
    private var trustOffers: [Endpoint: (server: ServerConfig, problem: CertTrust.Problem)] = [:]
    /// The last test of a browser sign-in account found a sign-in problem
    /// (results offer "Sign In Again").
    private var lastSignInProblem = false
    /// The GNOME Online Accounts hint's discovery, for "Use the Browser
    /// Instead".
    private var goaHint: Discovery?
    /// The app-password account chosen instead of the browser sign-in
    /// (oauth.go `appPassword`): Next continues with it while the address
    /// stays the same.
    private var appPassword: AccountConfig?
    private var identityProblemsShown = false
    /// `requestPassword`'s reason, asked by `start()`.
    private var passwordRequest: ErrorCode?
    private var started = false
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "accountwizard")

    /// `editing` prefills the pages, skips discovery, keeps the stored
    /// password on an empty one and saves with account.update; the wizard
    /// then opens on the Servers page with Back leading to the identity
    /// (NewEdit). With `signIn` and an account of the browser sign-in it
    /// opens straight on the sign-in instead (NewEditSignIn).
    public init(client: RPCClient, editing: Account? = nil, signIn: Bool = false) {
        self.client = client
        self.editing = editing
        guard let a = editing else {
            signInMode = false
            return
        }
        let kind = signInKind(a.config)
        signInMode = signIn && kind == .oauth
        identity = Identity(displayName: a.config.displayName ?? "", email: a.config.email, password: "")
        if kind != .password {
            // The address and the sign-in belong to GNOME Online Accounts
            // or to the browser sign-in; only the name can change here, and
            // the test re-checks the sign-in.
            linkedCfg = a.config
            pages = [.identity]
            if signInMode {
                let provider = accountProvider(a.config)
                oauth = OAuthState(provider: provider, name: oauthProviderLabel(provider, email: a.config.email), config: nil)
                oauthView = promptView(oauth?.name ?? "")
                pages = [.oauth]
            }
            return
        }
        setFields(from: a.config)
        pages = [.identity, .servers]
    }

    // MARK: Presentation

    public var isEditing: Bool { editing != nil }

    /// The e-mail row is read-only for a linked account being edited.
    public var emailEditable: Bool { !(isEditing && linkedCfg != nil) }

    /// The password row is hidden for a linked account being edited.
    public var passwordVisible: Bool { !(isEditing && linkedCfg != nil) }

    /// The dialog and identity page title ("Sign In" for NewEditSignIn).
    public var title: String {
        if signInMode {
            return L10n.T("Sign In")
        }
        return isEditing ? L10n.T("Edit Account") : L10n.T("Add Account")
    }

    public var passwordTitle: String {
        isEditing ? L10n.T("New Password (leave empty to keep)") : L10n.T("Password")
    }

    /// The identity page's button.
    public var nextLabel: String {
        withoutMnemonic(isEditing && linkedCfg != nil ? L10n.T("_Test Connection") : L10n.T("_Next"))
    }

    public var addLabel: String {
        withoutMnemonic(isEditing ? L10n.T("_Save") : L10n.T("_Add Account"))
    }

    public var addAnywayLabel: String {
        withoutMnemonic(isEditing ? L10n.T("Save _Anyway") : L10n.T("Add _Anyway"))
    }

    /// The results page's first button: Sign In Again once the browser
    /// sign-in was refused, Edit Servers otherwise.
    public var editLabel: String {
        withoutMnemonic(lastSignInProblem ? L10n.T("_Sign In Again") : L10n.T("_Edit Servers"))
    }

    /// The header's Back is offered: not on the first page, and not while
    /// the browser sign-in waits (`can-pop: false`).
    public var canGoBack: Bool {
        pages.count > 1 && !(pages.last == .oauth && oauthView == .waiting)
    }

    /// The account under test signs in through the browser.
    private var signsInWithBrowser: Bool {
        linkedCfg.map { signInKind($0) == .oauth } ?? false
    }

    /// The progress title while the account is stored.
    public var progressTitle: String {
        isEditing ? L10n.T("Saving Account…") : L10n.T("Adding Account…")
    }

    // MARK: Lifecycle

    /// Delivers the initial state to the callbacks and asks the daemon for
    /// linked accounts. Call once, after wiring the callbacks.
    public func start() {
        onIdentity?(identity)
        if let a = editing, linkedCfg == nil {
            onApplyConfig?(a.config)
        }
        onPins?(shownPins.imap, shownPins.smtp)
        if let oauthView {
            onOAuth?(oauthView)
        }
        onPages?(pages)
        started = true
        showPasswordRequest()
        loadLinked(then: nil)
    }

    /// wizard.go `RequestPassword`: the edit wizard of a password account
    /// whose password is missing (`authRequired`) or was refused
    /// (`authFailed`, and any other reason) opens on the identity page with
    /// the password row flagged and focused and the banner saying why; the
    /// banner goes as the password is typed. Ignored for an account the
    /// daemon signs in (GNOME Online Accounts, the browser sign-in) and when
    /// adding one. Call before `start()`.
    public func requestPassword(reason: ErrorCode) {
        guard editing != nil, linkedCfg == nil, !signInMode else { return }
        pages = [.identity]
        passwordRequest = reason
        if started {
            onPages?(pages)
            showPasswordRequest()
        }
    }

    private func showPasswordRequest() {
        guard let reason = passwordRequest else { return }
        passwordRequest = nil
        askPassword(reason)
    }

    /// wizard.go `askPassword`: flags the identity page's password row as
    /// the thing to fix, says why in its banner (`passwordBannerText`) and
    /// focuses it; typing clears both (`setIdentity`).
    private func askPassword(_ reason: ErrorCode) {
        showIdentityProblems(IdentityProblems(password: true), banner: passwordBannerText(reason))
        onFocus?(.password)
    }

    /// The wizard went away: a sign-in under way is cancelled and every late
    /// reply is dropped from now on (`ConnectClosed`).
    public func close() {
        cancelSession()
        closed = true
    }

    // MARK: Inputs from the UI

    /// The identity rows as typed. A change of the address or password
    /// clears the flagged fields and the banner, as the GTK rows do.
    public func setIdentity(name: String, email: String, password: String) {
        let changed = email != identity.email || password != identity.password
        identity = Identity(displayName: name, email: email, password: password)
        if changed, identityProblemsShown {
            identityProblemsShown = false
            onIdentityProblems?(IdentityProblems(), nil)
        }
    }

    /// The Servers page rows as typed. The rows know nothing of pins: each
    /// endpoint gets the pin trusted for its host and port, if any.
    public func setServers(name: String, imap: ServerFields, smtp: ServerFields) {
        accountName = name
        self.imap = imap
        self.smtp = smtp
        applyPins()
    }

    /// The identity page's Next button (wizard.go `onNext`): validate, then
    /// ask the daemon for server settings. A Google or Microsoft 365 address
    /// goes to the connection test (signed in through GNOME Online
    /// Accounts), to the sign-in hint or to the browser sign-in; an IMAP hit
    /// goes to the connection test; a miss opens the Servers page with
    /// guessed defaults. The password is asked for only once the account
    /// turns out to need one. An address the app password was chosen for
    /// skips discovery.
    public func next() {
        var id = identity
        let p = validateIdentity(id, passwordRequired: false)
        if p.any {
            showIdentityProblems(p, banner: nil)
            onFocus?(.email)
            return
        }
        id.email = validateEmail(id.email) ?? id.email
        if editing != nil {
            if linkedCfg != nil {
                replace([.identity, .testing])
                runTest()
                return
            }
            // The servers are known; only the identity may have changed.
            push(.servers)
            return
        }
        // A new account's way is decided afresh: a sign-in of an earlier
        // attempt is dropped.
        linkedCfg = nil
        cancelSession()
        if let alt = appPassword {
            if alt.email.caseInsensitiveCompare(id.email) == .orderedSame {
                if requirePassword(banner: L10n.T("Enter the app password for this account")) {
                    continueWithAppPassword()
                }
                return
            }
            appPassword = nil
        }
        if let l = linkedMatch(linked, email: id.email), !l.configured {
            useLinked(l)
            return
        }
        setBusy(true)
        op += 1
        let op = self.op
        let email = id.email
        let client = client
        Task { [weak self] in
            let outcome: Result<AccountDiscoverResult, any Error>
            do {
                outcome = .success(try await client.call(API.AccountDiscover.self, AccountDiscoverParams(email: email), timeout: RPCTimeouts.discover))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed, op == self.op else { return }
            self.setBusy(false)
            self.discovered(outcome, identity: id)
        }
    }

    private func discovered(_ outcome: Result<AccountDiscoverResult, any Error>, identity id: Identity) {
        let d = classifyDiscovery(outcome)
        let res = try? outcome.get()
        switch d.path {
        case .goa:
            // Signed in through GNOME Online Accounts: the daemon's account
            // is complete.
            log.info("account discovered: \(res?.source.rawValue ?? "", privacy: .public)")
            if let cfg = d.config {
                startLinked(cfg)
            }
            return
        case .goaHint:
            // The sign-in must happen in GNOME Online Accounts first, or in
            // the browser.
            log.info("account discovered: \(res?.source.rawValue ?? "", privacy: .public)")
            showGOAHint(providerName: res?.providerName, discovery: d)
            return
        case .oauth:
            log.info("account discovered: \(res?.source.rawValue ?? "", privacy: .public), browser sign-in")
            showOAuthPrompt(provider: d.provider, config: d.config, passwordAlt: d.passwordAlt)
            return
        case .password:
            break
        }
        guard requirePassword() else { return }
        switch outcome {
        case .success(let res) where res.config != nil:
            log.info("account discovered: \(res.source.rawValue, privacy: .public)")
            applyConfig(mergeIdentity(res.config ?? guessConfig(id.email), id))
            replace([.identity, .servers, .testing])
            runTest()
        case .success(let res):
            log.debug("account.discover: nothing found (\(res.source.rawValue, privacy: .public))")
            applyConfig(mergeIdentity(guessConfig(id.email), id))
            push(.servers)
        case .failure(let error):
            log.debug("account.discover: \(String(describing: error), privacy: .public)")
            applyConfig(mergeIdentity(guessConfig(id.email), id))
            push(.servers)
        }
    }

    /// The Servers page's Test Connection button (wizard.go `onTest`).
    public func testServers() {
        let p = validateServers(imap: imap, smtp: smtp)
        if p.any {
            onServerProblems?(p)
            return
        }
        if pages.last != .testing {
            push(.testing)
        }
        runTest()
    }

    /// The results page's first button (wizard.go `onEdit`): back to the
    /// Servers page or, once the browser sign-in was refused, to its prompt
    /// for the same account (oauth.go `onSignInAgain`).
    public func edit() {
        if lastSignInProblem {
            showOAuthPrompt(provider: linkedCfg.flatMap(accountProvider), config: oauth?.config, passwordAlt: oauth?.passwordAlt)
            return
        }
        popTo(.servers)
    }

    /// The results page's Retry button.
    public func retry() {
        runTest()
    }

    /// The results page's Add Account / Save button.
    public func add() {
        save()
    }

    /// The results page's Add Anyway / Save Anyway button.
    public func addAnyway() {
        save()
    }

    /// The header's Back button: pops the visible page (not while the
    /// browser sign-in waits).
    public func back() {
        guard canGoBack else { return }
        pages.removeLast()
        onPages?(pages)
    }

    /// Fills the identity from a linked account and goes straight to the
    /// connection test with the account the daemon built for it: there is
    /// nothing to type and nothing to discover (linked.go `useLinked`).
    public func useLinked(_ l: LinkedAccount) {
        guard let cfg = l.config else {
            // A daemon older than the config field; the sign-in is there,
            // but not the account to add it as.
            onToast?(L10n.T("The mail service does not describe this account; update it and try again"))
            return
        }
        identity.email = l.email
        if identity.displayName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            identity.displayName = l.name ?? ""
        }
        onIdentity?(identity)
        startLinked(cfg)
    }

    /// Asks the daemon again and continues when the typed address has
    /// appeared among the linked accounts (linked.go `onGOARecheck`).
    public func recheckLinked() {
        let email = validateEmail(identity.email) ?? ""
        loadLinked { [weak self] linked in
            guard let self else { return }
            if let l = linkedMatch(linked, email: email), !l.configured {
                self.useLinked(l)
                return
            }
            self.onToast?(L10n.T("This address is not signed in yet"))
        }
    }

    // MARK: Navigation (AdwNavigationView over tags)

    private func replace(_ stack: [WizardPage]) {
        pages = stack
        onPages?(pages)
    }

    private func push(_ page: WizardPage) {
        if pages.last == page {
            return
        }
        if let i = pages.firstIndex(of: page) {
            // Already below the top: a navigation view would refuse the push.
            pages.removeSubrange((i + 1)...)
        } else {
            pages.append(page)
        }
        onPages?(pages)
    }

    private func popTo(_ page: WizardPage) {
        guard let i = pages.firstIndex(of: page) else { return }
        pages.removeSubrange((i + 1)...)
        onPages?(pages)
    }

    // MARK: Fields

    private func setBusy(_ b: Bool) {
        busy = b
        onBusy?(b)
    }

    private func setOAuthStarting(_ b: Bool) {
        guard oauthStarting != b else { return }
        oauthStarting = b
        onOAuthStarting?(b)
    }

    private func showIdentityProblems(_ p: IdentityProblems, banner: String?) {
        identityProblemsShown = true
        onIdentityProblems?(p, banner)
    }

    /// Stores the endpoint rows of a configuration (wizard.go
    /// `serverRows.apply`): its pin, if any, becomes the endpoint's pinned
    /// certificate and any earlier one is dropped.
    private func setFields(from cfg: AccountConfig) {
        accountName = cfg.name
        if let s = cfg.imap {
            imap = ServerFields(host: s.host, port: s.port, security: s.security, username: s.username)
            pinned[.imap] = (s.certificateSha256 ?? "").isEmpty ? nil : s
        }
        if let s = cfg.smtp {
            smtp = ServerFields(host: s.host, port: s.port, security: s.security, username: s.username)
            pinned[.smtp] = (s.certificateSha256 ?? "").isEmpty ? nil : s
        }
        applyPins()
    }

    // MARK: Pinned certificates (certtrust)

    /// Gives each endpoint the pin trusted for its host and port
    /// (`CertTrust.keepPin`) and reports a change.
    private func applyPins() {
        imap.certificateSha256 = pinned[.imap].map { CertTrust.keepPin($0, fieldsConfig(imap)) } ?? ""
        smtp.certificateSha256 = pinned[.smtp].map { CertTrust.keepPin($0, fieldsConfig(smtp)) } ?? ""
        let now = (CertTrust.formatFingerprint(imap.certificateSha256), CertTrust.formatFingerprint(smtp.certificateSha256))
        guard now != shownPins else { return }
        shownPins = now
        onPins?(now.0, now.1)
    }

    /// The configuration the rows of one endpoint stand for (without a
    /// pin: `keepPin` decides it).
    private func fieldsConfig(_ f: ServerFields) -> ServerConfig {
        ServerConfig(host: f.host, port: f.port, security: f.security, username: f.username, authMethod: .password)
    }

    /// The fingerprints pinned now, as shown ("" for none).
    public var pinnedFingerprints: (imap: String, smtp: String) { shownPins }

    /// The Servers page's Forget (certtrust: Forget clears `pinned`): the
    /// endpoint verifies the server's certificate again; the next test
    /// shows whether it passes.
    public func forgetPin(_ endpoint: Endpoint) {
        pinned[endpoint] = nil
        applyPins()
    }

    /// A results row's "Trust Certificate…" (trust.go `onTrust`): asks for
    /// the confirmation with the certificate's details and, once
    /// confirmed, pins it to the endpoint it was tested with and tests
    /// again. When the other endpoint presented the same certificate (a
    /// mail bridge serving IMAP and SMTP), one confirmation names and pins
    /// both. Nothing is pinned without `onConfirmTrust` answering true, and
    /// a confirmation that arrives after anything else started is dropped.
    public func trustCertificate(_ endpoint: Endpoint) {
        guard !closed, let offer = trustOffers[endpoint], let cert = offer.problem.cert, let confirm = onConfirmTrust else {
            return
        }
        var targets: [Endpoint] = [endpoint]
        let other: Endpoint = endpoint == .imap ? .smtp : .imap
        if let o = trustOffers[other], CertTrust.sameCertificate(cert, o.problem.cert) {
            targets = [.imap, .smtp]
        }
        let servers = targets.compactMap { trustOffers[$0].map { CertTrust.serverName($0.server) } }
        // A pinned certificate that changed gets its own warning, with the
        // fingerprint trusted before.
        let problems = targets.compactMap { trustOffers[$0]?.problem }
        let changed = problems.contains { $0.category == .changed }
        let previous = changed ? (problems.first { !$0.expected.isEmpty }?.expected ?? "") : ""
        let prompt = trustPrompt(cert, servers: servers, changed: changed, previous: previous)
        let op = self.op
        Task { [weak self] in
            let confirmed = await confirm(prompt)
            guard confirmed, let self, !self.closed, op == self.op else { return }
            for t in targets {
                guard let o = self.trustOffers[t], let c = o.problem.cert else { continue }
                var sc = o.server
                sc.certificateSha256 = c.sha256
                self.pinned[t] = sc
            }
            self.log.info("certificate trusted, endpoints \(targets.count, privacy: .public)")
            self.applyPins()
            self.runTest()
        }
    }

    /// Fills the Servers page without triggering the port logic.
    private func applyConfig(_ cfg: AccountConfig) {
        setFields(from: cfg)
        onApplyConfig?(cfg)
    }

    private func assembleConfig() -> AccountConfig {
        if let linkedCfg {
            return withIdentity(linkedCfg, identity)
        }
        return buildConfig(identity: identity, name: accountName, imap: imap, smtp: smtp)
    }

    /// Flags the empty password row once discovery has shown the account
    /// needs one (a Microsoft 365 account does not).
    private func requirePassword(banner: String = L10n.T("Enter the password for this account")) -> Bool {
        if editing != nil || !identity.password.isEmpty {
            return true
        }
        showIdentityProblems(IdentityProblems(password: true), banner: banner)
        onFocus?(.password)
        return false
    }

    /// The secrets for account.test / add / update (oauth.go
    /// `credentials`): the completed browser session, nothing for any other
    /// account the daemon built (GNOME Online Accounts, or a browser
    /// sign-in the daemon holds already), the typed password otherwise.
    private func credentials() -> Credentials {
        guard let linkedCfg else {
            return credentialsFor(identity)
        }
        if signInKind(linkedCfg) == .oauth, let st = oauth, st.complete, let session = st.sessionId {
            return Credentials(oauthSession: session)
        }
        return Credentials()
    }

    // MARK: Linked accounts

    /// Asks the daemon for accounts signed in elsewhere on the desktop;
    /// then runs `then` with the list (empty on failure: the list is a
    /// convenience).
    private func loadLinked(then: (@MainActor ([LinkedAccount]) -> Void)?) {
        op += 1
        let op = self.op
        let client = client
        Task { [weak self] in
            var accounts: [LinkedAccount] = []
            do {
                accounts = try await client.call(API.AccountLinked.self, EmptyParams(), timeout: RPCTimeouts.default).accounts
            } catch {
                self?.log.debug("account.linked: \(String(describing: error), privacy: .public)")
            }
            guard let self, !self.closed, op == self.op else { return }
            self.linked = accounts
            self.onLinked?(accounts)
            then?(accounts)
        }
    }

    /// Switches to an account whose sign-in belongs to GNOME Online
    /// Accounts (no password, no servers to edit) and tests it.
    private func startLinked(_ config: AccountConfig) {
        cancelSession()
        appPassword = nil
        linkedCfg = withIdentity(config, identity)
        identity.password = ""
        onIdentity?(identity)
        replace([.identity, .testing])
        runTest()
    }

    /// Opens the "sign in through GNOME Settings" page for an address of
    /// the discovered provider that the desktop is not signed in to yet,
    /// with the browser sign-in as the way around when the daemon offers
    /// it. The daemon's untrusted `providerName` names only a provider
    /// this client has no name of.
    private func showGOAHint(providerName untrusted: String?, discovery: Discovery) {
        goaHint = discovery
        var name = providerName(discovery.provider)
        if name.isEmpty {
            name = untrusted ?? ""
        }
        onGOAHint?(goaHintText(providerName: name), discovery.oauthAlt != nil)
        push(.goa)
    }

    /// The hint page's "Use the Browser Instead" (linked.go
    /// `onGOABrowser`): the daemon's own sign-in for the address.
    public func useBrowser() {
        guard let hint = goaHint, let alt = hint.oauthAlt else { return }
        showOAuthPrompt(provider: accountProvider(alt), config: alt, passwordAlt: hint.passwordAlt)
    }

    // MARK: Browser sign-in (oauth.go)

    private func promptView(_ name: String) -> OAuthView {
        .prompt(description: oauthPromptText(name), signInLabel: withoutMnemonic(oauthSignInLabel(name)))
    }

    /// Switches the sign-in page; its Sign In button is usable again
    /// (oauth.go `showOAuthStack`).
    private func showOAuthView(_ v: OAuthView) {
        oauthView = v
        setOAuthStarting(false)
        onOAuth?(v)
    }

    /// Opens the browser sign-in's prompt (oauth.go `showOAuthPrompt`):
    /// `config` is the new account to sign in, nil when editing (the
    /// account's id is signed in again); `passwordAlt` the app-password
    /// account the page may offer instead. A sign-in of before is dropped.
    private func showOAuthPrompt(provider: LinkedProvider?, config: AccountConfig?, passwordAlt: AccountConfig?) {
        cancelSession()
        let name = oauthProviderLabel(provider, email: config?.email ?? identity.email)
        oauth = OAuthState(provider: provider, name: name, config: config, passwordAlt: passwordAlt)
        showOAuthView(promptView(name))
        replace(signInMode ? [.oauth] : [.identity, .oauth])
    }

    /// The prompt's "Sign In with …" (oauth.go `onOAuthSignIn`): opens a
    /// session, hands its page to the browser and waits for it. An existing
    /// account is signed in again by its id. Only the button waits for the
    /// answer; Back stays possible and drops it.
    public func signInWithProvider() {
        guard let st = oauth, !oauthStarting else { return }
        cancelSession()
        var params = AccountOAuthStartParams(browserPage: browserPage())
        if let editing {
            params.accountId = editing.id
        } else if let cfg = st.config {
            params.config = withIdentity(cfg, identity)
        } else {
            return
        }
        setOAuthStarting(true)
        op += 1
        let op = self.op
        let client = client
        Task { [weak self] in
            let outcome: Result<AccountOAuthStartResult, any Error>
            do {
                outcome = .success(try await client.call(API.AccountOAuthStart.self, params, timeout: RPCTimeouts.oauthStart))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed, op == self.op else {
                // Nobody waits for this session any more: the wizard closed
                // or moved on.
                if case .success(let res) = outcome {
                    Self.cancelInBackground(client, res.sessionId)
                }
                return
            }
            self.setOAuthStarting(false)
            guard self.oauth != nil, self.pages.last == .oauth else {
                // Back left the sign-in while it started: its answer,
                // success or failure, is dropped (oauth.go `oauthShown`).
                if case .success(let res) = outcome {
                    Self.cancelInBackground(client, res.sessionId)
                }
                return
            }
            self.started(outcome)
        }
    }

    private func started(_ outcome: Result<AccountOAuthStartResult, any Error>) {
        switch outcome {
        case .failure(let error):
            log.debug("account.oauthStart: \(String(describing: error), privacy: .public)")
            if let e = error as? RPCError, e.code == .oauthClientMissing {
                showOAuthUnavailable()
                return
            }
            onToast?(rpcErrorText(L10n.T("Starting the sign-in"), error))
        case .success(let res):
            guard isBrowserURL(res.authUrl) else {
                // Only an https address from the daemon is opened: the
                // session is let go and the prompt stays, as nothing could
                // come back from the browser.
                log.warning("sign-in address refused: not https")
                Self.cancelInBackground(client, res.sessionId)
                onToast?(refusedBrowserURLText())
                return
            }
            oauth?.sessionId = res.sessionId
            oauth?.authUrl = res.authUrl
            oauth?.complete = false
            showOAuthView(.waiting)
            launch(res.authUrl)
            waitOAuth(res.sessionId)
        }
    }

    /// Calls account.oauthWait until the session ends (oauth.go
    /// `waitOAuth`): the daemon answers `pending` after at most a minute,
    /// and the next call goes out only while the wizard is open and nothing
    /// else started.
    private func waitOAuth(_ sessionId: String) {
        op += 1
        let op = self.op
        let client = client
        Task { [weak self] in
            while true {
                let outcome: Result<AccountOAuthWaitResult, any Error>
                do {
                    outcome = .success(try await client.call(
                        API.AccountOAuthWait.self, AccountOAuthWaitParams(sessionId: sessionId), timeout: RPCTimeouts.oauthWaitCall))
                } catch {
                    outcome = .failure(error)
                }
                guard let self, !self.closed, op == self.op, self.oauth?.sessionId == sessionId else { return }
                switch outcome {
                case .success(let res) where res.status == .pending:
                    continue
                case .success(let res) where res.status == .complete && res.config != nil:
                    if let cfg = res.config {
                        self.oauthCompleted(cfg)
                    }
                case .success(let res):
                    self.log.info("account.oauthWait: unexpected status \(res.status.rawValue, privacy: .public)")
                    self.oauthFailed(rpcErrorText(L10n.T("Signing in"), nil))
                case .failure(let error):
                    self.log.info("browser sign-in failed: \(String(describing: error), privacy: .public)")
                    self.oauthFailed(oauthErrorText(self.oauth?.name ?? "", error))
                }
                return
            }
        }
    }

    /// The browser came back signed in: the account is the daemon's, as
    /// for GNOME Online Accounts (`startLinked`), and its connection is
    /// tested with the session.
    private func oauthCompleted(_ config: AccountConfig) {
        log.info("browser sign-in complete")
        linkedCfg = withIdentity(config, identity)
        appPassword = nil
        oauth?.complete = true
        identity.password = ""
        onIdentity?(identity)
        // Back from the test leads to a prompt, ready to sign in again.
        showOAuthView(promptView(oauth?.name ?? ""))
        replace(signInMode ? [.oauth, .testing] : [.identity, .oauth, .testing])
        runTest()
    }

    /// The session ended without a sign-in (oauth.go `oauthFailed`): a
    /// session the daemon may still hold is cancelled, the prompt comes
    /// back and the toast says why.
    private func oauthFailed(_ text: String) {
        cancelSession()
        showOAuthView(promptView(oauth?.name ?? ""))
        onToast?(text)
    }

    /// Hands the provider's page to the browser (oauth.go `launch`): only
    /// an https address from the daemon (`started` refused any other; this
    /// guards "Open the Browser Again" too).
    private func launch(_ url: String) {
        guard isBrowserURL(url) else {
            log.warning("sign-in address refused: not https")
            onToast?(refusedBrowserURLText())
            return
        }
        onOpenURL?(url)
    }

    /// No OAuth client for the provider (oauth.go `showOAuthUnavailable`).
    private func showOAuthUnavailable() {
        showOAuthView(.unavailable(description: oauthUnavailableText(oauth?.name ?? ""), passwordAlternative: oauth?.passwordAlt != nil))
    }

    /// The waiting page's "Open the Browser Again".
    public func reopenBrowser() {
        guard oauthView == .waiting, let url = oauth?.authUrl else { return }
        launch(url)
    }

    /// The waiting page's Cancel (oauth.go `onOAuthCancel`): the session is
    /// cancelled and the prompt comes back, without a toast.
    public func cancelOAuth() {
        guard oauthView == .waiting else { return }
        op += 1
        cancelSession()
        showOAuthView(promptView(oauth?.name ?? ""))
    }

    /// "Use an App Password Instead" (oauth.go `onOAuthPassword`): the
    /// IMAP/SMTP account of the alternative with the identity's password,
    /// tested at once when one is typed; otherwise back to the identity
    /// for it.
    public func useAppPassword() {
        guard let alt = oauth?.passwordAlt else { return }
        cancelSession()
        appPassword = alt
        oauth = nil
        linkedCfg = nil
        continueWithAppPassword()
    }

    /// Continues with the chosen app-password account (oauth.go
    /// `useAppPassword`): to the connection test when a password is typed,
    /// else back to the identity page for one (Next then continues here).
    private func continueWithAppPassword() {
        guard let alt = appPassword else { return }
        var id = identity
        id.email = validateEmail(id.email) ?? id.email
        applyConfig(mergeIdentity(alt, id))
        if id.password.isEmpty {
            replace([.identity])
            _ = requirePassword(banner: L10n.T("Enter the app password for this account"))
            return
        }
        replace([.identity, .servers, .testing])
        runTest()
    }

    /// Drops the session of the browser sign-in, if any (oauth.go
    /// `cancelSession`): account.oauthCancel, fire and forget.
    private func cancelSession() {
        guard let session = oauth?.sessionId else { return }
        oauth?.sessionId = nil
        oauth?.authUrl = nil
        oauth?.complete = false
        Self.cancelInBackground(client, session)
    }

    private nonisolated static func cancelInBackground(_ client: RPCClient, _ session: String) {
        guard !session.isEmpty else { return }
        Task {
            do {
                _ = try await client.call(API.AccountOAuthCancel.self, AccountOAuthCancelParams(sessionId: session))
            } catch {
                Logger(subsystem: "io.github.schotek.Malachi", category: "accountwizard")
                    .debug("account.oauthCancel: \(String(describing: error), privacy: .public)")
            }
        }
    }

    // MARK: Test

    /// Calls account.test with the current settings (wizard.go `runTest`).
    private func runTest() {
        onTesting?(.progress(title: L10n.T("Testing Connection…")))
        trustOffers = [:]
        var params = AccountTestParams(config: assembleConfig(), credentials: credentials())
        if let editing {
            params.accountId = editing.id // an empty password means "use the stored one"
        }
        op += 1
        let op = self.op
        let client = client
        Task { [weak self] in
            let outcome: Result<AccountTestResult, any Error>
            do {
                outcome = .success(try await client.call(API.AccountTest.self, params, timeout: RPCTimeouts.test))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed, op == self.op else { return }
            self.showResults(outcome, tested: params.config)
        }
    }

    private func showResults(_ outcome: Result<AccountTestResult, any Error>, tested: AccountConfig) {
        // A Graph account has one endpoint, the mailbox; a Google account is
        // tested like any IMAP one, only without a password to correct.
        let linked = linkedCfg != nil
        let browser = signsInWithBrowser
        let graph = linked && linkedCfg?.protocolKind == .graph
        var view = ResultsView(icon: "dialog-warning-symbolic", title: L10n.T("Connection Failed"), buttons: .none)
        var result: Outcome
        // A password account with no password stored and none typed.
        var missingPassword = false
        switch outcome {
        case .failure(let error):
            let row = EndpointRow(icon: "dialog-warning-symbolic", text: rpcErrorText(L10n.T("Testing the connection"), error))
            if graph {
                view.graph = row
            } else {
                view.imap = row
                view.smtp = row
            }
            result = .failed
            if passwordMissing(error, linked: linked) {
                // No password stored and none typed: ask for it like for a
                // refused one, and offer no "Save Anyway" without it.
                result = .authFailed
                missingPassword = true
            }
        case .success(let res) where graph:
            let (icon, text) = endpointSummary(res.graph)
            view.graph = EndpointRow(icon: icon, text: text)
            result = classify(res)
        case .success(let res):
            // A certificate the user may trust (trust.go `offerFor`) puts
            // "Trust Certificate…" into its row; never for an account the
            // daemon built.
            trustOffers = [:]
            if !linked {
                if let sc = tested.imap, let p = CertTrust.trustable(res.imap, sc) {
                    trustOffers[.imap] = (sc, p)
                }
                if let sc = tested.smtp, let p = CertTrust.trustable(res.smtp, sc) {
                    trustOffers[.smtp] = (sc, p)
                }
            }
            let (imapIcon, imapText) = endpointSummary(res.imap)
            view.imap = EndpointRow(icon: imapIcon, text: imapText, trust: trustOffers[.imap] != nil)
            let (smtpIcon, smtpText) = endpointSummary(res.smtp)
            view.smtp = EndpointRow(icon: smtpIcon, text: smtpText, trust: trustOffers[.smtp] != nil)
            result = classify(res)
        }
        lastSignInProblem = false
        if browser {
            // The sign-in, or the permissions granted in the browser, are
            // the problem: offered again instead of a password.
            if isSignInProblem(outcome) {
                result = .failed
                lastSignInProblem = true
                view.description = L10n.T("The server refused the sign-in. Sign in again and make sure access to mail is allowed.")
            }
        } else if linked, result == .authFailed {
            // There is no password to correct here: the sign-in, or the
            // permissions it was granted, live in GNOME Online Accounts.
            result = .failed
            view.description = L10n.T("The server refused the sign-in. Sign in to the account again in Settings → Online Accounts and make sure access to mail is allowed.")
        }
        lastOutcome = result

        switch result {
        case .ok:
            view.icon = "emblem-ok-symbolic"
            view.title = isEditing ? L10n.T("Ready to Save") : L10n.T("Ready to Add")
        case .authFailed:
            popTo(.identity)
            askPassword(missingPassword ? .authRequired : .authFailed)
        case .failed:
            break
        }
        view.buttons = buttons(for: result)
        lastResults = view
        onTesting?(.results(view))
    }

    /// The edit button is Edit Servers for a password account and Sign In
    /// Again for a browser sign-in with a sign-in problem; a GNOME Online
    /// Accounts account has neither.
    private func buttons(for o: Outcome) -> WizardButtons {
        let edit = linkedCfg == nil || (signsInWithBrowser && lastSignInProblem)
        return WizardButtons(edit: edit, retry: o == .failed, addAnyway: o == .failed, add: o == .ok)
    }

    // MARK: Save

    /// Stores the account (account.add, or account.update when editing)
    /// and reports `onDone` on success (wizard.go `onAdd`).
    private func save() {
        let cfg = assembleConfig()
        let creds = credentials()
        let editing = editing
        onTesting?(.progress(title: progressTitle))
        op += 1
        let op = self.op
        let client = client
        Task { [weak self] in
            let outcome: Result<AccountID, any Error>
            do {
                if let editing {
                    _ = try await client.call(
                        API.AccountUpdate.self,
                        AccountUpdateParams(accountId: editing.id, config: cfg, credentials: creds),
                        timeout: RPCTimeouts.save
                    )
                    outcome = .success(editing.id)
                } else {
                    let res = try await client.call(
                        API.AccountAdd.self, AccountAddParams(config: cfg, credentials: creds), timeout: RPCTimeouts.save
                    )
                    outcome = .success(res.accountId)
                }
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed, op == self.op else { return }
            switch outcome {
            case .failure(let error):
                if var view = self.lastResults {
                    view.buttons = self.buttons(for: self.lastOutcome)
                    self.lastResults = view
                    self.onTesting?(.results(view))
                }
                self.onToast?(saveErrorText(error, editing: editing != nil))
            case .success(let id):
                self.log.info("account saved: \(id.rawValue, privacy: .public), edit \(editing != nil)")
                // The daemon consumed the sign-in; closing must not cancel it.
                self.oauth = nil
                self.onDone?(id, cfg)
            }
        }
    }
}

/// The toast for a failed account.add / account.update (wizard.go
/// `saveErrorText`): a conflict is the one code with its own sentence.
public func saveErrorText(_ error: any Error, editing: Bool) -> String {
    if let e = error as? RPCError, e.code == .conflict {
        return L10n.T("An account with this e-mail address already exists")
    }
    if editing {
        return rpcErrorText(L10n.T("Saving the account"), error)
    }
    return rpcErrorText(L10n.T("Adding the account"), error)
}

/// A GTK label with its mnemonic marker removed: `_Next` → `Next`, `__` → `_`.
func withoutMnemonic(_ s: String) -> String {
    var out = ""
    var iterator = s.makeIterator()
    while let c = iterator.next() {
        if c == "_" {
            if let n = iterator.next() {
                out.append(n)
            }
            continue
        }
        out.append(c)
    }
    return out
}
