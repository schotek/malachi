// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Mail rules (docs/api.md §4.21, §5 notify.rulesChanged;
// backend/pkg/api/rules.go; the design is docs/plans/rules.md). A rule is
// conditions over a message's addresses, subject, text and classification,
// and actions on it. The daemon evaluates the enabled rules, in their
// order, on every message stored in the inbox of a mail account (imap,
// graph) once per Message-ID; rules.apply runs them on stored mail by
// hand, with the local actions only. Every string of a rule may come from
// mail (a prefilled sender, a list id): it is shown as plain text only.

import Foundation

/// api.RuleMatch: how a rule's conditions combine.
public struct RuleMatch: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Every condition holds.
    public static let all: RuleMatch = "all"
    /// At least one condition holds.
    public static let any: RuleMatch = "any"
}

/// api.RuleField: what a condition looks at.
public struct RuleField: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// The From addresses (name and address).
    public static let from: RuleField = "from"
    public static let to: RuleField = "to"
    public static let cc: RuleField = "cc"
    /// The To and Cc addresses.
    public static let toOrCc: RuleField = "toOrCc"
    public static let subject: RuleField = "subject"
    /// The plain text of the message, its first `API.Limits.maxRuleBodyBytes`.
    public static let body: RuleField = "body"
    /// The List-Id identifier as `BulkInfo.listId`.
    public static let listId: RuleField = "listId"
    /// The bulk classification: `is`/`isNot` with `RuleBulk.any` or a
    /// `BulkKind`.
    public static let bulk: RuleField = "bulk"
    /// `is` with "true" or "false".
    public static let hasAttachments: RuleField = "hasAttachments"
}

/// api.RuleBulkAny: the value of a bulk condition that holds for every
/// kind.
public enum RuleBulk {
    public static let any = "any"
}

/// api.RuleOperator: how a condition compares. Text comparisons ignore case
/// after NFC normalisation; an address field holds when any of its
/// addresses does (notContains and isNot: when none does).
public struct RuleOperator: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let contains: RuleOperator = "contains"
    public static let notContains: RuleOperator = "notContains"
    public static let `is`: RuleOperator = "is"
    public static let isNot: RuleOperator = "isNot"
    public static let startsWith: RuleOperator = "startsWith"
    public static let endsWith: RuleOperator = "endsWith"
    /// The address's domain is the value or a subdomain of it (address
    /// fields only).
    public static let domainIs: RuleOperator = "domainIs"
}

/// api.RuleCondition: one condition of a rule.
public struct RuleCondition: Codable, Sendable, Hashable {
    public var field: RuleField
    public var op: RuleOperator
    public var value: String

    public init(field: RuleField, op: RuleOperator, value: String = "") {
        self.field = field
        self.op = op
        self.value = value
    }

    private enum CodingKeys: String, CodingKey {
        case field, op, value
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        field = try c.decode(RuleField.self, forKey: .field)
        op = try c.decodeIfPresent(RuleOperator.self, forKey: .op) ?? ""
        value = try c.decodeIfPresent(String.self, forKey: .value) ?? ""
    }
}

/// api.RuleActionKind: what an action does.
public struct RuleActionKind: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Moves the message to `folderId` (a selectable folder of the rule's
    /// account, not the outbox or drafts); only a rule with an account has
    /// it.
    public static let move: RuleActionKind = "move"
    public static let markRead: RuleActionKind = "markRead"
    /// Stars the message.
    public static let flag: RuleActionKind = "flag"
    /// Moves the message to the account's trash.
    public static let trash: RuleActionKind = "trash"
    /// Moves the message to the account's junk folder.
    public static let junk: RuleActionKind = "junk"
    /// Sends the message, attached whole, to `to` (outgoing).
    public static let forward: RuleActionKind = "forward"
    /// Answers the sender with `text` (outgoing; at most once per sender
    /// in `API.Limits.ruleReplyInterval`).
    public static let autoReply: RuleActionKind = "autoReply"

    /// api.RuleActionKind.IsOutgoing: the action sends mail.
    public var isOutgoing: Bool { self == .forward || self == .autoReply }
}

/// api.RuleAction: one action of a rule. Only the fields of its kind are
/// set; the others are left out on the wire (Go's omitempty).
public struct RuleAction: Codable, Sendable, Hashable {
    public var kind: RuleActionKind
    /// The target of move; nil for none.
    public var folderId: FolderID?
    /// The one address of forward.
    public var to: String
    /// Subject of autoReply; empty = "Re: " and the message's subject.
    public var subject: String
    /// The plain text of autoReply.
    public var text: String

    public init(kind: RuleActionKind, folderId: FolderID? = nil, to: String = "", subject: String = "", text: String = "") {
        self.kind = kind
        self.folderId = folderId
        self.to = to
        self.subject = subject
        self.text = text
    }

    private enum CodingKeys: String, CodingKey {
        case kind, folderId, to, subject, text
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        kind = try c.decode(RuleActionKind.self, forKey: .kind)
        let folder = try c.decodeIfPresent(FolderID.self, forKey: .folderId)
        folderId = folder?.rawValue.isEmpty == false ? folder : nil
        to = try c.decodeIfPresent(String.self, forKey: .to) ?? ""
        subject = try c.decodeIfPresent(String.self, forKey: .subject) ?? ""
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(kind, forKey: .kind)
        if let folderId, !folderId.rawValue.isEmpty {
            try c.encode(folderId, forKey: .folderId)
        }
        if !to.isEmpty {
            try c.encode(to, forKey: .to)
        }
        if !subject.isEmpty {
            try c.encode(subject, forKey: .subject)
        }
        if !text.isEmpty {
            try c.encode(text, forKey: .text)
        }
    }
}

/// api.RuleProblem: why the daemon stopped a rule (it is disabled then).
public struct RuleProblem: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// The folder of a move action is gone.
    public static let folderGone: RuleProblem = "folderGone"
    /// An outgoing action could not be queued.
    public static let sendFailed: RuleProblem = "sendFailed"
}

/// api.RuleInput: a rule as the user edits it.
public struct RuleInput: Codable, Sendable, Hashable {
    public var name: String
    public var enabled: Bool
    /// Limits the rule to one mail account; nil = every enabled mail
    /// account (no move action then). Left out on the wire when nil.
    public var accountId: AccountID?
    public var match: RuleMatch
    @NullAsEmpty public var conditions: [RuleCondition]
    @NullAsEmpty public var actions: [RuleAction]
    /// No later rule runs on a message this rule matched.
    public var stop: Bool

    public init(
        name: String = "", enabled: Bool = false, accountId: AccountID? = nil, match: RuleMatch = .all,
        conditions: [RuleCondition] = [], actions: [RuleAction] = [], stop: Bool = false
    ) {
        self.name = name
        self.enabled = enabled
        self.accountId = accountId
        self.match = match
        self.conditions = conditions
        self.actions = actions
        self.stop = stop
    }

    private enum CodingKeys: String, CodingKey {
        case name, enabled, accountId, match, conditions, actions, stop
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        let account = try c.decodeIfPresent(AccountID.self, forKey: .accountId)
        accountId = account?.rawValue.isEmpty == false ? account : nil
        match = try c.decodeIfPresent(RuleMatch.self, forKey: .match) ?? ""
        _conditions = try c.decode(NullAsEmpty<RuleCondition>.self, forKey: .conditions)
        _actions = try c.decode(NullAsEmpty<RuleAction>.self, forKey: .actions)
        stop = try c.decodeIfPresent(Bool.self, forKey: .stop) ?? false
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(name, forKey: .name)
        try c.encode(enabled, forKey: .enabled)
        if let accountId, !accountId.rawValue.isEmpty {
            try c.encode(accountId, forKey: .accountId)
        }
        try c.encode(match, forKey: .match)
        try c.encode(conditions, forKey: .conditions)
        try c.encode(actions, forKey: .actions)
        try c.encode(stop, forKey: .stop)
    }
}

/// api.Rule: a stored rule; Go embeds `RuleInput`, so its fields sit next
/// to the rule's own on the wire (`input` here).
public struct Rule: Codable, Sendable, Hashable {
    public var id: String
    public var input: RuleInput
    /// An action sends mail (forward, autoReply).
    public var outgoing: Bool
    /// Set when the daemon disabled the rule.
    public var problem: RuleProblem?
    public var createdAt: Date
    public var updatedAt: Date
    /// The messages the rule acted on (automatically and by rules.apply);
    /// `lastAppliedAt` the latest.
    public var applied: Int
    public var lastAppliedAt: Date?
    /// Outgoing actions the protections refused (bulk or automatic mail,
    /// own address, limits).
    public var skipped: Int

    public init(
        id: String, input: RuleInput, outgoing: Bool = false, problem: RuleProblem? = nil,
        createdAt: Date = .goZero, updatedAt: Date = .goZero, applied: Int = 0, lastAppliedAt: Date? = nil, skipped: Int = 0
    ) {
        self.id = id
        self.input = input
        self.outgoing = outgoing
        self.problem = problem
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.applied = applied
        self.lastAppliedAt = lastAppliedAt
        self.skipped = skipped
    }

    // The embedded input's fields, as Go reads them (r.Name, r.Enabled, …).
    public var name: String { input.name }
    public var enabled: Bool { input.enabled }
    public var accountId: AccountID? { input.accountId }
    public var match: RuleMatch { input.match }
    public var conditions: [RuleCondition] { input.conditions }
    public var actions: [RuleAction] { input.actions }
    public var stop: Bool { input.stop }

    private enum CodingKeys: String, CodingKey {
        case id, outgoing, problem, createdAt, updatedAt, applied, lastAppliedAt, skipped
    }

    public init(from decoder: any Decoder) throws {
        input = try RuleInput(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        outgoing = try c.decodeIfPresent(Bool.self, forKey: .outgoing) ?? false
        let p = try c.decodeIfPresent(RuleProblem.self, forKey: .problem)
        problem = p?.rawValue.isEmpty == false ? p : nil
        createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt) ?? .goZero
        updatedAt = try c.decodeIfPresent(Date.self, forKey: .updatedAt) ?? .goZero
        applied = try c.decodeIfPresent(Int.self, forKey: .applied) ?? 0
        lastAppliedAt = try c.decodeIfPresent(Date.self, forKey: .lastAppliedAt)
        skipped = try c.decodeIfPresent(Int.self, forKey: .skipped) ?? 0
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try input.encode(to: encoder)
        try c.encode(outgoing, forKey: .outgoing)
        if let problem, !problem.rawValue.isEmpty {
            try c.encode(problem, forKey: .problem)
        }
        try c.encode(createdAt, forKey: .createdAt)
        try c.encode(updatedAt, forKey: .updatedAt)
        try c.encode(applied, forKey: .applied)
        if let lastAppliedAt {
            try c.encode(lastAppliedAt, forKey: .lastAppliedAt)
        }
        try c.encode(skipped, forKey: .skipped)
    }
}

/// api.RuleInvalidData: `error.data` of invalidArgument for a rule the
/// daemon refuses: the field ("name", "accountId", "conditions",
/// "actions", "confirmOutgoing") and, for conditions and actions, the
/// index.
public struct RuleInvalidData: Codable, Sendable, Equatable {
    public var field: String
    public var index: Int

    public init(field: String, index: Int = 0) {
        self.field = field
        self.index = index
    }

    private enum CodingKeys: String, CodingKey {
        case field, index
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        field = try c.decodeIfPresent(String.self, forKey: .field) ?? ""
        // Read leniently: `RPCError.data` keeps numbers as doubles, and
        // re-encoded they may come back as "2.0".
        if let i = try? c.decodeIfPresent(Int.self, forKey: .index) {
            index = i
        } else if let d = try? c.decodeIfPresent(Double.self, forKey: .index), d.isFinite, d >= 0, d < 1e6 {
            index = Int(d)
        } else {
            index = 0
        }
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(field, forKey: .field)
        if index != 0 {
            try c.encode(index, forKey: .index)
        }
    }
}

extension RPCError {
    /// The data of an invalidArgument about a rule; nil for any other code
    /// or data of another shape.
    public var ruleInvalid: RuleInvalidData? {
        guard code == .invalidArgument, let data, data.objectValue != nil,
              let raw = try? JSONCoding.encoder().encode(data),
              let d = try? JSONCoding.decoder().decode(RuleInvalidData.self, from: raw), !d.field.isEmpty else { return nil }
        return d
    }
}

// MARK: rules.* (§4.21)

/// api.RulesListResult: every rule in its order.
public struct RulesListResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var rules: [Rule]

    public init(rules: [Rule] = []) {
        self.rules = rules
    }
}

/// api.RulesCreateParams: adds a rule at the end. `confirmOutgoing` is
/// required when the rule is enabled with an outgoing action: the client
/// asked the user.
public struct RulesCreateParams: Codable, Sendable, Equatable {
    public var rule: RuleInput
    public var confirmOutgoing: Bool

    public init(rule: RuleInput, confirmOutgoing: Bool = false) {
        self.rule = rule
        self.confirmOutgoing = confirmOutgoing
    }

    private enum CodingKeys: String, CodingKey {
        case rule, confirmOutgoing
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        rule = try c.decode(RuleInput.self, forKey: .rule)
        confirmOutgoing = try c.decodeIfPresent(Bool.self, forKey: .confirmOutgoing) ?? false
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(rule, forKey: .rule)
        if confirmOutgoing {
            try c.encode(true, forKey: .confirmOutgoing)
        }
    }
}

/// api.RulesCreateResult and api.RulesUpdateResult: the rule as stored.
public struct RuleResult: Codable, Sendable, Equatable {
    public var rule: Rule

    public init(rule: Rule) {
        self.rule = rule
    }
}

/// api.RulesUpdateParams: replaces a rule's input. `confirmOutgoing` is
/// required when the updated rule is enabled with an outgoing action and
/// was not before, or an outgoing action's to, subject or text changed.
public struct RulesUpdateParams: Codable, Sendable, Equatable {
    public var id: String
    public var rule: RuleInput
    public var confirmOutgoing: Bool

    public init(id: String, rule: RuleInput, confirmOutgoing: Bool = false) {
        self.id = id
        self.rule = rule
        self.confirmOutgoing = confirmOutgoing
    }

    private enum CodingKeys: String, CodingKey {
        case id, rule, confirmOutgoing
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        rule = try c.decode(RuleInput.self, forKey: .rule)
        confirmOutgoing = try c.decodeIfPresent(Bool.self, forKey: .confirmOutgoing) ?? false
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(rule, forKey: .rule)
        if confirmOutgoing {
            try c.encode(true, forKey: .confirmOutgoing)
        }
    }
}

/// api.RulesDeleteParams.
public struct RulesDeleteParams: Codable, Sendable, Equatable {
    public var id: String

    public init(id: String) {
        self.id = id
    }
}

/// api.RulesReorderParams: the whole new order, every rule's id once.
public struct RulesReorderParams: Codable, Sendable, Equatable {
    public var ids: [String]

    public init(ids: [String]) {
        self.ids = ids
    }
}

/// api.RulesTestParams: which stored messages of a folder a rule, as
/// edited and not saved, matches.
public struct RulesTestParams: Codable, Sendable, Equatable {
    public var rule: RuleInput
    public var accountId: AccountID
    public var folderId: FolderID

    public init(rule: RuleInput, accountId: AccountID, folderId: FolderID) {
        self.rule = rule
        self.accountId = accountId
        self.folderId = folderId
    }
}

/// api.RulesTestResult: how many stored messages of the folder match and
/// the newest `API.Limits.maxRuleTestMessages` of them, newest first.
public struct RulesTestResult: Codable, Sendable, Equatable {
    public var count: Int
    @NullAsEmpty public var messages: [MessageSummary]

    public init(count: Int = 0, messages: [MessageSummary] = []) {
        self.count = count
        self.messages = messages
    }

    private enum CodingKeys: String, CodingKey {
        case count, messages
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        count = try c.decodeIfPresent(Int.self, forKey: .count) ?? 0
        _messages = try c.decode(NullAsEmpty<MessageSummary>.self, forKey: .messages)
    }
}

/// api.RulesApplyParams: runs rules by hand on stored mail, a folder's
/// messages (`folderId`) or `messageIds` of one account, never both.
/// `ruleIds` picks the rules; nil = every enabled rule of the account.
/// Only the local actions run.
public struct RulesApplyParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var folderId: FolderID?
    public var messageIds: [MessageID]?
    public var ruleIds: [String]?

    public init(accountId: AccountID, folderId: FolderID? = nil, messageIds: [MessageID]? = nil, ruleIds: [String]? = nil) {
        self.accountId = accountId
        self.folderId = folderId
        self.messageIds = messageIds
        self.ruleIds = ruleIds
    }

    private enum CodingKeys: String, CodingKey {
        case accountId, folderId, messageIds, ruleIds
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(accountId, forKey: .accountId)
        if let folderId, !folderId.rawValue.isEmpty {
            try c.encode(folderId, forKey: .folderId)
        }
        if let messageIds, !messageIds.isEmpty {
            try c.encode(messageIds, forKey: .messageIds)
        }
        if let ruleIds, !ruleIds.isEmpty {
            try c.encode(ruleIds, forKey: .ruleIds)
        }
    }
}

/// api.RulesApplyResult: what rules.apply did.
public struct RulesApplyResult: Codable, Sendable, Equatable {
    /// Messages looked at, messages a rule matched, messages an action
    /// changed.
    public var examined: Int
    public var matched: Int
    public var changed: Int
    /// The outgoing actions left out.
    public var skippedOutgoing: Int

    public init(examined: Int = 0, matched: Int = 0, changed: Int = 0, skippedOutgoing: Int = 0) {
        self.examined = examined
        self.matched = matched
        self.changed = changed
        self.skippedOutgoing = skippedOutgoing
    }

    private enum CodingKeys: String, CodingKey {
        case examined, matched, changed, skippedOutgoing
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        examined = try c.decodeIfPresent(Int.self, forKey: .examined) ?? 0
        matched = try c.decodeIfPresent(Int.self, forKey: .matched) ?? 0
        changed = try c.decodeIfPresent(Int.self, forKey: .changed) ?? 0
        skippedOutgoing = try c.decodeIfPresent(Int.self, forKey: .skippedOutgoing) ?? 0
    }
}

/// api.RulesChangedNotification (`notify.rulesChanged`): a rule was added,
/// changed, reordered, deleted or disabled by the daemon, or its counts
/// moved. The client calls rules.list again. No payload.
public struct RulesChangedNotification: Codable, Sendable, Equatable {
    public init() {}
}

// MARK: Methods

extension API {
    public enum RulesList: RPCMethod {
        public typealias Params = EmptyParams
        public typealias Result = RulesListResult
        public static let name = "rules.list"
    }

    public enum RulesCreate: RPCMethod {
        public typealias Params = RulesCreateParams
        public typealias Result = RuleResult
        public static let name = "rules.create"
    }

    public enum RulesUpdate: RPCMethod {
        public typealias Params = RulesUpdateParams
        public typealias Result = RuleResult
        public static let name = "rules.update"
    }

    public enum RulesDelete: RPCMethod {
        public typealias Params = RulesDeleteParams
        public typealias Result = EmptyResult
        public static let name = "rules.delete"
    }

    public enum RulesReorder: RPCMethod {
        public typealias Params = RulesReorderParams
        public typealias Result = RulesListResult
        public static let name = "rules.reorder"
    }

    /// Reads the folder's stored messages (up to the bodies of a body
    /// condition).
    public enum RulesTest: RPCMethod {
        public typealias Params = RulesTestParams
        public typealias Result = RulesTestResult
        public static let name = "rules.test"
        public static let timeout = RPCTimeouts.rulesTest
    }

    /// Runs over a whole folder in batches before it answers.
    public enum RulesApply: RPCMethod {
        public typealias Params = RulesApplyParams
        public typealias Result = RulesApplyResult
        public static let name = "rules.apply"
        public static let timeout = RPCTimeouts.rulesApply
    }

    /// The rules' methods, which end methods.go's table.
    static let rulesMethods: [any RPCMethod.Type] = [
        RulesList.self, RulesCreate.self, RulesUpdate.self, RulesDelete.self,
        RulesReorder.self, RulesTest.self, RulesApply.self,
    ]
}

// MARK: Limits

extension API.Limits {
    /// pkg/api/rules.go.
    public static let maxRules = 200
    public static let maxRuleConditions = 20
    public static let maxRuleActions = 10
    public static let maxRuleNameBytes = 200
    public static let maxRuleValueBytes = 500
    public static let maxRuleReplyBytes = 8192
    public static let maxRuleBodyBytes = 64 * 1024
    public static let maxRuleTestMessages = 50
    /// api.RuleOutgoingPerHour: what one rule sends in an hour at most.
    public static let ruleOutgoingPerHour = 20
    /// api.RuleReplyInterval: how long autoReply leaves a sender alone.
    public static let ruleReplyInterval: TimeInterval = 4 * 24 * 3600
}
