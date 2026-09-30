// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The flow of the assistant that adds a Jira account (kind `jira`), with
/// the widgets replaced by callbacks, as `WizardController` does for mail
/// accounts. Its pages, texts and rules are ui/internal/jira/wizard.go
/// (`Jira` here); macOS is the first client, so there is no GTK flow to
/// mirror yet (Swift-first: mirror in ui/internal/jira when GTK gets Jira
/// accounts).
///
/// Three pages: the site (account.detectSite), the credentials (checked by
/// account.listSpaces, which also lists the spaces with an estimate of
/// their issues) and the spaces with the offline window and "Only Issues
/// Involving Me" (account.add). Editing an account (M1: its token only)
/// opens on the credentials page and saves the unchanged configuration
/// with the new token through account.update.
///
/// Every RPC reply is dropped once the assistant closed (`closed`) or
/// anything else started since (`op`: Back, another call). Nothing typed
/// here is logged; the token leaves only in `Credentials`.
///
/// Every callback runs on the main actor. Wire them, then call `start()`.
@MainActor
public final class JiraWizardController {
    public typealias Page = Jira.Page

    /// The fields the UI can flag and focus.
    public enum Field: Sendable, Hashable {
        case site
        case login
        case token
        /// The account's address on a Data Center site that hides it.
        case email
    }

    // MARK: Outputs

    /// The page stack changed; the last page is the visible one.
    public var onPages: (@MainActor ([Page]) -> Void)?
    /// A call started, with its progress text, or finished (nil). The
    /// visible page waits for it; Back stays usable and drops its reply.
    public var onBusy: (@MainActor (String?) -> Void)?
    /// What the site field allows now: Next, and the text under the field
    /// ("" for none) (`Jira.checkSiteInput`).
    public var onSiteCheck: (@MainActor (_ ok: Bool, _ problem: String) -> Void)?
    /// The text under the site field once the site answered
    /// (`Jira.detected`); "" when the address changed since.
    public var onDetected: (@MainActor (String) -> Void)?
    /// The credentials page for the site's deployment
    /// (`Jira.credentialFields`); the fields are `login` and an empty token.
    public var onCredentialPage: (@MainActor (Jira.CredentialPage) -> Void)?
    /// The spaces page's list, in the daemon's order, with the ids chosen.
    public var onSpaces: (@MainActor (_ rows: [Jira.SpaceRow], _ selected: Set<String>) -> Void)?
    /// Why the spaces page cannot add the account now ("" when it can)
    /// (`Jira.spacesProblem`).
    public var onSpacesProblem: (@MainActor (String) -> Void)?
    /// Whether the spaces page asks for the account's e-mail address (a
    /// Data Center site that does not reveal it, `Jira.needsEmail`), and
    /// the address to show in the field.
    public var onEmailField: (@MainActor (_ shown: Bool, _ email: String) -> Void)?
    /// A page's banner; nil hides it.
    public var onBanner: (@MainActor (Page, String?) -> Void)?
    /// The fields to flag; an empty set clears the flags.
    public var onProblems: (@MainActor (Set<Field>) -> Void)?
    /// Move the keyboard focus to a field.
    public var onFocus: (@MainActor (Field) -> Void)?
    /// Open the page where a Jira Cloud user creates an API token.
    public var onOpenURL: (@MainActor (String) -> Void)?
    /// The account was stored; the UI closes the assistant.
    public var onDone: (@MainActor (AccountID, AccountConfig) -> Void)?

    // MARK: State

    public let client: RPCClient
    /// The account whose token is replaced; nil when adding one.
    public let editing: Account?
    /// The fixed texts (`Jira.wizardTexts`).
    public let texts = Jira.wizardTexts()

    public private(set) var pages: [Page]
    public private(set) var closed = false
    /// Bumped per RPC and by Back so stale replies bail out.
    public private(set) var op = 0
    /// The progress text of the call under way; nil when none runs.
    public private(set) var progress: String?

    /// The site field as typed.
    public private(set) var siteInput = ""
    /// What account.detectSite found for `siteInput`; nil until it
    /// answered and again once the address changed. When editing, the
    /// account's site.
    public private(set) var site: AccountDetectSiteResult?
    /// The Atlassian account's e-mail address (Jira Cloud's login).
    public private(set) var login = ""
    /// The token as typed; sent trimmed.
    public private(set) var token = ""
    /// The account's address on Data Center (the signed-in user's, or
    /// typed when the site hides it).
    public private(set) var email = ""
    /// The user account.listSpaces signed in as.
    public private(set) var user: SiteUser?
    /// The spaces of the last account.listSpaces, in its order.
    public private(set) var spaces: [Space] = []
    /// The ids of the chosen spaces.
    public private(set) var selected: Set<String> = []
    public private(set) var onlyMine = false
    /// The offline window, one of `Jira.offlineChoices`.
    public private(set) var offlineDays = API.Limits.defaultJiraOfflineDays
    /// The spaces page asks for the account's address.
    public private(set) var needsEmail = false

    /// The site `spaces` were listed for: a space id means nothing on
    /// another site, so the choice starts over there.
    private var spacesSite: String?
    private var problems: Set<Field> = []
    /// `requestToken`'s reason, shown by `start()`.
    private var tokenRequest: ErrorCode?
    private var started = false
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "jirawizard")

    /// `editing` opens on the credentials page and saves with
    /// account.update; the configuration stays as it is.
    public init(client: RPCClient, editing: Account? = nil) {
        self.client = client
        self.editing = editing
        guard let a = editing else {
            pages = [.site]
            return
        }
        pages = [.credentials]
        email = a.config.email
        if let jc = a.config.jira {
            siteInput = jc.siteUrl
            site = AccountDetectSiteResult(kind: .jira, siteUrl: jc.siteUrl, deployment: jc.deployment, cloudId: jc.cloudId)
            login = jc.login ?? ""
            offlineDays = Jira.offlineChoices[Jira.indexOfOfflineDays(jc.offlineDays ?? 0)]
            onlyMine = jc.onlyMine ?? false
        }
    }

    // MARK: Presentation

    public var isEditing: Bool { editing != nil }

    /// The window's title.
    public var title: String {
        isEditing ? L10n.T("Edit Account") : texts.title
    }

    /// The deployment the pages are for: the found site's, Jira Cloud
    /// before one was found.
    public var deployment: JiraDeployment { site?.deployment ?? .cloud }

    /// The credentials page of `deployment`.
    public var credentialPage: Jira.CredentialPage { Jira.credentialFields(deployment) }

    /// The login belongs to the account being edited (its address and its
    /// uniqueness); only the token changes.
    public var loginEditable: Bool { !isEditing }

    /// A page's title in the header.
    public func pageTitle(_ page: Page) -> String {
        switch page {
        case .site: return texts.siteTitle
        case .credentials: return L10n.T("Sign In")
        case .spaces: return texts.spacesTitle
        }
    }

    /// A page's button: Next, Save when editing, Add Account on the spaces.
    public func nextLabel(_ page: Page) -> String {
        switch page {
        case .site: return withoutMnemonic(L10n.T("_Next"))
        case .credentials: return withoutMnemonic(isEditing ? L10n.T("_Save") : L10n.T("_Next"))
        case .spaces: return withoutMnemonic(L10n.T("_Add Account"))
        }
    }

    /// The header's Back is offered.
    public var canGoBack: Bool { pages.count > 1 }

    /// The labels of the offline window's choices and the one shown.
    public var offlineLabels: [String] { Jira.offlineChoiceLabels() }
    public var offlineIndex: Int { Jira.indexOfOfflineDays(offlineDays) }

    /// A call runs.
    public var busy: Bool { progress != nil }

    // MARK: Lifecycle

    /// Delivers the initial state to the callbacks. Call once, after
    /// wiring them.
    public func start() {
        let check = Jira.checkSiteInput(siteInput)
        onSiteCheck?(check.ok, check.problem)
        onCredentialPage?(credentialPage)
        onPages?(pages)
        started = true
        showTokenRequest()
    }

    /// The edit assistant of an account whose token is missing
    /// (`authRequired`) or was refused (`authFailed`): the credentials page
    /// says so in its banner, with the token field flagged and focused
    /// (the sign-in banner's button, `WizardController.requestPassword` for
    /// mail accounts). Ignored when adding. Call before `start()`.
    public func requestToken(reason: ErrorCode) {
        guard isEditing else { return }
        tokenRequest = reason
        if started {
            showTokenRequest()
        }
    }

    private func showTokenRequest() {
        guard let reason = tokenRequest else { return }
        tokenRequest = nil
        let f = Jira.failureOf(.save, Jira.classOf(reason), deployment, editing: true)
        if !f.banner.isEmpty {
            onBanner?(.credentials, f.banner)
        }
        flag([.token])
        onFocus?(.token)
    }

    /// The assistant went away: every late reply is dropped from now on.
    public func close() {
        closed = true
        progress = nil
    }

    // MARK: Inputs from the UI

    /// The site field as typed. A changed address forgets the site found
    /// for the previous one.
    public func setSite(_ raw: String) {
        guard raw != siteInput else { return }
        siteInput = raw
        let check = Jira.checkSiteInput(raw)
        onSiteCheck?(check.ok, check.problem)
        clear(.site)
        onBanner?(.site, nil)
        if !isEditing, site != nil {
            site = nil
            onDetected?("")
        }
    }

    /// The credentials as typed. The login of an account being edited
    /// stays its own.
    public func setCredentials(login: String, token: String) {
        let newLogin = isEditing ? self.login : login
        guard newLogin != self.login || token != self.token else { return }
        if newLogin != self.login {
            clear(.login)
        }
        if token != self.token {
            clear(.token)
        }
        self.login = newLogin
        self.token = token
        onBanner?(.credentials, nil)
    }

    /// The account's address on the spaces page (Data Center).
    public func setEmail(_ s: String) {
        guard s != email else { return }
        email = s
        clear(.email)
    }

    /// A space's check box.
    public func setSpace(_ id: String, selected on: Bool) {
        guard spaces.contains(where: { $0.id == id }), on != selected.contains(id) else { return }
        if on {
            selected.insert(id)
        } else {
            selected.remove(id)
        }
        onSpacesProblem?(Jira.spacesProblem(selected.count))
        onBanner?(.spaces, nil)
    }

    /// "Only Issues Involving Me".
    public func setOnlyMine(_ on: Bool) {
        onlyMine = on
    }

    /// The offline window's choice (an index of `Jira.offlineChoices`).
    /// The estimates on the spaces page count the issues of the window, so
    /// a new window asks for them again.
    public func setOfflineIndex(_ i: Int) {
        guard Jira.offlineChoices.indices.contains(i) else { return }
        let days = Jira.offlineChoices[i]
        guard days != offlineDays else { return }
        offlineDays = days
        if !isEditing, pages.last == .spaces, !spaces.isEmpty {
            refreshCounts()
        }
    }

    /// The visible page's button: look the site up, check the credentials
    /// and list the spaces (or save the token when editing), add the
    /// account.
    public func next() {
        guard !closed, !busy, let page = pages.last else { return }
        switch page {
        case .site:
            detect()
        case .credentials:
            if isEditing {
                save()
            } else {
                loadSpaces()
            }
        case .spaces:
            add()
        }
    }

    /// The header's Back: pops the visible page; a call under way is
    /// dropped.
    public func back() {
        guard canGoBack else { return }
        op += 1
        setBusy(nil)
        pages.removeLast()
        onPages?(pages)
    }

    /// The credentials page's "Create API Token…" (Jira Cloud only).
    public func openTokenHelp() {
        let url = credentialPage.helpURL
        guard !url.isEmpty else { return }
        onOpenURL?(url)
    }

    // MARK: Navigation

    private func push(_ page: Page) {
        if let i = pages.firstIndex(of: page) {
            pages.removeSubrange((i + 1)...)
        } else {
            pages.append(page)
        }
        onPages?(pages)
    }

    private func popTo(_ page: Page) {
        guard let i = pages.firstIndex(of: page), i + 1 < pages.count else { return }
        pages.removeSubrange((i + 1)...)
        onPages?(pages)
    }

    // MARK: Fields

    private func setBusy(_ text: String?) {
        guard progress != text else { return }
        progress = text
        onBusy?(text)
    }

    private func flag(_ fields: Set<Field>) {
        problems = fields
        onProblems?(fields)
    }

    private func clear(_ field: Field) {
        guard problems.contains(field) else { return }
        problems.remove(field)
        onProblems?(problems)
    }

    /// The token as sent: a pasted one often carries a line break.
    private var trimmedToken: String { Jira.trimSpace(token) }

    /// What the pages collected, for account.listSpaces and account.add.
    private func setup(_ site: AccountDetectSiteResult) -> Jira.Setup {
        Jira.Setup(
            site: site, login: login, email: email, spaces: spaces.filter { selected.contains($0.id) },
            onlyMine: onlyMine, offlineDays: offlineDays
        )
    }

    /// A failed step (`Jira.failureOf`): back to its page with the banner,
    /// the client's sentence for the error when the step has none; a
    /// refused or missing token flags the token field.
    private func fail(_ step: Jira.Step, _ error: any Error) {
        fail(step, Jira.classify(error), error)
    }

    private func fail(_ step: Jira.Step, _ cls: Jira.ErrorClass, _ error: (any Error)?) {
        log.info("jira assistant step \(step.rawValue, privacy: .public) failed: class \(cls.rawValue, privacy: .public)")
        let f = Jira.failureOf(step, cls, deployment, editing: isEditing)
        popTo(f.page)
        onBanner?(f.page, f.banner.isEmpty ? rpcErrorText(f.what, error) : f.banner)
        switch (f.page, cls) {
        case (.site, .invalid), (.site, .server):
            flag([.site])
            onFocus?(.site)
        case (.credentials, .authFailed), (.credentials, .authRequired):
            flag([.token])
            onFocus?(.token)
        default:
            break
        }
    }

    // MARK: Site

    /// account.detectSite for the typed address.
    private func detect() {
        let check = Jira.checkSiteInput(siteInput)
        guard check.ok else {
            onSiteCheck?(check.ok, check.problem)
            flag([.site])
            onFocus?(.site)
            return
        }
        onBanner?(.site, nil)
        let params = AccountDetectSiteParams(url: Jira.trimSpace(siteInput))
        setBusy(texts.lookingUp)
        op += 1
        let op = self.op
        let client = client
        Task { [weak self] in
            let outcome: Result<AccountDetectSiteResult, any Error>
            do {
                outcome = .success(try await client.call(API.AccountDetectSite.self, params, timeout: RPCTimeouts.detectSite))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed, op == self.op else { return }
            self.setBusy(nil)
            self.detected(outcome)
        }
    }

    private func detected(_ outcome: Result<AccountDetectSiteResult, any Error>) {
        switch outcome {
        case .failure(let error):
            fail(.detect, error)
        case .success(let res):
            guard res.kind == .jira, !res.siteUrl.isEmpty else {
                // Not a site this client can add: as the daemon's
                // serverError says, it is not Jira.
                fail(.detect, .server, nil)
                return
            }
            log.info("jira site found: \(res.deployment.rawValue, privacy: .public)")
            site = res
            onDetected?(Jira.detected(res))
            onCredentialPage?(credentialPage)
            push(.credentials)
            onFocus?(res.deployment == .cloud && login.isEmpty ? .login : .token)
        }
    }

    // MARK: Credentials

    /// Checks the typed credentials before a call: Jira Cloud needs the
    /// login (an address), every site a token.
    private func credentialsProblems() -> Set<Field> {
        var p: Set<Field> = []
        if deployment == .cloud, validateEmail(login) == nil {
            p.insert(.login)
        }
        if trimmedToken.isEmpty {
            p.insert(.token)
        }
        return p
    }

    /// Flags what `credentialsProblems` found; a missing token alone gets
    /// the page's prompt as the banner.
    private func showCredentialsProblems(_ p: Set<Field>) {
        flag(p)
        if p == [.token] {
            onBanner?(.credentials, credentialPage.tokenPrompt)
        }
        onFocus?(p.contains(.login) ? .login : .token)
    }

    /// account.listSpaces with the typed credentials: the sign-in test and
    /// the spaces page's list, with the estimates for the offline window.
    private func loadSpaces() {
        guard let site else {
            popTo(.site)
            return
        }
        let p = credentialsProblems()
        guard p.isEmpty else {
            showCredentialsProblems(p)
            return
        }
        onBanner?(.credentials, nil)
        listSpaces(site, refresh: false)
    }

    /// The estimates again, for a new offline window, without leaving the
    /// spaces page; the old ones are gone meanwhile.
    private func refreshCounts() {
        guard let site else { return }
        for i in spaces.indices {
            spaces[i].issues = -1
        }
        onSpaces?(Jira.spaceRows(spaces), selected)
        listSpaces(site, refresh: true)
    }

    private func listSpaces(_ site: AccountDetectSiteResult, refresh: Bool) {
        var s = setup(site)
        s.spaces = []
        let params = AccountListSpacesParams(config: s.config(), credentials: Credentials(password: trimmedToken), counts: true)
        setBusy(texts.loadingSpaces)
        op += 1
        let op = self.op
        let client = client
        Task { [weak self] in
            let outcome: Result<AccountListSpacesResult, any Error>
            do {
                outcome = .success(try await client.call(API.AccountListSpaces.self, params, timeout: RPCTimeouts.listSpaces))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed, op == self.op else { return }
            self.setBusy(nil)
            switch outcome {
            case .success(let res):
                self.spacesLoaded(res, site: site, refresh: refresh)
            case .failure(let error) where refresh:
                // The page stays; only the estimates are missing.
                let cls = Jira.classify(error)
                self.log.info("jira spaces recount failed: class \(cls.rawValue, privacy: .public)")
                let f = Jira.failureOf(.spaces, cls, site.deployment, editing: false)
                self.onBanner?(.spaces, f.banner.isEmpty ? rpcErrorText(f.what, error) : f.banner)
            case .failure(let error):
                self.fail(.spaces, error)
            }
        }
    }

    private func spacesLoaded(_ res: AccountListSpacesResult, site: AccountDetectSiteResult, refresh: Bool) {
        log.info("jira spaces listed: \(res.spaces.count, privacy: .public)")
        if spacesSite != site.siteUrl {
            selected = []
        }
        spacesSite = site.siteUrl
        user = res.user
        spaces = res.spaces
        selected.formIntersection(spaces.map(\.id))
        if !refresh, selected.isEmpty, spaces.count == 1 {
            // A single space is what the user means.
            selected = [spaces[0].id]
        }
        needsEmail = Jira.needsEmail(site.deployment, res.user)
        if site.deployment != .cloud, !needsEmail {
            email = Jira.trimSpace(res.user.email ?? "")
        }
        onSpaces?(Jira.spaceRows(spaces), selected)
        onSpacesProblem?(Jira.spacesProblem(selected.count))
        onEmailField?(needsEmail, email)
        if !refresh {
            onBanner?(.spaces, nil)
            push(.spaces)
        }
    }

    // MARK: Save

    /// account.add from the spaces page.
    private func add() {
        guard let site else {
            popTo(.site)
            return
        }
        let problem = Jira.spacesProblem(selected.count)
        guard problem.isEmpty else {
            onBanner?(.spaces, problem)
            return
        }
        var s = setup(site)
        if needsEmail {
            guard let address = validateEmail(email) else {
                flag([.email])
                onFocus?(.email)
                return
            }
            s.email = address
        }
        onBanner?(.spaces, nil)
        store(s.config(), step: texts.adding)
    }

    /// account.update of the account being edited: its configuration as
    /// it is, with the new token. The token is tried on the site first
    /// (account.listSpaces with the account's configuration), so a refused
    /// one never replaces the stored token.
    private func save() {
        guard let editing else { return }
        guard !trimmedToken.isEmpty else {
            showCredentialsProblems([.token])
            return
        }
        onBanner?(.credentials, nil)
        let params = AccountListSpacesParams(
            accountId: editing.id, config: editing.config, credentials: Credentials(password: trimmedToken)
        )
        setBusy(texts.saving)
        op += 1
        let op = self.op
        let client = client
        Task { [weak self] in
            var failure: (any Error)?
            do {
                _ = try await client.call(API.AccountListSpaces.self, params, timeout: RPCTimeouts.listSpaces)
            } catch {
                failure = error
            }
            guard let self, !self.closed, op == self.op else { return }
            if let failure {
                self.setBusy(nil)
                self.fail(.save, failure)
                return
            }
            self.store(editing.config, step: self.texts.saving)
        }
    }

    private func store(_ cfg: AccountConfig, step: String) {
        let creds = Credentials(password: trimmedToken)
        let editing = editing
        setBusy(step)
        op += 1
        let op = self.op
        let client = client
        Task { [weak self] in
            let outcome: Result<AccountID, any Error>
            do {
                if let editing {
                    _ = try await client.call(
                        API.AccountUpdate.self, AccountUpdateParams(accountId: editing.id, config: cfg, credentials: creds),
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
            self.setBusy(nil)
            switch outcome {
            case .failure(let error):
                self.fail(.save, error)
            case .success(let id):
                self.log.info("jira account saved: \(id.rawValue, privacy: .public), edit \(editing != nil)")
                self.onDone?(id, cfg)
            }
        }
    }
}
