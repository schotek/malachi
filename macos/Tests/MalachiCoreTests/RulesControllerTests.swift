// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The rules' controllers against a fake daemon (as ContactsControllerTests):
// the list and its reload, the switch with and without the outgoing
// question, the order, Remove with its question, Apply Rules… and the
// editor's Save and Try It, with the error mapping of each. Invented data.

/// The daemon: a handler per method over the JSON of the params; every
/// call is recorded.
private final class FakeRulesDaemon: RulesCaller, @unchecked Sendable {
    typealias Handler = @Sendable (Data) throws -> Data

    private let lock = NSLock()
    private var handlers: [String: Handler] = [:]
    private var calls: [(String, Data)] = []

    func on(_ method: String, _ h: @escaping Handler) {
        lock.withLock { handlers[method] = h }
    }

    func answer<T: Encodable & Sendable>(_ method: String, _ v: T) {
        let data = (try? JSONCoding.encoder().encode(v)) ?? Data()
        on(method) { _ in data }
    }

    func fail(_ method: String, _ e: RPCError) {
        on(method) { _ in throw e }
    }

    func count(_ method: String) -> Int {
        lock.withLock { calls.filter { $0.0 == method }.count }
    }

    func last<T: Decodable>(_ method: String, as type: T.Type) throws -> T {
        guard let d = lock.withLock({ calls.last { $0.0 == method }?.1 }) else {
            throw RPCError(code: .methodNotFound, message: method)
        }
        return try JSONCoding.decoder().decode(type, from: d)
    }

    func call<M: RPCMethod>(_ method: M.Type, _ params: M.Params, timeout: Duration?) async throws -> M.Result {
        let raw = try JSONCoding.encoder().encode(params)
        let h = lock.withLock { () -> Handler? in
            calls.append((M.name, raw))
            return handlers[M.name]
        }
        guard let h else { throw RPCError(code: .methodNotFound, message: M.name) }
        return try JSONCoding.decoder().decode(M.Result.self, from: try h(raw))
    }
}

private func account(_ id: String, _ name: String, kind: AccountKind? = nil) -> Account {
    Account(id: AccountID(id), config: AccountConfig(name: name, email: "\(id)@example.org", kind: kind),
            enabled: true, state: SyncState(accountId: AccountID(id), status: .idle))
}

private func folder(_ acc: String, _ id: String, _ name: String, _ role: FolderRole = .none) -> Folder {
    Folder(id: FolderID(id), accountId: AccountID(acc), name: name, path: name, role: role, subscribed: true,
           selectable: true, synced: true, unread: 0, total: 10)
}

private let moveNews = Rule(id: "r_news", input: RuleInput(
    name: "News", enabled: true, accountId: "acc_w", match: .all,
    conditions: [RuleCondition(field: .from, op: .domainIs, value: "news.example.com")],
    actions: [RuleAction(kind: .move, folderId: "f_news")]))

private let fwdRule = Rule(id: "r_fwd", input: RuleInput(
    name: "Forward", enabled: false, match: .all,
    conditions: [RuleCondition(field: .from, op: .is, value: "boss@example.com")],
    actions: [RuleAction(kind: .forward, to: "phone@example.org")]), outgoing: true)

/// A daemon with two mail accounts, a Jira account, folders and two rules.
private func daemon() -> FakeRulesDaemon {
    let d = FakeRulesDaemon()
    d.answer(API.RulesList.name, RulesListResult(rules: [moveNews, fwdRule]))
    d.answer(API.AccountList.name, AccountListResult(accounts: [
        account("acc_w", "Work"), account("acc_h", "Home", kind: .graph), account("acc_j", "Jira", kind: .jira),
    ]))
    d.on(API.FolderList.name) { raw in
        let p = try JSONCoding.decoder().decode(FolderListParams.self, from: raw)
        let folders = p.accountId == "acc_w"
            ? [folder("acc_w", "f_in", "INBOX", .inbox), folder("acc_w", "f_news", "News"), folder("acc_w", "f_out", "Outbox", .outbox)]
            : [folder("acc_h", "f_h_in", "INBOX", .inbox)]
        return try JSONCoding.encoder().encode(FolderListResult(folders: folders))
    }
    return d
}

/// A `Confirm` that answers `yes` and records the questions.
@MainActor
private final class Asker {
    var yes: Bool
    var asked: [Rules.Question] = []

    init(_ yes: Bool) { self.yes = yes }

    var confirm: Rules.Confirm {
        { q in
            self.asked.append(q)
            return self.yes
        }
    }
}

@MainActor
struct RulesControllerTests {
    private func loaded(_ d: FakeRulesDaemon) async -> RulesController {
        let c = RulesController(caller: d, date: { _ in "today" })
        await c.reload()
        return c
    }

    @Test func listsRulesAccountsAndMailFolders() async throws {
        let d = daemon()
        let c = await loaded(d)
        #expect(c.loaded && c.rules.map(\.id) == ["r_news", "r_fwd"])
        #expect(c.mailAccounts.map(\.id) == ["acc_w", "acc_h"], "no issue tracker")
        #expect(d.count(API.FolderList.name) == 2, "the mail accounts' folders only")
        let rows = c.rows
        #expect(rows[0].actions == "move to “News”" && rows[0].scope == "Work")
        #expect(rows[1].scope == "All Mail Accounts" && rows[1].outgoing && rows[1].status == "Not used yet")
        #expect(c.applies(to: "acc_w") && !c.applies(to: "acc_h"), "the forward rule is off")
        var changes = 0
        let token = c.observe { changes += 1 }
        c.handle(API.Notify.rulesChanged)
        c.handle(API.Notify.newMessage)
        try await settle(c)
        #expect(d.count(API.RulesList.name) == 2 && changes == 1)
        token.cancel()
    }

    @Test func switchAsksForOutgoingRulesOnly() async throws {
        let d = daemon()
        let c = await loaded(d)
        d.answer(API.RulesUpdate.name, RuleResult(rule: Rule(id: "r_news", input: moveNews.input)))
        let ask = Asker(true)
        // A local rule turned off: no question.
        #expect(await c.setEnabled("r_news", false, confirm: ask.confirm) == nil)
        #expect(ask.asked.isEmpty)
        let off = try d.last(API.RulesUpdate.name, as: RulesUpdateParams.self)
        #expect(!off.rule.enabled && !off.confirmOutgoing)
        // The forwarding rule turned on: the question, then confirmOutgoing.
        let no = Asker(false)
        #expect(await c.setEnabled("r_fwd", true, confirm: no.confirm) == nil)
        #expect(no.asked.first?.heading == "Send Mail Automatically?" && no.asked.first?.body.contains("phone@example.org") == true)
        #expect(d.count(API.RulesUpdate.name) == 1, "declined: nothing sent")
        d.answer(API.RulesUpdate.name, RuleResult(rule: Rule(id: "r_fwd", input: Rules.toggled(fwdRule), outgoing: true)))
        #expect(await c.setEnabled("r_fwd", true, confirm: ask.confirm) == nil)
        let on = try d.last(API.RulesUpdate.name, as: RulesUpdateParams.self)
        #expect(on.id == "r_fwd" && on.rule.enabled && on.confirmOutgoing)
        #expect(c.rule("r_fwd")?.enabled == true)
    }

    @Test func errorsMapToTheirSentences() async {
        let d = daemon()
        let c = await loaded(d)
        let ask = Asker(true)
        d.fail(API.RulesUpdate.name, RPCError(code: .ruleNotFound, message: "gone"))
        #expect(await c.setEnabled("r_news", false, confirm: ask.confirm) == "This rule no longer exists.")
        d.fail(API.RulesUpdate.name, RPCError(code: .invalidArgument, message: "x", data: .object(["field": .string("accountId")])))
        #expect(await c.setEnabled("r_news", false, confirm: ask.confirm) == "The account of this rule no longer exists.")
        d.fail(API.RulesUpdate.name, RPCError(code: .storageError, message: "x"))
        #expect(await c.setEnabled("r_news", false, confirm: ask.confirm) == "The rule could not be saved.")
        d.fail(API.RulesApply.name, RPCError(code: .folderNotFound, message: "x"))
        #expect(await c.apply(FolderKey(account: "acc_w", folder: "f_in"), folderName: "INBOX", confirm: ask.confirm)
            == "The rules could not be applied.")
    }

    @Test func reorderShowsAtOnceAndTakesBackARefusal() async throws {
        let d = daemon()
        let c = await loaded(d)
        d.answer(API.RulesReorder.name, RulesListResult(rules: [fwdRule, moveNews]))
        #expect(await c.move(from: 0, to: 1) == nil)
        #expect(try d.last(API.RulesReorder.name, as: RulesReorderParams.self).ids == ["r_fwd", "r_news"])
        #expect(c.rules.map(\.id) == ["r_fwd", "r_news"])
        #expect(await c.move(from: 1, to: 1) == nil && d.count(API.RulesReorder.name) == 1, "no move")
        d.fail(API.RulesReorder.name, RPCError(code: .invalidArgument, message: "x"))
        #expect(await c.move(from: 0, to: 1) == "The rule could not be saved.")
        #expect(c.rules.map(\.id) == ["r_fwd", "r_news"], "taken back")
        try await settle(c)
    }

    @Test func removeAsksFirst() async {
        let d = daemon()
        let c = await loaded(d)
        d.answer(API.RulesDelete.name, EmptyResult())
        let no = Asker(false)
        #expect(await c.remove("r_news", confirm: no.confirm) == nil)
        #expect(no.asked.first == Rules.Question(
            heading: "Remove the Rule “News”?", body: "Messages it already moved or marked stay as they are.",
            confirm: "Remove", destructive: true))
        #expect(d.count(API.RulesDelete.name) == 0)
        let yes = Asker(true)
        #expect(await c.remove("r_news", confirm: yes.confirm) == nil)
        #expect(d.count(API.RulesDelete.name) == 1 && c.rule("r_news") == nil)
    }

    @Test func applyAsksAndSaysWhatChanged() async throws {
        let d = daemon()
        let c = await loaded(d)
        d.answer(API.RulesApply.name, RulesApplyResult(examined: 10, matched: 3, changed: 2))
        let k = FolderKey(account: "acc_w", folder: "f_in")
        let no = Asker(false)
        #expect(await c.apply(k, folderName: "INBOX", confirm: no.confirm) == nil)
        #expect(no.asked.first?.heading == "Apply Rules to “INBOX”?" && no.asked.first?.confirm == "Apply")
        #expect(d.count(API.RulesApply.name) == 0)
        let yes = Asker(true)
        #expect(await c.apply(k, folderName: "INBOX", confirm: yes.confirm) == "The rules changed 2 messages.")
        let p = try d.last(API.RulesApply.name, as: RulesApplyParams.self)
        #expect(p.accountId == "acc_w" && p.folderId == "f_in" && p.messageIds == nil && p.ruleIds == nil)
        #expect(c.applying.isEmpty)
    }

    // MARK: The editor

    @Test func editorValidatesAndCreates() async throws {
        let d = daemon()
        let c = await loaded(d)
        let e = RuleEditorController(caller: d, newFor: "acc_w", rules: c)
        #expect(!e.canSave && e.problemTexts("name", 0) == ["Enter a name for the rule."])
        #expect(e.testFolder == FolderKey(account: "acc_w", folder: "f_in"))
        #expect(e.moveFolders.map(\.id.rawValue) == ["f_in", "f_news"], "no outbox")
        e.setName("News")
        e.setValue(0, "news.example.com")
        e.setOperator(0, .domainIs)
        e.addAction(.move)
        #expect(e.problemTexts("actions", 0) == ["Choose a folder."])
        e.setFolder(0, "f_news")
        #expect(e.canSave)
        e.setAccount("acc_h")
        #expect(e.form.actions[0].folderId == nil && e.testFolder == FolderKey(account: "acc_h", folder: "f_h_in"))
        e.setAccount("acc_w")
        e.setFolder(0, "f_news")
        d.answer(API.RulesCreate.name, RuleResult(rule: Rule(id: "r_new", input: e.form.trimmed)))
        let ask = Asker(true)
        let saved = await e.save(confirm: ask.confirm)
        #expect(saved?.id == "r_new" && ask.asked.isEmpty)
        let p = try d.last(API.RulesCreate.name, as: RulesCreateParams.self)
        #expect(p.rule.name == "News" && p.rule.accountId == "acc_w" && !p.confirmOutgoing)
    }

    @Test func editorAsksBeforeAForwardAndSaysARefusal() async throws {
        let d = daemon()
        let c = await loaded(d)
        var edited = fwdRule
        edited.input.enabled = true
        let e = RuleEditorController(caller: d, editing: edited, rules: c)
        #expect(Rules.editorTitle(e.form) == "Edit Rule")
        e.setForwardTo(0, "other@example.org")
        d.fail(API.RulesUpdate.name, RPCError(code: .invalidArgument, message: "x", data: .object(["field": .string("confirmOutgoing")])))
        let no = Asker(false)
        #expect(await e.save(confirm: no.confirm) == nil && d.count(API.RulesUpdate.name) == 0)
        let yes = Asker(true)
        #expect(await e.save(confirm: yes.confirm) == nil)
        #expect(e.error == "Rules that send mail need your confirmation.")
        let p = try d.last(API.RulesUpdate.name, as: RulesUpdateParams.self)
        #expect(p.id == "r_fwd" && p.confirmOutgoing && p.rule.actions[0].to == "other@example.org")
        e.setName("Forward!")
        #expect(e.error.isEmpty, "a change clears the refusal")
    }

    @Test func editorTriesTheRule() async throws {
        let d = daemon()
        let c = await loaded(d)
        let e = RuleEditorController(caller: d, editing: moveNews, rules: c)
        d.answer(API.RulesTest.name, RulesTestResult(count: 3))
        await e.test()
        #expect(e.testText == "3 messages in this folder match.")
        let p = try d.last(API.RulesTest.name, as: RulesTestParams.self)
        #expect(p.accountId == "acc_w" && p.folderId == "f_in" && p.rule.name == "News")
        d.answer(API.RulesTest.name, RulesTestResult(count: 0))
        e.setTestFolder(FolderKey(account: "acc_w", folder: "f_news"))
        #expect(e.testText.isEmpty)
        await e.test()
        #expect(e.testText == "No message in this folder matches.")
        #expect(e.testFolders.map(\.id.rawValue) == ["f_in", "f_news"])
    }

    /// Waits until no call of the controller runs.
    private func settle(_ c: RulesController) async throws {
        let deadline = ContinuousClock.now + .seconds(5)
        repeat {
            try await Task.sleep(for: .milliseconds(2))
            if ContinuousClock.now > deadline {
                Issue.record("calls still running")
                return
            }
        } while c.pending > 0
    }
}
