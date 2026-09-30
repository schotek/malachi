// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The Change Status menu's controller over a fake daemon: the items of
// issue.transitions (a transition that needs fields in Jira disabled with
// the hint), issue.transition with its toast and the refreshed issue for
// the cards, the failure toasts, the stale-reply discipline of the loads,
// one transition per issue at a time, and nothing at all for an account
// without the capability.

private struct Timeout: Error {}

@MainActor
private func waitUntil(_ timeout: Duration = .seconds(5), _ cond: @MainActor () async throws -> Bool) async throws {
    let deadline = ContinuousClock.now + timeout
    while try await !cond() {
        if ContinuousClock.now > deadline { throw Timeout() }
        try await Task.sleep(for: .milliseconds(10))
    }
}

private let jiraAccount: AccountID = "j"
private let plainAccount: AccountID = "p"
private let mailAccount: AccountID = "m"

private let issue = IssueInfo(
    key: "ITSD-42", url: "https://acme.atlassian.net/browse/ITSD-42", summary: "The printer on the third floor",
    status: "To Do", statusCategory: .todo, assignee: "Jana Dvořáková"
)

private var inProgress: IssueInfo {
    var i = issue
    i.status = "In Progress"
    i.statusCategory = .inProgress
    return i
}

private func jira(_ id: AccountID, _ caps: [Capability]?) -> Account {
    Account(
        id: id, config: AccountConfig(name: "Acme Jira", email: "jana@acme.example", kind: .jira), enabled: true,
        state: SyncState(accountId: id, status: .idle), capabilities: caps
    )
}

/// The daemon's answers and what it was asked, off the main actor.
private actor Script {
    var listError: RPCError?
    var listDelay: Duration = .zero
    var transitionError: RPCError?
    var transitionDelay: Duration = .zero
    var refreshed = inProgress
    private(set) var lists: [IssueTransitionsParams] = []
    private(set) var transitions: [IssueTransitionParams] = []

    func set(listError: RPCError?) { self.listError = listError }
    func set(listDelay: Duration) { self.listDelay = listDelay }
    func set(transitionError: RPCError?) { self.transitionError = transitionError }
    func set(transitionDelay: Duration) { self.transitionDelay = transitionDelay }
    func set(refreshed: IssueInfo) { self.refreshed = refreshed }

    func list(_ params: Data) async throws -> Data {
        lists.append(try decode(IssueTransitionsParams.self, params))
        if listDelay > .zero {
            try await Task.sleep(for: listDelay)
        }
        if let listError {
            throw listError
        }
        return try encode(IssueTransitionsResult(issue: issue, transitions: [
            IssueTransition(id: "11", name: "Start Progress", to: "In Progress", toCategory: .inProgress),
            IssueTransition(id: "31", name: "Resolve", to: "Resolved", toCategory: .done, needsInput: true),
            IssueTransition(id: "21", name: "Done", to: "Done", toCategory: .done),
        ]))
    }

    func transition(_ params: Data) async throws -> Data {
        transitions.append(try decode(IssueTransitionParams.self, params))
        if transitionDelay > .zero {
            try await Task.sleep(for: transitionDelay)
        }
        if let transitionError {
            throw transitionError
        }
        return try encode(IssueTransitionResult(issue: refreshed))
    }

    private func encode<T: Encodable>(_ v: T) throws -> Data { try JSONCoding.encoder().encode(v) }
    private func decode<T: Decodable>(_ t: T.Type, _ d: Data) throws -> T {
        try JSONCoding.decoder().decode(t, from: d.isEmpty ? Data("{}".utf8) : d)
    }
}

@MainActor
private final class Harness {
    let fake: FakeDaemon
    let script = Script()
    let client: RPCClient
    let controller: IssueActionsController
    var accounts: [AccountID: Account] = [
        jiraAccount: jira(jiraAccount, [.comment, .forward, .transition]),
        plainAccount: jira(plainAccount, [.comment, .forward]),
        mailAccount: Account(
            id: mailAccount, config: AccountConfig(name: "Mail", email: "me@example.invalid"), enabled: true,
            state: SyncState(accountId: mailAccount, status: .idle)),
    ]
    var toasts: [String] = []
    var busy: [(AccountID, String, Bool)] = []
    var changed: [(AccountID, IssueInfo)] = []
    var loaded: [Result<IssueActionsController.Loaded, any Error>] = []

    init() async throws {
        fake = try FakeDaemon()
        let script = script
        await fake.on(API.IssueTransitions.name) { try await script.list($0) }
        await fake.on(API.IssueTransition.name) { try await script.transition($0) }
        try await fake.start()
        client = RPCClient(socketPath: fake.path)
        try await client.connect()
        // The harness outlives the controller: the captures are unowned.
        controller = IssueActionsController(client: client, account: { _ in nil }, toast: { _ in })
        controller.account = { [unowned self] in self.accounts[$0] }
        controller.toast = { [unowned self] in self.toasts.append($0) }
        controller.onBusy = { [unowned self] a, k, b in self.busy.append((a, k, b)) }
        controller.onIssueChanged = { [unowned self] a, i in self.changed.append((a, i)) }
    }

    func load(_ account: AccountID = jiraAccount, _ message: MessageID = "m1") -> Bool {
        controller.loadTransitions(IssueActionsController.Subject(accountId: account, messageId: message)) { [unowned self] in
            self.loaded.append($0)
        }
    }

    func stop() async {
        await client.close()
        await fake.stop()
    }
}

/// The transition items of the script's answer.
private let start = Jira.TransitionItem(id: "11", title: "Start Progress", target: "In Progress", subtitle: "In Progress", enabled: true)
private let resolve = Jira.TransitionItem(id: "31", title: "Resolve", target: "Resolved", subtitle: "Resolved", hint: "Needs fields in Jira")
private let done = Jira.TransitionItem(id: "21", title: "Done", target: "Done", enabled: true)

@MainActor
@Suite(.serialized) struct IssueActionsControllerTests {
    @Test func loadListsTheTransitions() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        #expect(h.controller.canTransition(jiraAccount))
        #expect(h.load())
        try await waitUntil { h.loaded.count == 1 }
        let got = try h.loaded[0].get()
        #expect(got.issue == issue)
        #expect(got.items == [start, resolve, done])
        #expect(!got.items[1].enabled && got.items[1].hint == "Needs fields in Jira", "a transition that needs input is listed disabled")
        #expect(await h.script.lists == [IssueTransitionsParams(accountId: jiraAccount, messageId: "m1")])
        #expect(await h.fake.calls == ["issue.transitions"])
    }

    @Test func nothingWithoutTheCapability() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        #expect(!h.controller.canTransition(plainAccount), "comment and forward alone do not change statuses")
        #expect(!h.controller.canTransition(mailAccount), "a mail account never does")
        #expect(!h.controller.canTransition("unknown"))
        #expect(!h.load(plainAccount))
        #expect(!h.load(mailAccount))
        #expect(!h.controller.perform(IssueActionsController.Subject(accountId: plainAccount, messageId: "m1"), start, issue: issue))
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.loaded.isEmpty && h.toasts.isEmpty && h.busy.isEmpty)
        #expect(await h.fake.calls.isEmpty, "the daemon is never asked")
    }

    @Test func loadFailureReachesTheMenu() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(listError: RPCError(code: .networkError, message: "dial tcp: refused"))
        #expect(h.load())
        try await waitUntil { h.loaded.count == 1 }
        guard case .failure(let err) = h.loaded[0] else {
            Issue.record("the failure is passed on")
            return
        }
        #expect((err as? RPCError)?.code == .networkError)
        #expect(rpcErrorText(Jira.loadTransitionsAction(), err) == "Loading the status changes failed: the server could not be reached")
        #expect(h.toasts.isEmpty, "the menu shows the failure; no toast")
    }

    @Test func onlyTheNewestLoadAnswers() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(listDelay: .milliseconds(150))
        #expect(h.load(jiraAccount, "m1"))
        try await Task.sleep(for: .milliseconds(20))
        await h.script.set(listDelay: .zero)
        #expect(h.load(jiraAccount, "m2"))
        try await waitUntil { h.loaded.count == 1 }
        try await Task.sleep(for: .milliseconds(250))
        #expect(h.loaded.count == 1, "the first load's reply was dropped")
        #expect(await h.script.lists.map(\.messageId) == ["m1", "m2"], "both were asked")

        // cancelLoad: the menu closed before the answer.
        await h.script.set(listDelay: .milliseconds(100))
        #expect(h.load(jiraAccount, "m3"))
        h.controller.cancelLoad()
        try await Task.sleep(for: .milliseconds(250))
        #expect(h.loaded.count == 1, "a cancelled load answers nobody")
    }

    @Test func performChangesTheStatus() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let subject = IssueActionsController.Subject(accountId: jiraAccount, messageId: "m1")
        #expect(h.controller.perform(subject, start, issue: issue))
        #expect(h.controller.isBusy(account: jiraAccount, key: "ITSD-42"))
        #expect(h.busy.count == 1 && h.busy[0].0 == jiraAccount && h.busy[0].1 == "ITSD-42" && h.busy[0].2)
        try await waitUntil { h.toasts.count == 1 }
        #expect(h.toasts == ["Status changed to In Progress"])
        #expect(h.changed.count == 1 && h.changed[0].0 == jiraAccount && h.changed[0].1 == inProgress, "the cards get the refreshed issue")
        #expect(h.busy.count == 2 && !h.busy[1].2)
        #expect(!h.controller.isBusy(account: jiraAccount, key: "ITSD-42"))
        #expect(await h.script.transitions == [IssueTransitionParams(accountId: jiraAccount, messageId: "m1", transitionId: "11")])
        #expect(await h.fake.calls == ["issue.transition"])
    }

    @Test func theToastNamesTheTargetEvenWhenTheRefreshWasLate() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        // The daemon's refresh timed out: the result still shows To Do.
        await h.script.set(refreshed: issue)
        let subject = IssueActionsController.Subject(accountId: jiraAccount, messageId: "m1")
        #expect(h.controller.perform(subject, start, issue: issue))
        try await waitUntil { h.toasts.count == 1 }
        #expect(h.toasts == ["Status changed to In Progress"])
        #expect(h.changed.count == 1 && h.changed[0].1 == issue, "the card shows what the daemon knows; the sync brings the rest")
    }

    @Test func failuresToast() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let subject = IssueActionsController.Subject(accountId: jiraAccount, messageId: "m1")
        await h.script.set(transitionError: RPCError(code: .serverError, message: "Transition is not allowed by the workflow"))
        #expect(h.controller.perform(subject, start, issue: issue))
        try await waitUntil { h.toasts.count == 1 }
        #expect(h.toasts[0] == "The status could not be changed: Transition is not allowed by the workflow")
        #expect(h.changed.isEmpty, "no issue to apply")
        #expect(h.busy.map(\.2) == [true, false])

        await h.script.set(transitionError: RPCError(code: .networkError, message: "refused"))
        #expect(h.controller.perform(subject, done, issue: issue))
        try await waitUntil { h.toasts.count == 2 }
        #expect(h.toasts[1] == "Changing the status failed: the server could not be reached")

        await h.script.set(transitionError: RPCError(code: .invalidArgument, message: "transition needs input"))
        #expect(h.controller.perform(subject, done, issue: issue))
        try await waitUntil { h.toasts.count == 3 }
        #expect(h.toasts[2] == "Changing the status was rejected: transition needs input")
    }

    @Test func aDisabledItemIsNeverPerformed() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        let subject = IssueActionsController.Subject(accountId: jiraAccount, messageId: "m1")
        #expect(!h.controller.perform(subject, resolve, issue: issue), "needs fields in Jira")
        #expect(!h.controller.perform(subject, Jira.TransitionItem(title: "x", enabled: true), issue: issue), "no id")
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.busy.isEmpty && h.toasts.isEmpty)
        #expect(await h.fake.calls.isEmpty)
    }

    @Test func oneTransitionPerIssueAtATime() async throws {
        let h = try await Harness()
        defer { Task { await h.stop() } }
        await h.script.set(transitionDelay: .milliseconds(150))
        let subject = IssueActionsController.Subject(accountId: jiraAccount, messageId: "m1")
        #expect(h.controller.perform(subject, start, issue: issue))
        #expect(!h.controller.perform(subject, done, issue: issue), "the issue is busy")
        // Another issue of the account is not.
        var other = issue
        other.key = "WEB-7"
        #expect(h.controller.perform(IssueActionsController.Subject(accountId: jiraAccount, messageId: "w1"), done, issue: other))
        try await waitUntil { h.toasts.count == 2 }
        #expect(await h.script.transitions.map(\.transitionId) == ["11", "21"])
        #expect(!h.controller.isBusy(account: jiraAccount, key: "ITSD-42") && !h.controller.isBusy(account: jiraAccount, key: "WEB-7"))
        // Free again: the next choice goes through.
        #expect(h.controller.perform(subject, done, issue: issue))
        try await waitUntil { h.toasts.count == 3 }
    }
}
