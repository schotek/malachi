// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package rules

import (
	"fmt"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Translator translates a msgid of the malachi domain: T a plain one, N a
// plural form for n, C one with a context (board.Translator; GTK passes
// i18n.Tr, macOS and Windows L10n with key = msgid).
type Translator interface {
	T(msgid string) string
	N(msgid, plural string, n int) string
	C(context, msgid string) string
}

// DateText writes a time for a status line in the user's locale (the
// caller's date formatter; "date" in the tests).
type DateText func(t time.Time) string

// Names are the names a summary shows for ids: an account's ("" when it
// is gone) and a folder's ("" when it is gone).
type Names struct {
	Account func(id api.AccountID) string
	Folder  func(account api.AccountID, id api.FolderID) string
}

// ---------------------------------------------------------------------------
// The Rules page of Preferences
// ---------------------------------------------------------------------------

// PageTitle is the Preferences page (after Integrations) and its tab.
func PageTitle(tr Translator) string {
	// TRANSLATORS: the Preferences page of the mail rules (filters that
	// move, mark, forward or answer new mail).
	return tr.T("Rules")
}

// PageDescription is the text over the list of rules.
func PageDescription(tr Translator) string {
	return tr.T("Rules act on new mail in the inbox as it arrives, in the order of this list. Malachi Mail runs them on this computer, only while it is running.")
}

// EmptyTitle and EmptyDescription are the page without rules.
func EmptyTitle(tr Translator) string {
	return tr.T("No Rules")
}

// EmptyDescription explains rules on the empty page.
func EmptyDescription(tr Translator) string {
	return tr.T("Move, mark or forward new messages automatically. You can also create a rule from a message in its context menu.")
}

// AddRule is the page's button that opens the editor of a new rule.
func AddRule(tr Translator) string {
	return tr.T("Add Rule…")
}

// EditRule is a row's button (and its tooltip) that opens the editor.
func EditRule(tr Translator) string {
	return tr.T("Edit Rule…")
}

// RemoveRule is a row's button (and its tooltip) that asks DeleteQuestion.
func RemoveRule(tr Translator) string {
	return tr.T("Remove Rule")
}

// EnabledTooltip is the tooltip of a row's switch.
func EnabledTooltip(tr Translator) string {
	return tr.T("Turn the rule on or off")
}

// Row is a rule of the page's list.
type Row struct {
	ID      string
	Enabled bool
	// Title is the rule's name.
	Title string
	// Conditions and Actions are one line each (ConditionsLine,
	// ActionsLine).
	Conditions, Actions string
	// Scope is the account the rule is for, or All Mail Accounts.
	Scope string
	// Status is how often the rule acted (StatusLine); Problem why the
	// daemon turned it off ("" when it did not), shown as a warning.
	Status, Problem string
	// Outgoing: the rule sends mail; the row shows a sending icon.
	Outgoing bool
}

// Rows are the page's rows in the rules' order.
func Rows(rules []api.Rule, names Names, date DateText, tr Translator) []Row {
	out := make([]Row, 0, len(rules))
	for _, r := range rules {
		out = append(out, Row{
			ID:         r.ID,
			Enabled:    r.Enabled,
			Title:      r.Name,
			Conditions: ConditionsLine(r.RuleInput, tr),
			Actions:    ActionsLine(r.RuleInput, r.AccountID, names, tr),
			Scope:      ScopeText(r.AccountID, names, tr),
			Status:     StatusLine(r, date, tr),
			Problem:    ProblemOf(r.Problem, tr),
			Outgoing:   r.Outgoing,
		})
	}
	return out
}

// ScopeText is the account a rule is for.
func ScopeText(id api.AccountID, names Names, tr Translator) string {
	if id == "" {
		return AllAccounts(tr)
	}
	if names.Account != nil {
		if n := names.Account(id); n != "" {
			return n
		}
	}
	return tr.T("Removed account")
}

// AllAccounts is the account choice of a rule for every mail account.
func AllAccounts(tr Translator) string {
	return tr.T("All Mail Accounts")
}

// StatusLine is how often the rule acted and when last, and how many
// outgoing messages its protections held back.
func StatusLine(r api.Rule, date DateText, tr Translator) string {
	var line string
	switch {
	case r.Applied == 0:
		line = tr.T("Not used yet")
	case r.LastAppliedAt != nil && date != nil:
		line = fmt.Sprintf(tr.N("Used on %d message, last %s", "Used on %d messages, last %s", r.Applied), r.Applied, date(*r.LastAppliedAt))
	default:
		line = fmt.Sprintf(tr.N("Used on %d message", "Used on %d messages", r.Applied), r.Applied)
	}
	if r.Skipped > 0 {
		// TRANSLATORS: after the status line of a rule: messages it would
		// have forwarded or answered and did not (bulk mail, an automatic
		// message, the user's own address, a limit).
		line = fmt.Sprintf(tr.N("%s · %d outgoing message held back", "%s · %d outgoing messages held back", r.Skipped), line, r.Skipped)
	}
	return line
}

// ProblemOf is why the daemon turned a rule off; "" for none.
func ProblemOf(p api.RuleProblem, tr Translator) string {
	switch p {
	case api.RuleProblemFolderGone:
		return tr.T("The folder this rule moves messages to no longer exists, so the rule was turned off.")
	case api.RuleProblemSendFailed:
		return tr.T("This rule could not send a message, so it was turned off.")
	}
	return ""
}

// Question is a confirmation: its heading, its text and the label of the
// button that goes ahead (the other one is Cancel).
type Question struct {
	Heading, Body, Confirm string
	// Destructive: the button that goes ahead is shown as destructive.
	Destructive bool
}

// DeleteQuestion asks before Remove deletes a rule.
func DeleteQuestion(name string, tr Translator) Question {
	return Question{
		Heading:     fmt.Sprintf(tr.T("Remove the Rule “%s”?"), name),
		Body:        tr.T("Messages it already moved or marked stay as they are."),
		Confirm:     tr.T("Remove"),
		Destructive: true,
	}
}

// OutgoingQuestion asks before a rule that sends mail is saved enabled or
// turned on (NeedsConfirm). It names where forward sends and says that
// replies go out on their own.
func OutgoingQuestion(in api.RuleInput, tr Translator) Question {
	q := Question{
		Heading: tr.T("Send Mail Automatically?"),
		Confirm: tr.T("Turn On"),
	}
	var to string
	reply := false
	for _, a := range in.Actions {
		switch a.Kind {
		case api.RuleActionForward:
			to = a.To
		case api.RuleActionAutoReply:
			reply = true
		}
	}
	switch {
	case to != "" && reply:
		q.Body = fmt.Sprintf(tr.T("This rule forwards every matching message to %s and answers its sender, without asking you. Bulk mail, automatic messages and your own messages are never forwarded or answered."), to)
	case to != "":
		q.Body = fmt.Sprintf(tr.T("This rule forwards every matching message to %s without asking you. Bulk mail, automatic messages and your own messages are never forwarded."), to)
	default:
		q.Body = tr.T("This rule answers the sender of every matching message without asking you, at most once in four days per sender. Bulk mail, automatic messages and your own messages are never answered.")
	}
	return q
}

// ---------------------------------------------------------------------------
// The editor
// ---------------------------------------------------------------------------

// EditorTitle is the editor's title: New Rule or Edit Rule.
func EditorTitle(f Form, tr Translator) string {
	if f.ID == "" {
		return tr.T("New Rule")
	}
	// TRANSLATORS: the title of the editor of a stored mail rule.
	return tr.T("Edit Rule")
}

// NameLabel is the label of the rule's name field.
func NameLabel(tr Translator) string {
	return tr.T("Name")
}

// AccountLabel is the label of the account choice (the accounts, then
// AllAccounts).
func AccountLabel(tr Translator) string {
	// TRANSLATORS: the label of the account choice of the rule editor.
	return tr.T("Apply to")
}

// MatchLabel is the label of the choice of how conditions combine.
func MatchLabel(tr Translator) string {
	return tr.T("When a new message arrives")
}

// MatchText is a choice of how conditions combine.
func MatchText(m api.RuleMatch, tr Translator) string {
	if m == api.RuleMatchAny {
		return tr.T("and any of these conditions is met")
	}
	return tr.T("and all of these conditions are met")
}

// ActionsHeading is the heading over the actions.
func ActionsHeading(tr Translator) string {
	return tr.T("Do the following")
}

// AddCondition and AddAction are the buttons under the rows.
func AddCondition(tr Translator) string {
	return tr.T("Add Condition")
}

// AddAction is the button (a menu of AvailableActions) under the actions.
func AddAction(tr Translator) string {
	return tr.T("Add Action")
}

// RemoveRow is the tooltip of a row's remove button.
func RemoveRow(tr Translator) string {
	return tr.T("Remove")
}

// EnabledLabel is the editor's switch.
func EnabledLabel(tr Translator) string {
	return tr.T("Rule is on")
}

// StopLabel is the editor's check box of api.RuleInput.Stop.
func StopLabel(tr Translator) string {
	return tr.T("Stop processing more rules")
}

// StopHint is the line under StopLabel.
func StopHint(tr Translator) string {
	return tr.T("Rules below this one do not run on a message this rule matched.")
}

// FieldText is a condition field's choice.
func FieldText(f api.RuleField, tr Translator) string {
	switch f {
	case api.RuleFieldFrom:
		return tr.C("rule field", "From")
	case api.RuleFieldTo:
		return tr.C("rule field", "To")
	case api.RuleFieldCc:
		return tr.C("rule field", "Cc")
	case api.RuleFieldToOrCc:
		return tr.C("rule field", "To or Cc")
	case api.RuleFieldSubject:
		return tr.C("rule field", "Subject")
	case api.RuleFieldBody:
		return tr.C("rule field", "Message text")
	case api.RuleFieldListID:
		// TRANSLATORS: the List-Id of mailing-list mail.
		return tr.C("rule field", "Mailing list ID")
	case api.RuleFieldBulk:
		return tr.C("rule field", "Bulk mail")
	case api.RuleFieldHasAttachments:
		return tr.C("rule field", "Attachments")
	}
	return string(f)
}

// OperatorText is an operator's choice; it reads after FieldText ("From
// contains …").
func OperatorText(f api.RuleField, op api.RuleOperator, tr Translator) string {
	if ValueKindOf(f) == ValueBool {
		// TRANSLATORS: Attachments [are] yes/no in the rule editor.
		return tr.C("rule operator", "are")
	}
	switch op {
	case api.RuleOpContains:
		return tr.C("rule operator", "contains")
	case api.RuleOpNotContains:
		return tr.C("rule operator", "does not contain")
	case api.RuleOpIs:
		return tr.C("rule operator", "is")
	case api.RuleOpIsNot:
		return tr.C("rule operator", "is not")
	case api.RuleOpStartsWith:
		return tr.C("rule operator", "begins with")
	case api.RuleOpEndsWith:
		return tr.C("rule operator", "ends with")
	case api.RuleOpDomainIs:
		return tr.C("rule operator", "is from domain")
	}
	return string(op)
}

// ValueText is a value's choice of a bulk or a hasAttachments condition;
// a text value is itself.
func ValueText(f api.RuleField, value string, tr Translator) string {
	switch ValueKindOf(f) {
	case ValueBulk:
		switch value {
		case api.RuleBulkAny:
			return tr.C("bulk kind", "any kind")
		case string(api.BulkNewsletter):
			return tr.C("bulk kind", "newsletter")
		case string(api.BulkList):
			return tr.C("bulk kind", "mailing list")
		case string(api.BulkAutomated):
			return tr.C("bulk kind", "automated message")
		}
	case ValueBool:
		if value == ValueFalse {
			return tr.C("attachments", "none")
		}
		return tr.C("attachments", "present")
	}
	return value
}

// ValuePlaceholder is the placeholder of a condition's text value.
func ValuePlaceholder(f api.RuleField, op api.RuleOperator, tr Translator) string {
	switch {
	case op == api.RuleOpDomainIs:
		return tr.T("example.com")
	case IsAddressField(f):
		return tr.T("Name or address")
	case f == api.RuleFieldListID:
		return tr.T("list.example.com")
	}
	return tr.T("Text")
}

// ActionText is an action kind's choice (and an item of Add Action).
func ActionText(k api.RuleActionKind, tr Translator) string {
	switch k {
	case api.RuleActionMove:
		return tr.C("rule action", "Move to folder")
	case api.RuleActionMarkRead:
		return tr.C("rule action", "Mark as read")
	case api.RuleActionFlag:
		return tr.C("rule action", "Star")
	case api.RuleActionTrash:
		return tr.C("rule action", "Move to Trash")
	case api.RuleActionJunk:
		return tr.C("rule action", "Move to Junk")
	case api.RuleActionForward:
		return tr.C("rule action", "Forward to")
	case api.RuleActionAutoReply:
		return tr.C("rule action", "Reply automatically")
	}
	return string(k)
}

// FolderPlaceholder is the Move To action's folder choice before a folder
// is chosen.
func FolderPlaceholder(tr Translator) string {
	return tr.T("Choose a Folder")
}

// ForwardPlaceholder is the placeholder of forward's address.
func ForwardPlaceholder(tr Translator) string {
	return tr.T("name@example.com")
}

// ReplySubjectPlaceholder and ReplyTextPlaceholder are the placeholders of
// autoReply's fields.
func ReplySubjectPlaceholder(tr Translator) string {
	return tr.T("Subject (Re: and the original subject if empty)")
}

// ReplyTextPlaceholder is the placeholder of autoReply's text.
func ReplyTextPlaceholder(tr Translator) string {
	return tr.T("Reply text")
}

// OutgoingHint is the line under an outgoing action in the editor.
func OutgoingHint(k api.RuleActionKind, tr Translator) string {
	if k == api.RuleActionAutoReply {
		return tr.T("Each sender gets at most one automatic reply in four days. Bulk mail, automatic messages and your own messages are never answered.")
	}
	return tr.T("The message is sent whole as an attachment. Bulk mail, automatic messages and your own messages are never forwarded.")
}

// ProblemText is the text of a problem Validate found, shown under its
// row.
func ProblemText(p Problem, tr Translator) string {
	switch p.Kind {
	case ProblemNameMissing:
		return tr.T("Enter a name for the rule.")
	case ProblemTooLong:
		return tr.T("This is too long.")
	case ProblemBadCharacters:
		return tr.T("This contains characters that cannot be used.")
	case ProblemNoCondition:
		return tr.T("Add at least one condition.")
	case ProblemTooManyConditions:
		return fmt.Sprintf(tr.N("A rule can have at most %d condition.", "A rule can have at most %d conditions.", api.MaxRuleConditions), api.MaxRuleConditions)
	case ProblemValueMissing:
		return tr.T("Enter a value.")
	case ProblemBadOperator:
		return tr.T("Choose how to compare.")
	case ProblemNoAction:
		return tr.T("Add at least one action.")
	case ProblemTooManyActions:
		return fmt.Sprintf(tr.N("A rule can have at most %d action.", "A rule can have at most %d actions.", api.MaxRuleActions), api.MaxRuleActions)
	case ProblemActionTwice:
		return tr.T("This action is already in the rule.")
	case ProblemOnePlace:
		return tr.T("A rule can move a message to only one place.")
	case ProblemMoveNeedsAccount:
		return tr.T("Choose an account to move messages to one of its folders.")
	case ProblemFolderMissing:
		return tr.T("Choose a folder.")
	case ProblemAddressInvalid:
		return tr.T("Enter one valid email address.")
	case ProblemReplyMissing:
		return tr.T("Enter the text of the reply.")
	}
	return ""
}

// InvalidText is the text of the daemon's invalidArgument with
// RuleInvalidData, for what the editor did not catch.
func InvalidText(d api.RuleInvalidData, tr Translator) string {
	switch d.Field {
	case "name":
		return tr.T("Enter a name for the rule.")
	case "accountId":
		return tr.T("The account of this rule no longer exists.")
	case "confirmOutgoing":
		return tr.T("Rules that send mail need your confirmation.")
	case "conditions":
		return tr.T("A condition of this rule cannot be used.")
	case "actions":
		return tr.T("An action of this rule cannot be used.")
	}
	return tr.T("This rule cannot be saved.")
}

// NotFoundText is ruleNotFound (another window or client removed it).
func NotFoundText(tr Translator) string {
	return tr.T("This rule no longer exists.")
}

// SaveFailedText is the toast of a failed rules.create or rules.update
// for any other error.
func SaveFailedText(tr Translator) string {
	return tr.T("The rule could not be saved.")
}

// TestButton is the editor's button that calls rules.test.
func TestButton(tr Translator) string {
	// TRANSLATORS: a button of the rule editor: shows which stored
	// messages of a folder the rule would match.
	return tr.T("Try It")
}

// TestFolderLabel is the label of the folder Try It looks in (the inbox
// of the rule's account, or of the first account, by default).
func TestFolderLabel(tr Translator) string {
	return tr.T("Try in")
}

// TestResultText is the result of rules.test.
func TestResultText(count int, tr Translator) string {
	if count == 0 {
		return tr.T("No message in this folder matches.")
	}
	return fmt.Sprintf(tr.N("%d message in this folder matches.", "%d messages in this folder match.", count), count)
}

// ---------------------------------------------------------------------------
// Summaries
// ---------------------------------------------------------------------------

// ConditionText is one condition as a phrase: field, operator, value.
func ConditionText(c api.RuleCondition, tr Translator) string {
	value := ValueText(c.Field, c.Value, tr)
	if ValueKindOf(c.Field) == ValueTextField {
		// TRANSLATORS: a condition of a rule in its summary: field,
		// comparison, quoted value ("From contains “acme”").
		return fmt.Sprintf(tr.C("rule condition", "%s %s “%s”"), FieldText(c.Field, tr), OperatorText(c.Field, c.Op, tr), value)
	}
	// TRANSLATORS: a condition with a chosen value ("Bulk mail is newsletter").
	return fmt.Sprintf(tr.C("rule condition", "%s %s %s"), FieldText(c.Field, tr), OperatorText(c.Field, c.Op, tr), value)
}

// ConditionsLine is a rule's conditions in one line, joined by the
// rule's match.
func ConditionsLine(in api.RuleInput, tr Translator) string {
	parts := make([]string, len(in.Conditions))
	for i, c := range in.Conditions {
		parts[i] = ConditionText(c, tr)
	}
	// TRANSLATORS: joins the conditions of a rule that all have to hold
	// (with spaces around it).
	sep := tr.C("all conditions", " and ")
	if in.Match == api.RuleMatchAny {
		// TRANSLATORS: joins the conditions of a rule of which one has
		// to hold (with spaces around it).
		sep = tr.C("any condition", " or ")
	}
	return strings.Join(parts, sep)
}

// ActionPhrase is one action as a phrase.
func ActionPhrase(a api.RuleAction, account api.AccountID, names Names, tr Translator) string {
	switch a.Kind {
	case api.RuleActionMove:
		name := ""
		if names.Folder != nil {
			name = names.Folder(account, a.FolderID)
		}
		if name == "" {
			return tr.T("move to a missing folder")
		}
		return fmt.Sprintf(tr.T("move to “%s”"), name)
	case api.RuleActionMarkRead:
		return tr.T("mark as read")
	case api.RuleActionFlag:
		return tr.T("star")
	case api.RuleActionTrash:
		return tr.T("move to Trash")
	case api.RuleActionJunk:
		return tr.T("move to Junk")
	case api.RuleActionForward:
		return fmt.Sprintf(tr.T("forward to %s"), a.To)
	case api.RuleActionAutoReply:
		return tr.T("reply automatically")
	}
	return string(a.Kind)
}

// ActionsLine is a rule's actions in one line in the order the daemon
// runs them, with stop at the end.
func ActionsLine(in api.RuleInput, account api.AccountID, names Names, tr Translator) string {
	var parts []string
	for _, k := range ActionKinds {
		for _, a := range in.Actions {
			if a.Kind == k {
				parts = append(parts, ActionPhrase(a, account, names, tr))
			}
		}
	}
	if in.Stop {
		parts = append(parts, tr.T("stop processing more rules"))
	}
	// TRANSLATORS: joins the actions of a rule in its summary.
	return strings.Join(parts, tr.C("rule actions", ", "))
}

// PrefillName is the name of a rule made from a message: after its
// sender.
func PrefillName(sender string, tr Translator) string {
	// TRANSLATORS: the name of a rule made from a message; %s is the
	// sender's name or address.
	return fmt.Sprintf(tr.T("Messages from %s"), sender)
}

// ---------------------------------------------------------------------------
// The menus and Apply Rules
// ---------------------------------------------------------------------------

// CreateFromMessageLabel is the item of a message's context menu.
func CreateFromMessageLabel(tr Translator) string {
	return tr.T("Create Rule from Message…")
}

// ApplyLabel is the item of a folder's context menu.
func ApplyLabel(tr Translator) string {
	return tr.T("Apply Rules…")
}

// ApplyQuestion asks before Apply Rules… runs the account's rules on the
// folder.
func ApplyQuestion(folder string, tr Translator) Question {
	return Question{
		Heading: fmt.Sprintf(tr.T("Apply Rules to “%s”?"), folder),
		Body:    tr.T("The rules that are on run on the messages of this folder kept on this computer. Rules do not forward or reply to these messages."),
		Confirm: tr.T("Apply"),
	}
}

// ApplyingText is the progress line while rules.apply runs.
func ApplyingText(tr Translator) string {
	return tr.T("Applying rules")
}

// ApplyResultText is the toast after rules.apply.
func ApplyResultText(r api.RulesApplyResult, tr Translator) string {
	if r.Changed == 0 {
		return tr.T("The rules changed no message.")
	}
	return fmt.Sprintf(tr.N("The rules changed %d message.", "The rules changed %d messages.", r.Changed), r.Changed)
}

// ApplyFailedText is the toast of a failed rules.apply.
func ApplyFailedText(tr Translator) string {
	return tr.T("The rules could not be applied.")
}
