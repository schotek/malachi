// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The Jira account assistant (JiraWizardController) against a fake daemon:
// account.detectSite, account.listSpaces, account.add and account.update.
// Fictional sites and people only.

private let cloudId = "0b9e3d2c-1a2b-4c3d-8e9f-001122334455"
private let cloudSiteJSON = #"{"kind":"jira","siteUrl":"https://acme.atlassian.net","deployment":"cloud","cloudId":"0b9e3d2c-1a2b-4c3d-8e9f-001122334455","title":"Acme"}"#
private let dcSiteJSON = #"{"kind":"jira","siteUrl":"https://jira.acme.example/jira","deployment":"datacenter","title":"Acme Jira","version":"9.12.4"}"#
private let spacesJSON = [
    #"{"user":{"name":"Jana Dvořáková","email":"jana@acme.example"},"spaces":["#,
    #"{"id":"10001","key":"ITSD","name":"IT Service Desk","serviceDesk":true,"issues":120},"#,
    #"{"id":"10002","key":"WEB","name":"Website","issues":1},"#,
    #"{"id":"10003","key":"MOB","name":"Mobile","issues":-1}],"#,
    #""statuses":[{"id":"3","name":"In Progress","category":"inProgress"}]}"#,
].joined()
private let recountedJSON = [
    #"{"user":{"name":"Jana Dvořáková","email":"jana@acme.example"},"spaces":["#,
    #"{"id":"10001","key":"ITSD","name":"IT Service Desk","serviceDesk":true,"issues":300},"#,
    #"{"id":"10002","key":"WEB","name":"Website","issues":4},"#,
    #"{"id":"10003","key":"MOB","name":"Mobile","issues":0}]}"#,
].joined()
// A Data Center user whose address the site does not reveal, one space.
private let dcSpacesJSON = #"{"user":{"name":"Jana Dvořáková"},"spaces":[{"id":"20001","key":"OPS","name":"Operations","issues":7}]}"#

/// Records the parameters each method was called with.
private actor ParamsLog {
    var byMethod: [String: [Data]] = [:]

    func record(_ method: String, _ params: Data) {
        byMethod[method, default: []].append(params)
    }

    func count(_ method: String) -> Int {
        byMethod[method]?.count ?? 0
    }

    func last<T: Decodable>(_ method: String, as type: T.Type) throws -> T? {
        guard let data = byMethod[method]?.last else { return nil }
        return try JSONCoding.decoder().decode(type, from: data)
    }

    func lastRaw(_ method: String) -> Data? {
        byMethod[method]?.last
    }
}

private struct Banner: Equatable {
    var page: Jira.Page
    var text: String?
}

private struct Check: Equatable {
    var ok: Bool
    var problem: String
}

private struct EmailField: Equatable {
    var shown: Bool
    var email: String
}

/// Collects what the controller reports.
@MainActor
private final class Recorder {
    var pages: [[Jira.Page]] = []
    var busy: [String?] = []
    var checks: [Check] = []
    var detected: [String] = []
    var credentialPages: [Jira.CredentialPage] = []
    var spaces: [[Jira.SpaceRow]] = []
    var selected: [Set<String>] = []
    var spacesProblems: [String] = []
    var emailFields: [EmailField] = []
    var banners: [Banner] = []
    var problems: [Set<JiraWizardController.Field>] = []
    var focus: [JiraWizardController.Field] = []
    var opened: [String] = []
    var done: [AccountID] = []
    var doneConfigs: [AccountConfig] = []

    func attach(_ w: JiraWizardController) {
        w.onPages = { [unowned self] in self.pages.append($0) }
        w.onBusy = { [unowned self] in self.busy.append($0) }
        w.onSiteCheck = { [unowned self] ok, problem in self.checks.append(Check(ok: ok, problem: problem)) }
        w.onDetected = { [unowned self] in self.detected.append($0) }
        w.onCredentialPage = { [unowned self] in self.credentialPages.append($0) }
        w.onSpaces = { [unowned self] rows, selected in
            self.spaces.append(rows)
            self.selected.append(selected)
        }
        w.onSpacesProblem = { [unowned self] in self.spacesProblems.append($0) }
        w.onEmailField = { [unowned self] shown, email in self.emailFields.append(EmailField(shown: shown, email: email)) }
        w.onBanner = { [unowned self] page, text in self.banners.append(Banner(page: page, text: text)) }
        w.onProblems = { [unowned self] in self.problems.append($0) }
        w.onFocus = { [unowned self] in self.focus.append($0) }
        w.onOpenURL = { [unowned self] in self.opened.append($0) }
        w.onDone = { [unowned self] id, cfg in
            self.done.append(id)
            self.doneConfigs.append(cfg)
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

private func makeFake() async throws -> FakeDaemon {
    let fake = try FakeDaemon()
    try await fake.start()
    return fake
}

/// A started assistant on a connected client.
@MainActor
private func startWizard(
    _ fake: FakeDaemon, editing: Account? = nil, requestToken: ErrorCode? = nil
) async throws -> (JiraWizardController, Recorder) {
    let client = RPCClient(socketPath: fake.path)
    try await client.connect()
    let w = JiraWizardController(client: client, editing: editing)
    if let requestToken {
        w.requestToken(reason: requestToken)
    }
    let rec = Recorder()
    rec.attach(w)
    w.start()
    return (w, rec)
}

/// A Jira Cloud account as account.list returns it.
private func cloudAccount(id: AccountID = "acc-j1") -> Account {
    Account(
        id: id,
        config: AccountConfig(
            name: "Acme", email: "jana@acme.example", kind: .jira,
            jira: JiraConfig(
                siteUrl: "https://acme.atlassian.net", deployment: .cloud, cloudId: cloudId, login: "jana@acme.example",
                spaces: [SpaceRef(id: "10001", key: "ITSD", name: "IT Service Desk"), SpaceRef(id: "10002", key: "WEB")],
                offlineDays: 90, onlyMine: true, hideEvents: true, disabledFolders: [.watching],
                botNames: ["Issue Sync"]
            )
        ),
        enabled: true,
        state: SyncState(accountId: id, status: .authRequired),
        capabilities: []
    )
}

/// The rows of `spacesJSON` on the spaces page.
private let spaceRows = [
    Jira.SpaceRow(id: "10001", title: "ITSD – IT Service Desk", count: "about 120 issues", serviceDesk: true),
    Jira.SpaceRow(id: "10002", title: "WEB – Website", count: "about 1 issue"),
    Jira.SpaceRow(id: "10003", title: "MOB – Mobile", count: ""),
]

@MainActor
@Suite(.serialized) struct JiraWizardControllerTests {
    /// The site of a Jira Cloud found and the credentials page shown.
    private func reachCredentials(_ w: JiraWizardController, _ rec: Recorder) async throws {
        w.setSite("acme.atlassian.net")
        w.next()
        try await waitUntil { rec.pages.last == [.site, .credentials] }
    }

    /// The spaces page reached with valid Jira Cloud credentials.
    private func reachSpaces(_ w: JiraWizardController, _ rec: Recorder) async throws {
        try await reachCredentials(w, rec)
        w.setCredentials(login: "jana@acme.example", token: "tok-123")
        w.next()
        try await waitUntil { rec.pages.last == [.site, .credentials, .spaces] }
    }

    @Test func addsACloudAccount() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let log = ParamsLog()
        await fake.on(API.AccountDetectSite.name) { p in
            await log.record(API.AccountDetectSite.name, p)
            return json(cloudSiteJSON)
        }
        await fake.on(API.AccountListSpaces.name) { p in
            await log.record(API.AccountListSpaces.name, p)
            return json(spacesJSON)
        }
        await fake.on(API.AccountAdd.name) { p in
            await log.record(API.AccountAdd.name, p)
            return json(#"{"accountId":"acc-7"}"#)
        }
        let (w, rec) = try await startWizard(fake)
        #expect(rec.pages == [[.site]])
        #expect(rec.checks == [Check(ok: false, problem: "")])
        #expect(!w.isEditing && !w.canGoBack && w.loginEditable)
        #expect(w.title == "Add Jira Account")
        #expect(w.pageTitle(.site) == "Jira Site")
        #expect(w.pageTitle(.credentials) == "Sign In")
        #expect(w.pageTitle(.spaces) == "Spaces")
        #expect(w.nextLabel(.site) == "Next")
        #expect(w.nextLabel(.credentials) == "Next")
        #expect(w.nextLabel(.spaces) == "Add Account")
        #expect(w.offlineLabels == ["1 week", "1 month", "3 months", "1 year"])
        #expect(w.offlineIndex == 1, "30 days by default")

        // Nothing typed: nothing asked.
        w.next()
        #expect(rec.problems == [[.site]])
        #expect(rec.busy.isEmpty)

        w.setSite("  acme.atlassian.net ")
        #expect(rec.checks.last == Check(ok: true, problem: ""))
        #expect(rec.problems.last == [])
        w.next()
        try await waitUntil { rec.pages.last == [.site, .credentials] }
        #expect(rec.busy == ["Looking up the Jira site", nil])
        #expect(rec.detected == ["Found Acme"])
        #expect(rec.credentialPages.last == Jira.credentialFields(.cloud))
        #expect(rec.focus.last == .login)
        #expect(w.canGoBack)
        let detect = try #require(try await log.last(API.AccountDetectSite.name, as: AccountDetectSiteParams.self))
        #expect(detect.url == "acme.atlassian.net", "sent trimmed")

        // Missing credentials are flagged, nothing is asked.
        w.next()
        #expect(rec.problems.last == [.login, .token])
        #expect(rec.focus.last == .login)
        w.setCredentials(login: "jana@acme.example", token: "tok")
        #expect(rec.problems.last == [])
        w.setCredentials(login: "jana@acme.example", token: " tok-123\n")
        w.next()
        try await waitUntil { rec.pages.last == [.site, .credentials, .spaces] }
        #expect(Array(rec.busy.suffix(2)) == ["Loading the spaces", nil])
        let listed = try #require(try await log.last(API.AccountListSpaces.name, as: AccountListSpacesParams.self))
        #expect(listed.counts == true)
        #expect(listed.accountId == nil)
        #expect(listed.credentials.password == "tok-123", "a pasted line break is trimmed")
        #expect(listed.config.kind == .jira)
        #expect(listed.config.email == "jana@acme.example")
        #expect(listed.config.name == "Acme")
        #expect(listed.config.jira?.login == "jana@acme.example")
        #expect(listed.config.jira?.cloudId == cloudId)
        #expect(listed.config.jira?.spaces == [])
        #expect(listed.config.jira?.offlineDays == 30)
        #expect(rec.spaces == [spaceRows])
        #expect(rec.selected == [[]])
        #expect(rec.spacesProblems.last == "Select at least one space")
        #expect(rec.emailFields.last == EmailField(shown: false, email: ""))
        #expect(w.user == SiteUser(name: "Jana Dvořáková", email: "jana@acme.example"))

        // Add without a space: the page says why.
        w.next()
        #expect(rec.banners.last == Banner(page: .spaces, text: "Select at least one space"))
        #expect(await log.count(API.AccountAdd.name) == 0)

        w.setSpace("10002", selected: true)
        w.setSpace("10001", selected: true)
        w.setSpace("99999", selected: true)
        #expect(w.selected == ["10001", "10002"], "an unknown space is ignored")
        #expect(rec.spacesProblems.last == "")
        w.setOnlyMine(true)
        w.next()
        try await waitUntil { !rec.done.isEmpty }
        #expect(Array(rec.busy.suffix(2)) == ["Adding the account", nil])
        let want = AccountAddParams(
            config: AccountConfig(
                name: "Acme", email: "jana@acme.example", kind: .jira,
                jira: JiraConfig(
                    siteUrl: "https://acme.atlassian.net", deployment: .cloud, cloudId: cloudId, login: "jana@acme.example",
                    spaces: [SpaceRef(id: "10001", key: "ITSD", name: "IT Service Desk"), SpaceRef(id: "10002", key: "WEB", name: "Website")],
                    offlineDays: 30, onlyMine: true
                )
            ),
            credentials: Credentials(password: "tok-123")
        )
        let sent = try #require(try await log.last(API.AccountAdd.name, as: AccountAddParams.self))
        #expect(sent == want)
        // Exactly these members on the wire: nothing a mail account has,
        // no empty Jira list.
        let raw = try #require(await log.lastRaw(API.AccountAdd.name))
        let object = try #require(try JSONSerialization.jsonObject(with: raw) as? [String: Any])
        let config = try #require(object["config"] as? [String: Any])
        #expect(Set(config.keys) == ["name", "email", "kind", "jira"])
        let jira = try #require(config["jira"] as? [String: Any])
        #expect(Set(jira.keys) == ["siteUrl", "deployment", "cloudId", "login", "spaces", "offlineDays", "onlyMine"])
        #expect(rec.done == ["acc-7"])
        #expect(rec.doneConfigs == [want.config])
        #expect(await fake.calls == [API.AccountDetectSite.name, API.AccountListSpaces.name, API.AccountAdd.name])
    }

    @Test func anAddressThatIsNotOneIsRefusedWithoutACall() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let (w, rec) = try await startWizard(fake)
        w.setSite("acme atlassian")
        #expect(rec.checks.last == Check(ok: false, problem: "This is not a web address"))
        w.next()
        #expect(rec.problems.last == [.site])
        #expect(rec.focus.last == .site)
        #expect(rec.busy.isEmpty)
        #expect(await fake.calls.isEmpty)
    }

    @Test func aFailedLookupShowsTheBanner() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDetectSite.name) { _ in throw RPCError(code: .serverError, message: "not jira") }
        let (w, rec) = try await startWizard(fake)
        w.setSite("example.org")
        w.next()
        try await waitUntil { rec.busy.count == 2 }
        #expect(rec.pages == [[.site]])
        #expect(rec.banners.last == Banner(page: .site, text: "This address is not a Jira site"))
        #expect(rec.problems.last == [.site])
        #expect(rec.focus.last == .site)
        #expect(rec.detected.isEmpty)

        // A network failure: the client's sentence for the step.
        let network = RPCError(code: .networkError, message: "no route")
        await fake.on(API.AccountDetectSite.name) { _ in throw network }
        w.next()
        try await waitUntil { rec.busy.count == 4 }
        #expect(rec.banners.last == Banner(page: .site, text: rpcErrorText("Looking up the Jira site", network)))
        #expect(rec.pages == [[.site]])

        // Typing clears the banner and the flag.
        w.setSite("jira.example.org")
        #expect(rec.banners.last == Banner(page: .site, text: nil))
        #expect(rec.problems.last == [])
    }

    @Test func aSiteOfAnotherKindIsNotJira() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDetectSite.name) { _ in
            json(#"{"kind":"tracker","siteUrl":"https://tracker.acme.example","deployment":"cloud"}"#)
        }
        let (w, rec) = try await startWizard(fake)
        w.setSite("tracker.acme.example")
        w.next()
        try await waitUntil { rec.busy.count == 2 }
        #expect(rec.pages == [[.site]])
        #expect(rec.banners.last == Banner(page: .site, text: "This address is not a Jira site"))
        #expect(rec.problems.last == [.site])
        #expect(w.site == nil)
    }

    @Test func aChangedAddressForgetsTheSite() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDetectSite.name) { _ in json(cloudSiteJSON) }
        let (w, rec) = try await startWizard(fake)
        try await reachCredentials(w, rec)
        #expect(w.site?.siteUrl == "https://acme.atlassian.net")
        w.back()
        #expect(rec.pages.last == [.site])
        w.setSite("acme.atlassian.net/")
        #expect(w.site == nil)
        #expect(rec.detected == ["Found Acme", ""])
    }

    @Test func aRefusedTokenGoesBackToTheCredentials() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDetectSite.name) { _ in json(cloudSiteJSON) }
        await fake.on(API.AccountListSpaces.name) { _ in throw RPCError(code: .authFailed, message: "401") }
        let (w, rec) = try await startWizard(fake)
        try await reachCredentials(w, rec)
        w.setCredentials(login: "jana@acme.example", token: "wrong")
        w.next()
        try await waitUntil { rec.busy.count == 4 }
        #expect(rec.pages.last == [.site, .credentials])
        #expect(rec.banners.last == Banner(page: .credentials, text: "The Jira site rejected the token"))
        #expect(rec.problems.last == [.token])
        #expect(rec.focus.last == .token)
        #expect(rec.spaces.isEmpty)

        // The site lists the spaces for the next token, but refuses the
        // add: back to the credentials again.
        await fake.on(API.AccountListSpaces.name) { _ in json(spacesJSON) }
        await fake.on(API.AccountAdd.name) { _ in throw RPCError(code: .authFailed, message: "401") }
        w.setCredentials(login: "jana@acme.example", token: "tok-123")
        #expect(rec.banners.last == Banner(page: .credentials, text: nil))
        #expect(rec.problems.last == [])
        w.next()
        try await waitUntil { rec.pages.last == [.site, .credentials, .spaces] }
        w.setSpace("10001", selected: true)
        w.next()
        try await waitUntil { rec.pages.last == [.site, .credentials] }
        #expect(rec.banners.last == Banner(page: .credentials, text: "The Jira site rejected the token"))
        #expect(rec.problems.last == [.token])
        #expect(rec.done.isEmpty)
    }

    @Test func aReplyAfterBackIsDropped() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDetectSite.name) { _ in json(cloudSiteJSON) }
        await fake.on(API.AccountListSpaces.name) { _ in
            try? await Task.sleep(for: .milliseconds(300))
            return json(spacesJSON)
        }
        let (w, rec) = try await startWizard(fake)
        try await reachCredentials(w, rec)
        w.setCredentials(login: "jana@acme.example", token: "tok-123")
        w.next()
        #expect(w.busy)
        try await waitUntil { await fake.calls.contains(API.AccountListSpaces.name) }
        w.back()
        #expect(rec.pages.last == [.site])
        #expect(!w.busy)
        #expect(rec.busy == ["Looking up the Jira site", nil, "Loading the spaces", nil])
        try await Task.sleep(for: .milliseconds(600))
        #expect(rec.pages.last == [.site], "the late spaces do not push their page")
        #expect(rec.spaces.isEmpty)
        #expect(w.spaces.isEmpty)
        #expect(rec.busy.count == 4)
    }

    @Test func closeDropsLateReplies() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDetectSite.name) { _ in
            try? await Task.sleep(for: .milliseconds(300))
            return json(cloudSiteJSON)
        }
        let (w, rec) = try await startWizard(fake)
        w.setSite("acme.atlassian.net")
        w.next()
        try await waitUntil { await fake.calls.contains(API.AccountDetectSite.name) }
        w.close()
        #expect(w.closed)
        try await Task.sleep(for: .milliseconds(600))
        #expect(rec.pages == [[.site]])
        #expect(rec.detected.isEmpty)
        #expect(w.site == nil)
        // Nothing starts once closed.
        w.next()
        #expect(await fake.calls == [API.AccountDetectSite.name])
    }

    @Test func aConflictStaysOnTheSpaces() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountDetectSite.name) { _ in json(cloudSiteJSON) }
        await fake.on(API.AccountListSpaces.name) { _ in json(spacesJSON) }
        await fake.on(API.AccountAdd.name) { _ in throw RPCError(code: .conflict, message: "exists") }
        let (w, rec) = try await startWizard(fake)
        try await reachSpaces(w, rec)
        w.setSpace("10003", selected: true)
        w.next()
        try await waitUntil { rec.busy.last == .some(nil) && rec.busy.count == 6 }
        #expect(rec.pages.last == [.site, .credentials, .spaces])
        #expect(rec.banners.last == Banner(page: .spaces, text: "An account for this Jira site already exists"))
        #expect(rec.done.isEmpty)
    }

    @Test func aNewOfflineWindowCountsAgain() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let log = ParamsLog()
        await fake.on(API.AccountDetectSite.name) { _ in json(cloudSiteJSON) }
        await fake.on(API.AccountListSpaces.name) { p in
            await log.record(API.AccountListSpaces.name, p)
            let n = await log.count(API.AccountListSpaces.name)
            return json(n == 1 ? spacesJSON : recountedJSON)
        }
        await fake.on(API.AccountAdd.name) { p in
            await log.record(API.AccountAdd.name, p)
            return json(#"{"accountId":"acc-8"}"#)
        }
        let (w, rec) = try await startWizard(fake)
        try await reachSpaces(w, rec)
        w.setSpace("10002", selected: true)
        w.setOfflineIndex(9)
        w.setOfflineIndex(1)
        #expect(rec.spaces.count == 1, "no change, no new count")

        w.setOfflineIndex(2)
        #expect(w.offlineDays == 90)
        #expect(rec.spaces.last?.map(\.count) == ["", "", ""], "the old estimates are gone meanwhile")
        #expect(rec.busy.last == "Loading the spaces")
        try await waitUntil { !w.busy }
        #expect(rec.pages.last == [.site, .credentials, .spaces])
        #expect(rec.spaces.last?.map(\.count) == ["about 300 issues", "about 4 issues", "about 0 issues"])
        #expect(rec.selected.last == ["10002"], "the choice stays")
        let recount = try #require(try await log.last(API.AccountListSpaces.name, as: AccountListSpacesParams.self))
        #expect(recount.config.jira?.offlineDays == 90)
        #expect(recount.counts == true)

        w.next()
        try await waitUntil { !rec.done.isEmpty }
        let sent = try #require(try await log.last(API.AccountAdd.name, as: AccountAddParams.self))
        #expect(sent.config.jira?.offlineDays == 90)
        #expect(sent.config.jira?.spaces == [SpaceRef(id: "10002", key: "WEB", name: "Website")])
        #expect(sent.config.jira?.onlyMine == nil)
    }

    @Test func aDataCenterSiteAsksForTheHiddenAddress() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let log = ParamsLog()
        await fake.on(API.AccountDetectSite.name) { _ in json(dcSiteJSON) }
        await fake.on(API.AccountListSpaces.name) { p in
            await log.record(API.AccountListSpaces.name, p)
            return json(dcSpacesJSON)
        }
        await fake.on(API.AccountAdd.name) { p in
            await log.record(API.AccountAdd.name, p)
            return json(#"{"accountId":"acc-9"}"#)
        }
        let (w, rec) = try await startWizard(fake)
        w.setSite("https://jira.acme.example/jira")
        w.next()
        try await waitUntil { rec.pages.last == [.site, .credentials] }
        #expect(rec.detected == ["Found Acme Jira, version 9.12.4"])
        #expect(rec.credentialPages.last == Jira.credentialFields(.datacenter))
        #expect(rec.focus.last == .token)

        // No token: the page asks for one.
        w.next()
        #expect(rec.problems.last == [.token])
        #expect(rec.banners.last == Banner(page: .credentials, text: "Enter the personal access token for this account"))
        // Data Center has no token page to open.
        w.openTokenHelp()
        #expect(rec.opened.isEmpty)

        w.setCredentials(login: "", token: "pat-1")
        w.next()
        try await waitUntil { rec.pages.last == [.site, .credentials, .spaces] }
        let listed = try #require(try await log.last(API.AccountListSpaces.name, as: AccountListSpacesParams.self))
        #expect(listed.config.jira?.login == nil)
        #expect(listed.config.jira?.cloudId == nil)
        #expect(listed.config.jira?.deployment == .datacenter)
        #expect(listed.credentials.password == "pat-1")
        #expect(rec.emailFields.last == EmailField(shown: true, email: ""))
        #expect(rec.selected.last == ["20001"], "a single space is chosen")
        #expect(rec.spacesProblems.last == "")

        // The address is needed and must be one.
        w.next()
        #expect(rec.problems.last == [.email])
        #expect(rec.focus.last == .email)
        w.setEmail("jana")
        w.next()
        #expect(rec.problems.last == [.email])
        #expect(await log.count(API.AccountAdd.name) == 0)
        w.setEmail(" jana@acme.example ")
        w.next()
        try await waitUntil { !rec.done.isEmpty }
        let sent = try #require(try await log.last(API.AccountAdd.name, as: AccountAddParams.self))
        #expect(sent.config == AccountConfig(
            name: "Acme Jira", email: "jana@acme.example", kind: .jira,
            jira: JiraConfig(
                siteUrl: "https://jira.acme.example/jira", deployment: .datacenter,
                spaces: [SpaceRef(id: "20001", key: "OPS", name: "Operations")], offlineDays: 30
            )
        ))
        #expect(sent.credentials == Credentials(password: "pat-1"))
    }

    @Test func editingReplacesTheTokenOnly() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let log = ParamsLog()
        await fake.on(API.AccountListSpaces.name) { p in
            await log.record(API.AccountListSpaces.name, p)
            return json(#"{"user":{"name":"Jana Dvořáková"},"spaces":[],"statuses":[]}"#)
        }
        await fake.on(API.AccountUpdate.name) { p in
            await log.record(API.AccountUpdate.name, p)
            return json("{}")
        }
        let account = cloudAccount()
        let (w, rec) = try await startWizard(fake, editing: account)
        #expect(w.isEditing)
        #expect(rec.pages == [[.credentials]])
        #expect(!w.canGoBack)
        #expect(w.title == "Edit Account")
        #expect(w.nextLabel(.credentials) == "Save")
        #expect(!w.loginEditable)
        #expect(w.login == "jana@acme.example")
        #expect(rec.credentialPages == [Jira.credentialFields(.cloud)])
        #expect(rec.banners.isEmpty, "no reason, no banner")

        // The token is required; the login cannot change here.
        w.next()
        #expect(rec.banners.last == Banner(page: .credentials, text: "Enter the API token for this account"))
        #expect(rec.problems.last == [.token])
        w.setCredentials(login: "other@acme.example", token: " tok-new ")
        #expect(w.login == "jana@acme.example")
        w.openTokenHelp()
        #expect(rec.opened == [Jira.tokenHelpURL])
        w.next()
        try await waitUntil { !rec.done.isEmpty }
        #expect(rec.busy == ["Saving the account", nil])
        let sent = try #require(try await log.last(API.AccountUpdate.name, as: AccountUpdateParams.self))
        #expect(sent == AccountUpdateParams(accountId: "acc-j1", config: account.config, credentials: Credentials(password: "tok-new")))
        #expect(rec.done == ["acc-j1"])
        #expect(rec.doneConfigs == [account.config])
        let tried = try #require(try await log.last(API.AccountListSpaces.name, as: AccountListSpacesParams.self))
        #expect(tried == AccountListSpacesParams(accountId: "acc-j1", config: account.config, credentials: Credentials(password: "tok-new")))
        #expect(await fake.calls == [API.AccountListSpaces.name, API.AccountUpdate.name], "the token is tried before it is stored")
    }

    @Test func editingKeepsTheStoredTokenWhenTheNewOneIsRefused() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountListSpaces.name) { _ in throw RPCError(code: .authFailed, message: "401") }
        let (w, rec) = try await startWizard(fake, editing: cloudAccount())
        w.setCredentials(login: "", token: "wrong")
        w.next()
        try await waitUntil { rec.busy.count == 2 }
        #expect(rec.banners.last == Banner(page: .credentials, text: "The Jira site rejected the token"))
        #expect(rec.problems.last == [.token])
        #expect(rec.done.isEmpty)
        #expect(await fake.calls == [API.AccountListSpaces.name], "account.update is never sent")
    }

    @Test func editingAsksForTheTokenWithTheReason() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountListSpaces.name) { _ in throw RPCError(code: .authFailed, message: "401") }
        let (w, rec) = try await startWizard(fake, editing: cloudAccount(), requestToken: .authFailed)
        #expect(rec.banners == [Banner(page: .credentials, text: "The Jira site rejected the token")])
        #expect(rec.problems == [[.token]])
        #expect(rec.focus == [.token])
        w.requestToken(reason: .authRequired)
        #expect(rec.banners.last == Banner(page: .credentials, text: "Enter the API token for this account"))

        // A refused save stays on the page with the reason.
        w.setCredentials(login: "", token: "still-wrong")
        w.next()
        try await waitUntil { rec.busy.count == 2 }
        #expect(rec.pages == [[.credentials]])
        #expect(rec.banners.last == Banner(page: .credentials, text: "The Jira site rejected the token"))
        #expect(rec.done.isEmpty)
    }

    @Test func requestTokenIsIgnoredWhenAdding() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let (_, rec) = try await startWizard(fake, requestToken: .authFailed)
        #expect(rec.banners.isEmpty)
        #expect(rec.pages == [[.site]])
    }
}
