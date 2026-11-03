// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Invented rules for looking at Settings → Rules and the rule editor
// without a daemon (MALACHI_RULES_SAMPLES=1, read once at launch; the dev
// steps `prefs-rules` and `rules-editor` of MALACHI_START use it): a
// `RulesCaller` that answers account.list, folder.list and rules.* from
// memory, as the daemon would (create appends, reorder takes the whole
// order, test counts a few, apply changes nothing). Two mail accounts and
// four rules: one that moves a newsletter, one for every account, one that
// forwards (outgoing) and one the daemon turned off. Nothing here is
// anybody's data.

import Foundation

/// The samples' daemon. An actor: the controllers call it from tasks.
public actor SampleRulesCaller: RulesCaller {
    /// MALACHI_RULES_SAMPLES=1.
    public static var requested: Bool {
        ProcessInfo.processInfo.environment["MALACHI_RULES_SAMPLES"] == "1"
    }

    public static let work: AccountID = "acc_sample_work"
    public static let home: AccountID = "acc_sample_home"
    /// The rule the dev step `rules-editor` opens.
    public static let newsletters = "r_sample_news"

    /// Technical English of the samples' errors (never shown).
    private static let notSampled = "not in the samples"
    private static let wrongType = "sample type"

    private var rules: [Rule]
    private let accounts: [Account]
    private let folders: [AccountID: [Folder]]
    private var next = 1

    public init(now: Date = Date()) {
        // macOS-only strings: invented sample data, not for translation.
        accounts = [Self.work, Self.home].map { id in
            let work = id == Self.work
            return Account(
                id: id,
                config: AccountConfig(name: work ? "Work" : "Home", email: work ? "jana@firma.example" : "jana@example.org"),
                enabled: true, state: SyncState(accountId: id, status: .idle))
        }
        func folder(_ acc: AccountID, _ id: String, _ name: String, _ role: FolderRole = .none, total: Int = 20) -> Folder {
            Folder(id: FolderID(id), accountId: acc, name: name, path: name, role: role, subscribed: true, selectable: true,
                   synced: true, unread: 0, total: total)
        }
        folders = [
            Self.work: [
                folder(Self.work, "f_w_inbox", "INBOX", .inbox, total: 120), folder(Self.work, "f_w_sent", "Sent", .sent),
                folder(Self.work, "f_w_drafts", "Drafts", .drafts), folder(Self.work, "f_w_trash", "Trash", .trash),
                folder(Self.work, "f_w_junk", "Junk", .junk), folder(Self.work, "f_w_news", "Newsletters"),
                folder(Self.work, "f_w_invoices", "Invoices"),
            ],
            Self.home: [
                folder(Self.home, "f_h_inbox", "INBOX", .inbox, total: 40), folder(Self.home, "f_h_sent", "Sent", .sent),
                folder(Self.home, "f_h_trash", "Trash", .trash), folder(Self.home, "f_h_lists", "Lists"),
            ],
        ]
        let week = now.addingTimeInterval(-7 * 86400)
        rules = [
            Rule(id: Self.newsletters, input: RuleInput(
                name: "Newsletters", enabled: true, accountId: Self.work, match: .any,
                conditions: [RuleCondition(field: .bulk, op: .is, value: "newsletter"),
                             RuleCondition(field: .from, op: .domainIs, value: "news.example.com")],
                actions: [RuleAction(kind: .markRead), RuleAction(kind: .move, folderId: "f_w_news")]),
                 createdAt: week, updatedAt: week, applied: 37, lastAppliedAt: now.addingTimeInterval(-3600)),
            Rule(id: "r_sample_invoices", input: RuleInput(
                name: "Invoices", enabled: true, match: .all,
                conditions: [RuleCondition(field: .subject, op: .contains, value: "invoice"),
                             RuleCondition(field: .hasAttachments, op: .is, value: "true")],
                actions: [RuleAction(kind: .flag)], stop: true),
                 createdAt: week, updatedAt: week),
            Rule(id: "r_sample_forward", input: RuleInput(
                name: "Boss to phone", enabled: true, accountId: Self.work, match: .all,
                conditions: [RuleCondition(field: .from, op: .is, value: "boss@firma.example")],
                actions: [RuleAction(kind: .forward, to: "jana.phone@example.org")]),
                 outgoing: true, createdAt: week, updatedAt: week, applied: 4, lastAppliedAt: week, skipped: 1),
            Rule(id: "r_sample_lists", input: RuleInput(
                name: "Mailing lists", enabled: false, accountId: Self.home, match: .all,
                conditions: [RuleCondition(field: .listId, op: .endsWith, value: "lists.example.net")],
                actions: [RuleAction(kind: .move, folderId: "f_h_gone")]),
                 problem: .folderGone, createdAt: week, updatedAt: week, applied: 12, lastAppliedAt: week),
        ]
    }

    // MARK: RulesCaller

    public func call<M: RPCMethod>(_ method: M.Type, _ params: M.Params, timeout: Duration?) async throws -> M.Result {
        try await Task.sleep(for: .milliseconds(40))
        let result: any Sendable
        switch params {
        case is EmptyParams where M.name == API.AccountList.name:
            result = AccountListResult(accounts: accounts)
        case is EmptyParams where M.name == API.RulesList.name:
            result = RulesListResult(rules: rules)
        case let p as FolderListParams:
            result = FolderListResult(folders: folders[p.accountId] ?? [])
        case let p as RulesCreateParams:
            try check(p.rule, confirmed: p.confirmOutgoing, old: nil)
            let r = stored(id: "r_sample_new_\(next)", p.rule)
            next += 1
            rules.append(r)
            result = RuleResult(rule: r)
        case let p as RulesUpdateParams:
            guard let i = rules.firstIndex(where: { $0.id == p.id }) else { throw RPCError(code: .ruleNotFound, message: Self.notSampled) }
            try check(p.rule, confirmed: p.confirmOutgoing, old: rules[i])
            var r = stored(id: p.id, p.rule)
            r.applied = rules[i].applied
            r.lastAppliedAt = rules[i].lastAppliedAt
            r.skipped = rules[i].skipped
            rules[i] = r
            result = RuleResult(rule: r)
        case let p as RulesDeleteParams:
            guard rules.contains(where: { $0.id == p.id }) else { throw RPCError(code: .ruleNotFound, message: Self.notSampled) }
            rules.removeAll { $0.id == p.id }
            result = EmptyResult()
        case let p as RulesReorderParams:
            guard Set(p.ids) == Set(rules.map(\.id)), p.ids.count == rules.count else {
                throw RPCError(code: .invalidArgument, message: Self.notSampled)
            }
            let before = rules
            rules = p.ids.compactMap { id in before.first { $0.id == id } }
            result = RulesListResult(rules: rules)
        case is RulesTestParams:
            result = RulesTestResult(count: 3)
        case is RulesApplyParams:
            result = RulesApplyResult(examined: 120, matched: 2, changed: 2)
        default:
            throw RPCError(code: .notImplemented, message: Self.notSampled)
        }
        guard let r = result as? M.Result else { throw RPCError(code: .internalError, message: Self.wrongType) }
        return r
    }

    private func check(_ input: RuleInput, confirmed: Bool, old: Rule?) throws {
        if Rules.needsConfirm(old, input), !confirmed {
            throw RPCError(code: .invalidArgument, message: Self.notSampled, data: .object(["field": .string("confirmOutgoing")]))
        }
    }

    private func stored(id: String, _ input: RuleInput) -> Rule {
        let now = Date()
        return Rule(id: id, input: input, outgoing: Rules.isOutgoing(input), createdAt: now, updatedAt: now)
    }
}
