// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The mail rules of the application (docs/api.md §4.21, the flow of
// ui/internal/rules): `RulesController` keeps the daemon's rules, the
// accounts and their folders for the names a summary shows, and runs what
// Settings → Rules and the folders' Apply Rules… do; `RuleEditorController`
// keeps the editor sheet's form. Both are `@MainActor`; the views only show
// what they hold and report clicks. Every question goes through the
// caller's `Confirm` (an alert on the right window), every failure comes
// back as a sentence for a toast or the sheet, never the daemon's message.

import Foundation
import os

private let rulesLog = Logger(subsystem: "io.github.schotek.Malachi", category: "rules")

/// The daemon as the rules' controllers call it: `RPCClient`, the samples
/// (`SampleRulesCaller`) or a fake in the tests.
public protocol RulesCaller: Sendable {
    func call<M: RPCMethod>(_ method: M.Type, _ params: M.Params, timeout: Duration?) async throws -> M.Result
}

extension RPCClient: RulesCaller {}

extension Rules {
    /// Asks a question (an alert on the window the action came from); true
    /// goes ahead.
    public typealias Confirm = @MainActor (Question) async -> Bool

    /// The sentence of a failed rules.* call: ruleNotFound is
    /// `notFoundText`, an invalidArgument with `RuleInvalidData`
    /// `invalidText`, anything else `failed` (`saveFailedText` or
    /// `applyFailedText`). The daemon's message is never shown.
    public static func errorText(_ error: any Error, failed: String) -> String {
        let e = ContactsOptions.asRPCError(error)
        if e.code == .ruleNotFound {
            return notFoundText
        }
        if let d = e.ruleInvalid {
            return invalidText(d)
        }
        return failed
    }
}

/// The application's rules: one per application (`AppRules`), shared by
/// Settings → Rules and the folders' context menus. It lists rules.list,
/// account.list and the mail accounts' folders when started and again on
/// notify.rulesChanged and notify.accountsChanged (`handle`), and writes
/// through `setEnabled`, `move`, `remove` and `apply`, each of which
/// answers nil or the sentence of its failure.
@MainActor
public final class RulesController {
    /// The rules in their order, as last listed.
    public private(set) var rules: [Rule] = []
    /// Every account (for the names) and the mail accounts' folders.
    public private(set) var accounts: [Account] = []
    public private(set) var folders: [AccountID: [Folder]] = [:]
    /// The first answer arrived.
    public private(set) var loaded = false
    /// The rule ids whose switch or removal is under way.
    public private(set) var busy: Set<String> = []
    /// The folders an Apply Rules… runs on.
    public private(set) var applying: Set<FolderKey> = []
    /// Calls without their answer (the tests wait for none).
    public private(set) var pending = 0

    private let caller: any RulesCaller
    private let date: Rules.DateText
    private let observers = BoardObservers()
    private var gen = 0
    private var stopped = false

    public init(
        caller: any RulesCaller,
        date: @escaping Rules.DateText = { DateFormatter.localizedString(from: $0, dateStyle: .medium, timeStyle: .short) }
    ) {
        self.caller = caller
        self.date = date
    }

    /// Calls `f` after every change of what the controller holds.
    public func observe(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken {
        observers.add(f)
    }

    /// Lists everything (when the connection comes).
    public func start() {
        stopped = false
        refresh()
    }

    /// Nothing is listed any more (the connection went).
    public func stop() {
        stopped = true
        gen += 1
    }

    /// notify.rulesChanged lists the rules alone again (the daemon sends it
    /// whenever a rule's counts move, so on every message a rule acted on;
    /// one listing runs at a time and one more follows when another came
    /// meanwhile); notify.accountsChanged lists everything again.
    public func handle(_ method: String) {
        switch method {
        case API.Notify.rulesChanged: refreshRules()
        case API.Notify.accountsChanged: refresh()
        default: break
        }
    }

    /// Lists the rules, the accounts and the mail accounts' folders again.
    public func refresh() {
        guard !stopped else { return }
        Task { await reload() }
    }

    /// Lists the rules alone again (rules.list).
    public func refreshRules() {
        guard !stopped else { return }
        if rulesListing {
            rulesAgain = true
            return
        }
        rulesListing = true
        pending += 1
        Task {
            await reloadRules()
            pending -= 1
        }
    }

    private var rulesListing = false
    private var rulesAgain = false

    private func reloadRules() async {
        let caller = caller
        repeat {
            rulesAgain = false
            let my = gen
            do {
                let r = try await caller.call(API.RulesList.self, EmptyParams(), timeout: nil)
                // A full listing started meanwhile brings the rules too.
                if my == gen, !stopped {
                    rules = r.rules
                    observers.notify()
                }
            } catch {
                rulesLog.info("rules.list failed: \(ContactsOptions.asRPCError(error).code.rawValue, privacy: .public)")
            }
        } while rulesAgain && !stopped
        rulesListing = false
    }

    /// `refresh`, waited for.
    public func reload() async {
        gen += 1
        let my = gen
        pending += 1
        defer { pending -= 1 }
        let caller = caller
        do {
            let r = try await caller.call(API.RulesList.self, EmptyParams(), timeout: nil)
            let a = try await caller.call(API.AccountList.self, EmptyParams(), timeout: nil)
            var folders: [AccountID: [Folder]] = [:]
            for acc in a.accounts where Rules.isMailAccount(acc) {
                if let f = try? await caller.call(API.FolderList.self, FolderListParams(accountId: acc.id), timeout: nil) {
                    folders[acc.id] = f.folders
                }
            }
            guard my == gen, !stopped else { return }
            rules = r.rules
            accounts = a.accounts
            self.folders = folders
            loaded = true
            observers.notify()
        } catch {
            rulesLog.info("rules.list failed: \(ContactsOptions.asRPCError(error).code.rawValue, privacy: .public)")
        }
    }

    /// The folders of the main window's sidebar, fresher than the listing
    /// here (a folder made since): they replace what was listed for their
    /// accounts.
    public func merge(folders live: [AccountID: [Folder]]) {
        var changed = false
        for (account, list) in live where !list.isEmpty && folders[account] != list {
            folders[account] = list
            changed = true
        }
        if changed {
            observers.notify()
        }
    }

    // MARK: What the views show

    /// The mail accounts a rule can be for, in account order.
    public var mailAccounts: [Account] {
        accounts.filter(Rules.isMailAccount)
    }

    /// The names the summaries show.
    public var names: Rules.Names {
        let accounts = accounts
        let folders = folders
        return Rules.Names(
            account: { id in accounts.first { $0.id == id }.map(accountLabel) ?? "" },
            folder: { acc, id in
                guard let acc, let id, let f = folders[acc]?.first(where: { $0.id == id }) else { return "" }
                return folderTitle(f)
            })
    }

    /// Settings → Rules' rows.
    public var rows: [Rules.Row] {
        Rules.rows(rules, names: names, date: date)
    }

    public func rule(_ id: String) -> Rule? {
        rules.first { $0.id == id }
    }

    /// Apply Rules… of a folder of the account is enabled
    /// (`Rules.appliesToAccount`).
    public func applies(to account: AccountID) -> Bool {
        Rules.appliesToAccount(rules, account)
    }

    // MARK: Writes

    /// A row's switch: rules.update with enabled flipped to `on`, after
    /// `Rules.outgoingQuestion` when `Rules.needsConfirm` says so (a
    /// declined question changes nothing; the view shows the rule as it
    /// is again).
    public func setEnabled(_ id: String, _ on: Bool, confirm: Rules.Confirm) async -> String? {
        guard let r = rule(id), r.enabled != on, !busy.contains(id) else {
            observers.notify()
            return nil
        }
        let input = Rules.toggled(r)
        let ask = Rules.needsConfirm(r, input)
        if ask {
            guard await confirm(Rules.outgoingQuestion(input)) else {
                observers.notify()
                return nil
            }
        }
        return await write(id) {
            let res = try await $0.call(API.RulesUpdate.self, RulesUpdateParams(id: id, rule: input, confirmOutgoing: ask), timeout: nil)
            self.replace(res.rule)
        } failed: { Rules.errorText($0, failed: Rules.saveFailedText) }
    }

    /// A drag or ⌥↑/↓: the rule at `from` dropped at `to`
    /// (`Rules.moved`); shown at once, the daemon's order once it answers,
    /// the listed one again when it refuses.
    public func move(from: Int, to: Int) async -> String? {
        let ids = Rules.moved(rules.map(\.id), from: from, to: to)
        guard ids != rules.map(\.id) else { return nil }
        let before = rules
        rules = ids.compactMap { id in before.first { $0.id == id } }
        observers.notify()
        pending += 1
        defer { pending -= 1 }
        do {
            let res = try await caller.call(API.RulesReorder.self, RulesReorderParams(ids: ids), timeout: nil)
            rules = res.rules
            observers.notify()
            return nil
        } catch {
            rules = before
            observers.notify()
            refresh()
            return Rules.errorText(error, failed: Rules.saveFailedText)
        }
    }

    /// Remove: `Rules.deleteQuestion`, then rules.delete.
    public func remove(_ id: String, confirm: Rules.Confirm) async -> String? {
        guard let r = rule(id), !busy.contains(id) else { return nil }
        guard await confirm(Rules.deleteQuestion(r.name)) else { return nil }
        return await write(id) {
            _ = try await $0.call(API.RulesDelete.self, RulesDeleteParams(id: id), timeout: nil)
            self.rules.removeAll { $0.id == id }
        } failed: { Rules.errorText($0, failed: Rules.saveFailedText) }
    }

    /// Apply Rules… of a folder: `Rules.applyQuestion` with the folder's
    /// name, then rules.apply over the folder; the answer is the toast's
    /// sentence (`Rules.applyResultText`, or the failure's), nil when the
    /// question was declined or the folder already runs.
    public func apply(_ k: FolderKey, folderName: String, confirm: Rules.Confirm) async -> String? {
        guard !applying.contains(k) else { return nil }
        guard await confirm(Rules.applyQuestion(folderName)) else { return nil }
        applying.insert(k)
        observers.notify()
        pending += 1
        defer {
            pending -= 1
            applying.remove(k)
            observers.notify()
        }
        do {
            let res = try await caller.call(API.RulesApply.self, RulesApplyParams(accountId: k.account, folderId: k.folder), timeout: nil)
            return Rules.applyResultText(res)
        } catch {
            return Rules.errorText(error, failed: Rules.applyFailedText)
        }
    }

    /// A rule the editor saved: shown at once (the notification lists
    /// again).
    public func saved(_ r: Rule) {
        replace(r, append: true)
    }

    // MARK: Internals

    private func replace(_ r: Rule, append: Bool = false) {
        if let i = rules.firstIndex(where: { $0.id == r.id }) {
            rules[i] = r
        } else if append {
            rules.append(r)
        }
        observers.notify()
    }

    private func write(
        _ id: String, _ body: @MainActor (any RulesCaller) async throws -> Void,
        failed: (any Error) -> String
    ) async -> String? {
        busy.insert(id)
        observers.notify()
        pending += 1
        defer {
            pending -= 1
            busy.remove(id)
            observers.notify()
        }
        do {
            try await body(caller)
            return nil
        } catch {
            rulesLog.info("rules write failed: \(ContactsOptions.asRPCError(error).code.rawValue, privacy: .public)")
            refresh()
            return failed(error)
        }
    }
}

/// The rule editor's form (rules.Form): what the sheet shows and changes,
/// its problems (`Rules.validate`; Save is enabled without any), Save
/// (rules.create or rules.update after `Rules.outgoingQuestion` when
/// `Rules.needsConfirm`) and Try It (rules.test over the folder chosen).
@MainActor
public final class RuleEditorController {
    public private(set) var form: Rules.Form
    /// The stored rule edited; nil for a new one.
    public let stored: Rule?
    /// The mail accounts a rule can be for, and their folders.
    public let accounts: [Account]
    public private(set) var folders: [AccountID: [Folder]]
    /// A save is under way.
    public private(set) var saving = false
    /// The daemon refused the save ("" none).
    public private(set) var error = ""
    /// Try It: the folder it looks in, the line of its result ("" before),
    /// and whether it runs.
    public private(set) var testFolder: FolderKey?
    public private(set) var testText = ""
    public private(set) var testing = false
    /// Calls without their answer.
    public private(set) var pending = 0

    /// The form or a state changed; the view reads it anew.
    public var onChange: (@MainActor () -> Void)?

    private let caller: any RulesCaller

    public init(caller: any RulesCaller, form: Rules.Form, stored: Rule?, accounts: [Account], folders: [AccountID: [Folder]]) {
        self.caller = caller
        self.form = form
        self.stored = stored
        self.accounts = accounts.filter(Rules.isMailAccount)
        self.folders = folders
        testFolder = defaultTestFolder()
    }

    /// The editor of a new rule (Add Rule…).
    public convenience init(caller: any RulesCaller, newFor account: AccountID?, rules: RulesController) {
        self.init(caller: caller, form: Rules.newForm(account), stored: nil, accounts: rules.accounts, folders: rules.folders)
    }

    /// The editor of a stored rule (Edit Rule…).
    public convenience init(caller: any RulesCaller, editing r: Rule, rules: RulesController) {
        self.init(caller: caller, form: Rules.fromRule(r), stored: r, accounts: rules.accounts, folders: rules.folders)
    }

    // MARK: What the view shows

    public var problems: [Rules.Problem] { Rules.validate(form) }

    public var canSave: Bool { problems.isEmpty && !saving }

    /// The problems of one row ("name", "conditions", "actions" and the
    /// row's index; the index is 0 for the list's own).
    public func problemTexts(_ field: String, _ index: Int) -> [String] {
        problems.filter { $0.field == field && $0.index == index }.map(Rules.problemText)
    }

    /// The folders a Move To action offers (`Rules.moveFolders`) for the
    /// form's account.
    public var moveFolders: [Folder] {
        moveTargets.map(\.folder)
    }

    /// `moveFolders` in the sidebar's order with their depth there
    /// (`folderTree`, as the context menu's Move To indents them).
    public var moveTargets: [(folder: Folder, depth: Int)] {
        guard let id = form.accountId else { return [] }
        let list = folders[id] ?? []
        var tree: [(folder: Folder, depth: Int)] = []
        if let a = accounts.first(where: { $0.id == id }) {
            tree = folderTree(a, list, CollapseState(), FavouriteState())
                .filter { !$0.header }
                .compactMap { e in e.folder.map { (folder: $0, depth: e.depth) } }
        }
        if tree.isEmpty {
            tree = list.map { (folder: $0, depth: 0) }
        }
        let offered = Set(Rules.moveFolders(tree.map(\.folder)).map(\.id))
        return tree.filter { offered.contains($0.folder.id) }
    }

    /// The folders Try It can look in: the form's account's, or every mail
    /// account's for a rule for every account (`Rules.canApplyIn`).
    public var testFolders: [Folder] {
        let ids = form.accountId.map { [$0] } ?? accounts.map(\.id)
        return ids.flatMap { (folders[$0] ?? []).filter(Rules.canApplyIn) }
    }

    /// Add Action's menu (`Rules.availableActions`).
    public var availableActions: [RuleActionKind] { Rules.availableActions(form) }

    // MARK: Changes

    public func setName(_ s: String) { change { $0.name = s } }

    public func setEnabled(_ on: Bool) { change { $0.enabled = on } }

    public func setStop(_ on: Bool) { change { $0.stop = on } }

    public func setMatch(_ m: RuleMatch) { change { $0.match = m } }

    /// Apply to: a move action's folder belongs to the account before, so
    /// it goes; Try It looks in the new account's inbox.
    public func setAccount(_ id: AccountID?) {
        guard id != form.accountId else { return }
        change { f in
            f.accountId = id
            for i in f.actions.indices where f.actions[i].kind == .move {
                f.actions[i].folderId = nil
            }
        }
        testFolder = defaultTestFolder()
        testText = ""
        onChange?()
    }

    public func addCondition() {
        change { $0.conditions.append(Rules.newCondition(.from)) }
    }

    public func removeCondition(_ i: Int) {
        change { f in
            if f.conditions.indices.contains(i) { f.conditions.remove(at: i) }
        }
    }

    /// A condition's field: `Rules.newCondition` keeps what still fits.
    public func setField(_ i: Int, _ field: RuleField) {
        change { f in
            guard f.conditions.indices.contains(i), f.conditions[i].field != field else { return }
            f.conditions[i] = Rules.newCondition(field, old: f.conditions[i])
        }
    }

    public func setOperator(_ i: Int, _ op: RuleOperator) {
        change { f in
            if f.conditions.indices.contains(i) { f.conditions[i].op = op }
        }
    }

    public func setValue(_ i: Int, _ value: String) {
        change { f in
            if f.conditions.indices.contains(i) { f.conditions[i].value = value }
        }
    }

    public func addAction(_ kind: RuleActionKind) {
        guard availableActions.contains(kind) else { return }
        change { $0.actions.append(RuleAction(kind: kind)) }
    }

    public func removeAction(_ i: Int) {
        change { f in
            if f.actions.indices.contains(i) { f.actions.remove(at: i) }
        }
    }

    public func setFolder(_ i: Int, _ id: FolderID?) {
        change { f in
            if f.actions.indices.contains(i) { f.actions[i].folderId = id }
        }
    }

    public func setForwardTo(_ i: Int, _ to: String) {
        change { f in
            if f.actions.indices.contains(i) { f.actions[i].to = to }
        }
    }

    public func setReplySubject(_ i: Int, _ s: String) {
        change { f in
            if f.actions.indices.contains(i) { f.actions[i].subject = s }
        }
    }

    public func setReplyText(_ i: Int, _ s: String) {
        change { f in
            if f.actions.indices.contains(i) { f.actions[i].text = s }
        }
    }

    /// Try It's folder.
    public func setTestFolder(_ k: FolderKey?) {
        testFolder = k
        testText = ""
        onChange?()
    }

    // MARK: Save and Try It

    /// Save: rules.create or rules.update with `Form.trimmed`, after
    /// `Rules.outgoingQuestion` when `Rules.needsConfirm`. The stored rule
    /// on success; nil when the form has problems, the question was
    /// declined or the daemon refused (`error` says why).
    public func save(confirm: Rules.Confirm) async -> Rule? {
        guard canSave else { return nil }
        let input = form.trimmed
        let ask = Rules.needsConfirm(stored, input)
        if ask {
            guard await confirm(Rules.outgoingQuestion(input)) else { return nil }
        }
        saving = true
        error = ""
        onChange?()
        pending += 1
        defer {
            pending -= 1
            saving = false
            onChange?()
        }
        do {
            let res: RuleResult
            if form.id.isEmpty {
                res = try await caller.call(API.RulesCreate.self, RulesCreateParams(rule: input, confirmOutgoing: ask), timeout: nil)
            } else {
                res = try await caller.call(
                    API.RulesUpdate.self, RulesUpdateParams(id: form.id, rule: input, confirmOutgoing: ask), timeout: nil)
            }
            return res.rule
        } catch {
            rulesLog.info("rules save failed: \(ContactsOptions.asRPCError(error).code.rawValue, privacy: .public)")
            self.error = Rules.errorText(error, failed: Rules.saveFailedText)
            return nil
        }
    }

    /// Try It: rules.test with the form as edited over `testFolder`; the
    /// line is `Rules.testResultText`, or the daemon's refusal of the rule.
    public func test() async {
        guard let k = testFolder, problems.isEmpty, !testing else { return }
        testing = true
        testText = ""
        onChange?()
        pending += 1
        defer {
            pending -= 1
            testing = false
            onChange?()
        }
        do {
            let res = try await caller.call(
                API.RulesTest.self, RulesTestParams(rule: form.trimmed, accountId: k.account, folderId: k.folder), timeout: nil)
            testText = Rules.testResultText(res.count)
        } catch {
            rulesLog.info("rules.test failed: \(ContactsOptions.asRPCError(error).code.rawValue, privacy: .public)")
            // The daemon's refusal of the rule as the editor says it, an
            // invalidArgument without data as a rule it cannot use, anything
            // else as the usual sentence of a failed action (no network, a
            // timeout, no backend), never the daemon's message.
            let e = ContactsOptions.asRPCError(error)
            if e.code == .invalidArgument {
                testText = Rules.invalidText(e.ruleInvalid ?? RuleInvalidData(field: ""))
            } else {
                testText = rpcErrorText(Rules.testButton, error)
            }
        }
    }

    // MARK: Internals

    private func change(_ f: (inout Rules.Form) -> Void) {
        let before = form
        f(&form)
        if form != before {
            error = ""
            onChange?()
        }
    }

    /// rules.TestFolderLabel: the inbox of the rule's account, or of the
    /// first mail account.
    private func defaultTestFolder() -> FolderKey? {
        guard let a = form.accountId ?? accounts.first?.id else { return nil }
        guard let f = (folders[a] ?? []).first(where: { $0.role == .inbox }) ?? (folders[a] ?? []).first(where: Rules.canApplyIn) else {
            return nil
        }
        return FolderKey(account: a, folder: f.id)
    }
}
