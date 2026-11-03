// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package rules is the pure part of the mail rules (docs/api.md §4.21,
// docs/plans/rules.md): whether a rule's conditions hold for a message,
// which actions run in which order over several rules, and the
// validation of a rule as the user edits it. It has no store, no clock
// and no network; internal/core feeds it a message and carries the plan
// out.
//
// Every message string is hostile input: invalid UTF-8 is replaced
// before it is compared, and comparisons are on NFC-normalised,
// case-folded text, so a value matches however its letters were
// composed or cased.
package rules

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Message is what the conditions look at.
type Message struct {
	From, To, CC []api.Address
	Subject      string
	// Body is the plain text of the message (the text part, else the
	// text of the HTML part), at most api.MaxRuleBodyBytes of it; HasBody
	// says whether it is known (a message whose body is not stored yet
	// matches no body condition).
	Body    string
	HasBody bool
	// ListID is the cleaned List-Id (BulkInfo.ListID).
	ListID string
	// Bulk is the message's bulk kind; "" for one that is not bulk or not
	// classified yet.
	Bulk           api.BulkKind
	HasAttachments bool
}

// fold is the comparison form of s: valid UTF-8, case-folded, NFC.
func fold(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	// A Caser is stateful, so one per call.
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(s)))
}

// NeedsBody reports whether a rule of the list has a body condition: the
// caller reads the body only then.
func NeedsBody(list []api.Rule) bool {
	for _, r := range list {
		if HasBodyCondition(r.RuleInput) {
			return true
		}
	}
	return false
}

// HasBodyCondition reports whether the rule input has a body condition.
func HasBodyCondition(in api.RuleInput) bool {
	for _, c := range in.Conditions {
		if c.Field == api.RuleFieldBody {
			return true
		}
	}
	return false
}

// Matches reports whether the rule's conditions hold for m, combined by
// its match ("any", else all). A rule without conditions never matches.
func Matches(in api.RuleInput, m Message) bool {
	if len(in.Conditions) == 0 {
		return false
	}
	anyOf := in.Match == api.RuleMatchAny
	for _, c := range in.Conditions {
		ok := conditionHolds(c, m)
		if anyOf && ok {
			return true
		}
		if !anyOf && !ok {
			return false
		}
	}
	return !anyOf
}

// conditionHolds is one condition; an unknown field or operator never
// holds.
func conditionHolds(c api.RuleCondition, m Message) bool {
	switch c.Field {
	case api.RuleFieldFrom:
		return addressHolds(c.Op, c.Value, m.From)
	case api.RuleFieldTo:
		return addressHolds(c.Op, c.Value, m.To)
	case api.RuleFieldCc:
		return addressHolds(c.Op, c.Value, m.CC)
	case api.RuleFieldToOrCc:
		all := make([]api.Address, 0, len(m.To)+len(m.CC))
		all = append(append(all, m.To...), m.CC...)
		return addressHolds(c.Op, c.Value, all)
	case api.RuleFieldSubject:
		return textHolds(c.Op, c.Value, m.Subject)
	case api.RuleFieldBody:
		if !m.HasBody {
			return false
		}
		return textHolds(c.Op, c.Value, truncate(m.Body, api.MaxRuleBodyBytes))
	case api.RuleFieldListID:
		return textHolds(c.Op, c.Value, m.ListID)
	case api.RuleFieldBulk:
		is := m.Bulk != "" && (c.Value == api.RuleBulkAny || string(m.Bulk) == c.Value)
		switch c.Op {
		case api.RuleOpIs:
			return is
		case api.RuleOpIsNot:
			return !is
		}
		return false
	case api.RuleFieldHasAttachments:
		if c.Op != api.RuleOpIs {
			return false
		}
		switch c.Value {
		case "true":
			return m.HasAttachments
		case "false":
			return !m.HasAttachments
		}
	}
	return false
}

// textHolds compares one text value.
func textHolds(op api.RuleOperator, value, text string) bool {
	v, t := fold(value), fold(text)
	switch op {
	case api.RuleOpContains:
		return strings.Contains(t, v)
	case api.RuleOpNotContains:
		return !strings.Contains(t, v)
	case api.RuleOpIs:
		return t == v
	case api.RuleOpIsNot:
		return t != v
	case api.RuleOpStartsWith:
		return strings.HasPrefix(t, v)
	case api.RuleOpEndsWith:
		return strings.HasSuffix(t, v)
	}
	return false
}

// addressHolds compares an address field: contains and notContains look
// at "Name <address>", the other text operators at the bare address,
// domainIs at its domain. It holds when any address does; notContains
// and isNot when no address holds the positive form.
func addressHolds(op api.RuleOperator, value string, list []api.Address) bool {
	switch op {
	case api.RuleOpNotContains:
		return !addressHolds(api.RuleOpContains, value, list)
	case api.RuleOpIsNot:
		return !addressHolds(api.RuleOpIs, value, list)
	}
	v := fold(value)
	if op == api.RuleOpDomainIs {
		v = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "@"), ".")
		if v == "" {
			return false
		}
	}
	for _, a := range list {
		addr := fold(strings.TrimSpace(a.Address))
		switch op {
		case api.RuleOpContains:
			full := "<" + addr + ">"
			if name := strings.TrimSpace(a.Name); name != "" {
				full = fold(name) + " " + full
			}
			if strings.Contains(full, v) {
				return true
			}
		case api.RuleOpIs:
			if addr != "" && addr == v {
				return true
			}
		case api.RuleOpStartsWith:
			if addr != "" && strings.HasPrefix(addr, v) {
				return true
			}
		case api.RuleOpEndsWith:
			if addr != "" && strings.HasSuffix(addr, v) {
				return true
			}
		case api.RuleOpDomainIs:
			d := Domain(addr)
			if d != "" && (d == v || strings.HasSuffix(d, "."+v)) {
				return true
			}
		}
	}
	return false
}

// Domain is the domain of an address (after its last "@"), without a
// trailing dot; "" when there is none.
func Domain(addr string) string {
	i := strings.LastIndexByte(addr, '@')
	if i < 0 {
		return ""
	}
	return strings.TrimSuffix(addr[i+1:], ".")
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

// Truncate is truncate for the caller that reads a body.
func Truncate(s string, n int) string { return truncate(s, n) }

// actionOrder is the fixed order of one rule's actions; the placing
// actions (move, junk, trash) go last and a rule has at most one.
var actionOrder = []api.RuleActionKind{
	api.RuleActionMarkRead, api.RuleActionFlag, api.RuleActionForward, api.RuleActionAutoReply,
	api.RuleActionMove, api.RuleActionJunk, api.RuleActionTrash,
}

// IsPlacing reports whether the action decides where the message goes.
func IsPlacing(k api.RuleActionKind) bool {
	return k == api.RuleActionMove || k == api.RuleActionJunk || k == api.RuleActionTrash
}

// Step is one rule that matched and the actions it runs, in the fixed
// order; a placing action when another rule placed the message before is
// left out.
type Step struct {
	RuleID  string
	Actions []api.RuleAction
}

// Plan evaluates the rules in their order on m: every rule that matches
// is a step, until one with stop matched. The caller passes only the
// rules that apply (enabled, of the account, in time).
func Plan(list []api.Rule, m Message) []Step {
	var steps []Step
	placed := false
	for _, r := range list {
		if !Matches(r.RuleInput, m) {
			continue
		}
		st := Step{RuleID: r.ID}
		for _, k := range actionOrder {
			i := indexOfKind(r.Actions, k)
			if i < 0 {
				continue
			}
			if IsPlacing(k) {
				if placed {
					continue
				}
				placed = true
			}
			st.Actions = append(st.Actions, r.Actions[i])
		}
		steps = append(steps, st)
		if r.Stop {
			break
		}
	}
	return steps
}

// indexOfKind is the first action of the kind, -1 for none (each kind at
// most once).
func indexOfKind(list []api.RuleAction, k api.RuleActionKind) int {
	for i, a := range list {
		if a.Kind == k {
			return i
		}
	}
	return -1
}

// Effects is what a plan does to the message, gathered over its steps.
type Effects struct {
	MarkRead, Flag bool
	// Place is the first placing action and PlaceRule its rule; nil when
	// none.
	Place     *api.RuleAction
	PlaceRule string
	// Outgoing are the outgoing actions with their rules, in order.
	Outgoing []Outgoing
}

// Outgoing is one outgoing action of a step.
type Outgoing struct {
	RuleID string
	Action api.RuleAction
}

// EffectsOf gathers the steps.
func EffectsOf(steps []Step) Effects {
	var e Effects
	for _, st := range steps {
		for _, a := range st.Actions {
			switch {
			case a.Kind == api.RuleActionMarkRead:
				e.MarkRead = true
			case a.Kind == api.RuleActionFlag:
				e.Flag = true
			case a.Kind.IsOutgoing():
				e.Outgoing = append(e.Outgoing, Outgoing{RuleID: st.RuleID, Action: a})
			case IsPlacing(a.Kind) && e.Place == nil:
				act := a
				e.Place, e.PlaceRule = &act, st.RuleID
			}
		}
	}
	return e
}

// HasOutgoing reports whether a rule input has an outgoing action.
func HasOutgoing(in api.RuleInput) bool {
	for _, a := range in.Actions {
		if a.Kind.IsOutgoing() {
			return true
		}
	}
	return false
}

// NeedsConfirm reports whether saving in over old (nil for a new rule)
// needs confirmOutgoing (R12): in is enabled with an outgoing action and
// old was not, or an outgoing action's To, Subject or Text changed.
func NeedsConfirm(old *api.RuleInput, in api.RuleInput) bool {
	if !in.Enabled || !HasOutgoing(in) {
		return false
	}
	if old == nil || !old.Enabled || !HasOutgoing(*old) {
		return true
	}
	return outgoingKey(*old) != outgoingKey(in)
}

func outgoingKey(in api.RuleInput) string {
	var b strings.Builder
	for _, k := range []api.RuleActionKind{api.RuleActionForward, api.RuleActionAutoReply} {
		if i := indexOfKind(in.Actions, k); i >= 0 {
			a := in.Actions[i]
			b.WriteString(string(k) + "\x00" + a.To + "\x00" + a.Subject + "\x00" + a.Text + "\x00")
		}
	}
	return b.String()
}
