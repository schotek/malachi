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
    var testing: [WizardController.TestingView] = []
    var focus: [WizardController.IdentityField] = []
    var toasts: [String] = []
    var done: [AccountID] = []
    var doneConfigs: [AccountConfig] = []

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
        w.onGOAHint = { [unowned self] in self.goaHints.append($0) }
        w.onTesting = { [unowned self] in self.testing.append($0) }
        w.onFocus = { [unowned self] in self.focus.append($0) }
        w.onToast = { [unowned self] in self.toasts.append($0) }
        w.onDone = { [unowned self] id, cfg in
            self.done.append(id)
            self.doneConfigs.append(cfg)
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
private func startWizard(_ fake: FakeDaemon, editing: Account? = nil) async throws -> (WizardController, Recorder) {
    let client = try await connect(fake)
    let w = WizardController(client: client, editing: editing)
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
}
