// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// ui/internal/rules text.go: every text of the mail rules, with the GTK
// msgids as keys. Names and values of a rule are shown as plain text only.

extension Rules {
    /// rules.DateText: writes a time for a status line in the user's
    /// locale.
    public typealias DateText = (Date) -> String

    /// rules.Names: the names a summary shows for ids: an account's ("" when
    /// it is gone) and a folder's ("" when it is gone).
    public struct Names {
        public var account: ((AccountID) -> String)?
        public var folder: ((AccountID?, FolderID?) -> String)?

        public init(account: ((AccountID) -> String)? = nil, folder: ((AccountID?, FolderID?) -> String)? = nil) {
            self.account = account
            self.folder = folder
        }
    }

    // MARK: The Rules page of Settings

    /// rules.PageTitle: the Settings page (after Integrations) and its tab.
    public static var pageTitle: String {
        // TRANSLATORS: the Preferences page of the mail rules (filters that move, mark, forward or answer new mail).
        L10n.T("Rules")
    }

    /// rules.PageDescription: the text over the list of rules.
    public static var pageDescription: String {
        L10n.T("Rules act on new mail in the inbox as it arrives, in the order of this list. Malachi Mail runs them on this computer, only while it is running.")
    }

    /// rules.EmptyTitle and rules.EmptyDescription: the page without rules.
    public static var emptyTitle: String { L10n.T("No Rules") }

    public static var emptyDescription: String {
        L10n.T("Move, mark or forward new messages automatically. You can also create a rule from a message in its context menu.")
    }

    /// rules.AddRule: the page's button that opens the editor of a new
    /// rule.
    public static var addRule: String { L10n.T("Add Rule…") }

    /// rules.EditRule: a row's button (and its tooltip).
    public static var editRule: String { L10n.T("Edit Rule…") }

    /// rules.RemoveRule: a row's button (and its tooltip) that asks
    /// `deleteQuestion`.
    public static var removeRule: String { L10n.T("Remove Rule") }

    /// rules.EnabledTooltip: the tooltip of a row's switch.
    public static var enabledTooltip: String { L10n.T("Turn the rule on or off") }

    /// rules.Row: a rule of the page's list.
    public struct Row: Sendable, Equatable {
        public var id: String
        public var enabled: Bool
        /// The rule's name.
        public var title: String
        /// One line each (`conditionsLine`, `actionsLine`).
        public var conditions: String
        public var actions: String
        /// The account the rule is for, or All Mail Accounts.
        public var scope: String
        /// How often the rule acted (`statusLine`); `problem` why the
        /// daemon turned it off ("" when it did not), shown as a warning.
        public var status: String
        public var problem: String
        /// The rule sends mail; the row shows a sending symbol.
        public var outgoing: Bool
    }

    /// rules.Rows: the page's rows in the rules' order.
    public static func rows(_ rules: [Rule], names: Names, date: DateText?) -> [Row] {
        rules.map { r in
            Row(id: r.id, enabled: r.enabled, title: r.name,
                conditions: conditionsLine(r.input), actions: actionsLine(r.input, account: r.accountId, names: names),
                scope: scopeText(r.accountId, names: names), status: statusLine(r, date: date),
                problem: problemOf(r.problem), outgoing: r.outgoing)
        }
    }

    /// rules.ScopeText: the account a rule is for.
    public static func scopeText(_ id: AccountID?, names: Names) -> String {
        guard let id else { return allAccounts }
        if let n = names.account?(id), !n.isEmpty {
            return n
        }
        return L10n.T("Removed account")
    }

    /// rules.AllAccounts: the account choice of a rule for every mail
    /// account.
    public static var allAccounts: String { L10n.T("All Mail Accounts") }

    /// rules.StatusLine: how often the rule acted and when last, and how
    /// many outgoing messages its protections held back.
    public static func statusLine(_ r: Rule, date: DateText?) -> String {
        var line: String
        if r.applied == 0 {
            line = L10n.T("Not used yet")
        } else if let last = r.lastAppliedAt, let date {
            line = L10n.N("Used on %d message, last %s", "Used on %d messages, last %s", r.applied, r.applied, date(last))
        } else {
            line = L10n.N("Used on %d message", "Used on %d messages", r.applied)
        }
        if r.skipped > 0 {
            // TRANSLATORS: after the status line of a rule: messages it would have forwarded or answered and did not (bulk mail, an automatic message, the user's own address, a limit).
            line = L10n.N("%s · %d outgoing message held back", "%s · %d outgoing messages held back", r.skipped, line, r.skipped)
        }
        return line
    }

    /// rules.ProblemOf: why the daemon turned a rule off; "" for none.
    public static func problemOf(_ p: RuleProblem?) -> String {
        guard let p else { return "" }
        switch p {
        case .folderGone:
            return L10n.T("The folder this rule moves messages to no longer exists, so the rule was turned off.")
        case .sendFailed:
            return L10n.T("This rule could not send a message, so it was turned off.")
        default:
            return ""
        }
    }

    /// rules.Question: a confirmation: its heading, its text and the label
    /// of the button that goes ahead (the other one is Cancel).
    public struct Question: Sendable, Equatable {
        public var heading: String
        public var body: String
        public var confirm: String
        /// The button that goes ahead is shown as destructive.
        public var destructive = false
    }

    /// rules.DeleteQuestion: asks before Remove deletes a rule.
    public static func deleteQuestion(_ name: String) -> Question {
        Question(
            heading: L10n.T("Remove the Rule “%s”?", name),
            body: L10n.T("Messages it already moved or marked stay as they are."),
            confirm: L10n.T("Remove"),
            destructive: true)
    }

    /// rules.OutgoingQuestion: asks before a rule that sends mail is saved
    /// enabled or turned on (`needsConfirm`). It names where forward sends
    /// and says that replies go out on their own.
    public static func outgoingQuestion(_ input: RuleInput) -> Question {
        var to = ""
        var reply = false
        for a in input.actions {
            switch a.kind {
            case .forward: to = a.to
            case .autoReply: reply = true
            default: break
            }
        }
        let body: String
        if !to.isEmpty, reply {
            body = L10n.T("This rule forwards every matching message to %s and answers its sender, without asking you. Bulk mail, automatic messages and your own messages are never forwarded or answered.", to)
        } else if !to.isEmpty {
            body = L10n.T("This rule forwards every matching message to %s without asking you. Bulk mail, automatic messages and your own messages are never forwarded.", to)
        } else {
            body = L10n.T("This rule answers the sender of every matching message without asking you, at most once in four days per sender. Bulk mail, automatic messages and your own messages are never answered.")
        }
        return Question(heading: L10n.T("Send Mail Automatically?"), body: body, confirm: L10n.T("Turn On"))
    }

    // MARK: The editor

    /// rules.EditorTitle: New Rule or Edit Rule.
    public static func editorTitle(_ f: Form) -> String {
        // TRANSLATORS: the title of the editor of a stored mail rule.
        f.id.isEmpty ? L10n.T("New Rule") : L10n.T("Edit Rule")
    }

    /// rules.NameLabel.
    public static var nameLabel: String { L10n.T("Name") }

    /// rules.AccountLabel: the label of the account choice (the accounts,
    /// then `allAccounts`).
    public static var accountLabel: String {
        // TRANSLATORS: the label of the account choice of the rule editor.
        L10n.T("Apply to")
    }

    /// rules.MatchLabel: the label of the choice of how conditions combine.
    public static var matchLabel: String { L10n.T("When a new message arrives") }

    /// rules.MatchText: a choice of how conditions combine.
    public static func matchText(_ m: RuleMatch) -> String {
        m == .any ? L10n.T("and any of these conditions is met") : L10n.T("and all of these conditions are met")
    }

    /// rules.ActionsHeading: the heading over the actions.
    public static var actionsHeading: String { L10n.T("Do the following") }

    /// rules.AddCondition and rules.AddAction: the buttons under the rows
    /// (Add Action is a menu of `availableActions`).
    public static var addCondition: String { L10n.T("Add Condition") }

    public static var addAction: String { L10n.T("Add Action") }

    /// rules.RemoveRow: the tooltip of a row's remove button.
    public static var removeRow: String { L10n.T("Remove") }

    /// rules.EnabledLabel: the editor's switch.
    public static var enabledLabel: String { L10n.T("Rule is on") }

    /// rules.StopLabel and rules.StopHint: the editor's check box of
    /// `RuleInput.stop` and the line under it.
    public static var stopLabel: String { L10n.T("Stop processing more rules") }

    public static var stopHint: String { L10n.T("Rules below this one do not run on a message this rule matched.") }

    /// rules.FieldText: a condition field's choice.
    public static func fieldText(_ f: RuleField) -> String {
        switch f {
        case .from: return L10n.C("rule field", "From")
        case .to: return L10n.C("rule field", "To")
        case .cc: return L10n.C("rule field", "Cc")
        case .toOrCc: return L10n.C("rule field", "To or Cc")
        case .subject: return L10n.C("rule field", "Subject")
        case .body: return L10n.C("rule field", "Message text")
        case .listId:
            // TRANSLATORS: the List-Id of mailing-list mail.
            return L10n.C("rule field", "Mailing list ID")
        case .bulk: return L10n.C("rule field", "Bulk mail")
        case .hasAttachments: return L10n.C("rule field", "Attachments")
        default: return f.rawValue
        }
    }

    /// rules.OperatorText: an operator's choice; it reads after
    /// `fieldText` ("From contains …").
    public static func operatorText(_ f: RuleField, _ op: RuleOperator) -> String {
        if valueKind(of: f) == .bool {
            // TRANSLATORS: Attachments [are] yes/no in the rule editor.
            return L10n.C("rule operator", "are")
        }
        switch op {
        case .contains: return L10n.C("rule operator", "contains")
        case .notContains: return L10n.C("rule operator", "does not contain")
        case .is: return L10n.C("rule operator", "is")
        case .isNot: return L10n.C("rule operator", "is not")
        case .startsWith: return L10n.C("rule operator", "begins with")
        case .endsWith: return L10n.C("rule operator", "ends with")
        case .domainIs: return L10n.C("rule operator", "is from domain")
        default: return op.rawValue
        }
    }

    /// rules.ValueText: a value's choice of a bulk or a hasAttachments
    /// condition; a text value is itself.
    public static func valueText(_ f: RuleField, _ value: String) -> String {
        switch valueKind(of: f) {
        case .bulk:
            switch value {
            case RuleBulk.any: return L10n.C("bulk kind", "any kind")
            case BulkKind.newsletter.rawValue: return L10n.C("bulk kind", "newsletter")
            case BulkKind.list.rawValue: return L10n.C("bulk kind", "mailing list")
            case BulkKind.automated.rawValue: return L10n.C("bulk kind", "automated message")
            default: return value
            }
        case .bool:
            return value == valueFalse ? L10n.C("attachments", "none") : L10n.C("attachments", "present")
        case .textField:
            return value
        }
    }

    /// rules.ValuePlaceholder: the placeholder of a condition's text value.
    public static func valuePlaceholder(_ f: RuleField, _ op: RuleOperator) -> String {
        if op == .domainIs {
            return L10n.T("example.com")
        }
        if isAddressField(f) {
            return L10n.T("Name or address")
        }
        if f == .listId {
            return L10n.T("list.example.com")
        }
        return L10n.T("Text")
    }

    /// rules.ActionText: an action kind's choice (and an item of Add
    /// Action).
    public static func actionText(_ k: RuleActionKind) -> String {
        switch k {
        case .move: return L10n.C("rule action", "Move to folder")
        case .markRead: return L10n.C("rule action", "Mark as read")
        case .flag: return L10n.C("rule action", "Star")
        case .trash: return L10n.C("rule action", "Move to Trash")
        case .junk: return L10n.C("rule action", "Move to Junk")
        case .forward: return L10n.C("rule action", "Forward to")
        case .autoReply: return L10n.C("rule action", "Reply automatically")
        default: return k.rawValue
        }
    }

    /// rules.FolderPlaceholder: the Move To action's folder choice before a
    /// folder is chosen.
    public static var folderPlaceholder: String { L10n.T("Choose a Folder") }

    /// rules.ForwardPlaceholder: the placeholder of forward's address.
    public static var forwardPlaceholder: String { L10n.T("name@example.com") }

    /// rules.ReplySubjectPlaceholder and rules.ReplyTextPlaceholder.
    public static var replySubjectPlaceholder: String { L10n.T("Subject (Re: and the original subject if empty)") }

    public static var replyTextPlaceholder: String { L10n.T("Reply text") }

    /// rules.OutgoingHint: the line under an outgoing action in the editor.
    public static func outgoingHint(_ k: RuleActionKind) -> String {
        if k == .autoReply {
            return L10n.T("Each sender gets at most one automatic reply in four days. Bulk mail, automatic messages and your own messages are never answered.")
        }
        return L10n.T("The message is sent whole as an attachment. Bulk mail, automatic messages and your own messages are never forwarded.")
    }

    /// rules.ProblemText: the text of a problem `validate` found, shown
    /// under its row.
    public static func problemText(_ p: Problem) -> String {
        switch p.kind {
        case .nameMissing: return L10n.T("Enter a name for the rule.")
        case .tooLong: return L10n.T("This is too long.")
        case .badCharacters: return L10n.T("This contains characters that cannot be used.")
        case .noCondition: return L10n.T("Add at least one condition.")
        case .tooManyConditions:
            return L10n.N("A rule can have at most %d condition.", "A rule can have at most %d conditions.", API.Limits.maxRuleConditions)
        case .valueMissing: return L10n.T("Enter a value.")
        case .badOperator: return L10n.T("Choose how to compare.")
        case .noAction: return L10n.T("Add at least one action.")
        case .tooManyActions:
            return L10n.N("A rule can have at most %d action.", "A rule can have at most %d actions.", API.Limits.maxRuleActions)
        case .actionTwice: return L10n.T("This action is already in the rule.")
        case .onePlace: return L10n.T("A rule can move a message to only one place.")
        case .moveNeedsAccount: return L10n.T("Choose an account to move messages to one of its folders.")
        case .folderMissing: return L10n.T("Choose a folder.")
        case .addressInvalid: return L10n.T("Enter one valid email address.")
        case .replyMissing: return L10n.T("Enter the text of the reply.")
        }
    }

    /// rules.InvalidText: the daemon's invalidArgument with
    /// `RuleInvalidData`, for what the editor did not catch.
    public static func invalidText(_ d: RuleInvalidData) -> String {
        switch d.field {
        case "name": return L10n.T("Enter a name for the rule.")
        case "accountId": return L10n.T("The account of this rule no longer exists.")
        case "confirmOutgoing": return L10n.T("Rules that send mail need your confirmation.")
        case "conditions": return L10n.T("A condition of this rule cannot be used.")
        case "actions": return L10n.T("An action of this rule cannot be used.")
        default: return L10n.T("This rule cannot be saved.")
        }
    }

    /// rules.NotFoundText: ruleNotFound (another window or client removed
    /// it).
    public static var notFoundText: String { L10n.T("This rule no longer exists.") }

    /// rules.SaveFailedText: a failed rules.create or rules.update for any
    /// other error.
    public static var saveFailedText: String { L10n.T("The rule could not be saved.") }

    /// rules.TestButton: the editor's button that calls rules.test.
    public static var testButton: String {
        // TRANSLATORS: a button of the rule editor: shows which stored messages of a folder the rule would match.
        L10n.T("Try It")
    }

    /// rules.TestFolderLabel: the label of the folder Try It looks in.
    public static var testFolderLabel: String { L10n.T("Try in") }

    /// rules.TestResultText: the result of rules.test.
    public static func testResultText(_ count: Int) -> String {
        if count == 0 {
            return L10n.T("No message in this folder matches.")
        }
        return L10n.N("%d message in this folder matches.", "%d messages in this folder match.", count)
    }

    // MARK: Summaries

    /// rules.ConditionText: one condition as a phrase: field, operator,
    /// value.
    public static func conditionText(_ c: RuleCondition) -> String {
        let value = valueText(c.field, c.value)
        let field = fieldText(c.field)
        let op = operatorText(c.field, c.op)
        if valueKind(of: c.field) == .textField {
            // TRANSLATORS: a condition of a rule in its summary: field, comparison, quoted value ("From contains “acme”").
            return L10n.format(L10n.C("rule condition", "%s %s “%s”"), [field, op, value])
        }
        // TRANSLATORS: a condition with a chosen value ("Bulk mail is newsletter").
        return L10n.format(L10n.C("rule condition", "%s %s %s"), [field, op, value])
    }

    /// rules.ConditionsLine: a rule's conditions in one line, joined by the
    /// rule's match.
    public static func conditionsLine(_ input: RuleInput) -> String {
        let parts = input.conditions.map(conditionText)
        // TRANSLATORS: joins the conditions of a rule that all have to hold (with spaces around it).
        var sep = L10n.C("all conditions", " and ")
        if input.match == .any {
            // TRANSLATORS: joins the conditions of a rule of which one has to hold (with spaces around it).
            sep = L10n.C("any condition", " or ")
        }
        return parts.joined(separator: sep)
    }

    /// rules.ActionPhrase: one action as a phrase.
    public static func actionPhrase(_ a: RuleAction, account: AccountID?, names: Names) -> String {
        switch a.kind {
        case .move:
            let name = names.folder?(account, a.folderId) ?? ""
            if name.isEmpty {
                return L10n.T("move to a missing folder")
            }
            return L10n.T("move to “%s”", name)
        case .markRead: return L10n.T("mark as read")
        case .flag: return L10n.T("star")
        case .trash: return L10n.T("move to Trash")
        case .junk: return L10n.T("move to Junk")
        case .forward: return L10n.T("forward to %s", a.to)
        case .autoReply: return L10n.T("reply automatically")
        default: return a.kind.rawValue
        }
    }

    /// rules.ActionsLine: a rule's actions in one line in the order the
    /// daemon runs them, with stop at the end.
    public static func actionsLine(_ input: RuleInput, account: AccountID?, names: Names) -> String {
        var parts: [String] = []
        for k in actionKinds {
            for a in input.actions where a.kind == k {
                parts.append(actionPhrase(a, account: account, names: names))
            }
        }
        if input.stop {
            parts.append(L10n.T("stop processing more rules"))
        }
        // TRANSLATORS: joins the actions of a rule in its summary.
        return parts.joined(separator: L10n.C("rule actions", ", "))
    }

    /// rules.PrefillName: the name of a rule made from a message, after its
    /// sender.
    public static func prefillName(_ sender: String) -> String {
        // TRANSLATORS: the name of a rule made from a message; %s is the sender's name or address.
        L10n.T("Messages from %s", sender)
    }

    // MARK: The menus and Apply Rules

    /// rules.CreateFromMessageLabel: the item of a message's context menu.
    public static var createFromMessageLabel: String { L10n.T("Create Rule from Message…") }

    /// rules.ApplyLabel: the item of a folder's context menu.
    public static var applyLabel: String { L10n.T("Apply Rules…") }

    /// rules.ApplyQuestion: asks before Apply Rules… runs the account's
    /// rules on the folder.
    public static func applyQuestion(_ folder: String) -> Question {
        Question(
            heading: L10n.T("Apply Rules to “%s”?", folder),
            body: L10n.T("The rules that are on run on the messages of this folder kept on this computer. Rules do not forward or reply to these messages."),
            confirm: L10n.T("Apply"))
    }

    /// rules.ApplyingText: the progress line while rules.apply runs.
    public static var applyingText: String { L10n.T("Applying rules") }

    /// rules.ApplyResultText: the toast after rules.apply.
    public static func applyResultText(_ r: RulesApplyResult) -> String {
        if r.changed == 0 {
            return L10n.T("The rules changed no message.")
        }
        return L10n.N("The rules changed %d message.", "The rules changed %d messages.", r.changed)
    }

    /// rules.ApplyFailedText: a failed rules.apply.
    public static var applyFailedText: String { L10n.T("The rules could not be applied.") }
}
