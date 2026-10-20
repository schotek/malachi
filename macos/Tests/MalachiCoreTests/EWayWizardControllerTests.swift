// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The eWay-CRM account assistant (EWayWizardController) against a fake
// daemon: ui/internal/accountwizard eway_flow_test.go, ported.
// Fictional instances and people only.

private let detectedJSON = #"{"serviceUrl":"https://hosting.eway-crm.com/acme","microsoftSignIn":false,"version":"10.0"}"#
private let microsoftJSON = #"{"serviceUrl":"https://hosting.eway-crm.com/acme","microsoftSignIn":true}"#
private let testOKJSON = #"{"eway":{"ok":true,"latencyMs":120}}"#
private let addedJSON = #"{"accountId":"acc-e1"}"#

/// Records the parameters each method was called with, in order.
private actor CallLog {
    var calls: [(String, Data)] = []

    func record(_ method: String, _ params: Data) {
        calls.append((method, params))
    }

    func count(_ method: String) -> Int {
        calls.filter { $0.0 == method }.count
    }

    var methods: [String] { calls.map(\.0) }

    func last<T: Decodable>(_ method: String, as type: T.Type) throws -> T? {
        guard let data = calls.last(where: { $0.0 == method })?.1 else { return nil }
        return try JSONCoding.decoder().decode(type, from: data)
    }
}

private struct Banner: Equatable {
    var page: CRM.WizardPage
    var text: String?
}

private struct Check: Equatable {
    var ok: Bool
    var problem: String
}

@MainActor
private final class Recorder {
    var pages: [[CRM.WizardPage]] = []
    var busy: [String?] = []
    var checks: [Check] = []
    var names: [String] = []
    var banners: [Banner] = []
    var problems: [CRM.WizardField] = []
    var focus: [CRM.WizardField] = []
    var done: [AccountID] = []
    var configs: [AccountConfig] = []

    func attach(_ w: EWayWizardController) {
        w.onPages = { [unowned self] in self.pages.append($0) }
        w.onBusy = { [unowned self] in self.busy.append($0) }
        w.onServiceCheck = { [unowned self] ok, problem in self.checks.append(Check(ok: ok, problem: problem)) }
        w.onName = { [unowned self] in self.names.append($0) }
        w.onBanner = { [unowned self] page, text in self.banners.append(Banner(page: page, text: text)) }
        w.onProblem = { [unowned self] in self.problems.append($0) }
        w.onFocus = { [unowned self] in self.focus.append($0) }
        w.onDone = { [unowned self] id, cfg in
            self.done.append(id)
            self.configs.append(cfg)
        }
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

/// A fake daemon answering `replies` (method → JSON or error) and logging
/// every call.
private func makeFake(_ log: CallLog, _ replies: [String: Result<String, RPCError>]) async throws -> FakeDaemon {
    let fake = try FakeDaemon()
    try await fake.start()
    for (method, reply) in replies {
        await fake.on(method) { p in
            await log.record(method, p)
            switch reply {
            case .success(let s): return json(s)
            case .failure(let e): throw e
            }
        }
    }
    return fake
}

@MainActor
private func startWizard(_ fake: FakeDaemon, editing: Account? = nil) async throws -> (EWayWizardController, Recorder) {
    let client = RPCClient(socketPath: fake.path)
    try await client.connect()
    let w = EWayWizardController(client: client, editing: editing)
    let rec = Recorder()
    rec.attach(w)
    w.start()
    return (w, rec)
}

private func ewayAccount() -> Account {
    Account(
        id: "acc-e1",
        config: AccountConfig(
            name: "Acme", email: "jana@acme.example", kind: .eway,
            eway: EwayConfig(serviceUrl: "https://hosting.eway-crm.com/acme", username: "jana",
                             userGuid: "0b9e3d2c-1a2b-4c3d-8e9f-001122334455")),
        enabled: true, state: SyncState(accountId: "acc-e1", status: .idle), capabilities: [.crm])
}

@MainActor
@Suite(.serialized) struct EWayWizardControllerTests {
    @Test func adds() async throws {
        let log = CallLog()
        let fake = try await makeFake(log, [
            API.AccountDetectEway.name: .success(detectedJSON),
            API.AccountTest.name: .success(testOKJSON),
            API.AccountAdd.name: .success(addedJSON),
        ])
        defer { Task { await fake.stop() } }
        let (w, rec) = try await startWizard(fake)
        #expect(rec.checks.last?.ok == false && w.title == "Add eWay-CRM Account" && w.nextLabel(.service) == "_Next" && !w.canGoBack)
        w.setService(" hosting.eway-crm.com/acme/API.svc ")
        #expect(rec.checks.last?.ok == true)
        w.next()
        #expect(rec.busy.last == "Contacting eWay-CRM")
        try await waitUntil { rec.pages.last == [.service, .credentials] }
        let detect = try #require(try await log.last(API.AccountDetectEway.name, as: AccountDetectEwayParams.self))
        #expect(detect.serviceUrl == "hosting.eway-crm.com/acme/API.svc")
        #expect(rec.names.last == "acme" && w.serviceURL == "https://hosting.eway-crm.com/acme" && rec.focus.last == .user)
        #expect(w.nextLabel(.credentials) == "_Add Account" && w.passwordLabel == "Password" && w.pageTitle(.credentials) == "Sign In")

        // No password: flagged, nothing asked.
        w.setCredentials(user: "jana", password: "")
        w.next()
        #expect(rec.problems.last == .password && rec.banners.last?.text == "Enter the password for this account")
        #expect(await log.count(API.AccountTest.name) == 0)
        w.setName("Acme CRM")
        w.setCredentials(user: " jana ", password: "s3cret pass")
        w.next()
        try await waitUntil { rec.done.count == 1 }
        let tp = try #require(try await log.last(API.AccountTest.name, as: AccountTestParams.self))
        #expect(tp.accountId == nil && tp.credentials.password == "s3cret pass" && tp.config.kind == .eway)
        #expect(tp.config.eway == EwayConfig(serviceUrl: "https://hosting.eway-crm.com/acme", username: "jana", followThreads: true))
        let ap = try #require(try await log.last(API.AccountAdd.name, as: AccountAddParams.self))
        #expect(ap.config.name == "Acme CRM" && ap.config.kind == .eway && ap.credentials.password == "s3cret pass")
        #expect(ap.config.eway == EwayConfig(serviceUrl: "https://hosting.eway-crm.com/acme", username: "jana", followThreads: true))
        #expect(rec.done == ["acc-e1"])
        #expect(await log.methods == [API.AccountDetectEway.name, API.AccountTest.name, API.AccountAdd.name])
        #expect(rec.busy.contains("Signing in") && rec.busy.contains("Adding the account") && rec.busy.last == .some(nil))
        #expect(EWayWizardController.saveTimeout == .seconds(60) && EWayWizardController.detectTimeout == .seconds(15))
    }

    /// http is refused before anything is asked; a Microsoft instance is
    /// told on the address page, and no password page follows.
    @Test func refusesHTTPAndMicrosoft() async throws {
        let log = CallLog()
        let fake = try await makeFake(log, [API.AccountDetectEway.name: .success(microsoftJSON)])
        defer { Task { await fake.stop() } }
        let (w, rec) = try await startWizard(fake)
        w.setService("http://hosting.eway-crm.com/acme")
        #expect(rec.checks.last == Check(ok: false, problem: "Malachi Mail connects to eWay-CRM over https only."))
        w.next()
        #expect(await log.count(API.AccountDetectEway.name) == 0 && rec.problems.last == .service)
        w.setService("hosting.eway-crm.com/acme")
        w.next()
        try await waitUntil { rec.busy.last == .some(nil) && rec.banners.last?.text != nil }
        #expect(rec.banners.last == Banner(
            page: .service, text: "This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet."))
        #expect(w.top == .service && rec.problems.last == .service && w.service == nil)
    }

    @Test func errors() async throws {
        let log = CallLog()
        let fake = try await makeFake(log, [
            API.AccountDetectEway.name: .failure(RPCError(code: .networkError, message: "dial")),
        ])
        defer { Task { await fake.stop() } }
        let (w, rec) = try await startWizard(fake)
        w.setService("crm.example.org")
        w.next()
        try await waitUntil { rec.busy.count == 2 && rec.busy.last == .some(nil) }
        #expect(rec.banners.last == Banner(page: .service, text: "Contacting eWay-CRM failed: the server could not be reached"))

        // A wrong password (in the test's endpoint result) flags the password.
        await fake.on(API.AccountDetectEway.name) { _ in json(detectedJSON) }
        await fake.on(API.AccountTest.name) { p in
            await log.record(API.AccountTest.name, p)
            return json(#"{"eway":{"ok":false,"latencyMs":0,"error":{"code":1201,"message":"eway: LogIn: rcLoginFailed"}}}"#)
        }
        w.next()
        try await waitUntil { rec.pages.last == [.service, .credentials] }
        w.setCredentials(user: "jana", password: "wrong")
        w.next()
        try await waitUntil { rec.problems.last == .password }
        #expect(rec.banners.last == Banner(page: .credentials, text: "The server rejected the user name or password"))
        #expect(await log.count(API.AccountAdd.name) == 0)

        // A plan without API access, at add (the daemon signs in again).
        await fake.on(API.AccountTest.name) { _ in json(testOKJSON) }
        await fake.on(API.AccountAdd.name) { _ in
            throw RPCError(code: .crmSignInUnsupported, message: "plan", data: .object(["reason": .string("plan")]))
        }
        w.setCredentials(user: "jana", password: "right")
        w.next()
        try await waitUntil { rec.banners.last?.text == "This eWay-CRM does not allow access through its API." }
        #expect(w.top == .credentials && rec.done.isEmpty)

        // Microsoft found only by account.add: back to the address page.
        await fake.on(API.AccountAdd.name) { _ in
            throw RPCError(code: .crmSignInUnsupported, message: "ms", data: .object(["reason": .string("microsoft")]))
        }
        w.next()
        try await waitUntil { rec.pages.last == [.service] }
        #expect(rec.problems.last == .service)
    }

    /// Back drops the reply of the call under way; a changed address
    /// forgets the instance; a closed assistant takes no late reply.
    @Test func backDropsReply() async throws {
        let log = CallLog()
        let fake = try await makeFake(log, [:])
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDetectEway.name) { p in
            await log.record(API.AccountDetectEway.name, p)
            try? await Task.sleep(for: .milliseconds(200))
            return json(detectedJSON)
        }
        var (w, rec) = try await startWizard(fake)
        w.setService("hosting.eway-crm.com/acme")
        w.next()
        w.next()
        try await waitUntil { await log.count(API.AccountDetectEway.name) == 1 }
        w.close()
        try await Task.sleep(for: .milliseconds(400))
        let detects = await log.count(API.AccountDetectEway.name)
        #expect(rec.pages.count == 1 && detects == 1, "a late reply after close")

        let log2 = CallLog()
        let fake2 = try await makeFake(log2, [API.AccountDetectEway.name: .success(detectedJSON)])
        defer { Task { await fake2.stop() } }
        await fake2.on(API.AccountTest.name) { p in
            await log2.record(API.AccountTest.name, p)
            try? await Task.sleep(for: .milliseconds(200))
            return json(testOKJSON)
        }
        (w, rec) = try await startWizard(fake2)
        w.setService("hosting.eway-crm.com/acme")
        w.next()
        try await waitUntil { rec.pages.last == [.service, .credentials] }
        w.setCredentials(user: "jana", password: "pw")
        w.next()
        try await waitUntil { await log2.count(API.AccountTest.name) == 1 }
        w.back()
        try await Task.sleep(for: .milliseconds(400))
        #expect(await log2.count(API.AccountAdd.name) == 0 && w.top == .service && !w.busy)
        w.setService("crm.example.org")
        #expect(w.service == nil, "a changed address keeps the instance")
    }

    /// Editing: on the sign-in page with the account's address, name and
    /// user; an empty password keeps the stored one (account.test with
    /// accountId), unless the user name changed.
    @Test func edits() async throws {
        let log = CallLog()
        let fake = try await makeFake(log, [
            API.AccountTest.name: .success(testOKJSON), API.AccountUpdate.name: .success("{}"),
        ])
        defer { Task { await fake.stop() } }
        let acc = ewayAccount()
        var (w, rec) = try await startWizard(fake, editing: acc)
        #expect(w.title == "Edit Account" && w.top == .credentials && !w.canGoBack && w.nextLabel(.credentials) == "_Save")
        #expect(w.passwordLabel == "New Password (leave empty to keep)" && rec.names.last == "Acme" && w.user == "jana")
        #expect(w.serviceURL == "https://hosting.eway-crm.com/acme")
        w.setService("https://other.example.org")
        #expect(w.serviceURL == "https://hosting.eway-crm.com/acme", "the address changed when editing")
        w.setName("Acme Sales")
        w.next()
        try await waitUntil { rec.done.count == 1 }
        let tp = try #require(try await log.last(API.AccountTest.name, as: AccountTestParams.self))
        #expect(tp.accountId == "acc-e1" && (tp.credentials.password ?? "").isEmpty && tp.config.eway?.username == "jana")
        let up = try #require(try await log.last(API.AccountUpdate.name, as: AccountUpdateParams.self))
        #expect(up.accountId == "acc-e1" && up.config.name == "Acme Sales" && up.config.email == "jana@acme.example")
        #expect(up.config.eway?.userGuid == acc.config.eway?.userGuid && (up.credentials.password ?? "").isEmpty)
        #expect(rec.busy.contains("Saving the account"))

        // Another user needs a password, and forgets the user GUID.
        let log2 = CallLog()
        let fake2 = try await makeFake(log2, [
            API.AccountTest.name: .success(testOKJSON), API.AccountUpdate.name: .success("{}"),
        ])
        defer { Task { await fake2.stop() } }
        (w, rec) = try await startWizard(fake2, editing: acc)
        w.setCredentials(user: "petr", password: "")
        w.next()
        #expect(await log2.count(API.AccountTest.name) == 0 && rec.problems.last == .password)
        w.setCredentials(user: "petr", password: "pw2")
        w.next()
        try await waitUntil { rec.done.count == 1 }
        let up2 = try #require(try await log2.last(API.AccountUpdate.name, as: AccountUpdateParams.self))
        #expect(up2.config.eway?.username == "petr" && up2.config.eway?.userGuid == nil && up2.credentials.password == "pw2")

        // A failed update: the client's sentence, on the sign-in page.
        let log3 = CallLog()
        let fake3 = try await makeFake(log3, [
            API.AccountTest.name: .success(testOKJSON),
            API.AccountUpdate.name: .failure(RPCError(code: .keyringError, message: "boom")),
        ])
        defer { Task { await fake3.stop() } }
        (w, rec) = try await startWizard(fake3, editing: acc)
        w.next()
        try await waitUntil { rec.banners.last?.text == "Saving the account failed: the system keyring is unavailable" }
        #expect(rec.done.isEmpty && w.top == .credentials)
    }

    /// D22: the check box "Also save later messages of saved
    /// conversations": on for a new account, the account's value when
    /// editing, sent with account.test, account.add and account.update
    /// (eway_flow_test.go `TestEwayFlowFollowThreads`).
    @Test func followThreads() async throws {
        let log = CallLog()
        let fake = try await makeFake(log, [
            API.AccountDetectEway.name: .success(detectedJSON),
            API.AccountTest.name: .success(testOKJSON),
            API.AccountAdd.name: .success(addedJSON),
        ])
        defer { Task { await fake.stop() } }
        var (w, rec) = try await startWizard(fake)
        #expect(w.followThreads, "a new account follows saved conversations")
        w.setService("hosting.eway-crm.com/acme")
        w.next()
        try await waitUntil { rec.pages.last == [.service, .credentials] }
        w.setFollowThreads(false)
        w.setCredentials(user: "jana", password: "pw")
        w.next()
        try await waitUntil { rec.done.count == 1 }
        let ap = try #require(try await log.last(API.AccountAdd.name, as: AccountAddParams.self))
        #expect(ap.config.eway?.followThreads == false)

        let log2 = CallLog()
        let fake2 = try await makeFake(log2, [
            API.AccountTest.name: .success(testOKJSON), API.AccountUpdate.name: .success("{}"),
        ])
        defer { Task { await fake2.stop() } }
        let acc = ewayAccount()
        (w, rec) = try await startWizard(fake2, editing: acc)
        #expect(!w.followThreads, "the edited account's value")
        w.setFollowThreads(true)
        w.next()
        try await waitUntil { rec.done.count == 1 }
        let tp = try #require(try await log2.last(API.AccountTest.name, as: AccountTestParams.self))
        let up = try #require(try await log2.last(API.AccountUpdate.name, as: AccountUpdateParams.self))
        #expect(tp.config.eway?.followThreads == true && up.config.eway?.followThreads == true)
        #expect(acc.config.eway?.followThreads == false)
        let t = CRM.wizardTexts()
        #expect(t.followThreads == "Also save later messages of saved conversations")
        #expect(t.followThreadsHint == "Replies and new messages in a conversation you saved are saved to eWay-CRM with the same links.")
    }
}
