// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The flow of ui/internal/accountwizard/wizard.go against a fake daemon.

// One line: the transport is newline-delimited.
private let discoveredJSON = [
    #"{"config":{"name":"Example","email":"me@example.com","#,
    #""imap":{"host":"imap.example.com","port":993,"security":"tls","username":"me@example.com","authMethod":"password"},"#,
    #""smtp":{"host":"smtp.example.com","port":587,"security":"starttls","username":"me@example.com","authMethod":"password"}},"#,
    #""source":"ispdb"}"#,
].joined()

private let testOKJSON = #"{"imap":{"ok":true,"latencyMs":12},"smtp":{"ok":true,"latencyMs":5}}"#
private let testAuthFailedJSON = #"{"imap":{"ok":false,"error":{"code":1201,"message":"bad"},"latencyMs":0},"smtp":{"ok":true,"latencyMs":5}}"#

// The browser sign-in (oauth.go): a Google address without GNOME Online
// Accounts, discovered as the daemon's own sign-in with the app password as
// the alternative, and a Microsoft 365 one without any.
private let gmailOAuthConfigJSON = [
    #"{"name":"me@gmail.com","email":"me@gmail.com","#,
    #""imap":{"host":"imap.gmail.com","port":993,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},"#,
    #""smtp":{"host":"smtp.gmail.com","port":465,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},"#,
    #""oauth2":{"source":"daemon","provider":"google"}}"#,
].joined()
private let gmailPasswordConfigJSON = [
    #"{"name":"me@gmail.com","email":"me@gmail.com","#,
    #""imap":{"host":"imap.gmail.com","port":993,"security":"tls","username":"me@gmail.com","authMethod":"password"},"#,
    #""smtp":{"host":"smtp.gmail.com","port":465,"security":"tls","username":"me@gmail.com","authMethod":"password"}}"#,
].joined()
private let gmailDiscoveredJSON = #"{"source":"provider","providerName":"Google","config":\#(gmailOAuthConfigJSON),"alternatives":[\#(gmailPasswordConfigJSON)]}"#
private let graphOAuthConfigJSON = #"{"name":"me@contoso.com","email":"me@contoso.com","kind":"graph","graph":{"source":"daemon"},"oauth2":{"source":"daemon","provider":"office365"}}"#
private let graphDiscoveredJSON = #"{"source":"provider","providerName":"Microsoft 365","config":\#(graphOAuthConfigJSON)}"#
private let authURL = "https://accounts.google.com/o/oauth2/v2/auth?state=x"
private let oauthStartJSON = #"{"sessionId":"s_1","authUrl":"https://accounts.google.com/o/oauth2/v2/auth?state=x","expiresAt":"2026-09-25T10:10:00Z"}"#
private let oauthCompleteJSON = #"{"status":"complete","config":\#(gmailOAuthConfigJSON)}"#

private let googlePrompt = WizardController.OAuthView.prompt(
    description: "Your browser will open so you can sign in to Google. Malachi Mail never sees your password; it only receives permission to read and send your mail.",
    signInLabel: "Sign In with Google")
private let microsoftPrompt = WizardController.OAuthView.prompt(
    description: "Your browser will open so you can sign in to Microsoft 365. Malachi Mail never sees your password; it only receives permission to read and send your mail.",
    signInLabel: "Sign In with Microsoft 365")

/// Counts calls, for handlers that answer differently the second time.
private actor Counter {
    var n = 0

    func next() -> Int {
        n += 1
        return n
    }
}

/// An account of the browser sign-in, as account.list returns it.
private func oauthAccount(id: AccountID = "acc-9", status: SyncStatus = .authRequired) -> Account {
    let oauth = ServerConfig(host: "imap.gmail.com", port: 993, security: .tls, username: "me@gmail.com", authMethod: .oauth2)
    let smtp = ServerConfig(host: "smtp.gmail.com", port: 465, security: .tls, username: "me@gmail.com", authMethod: .oauth2)
    return Account(
        id: id,
        config: AccountConfig(
            name: "Gmail", email: "me@gmail.com", displayName: "Me", imap: oauth, smtp: smtp,
            oauth2: OAuth2Config(source: .daemon, provider: .google)
        ),
        enabled: true,
        state: SyncState(accountId: id, status: status)
    )
}

/// Records the parameters each method was called with.
private actor ParamsLog {
    var byMethod: [String: [Data]] = [:]

    func record(_ method: String, _ params: Data) {
        byMethod[method, default: []].append(params)
    }

    func last<T: Decodable>(_ method: String, as type: T.Type) throws -> T? {
        guard let data = byMethod[method]?.last else { return nil }
        return try JSONCoding.decoder().decode(type, from: data)
    }
}

/// Collects what the controller reports.
@MainActor
private final class Recorder {
    var pages: [[WizardController.WizardPage]] = []
    var busy: [Bool] = []
    var identityProblems: [IdentityProblems] = []
    var banners: [String?] = []
    var serverProblems: [ServerProblems] = []
    var identities: [Identity] = []
    var applied: [AccountConfig] = []
    var linked: [[LinkedAccount]] = []
    var goaHints: [String] = []
    var goaHintBrowser: [Bool] = []
    var oauth: [WizardController.OAuthView] = []
    var starting: [Bool] = []
    var opened: [String] = []
    var testing: [WizardController.TestingView] = []
    var focus: [WizardController.IdentityField] = []
    var toasts: [String] = []
    var done: [AccountID] = []
    var doneConfigs: [AccountConfig] = []
    var pins: [[String]] = []
    var prompts: [TrustPrompt] = []
    /// What the trust confirmation answers.
    var trustAnswer = true

    func attach(_ w: WizardController) {
        w.onPages = { [unowned self] in self.pages.append($0) }
        w.onBusy = { [unowned self] in self.busy.append($0) }
        w.onIdentityProblems = { [unowned self] p, banner in
            self.identityProblems.append(p)
            self.banners.append(banner)
        }
        w.onServerProblems = { [unowned self] in self.serverProblems.append($0) }
        w.onIdentity = { [unowned self] in self.identities.append($0) }
        w.onApplyConfig = { [unowned self] in self.applied.append($0) }
        w.onLinked = { [unowned self] in self.linked.append($0) }
        w.onGOAHint = { [unowned self] text, browser in
            self.goaHints.append(text)
            self.goaHintBrowser.append(browser)
        }
        w.onOAuth = { [unowned self] in self.oauth.append($0) }
        w.onOAuthStarting = { [unowned self] in self.starting.append($0) }
        w.onOpenURL = { [unowned self] in self.opened.append($0) }
        w.onTesting = { [unowned self] in self.testing.append($0) }
        w.onFocus = { [unowned self] in self.focus.append($0) }
        w.onToast = { [unowned self] in self.toasts.append($0) }
        w.onDone = { [unowned self] id, cfg in
            self.done.append(id)
            self.doneConfigs.append(cfg)
        }
        w.onPins = { [unowned self] imap, smtp in self.pins.append([imap, smtp]) }
        w.onConfirmTrust = { [unowned self] prompt in
            self.prompts.append(prompt)
            return self.trustAnswer
        }
    }

    var lastResults: WizardController.ResultsView? {
        if case .results(let v)? = testing.last { return v }
        return nil
    }
}

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: @MainActor () async -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

/// A fake with linked accounts answered (empty, as on macOS) and started.
private func makeFake() async throws -> FakeDaemon {
    let fake = try FakeDaemon()
    await fake.on(API.AccountLinked.name) { _ in json(#"{"accounts":[]}"#) }
    try await fake.start()
    return fake
}

private func connect(_ fake: FakeDaemon) async throws -> RPCClient {
    let client = RPCClient(socketPath: fake.path)
    try await client.connect()
    return client
}

/// A started wizard whose linked accounts have loaded.
@MainActor
private func startWizard(_ fake: FakeDaemon, editing: Account? = nil, signIn: Bool = false) async throws -> (WizardController, Recorder) {
    let client = try await connect(fake)
    let w = WizardController(client: client, editing: editing, signIn: signIn)
    let rec = Recorder()
    rec.attach(w)
    w.start()
    try await waitUntil { !rec.linked.isEmpty }
    return (w, rec)
}

private func imapAccount(id: AccountID = "acc-9") -> Account {
    Account(
        id: id,
        config: AccountConfig(
            name: "Work", email: "me@example.com", displayName: "Me", kind: .imap,
            imap: ServerConfig(host: "imap.example.com", port: 993, security: .tls, username: "me", authMethod: .password),
            smtp: ServerConfig(host: "smtp.example.com", port: 465, security: .tls, username: "me", authMethod: .password)
        ),
        enabled: true,
        state: SyncState(accountId: id, status: .idle)
    )
}

// A server with its own certificate (a mail bridge): account.test refuses
// it with the certificate in the details (docs/api.md §2).
private let certA = String(repeating: "ab", count: 32)
private let certB = String(repeating: "0f", count: 32)
private let fingerprintA = Array(repeating: "ABAB", count: 16).joined(separator: " ")
private let fingerprintB = Array(repeating: "0F0F", count: 16).joined(separator: " ")

private func refusedJSON(_ sha: String, reason: String = "untrusted", expected: String? = nil) -> String {
    let expectedMember = expected.map { #""expectedSha256":"\#($0)","# } ?? ""
    return [
        #"{"ok":false,"latencyMs":3,"error":{"code":1303,"message":"x509: certificate signed by unknown authority","#,
        #""data":{"reason":"\#(reason)",\#(expectedMember)"certificate":{"sha256":"\#(sha)","subject":"127.0.0.1","issuer":"127.0.0.1","#,
        #""ipAddresses":["127.0.0.1"],"notBefore":"2024-01-02T03:04:05Z","notAfter":"2044-01-02T03:04:05Z","selfSigned":true}}}}"#,
    ].joined()
}

private func testRefusedJSON(imap: String?, smtp: String?) -> String {
    let ok = #"{"ok":true,"latencyMs":5}"#
    return #"{"imap":\#(imap.map { refusedJSON($0) } ?? ok),"smtp":\#(smtp.map { refusedJSON($0) } ?? ok)}"#
}

/// A password account whose IMAP endpoint pins `certA`.
private func pinnedAccount(id: AccountID = "acc-9") -> Account {
    var a = imapAccount(id: id)
    a.config.imap?.certificateSha256 = certA
    return a
}

@MainActor
@Suite(.serialized) struct WizardControllerTests {
    @Test func startsOnTheIdentityPageAndLoadsLinkedAccounts() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let (w, rec) = try await startWizard(fake)
        #expect(rec.pages == [[.identity]])
        #expect(rec.linked == [[]])
        #expect(rec.identities == [Identity()])
        #expect(!w.isEditing)
        #expect(w.title == "Add Account")
        #expect(w.passwordTitle == "Password")
        #expect(w.nextLabel == "Next")
        #expect(w.addLabel == "Add Account")
        #expect(w.addAnywayLabel == "Add Anyway")
        #expect(w.progressTitle == "Adding Account…")
        #expect(w.emailEditable && w.passwordVisible)
    }

    @Test func invalidAddressFlagsTheEmailRow() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "not an address", password: "pw")
        w.next()
        #expect(rec.identityProblems == [IdentityProblems(email: true)])
        #expect(rec.banners == [nil])
        #expect(rec.focus == [.email])
        #expect(rec.busy.isEmpty)
        #expect(await fake.calls == [API.AccountLinked.name])
        // Typing clears the flag.
        w.setIdentity(name: "", email: "me@example.com", password: "pw")
        #expect(rec.identityProblems.last == IdentityProblems())
        #expect(rec.banners.count == 2)
    }

    @Test func discoverySuccessFillsTheServersAndStartsTheTest() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in json(discoveredJSON) }
        await fake.on(API.AccountTest.name) { _ in
            try? await Task.sleep(for: .seconds(10))
            return json(testOKJSON)
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: " me@example.com ", password: "pw")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .servers, .testing] }
        try await waitUntil { await fake.calls.contains(API.AccountTest.name) }
        #expect(rec.busy == [true, false])
        #expect(rec.testing == [.progress(title: "Testing Connection…")])
        let cfg = try #require(rec.applied.last)
        #expect(cfg.imap?.host == "imap.example.com")
        #expect(cfg.smtp?.port == 587)
        #expect(cfg.email == "me@example.com")
        #expect(cfg.displayName == "Me")
        #expect(w.imap.host == "imap.example.com")
        #expect(w.accountName == "Example")
        #expect(rec.identityProblems.isEmpty)
    }

    @Test func discoveryFailureOpensTheServersPageWithAGuess() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in throw RPCError(code: .networkError, message: "no route") }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: "me@example.com", password: "pw")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .servers] }
        #expect(rec.busy == [true, false])
        let cfg = try #require(rec.applied.last)
        #expect(cfg.name == "example.com")
        #expect(cfg.imap?.host == "imap.example.com")
        #expect(cfg.imap?.port == 993)
        #expect(cfg.smtp?.host == "smtp.example.com")
        #expect(cfg.smtp?.security == .starttls)
        #expect(cfg.imap?.username == "me@example.com")
        #expect(w.smtp.port == 587)
        #expect(rec.testing.isEmpty)
        #expect(await !fake.calls.contains(API.AccountTest.name))
    }

    @Test func nothingFoundOpensTheServersPageWithAGuess() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in json(#"{"source":"none"}"#) }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "me@example.com", password: "pw")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .servers] }
        #expect(rec.applied.last?.imap?.host == "imap.example.com")
        #expect(rec.applied.last?.displayName == nil)
    }

    @Test func emptyPasswordIsAskedForAfterDiscovery() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in json(discoveredJSON) }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: "me@example.com", password: "")
        w.next()
        try await waitUntil { !rec.identityProblems.isEmpty }
        #expect(rec.identityProblems == [IdentityProblems(password: true)])
        #expect(rec.banners == ["Enter the password for this account"])
        #expect(rec.focus == [.password])
        #expect(rec.pages == [[.identity]])
        #expect(rec.busy == [true, false])
        #expect(rec.applied.isEmpty)
        #expect(await !fake.calls.contains(API.AccountTest.name))
        // The banner goes away as soon as the password changes.
        w.setIdentity(name: "Me", email: "me@example.com", password: "p")
        #expect(rec.identityProblems.last == IdentityProblems())
        #expect(rec.banners.last == .some(nil))
    }

    @Test func authFailureReturnsToTheIdentityPage() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in json(discoveredJSON) }
        await fake.on(API.AccountTest.name) { _ in json(testAuthFailedJSON) }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: "me@example.com", password: "wrong")
        w.next()
        try await waitUntil { rec.lastResults != nil }
        #expect(rec.pages.last == [.identity])
        #expect(rec.identityProblems.last == IdentityProblems(password: true))
        #expect(rec.banners.last == "The server rejected the user name or password")
        #expect(rec.focus.last == .password)
        let v = try #require(rec.lastResults)
        #expect(v.icon == "dialog-warning-symbolic")
        #expect(v.title == "Connection Failed")
        #expect(v.description == nil)
        #expect(v.imap == WizardController.EndpointRow(icon: "dialog-error-symbolic", text: "The server rejected the user name or password"))
        #expect(v.smtp == WizardController.EndpointRow(icon: "emblem-ok-symbolic", text: "Connected in 5 ms"))
        #expect(v.graph == nil)
        #expect(v.buttons == WizardController.WizardButtons(edit: true))
        #expect(w.lastOutcome == .authFailed)
    }

    @Test func testFailureOffersRetryAndAddAnyway() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in json(discoveredJSON) }
        await fake.on(API.AccountTest.name) { _ in throw RPCError(code: .serverTimeout, message: "slow") }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: "me@example.com", password: "pw")
        w.next()
        try await waitUntil { rec.lastResults != nil }
        let v = try #require(rec.lastResults)
        #expect(v.title == "Connection Failed")
        #expect(v.imap?.icon == "dialog-warning-symbolic")
        #expect(v.imap?.text == "Testing the connection failed: the server did not respond in time")
        #expect(v.smtp == v.imap)
        #expect(v.buttons == WizardController.WizardButtons(edit: true, retry: true, addAnyway: true, add: false))
        #expect(rec.pages.last == [.identity, .servers, .testing])
        #expect(w.lastOutcome == .failed)

        // Edit Servers pops to the Servers page; Test Connection pushes back.
        w.edit()
        #expect(rec.pages.last == [.identity, .servers])
        w.testServers()
        #expect(rec.pages.last == [.identity, .servers, .testing])
        try await waitUntil { rec.testing.count >= 4 }
    }

    @Test func serverValidationFlagsEmptyRows() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let (w, rec) = try await startWizard(fake)
        w.setServers(name: "x", imap: ServerFields(host: " ", port: 993, security: .tls, username: "me"),
                     smtp: ServerFields(host: "smtp.example.com", port: 587, security: .starttls, username: ""))
        w.testServers()
        #expect(rec.serverProblems == [ServerProblems(imapHost: true, smtpUser: true)])
        #expect(rec.pages == [[.identity]])
        #expect(rec.testing.isEmpty)
    }

    @Test func testSuccessOffersAdd() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in json(discoveredJSON) }
        await fake.on(API.AccountTest.name) { _ in json(testOKJSON) }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: "me@example.com", password: "pw")
        w.next()
        try await waitUntil { rec.lastResults != nil }
        let v = try #require(rec.lastResults)
        #expect(v.icon == "emblem-ok-symbolic")
        #expect(v.title == "Ready to Add")
        #expect(v.imap == WizardController.EndpointRow(icon: "emblem-ok-symbolic", text: "Connected in 12 ms"))
        #expect(v.buttons == WizardController.WizardButtons(edit: true, retry: false, addAnyway: false, add: true))
        #expect(w.lastOutcome == .ok)
        #expect(rec.pages.last == [.identity, .servers, .testing])
    }

    @Test func addConflictShowsAToastAndKeepsTheResults() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in json(discoveredJSON) }
        await fake.on(API.AccountTest.name) { _ in json(testOKJSON) }
        await fake.on(API.AccountAdd.name) { _ in throw RPCError(code: .conflict, message: "exists") }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: "me@example.com", password: "pw")
        w.next()
        try await waitUntil { rec.lastResults != nil }
        let before = rec.testing.count
        w.add()
        #expect(rec.testing[before] == .progress(title: "Adding Account…"))
        try await waitUntil { !rec.toasts.isEmpty }
        #expect(rec.toasts == ["An account with this e-mail address already exists"])
        #expect(rec.lastResults?.buttons == WizardController.WizardButtons(edit: true, add: true))
        #expect(rec.done.isEmpty)
    }

    @Test func addSuccessReportsDone() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let params = ParamsLog()
        await fake.on(API.AccountDiscover.name) { _ in json(discoveredJSON) }
        await fake.on(API.AccountTest.name) { _ in json(testOKJSON) }
        await fake.on(API.AccountAdd.name) { p in
            await params.record(API.AccountAdd.name, p)
            return json(#"{"accountId":"acc-1"}"#)
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: "me@example.com", password: "pw")
        w.next()
        try await waitUntil { rec.lastResults != nil }
        w.add()
        try await waitUntil { !rec.done.isEmpty }
        #expect(rec.done == ["acc-1"])
        #expect(rec.doneConfigs.last?.email == "me@example.com")
        let sent = try #require(try await params.last(API.AccountAdd.name, as: AccountAddParams.self))
        #expect(sent.credentials.password == "pw")
        #expect(sent.config.imap?.host == "imap.example.com")
        #expect(sent.config.imap?.authMethod == .password)
        #expect(sent.config.displayName == "Me")
    }

    @Test func editModeStartsOnTheServersPageAndUpdates() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let params = ParamsLog()
        await fake.on(API.AccountTest.name) { p in
            await params.record(API.AccountTest.name, p)
            return json(testOKJSON)
        }
        await fake.on(API.AccountUpdate.name) { p in
            await params.record(API.AccountUpdate.name, p)
            return json("{}")
        }
        let account = imapAccount()
        let (w, rec) = try await startWizard(fake, editing: account)
        #expect(w.isEditing)
        #expect(rec.pages == [[.identity, .servers]])
        #expect(rec.applied == [account.config])
        #expect(rec.identities == [Identity(displayName: "Me", email: "me@example.com", password: "")])
        #expect(w.title == "Edit Account")
        #expect(w.passwordTitle == "New Password (leave empty to keep)")
        #expect(w.nextLabel == "Next")
        #expect(w.addLabel == "Save")
        #expect(w.addAnywayLabel == "Save Anyway")
        #expect(w.progressTitle == "Saving Account…")

        // Back to the identity, Next pushes the Servers page again without discovery.
        w.back()
        #expect(rec.pages.last == [.identity])
        w.setIdentity(name: "New Me", email: "me@example.com", password: "")
        w.next()
        #expect(rec.pages.last == [.identity, .servers])
        #expect(rec.busy.isEmpty)

        w.setServers(name: "Work", imap: ServerFields(host: "imap.example.com", port: 993, security: .tls, username: "me"),
                     smtp: ServerFields(host: "smtp.example.com", port: 465, security: .tls, username: "me"))
        w.testServers()
        try await waitUntil { rec.lastResults != nil }
        #expect(rec.lastResults?.title == "Ready to Save")
        let tested = try #require(try await params.last(API.AccountTest.name, as: AccountTestParams.self))
        #expect(tested.accountId == "acc-9")
        #expect(tested.credentials.password == nil)

        w.add()
        try await waitUntil { !rec.done.isEmpty }
        #expect(rec.done == ["acc-9"])
        let updated = try #require(try await params.last(API.AccountUpdate.name, as: AccountUpdateParams.self))
        #expect(updated.accountId == "acc-9")
        #expect(updated.config.displayName == "New Me")
        #expect(updated.credentials.password == nil)
        #expect(await !fake.calls.contains(API.AccountDiscover.name))
        #expect(await !fake.calls.contains(API.AccountAdd.name))
    }

    @Test func providerWithoutSignInShowsTheHintPage() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in
            json(#"{"config":{"name":"me@outlook.com","email":"me@outlook.com","kind":"graph","graph":{"source":"goa"}},"source":"provider","providerName":"Microsoft 365"}"#)
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "me@outlook.com", password: "")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .goa] }
        #expect(rec.goaHints == ["This address belongs to a Microsoft 365 account. Add it under Settings → Online Accounts, then come back here."])
        #expect(rec.goaHintBrowser == [false], "no browser sign-in offered")
        #expect(rec.identityProblems.isEmpty)
        #expect(w.linkedCfg == nil)
        #expect(await !fake.calls.contains(API.AccountTest.name))
        // Back leads to the identity page.
        w.back()
        #expect(rec.pages.last == [.identity])
    }

    @Test func linkedDiscoveryTestsTheGraphAccount() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let params = ParamsLog()
        await fake.on(API.AccountDiscover.name) { _ in
            json(#"{"config":{"name":"me@outlook.com","email":"me@outlook.com","kind":"graph","graph":{"source":"goa","goaAccountId":"goa-1"}},"source":"goa"}"#)
        }
        await fake.on(API.AccountTest.name) { p in
            await params.record(API.AccountTest.name, p)
            return json(#"{"graph":{"ok":true,"latencyMs":3}}"#)
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: "me@outlook.com", password: "typed")
        w.next()
        try await waitUntil { rec.lastResults != nil }
        #expect(rec.pages.last == [.identity, .testing])
        #expect(w.linkedCfg?.name == "outlook.com")
        #expect(w.identity.password == "")
        #expect(rec.identities.last?.password == "")
        let v = try #require(rec.lastResults)
        #expect(v.title == "Ready to Add")
        #expect(v.graph == WizardController.EndpointRow(icon: "emblem-ok-symbolic", text: "Connected in 3 ms"))
        #expect(v.imap == nil && v.smtp == nil)
        #expect(v.buttons == WizardController.WizardButtons(edit: false, add: true))
        let tested = try #require(try await params.last(API.AccountTest.name, as: AccountTestParams.self))
        #expect(tested.credentials.password == nil)
        #expect(tested.config.displayName == "Me")
    }

    @Test func linkedAuthFailureIsAFailureWithADescription() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in
            json(#"{"config":{"name":"x","email":"me@outlook.com","kind":"graph","graph":{"source":"goa","goaAccountId":"goa-1"}},"source":"goa"}"#)
        }
        await fake.on(API.AccountTest.name) { _ in
            json(#"{"graph":{"ok":false,"error":{"code":1201,"message":"denied"},"latencyMs":0}}"#)
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "me@outlook.com", password: "")
        w.next()
        try await waitUntil { rec.lastResults != nil }
        let v = try #require(rec.lastResults)
        #expect(v.title == "Connection Failed")
        #expect(v.description == "The server refused the sign-in. Sign in to the account again in Settings → Online Accounts and make sure access to mail is allowed.")
        #expect(v.buttons == WizardController.WizardButtons(edit: false, retry: true, addAnyway: true, add: false))
        #expect(w.lastOutcome == .failed)
        #expect(rec.pages.last == [.identity, .testing])
        #expect(rec.identityProblems.isEmpty)
    }

    @Test func closedControllerIgnoresLateReplies() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in
            try? await Task.sleep(for: .milliseconds(200))
            return json(discoveredJSON)
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: "me@example.com", password: "pw")
        w.next()
        #expect(rec.busy == [true])
        w.close()
        #expect(w.closed)
        try await Task.sleep(for: .milliseconds(400))
        #expect(rec.busy == [true])
        #expect(rec.pages == [[.identity]])
        #expect(rec.applied.isEmpty)
        #expect(rec.testing.isEmpty)
        #expect(await fake.calls.contains(API.AccountDiscover.name))
    }

    @Test func staleDiscoverReplyIsDropped() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { p in
            // The first address answers late, the second at once.
            if String(decoding: p, as: UTF8.self).contains("first@") {
                try? await Task.sleep(for: .milliseconds(200))
                return json(#"{"source":"none"}"#)
            }
            return json(discoveredJSON)
        }
        await fake.on(API.AccountTest.name) { _ in
            try? await Task.sleep(for: .seconds(10))
            return json(testOKJSON)
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: "first@example.com", password: "pw")
        w.next()
        w.setIdentity(name: "Me", email: "me@example.com", password: "pw")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .servers, .testing] }
        try await Task.sleep(for: .milliseconds(300))
        #expect(rec.applied.count == 1)
        #expect(rec.applied.last?.imap?.host == "imap.example.com")
        #expect(rec.pages.last == [.identity, .servers, .testing])
        #expect(rec.busy == [true, true, false])
    }

    @Test func recheckWithoutASignInToasts() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "me@outlook.com", password: "")
        w.recheckLinked()
        try await waitUntil { !rec.toasts.isEmpty }
        #expect(rec.toasts == ["This address is not signed in yet"])
        #expect(rec.linked.count == 2)
    }

    @Test func useLinkedWithoutAConfigToasts() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let (w, rec) = try await startWizard(fake)
        w.useLinked(LinkedAccount(provider: .google, email: "me@gmail.com", goaAccountId: "g", configured: false, attentionNeeded: false))
        #expect(rec.toasts == ["The mail service does not describe this account; update it and try again"])
        #expect(rec.pages == [[.identity]])
    }

    @Test func saveErrorTexts() {
        #expect(saveErrorText(RPCError(code: .conflict, message: "x"), editing: false) == "An account with this e-mail address already exists")
        #expect(saveErrorText(RPCError(code: .keyringError, message: "x"), editing: false) == "Adding the account failed: the system keyring is unavailable")
        #expect(saveErrorText(RPCError(code: .keyringError, message: "x"), editing: true) == "Saving the account failed: the system keyring is unavailable")
        #expect(saveErrorText(RPCClient.ClientError.disconnected, editing: true) == "Saving the account needs a running mail backend")
    }

    // MARK: Browser sign-in (oauth.go)

    @Test func daemonDiscoveryShowsTheBrowserPrompt() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in json(gmailDiscoveredJSON) }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: "me@gmail.com", password: "")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .oauth] }
        #expect(rec.oauth == [googlePrompt])
        #expect(rec.identityProblems.isEmpty, "no password is asked for")
        #expect(w.oauth?.provider == .google && w.oauth?.name == "Google")
        #expect(w.oauth?.config?.oauth2?.source == .daemon)
        #expect(w.oauth?.passwordAlt?.imap?.authMethod == .password)
        #expect(w.canGoBack)
        #expect(await !fake.calls.contains(API.AccountTest.name))
    }

    @Test func browserSignInTestsAndAddsWithTheSession() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let params = ParamsLog()
        let waits = Counter()
        await fake.on(API.AccountDiscover.name) { _ in json(gmailDiscoveredJSON) }
        await fake.on(API.AccountOAuthStart.name) { p in
            await params.record(API.AccountOAuthStart.name, p)
            return json(oauthStartJSON)
        }
        await fake.on(API.AccountOAuthWait.name) { p in
            await params.record(API.AccountOAuthWait.name, p)
            // The browser comes back on the second call.
            return json(await waits.next() == 1 ? #"{"status":"pending"}"# : oauthCompleteJSON)
        }
        await fake.on(API.AccountTest.name) { p in
            await params.record(API.AccountTest.name, p)
            return json(testOKJSON)
        }
        await fake.on(API.AccountAdd.name) { p in
            await params.record(API.AccountAdd.name, p)
            return json(#"{"accountId":"acc-7"}"#)
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: "me@gmail.com", password: "")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .oauth] }
        w.signInWithProvider()
        try await waitUntil { rec.lastResults != nil }

        #expect(rec.opened == [authURL])
        #expect(rec.oauth == [googlePrompt, .waiting, googlePrompt])
        #expect(rec.pages.last == [.identity, .oauth, .testing])
        #expect(await waits.n == 2)
        let started = try #require(try await params.last(API.AccountOAuthStart.name, as: AccountOAuthStartParams.self))
        #expect(started.accountId == nil)
        #expect(started.config?.oauth2 == OAuth2Config(source: .daemon, provider: .google))
        #expect(started.config?.displayName == "Me" && started.config?.name == "gmail.com", "the identity is added at the start")
        #expect(started.browserPage == browserPage())
        #expect(try await params.last(API.AccountOAuthWait.name, as: AccountOAuthWaitParams.self)?.sessionId == "s_1")
        let tested = try #require(try await params.last(API.AccountTest.name, as: AccountTestParams.self))
        #expect(tested.credentials == Credentials(oauthSession: "s_1"))
        #expect(tested.accountId == nil)
        #expect(tested.config.oauth2?.source == .daemon && tested.config.displayName == "Me")
        let v = try #require(rec.lastResults)
        #expect(v.title == "Ready to Add")
        #expect(v.buttons == WizardController.WizardButtons(edit: false, add: true))
        #expect(w.editLabel == "Edit Servers", "Sign In Again only after a refused sign-in")

        w.add()
        try await waitUntil { !rec.done.isEmpty }
        #expect(rec.done == ["acc-7"])
        let added = try #require(try await params.last(API.AccountAdd.name, as: AccountAddParams.self))
        #expect(added.credentials == Credentials(oauthSession: "s_1"))
        #expect(added.config.oauth2?.source == .daemon)
        // The daemon consumed the session: closing does not cancel it.
        w.close()
        try await Task.sleep(for: .milliseconds(200))
        #expect(await !fake.calls.contains(API.AccountOAuthCancel.name))
    }

    @Test func waitErrorReturnsToThePromptWithTheReason() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let waits = Counter()
        await fake.on(API.AccountDiscover.name) { _ in json(gmailDiscoveredJSON) }
        await fake.on(API.AccountOAuthStart.name) { _ in json(oauthStartJSON) }
        await fake.on(API.AccountOAuthWait.name) { _ in
            if await waits.next() == 1 {
                throw RPCError(code: .authFailed, message: "invalid_grant")
            }
            throw RPCError(code: .invalidArgument, message: "other mailbox", data: .object(["signedInAs": .string("other@gmail.com")]))
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "me@gmail.com", password: "")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .oauth] }
        w.signInWithProvider()
        try await waitUntil { !rec.toasts.isEmpty }
        #expect(rec.toasts == ["The sign-in with Google was refused"])
        #expect(rec.oauth == [googlePrompt, .waiting, googlePrompt])
        #expect(rec.pages.last == [.identity, .oauth])
        #expect(w.oauth?.sessionId == nil && w.canGoBack)
        #expect(w.linkedCfg == nil)
        // A session the daemon may still hold is let go.
        try await waitUntil { await fake.calls.contains(API.AccountOAuthCancel.name) }

        // Another mailbox in the browser.
        w.signInWithProvider()
        try await waitUntil { rec.toasts.count == 2 }
        #expect(rec.toasts.last == "The browser signed in to other@gmail.com, not to this address")
        #expect(rec.oauth.last == googlePrompt)
        #expect(await !fake.calls.contains(API.AccountTest.name))
    }

    @Test func startErrorStaysOnThePrompt() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in json(gmailDiscoveredJSON) }
        await fake.on(API.AccountOAuthStart.name) { _ in throw RPCError(code: .unavailable, message: "too many") }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "me@gmail.com", password: "")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .oauth] }
        w.signInWithProvider()
        try await waitUntil { !rec.toasts.isEmpty }
        #expect(rec.toasts == ["Starting the sign-in failed"])
        #expect(rec.oauth == [googlePrompt])
        #expect(rec.opened.isEmpty)
        #expect(rec.busy == [true, false], "only the discovery kept the pages busy")
        #expect(rec.starting == [true, false])
        #expect(await !fake.calls.contains(API.AccountOAuthWait.name))
    }

    @Test func onlyTheSignInButtonWaitsForTheStart() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in json(gmailDiscoveredJSON) }
        await fake.on(API.AccountOAuthStart.name) { _ in
            try? await Task.sleep(for: .milliseconds(300))
            return json(oauthStartJSON)
        }
        await fake.on(API.AccountOAuthWait.name) { _ in
            try? await Task.sleep(for: .seconds(10))
            return json(#"{"status":"pending"}"#)
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "me@gmail.com", password: "")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .oauth] }
        #expect(rec.busy == [true, false])
        w.signInWithProvider()
        #expect(w.oauthStarting && !w.busy)
        #expect(rec.starting == [true])
        #expect(rec.busy == [true, false], "the identity and server pages stay usable")
        #expect(w.canGoBack, "Back stays while the sign-in starts")
        // A second click while it starts is ignored.
        w.signInWithProvider()
        try await waitUntil { rec.oauth.last == .waiting }
        #expect(rec.starting == [true, false])
        #expect(!w.oauthStarting)
        #expect(await fake.calls.filter { $0 == API.AccountOAuthStart.name }.count == 1)
        w.close()
    }

    @Test func backDuringTheStartDropsItsAnswer() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let params = ParamsLog()
        let starts = Counter()
        await fake.on(API.AccountDiscover.name) { _ in json(gmailDiscoveredJSON) }
        await fake.on(API.AccountOAuthStart.name) { _ in
            try? await Task.sleep(for: .milliseconds(200))
            // The first start fails, the second opens a session.
            if await starts.next() == 1 {
                throw RPCError(code: .oauthClientMissing, message: "no client")
            }
            return json(oauthStartJSON)
        }
        await fake.on(API.AccountOAuthCancel.name) { p in
            await params.record(API.AccountOAuthCancel.name, p)
            return json("{}")
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "me@gmail.com", password: "")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .oauth] }

        // A failure after Back: neither the notice nor a toast.
        w.signInWithProvider()
        w.back()
        #expect(rec.pages.last == [.identity])
        try await waitUntil { rec.starting == [true, false] }
        #expect(rec.oauth == [googlePrompt], "the page does not switch to the missing client")
        #expect(rec.toasts.isEmpty)
        #expect(rec.pages.last == [.identity])

        // A session opened after Back is let go, never opened or waited on.
        w.next()
        try await waitUntil { rec.oauth.count == 2 && rec.pages.last == [.identity, .oauth] }
        w.signInWithProvider()
        w.back()
        try await waitUntil { await fake.calls.contains(API.AccountOAuthCancel.name) }
        #expect(try await params.last(API.AccountOAuthCancel.name, as: AccountOAuthCancelParams.self)?.sessionId == "s_1")
        #expect(rec.starting == [true, false, true, false])
        #expect(rec.oauth == [googlePrompt, googlePrompt])
        #expect(rec.opened.isEmpty && rec.toasts.isEmpty)
        #expect(rec.pages.last == [.identity])
        #expect(w.oauth?.sessionId == nil)
        #expect(rec.busy == [true, false, true, false], "only the discoveries kept the pages busy")
        #expect(await !fake.calls.contains(API.AccountOAuthWait.name))
    }

    @Test func cancelWhileWaitingCancelsTheSession() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let params = ParamsLog()
        await fake.on(API.AccountDiscover.name) { _ in json(gmailDiscoveredJSON) }
        await fake.on(API.AccountOAuthStart.name) { _ in json(oauthStartJSON) }
        await fake.on(API.AccountOAuthWait.name) { _ in
            try? await Task.sleep(for: .seconds(10))
            return json(#"{"status":"pending"}"#)
        }
        await fake.on(API.AccountOAuthCancel.name) { p in
            await params.record(API.AccountOAuthCancel.name, p)
            return json("{}")
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "me@gmail.com", password: "")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .oauth] }
        w.signInWithProvider()
        try await waitUntil { rec.oauth.last == .waiting }
        #expect(!w.canGoBack, "no Back while the browser is out")
        w.back()
        #expect(rec.pages.last == [.identity, .oauth])
        w.reopenBrowser()
        #expect(rec.opened == [authURL, authURL])

        w.cancelOAuth()
        #expect(rec.oauth.last == googlePrompt)
        #expect(w.oauth?.sessionId == nil && w.canGoBack)
        try await waitUntil { await fake.calls.contains(API.AccountOAuthCancel.name) }
        #expect(try await params.last(API.AccountOAuthCancel.name, as: AccountOAuthCancelParams.self)?.sessionId == "s_1")
        #expect(rec.toasts.isEmpty, "the user's own cancel says nothing")
        // Reopening is for the wait only.
        w.reopenBrowser()
        #expect(rec.opened.count == 2)
    }

    @Test func closingWhileWaitingCancelsTheSession() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in json(gmailDiscoveredJSON) }
        await fake.on(API.AccountOAuthStart.name) { _ in json(oauthStartJSON) }
        await fake.on(API.AccountOAuthWait.name) { _ in
            try? await Task.sleep(for: .seconds(10))
            return json(#"{"status":"pending"}"#)
        }
        await fake.on(API.AccountOAuthCancel.name) { _ in json("{}") }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "me@gmail.com", password: "")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .oauth] }
        w.signInWithProvider()
        try await waitUntil { rec.oauth.last == .waiting }
        w.close()
        try await waitUntil { await fake.calls.contains(API.AccountOAuthCancel.name) }
        #expect(w.closed)
    }

    @Test func missingClientOffersTheAppPasswordForGoogle() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let params = ParamsLog()
        await fake.on(API.AccountDiscover.name) { _ in json(gmailDiscoveredJSON) }
        await fake.on(API.AccountOAuthStart.name) { _ in throw RPCError(code: .oauthClientMissing, message: "no client") }
        await fake.on(API.AccountTest.name) { p in
            await params.record(API.AccountTest.name, p)
            try? await Task.sleep(for: .seconds(10))
            return json(testOKJSON)
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: "me@gmail.com", password: "app-pw")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .oauth] }
        w.signInWithProvider()
        try await waitUntil { rec.oauth.count == 2 }
        #expect(rec.oauth.last == .unavailable(
            description: "No OAuth client is configured for Google on this computer. Add a client ID to the mail backend's configuration and try again.",
            passwordAlternative: true))
        #expect(rec.toasts.isEmpty)

        w.useAppPassword()
        #expect(rec.pages.last == [.identity, .servers, .testing])
        #expect(w.linkedCfg == nil && w.oauth == nil)
        let cfg = try #require(rec.applied.last)
        #expect(cfg.imap?.host == "imap.gmail.com" && cfg.imap?.authMethod == .password)
        #expect(cfg.smtp?.port == 465 && cfg.displayName == "Me")
        #expect(w.imap.host == "imap.gmail.com")
        try await waitUntil { await fake.calls.contains(API.AccountTest.name) }
        let tested = try #require(try await params.last(API.AccountTest.name, as: AccountTestParams.self))
        #expect(tested.credentials == Credentials(password: "app-pw"))
        #expect(tested.config.imap?.authMethod == .password && tested.config.oauth2 == nil)
        #expect(w.editLabel == "Edit Servers")
    }

    @Test func appPasswordWithoutAPasswordAsksForItAndSkipsDiscovery() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let params = ParamsLog()
        await fake.on(API.AccountDiscover.name) { _ in json(gmailDiscoveredJSON) }
        await fake.on(API.AccountOAuthStart.name) { _ in throw RPCError(code: .oauthClientMissing, message: "no client") }
        await fake.on(API.AccountTest.name) { p in
            await params.record(API.AccountTest.name, p)
            return json(testOKJSON)
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "me@gmail.com", password: "")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .oauth] }
        w.signInWithProvider()
        try await waitUntil { rec.oauth.count == 2 }
        w.useAppPassword()
        #expect(rec.pages.last == [.identity])
        #expect(rec.identityProblems.last == IdentityProblems(password: true))
        #expect(rec.banners.last == "Enter the app password for this account")
        #expect(rec.focus.last == .password)

        // Next with the password goes the password way without asking again.
        w.setIdentity(name: "", email: "me@gmail.com", password: "app-pw")
        w.next()
        #expect(rec.pages.last == [.identity, .servers, .testing])
        try await waitUntil { rec.lastResults != nil }
        #expect(await fake.calls.filter { $0 == API.AccountDiscover.name }.count == 1)
        let tested = try #require(try await params.last(API.AccountTest.name, as: AccountTestParams.self))
        #expect(tested.credentials == Credentials(password: "app-pw"))
        #expect(tested.config.imap?.host == "imap.gmail.com")
        #expect(rec.lastResults?.title == "Ready to Add")

        // Another address forgets the choice and discovers again.
        w.back()
        w.back()
        w.setIdentity(name: "", email: "you@gmail.com", password: "pw")
        w.next()
        try await waitUntil { await fake.calls.filter { $0 == API.AccountDiscover.name }.count == 2 }
    }

    @Test func missingClientForMicrosoftOffersNoPassword() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in json(graphDiscoveredJSON) }
        await fake.on(API.AccountOAuthStart.name) { _ in throw RPCError(code: .oauthClientMissing, message: "no client") }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "me@contoso.com", password: "")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .oauth] }
        #expect(rec.oauth == [microsoftPrompt])
        w.signInWithProvider()
        try await waitUntil { rec.oauth.count == 2 }
        #expect(rec.oauth.last == .unavailable(
            description: "No OAuth client is configured for Microsoft 365 on this computer. Add a client ID to the mail backend's configuration and try again.",
            passwordAlternative: false))
        w.useAppPassword()
        #expect(rec.pages.last == [.identity, .oauth], "nothing to fall back to")
        #expect(w.canGoBack)
        w.back()
        #expect(rec.pages.last == [.identity])
    }

    @Test func goaHintOffersTheBrowser() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDiscover.name) { _ in
            json(#"{"config":{"name":"me@contoso.com","email":"me@contoso.com","kind":"graph","graph":{"source":"goa"}},"source":"provider","providerName":"Microsoft 365","alternatives":[\#(graphOAuthConfigJSON)]}"#)
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "me@contoso.com", password: "")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .goa] }
        #expect(rec.goaHintBrowser == [true])
        w.useBrowser()
        #expect(rec.pages.last == [.identity, .oauth])
        #expect(rec.oauth == [microsoftPrompt])
        #expect(w.oauth?.config?.graph?.source == .daemon)
        #expect(w.oauth?.passwordAlt == nil)
    }

    @Test func signInModeStartsOnTheBrowserAndUpdates() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let params = ParamsLog()
        let account = oauthAccount()
        await fake.on(API.AccountOAuthStart.name) { p in
            await params.record(API.AccountOAuthStart.name, p)
            return json(oauthStartJSON)
        }
        await fake.on(API.AccountOAuthWait.name) { _ in json(oauthCompleteJSON) }
        await fake.on(API.AccountTest.name) { p in
            await params.record(API.AccountTest.name, p)
            return json(testOKJSON)
        }
        await fake.on(API.AccountUpdate.name) { p in
            await params.record(API.AccountUpdate.name, p)
            return json("{}")
        }
        let (w, rec) = try await startWizard(fake, editing: account, signIn: true)
        #expect(w.signInMode && w.isEditing)
        #expect(w.title == "Sign In")
        #expect(w.oauth?.config == nil && w.oauth?.name == "Google")
        #expect(rec.pages == [[.oauth]])
        #expect(rec.oauth == [googlePrompt])
        #expect(!w.canGoBack)
        #expect(!w.emailEditable && !w.passwordVisible)

        w.signInWithProvider()
        try await waitUntil { rec.lastResults != nil }
        #expect(rec.pages.last == [.oauth, .testing])
        #expect(w.canGoBack)
        let started = try #require(try await params.last(API.AccountOAuthStart.name, as: AccountOAuthStartParams.self))
        #expect(started.accountId == "acc-9" && started.config == nil)
        let tested = try #require(try await params.last(API.AccountTest.name, as: AccountTestParams.self))
        #expect(tested.accountId == "acc-9")
        #expect(tested.credentials == Credentials(oauthSession: "s_1"))
        #expect(rec.lastResults?.title == "Ready to Save")

        w.add()
        try await waitUntil { !rec.done.isEmpty }
        #expect(rec.done == ["acc-9"])
        let updated = try #require(try await params.last(API.AccountUpdate.name, as: AccountUpdateParams.self))
        #expect(updated.accountId == "acc-9")
        #expect(updated.credentials == Credentials(oauthSession: "s_1"))
        #expect(updated.config.displayName == "Me")
        #expect(await !fake.calls.contains(API.AccountAdd.name))
        #expect(await !fake.calls.contains(API.AccountDiscover.name))
    }

    @Test func signInIsIgnoredForAPasswordAccount() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let (w, rec) = try await startWizard(fake, editing: imapAccount(), signIn: true)
        #expect(!w.signInMode)
        #expect(rec.pages == [[.identity, .servers]])
        #expect(rec.oauth.isEmpty)
    }

    @Test func editingABrowserAccountOffersSignInAgain() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let params = ParamsLog()
        await fake.on(API.AccountTest.name) { p in
            await params.record(API.AccountTest.name, p)
            return json(#"{"imap":{"ok":false,"error":{"code":1200,"message":"sign in"},"latencyMs":0},"smtp":{"ok":false,"error":{"code":1200,"message":"sign in"},"latencyMs":0}}"#)
        }
        let (w, rec) = try await startWizard(fake, editing: oauthAccount())
        #expect(!w.signInMode)
        #expect(rec.pages == [[.identity]])
        #expect(w.nextLabel == "Test Connection")
        #expect(!w.emailEditable && !w.passwordVisible)
        #expect(w.title == "Edit Account")
        #expect(w.editLabel == "Edit Servers")

        w.next()
        try await waitUntil { rec.lastResults != nil }
        #expect(rec.pages.last == [.identity, .testing])
        let tested = try #require(try await params.last(API.AccountTest.name, as: AccountTestParams.self))
        #expect(tested.accountId == "acc-9")
        #expect(tested.credentials == Credentials(), "the stored sign-in is tested")
        let v = try #require(rec.lastResults)
        #expect(v.title == "Connection Failed")
        #expect(v.description == "The server refused the sign-in. Sign in again and make sure access to mail is allowed.")
        #expect(v.buttons == WizardController.WizardButtons(edit: true, retry: true, addAnyway: true))
        #expect(w.editLabel == "Sign In Again")
        #expect(w.lastOutcome == .failed)
        #expect(rec.identityProblems.isEmpty)

        // Sign In Again: the prompt, then a sign-in by the account's id.
        w.edit()
        #expect(rec.pages.last == [.identity, .oauth])
        #expect(rec.oauth == [googlePrompt])
        #expect(w.oauth?.config == nil && w.oauth?.provider == .google)
    }

    @Test func aStartWithoutAnHTTPSAddressIsNotOpened() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let params = ParamsLog()
        await fake.on(API.AccountDiscover.name) { _ in json(gmailDiscoveredJSON) }
        await fake.on(API.AccountOAuthStart.name) { _ in
            json(#"{"sessionId":"s_1","authUrl":"http://accounts.google.com/x","expiresAt":"2026-09-25T10:10:00Z"}"#)
        }
        await fake.on(API.AccountOAuthCancel.name) { p in
            await params.record(API.AccountOAuthCancel.name, p)
            return json("{}")
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "", email: "me@gmail.com", password: "")
        w.next()
        try await waitUntil { rec.pages.last == [.identity, .oauth] }
        w.signInWithProvider()
        try await waitUntil { !rec.toasts.isEmpty }
        #expect(rec.toasts == ["The link could not be opened: not an https address"])
        #expect(rec.opened.isEmpty)
        #expect(rec.oauth == [googlePrompt], "the prompt stays: nothing could come back from the browser")
        #expect(rec.starting == [true, false])
        #expect(w.oauth?.sessionId == nil && w.oauth?.authUrl == nil && w.canGoBack)
        // The session is let go, and nothing waits on it.
        try await waitUntil { await fake.calls.contains(API.AccountOAuthCancel.name) }
        #expect(try await params.last(API.AccountOAuthCancel.name, as: AccountOAuthCancelParams.self)?.sessionId == "s_1")
        #expect(await !fake.calls.contains(API.AccountOAuthWait.name))
    }

    @Test func browserAccountWithoutASignInProblemHidesEdit() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountTest.name) { _ in
            json(#"{"imap":{"ok":false,"error":{"code":1301,"message":"no route"},"latencyMs":0},"smtp":{"ok":true,"latencyMs":5}}"#)
        }
        let (w, rec) = try await startWizard(fake, editing: oauthAccount(status: .idle))
        w.next()
        try await waitUntil { rec.lastResults != nil }
        let v = try #require(rec.lastResults)
        #expect(v.description == nil)
        #expect(v.buttons == WizardController.WizardButtons(edit: false, retry: true, addAnyway: true))
    }


    // MARK: Trusting a certificate (trust.go)

    /// A discovered account whose test answers `first`, then OK; the
    /// params of every account.test and account.add are recorded.
    private func startRefused(_ first: String) async throws -> (WizardController, Recorder, FakeDaemon, ParamsLog) {
        let fake = try await makeFake()
        let params = ParamsLog()
        let counter = Counter()
        await fake.on(API.AccountDiscover.name) { _ in json(discoveredJSON) }
        await fake.on(API.AccountTest.name) { p in
            await params.record(API.AccountTest.name, p)
            return json(await counter.next() == 1 ? first : testOKJSON)
        }
        await fake.on(API.AccountAdd.name) { p in
            await params.record(API.AccountAdd.name, p)
            return json(#"{"accountId":"acc-1"}"#)
        }
        let (w, rec) = try await startWizard(fake)
        w.setIdentity(name: "Me", email: "me@example.com", password: "pw")
        w.next()
        try await waitUntil { rec.lastResults != nil }
        return (w, rec, fake, params)
    }

    @Test func oneConfirmationTrustsTheSameCertificateOnBothEndpoints() async throws {
        let (w, rec, fake, params) = try await startRefused(testRefusedJSON(imap: certA, smtp: certA))
        defer { Task { await fake.stop() } }
        let v = try #require(rec.lastResults)
        #expect(v.title == "Connection Failed")
        #expect(v.imap == WizardController.EndpointRow(
            icon: "dialog-error-symbolic", text: "The server's certificate is not from a trusted authority", trust: true))
        #expect(v.smtp?.trust == true)
        #expect(v.buttons == WizardController.WizardButtons(edit: true, retry: true, addAnyway: true))
        #expect(w.pinnedFingerprints.imap.isEmpty && w.pinnedFingerprints.smtp.isEmpty && rec.pins == [["", ""]])

        w.trustCertificate(.smtp)
        try await waitUntil { rec.lastResults?.title == "Ready to Add" }
        #expect(rec.prompts.count == 1)
        let prompt = try #require(rec.prompts.first)
        #expect(prompt.heading == "Trust This Certificate?" && prompt.confirmLabel == "_Trust")
        #expect(prompt.body.contains("for imap.example.com:993 and smtp.example.com:587 and no other"), "IMAP first")
        #expect(prompt.details.first == CertificateDetail(label: "SHA-256 fingerprint", value: fingerprintA, monospaced: true))
        #expect(prompt.details.dropFirst().first == CertificateDetail(label: "Issued to", value: "127.0.0.1"))
        #expect(!prompt.details.contains { $0.label == "Previously trusted" })
        #expect(rec.pins.last == [fingerprintA, fingerprintA])
        let tested = try #require(try await params.last(API.AccountTest.name, as: AccountTestParams.self))
        #expect(tested.config.imap?.certificateSha256 == certA && tested.config.smtp?.certificateSha256 == certA)
        #expect(tested.credentials.password == "pw")

        w.add()
        try await waitUntil { !rec.done.isEmpty }
        let added = try #require(try await params.last(API.AccountAdd.name, as: AccountAddParams.self))
        #expect(added.config.imap?.certificateSha256 == certA && added.config.smtp?.certificateSha256 == certA)
    }

    @Test func differentCertificatesTrustOnlyTheClickedEndpoint() async throws {
        let (w, rec, fake, params) = try await startRefused(testRefusedJSON(imap: certA, smtp: certB))
        defer { Task { await fake.stop() } }
        #expect(rec.lastResults?.imap?.trust == true && rec.lastResults?.smtp?.trust == true)
        w.trustCertificate(.smtp)
        try await waitUntil { rec.lastResults?.title == "Ready to Add" }
        let prompt = try #require(rec.prompts.last)
        #expect(prompt.body.contains("for smtp.example.com:587 and no other"))
        #expect(prompt.details.first?.value == fingerprintB)
        #expect(rec.pins.last == ["", fingerprintB])
        let tested = try #require(try await params.last(API.AccountTest.name, as: AccountTestParams.self))
        #expect(tested.config.imap?.certificateSha256 == nil && tested.config.smtp?.certificateSha256 == certB)
    }

    @Test func declinedConfirmationChangesNothing() async throws {
        let (w, rec, fake, _) = try await startRefused(testRefusedJSON(imap: certA, smtp: nil))
        defer { Task { await fake.stop() } }
        #expect(rec.lastResults?.imap?.trust == true)
        #expect(rec.lastResults?.smtp == WizardController.EndpointRow(icon: "emblem-ok-symbolic", text: "Connected in 5 ms"))
        rec.trustAnswer = false
        let testing = rec.testing.count
        w.trustCertificate(.imap)
        try await waitUntil { rec.prompts.count == 1 }
        try await Task.sleep(for: .milliseconds(100))
        #expect(rec.testing.count == testing, "no new test")
        #expect(rec.pins == [["", ""]])
        #expect(w.imap.certificateSha256.isEmpty)
        #expect(await fake.calls.filter { $0 == API.AccountTest.name }.count == 1)
        // A row without an offer asks nothing.
        w.trustCertificate(.smtp)
        #expect(rec.prompts.count == 1)
    }

    @Test func connectionFailuresOfferNoTrust() async throws {
        let handshake = #"{"imap":{"ok":false,"latencyMs":1,"error":{"code":1303,"message":"eof","data":{"reason":"handshake"}}},"#
            + #""smtp":\#(refusedJSON(certA, reason: "starttlsUnavailable"))}"#
        let (w, rec, fake, _) = try await startRefused(handshake)
        defer { Task { await fake.stop() } }
        #expect(rec.lastResults?.imap == WizardController.EndpointRow(
            icon: "dialog-error-symbolic", text: "The secure connection could not be established"))
        #expect(rec.lastResults?.smtp == WizardController.EndpointRow(icon: "dialog-error-symbolic", text: "The server does not offer STARTTLS"))
        w.trustCertificate(.imap)
        w.trustCertificate(.smtp)
        #expect(rec.prompts.isEmpty)
    }

    @Test func aChangedCertificateOffersTheNewOne() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let params = ParamsLog()
        let counter = Counter()
        let changed = #"{"imap":\#(refusedJSON(certB, reason: "pinMismatch", expected: certA)),"smtp":{"ok":true,"latencyMs":5}}"#
        await fake.on(API.AccountTest.name) { p in
            await params.record(API.AccountTest.name, p)
            return json(await counter.next() == 1 ? changed : testOKJSON)
        }
        let (w, rec) = try await startWizard(fake, editing: pinnedAccount())
        w.testServers()
        try await waitUntil { rec.lastResults != nil }
        #expect(rec.lastResults?.imap?.text == "The server presented a different certificate than the one you trust")
        #expect(rec.lastResults?.imap?.trust == true)
        w.trustCertificate(.imap)
        try await waitUntil { rec.lastResults?.title == "Ready to Save" }
        // Its own warning, with the fingerprint trusted before.
        let prompt = try #require(rec.prompts.first)
        #expect(prompt.heading == "Trust the New Certificate?" && prompt.confirmLabel == "_Trust")
        #expect(prompt.body.hasPrefix("The certificate of imap.example.com:993 has changed since you trusted it."))
        #expect(prompt.details.prefix(2) == [
            CertificateDetail(label: "SHA-256 fingerprint", value: fingerprintB, monospaced: true),
            CertificateDetail(label: "Previously trusted", value: fingerprintA, monospaced: true),
        ])
        #expect(rec.pins.last == [fingerprintB, ""])
        let tested = try #require(try await params.last(API.AccountTest.name, as: AccountTestParams.self))
        #expect(tested.config.imap?.certificateSha256 == certB && tested.accountId == "acc-9")
    }

    @Test func editingKeepsThePinWhileTheServerStays() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let params = ParamsLog()
        await fake.on(API.AccountTest.name) { p in
            await params.record(API.AccountTest.name, p)
            return json(testOKJSON)
        }
        await fake.on(API.AccountUpdate.name) { p in
            await params.record(API.AccountUpdate.name, p)
            return json("{}")
        }
        let (w, rec) = try await startWizard(fake, editing: pinnedAccount())
        #expect(rec.pins == [[fingerprintA, ""]])
        #expect(w.imap.certificateSha256 == certA)

        // The Servers page reads its rows without the pin: another user
        // name and the host in another case keep it.
        w.setServers(name: "Work", imap: ServerFields(host: " IMAP.example.com", port: 993, security: .tls, username: "me2"),
                     smtp: ServerFields(host: "smtp.example.com", port: 465, security: .tls, username: "me"))
        #expect(w.imap.certificateSha256 == certA && rec.pins.count == 1)
        w.testServers()
        try await waitUntil { rec.lastResults != nil }
        let tested = try #require(try await params.last(API.AccountTest.name, as: AccountTestParams.self))
        #expect(tested.config.imap?.certificateSha256 == certA && tested.config.smtp?.certificateSha256 == nil)

        // Another host drops it; the host it was trusted for restores it.
        w.setServers(name: "Work", imap: ServerFields(host: "imap2.example.com", port: 993, security: .tls, username: "me"),
                     smtp: ServerFields(host: "smtp.example.com", port: 465, security: .tls, username: "me"))
        #expect(w.imap.certificateSha256.isEmpty && rec.pins.last == ["", ""])
        w.setServers(name: "Work", imap: ServerFields(host: "imap.example.com", port: 143, security: .starttls, username: "me"),
                     smtp: ServerFields(host: "smtp.example.com", port: 465, security: .tls, username: "me"))
        #expect(w.imap.certificateSha256.isEmpty, "another port")
        w.setServers(name: "Work", imap: ServerFields(host: "imap.example.com", port: 993, security: .none, username: "me"),
                     smtp: ServerFields(host: "smtp.example.com", port: 465, security: .tls, username: "me"))
        #expect(w.imap.certificateSha256.isEmpty, "no pin without TLS")
        w.setServers(name: "Work", imap: ServerFields(host: "imap.example.com", port: 993, security: .tls, username: "me"),
                     smtp: ServerFields(host: "smtp.example.com", port: 465, security: .tls, username: "me"))
        #expect(w.imap.certificateSha256 == certA && rec.pins.last == [fingerprintA, ""])

        w.add()
        try await waitUntil { !rec.done.isEmpty }
        let updated = try #require(try await params.last(API.AccountUpdate.name, as: AccountUpdateParams.self))
        #expect(updated.config.imap?.certificateSha256 == certA && updated.config.smtp?.certificateSha256 == nil)
        #expect(rec.doneConfigs.last?.imap?.certificateSha256 == certA)
    }

    @Test func forgetDropsThePin() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let params = ParamsLog()
        await fake.on(API.AccountTest.name) { p in
            await params.record(API.AccountTest.name, p)
            return json(testOKJSON)
        }
        await fake.on(API.AccountUpdate.name) { p in
            await params.record(API.AccountUpdate.name, p)
            return json("{}")
        }
        let (w, rec) = try await startWizard(fake, editing: pinnedAccount())
        w.forgetPin(.imap)
        #expect(rec.pins.last == ["", ""] && w.imap.certificateSha256.isEmpty)
        // For good: the same server does not bring it back.
        w.setServers(name: "Work", imap: ServerFields(host: "imap.example.com", port: 993, security: .tls, username: "me"),
                     smtp: ServerFields(host: "smtp.example.com", port: 465, security: .tls, username: "me"))
        #expect(w.imap.certificateSha256.isEmpty)
        w.testServers()
        try await waitUntil { rec.lastResults != nil }
        let tested = try #require(try await params.last(API.AccountTest.name, as: AccountTestParams.self))
        #expect(tested.config.imap?.certificateSha256 == nil)
        w.add()
        try await waitUntil { !rec.done.isEmpty }
        let updated = try #require(try await params.last(API.AccountUpdate.name, as: AccountUpdateParams.self))
        #expect(updated.config.imap?.certificateSha256 == nil)
    }

    @Test func aBrowserAccountIsNeverOfferedTrust() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountTest.name) { _ in json(testRefusedJSON(imap: certA, smtp: certA)) }
        let (w, rec) = try await startWizard(fake, editing: oauthAccount(status: .idle))
        w.setIdentity(name: "Me", email: "me@gmail.com", password: "")
        w.next()
        try await waitUntil { rec.lastResults != nil }
        #expect(rec.lastResults?.imap?.trust == false && rec.lastResults?.smtp?.trust == false)
        w.trustCertificate(.imap)
        #expect(rec.prompts.isEmpty)
    }
}
