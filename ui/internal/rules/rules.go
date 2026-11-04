// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package rules is the reference of the mail rules' pure UI logic
// (docs/plans/rules.md, docs/api.md §4.21): the editable form of a rule
// and its validation, the choices each row of the editor offers, a rule
// prefilled from a message (Create Rule from Message…), the order after a
// drag, which outgoing changes need the user's confirmation, which rules
// apply to an account (Apply Rules of a folder's menu), and every text the
// Rules page, the editor and the questions show (text.go).
//
// The daemon stores and runs the rules (rules.*); this package only turns
// them into view models and back. It is pure (no GTK, no gettext, no cgo:
// the caller passes a Translator) and the reference ported one to one to
// macOS (macos/Sources/MalachiCore/Rules/) and Windows
// (windows/src/Malachi.Core/Rules/). Every string of a rule may come from
// mail (a prefilled sender, a list id); clients show it as plain text only.
//
// The flow every client follows:
//
//   - the Rules page of Preferences (after Integrations) calls rules.list
//     at open and on every notify.rulesChanged and shows Rows; the switch
//     of a row calls rules.update with Enabled flipped (asking
//     OutgoingQuestion first when NeedsConfirm says so), a drag or
//     Alt+Up/Down calls rules.reorder with Moved, Remove asks
//     DeleteQuestion and calls rules.delete;
//   - Add Rule… opens the editor with NewForm, Edit… with FromRule; the
//     editor shows Fields, OperatorsFor, ValueKindOf and ActionKinds,
//     Save is enabled when Validate finds nothing and sends
//     rules.create / rules.update with Input (after OutgoingQuestion when
//     NeedsConfirm), Test calls rules.test over the folder chosen and
//     shows TestResultText;
//   - Create Rule from Message… opens the editor with FromMessage;
//   - Apply Rules… of a folder's menu (offered when AppliesToAccount
//     finds a rule) asks ApplyQuestion, calls rules.apply {accountId,
//     folderId} and shows ApplyResultText as a toast.
package rules

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Fields are the condition fields in the order the editor offers them.
var Fields = []api.RuleField{
	api.RuleFieldFrom, api.RuleFieldTo, api.RuleFieldCc, api.RuleFieldToOrCc,
	api.RuleFieldSubject, api.RuleFieldBody, api.RuleFieldListID,
	api.RuleFieldBulk, api.RuleFieldHasAttachments,
}

// ActionKinds are the action kinds in the order the editor offers them
// (and the daemon runs them: docs/api.md §4.21).
var ActionKinds = []api.RuleActionKind{
	api.RuleActionMarkRead, api.RuleActionFlag,
	api.RuleActionForward, api.RuleActionAutoReply,
	api.RuleActionMove, api.RuleActionJunk, api.RuleActionTrash,
}

// BulkValues are the values of a bulk condition, in the editor's order.
var BulkValues = []string{api.RuleBulkAny, string(api.BulkNewsletter), string(api.BulkList), string(api.BulkAutomated)}

// The values of a hasAttachments condition.
const (
	ValueTrue  = "true"
	ValueFalse = "false"
)

// ValueKind is what the value of a condition is edited with.
type ValueKind int

const (
	// ValueTextField: a text field.
	ValueTextField ValueKind = iota
	// ValueBulk: a choice of BulkValues.
	ValueBulk
	// ValueBool: a choice of ValueTrue and ValueFalse.
	ValueBool
)

// ValueKindOf is the editor of a field's value.
func ValueKindOf(f api.RuleField) ValueKind {
	switch f {
	case api.RuleFieldBulk:
		return ValueBulk
	case api.RuleFieldHasAttachments:
		return ValueBool
	}
	return ValueTextField
}

// IsAddressField reports whether the field holds addresses.
func IsAddressField(f api.RuleField) bool {
	switch f {
	case api.RuleFieldFrom, api.RuleFieldTo, api.RuleFieldCc, api.RuleFieldToOrCc:
		return true
	}
	return false
}

// OperatorsFor are the operators a field takes, in the editor's order;
// the first is the default.
func OperatorsFor(f api.RuleField) []api.RuleOperator {
	switch ValueKindOf(f) {
	case ValueBulk:
		return []api.RuleOperator{api.RuleOpIs, api.RuleOpIsNot}
	case ValueBool:
		return []api.RuleOperator{api.RuleOpIs}
	}
	ops := []api.RuleOperator{api.RuleOpContains, api.RuleOpNotContains, api.RuleOpIs,
		api.RuleOpIsNot, api.RuleOpStartsWith, api.RuleOpEndsWith}
	if IsAddressField(f) {
		ops = append(ops, api.RuleOpDomainIs)
	}
	return ops
}

// DefaultValue is the value a condition gets when its field is chosen.
func DefaultValue(f api.RuleField) string {
	switch ValueKindOf(f) {
	case ValueBulk:
		return api.RuleBulkAny
	case ValueBool:
		return ValueTrue
	}
	return ""
}

// NewCondition is the condition a new row of the editor starts as (From
// contains), or the one of a row whose field the user changed: the
// operator kept when the new field takes it, the value when it is text
// both times.
func NewCondition(f api.RuleField, old *api.RuleCondition) api.RuleCondition {
	c := api.RuleCondition{Field: f, Op: OperatorsFor(f)[0], Value: DefaultValue(f)}
	if old == nil {
		return c
	}
	for _, op := range OperatorsFor(f) {
		if op == old.Op {
			c.Op = op
		}
	}
	if ValueKindOf(f) == ValueTextField && ValueKindOf(old.Field) == ValueTextField {
		c.Value = old.Value
	}
	return c
}

// Form is a rule as the editor holds it. AccountID "" is every mail
// account.
type Form struct {
	// ID is the edited rule's, "" for a new one.
	ID string
	api.RuleInput
}

// NewForm is the editor of a new rule: enabled, every condition, one
// empty From condition, no action yet; for the account given (the one
// the main window shows), "" for every account.
func NewForm(accountID api.AccountID) Form {
	return Form{RuleInput: api.RuleInput{
		Enabled:    true,
		AccountID:  accountID,
		Match:      api.RuleMatchAll,
		Conditions: []api.RuleCondition{NewCondition(api.RuleFieldFrom, nil)},
	}}
}

// FromRule is the editor of a stored rule; the slices are copies.
func FromRule(r api.Rule) Form {
	f := Form{ID: r.ID, RuleInput: r.RuleInput}
	f.Conditions = append([]api.RuleCondition(nil), r.Conditions...)
	f.Actions = append([]api.RuleAction(nil), r.Actions...)
	if f.Match == "" {
		f.Match = api.RuleMatchAll
	}
	return f
}

// Input is what rules.create and rules.update send: the strings trimmed,
// the fields of other action kinds cleared.
func (f Form) Input() api.RuleInput {
	in := f.RuleInput
	in.Name = strings.TrimSpace(in.Name)
	in.Conditions = make([]api.RuleCondition, len(f.Conditions))
	for i, c := range f.Conditions {
		if ValueKindOf(c.Field) == ValueTextField {
			c.Value = strings.TrimSpace(c.Value)
		}
		in.Conditions[i] = c
	}
	in.Actions = make([]api.RuleAction, len(f.Actions))
	for i, a := range f.Actions {
		out := api.RuleAction{Kind: a.Kind}
		switch a.Kind {
		case api.RuleActionMove:
			out.FolderID = a.FolderID
		case api.RuleActionForward:
			out.To = strings.TrimSpace(a.To)
		case api.RuleActionAutoReply:
			out.Subject = strings.TrimSpace(a.Subject)
			out.Text = strings.TrimRight(a.Text, " \t\r\n")
		}
		in.Actions[i] = out
	}
	return in
}

// IsOutgoing reports whether a rule input has an outgoing action.
func IsOutgoing(in api.RuleInput) bool {
	for _, a := range in.Actions {
		if a.Kind.IsOutgoing() {
			return true
		}
	}
	return false
}

// AvailableActions are the kinds an Add Action menu offers: each kind
// once, move only with an account, and none of move, junk and trash once
// the rule has one of them.
func AvailableActions(f Form) []api.RuleActionKind {
	has := map[api.RuleActionKind]bool{}
	for _, a := range f.Actions {
		has[a.Kind] = true
	}
	place := has[api.RuleActionMove] || has[api.RuleActionJunk] || has[api.RuleActionTrash]
	var out []api.RuleActionKind
	for _, k := range ActionKinds {
		if has[k] {
			continue
		}
		if isPlacing(k) && place {
			continue
		}
		if k == api.RuleActionMove && f.AccountID == "" {
			continue
		}
		out = append(out, k)
	}
	return out
}

func isPlacing(k api.RuleActionKind) bool {
	return k == api.RuleActionMove || k == api.RuleActionJunk || k == api.RuleActionTrash
}

// Problem is what Validate found: the field ("name", "accountId",
// "conditions", "actions") and, for the last two, the row; Kind picks the
// text (ProblemText).
type Problem struct {
	Field string
	Index int
	Kind  ProblemKind
}

// ProblemKind is what is wrong.
type ProblemKind int

const (
	ProblemNameMissing ProblemKind = iota + 1
	ProblemTooLong
	ProblemNoCondition
	ProblemTooManyConditions
	ProblemValueMissing
	ProblemBadCharacters
	ProblemNoAction
	ProblemTooManyActions
	ProblemActionTwice
	ProblemOnePlace
	ProblemMoveNeedsAccount
	ProblemFolderMissing
	ProblemAddressInvalid
	ProblemReplyMissing
	ProblemBadOperator
)

// Validate is every problem of the form, in the order of the editor;
// none means Save is enabled. The daemon checks again (RuleInvalidData).
func Validate(f Form) []Problem {
	in := f.Input()
	var out []Problem
	add := func(field string, i int, k ProblemKind) {
		out = append(out, Problem{Field: field, Index: i, Kind: k})
	}
	switch {
	case in.Name == "":
		add("name", 0, ProblemNameMissing)
	case len(in.Name) > api.MaxRuleNameBytes:
		add("name", 0, ProblemTooLong)
	case !Clean(in.Name):
		add("name", 0, ProblemBadCharacters)
	}
	switch {
	case len(in.Conditions) == 0:
		add("conditions", 0, ProblemNoCondition)
	case len(in.Conditions) > api.MaxRuleConditions:
		add("conditions", 0, ProblemTooManyConditions)
	}
	for i, c := range in.Conditions {
		if !operatorOf(c.Field, c.Op) {
			add("conditions", i, ProblemBadOperator)
			continue
		}
		switch ValueKindOf(c.Field) {
		case ValueBulk:
			if !contains(BulkValues, c.Value) {
				add("conditions", i, ProblemValueMissing)
			}
		case ValueBool:
			if c.Value != ValueTrue && c.Value != ValueFalse {
				add("conditions", i, ProblemValueMissing)
			}
		default:
			switch {
			case c.Value == "":
				add("conditions", i, ProblemValueMissing)
			case len(c.Value) > api.MaxRuleValueBytes:
				add("conditions", i, ProblemTooLong)
			case !Clean(c.Value):
				add("conditions", i, ProblemBadCharacters)
			}
		}
	}
	switch {
	case len(in.Actions) == 0:
		add("actions", 0, ProblemNoAction)
	case len(in.Actions) > api.MaxRuleActions:
		add("actions", 0, ProblemTooManyActions)
	}
	seen := map[api.RuleActionKind]bool{}
	placed := false
	for i, a := range in.Actions {
		if seen[a.Kind] {
			add("actions", i, ProblemActionTwice)
			continue
		}
		seen[a.Kind] = true
		if isPlacing(a.Kind) {
			if placed {
				add("actions", i, ProblemOnePlace)
				continue
			}
			placed = true
		}
		switch a.Kind {
		case api.RuleActionMove:
			if in.AccountID == "" {
				add("actions", i, ProblemMoveNeedsAccount)
			} else if a.FolderID == "" {
				add("actions", i, ProblemFolderMissing)
			}
		case api.RuleActionForward:
			if !ValidAddress(a.To) {
				add("actions", i, ProblemAddressInvalid)
			}
		case api.RuleActionAutoReply:
			switch {
			case strings.TrimSpace(a.Text) == "":
				add("actions", i, ProblemReplyMissing)
			case len(a.Text) > api.MaxRuleReplyBytes || len(a.Subject) > api.MaxRuleValueBytes:
				add("actions", i, ProblemTooLong)
			case !Clean(a.Subject) || !cleanText(a.Text):
				add("actions", i, ProblemBadCharacters)
			}
		}
	}
	return out
}

func operatorOf(f api.RuleField, op api.RuleOperator) bool {
	for _, o := range OperatorsFor(f) {
		if o == op {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Clean reports whether a one-line value has no control, format (bidi,
// invisible) or line-breaking character, the daemon's rule for every
// string of a rule.
func Clean(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' ' {
			return false
		}
	}
	return true
}

// cleanText is Clean that lets line breaks and tabs through (the text of
// an automatic reply).
func cleanText(s string) bool {
	return Clean(strings.NewReplacer("\r\n", "", "\n", "", "\t", "").Replace(s))
}

// ValidAddress is a bare address the daemon accepts as forward's To:
// local@domain, no spaces, angle brackets, commas or control characters,
// a dot in the domain.
func ValidAddress(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 || !Clean(s) || strings.ContainsAny(s, " <>,;:\"()[]\\") {
		return false
	}
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || at == len(s)-1 || strings.Count(s, "@") != 1 {
		return false
	}
	domain := s[at+1:]
	return strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") &&
		!strings.HasSuffix(domain, ".") && !strings.Contains(domain, "..")
}

// FromMessage is the editor of Create Rule from Message…: named after the
// sender, From is the sender's address and, for a mailing list or a
// newsletter with a List-Id, List ID is that id instead (one condition,
// which is what such mail has in common); one Move To action without a
// folder yet, so the user picks where it goes; for the message's
// account.
func FromMessage(m api.MessageSummary, accountID api.AccountID, tr Translator) Form {
	f := NewForm(accountID)
	sender := ""
	if len(m.From) > 0 {
		sender = strings.TrimSpace(m.From[0].Address)
		if name := strings.TrimSpace(m.From[0].Name); name != "" && Clean(name) {
			f.Name = PrefillName(name, tr)
		} else if sender != "" {
			f.Name = PrefillName(sender, tr)
		}
	}
	switch {
	case m.Bulk != nil && m.Bulk.ListID != "" && Clean(m.Bulk.ListID):
		f.Conditions = []api.RuleCondition{{Field: api.RuleFieldListID, Op: api.RuleOpIs, Value: m.Bulk.ListID}}
	case sender != "" && Clean(sender):
		f.Conditions = []api.RuleCondition{{Field: api.RuleFieldFrom, Op: api.RuleOpIs, Value: sender}}
	}
	if len(f.Name) > api.MaxRuleNameBytes {
		f.Name = truncate(f.Name, api.MaxRuleNameBytes)
	}
	if accountID != "" {
		f.Actions = []api.RuleAction{{Kind: api.RuleActionMove}}
	}
	return f
}

// truncate cuts s to at most n bytes on a rune boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// NeedsConfirm reports whether saving the form must ask
// OutgoingQuestion and send confirmOutgoing (docs/api.md §4.21): the rule
// is enabled with an outgoing action and either old (the stored rule,
// nil for a new one) was not, or an outgoing action's To, Subject or
// Text changed.
func NeedsConfirm(old *api.Rule, in api.RuleInput) bool {
	if !in.Enabled || !IsOutgoing(in) {
		return false
	}
	if old == nil || !old.Enabled || !IsOutgoing(old.RuleInput) {
		return true
	}
	return outgoingKey(old.RuleInput) != outgoingKey(in)
}

func outgoingKey(in api.RuleInput) string {
	var b strings.Builder
	for _, k := range []api.RuleActionKind{api.RuleActionForward, api.RuleActionAutoReply} {
		for _, a := range in.Actions {
			if a.Kind == k {
				b.WriteString(string(k) + "\x00" + a.To + "\x00" + a.Subject + "\x00" + a.Text + "\x00")
			}
		}
	}
	return b.String()
}

// Toggled is the input of a row's switch: the rule with Enabled flipped.
func Toggled(r api.Rule) api.RuleInput {
	in := FromRule(r).RuleInput
	in.Enabled = !r.Enabled
	return in
}

// Moved is the order of rules.reorder after the rule at from was dropped
// at to (an index of the list without it); an index out of range leaves
// the order as it is.
func Moved(ids []string, from, to int) []string {
	out := append([]string(nil), ids...)
	if from < 0 || from >= len(out) || to < 0 || to >= len(out) || from == to {
		return out
	}
	id := out[from]
	out = append(out[:from], out[from+1:]...)
	out = append(out[:to], append([]string{id}, out[to:]...)...)
	return out
}

// AppliesToAccount reports whether an enabled rule runs on the account's
// mail (its own or one for every account): Apply Rules… of the account's
// folders is offered then.
func AppliesToAccount(rules []api.Rule, accountID api.AccountID) bool {
	for _, r := range rules {
		if r.Enabled && (r.AccountID == "" || r.AccountID == accountID) {
			return true
		}
	}
	return false
}

// CanApplyIn reports whether Apply Rules… is offered for a folder of a
// mail account: one that holds messages, not the outbox or the drafts.
func CanApplyIn(f api.Folder) bool {
	return f.Selectable && f.Virtual == "" && f.Role != api.RoleOutbox && f.Role != api.RoleDrafts
}

// CanCreateFrom reports whether Create Rule from Message… is offered for
// a message in a folder of the role: not in the outbox, the drafts or
// the sent mail.
func CanCreateFrom(role api.FolderRole) bool {
	return role != api.RoleOutbox && role != api.RoleDrafts && role != api.RoleSent
}

// MoveFolders are the folders a Move To action offers, from the account's
// folders in the sidebar's order: the selectable ones other than the
// outbox, the drafts, an \All folder and an issue tracker's views.
func MoveFolders(folders []api.Folder) []api.Folder {
	var out []api.Folder
	for _, f := range folders {
		if !f.Selectable || f.Virtual != "" {
			continue
		}
		switch f.Role {
		case api.RoleOutbox, api.RoleDrafts, api.RoleAll:
			continue
		}
		out = append(out, f)
	}
	return out
}
