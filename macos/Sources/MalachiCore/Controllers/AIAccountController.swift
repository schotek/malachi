// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The dialog that adds or edits an AI provider account (kind `ai`,
/// docs/api.md §4.1; the design is docs/plans/ai-api.md §7.1) with the
/// widgets replaced by callbacks: a 1:1 port of ui/internal/aiaccount
/// controller.go (`Controller`); its texts and rules are `AIAccount`.
///
/// The provider is fixed (the catalogue entry on Settings → Integrations
/// chose it, and an existing account keeps its own); the dialog edits the
/// name, the server address of an OpenAI-compatible server with the
/// examples under it and a warning for http outside a loopback address
/// (D8), the API key (write-only: an existing account shows "Key stored ·
/// Replace Key…"), Test (account.test, which lists the provider's models
/// and says what went wrong), the model to pick from them or type, the
/// board's model, the certificate pin of an https server, and the consent
/// to sending mail to the provider (D7, Q17), which Save needs.
///
/// Everything typed is the form until Save, which is account.add (a new
/// account, with the key as credentials.password and the consent) or
/// account.update (the edited account, with credentials only when a new
/// key was typed, and the consent only when it was given now). Test is
/// account.test with the form, or with the account's stored key when
/// editing without a replacement. The form is checked here, by the
/// daemon's rules, before any call. Every reply is dropped once the dialog
/// closed or another call started since (`op`). Nothing typed here is
/// logged.
///
/// Every callback runs on the main actor. Wire them, then call `start()`.
@MainActor
public final class AIAccountController {
    /// aiaccount.Env: what the controller runs with besides the daemon.
    /// `errorText` is the client's general sentence for a failed call,
    /// with the step's action ("Testing the connection") as what; the
    /// tests pass a stand-in.
    public struct Env {
        public var errorText: @MainActor (String, any Error) -> String

        public init(errorText: @escaping @MainActor (String, any Error) -> String = { rpcErrorText($0, $1) }) {
            self.errorText = errorText
        }
    }

    /// aiaccount.Form: the dialog's fields as typed. `key` is the API key
    /// field, empty when nothing was typed (an existing account then keeps
    /// its stored key); `certificateSHA256` the pin of an https server;
    /// `consent` the check box.
    public struct Form: Sendable, Equatable {
        public var name = ""
        public var baseURL = ""
        public var key = ""
        public var model = ""
        public var boardModel = ""
        public var certificateSHA256 = ""
        public var consent = false

        public init() {}
    }

    /// aiaccount.TestOutcome: what the last Test said: whether the
    /// provider answered, the status line, and the TLS problem of a failed
    /// https connection whose certificate the user may trust (nil
    /// otherwise).
    public struct TestOutcome: Sendable, Equatable {
        public var ok = false
        public var text = ""
        public var trust: CertTrust.Problem?

        public init(ok: Bool = false, text: String = "", trust: CertTrust.Problem? = nil) {
            self.ok = ok
            self.text = text
            self.trust = trust
        }
    }

    // MARK: Outputs

    /// What the dialog shows changed (the form's rules, the models, the
    /// test's outcome, a problem): the UI reads the controller again.
    public var onChange: (@MainActor () -> Void)?
    /// A call started, with its progress text, or finished (nil).
    public var onBusy: (@MainActor (String?) -> Void)?
    /// The dialog's banner; nil hides it.
    public var onBanner: (@MainActor (String?) -> Void)?
    /// The field to flag and focus; `.none` clears the flag.
    public var onProblem: (@MainActor (AIAccount.Field) -> Void)?
    /// The account was stored; the UI closes the dialog.
    public var onDone: (@MainActor (AccountID, AccountConfig) -> Void)?
    /// Save had nothing to store; the UI closes the dialog.
    public var onClose: (@MainActor () -> Void)?

    // MARK: State

    public let client: RPCClient
    /// The fixed texts; the consent of the provider.
    public let texts = AIAccount.texts()
    public let consent: AIAccount.ConsentStrings

    /// The provider kind.
    public let provider: AIProvider
    /// The account being edited; nil when adding one.
    public let account: Account?
    /// The fields as typed.
    public private(set) var form = Form()
    /// The provider's models to pick from: the last test's, else what the
    /// daemon listed for an existing account; empty before either.
    public private(set) var models: [AIModel] = []
    /// What the last Test said; nil before one, and again once the server
    /// address or the key changed.
    public private(set) var test: TestOutcome?
    /// The progress text of the call under way; nil when none runs.
    public private(set) var progress: String?
    /// account.add or account.update is under way: the dialog waits.
    public private(set) var saving = false
    public private(set) var closed = false
    /// The field flagged, `.none` for none.
    public private(set) var problem: AIAccount.Field = .none
    /// The banner shown, nil for none.
    public private(set) var banner: String?
    /// Bumped per RPC so that stale replies bail out.
    public private(set) var op = 0

    private let env: Env
    /// An existing account's key field is open (Replace Key…).
    private var replacing = false
    private var shownBanner: String?
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "aiaccount")

    /// RPC budgets (docs/api.md §4.1): account.test asks the provider for
    /// its models once; account.add does the same and stores the key, so
    /// the keyring may prompt; assistant.providerModels answers from the
    /// daemon's cache or asks once.
    static let testTimeout: Duration = RPCTimeouts.test
    static let saveTimeout: Duration = .seconds(60)
    static let modelsTimeout: Duration = RPCTimeouts.assistantModels

    /// aiaccount.NewController: adding an account of `provider` when
    /// `editing` is nil, else editing that account (its provider wins).
    public init(client: RPCClient, provider: AIProvider, editing: Account? = nil, env: Env = Env()) {
        self.client = client
        self.env = env
        var p = provider
        account = editing
        if let a = editing {
            form.name = a.config.name
            if let ai = a.config.ai {
                p = ai.provider
                form.baseURL = ai.baseUrl
                form.model = ai.model
                form.boardModel = ai.boardModel
                form.certificateSHA256 = ai.certificateSha256 ?? ""
            }
        }
        if !p.isValid {
            p = .openaiCompatible
        }
        self.provider = p
        consent = AIAccount.consentTexts(for: p)
    }

    // MARK: Presentation

    /// The dialog edits an existing account.
    public var isEditing: Bool { account != nil }

    /// The name of the provider kind.
    public var providerName: String { Assistant.providerName(provider) }

    /// The dialog's title.
    public var title: String { isEditing ? texts.editTitle : texts.addTitle }

    /// The primary button: Add Account, or Save when editing.
    public var saveLabel: String { isEditing ? texts.save : texts.add }

    /// A provider with a server address of its own (the OpenAI-compatible
    /// server); the named providers have none.
    public var usesServer: Bool { provider == .openaiCompatible }

    /// A provider that may have no key (a local server).
    public var keyOptional: Bool { provider == .openaiCompatible }

    /// The key row of an existing account shows "Key stored" with Replace
    /// Key…, instead of the field.
    public var keyStored: Bool { isEditing && !replacing }

    /// The key field's label: New API Key while replacing, API Key
    /// (optional) for a server, else API Key.
    public var keyLabel: String {
        if isEditing {
            return texts.newKeyLabel
        }
        if keyOptional {
            return texts.keyOptionalLabel
        }
        return texts.keyLabel
    }

    /// The TLS problem of the last test whose certificate the dialog may
    /// offer to trust (an https server whose certificate was refused or
    /// changed); nil otherwise. `trust()` takes it.
    public var trustOffer: CertTrust.Problem? { test?.trust }

    /// A certificate pin in the form.
    public var pinned: Bool { !form.certificateSHA256.isEmpty }

    /// The pinned fingerprint as shown ("AB12 CD34 …").
    public var pinText: String { CertTrust.formatFingerprint(form.certificateSHA256) }

    /// The host of the address the account talks to: the provider's fixed
    /// one, or the server's as typed ("" while it is not an address).
    public var host: String {
        guard usesServer else { return fixedHost }
        return AIAccount.hostOf(form.baseURL)
    }

    private var fixedHost: String {
        AIAccount.hostOf(AIConfig(provider: provider, model: "").effectiveBaseURL)
    }

    /// The warning under the server address: plain http to a host that is
    /// not a loopback address (docs/plans/ai-api.md D8).
    public var httpWarning: Bool {
        guard usesServer, let u = AIAccount.normalizeBaseURL(form.baseURL) else { return false }
        return u.hasPrefix("http://") && !AIAccount.isLoopback(AIAccount.hostOf(u))
    }

    /// The warning's text; "" when there is none.
    public var httpWarningText: String {
        httpWarning ? AIAccount.httpWarning(host: host) : ""
    }

    /// The consent check box is offered: a new account, or an existing one
    /// without the current consent text's version.
    public var needsConsent: Bool {
        guard let a = account, let ai = a.config.ai else { return true }
        return !ai.consented
    }

    /// The consent's text, with the http warning added for a server
    /// reached over plain http.
    public var consentBody: String {
        var body = consent.body
        let w = httpWarningText
        if !w.isEmpty {
            body += " " + w
        }
        return body
    }

    /// A call is under way.
    public var busy: Bool { progress != nil }

    /// Test is offered: no call runs.
    public var canTest: Bool { !busy }

    /// Save is offered: no call runs, and the provider was reached.
    public var canSave: Bool { !busy && connected }

    /// The provider was reached with the form's key and server: a test of
    /// this dialog that succeeded (a changed key or server takes it back),
    /// or an existing account kept on its stored key and server, which
    /// connected when it was added. Until then only the name, the server,
    /// the key, the pin and Test are available:
    /// the models come from the test, so the model, the
    /// board's model, the consent and Save wait for it (`unlocked`).
    public var connected: Bool {
        if let test, test.ok {
            return true
        }
        guard keyStored, let ai = account?.config.ai else { return false }
        return AIAccount.hostOf(form.baseURL) == AIAccount.hostOf(ai.baseUrl)
    }

    /// The model group and the consent are available: connected and not
    /// saving.
    public var unlocked: Bool { connected && !saving }

    /// The form differs from the stored account in what the daemon acts
    /// on: the name, the server, the models, the pin, a new key, or a
    /// consent given now. Always true when adding.
    public var isChanged: Bool {
        guard let a = account else { return true }
        if replacing, !form.key.isEmpty {
            return true
        }
        if needsConsent, form.consent {
            return true
        }
        let old = a.config
        let cfg = config()
        if Jira.trimSpace(old.name) != cfg.name {
            return true
        }
        guard var x = old.ai, var y = cfg.ai else { return true }
        x.consent = nil
        y.consent = nil
        return x != y
    }

    /// aiaccount.Check: the first problem of the form by the daemon's
    /// rules (docs/api.md §4.1), or `.none` and "": the name, the server
    /// address (with https for a pin), the key (needed for a named
    /// provider, when adding or replacing), the models, and for Save the
    /// consent.
    public func check(forSave: Bool) -> (field: AIAccount.Field, problem: String) {
        check(forSave: forSave, forTest: false)
    }

    /// `check`; `forTest` is the Test button, before which the model may
    /// still be empty: the test is what lists the models to choose from
    /// (account.test accepts an empty model, an invalid one it refuses).
    private func check(forSave: Bool, forTest: Bool) -> (field: AIAccount.Field, problem: String) {
        let s = texts
        if Jira.trimSpace(form.name).isEmpty {
            return (.name, s.nameMissing)
        }
        if usesServer {
            let raw = Jira.trimSpace(form.baseURL)
            if raw.isEmpty {
                return (.server, s.serverMissing)
            }
            guard let u = AIAccount.normalizeBaseURL(raw) else {
                return (.server, s.serverInvalid)
            }
            if !form.certificateSHA256.isEmpty, !u.hasPrefix("https://") {
                return (.server, s.pinNeedsHTTPS)
            }
        }
        if !keyOptional, !isEditing || replacing, Jira.trimSpace(form.key).isEmpty {
            return (.key, s.keyMissing)
        }
        switch AIAccount.checkModel(form.model) {
        case .missing:
            if !forTest {
                return (.model, s.modelMissing)
            }
        case .invalid:
            return (.model, s.modelInvalid)
        case .ok:
            break
        }
        if !Jira.trimSpace(form.boardModel).isEmpty, AIAccount.checkModel(form.boardModel) == .invalid {
            return (.boardModel, s.modelInvalid)
        }
        if forSave, needsConsent, !form.consent {
            return (.consent, s.consentMissing)
        }
        return (.none, "")
    }

    // MARK: Lifecycle

    /// Delivers the initial state and, for an existing account, asks the
    /// daemon for the provider's models. Call it once, after setting the
    /// callbacks.
    public func start() {
        changed()
        if account != nil {
            loadModels()
        }
    }

    /// The dialog went away: every late reply is dropped from now on.
    public func close() {
        closed = true
        progress = nil
        saving = false
    }

    // MARK: Inputs from the UI

    /// The account's name as typed.
    public func setName(_ v: String) {
        guard !saving, v != form.name else { return }
        form.name = v
        edited(.name)
    }

    /// The server address as typed; the last test is about another server
    /// now, and a pin of another host goes.
    public func setBaseURL(_ v: String) {
        guard !saving, v != form.baseURL else { return }
        let oldHost = AIAccount.hostOf(form.baseURL)
        let newHost = AIAccount.hostOf(v)
        form.baseURL = v
        if !form.certificateSHA256.isEmpty, oldHost != newHost {
            form.certificateSHA256 = ""
        }
        test = nil
        edited(.server)
    }

    /// The key as typed; the last test is about another key now.
    public func setKey(_ v: String) {
        guard !saving, v != form.key else { return }
        form.key = v
        test = nil
        edited(.key)
    }

    /// The model as typed or picked.
    public func setModel(_ v: String) {
        guard !saving, v != form.model else { return }
        form.model = v
        edited(.model)
    }

    /// The board's model as typed or picked.
    public func setBoardModel(_ v: String) {
        guard !saving, v != form.boardModel else { return }
        form.boardModel = v
        edited(.boardModel)
    }

    /// The consent check box.
    public func setConsent(_ on: Bool) {
        guard !saving, on != form.consent else { return }
        form.consent = on
        edited(.consent)
    }

    /// Replace Key… of an existing account: the key field opens, empty.
    public func replaceKey() {
        guard !saving, isEditing, !replacing else { return }
        replacing = true
        form.key = ""
        test = nil
        edited(.key)
    }

    /// Takes a replacement back: the stored key stays, the field closes.
    public func keepKey() {
        guard !saving, replacing else { return }
        replacing = false
        form.key = ""
        test = nil
        edited(.key)
    }

    /// Pins the certificate the last test refused (`trustOffer`), after
    /// the user confirmed it with its fingerprint.
    public func trust() {
        guard !saving, let p = trustOffer, let cert = p.cert else { return }
        form.certificateSHA256 = cert.sha256
        test = nil
        edited(.server)
    }

    /// Drops the pin.
    public func forgetPin() {
        guard !saving, !form.certificateSHA256.isEmpty else { return }
        form.certificateSHA256 = ""
        test = nil
        edited(.server)
    }

    /// account.test with the form: the provider's models and the status.
    /// An existing account without a replaced key is tested with its
    /// stored key. The model may still be empty: the test lists the models.
    public func runTest() {
        guard !closed, !busy else { return }
        let (field, problem) = check(forSave: false, forTest: true)
        if field != .none {
            fail(field, problem)
            return
        }
        test = nil
        clearProblem()
        let params = AccountTestParams(accountId: account?.id, config: config(), credentials: credentials())
        setBusy(texts.testing)
        changed()
        op += 1
        let op = self.op
        let client = client
        Task { [weak self] in
            let outcome: Result<AccountTestResult, any Error>
            do {
                outcome = .success(try await client.call(API.AccountTest.self, params, timeout: Self.testTimeout))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed, op == self.op else { return }
            self.setBusy(nil)
            switch outcome {
            case .success(let res): self.tested(res.ai, nil)
            case .failure(let error): self.tested(nil, error)
            }
            self.changed()
        }
    }

    /// Reads what account.test answered.
    private func tested(_ res: AITestResult?, _ error: (any Error)?) {
        if error == nil, let res, res.ok {
            models = res.models
            test = TestOutcome(ok: true, text: texts.connected(res.models.count))
            log.info("ai account tested: \(res.models.count, privacy: .public) models")
            return
        }
        var error = error
        if error == nil {
            guard let res, let e = res.error else {
                test = TestOutcome(text: texts.unreachable)
                return
            }
            error = e
        }
        guard let error else { return }
        let f = failure(texts.testing, error)
        var out = TestOutcome(text: f.banner)
        out.trust = trustable(error)
        test = out
        flag(f.field)
    }

    /// The TLS problem of a failed test the dialog may offer to trust: an
    /// https server (only its host may be pinned), a certificate the system
    /// refused or one other than the pinned (never a connection failure),
    /// and not the pinned one.
    private func trustable(_ error: any Error) -> CertTrust.Problem? {
        guard usesServer, let e = error as? RPCError, let u = AIAccount.normalizeBaseURL(form.baseURL), u.hasPrefix("https://"),
              let p = CertTrust.details(e), let cert = p.cert, p.category != .connection else { return nil }
        if let pinned = normalizeCertificateSHA256(form.certificateSHA256), pinned == cert.sha256 {
            return nil
        }
        return p
    }

    /// account.add with the form, the key and the consent, or
    /// account.update of the edited account (credentials only with a new
    /// key, the consent only when given now); nothing (`onClose`) when an
    /// edited form changes nothing.
    public func save() {
        guard !closed, !busy, connected else { return }
        let (field, problem) = check(forSave: true)
        if field != .none {
            fail(field, problem)
            return
        }
        var cfg = config()
        if needsConsent, form.consent {
            cfg.ai?.consent = AIConsent(version: API.Limits.assistantConsentVersion)
        }
        if isEditing, !isChanged {
            onClose?()
            return
        }
        clearProblem()
        saving = true
        changed()
        op += 1
        let op = self.op
        let editing = account
        let creds = credentials()
        let what = editing == nil ? texts.adding : texts.saving
        setBusy(what)
        let client = client
        Task { [weak self] in
            var id: AccountID = editing?.id ?? ""
            var failure: (any Error)?
            do {
                if let editing {
                    _ = try await client.call(
                        API.AccountUpdate.self, AccountUpdateParams(accountId: editing.id, config: cfg, credentials: creds),
                        timeout: Self.saveTimeout)
                } else {
                    let res = try await client.call(
                        API.AccountAdd.self, AccountAddParams(config: cfg, credentials: creds), timeout: Self.saveTimeout)
                    id = res.accountId
                }
            } catch {
                failure = error
            }
            guard let self, !self.closed, op == self.op else { return }
            self.saving = false
            self.setBusy(nil)
            if let failure {
                let f = self.failure(what, failure)
                self.fail(f.field, f.banner)
                self.changed()
                return
            }
            // The key is write-only: it leaves the form once stored.
            self.form.key = ""
            self.replacing = false
            self.log.info("ai account saved: provider \(self.provider.rawValue, privacy: .public)")
            self.onDone?(id, cfg)
        }
    }

    // MARK: Internals

    /// The account configuration of the form: the edited account's with
    /// the name and the AI part replaced, or a new one.
    func config() -> AccountConfig {
        var cfg = account?.config ?? AccountConfig(name: "", email: "")
        cfg.kind = .ai
        cfg.name = Jira.trimSpace(form.name)
        var ai = AIConfig(provider: provider, model: Jira.trimSpace(form.model), boardModel: Jira.trimSpace(form.boardModel))
        if usesServer {
            if let u = AIAccount.normalizeBaseURL(form.baseURL) {
                ai.baseUrl = u
            }
            ai.certificateSha256 = form.certificateSHA256.isEmpty ? nil : form.certificateSHA256
        }
        if let stored = account?.config.ai {
            ai.consent = stored.consent
        }
        cfg.ai = ai
        return cfg
    }

    /// The key as typed when it goes with the call: always when adding,
    /// when editing only as a replacement.
    private func credentials() -> Credentials {
        if isEditing, !replacing {
            return Credentials()
        }
        return Credentials(password: Jira.trimSpace(form.key))
    }

    /// assistant.providerModels for the edited account, so that the model
    /// row has the provider's models without a test; a failure leaves the
    /// row to the typed id.
    private func loadModels() {
        guard let a = account else { return }
        op += 1
        let op = self.op
        let params = AssistantProviderModelsParams(accountId: a.id)
        let client = client
        Task { [weak self] in
            let res = try? await client.call(API.AssistantProviderModels.self, params, timeout: Self.modelsTimeout)
            guard let self, !self.closed, op == self.op, let res, self.test == nil else { return }
            self.models = res.models
            self.changed()
        }
    }

    /// The banner and field of a failed call: the dialog's own sentence
    /// (`AIAccount.failureOf`), else the client's for the error. Only the
    /// code is logged: the error may carry text from the provider.
    private func failure(_ what: String, _ error: any Error) -> AIAccount.Failure {
        let code = (error as? RPCError)?.code.rawValue ?? 0
        log.info("ai account call failed: \(what, privacy: .public), code \(code, privacy: .public)")
        var f = AIAccount.failureOf(error)
        if f.banner.isEmpty {
            f.banner = env.errorText(what, error)
        }
        if f.field == .server, !usesServer {
            f.field = .none
        }
        return f
    }

    /// Shows a problem: the banner and the flagged field.
    private func fail(_ field: AIAccount.Field, _ banner: String) {
        self.banner = banner
        flag(field)
        showBanner()
        changed()
    }

    /// A field changed, so the problem of that field and the banner are
    /// about another form.
    private func edited(_ field: AIAccount.Field) {
        if problem == field || banner != nil {
            clearProblem()
        }
        changed()
    }

    private func clearProblem() {
        banner = nil
        flag(.none)
        showBanner()
    }

    private func flag(_ field: AIAccount.Field) {
        guard problem != field else { return }
        problem = field
        onProblem?(field)
    }

    private func changed() {
        onChange?()
    }

    private func setBusy(_ text: String?) {
        guard progress != text else { return }
        progress = text
        onBusy?(text)
    }

    /// Tells the UI the banner when it changed.
    private func showBanner() {
        guard shownBanner != banner else { return }
        shownBanner = banner
        onBanner?(banner)
    }
}
