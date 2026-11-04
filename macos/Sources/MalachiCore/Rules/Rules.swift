// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The mail rules' pure UI logic, ported 1:1 from ui/internal/rules
/// (docs/plans/rules.md, docs/api.md §4.21): the editable form of a rule
/// and its validation, the choices each row of the editor offers, a rule
/// prefilled from a message (Create Rule from Message…), the order after a
/// drag, which outgoing changes need the user's confirmation, which rules
/// apply to an account (Apply Rules… of a folder's menu), and every text
/// the Rules page, the editor and the questions show (RulesText.swift,
/// with the GTK msgids as keys).
///
/// The daemon stores and runs the rules (rules.*); this only turns them
/// into view models and back. Every string of a rule may come from mail (a
/// prefilled sender, a list id): it is shown as plain text only. Lengths
/// are counted in UTF-8 bytes, as the daemon counts them.
///
/// The flow (`RulesController`, Settings → Rules, the rule editor sheet
/// and the context menus):
///
///   - Settings → Rules (after Integrations) calls rules.list at open and
///     on every notify.rulesChanged and shows `rows`; a row's switch calls
///     rules.update with enabled flipped (`outgoingQuestion` first when
///     `needsConfirm` says so), a drag or ⌥↑/↓ rules.reorder with `moved`,
///     Remove asks `deleteQuestion` and calls rules.delete;
///   - Add Rule… opens the editor with `newForm`, Edit Rule… with
///     `fromRule`; Save is enabled when `validate` finds nothing and sends
///     rules.create / rules.update with `Form.input` (after
///     `outgoingQuestion` when `needsConfirm`), Try It calls rules.test
///     over the folder chosen and shows `testResultText`;
///   - Create Rule from Message… opens the editor with `fromMessage`;
///   - Apply Rules… of a folder's menu (offered when `appliesToAccount`
///     finds a rule) asks `applyQuestion`, calls rules.apply and shows
///     `applyResultText` as a toast.
public enum Rules {
    /// rules.Fields: the condition fields in the order the editor offers
    /// them.
    public static let fields: [RuleField] = [
        .from, .to, .cc, .toOrCc, .subject, .body, .listId, .bulk, .hasAttachments,
    ]

    /// rules.ActionKinds: the action kinds in the order the editor offers
    /// them (and the daemon runs them).
    public static let actionKinds: [RuleActionKind] = [
        .markRead, .flag, .forward, .autoReply, .move, .junk, .trash,
    ]

    /// rules.BulkValues: the values of a bulk condition, in the editor's
    /// order.
    public static let bulkValues: [String] = [
        RuleBulk.any, BulkKind.newsletter.rawValue, BulkKind.list.rawValue, BulkKind.automated.rawValue,
    ]

    /// The values of a hasAttachments condition.
    public static let valueTrue = "true"
    public static let valueFalse = "false"

    /// rules.ValueKind: what the value of a condition is edited with.
    public enum ValueKind: Sendable, Equatable {
        /// A text field.
        case textField
        /// A choice of `bulkValues`.
        case bulk
        /// A choice of `valueTrue` and `valueFalse`.
        case bool
    }

    /// rules.ValueKindOf.
    public static func valueKind(of f: RuleField) -> ValueKind {
        switch f {
        case .bulk: .bulk
        case .hasAttachments: .bool
        default: .textField
        }
    }

    /// rules.IsAddressField.
    public static func isAddressField(_ f: RuleField) -> Bool {
        f == .from || f == .to || f == .cc || f == .toOrCc
    }

    /// rules.OperatorsFor: the operators a field takes, in the editor's
    /// order; the first is the default.
    public static func operators(for f: RuleField) -> [RuleOperator] {
        switch valueKind(of: f) {
        case .bulk:
            return [.is, .isNot]
        case .bool:
            return [.is]
        case .textField:
            var ops: [RuleOperator] = [.contains, .notContains, .is, .isNot, .startsWith, .endsWith]
            if isAddressField(f) {
                ops.append(.domainIs)
            }
            return ops
        }
    }

    /// rules.DefaultValue: the value a condition gets when its field is
    /// chosen.
    public static func defaultValue(_ f: RuleField) -> String {
        switch valueKind(of: f) {
        case .bulk: RuleBulk.any
        case .bool: valueTrue
        case .textField: ""
        }
    }

    /// rules.NewCondition: a new row of the editor (From contains), or the
    /// row whose field the user changed: the operator kept when the new
    /// field takes it, the value when it is text both times.
    public static func newCondition(_ f: RuleField, old: RuleCondition? = nil) -> RuleCondition {
        let ops = operators(for: f)
        var c = RuleCondition(field: f, op: ops[0], value: defaultValue(f))
        guard let old else { return c }
        if ops.contains(old.op) {
            c.op = old.op
        }
        if valueKind(of: f) == .textField, valueKind(of: old.field) == .textField {
            c.value = old.value
        }
        return c
    }

    // MARK: The form

    /// rules.Form: a rule as the editor holds it. `accountId` nil is every
    /// mail account.
    public struct Form: Sendable, Equatable {
        /// The edited rule's id, "" for a new one.
        public var id: String
        public var input: RuleInput

        public init(id: String = "", input: RuleInput) {
            self.id = id
            self.input = input
        }

        public var name: String {
            get { input.name }
            set { input.name = newValue }
        }

        public var enabled: Bool {
            get { input.enabled }
            set { input.enabled = newValue }
        }

        public var accountId: AccountID? {
            get { input.accountId }
            set { input.accountId = newValue?.rawValue.isEmpty == false ? newValue : nil }
        }

        public var match: RuleMatch {
            get { input.match }
            set { input.match = newValue }
        }

        public var conditions: [RuleCondition] {
            get { input.conditions }
            set { input.conditions = newValue }
        }

        public var actions: [RuleAction] {
            get { input.actions }
            set { input.actions = newValue }
        }

        public var stop: Bool {
            get { input.stop }
            set { input.stop = newValue }
        }

        /// rules.Form.Input: what rules.create and rules.update send, the
        /// strings trimmed, the fields of other action kinds cleared.
        public var trimmed: RuleInput {
            var in_ = input
            in_.name = Rules.trimSpace(input.name)
            in_.conditions = input.conditions.map { c in
                var c = c
                if Rules.valueKind(of: c.field) == .textField {
                    c.value = Rules.trimSpace(c.value)
                }
                return c
            }
            in_.actions = input.actions.map { a in
                var out = RuleAction(kind: a.kind)
                switch a.kind {
                case .move:
                    out.folderId = a.folderId
                case .forward:
                    out.to = Rules.trimSpace(a.to)
                case .autoReply:
                    out.subject = Rules.trimSpace(a.subject)
                    out.text = Rules.trimRight(a.text)
                default:
                    break
                }
                return out
            }
            return in_
        }
    }

    /// rules.NewForm: the editor of a new rule: enabled, every condition,
    /// one empty From condition, no action yet; for the account given (the
    /// one the main window shows), nil for every account.
    public static func newForm(_ accountId: AccountID?) -> Form {
        Form(input: RuleInput(
            enabled: true, accountId: accountId?.rawValue.isEmpty == false ? accountId : nil, match: .all,
            conditions: [newCondition(.from)]))
    }

    /// rules.FromRule: the editor of a stored rule.
    public static func fromRule(_ r: Rule) -> Form {
        var f = Form(id: r.id, input: r.input)
        if f.input.match.rawValue.isEmpty {
            f.input.match = .all
        }
        return f
    }

    /// rules.IsOutgoing: a rule input has an outgoing action.
    public static func isOutgoing(_ input: RuleInput) -> Bool {
        input.actions.contains { $0.kind.isOutgoing }
    }

    /// rules.AvailableActions: the kinds an Add Action menu offers: each
    /// kind once, move only with an account, and none of move, junk and
    /// trash once the rule has one of them.
    public static func availableActions(_ f: Form) -> [RuleActionKind] {
        let has = Set(f.actions.map(\.kind))
        let place = has.contains(.move) || has.contains(.junk) || has.contains(.trash)
        return actionKinds.filter { k in
            if has.contains(k) { return false }
            if isPlacing(k), place { return false }
            if k == .move, f.accountId == nil { return false }
            return true
        }
    }

    static func isPlacing(_ k: RuleActionKind) -> Bool {
        k == .move || k == .junk || k == .trash
    }

    // MARK: Validation

    /// rules.ProblemKind: what is wrong.
    public enum ProblemKind: Sendable, Equatable {
        case nameMissing, tooLong, noCondition, tooManyConditions, valueMissing, badCharacters
        case noAction, tooManyActions, actionTwice, onePlace, moveNeedsAccount, folderMissing
        case addressInvalid, replyMissing, badOperator
    }

    /// rules.Problem: what `validate` found: the field ("name",
    /// "conditions", "actions") and, for the last two, the row; `kind`
    /// picks the text (`problemText`).
    public struct Problem: Sendable, Equatable {
        public var field: String
        public var index: Int
        public var kind: ProblemKind

        public init(_ field: String, _ index: Int, _ kind: ProblemKind) {
            self.field = field
            self.index = index
            self.kind = kind
        }
    }

    /// rules.Validate: every problem of the form, in the order of the
    /// editor; none means Save is enabled. The daemon checks again
    /// (`RuleInvalidData`).
    public static func validate(_ f: Form) -> [Problem] {
        let input = f.trimmed
        var out: [Problem] = []
        func add(_ field: String, _ i: Int, _ k: ProblemKind) {
            out.append(Problem(field, i, k))
        }
        if input.name.isEmpty {
            add("name", 0, .nameMissing)
        } else if input.name.utf8.count > API.Limits.maxRuleNameBytes {
            add("name", 0, .tooLong)
        } else if !clean(input.name) {
            add("name", 0, .badCharacters)
        }
        if input.conditions.isEmpty {
            add("conditions", 0, .noCondition)
        } else if input.conditions.count > API.Limits.maxRuleConditions {
            add("conditions", 0, .tooManyConditions)
        }
        for (i, c) in input.conditions.enumerated() {
            guard operators(for: c.field).contains(c.op) else {
                add("conditions", i, .badOperator)
                continue
            }
            switch valueKind(of: c.field) {
            case .bulk:
                if !bulkValues.contains(c.value) {
                    add("conditions", i, .valueMissing)
                }
            case .bool:
                if c.value != valueTrue && c.value != valueFalse {
                    add("conditions", i, .valueMissing)
                }
            case .textField:
                if c.value.isEmpty {
                    add("conditions", i, .valueMissing)
                } else if c.value.utf8.count > API.Limits.maxRuleValueBytes {
                    add("conditions", i, .tooLong)
                } else if !clean(c.value) {
                    add("conditions", i, .badCharacters)
                }
            }
        }
        if input.actions.isEmpty {
            add("actions", 0, .noAction)
        } else if input.actions.count > API.Limits.maxRuleActions {
            add("actions", 0, .tooManyActions)
        }
        var seen: Set<RuleActionKind> = []
        var placed = false
        for (i, a) in input.actions.enumerated() {
            if seen.contains(a.kind) {
                add("actions", i, .actionTwice)
                continue
            }
            seen.insert(a.kind)
            if isPlacing(a.kind) {
                if placed {
                    add("actions", i, .onePlace)
                    continue
                }
                placed = true
            }
            switch a.kind {
            case .move:
                if input.accountId == nil {
                    add("actions", i, .moveNeedsAccount)
                } else if a.folderId == nil {
                    add("actions", i, .folderMissing)
                }
            case .forward:
                if !validAddress(a.to) {
                    add("actions", i, .addressInvalid)
                }
            case .autoReply:
                if trimSpace(a.text).isEmpty {
                    add("actions", i, .replyMissing)
                } else if a.text.utf8.count > API.Limits.maxRuleReplyBytes || a.subject.utf8.count > API.Limits.maxRuleValueBytes {
                    add("actions", i, .tooLong)
                } else if !clean(a.subject) || !cleanText(a.text) {
                    add("actions", i, .badCharacters)
                }
            default:
                break
            }
        }
        return out
    }

    /// rules.Clean: a one-line value has no control, format (bidi,
    /// invisible) or line-breaking character, the daemon's rule for every
    /// string of a rule.
    public static func clean(_ s: String) -> Bool {
        for r in s.unicodeScalars {
            switch r.properties.generalCategory {
            case .control, .format:
                return false
            default:
                break
            }
            if r == "\u{2028}" || r == "\u{2029}" {
                return false
            }
        }
        return true
    }

    /// rules.cleanText: `clean` that lets line breaks and tabs through (the
    /// text of an automatic reply): "\r\n", "\n" and "\t" go before the
    /// check, a lone "\r" stays and fails it.
    static func cleanText(_ s: String) -> Bool {
        var kept = String.UnicodeScalarView()
        let scalars = Array(s.unicodeScalars)
        var i = 0
        while i < scalars.count {
            let r = scalars[i]
            if r == "\r", i + 1 < scalars.count, scalars[i + 1] == "\n" {
                i += 2
                continue
            }
            if r == "\n" || r == "\t" {
                i += 1
                continue
            }
            kept.append(r)
            i += 1
        }
        return clean(String(kept))
    }

    /// rules.ValidAddress: a bare address the daemon accepts as forward's
    /// to: local@domain, no spaces, angle brackets, commas or control
    /// characters, a dot in the domain.
    public static func validAddress(_ raw: String) -> Bool {
        let s = trimSpace(raw)
        guard !s.isEmpty, s.utf8.count <= 254, clean(s) else { return false }
        let banned = Set(" <>,;:\"()[]\\".unicodeScalars)
        if s.unicodeScalars.contains(where: { banned.contains($0) }) {
            return false
        }
        let parts = s.split(separator: "@", omittingEmptySubsequences: false)
        guard parts.count == 2, !parts[0].isEmpty, !parts[1].isEmpty else { return false }
        let domain = parts[1]
        return domain.contains(".") && !domain.hasPrefix(".") && !domain.hasSuffix(".") && !domain.contains("..")
    }

    // MARK: Prefill

    /// rules.FromMessage: the editor of Create Rule from Message…: named
    /// after the sender, From is the sender's address and, for a mailing
    /// list or a newsletter with a List-Id, List ID is that id instead
    /// (one condition, which is what such mail has in common); one Move To
    /// action without a folder yet, so the user picks where it goes; for
    /// the message's account.
    public static func fromMessage(_ m: MessageSummary, accountId: AccountID?) -> Form {
        var f = newForm(accountId)
        var sender = ""
        if let first = m.from.first {
            sender = trimSpace(first.address)
            let name = trimSpace(first.name ?? "")
            if !name.isEmpty, clean(name) {
                f.name = prefillName(name)
            } else if !sender.isEmpty {
                f.name = prefillName(sender)
            }
        }
        if let list = m.bulk?.listId, !list.isEmpty, clean(list) {
            f.conditions = [RuleCondition(field: .listId, op: .is, value: list)]
        } else if !sender.isEmpty, clean(sender) {
            f.conditions = [RuleCondition(field: .from, op: .is, value: sender)]
        }
        if f.name.utf8.count > API.Limits.maxRuleNameBytes {
            f.name = truncate(f.name, API.Limits.maxRuleNameBytes)
        }
        if f.accountId != nil {
            f.actions = [RuleAction(kind: .move)]
        }
        return f
    }

    /// rules.truncate: `s` cut to at most `n` UTF-8 bytes on a scalar
    /// boundary.
    static func truncate(_ s: String, _ n: Int) -> String {
        guard s.utf8.count > n else { return s }
        var out = String.UnicodeScalarView()
        var used = 0
        for r in s.unicodeScalars {
            let w = String(r).utf8.count
            if used + w > n { break }
            out.append(r)
            used += w
        }
        return String(out)
    }

    // MARK: Confirmation, order, offers

    /// rules.NeedsConfirm: saving must ask `outgoingQuestion` and send
    /// confirmOutgoing (docs/api.md §4.21): the rule is enabled with an
    /// outgoing action and either `old` (the stored rule, nil for a new
    /// one) was not, or an outgoing action's to, subject or text changed.
    public static func needsConfirm(_ old: Rule?, _ input: RuleInput) -> Bool {
        guard input.enabled, isOutgoing(input) else { return false }
        guard let old, old.enabled, isOutgoing(old.input) else { return true }
        return outgoingKey(old.input) != outgoingKey(input)
    }

    static func outgoingKey(_ input: RuleInput) -> String {
        var b = ""
        for k in [RuleActionKind.forward, .autoReply] {
            for a in input.actions where a.kind == k {
                b += k.rawValue + "\u{0}" + a.to + "\u{0}" + a.subject + "\u{0}" + a.text + "\u{0}"
            }
        }
        return b
    }

    /// rules.Toggled: the input of a row's switch, the rule with enabled
    /// flipped.
    public static func toggled(_ r: Rule) -> RuleInput {
        var input = fromRule(r).input
        input.enabled = !r.enabled
        return input
    }

    /// rules.Moved: the order of rules.reorder after the rule at `from` was
    /// dropped at `to` (an index of the list without it); an index out of
    /// range leaves the order as it is.
    public static func moved(_ ids: [String], from: Int, to: Int) -> [String] {
        var out = ids
        guard from >= 0, from < out.count, to >= 0, to < out.count, from != to else { return out }
        let id = out.remove(at: from)
        out.insert(id, at: to)
        return out
    }

    /// rules.AppliesToAccount: an enabled rule runs on the account's mail
    /// (its own or one for every account); Apply Rules… of the account's
    /// folders is offered then.
    public static func appliesToAccount(_ rules: [Rule], _ accountId: AccountID) -> Bool {
        rules.contains { $0.enabled && ($0.accountId == nil || $0.accountId == accountId) }
    }

    /// rules.CanApplyIn: Apply Rules… is offered for a folder of a mail
    /// account that holds messages, not the outbox or the drafts.
    public static func canApplyIn(_ f: Folder) -> Bool {
        f.selectable && isPlain(f) && f.role != .outbox && f.role != .drafts
    }

    /// rules.CanCreateFrom: Create Rule from Message… is offered for a
    /// message in a folder of the role: not in the outbox, the drafts or
    /// the sent mail.
    public static func canCreateFrom(_ role: FolderRole) -> Bool {
        role != .outbox && role != .drafts && role != .sent
    }

    /// rules.MoveFolders: the folders a Move To action offers, from the
    /// account's folders in the sidebar's order: the selectable ones other
    /// than the outbox, the drafts, an \All folder and an issue tracker's
    /// views.
    public static func moveFolders(_ folders: [Folder]) -> [Folder] {
        folders.filter { f in
            f.selectable && isPlain(f) && f.role != .outbox && f.role != .drafts && f.role != .all
        }
    }

    /// The folder is not an issue tracker's view (Go's `Virtual == ""`).
    static func isPlain(_ f: Folder) -> Bool {
        (f.virtual?.rawValue ?? "").isEmpty
    }

    /// Whether an account's mail is what rules run on (docs/api.md §4.21:
    /// imap and graph, never an issue tracker, CalDAV or a CRM).
    public static func isMailAccount(_ a: Account) -> Bool {
        let k = a.config.protocolKind
        return k == .imap || k == .graph
    }

    // MARK: Strings

    /// strings.TrimSpace: White_Space at both ends goes.
    static func trimSpace(_ s: String) -> String {
        let scalars = s.unicodeScalars
        guard let first = scalars.firstIndex(where: { !$0.properties.isWhitespace }),
              let last = scalars.lastIndex(where: { !$0.properties.isWhitespace }) else { return "" }
        return String(scalars[first...last])
    }

    /// strings.TrimRight(s, " \t\r\n").
    static func trimRight(_ s: String) -> String {
        var scalars = Array(s.unicodeScalars)
        while let last = scalars.last, last == " " || last == "\t" || last == "\r" || last == "\n" {
            scalars.removeLast()
        }
        var out = String.UnicodeScalarView()
        out.append(contentsOf: scalars)
        return String(out)
    }
}
