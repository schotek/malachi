// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The settings of a Jira account (JiraAccountController) against a fake
// daemon: account.listSpaces with the stored token and account.update
// with empty credentials. Fictional sites and people only.

private let listingJSON = [
    #"{"user":{"name":"Jana Dvořáková","email":"jana@acme.example"},"spaces":["#,
    #"{"id":"10001","key":"ITSD","name":"IT Service Desk","serviceDesk":true,"issues":-1},"#,
    #"{"id":"10003","key":"MOB","name":"Mobile","issues":-1},"#,
    #"{"id":"10002","key":"WEB","name":"Website","issues":-1}],"#,
    #""statuses":[{"id":"1","name":"Open","category":"todo"},"#,
    #"{"id":"3","name":"In Progress","category":"inProgress"},"#,
    #"{"id":"5","name":"Resolved","category":"done"},"#,
    #"{"id":"6","name":"Done","category":"done"},"#,
    #"{"id":"10010","name":"Done","category":"done"}]}"#,
].joined()

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

/// Collects what the controller reports.
@MainActor
private final class Recorder {
    var changes = 0
    var busy: [String?] = []
    var banners: [String?] = []
    var done: [AccountID] = []
    var doneConfigs: [AccountConfig] = []
    var closes = 0
    var tokenRequests: [AccountID] = []

    func attach(_ c: JiraAccountController) {
        c.onChange = { [unowned self] in self.changes += 1 }
        c.onBusy = { [unowned self] in self.busy.append($0) }
        c.onBanner = { [unowned self] in self.banners.append($0) }
        c.onDone = { [unowned self] id, cfg in
            self.done.append(id)
            self.doneConfigs.append(cfg)
        }
        c.onClose = { [unowned self] in self.closes += 1 }
        c.onReplaceToken = { [unowned self] in self.tokenRequests.append($0.id) }
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

/// The stored account, as account.list returns it.
private func storedAccount(_ edit: (inout JiraConfig) -> Void = { _ in }) -> Account {
    var cfg = jiraCloudAccount()
    if var jc = cfg.jira {
        edit(&jc)
        cfg.jira = jc
    }
    return Account(
        id: "acc-j1", config: cfg, enabled: true, state: SyncState(accountId: "acc-j1", status: .idle),
        capabilities: [.comment, .forward])
}

/// A controller on a connected client, its callbacks recorded; not
/// started.
@MainActor
private func makeController(_ fake: FakeDaemon, _ account: Account) async throws -> (JiraAccountController, Recorder) {
    let client = RPCClient(socketPath: fake.path)
    try await client.connect()
    let c = JiraAccountController(client: client, account: account)
    let rec = Recorder()
    rec.attach(c)
    return (c, rec)
}

@MainActor
@Suite(.serialized) struct JiraAccountControllerTests {
    /// A fake that lists the spaces and accepts the update, both recorded.
    private func listingFake(_ log: ParamsLog) async throws -> FakeDaemon {
        let fake = try await makeFake()
        await fake.on(API.AccountListSpaces.name) { p in
            await log.record(API.AccountListSpaces.name, p)
            return json(listingJSON)
        }
        await fake.on(API.AccountUpdate.name) { p in
            await log.record(API.AccountUpdate.name, p)
            return json("{}")
        }
        return fake
    }

    @Test func loadsTheSpacesAndStatusesWithTheStoredToken() async throws {
        let log = ParamsLog()
        let fake = try await listingFake(log)
        defer { Task { await fake.stop() } }
        let account = storedAccount()
        let (c, rec) = try await makeController(fake, account)

        // Before the listing: what is stored.
        #expect(c.title == "Jira Account")
        #expect(c.saveLabel == "Save")
        #expect(c.site == Jira.SiteInfo(
            address: "https://acme.atlassian.net", deployment: "Jira Cloud", user: "jana@acme.example", tokenLabel: "API Token"))
        #expect(c.spaceRows.map(\.id) == ["10001", "10002"])
        #expect(c.selectedSpaces == ["10001", "10002"])
        #expect(c.statusGroups.isEmpty)
        #expect(c.offlineLabels == ["1 week", "1 month", "3 months", "1 year"] && c.offlineIndex == 1)
        #expect(c.notificationIndex == 0 && c.notificationHint == "" && c.sendersEditable)
        #expect(c.sendersPlaceholder == "@acme.atlassian.net")
        #expect(Jira.virtualFolders.allSatisfy { c.folderShown($0) })
        #expect(!c.isChanged && c.canSave && !c.busy)

        c.start()
        #expect(c.busy && !c.saving)
        #expect(rec.busy == ["Loading the spaces"])
        try await waitUntil { c.listing != nil }
        #expect(rec.busy == ["Loading the spaces", nil])
        #expect(rec.banners.isEmpty)
        #expect(rec.changes == 2, "the initial state and the listing")

        let sent = try #require(try await log.last(API.AccountListSpaces.name, as: AccountListSpacesParams.self))
        #expect(sent.accountId == "acc-j1")
        #expect(sent.config == account.config)
        #expect(sent.credentials == Credentials(), "no token: the daemon takes the stored one")
        #expect(sent.counts != true)
        let raw = try #require(await log.lastRaw(API.AccountListSpaces.name))
        let object = try #require(try JSONSerialization.jsonObject(with: raw) as? [String: Any])
        #expect((object["credentials"] as? [String: Any])?.isEmpty == true)

        #expect(c.site.user == "Jana Dvořáková" && c.site.userDetail == "jana@acme.example")
        #expect(c.spaceRows == [
            Jira.SpaceRow(id: "10001", title: "ITSD – IT Service Desk", serviceDesk: true),
            Jira.SpaceRow(id: "10003", title: "MOB – Mobile"),
            Jira.SpaceRow(id: "10002", title: "WEB – Website"),
        ])
        #expect(c.selectedSpaces == ["10001", "10002"])
        #expect(c.statusGroups.map(\.title) == ["To Do", "In Progress", "Done"])
        #expect(c.statusGroups.last?.choices == [
            Jira.StatusChoice(name: "Resolved", ids: ["5"], selected: true),
            Jira.StatusChoice(name: "Done", ids: ["6", "10010"], selected: true),
        ])
        #expect(!c.isChanged)
        #expect(await fake.calls == [API.AccountListSpaces.name])
    }

    @Test func aFailedListingStillEditsWhatIsStored() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let log = ParamsLog()
        await fake.on(API.AccountListSpaces.name) { _ in throw RPCError(code: .authFailed, message: "401") }
        await fake.on(API.AccountUpdate.name) { p in
            await log.record(API.AccountUpdate.name, p)
            return json("{}")
        }
        let account = storedAccount { $0.closedStatuses = [StatusRef(id: "5", name: "Resolved")] }
        let (c, rec) = try await makeController(fake, account)
        c.start()
        try await waitUntil { rec.busy.count == 2 }
        #expect(rec.banners == ["The Jira site rejected the token"])
        #expect(c.banner == "The Jira site rejected the token")
        #expect(c.listing == nil)
        // The stored spaces and statuses are what there is to choose from.
        #expect(c.spaceRows.map(\.title) == ["ITSD – IT Service Desk", "WEB – Website"])
        #expect(c.statusGroups == [Jira.StatusGroup(
            category: "", title: "Other", style: .plain,
            choices: [Jira.StatusChoice(name: "Resolved", ids: ["5"], selected: true)])])
        #expect(c.site.user == "jana@acme.example")

        // Editing keeps the banner of the listing; Save stores the change.
        c.setSpace("10002", selected: false)
        c.setOnlyMine(true)
        #expect(rec.banners.count == 1)
        #expect(c.isChanged && c.canSave)
        c.save()
        try await waitUntil { !rec.done.isEmpty }
        let sent = try #require(try await log.last(API.AccountUpdate.name, as: AccountUpdateParams.self))
        #expect(sent.config.jira?.spaces == [SpaceRef(id: "10001", key: "ITSD", name: "IT Service Desk")])
        #expect(sent.config.jira?.onlyMine == true)
        #expect(sent.config.jira?.closedStatuses == [StatusRef(id: "5", name: "Resolved")])

        // Other failures: the client's sentence for the step.
        let network = RPCError(code: .networkError, message: "no route")
        await fake.on(API.AccountListSpaces.name) { _ in throw network }
        let (c2, rec2) = try await makeController(fake, account)
        c2.start()
        try await waitUntil { rec2.busy.count == 2 }
        #expect(rec2.banners == [rpcErrorText("Loading the spaces", network)])
        await fake.on(API.AccountListSpaces.name) { _ in throw RPCError(code: .authRequired, message: "no token") }
        let (c3, rec3) = try await makeController(fake, account)
        c3.start()
        try await waitUntil { rec3.busy.count == 2 }
        #expect(rec3.banners == ["Enter the API token for this account"])
    }

    @Test func savesTheFormAndKeepsTheToken() async throws {
        let log = ParamsLog()
        let fake = try await listingFake(log)
        defer { Task { await fake.stop() } }
        let account = storedAccount()
        let (c, rec) = try await makeController(fake, account)
        c.start()
        try await waitUntil { c.listing != nil }

        c.setName("  Acme Jira ")
        c.setSpace("10003", selected: true)
        c.setSpace("10001", selected: false)
        c.setSpace("99999", selected: true)
        #expect(c.selectedSpaces == ["10002", "10003"], "an unknown space is ignored")
        c.setOfflineIndex(2)
        c.setOfflineIndex(17)
        #expect(c.offlineIndex == 2)
        c.setOnlyMine(true)
        c.setShowEvents(false)
        c.setFolder(.watching, shown: false)
        c.setFolder("archive", shown: false)
        #expect(!c.folderShown(.watching) && c.folderShown(.open))
        let resolved = try #require(c.statusGroups.last?.choices.first)
        c.setStatus(resolved, selected: false)
        #expect(c.statusGroups.last?.choices.map(\.selected) == [false, true])
        c.setNotificationIndex(1)
        #expect(c.notificationHint == "Hidden e-mails stay in your mailbox and come back when you turn this off")
        #expect(c.addEntry(.senders, " Jira@Acme.Example "))
        #expect(c.addEntry(.botNames, Jira.suggestedBotName))
        #expect(c.addEntry(.metadataFilters, Jira.suggestedMetadataFilter))
        #expect(c.addEntry(.authorPrefixes, "ACME"))
        #expect(c.isChanged && c.canSave)
        let changes = rec.changes

        c.save()
        #expect(c.saving && c.busy && !c.canSave)
        #expect(rec.changes == changes + 1)
        // While it saves the page waits.
        c.setOnlyMine(false)
        #expect(!c.addEntry(.authorPrefixes, "Globex"))
        c.save()
        try await waitUntil { !rec.done.isEmpty }
        #expect(Array(rec.busy.suffix(2)) == ["Saving the account", nil])
        #expect(!c.saving)

        var want = account.config
        want.name = "Acme Jira"
        want.jira?.spaces = [SpaceRef(id: "10003", key: "MOB", name: "Mobile"), SpaceRef(id: "10002", key: "WEB", name: "Website")]
        want.jira?.offlineDays = 90
        want.jira?.onlyMine = true
        want.jira?.hideEvents = true
        want.jira?.disabledFolders = [.watching]
        want.jira?.closedStatuses = [StatusRef(id: "6", name: "Done"), StatusRef(id: "10010", name: "Done")]
        want.jira?.notificationMail = .hide
        want.jira?.notificationSenders = ["jira@acme.example"]
        want.jira?.botNames = [Jira.suggestedBotName]
        want.jira?.metadataFilters = [Jira.suggestedMetadataFilter]
        want.jira?.authorPrefixes = ["ACME"]
        let sent = try #require(try await log.last(API.AccountUpdate.name, as: AccountUpdateParams.self))
        #expect(sent == AccountUpdateParams(accountId: "acc-j1", config: want, credentials: Credentials()))
        // No password on the wire: the daemon keeps the stored token.
        let raw = try #require(await log.lastRaw(API.AccountUpdate.name))
        let object = try #require(try JSONSerialization.jsonObject(with: raw) as? [String: Any])
        #expect((object["credentials"] as? [String: Any])?.isEmpty == true)
        #expect(rec.done == ["acc-j1"] && rec.doneConfigs == [want])
        #expect(rec.closes == 0)
        #expect(await log.count(API.AccountUpdate.name) == 1)
        #expect(rec.banners.isEmpty)
    }

    @Test func aFormThatChangesNothingClosesWithoutACall() async throws {
        let log = ParamsLog()
        let fake = try await listingFake(log)
        defer { Task { await fake.stop() } }
        // The stored account spells its defaults out.
        let account = storedAccount {
            $0.offlineDays = 30
            $0.notificationMail = .sync
        }
        let (c, rec) = try await makeController(fake, account)
        c.start()
        try await waitUntil { c.listing != nil }
        // Changes that come back to where they were.
        c.setOnlyMine(true)
        c.setOnlyMine(false)
        c.setSpace("10001", selected: false)
        c.setSpace("10001", selected: true)
        c.setName(" Acme ")
        let done = try #require(c.statusGroups.last?.choices.last)
        c.setStatus(done, selected: false)
        c.setStatus(done, selected: true)
        #expect(c.form.closedStatuses.isEmpty, "the statuses of the category done are the default")
        #expect(!c.isChanged)
        c.save()
        #expect(rec.closes == 1)
        #expect(!c.busy && rec.done.isEmpty)
        try await Task.sleep(for: .milliseconds(50))
        #expect(await log.count(API.AccountUpdate.name) == 0)
    }

    @Test func aRefusedSaveSaysWhy() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountListSpaces.name) { _ in json(listingJSON) }
        await fake.on(API.AccountUpdate.name) { _ in throw RPCError(code: .conflict, message: "exists") }
        let (c, rec) = try await makeController(fake, storedAccount())
        c.start()
        try await waitUntil { c.listing != nil }
        c.setOnlyMine(true)
        c.save()
        try await waitUntil { rec.banners.count == 1 }
        #expect(rec.banners == ["An account for this Jira site already exists"])
        #expect(!c.saving && !c.busy && c.canSave)
        #expect(rec.done.isEmpty && rec.closes == 0)
        #expect(c.form.onlyMine, "the form stays as it was")

        // The next change takes the banner away.
        c.setShowEvents(false)
        #expect(rec.banners.last == .some(nil))
        #expect(c.banner == nil)

        let invalid = RPCError(code: .invalidArgument, message: "jira: metadataFilters entry 1 is not a valid RE2 pattern")
        await fake.on(API.AccountUpdate.name) { _ in throw invalid }
        c.save()
        try await waitUntil { rec.banners.count == 3 }
        #expect(rec.banners.last == rpcErrorText("Saving the account", invalid))
        #expect(c.banner?.hasPrefix("Saving the account was rejected") == true)

        await fake.on(API.AccountUpdate.name) { _ in throw RPCError(code: .authFailed, message: "401") }
        c.save()
        try await waitUntil { rec.banners.last == "The Jira site rejected the token" }
        await fake.on(API.AccountUpdate.name) { _ in throw RPCError(code: .keyringError, message: "locked") }
        c.save()
        try await waitUntil { c.banner == rpcErrorText("Saving the account", RPCError(code: .keyringError, message: "locked")) }
    }

    @Test func aFormThatCannotBeSavedIsNotSent() async throws {
        let log = ParamsLog()
        let fake = try await listingFake(log)
        defer { Task { await fake.stop() } }
        let (c, rec) = try await makeController(fake, storedAccount())
        c.start()
        try await waitUntil { c.listing != nil }
        c.setSpace("10001", selected: false)
        c.setSpace("10002", selected: false)
        #expect(c.selectedSpaces.isEmpty)
        #expect(c.spacesProblem == "Select at least one space")
        #expect(!c.canSave)
        c.save()
        #expect(rec.banners == ["Select at least one space"])
        #expect(!c.busy)
        c.setSpace("10003", selected: true)
        #expect(rec.banners.last == .some(nil))
        #expect(c.canSave && c.spacesProblem == "")
        try await Task.sleep(for: .milliseconds(50))
        #expect(await log.count(API.AccountUpdate.name) == 0)
    }

    @Test func theListsCheckWhatIsAdded() async throws {
        let log = ParamsLog()
        let fake = try await listingFake(log)
        defer { Task { await fake.stop() } }
        let (c, rec) = try await makeController(fake, storedAccount { $0.botNames = ["Deploy Bot"] })
        c.start()
        try await waitUntil { c.listing != nil }

        #expect(c.entries(.botNames) == ["Deploy Bot"])
        #expect(c.suggestions(.botNames) == [Jira.Suggestion(
            value: Jira.suggestedBotName, label: "Add Issue Sync – Synchronization for Jira")])
        #expect(c.suggestions(.metadataFilters).map(\.value) == [Jira.suggestedMetadataFilter])
        #expect(c.suggestions(.authorPrefixes).isEmpty && c.suggestions(.senders).isEmpty)

        // Nothing typed: nothing added, nothing wrong.
        #expect(c.addEntry(.botNames, "  "))
        #expect(c.entries(.botNames) == ["Deploy Bot"] && c.problem(.botNames) == "")

        #expect(!c.addEntry(.botNames, "ab"))
        #expect(c.problem(.botNames) == "A bot name needs at least 3 characters")
        #expect(c.problem(.metadataFilters) == "", "a problem belongs to its list")
        let changes = rec.changes
        c.entryTyped(.botNames)
        #expect(c.problem(.botNames) == "" && rec.changes == changes + 1)
        c.entryTyped(.botNames)
        #expect(rec.changes == changes + 1, "nothing to clear")

        #expect(!c.addEntry(.botNames, "deploy  bot"))
        #expect(c.problem(.botNames) == "This entry is already in the list")
        #expect(!c.addEntry(.metadataFilters, "(unclosed"))
        #expect(c.problem(.metadataFilters) == "This pattern is not valid: missing closing )")
        #expect(!c.addEntry(.metadataFilters, #"(?<=Remote).*"#))
        #expect(c.problem(.metadataFilters) == "This pattern is not valid: invalid named capture")
        #expect(!c.addEntry(.senders, "acme.example"))
        #expect(c.problem(.senders) == "Enter an address, or a domain such as @example.org")
        #expect(!c.isChanged, "nothing was added")

        // A suggestion is added as it is, once.
        c.addSuggestion(.botNames, Jira.suggestedBotName)
        c.addSuggestion(.botNames, Jira.suggestedBotName)
        c.addSuggestion(.botNames, "Somebody Else")
        #expect(c.entries(.botNames) == ["Deploy Bot", Jira.suggestedBotName])
        #expect(c.problem(.botNames) == "" && c.suggestions(.botNames).isEmpty)
        #expect(c.addEntry(.metadataFilters, #" ^Sent from .*$ "#))
        #expect(c.entries(.metadataFilters) == [#"^Sent from .*$"#])
        #expect(c.problem(.metadataFilters) == "")
        #expect(c.addEntry(.senders, "@Acme.Example"))
        #expect(c.entries(.senders) == ["@acme.example"])
        #expect(c.isChanged)

        c.removeEntry(.botNames, at: 0)
        c.removeEntry(.botNames, at: 7)
        #expect(c.entries(.botNames) == [Jira.suggestedBotName])
        c.removeEntry(.botNames, at: 0)
        #expect(c.entries(.botNames).isEmpty)
        #expect(c.suggestions(.botNames).count == 1, "offered again once it is gone")

        // The senders do not matter when notifications are left alone.
        c.setNotificationIndex(2)
        #expect(!c.sendersEditable && c.notificationIndex == 2)
        c.setNotificationIndex(9)
        #expect(c.notificationIndex == 2)
    }

    @Test func aLateReplyIsDropped() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        let log = ParamsLog()
        await fake.on(API.AccountListSpaces.name) { _ in
            try? await Task.sleep(for: .milliseconds(300))
            return json(listingJSON)
        }
        await fake.on(API.AccountUpdate.name) { p in
            await log.record(API.AccountUpdate.name, p)
            try? await Task.sleep(for: .milliseconds(200))
            return json("{}")
        }
        // Save while the spaces load: the listing is not waited for.
        let (c, rec) = try await makeController(fake, storedAccount())
        c.start()
        try await waitUntil { await fake.calls.contains(API.AccountListSpaces.name) }
        c.setOnlyMine(true)
        c.save()
        #expect(rec.busy == ["Loading the spaces", "Saving the account"])
        try await waitUntil { !rec.done.isEmpty }
        try await Task.sleep(for: .milliseconds(400))
        #expect(c.listing == nil, "the listing arrived after Save started")
        #expect(rec.busy == ["Loading the spaces", "Saving the account", nil])

        // Closed while it saves: nothing is reported any more.
        let (c2, rec2) = try await makeController(fake, storedAccount())
        c2.setOnlyMine(true)
        c2.save()
        try await waitUntil { await log.count(API.AccountUpdate.name) == 2 }
        c2.close()
        #expect(c2.closed && !c2.busy && !c2.saving)
        try await Task.sleep(for: .milliseconds(400))
        #expect(rec2.done.isEmpty && rec2.banners.isEmpty)
        #expect(rec2.busy == ["Saving the account"])
        // A closed page does nothing.
        c2.save()
        c2.replaceToken()
        c2.tokenReplaced()
        #expect(rec2.tokenRequests.isEmpty)
        try await Task.sleep(for: .milliseconds(50))
        #expect(await log.count(API.AccountUpdate.name) == 2)
    }

    @Test func replacingTheTokenListsAgain() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountListSpaces.name) { _ in throw RPCError(code: .authFailed, message: "401") }
        let account = storedAccount()
        let (c, rec) = try await makeController(fake, account)
        c.start()
        try await waitUntil { rec.busy.count == 2 }
        #expect(rec.banners == ["The Jira site rejected the token"])

        c.setOnlyMine(true)
        c.replaceToken()
        #expect(rec.tokenRequests == ["acc-j1"])

        // The assistant stored a new token: the listing works now, and the
        // form keeps what was edited.
        await fake.on(API.AccountListSpaces.name) { _ in json(listingJSON) }
        c.tokenReplaced()
        #expect(rec.banners.last == .some(nil), "the old reason goes at once")
        try await waitUntil { c.listing != nil }
        #expect(rec.busy == ["Loading the spaces", nil, "Loading the spaces", nil])
        #expect(c.banner == nil)
        #expect(c.spaceRows.count == 3)
        #expect(c.form.onlyMine)
    }

    @Test func aDataCenterAccount() async throws {
        let fake = try await makeFake()
        defer { Task { await fake.stop() } }
        await fake.on(API.AccountListSpaces.name) { _ in throw RPCError(code: .authRequired, message: "no token") }
        let account = Account(
            id: "acc-dc", config: jiraDataCenterAccount(), enabled: true,
            state: SyncState(accountId: "acc-dc", status: .authRequired), capabilities: [])
        let (c, rec) = try await makeController(fake, account)
        #expect(c.deployment == .datacenter)
        #expect(c.site.deployment == "Jira Data Center" && c.site.tokenLabel == "Personal Access Token")
        #expect(c.sendersPlaceholder == "")
        c.start()
        try await waitUntil { rec.busy.count == 2 }
        #expect(rec.banners == ["Enter the personal access token for this account"])
    }
}
