// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The flow of the assistant that adds an eWay-CRM account (kind `eway`,
/// docs/eway.md §2.4), with the widgets replaced by callbacks: a 1:1 port
/// of ui/internal/accountwizard eway_flow.go (`ewayFlow`), tests included.
/// The texts and the rules of its fields and failures are `CRM`
/// (`wizardTexts`, `checkServiceInput`, `failureOf`).
///
/// Two pages: the web service address (account.detectEway, which also
/// says whether the instance signs in with Microsoft, which this version
/// cannot do: the assistant says so there, before any password is asked),
/// and the sign-in with the account's name, user name and password and
/// the check box "Also save later messages of saved conversations" (on
/// for a new account, docs/eway.md D22) (account.test, one sign-in, then
/// account.add). Editing an account
/// (`Integrations.Editor.eway`) opens on the sign-in page with the
/// account's address, name and user name; the password may stay empty to
/// keep the stored one, unless the user name changed. It saves through
/// account.update.
///
/// Every RPC reply is dropped once the assistant closed or anything else
/// started since (Back, another call). Nothing typed here is logged; the
/// password leaves only in `Credentials`. Every callback runs on the main
/// actor. Wire them, then call `start()`.
@MainActor
public final class EWayWizardController {
    public typealias Page = CRM.WizardPage
    public typealias Field = CRM.WizardField

    /// The RPC budgets (eway_flow.go): account.detectEway asks the instance
    /// one anonymous question; account.test signs in once (the daemon allows
    /// itself 45 s); account.add and account.update do both again and store
    /// the password.
    public static let detectTimeout: Duration = .seconds(15)
    public static let testTimeout: Duration = .seconds(50)
    public static let saveTimeout: Duration = .seconds(60)

    // MARK: Outputs

    /// The page stack changed; the last page is the visible one.
    public var onPages: (@MainActor ([Page]) -> Void)?
    /// A call started, with its progress text, or finished (nil).
    public var onBusy: (@MainActor (String?) -> Void)?
    /// What the address field allows now: Next, and the text under it
    /// ("" for none; `CRM.checkServiceInput`).
    public var onServiceCheck: (@MainActor (_ ok: Bool, _ problem: String) -> Void)?
    /// The name field's text (the default name once the address was looked
    /// up, while the user did not type one).
    public var onName: (@MainActor (String) -> Void)?
    /// A page's banner; nil hides it.
    public var onBanner: (@MainActor (Page, String?) -> Void)?
    /// The field to flag; `.none` clears the flag.
    public var onProblem: (@MainActor (Field) -> Void)?
    /// Move the keyboard focus to a field.
    public var onFocus: (@MainActor (Field) -> Void)?
    /// The account was stored; the UI closes the assistant.
    public var onDone: (@MainActor (AccountID, AccountConfig) -> Void)?

    // MARK: State

    public let client: RPCClient
    /// The account being edited; nil when adding one.
    public let editing: Account?
    public let texts = CRM.wizardTexts()

    public private(set) var pages: [Page]
    public private(set) var closed = false
    public private(set) var op = 0
    /// The progress text of the call under way; nil when none runs.
    public private(set) var progress: String?

    /// The address field as typed.
    public private(set) var serviceInput = ""
    /// What account.detectEway found for `serviceInput`; nil until it
    /// answered and again once the address changed. When editing, the
    /// account's service.
    public private(set) var service: AccountDetectEwayResult?
    public private(set) var name = ""
    public private(set) var user = ""
    /// The check box "Also save later messages of saved conversations"
    /// (`EwayConfig.followThreads`, docs/eway.md D22): on for a new
    /// account, the account's value when editing.
    public private(set) var followThreads = true
    private var password = ""
    private var nameTyped = false
    private var problem: Field = .none
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "ewaywizard")

    /// `editing` opens on the sign-in page and saves with account.update.
    public init(client: RPCClient, editing: Account? = nil) {
        self.client = client
        self.editing = editing
        guard let a = editing else {
            pages = [.service]
            return
        }
        pages = [.credentials]
        name = a.config.name
        nameTyped = true
        if let e = a.config.eway {
            serviceInput = e.serviceUrl
            service = AccountDetectEwayResult(serviceUrl: e.serviceUrl)
            user = e.username
            followThreads = e.followThreads
        } else {
            followThreads = false
        }
    }

    // MARK: Presentation

    public var isEditing: Bool { editing != nil }

    /// The window's title.
    public var title: String { isEditing ? texts.editTitle : texts.title }

    /// A page's title in its header.
    public func pageTitle(_ page: Page) -> String {
        page == .credentials ? texts.credentialsTitle : texts.title
    }

    /// A page's button, with its mnemonic: Next on the address, Add
    /// Account on the sign-in, Save when editing.
    public func nextLabel(_ page: Page) -> String {
        if page == .service {
            return texts.next
        }
        return isEditing ? texts.save : texts.add
    }

    /// The password field's label: optional when editing.
    public var passwordLabel: String { isEditing ? texts.newPasswordLabel : texts.passwordLabel }

    /// The address the sign-in page shows (read-only there).
    public var serviceURL: String { service?.serviceUrl ?? "" }

    public var canGoBack: Bool { pages.count > 1 }
    public var top: Page { pages.last ?? .service }
    public var busy: Bool { progress != nil }

    private var trimmedUser: String { Jira.trimSpace(user) }
    private var editedUser: String { Jira.trimSpace(editing?.config.eway?.username ?? "") }
    private var userChanged: Bool { editing == nil || trimmedUser != editedUser }
    private var credentials: Credentials { Credentials(password: password) }

    // MARK: Lifecycle

    /// Delivers the initial state to the callbacks. Call once.
    public func start() {
        let check = CRM.checkServiceInput(serviceInput)
        onServiceCheck?(check.ok, check.problem)
        onName?(name)
        onPages?(pages)
    }

    /// The assistant went away: every late reply is dropped from now on.
    public func close() {
        closed = true
        progress = nil
    }

    // MARK: Inputs from the UI

    /// The address field as typed. A changed address forgets what was
    /// found for the previous one.
    public func setService(_ raw: String) {
        guard !isEditing, raw != serviceInput else { return }
        serviceInput = raw
        let check = CRM.checkServiceInput(raw)
        onServiceCheck?(check.ok, check.problem)
        clear(.service)
        onBanner?(.service, nil)
        service = nil
    }

    /// The name field as typed; once typed, the default no longer
    /// replaces it.
    public func setName(_ s: String) {
        guard s != name else { return }
        name = s
        nameTyped = true
    }

    /// The user name and password as typed.
    public func setCredentials(user: String, password: String) {
        guard user != self.user || password != self.password else { return }
        if user != self.user {
            clear(.user)
        }
        if password != self.password {
            clear(.password)
        }
        self.user = user
        self.password = password
        onBanner?(.credentials, nil)
    }

    /// The check box as clicked; it goes with the configuration on
    /// account.test, account.add and account.update.
    public func setFollowThreads(_ on: Bool) {
        followThreads = on
    }

    /// The visible page's button.
    public func next() {
        guard !closed, !busy else { return }
        if top == .service {
            detect()
        } else {
            test()
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

    // MARK: Navigation and fields

    private func push(_ p: Page) {
        if let i = pages.firstIndex(of: p) {
            pages.removeSubrange((i + 1)...)
        } else {
            pages.append(p)
        }
        onPages?(pages)
    }

    private func popTo(_ p: Page) {
        guard let i = pages.firstIndex(of: p), i + 1 < pages.count else { return }
        pages.removeSubrange((i + 1)...)
        onPages?(pages)
    }

    private func setBusy(_ text: String?) {
        guard progress != text else { return }
        progress = text
        onBusy?(text)
    }

    private func flag(_ field: Field) {
        problem = field
        onProblem?(field)
        if field != .none {
            onFocus?(field)
        }
    }

    private func clear(_ field: Field) {
        guard problem == field, field != .none else { return }
        problem = .none
        onProblem?(.none)
    }

    /// A failed step (`CRM.failureOf`): back to its page with the banner,
    /// the client's sentence for the error when the step has none, the
    /// field flagged and focused.
    private func fail(_ step: CRM.WizardStep, _ error: any Error) {
        let f = CRM.failureOf(step, error, editing: isEditing)
        let code = (error as? RPCError)?.code.rawValue ?? 0
        log.info("eway assistant step \(step.rawValue, privacy: .public) failed: code \(code, privacy: .public)")
        popTo(f.page)
        onBanner?(f.page, f.banner.isEmpty ? rpcErrorText(f.what, error) : f.banner)
        flag(f.field)
    }

    // MARK: Steps

    /// account.detectEway for the typed address; http is refused before
    /// anything is asked.
    private func detect() {
        let check = CRM.checkServiceInput(serviceInput)
        guard check.ok else {
            onServiceCheck?(check.ok, check.problem)
            flag(.service)
            return
        }
        onBanner?(.service, nil)
        let params = AccountDetectEwayParams(serviceUrl: Jira.trimSpace(serviceInput))
        setBusy(texts.contacting)
        op += 1
        let op = self.op
        let client = client
        Task { [weak self] in
            let outcome: Result<AccountDetectEwayResult, any Error>
            do {
                outcome = .success(try await client.call(API.AccountDetectEway.self, params, timeout: Self.detectTimeout))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed, op == self.op else { return }
            self.setBusy(nil)
            self.detected(outcome)
        }
    }

    private func detected(_ outcome: Result<AccountDetectEwayResult, any Error>) {
        let res: AccountDetectEwayResult
        switch outcome {
        case .failure(let error):
            fail(.detect, error)
            return
        case .success(let r):
            res = r
        }
        if res.serviceUrl.isEmpty {
            fail(.detect, RPCError(code: .serverError, message: ""))
            return
        }
        if res.microsoftSignIn {
            fail(.detect, RPCError(
                code: .crmSignInUnsupported, message: "",
                data: .object(["reason": .string(CrmSignInReason.microsoft.rawValue)])))
            return
        }
        log.info("eway instance found")
        service = res
        if !nameTyped {
            name = CRM.defaultName(res.serviceUrl)
            onName?(name)
        }
        push(.credentials)
        onFocus?(trimmedUser.isEmpty ? .user : .password)
    }

    /// The account's configuration from the pages: the edited account's
    /// with the new name and user name (its user GUID forgotten when the
    /// user changed), or a new eway account.
    func config() -> AccountConfig {
        let n = Jira.trimSpace(name)
        if let editing {
            var cfg = editing.config
            cfg.name = n
            var e = cfg.eway ?? EwayConfig(serviceUrl: "", username: "")
            if userChanged {
                e.userGuid = nil
            }
            e.username = trimmedUser
            e.followThreads = followThreads
            cfg.eway = e
            return cfg
        }
        return AccountConfig(
            name: n, email: "", kind: .eway,
            eway: EwayConfig(serviceUrl: serviceURL, username: trimmedUser, followThreads: followThreads))
    }

    /// account.test with the typed credentials (the stored password when
    /// editing and it is left empty), then store.
    private func test() {
        guard service != nil else {
            popTo(.service)
            return
        }
        if trimmedUser.isEmpty {
            flag(.user)
            return
        }
        if password.isEmpty, userChanged {
            onBanner?(.credentials, texts.passwordPrompt)
            flag(.password)
            return
        }
        onBanner?(.credentials, nil)
        let cfg = config()
        let params = AccountTestParams(accountId: editing?.id, config: cfg, credentials: credentials)
        setBusy(texts.signingIn)
        op += 1
        let op = self.op
        let client = client
        Task { [weak self] in
            var failure: (any Error)?
            do {
                let res = try await client.call(API.AccountTest.self, params, timeout: Self.testTimeout)
                if let e = res.eway {
                    if !e.ok {
                        failure = e.error ?? RPCError(code: .serverError, message: "")
                    }
                } else {
                    failure = RPCError(code: .serverError, message: "")
                }
            } catch {
                failure = error
            }
            guard let self, !self.closed, op == self.op else { return }
            if let failure {
                self.setBusy(nil)
                self.fail(.test, failure)
                return
            }
            self.store(cfg)
        }
    }

    /// account.add, or account.update when editing.
    private func store(_ cfg: AccountConfig) {
        let creds = credentials
        let editing = editing
        setBusy(editing == nil ? texts.adding : texts.saving)
        op += 1
        let op = self.op
        let client = client
        Task { [weak self] in
            let outcome: Result<AccountID, any Error>
            do {
                if let editing {
                    _ = try await client.call(
                        API.AccountUpdate.self, AccountUpdateParams(accountId: editing.id, config: cfg, credentials: creds),
                        timeout: Self.saveTimeout)
                    outcome = .success(editing.id)
                } else {
                    let res = try await client.call(
                        API.AccountAdd.self, AccountAddParams(config: cfg, credentials: creds), timeout: Self.saveTimeout)
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
                self.log.info("eway account saved: \(id.rawValue, privacy: .public), edit \(editing != nil)")
                self.onDone?(id, cfg)
            }
        }
    }
}
