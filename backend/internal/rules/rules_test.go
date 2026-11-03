// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package rules

import (
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func cond(f api.RuleField, op api.RuleOperator, v string) api.RuleCondition {
	return api.RuleCondition{Field: f, Op: op, Value: v}
}

func one(c api.RuleCondition) api.RuleInput {
	return api.RuleInput{Match: api.RuleMatchAll, Conditions: []api.RuleCondition{c}}
}

var alice = api.Address{Name: "Alice Ångström", Address: "Alice@Mail.Example.COM"}

func TestAddressConditions(t *testing.T) {
	m := Message{From: []api.Address{alice}, To: []api.Address{{Address: "me@example.org"}}, CC: []api.Address{{Name: "Bob", Address: "bob@other.test"}}}
	cases := []struct {
		c    api.RuleCondition
		want bool
	}{
		{cond(api.RuleFieldFrom, api.RuleOpContains, "ångström"), true},
		// Decomposed Å (A + ring above) matches the composed name.
		{cond(api.RuleFieldFrom, api.RuleOpContains, "Ångstr"), true},
		{cond(api.RuleFieldFrom, api.RuleOpContains, "<alice@"), true},
		{cond(api.RuleFieldFrom, api.RuleOpNotContains, "alice"), false},
		{cond(api.RuleFieldFrom, api.RuleOpNotContains, "carol"), true},
		{cond(api.RuleFieldFrom, api.RuleOpIs, "alice@mail.example.com"), true},
		// is compares the bare address, not the name.
		{cond(api.RuleFieldFrom, api.RuleOpIs, "Alice Ångström"), false},
		{cond(api.RuleFieldFrom, api.RuleOpIsNot, "alice@mail.example.com"), false},
		{cond(api.RuleFieldFrom, api.RuleOpStartsWith, "alice@"), true},
		{cond(api.RuleFieldFrom, api.RuleOpStartsWith, "Alice Å"), false},
		{cond(api.RuleFieldFrom, api.RuleOpEndsWith, ".com"), true},
		{cond(api.RuleFieldFrom, api.RuleOpDomainIs, "example.com"), true},
		{cond(api.RuleFieldFrom, api.RuleOpDomainIs, "@EXAMPLE.com."), true},
		{cond(api.RuleFieldFrom, api.RuleOpDomainIs, "mail.example.com"), true},
		{cond(api.RuleFieldFrom, api.RuleOpDomainIs, "ample.com"), false},
		{cond(api.RuleFieldFrom, api.RuleOpDomainIs, "com.example"), false},
		{cond(api.RuleFieldTo, api.RuleOpIs, "me@example.org"), true},
		{cond(api.RuleFieldCc, api.RuleOpIs, "me@example.org"), false},
		{cond(api.RuleFieldToOrCc, api.RuleOpDomainIs, "other.test"), true},
		{cond(api.RuleFieldToOrCc, api.RuleOpIs, "me@example.org"), true},
		// domainIs is for addresses only.
		{cond(api.RuleFieldSubject, api.RuleOpDomainIs, "example.com"), false},
	}
	for _, c := range cases {
		if got := Matches(one(c.c), m); got != c.want {
			t.Errorf("%v: %v, want %v", c.c, got, c.want)
		}
	}
}

func TestEmptyAndHostileAddresses(t *testing.T) {
	empty := Message{}
	if Matches(one(cond(api.RuleFieldFrom, api.RuleOpContains, "x")), empty) {
		t.Error("contains on no address")
	}
	if !Matches(one(cond(api.RuleFieldFrom, api.RuleOpNotContains, "x")), empty) {
		t.Error("notContains on no address: none holds")
	}
	if !Matches(one(cond(api.RuleFieldFrom, api.RuleOpIsNot, "x@y.z")), empty) {
		t.Error("isNot on no address")
	}
	if Matches(one(cond(api.RuleFieldFrom, api.RuleOpDomainIs, "y.z")), empty) {
		t.Error("domainIs on no address")
	}
	hostile := Message{From: []api.Address{
		{Name: "\x00\xff\xfe", Address: ""},
		{Name: "", Address: "@"},
		{Name: "evil‮oc.elgoog", Address: "x@evil.test\x00"},
		{Name: strings.Repeat("A", 1<<20), Address: strings.Repeat("b", 1<<16) + "@" + strings.Repeat("c", 1<<16)},
	}}
	// Nothing panics, and an empty address is never "is" an empty value.
	if Matches(one(cond(api.RuleFieldFrom, api.RuleOpIs, "")), hostile) {
		t.Error("an empty address matched is \"\"")
	}
	if Matches(one(cond(api.RuleFieldFrom, api.RuleOpDomainIs, "")), hostile) {
		t.Error("an empty domain matched")
	}
	if !Matches(one(cond(api.RuleFieldFrom, api.RuleOpContains, "elgoog")), hostile) {
		t.Error("the text behind a bidi override is still compared")
	}
	if !Matches(one(cond(api.RuleFieldFrom, api.RuleOpContains, "�")), hostile) {
		t.Error("invalid UTF-8 compares as U+FFFD")
	}
}

func TestTextConditions(t *testing.T) {
	m := Message{Subject: "Invoice NO. 42 – Straße", Body: "Hello\nPlease pay.", HasBody: true, ListID: "go.news.example"}
	cases := []struct {
		c    api.RuleCondition
		want bool
	}{
		{cond(api.RuleFieldSubject, api.RuleOpContains, "invoice no."), true},
		{cond(api.RuleFieldSubject, api.RuleOpContains, "STRASSE"), true}, // full case folding
		{cond(api.RuleFieldSubject, api.RuleOpStartsWith, "invoice"), true},
		{cond(api.RuleFieldSubject, api.RuleOpEndsWith, "straße"), true},
		{cond(api.RuleFieldSubject, api.RuleOpIs, "invoice no. 42 – straße"), true},
		{cond(api.RuleFieldSubject, api.RuleOpIsNot, "x"), true},
		{cond(api.RuleFieldSubject, api.RuleOpNotContains, "invoice"), false},
		{cond(api.RuleFieldBody, api.RuleOpContains, "please PAY"), true},
		{cond(api.RuleFieldListID, api.RuleOpIs, "Go.News.Example"), true},
		{cond(api.RuleFieldListID, api.RuleOpContains, "other"), false},
	}
	for _, c := range cases {
		if got := Matches(one(c.c), m); got != c.want {
			t.Errorf("%v: %v, want %v", c.c, got, c.want)
		}
	}
	// A body not stored matches no body condition, not even notContains.
	noBody := Message{Body: "pay", HasBody: false}
	for _, op := range []api.RuleOperator{api.RuleOpContains, api.RuleOpNotContains, api.RuleOpIsNot} {
		if Matches(one(cond(api.RuleFieldBody, op, "x")), noBody) {
			t.Errorf("body %s matched a message without a body", op)
		}
	}
	// Only the first MaxRuleBodyBytes of the body count.
	long := Message{Body: strings.Repeat("a", api.MaxRuleBodyBytes) + "needle", HasBody: true}
	if Matches(one(cond(api.RuleFieldBody, api.RuleOpContains, "needle")), long) {
		t.Error("text past MaxRuleBodyBytes matched")
	}
	// A multibyte rune cut at the limit does not break the comparison.
	cut := Message{Body: strings.Repeat("a", api.MaxRuleBodyBytes-1) + "é", HasBody: true}
	if !Matches(one(cond(api.RuleFieldBody, api.RuleOpStartsWith, "aaa")), cut) {
		t.Error("a body cut in a rune")
	}
}

func TestBulkAndAttachments(t *testing.T) {
	news := Message{Bulk: api.BulkNewsletter, HasAttachments: true}
	plain := Message{}
	cases := []struct {
		c          api.RuleCondition
		news, none bool
	}{
		{cond(api.RuleFieldBulk, api.RuleOpIs, api.RuleBulkAny), true, false},
		{cond(api.RuleFieldBulk, api.RuleOpIsNot, api.RuleBulkAny), false, true},
		{cond(api.RuleFieldBulk, api.RuleOpIs, "newsletter"), true, false},
		{cond(api.RuleFieldBulk, api.RuleOpIs, "list"), false, false},
		{cond(api.RuleFieldBulk, api.RuleOpIsNot, "list"), true, true},
		{cond(api.RuleFieldBulk, api.RuleOpContains, "news"), false, false},
		{cond(api.RuleFieldHasAttachments, api.RuleOpIs, "true"), true, false},
		{cond(api.RuleFieldHasAttachments, api.RuleOpIs, "false"), false, true},
		{cond(api.RuleFieldHasAttachments, api.RuleOpIs, "yes"), false, false},
		{cond(api.RuleFieldHasAttachments, api.RuleOpIsNot, "true"), false, false},
		{cond("nonsense", api.RuleOpIs, "x"), false, false},
	}
	for _, c := range cases {
		if got := Matches(one(c.c), news); got != c.news {
			t.Errorf("%v on news: %v", c.c, got)
		}
		if got := Matches(one(c.c), plain); got != c.none {
			t.Errorf("%v on plain: %v", c.c, got)
		}
	}
}

func TestMatchAllAny(t *testing.T) {
	m := Message{Subject: "a", From: []api.Address{{Address: "x@y.z"}}}
	yes := cond(api.RuleFieldSubject, api.RuleOpIs, "a")
	no := cond(api.RuleFieldSubject, api.RuleOpIs, "b")
	all := api.RuleInput{Match: api.RuleMatchAll, Conditions: []api.RuleCondition{yes, no}}
	anyOf := api.RuleInput{Match: api.RuleMatchAny, Conditions: []api.RuleCondition{no, yes}}
	if Matches(all, m) || !Matches(anyOf, m) {
		t.Error("all/any")
	}
	if Matches(api.RuleInput{Match: api.RuleMatchAny}, m) || Matches(api.RuleInput{}, m) {
		t.Error("a rule without conditions matched")
	}
}

func rule(id string, stop bool, kinds ...api.RuleActionKind) api.Rule {
	r := api.Rule{ID: id, RuleInput: api.RuleInput{Match: api.RuleMatchAll, Stop: stop,
		Conditions: []api.RuleCondition{cond(api.RuleFieldSubject, api.RuleOpContains, "x")}}}
	for _, k := range kinds {
		a := api.RuleAction{Kind: k}
		switch k {
		case api.RuleActionMove:
			a.FolderID = api.FolderID("f_" + id)
		case api.RuleActionForward:
			a.To = id + "@fwd.example"
		case api.RuleActionAutoReply:
			a.Text = "away"
		}
		r.Actions = append(r.Actions, a)
	}
	return r
}

func kinds(st Step) []api.RuleActionKind {
	var out []api.RuleActionKind
	for _, a := range st.Actions {
		out = append(out, a.Kind)
	}
	return out
}

func TestPlanOrder(t *testing.T) {
	m := Message{Subject: "x"}
	// Actions run in the fixed order, whatever the rule's order.
	r1 := rule("r1", false, api.RuleActionMove, api.RuleActionAutoReply, api.RuleActionFlag, api.RuleActionForward, api.RuleActionMarkRead)
	// The second rule's trash loses to the first move; its flag stays.
	r2 := rule("r2", false, api.RuleActionTrash, api.RuleActionFlag)
	// A rule that does not match is no step.
	r3 := rule("r3", false, api.RuleActionJunk)
	r3.Conditions[0].Value = "nope"
	// A stop ends the evaluation.
	r4 := rule("r4", true, api.RuleActionMarkRead)
	r5 := rule("r5", false, api.RuleActionFlag)
	steps := Plan([]api.Rule{r1, r2, r3, r4, r5}, m)
	if len(steps) != 3 || steps[0].RuleID != "r1" || steps[1].RuleID != "r2" || steps[2].RuleID != "r4" {
		t.Fatalf("steps %+v", steps)
	}
	want := []api.RuleActionKind{api.RuleActionMarkRead, api.RuleActionFlag, api.RuleActionForward, api.RuleActionAutoReply, api.RuleActionMove}
	if got := kinds(steps[0]); !reflect.DeepEqual(got, want) {
		t.Errorf("r1 %v", got)
	}
	if got := kinds(steps[1]); !reflect.DeepEqual(got, []api.RuleActionKind{api.RuleActionFlag}) {
		t.Errorf("r2 %v", got)
	}
	e := EffectsOf(steps)
	if !e.MarkRead || !e.Flag || e.Place == nil || e.Place.Kind != api.RuleActionMove || e.PlaceRule != "r1" || len(e.Outgoing) != 2 {
		t.Errorf("effects %+v", e)
	}
	// A matched rule whose only action lost is still a step (it counts as
	// applied).
	steps = Plan([]api.Rule{rule("a", false, api.RuleActionJunk), rule("b", false, api.RuleActionTrash)}, m)
	if len(steps) != 2 || len(steps[1].Actions) != 0 {
		t.Errorf("steps %+v", steps)
	}
	if NeedsBody([]api.Rule{r1}) {
		t.Error("no body condition")
	}
}

func TestNeedsConfirm(t *testing.T) {
	plain := api.RuleInput{Enabled: true, Actions: []api.RuleAction{{Kind: api.RuleActionFlag}}}
	fwd := api.RuleInput{Enabled: true, Actions: []api.RuleAction{{Kind: api.RuleActionForward, To: "a@b.c"}}}
	off := fwd
	off.Enabled = false
	other := api.RuleInput{Enabled: true, Actions: []api.RuleAction{{Kind: api.RuleActionForward, To: "x@b.c"}}}
	cases := []struct {
		old  *api.RuleInput
		in   api.RuleInput
		want bool
	}{
		{nil, plain, false},
		{nil, fwd, true},
		{nil, off, false},
		{&plain, fwd, true},
		{&off, fwd, true},
		{&fwd, fwd, false},
		{&fwd, other, true},
		{&fwd, plain, false},
	}
	for i, c := range cases {
		if got := NeedsConfirm(c.old, c.in); got != c.want {
			t.Errorf("%d: %v", i, got)
		}
	}
}
