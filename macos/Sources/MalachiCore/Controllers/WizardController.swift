// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The flow of the "Add Account" wizard, the counterpart of
/// ui/internal/accountwizard/wizard.go and linked.go with the widgets
/// replaced by callbacks. It owns the page stack (`pages`, mirroring
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
        case testing

        /// The order pages appear in, for the direction of a transition.
        public var rank: Int {
            switch self {
            case .identity: return 0
            case .servers: return 1
            case .goa: return 2
            case .testing: return 3
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
    /// symbol by the UI) and the subtitle.
    public struct EndpointRow: Sendable, Equatable {
        public var icon: String
        public var text: String

        public init(icon: String, text: String) {
            self.icon = icon
            self.text = text
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
    /// The text of the sign-in hint page.
    public var onGOAHint: (@MainActor (String) -> Void)?
    /// The testing page's content.
    public var onTesting: (@MainActor (TestingView) -> Void)?
    /// Move the keyboard focus to an identity field.
    public var onFocus: (@MainActor (IdentityField) -> Void)?
    /// Show a toast inside the wizard.
    public var onToast: (@MainActor (String) -> Void)?
    /// The account was stored; the UI closes the wizard.
    public var onDone: (@MainActor (AccountID, AccountConfig) -> Void)?

    // MARK: State

    public let client: RPCClient
    /// The account being changed; nil when adding a new one.
    public let editing: Account?

    public private(set) var identity = Identity()
    public private(set) var accountName = ""
    public private(set) var imap = ServerFields(port: 993, security: .tls)
    public private(set) var smtp = ServerFields(port: 587, security: .starttls)
    public private(set) var linked: [LinkedAccount] = []
    /// Set for an account whose sign-in lives in GNOME Online Accounts:
    /// the daemon built it, there is no password and the servers are not
    /// the user's to edit. nil is the password path.
    public private(set) var linkedCfg: AccountConfig?
    public private(set) var lastOutcome: Outcome = .failed
    public private(set) var pages: [WizardPage] = [.identity]
    public private(set) var closed = false
    /// Bumped per RPC so stale replies bail out.
    public private(set) var op = 0
    public private(set) var busy = false

    private var lastResults: ResultsView?
    private var identityProblemsShown = false
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "accountwizard")

    /// `editing` prefills the pages, skips discovery, keeps the stored
    /// password on an empty one and saves with account.update; the wizard
    /// then opens on the Servers page with Back leading to the identity.
    public init(client: RPCClient, editing: Account? = nil) {
        self.client = client
        self.editing = editing
        guard let a = editing else { return }
        identity = Identity(displayName: a.config.displayName ?? "", email: a.config.email, password: "")
        if goaOwned(a.config) {
            // The address and the sign-in belong to GNOME Online Accounts;
            // only the name can change here, and the test re-checks the
            // sign-in.
            linkedCfg = a.config
            pages = [.identity]
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

    /// The dialog and identity page title.
    public var title: String { isEditing ? L10n.T("Edit Account") : L10n.T("Add Account") }

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
        onPages?(pages)
        loadLinked(then: nil)
    }

    /// The wizard went away: every late reply is dropped from now on.
    public func close() {
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

    /// The Servers page rows as typed.
    public func setServers(name: String, imap: ServerFields, smtp: ServerFields) {
        accountName = name
        self.imap = imap
        self.smtp = smtp
    }

    /// The identity page's Next button (wizard.go `onNext`): validate, then
    /// ask the daemon for server settings. A Microsoft 365 address goes to
    /// the connection test (signed in through GNOME Online Accounts) or to
    /// the sign-in hint; an IMAP hit goes to the connection test; a miss
    /// opens the Servers page with guessed defaults. The password is asked
    /// for only once the account turns out to need one.
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
        if case .success(let res) = outcome, let cfg = res.config, goaOwned(cfg) {
            // Signed in through GNOME Online Accounts: the daemon's account
            // is complete; otherwise it is the hint that the sign-in must
            // happen there first.
            log.info("account discovered: \(res.source.rawValue, privacy: .public)")
            if linkedAccountID(cfg) != nil {
                startLinked(cfg)
                return
            }
            showGOAHint(providerName: res.providerName)
            return
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

    /// The results page's Edit Servers button.
    public func edit() {
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

    /// The header's Back button: pops the visible page.
    public func back() {
        guard pages.count > 1 else { return }
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

    private func showIdentityProblems(_ p: IdentityProblems, banner: String?) {
        identityProblemsShown = true
        onIdentityProblems?(p, banner)
    }

    /// Stores the endpoint rows of a configuration (wizard.go `serverRows.apply`).
    private func setFields(from cfg: AccountConfig) {
        accountName = cfg.name
        if let s = cfg.imap {
            imap = ServerFields(host: s.host, port: s.port, security: s.security, username: s.username)
        }
        if let s = cfg.smtp {
            smtp = ServerFields(host: s.host, port: s.port, security: s.security, username: s.username)
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
    private func requirePassword() -> Bool {
        if editing != nil || !identity.password.isEmpty {
            return true
        }
        showIdentityProblems(IdentityProblems(password: true), banner: L10n.T("Enter the password for this account"))
        onFocus?(.password)
        return false
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
        linkedCfg = withIdentity(config, identity)
        identity.password = ""
        onIdentity?(identity)
        replace([.identity, .testing])
        runTest()
    }

    /// Opens the "sign in through GNOME Settings" page for an address of
    /// the named provider that the desktop is not signed in to yet.
    private func showGOAHint(providerName: String?) {
        onGOAHint?(goaHintText(providerName: providerName))
        push(.goa)
    }

    // MARK: Test

    /// Calls account.test with the current settings (wizard.go `runTest`).
    private func runTest() {
        onTesting?(.progress(title: L10n.T("Testing Connection…")))
        var params = AccountTestParams(config: assembleConfig())
        if linkedCfg == nil {
            params.credentials = credentialsFor(identity)
        }
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
            self.showResults(outcome)
        }
    }

    private func showResults(_ outcome: Result<AccountTestResult, any Error>) {
        // A Graph account has one endpoint, the mailbox; a Google account is
        // tested like any IMAP one, only without a password to correct.
        let linked = linkedCfg != nil
        let graph = linked && linkedCfg?.protocolKind == .graph
        var view = ResultsView(icon: "dialog-warning-symbolic", title: L10n.T("Connection Failed"), buttons: .none)
        var result: Outcome
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
        case .success(let res) where graph:
            let (icon, text) = endpointSummary(res.graph)
            view.graph = EndpointRow(icon: icon, text: text)
            result = classify(res)
        case .success(let res):
            let (imapIcon, imapText) = endpointSummary(res.imap)
            view.imap = EndpointRow(icon: imapIcon, text: imapText)
            let (smtpIcon, smtpText) = endpointSummary(res.smtp)
            view.smtp = EndpointRow(icon: smtpIcon, text: smtpText)
            result = classify(res)
        }
        if linked, result == .authFailed {
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
            showIdentityProblems(IdentityProblems(password: true), banner: L10n.T("The server rejected the user name or password"))
            onFocus?(.password)
        case .failed:
            break
        }
        view.buttons = buttons(for: result)
        lastResults = view
        onTesting?(.results(view))
    }

    private func buttons(for o: Outcome) -> WizardButtons {
        WizardButtons(edit: linkedCfg == nil, retry: o == .failed, addAnyway: o == .failed, add: o == .ok)
    }

    // MARK: Save

    /// Stores the account (account.add, or account.update when editing)
    /// and reports `onDone` on success (wizard.go `onAdd`).
    private func save() {
        let cfg = assembleConfig()
        let creds = linkedCfg == nil ? credentialsFor(identity) : Credentials()
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
private func withoutMnemonic(_ s: String) -> String {
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
