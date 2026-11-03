// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package rules

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// identity translates nothing.
type identity struct{}

func (identity) T(msgid string) string { return msgid }
func (identity) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (identity) C(_, msgid string) string { return msgid }

var tr identity

func TestOperatorsFor(t *testing.T) {
	cases := map[api.RuleField]string{
		api.RuleFieldFrom:           "contains notContains is isNot startsWith endsWith domainIs",
		api.RuleFieldToOrCc:         "contains notContains is isNot startsWith endsWith domainIs",
		api.RuleFieldSubject:        "contains notContains is isNot startsWith endsWith",
		api.RuleFieldListID:         "contains notContains is isNot startsWith endsWith",
		api.RuleFieldBulk:           "is isNot",
		api.RuleFieldHasAttachments: "is",
	}
	for f, want := range cases {
		var got []string
		for _, op := range OperatorsFor(f) {
			got = append(got, string(op))
		}
		if strings.Join(got, " ") != want {
			t.Errorf("%s: %v", f, got)
		}
	}
}

func TestNewCondition(t *testing.T) {
	if c := NewCondition(api.RuleFieldFrom, nil); c != (api.RuleCondition{Field: "from", Op: "contains"}) {
		t.Errorf("new: %+v", c)
	}
	old := api.RuleCondition{Field: api.RuleFieldFrom, Op: api.RuleOpDomainIs, Value: "acme.cz"}
	// Subject has no domainIs: the first operator, the text kept.
	if c := NewCondition(api.RuleFieldSubject, &old); c != (api.RuleCondition{Field: "subject", Op: "contains", Value: "acme.cz"}) {
		t.Errorf("to subject: %+v", c)
	}
	old.Op = api.RuleOpIsNot
	if c := NewCondition(api.RuleFieldBulk, &old); c != (api.RuleCondition{Field: "bulk", Op: "isNot", Value: "any"}) {
		t.Errorf("to bulk: %+v", c)
	}
	bulk := api.RuleCondition{Field: api.RuleFieldBulk, Op: api.RuleOpIs, Value: "list"}
	if c := NewCondition(api.RuleFieldTo, &bulk); c != (api.RuleCondition{Field: "to", Op: "is"}) {
		t.Errorf("from bulk: %+v", c)
	}
	if c := NewCondition(api.RuleFieldHasAttachments, nil); c.Value != ValueTrue {
		t.Errorf("attachments: %+v", c)
	}
}

func valid() Form {
	f := NewForm("acc")
	f.Name = " News "
	f.Conditions[0].Value = " acme.cz "
	f.Actions = []api.RuleAction{{Kind: api.RuleActionMove, FolderID: "f_news", To: "stale"}, {Kind: api.RuleActionMarkRead}}
	return f
}

func TestInput(t *testing.T) {
	in := valid().Input()
	if in.Name != "News" || in.Conditions[0].Value != "acme.cz" {
		t.Errorf("not trimmed: %+v", in)
	}
	if in.Actions[0] != (api.RuleAction{Kind: api.RuleActionMove, FolderID: "f_news"}) {
		t.Errorf("other fields kept: %+v", in.Actions[0])
	}
	f := valid()
	f.Actions = []api.RuleAction{{Kind: api.RuleActionAutoReply, Subject: " s ", Text: "Hi\n\n", FolderID: "x"}}
	if a := f.Input().Actions[0]; a != (api.RuleAction{Kind: api.RuleActionAutoReply, Subject: "s", Text: "Hi"}) {
		t.Errorf("reply: %+v", a)
	}
	// The form's own slices are not touched.
	f = valid()
	_ = f.Input()
	if f.Name != " News " || f.Conditions[0].Value != " acme.cz " {
		t.Error("Input changed the form")
	}
}

func kinds(ps []Problem) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Field+":"+strings.Repeat("i", p.Index)+":"+ProblemText(p, tr))
	}
	return strings.Join(out, " | ")
}

func TestValidate(t *testing.T) {
	if ps := Validate(valid()); len(ps) != 0 {
		t.Fatalf("valid form: %s", kinds(ps))
	}
	cases := []struct {
		name string
		edit func(*Form)
		want []Problem
	}{
		{"no name", func(f *Form) { f.Name = "  " }, []Problem{{"name", 0, ProblemNameMissing}}},
		{"long name", func(f *Form) { f.Name = strings.Repeat("x", api.MaxRuleNameBytes+1) }, []Problem{{"name", 0, ProblemTooLong}}},
		{"bidi in name", func(f *Form) { f.Name = "a‮b" }, []Problem{{"name", 0, ProblemBadCharacters}}},
		{"newline in value", func(f *Form) { f.Conditions[0].Value = "a\nb" }, []Problem{{"conditions", 0, ProblemBadCharacters}}},
		{"no condition", func(f *Form) { f.Conditions = nil }, []Problem{{"conditions", 0, ProblemNoCondition}}},
		{"empty value", func(f *Form) {
			f.Conditions = append(f.Conditions, api.RuleCondition{Field: api.RuleFieldSubject, Op: api.RuleOpIs, Value: " "})
		}, []Problem{{"conditions", 1, ProblemValueMissing}}},
		{"domain of a subject", func(f *Form) { f.Conditions[0].Field = api.RuleFieldSubject; f.Conditions[0].Op = api.RuleOpDomainIs },
			[]Problem{{"conditions", 0, ProblemBadOperator}}},
		{"bad bulk", func(f *Form) {
			f.Conditions[0] = api.RuleCondition{Field: api.RuleFieldBulk, Op: api.RuleOpIs, Value: "spam"}
		}, []Problem{{"conditions", 0, ProblemValueMissing}}},
		{"no action", func(f *Form) { f.Actions = nil }, []Problem{{"actions", 0, ProblemNoAction}}},
		{"twice", func(f *Form) { f.Actions = append(f.Actions, api.RuleAction{Kind: api.RuleActionMarkRead}) },
			[]Problem{{"actions", 2, ProblemActionTwice}}},
		{"two places", func(f *Form) { f.Actions = append(f.Actions, api.RuleAction{Kind: api.RuleActionTrash}) },
			[]Problem{{"actions", 2, ProblemOnePlace}}},
		{"move for every account", func(f *Form) { f.AccountID = "" }, []Problem{{"actions", 0, ProblemMoveNeedsAccount}}},
		{"move nowhere", func(f *Form) { f.Actions[0].FolderID = "" }, []Problem{{"actions", 0, ProblemFolderMissing}}},
		{"forward to a list", func(f *Form) {
			f.Actions = []api.RuleAction{{Kind: api.RuleActionForward, To: "a@b.cz, c@d.cz"}}
		}, []Problem{{"actions", 0, ProblemAddressInvalid}}},
		{"empty reply", func(f *Form) { f.Actions = []api.RuleAction{{Kind: api.RuleActionAutoReply, Text: " \n"}} },
			[]Problem{{"actions", 0, ProblemReplyMissing}}},
		{"reply with tabs and lines", func(f *Form) {
			f.Actions = []api.RuleAction{{Kind: api.RuleActionAutoReply, Text: "Hi\r\n\tthere"}}
		}, nil},
		{"reply with a bidi override", func(f *Form) {
			f.Actions = []api.RuleAction{{Kind: api.RuleActionAutoReply, Text: "Hi ‮"}}
		}, []Problem{{"actions", 0, ProblemBadCharacters}}},
	}
	for _, c := range cases {
		f := valid()
		c.edit(&f)
		if got := Validate(f); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %s, want %s", c.name, kinds(got), kinds(c.want))
		}
	}
}

func TestValidAddress(t *testing.T) {
	for s, want := range map[string]bool{
		"a@b.cz": true, " a.b+c@mail.example.com ": true,
		"": false, "a": false, "a@b": false, "@b.cz": false, "a@": false, "a@@b.cz": false,
		"A <a@b.cz>": false, "a@b..cz": false, "a@.b.cz": false, "a b@c.cz": false, "a@b.cz‮": false,
	} {
		if ValidAddress(s) != want {
			t.Errorf("%q: %v", s, !want)
		}
	}
}

func TestAvailableActions(t *testing.T) {
	join := func(ks []api.RuleActionKind) string {
		var s []string
		for _, k := range ks {
			s = append(s, string(k))
		}
		return strings.Join(s, " ")
	}
	f := NewForm("acc")
	if got := join(AvailableActions(f)); got != "markRead flag forward autoReply move junk trash" {
		t.Errorf("new: %s", got)
	}
	f.AccountID = ""
	if got := join(AvailableActions(f)); got != "markRead flag forward autoReply junk trash" {
		t.Errorf("every account: %s", got)
	}
	f.Actions = []api.RuleAction{{Kind: api.RuleActionJunk}, {Kind: api.RuleActionFlag}}
	if got := join(AvailableActions(f)); got != "markRead forward autoReply" {
		t.Errorf("placed: %s", got)
	}
}

func TestFromMessage(t *testing.T) {
	m := api.MessageSummary{From: []api.Address{{Name: "Acme News", Address: "news@acme.cz"}}}
	f := FromMessage(m, "acc", tr)
	if f.Name != "Messages from Acme News" || f.AccountID != "acc" || !f.Enabled ||
		!reflect.DeepEqual(f.Conditions, []api.RuleCondition{{Field: "from", Op: "is", Value: "news@acme.cz"}}) ||
		!reflect.DeepEqual(f.Actions, []api.RuleAction{{Kind: api.RuleActionMove}}) {
		t.Errorf("sender: %+v", f)
	}
	m.Bulk = &api.BulkInfo{Kind: api.BulkList, ListID: "dev.lists.acme.cz"}
	f = FromMessage(m, "acc", tr)
	if !reflect.DeepEqual(f.Conditions, []api.RuleCondition{{Field: "listId", Op: "is", Value: "dev.lists.acme.cz"}}) {
		t.Errorf("list: %+v", f.Conditions)
	}
	// A name with a bidi override is not used; the address is.
	m = api.MessageSummary{From: []api.Address{{Name: "evil‮", Address: "x@y.cz"}}}
	if f = FromMessage(m, "", tr); f.Name != "Messages from x@y.cz" || len(f.Actions) != 0 {
		t.Errorf("bidi name: %+v", f)
	}
	// Without a sender: the empty From condition of a new rule.
	if f = FromMessage(api.MessageSummary{}, "acc", tr); f.Name != "" || f.Conditions[0].Value != "" {
		t.Errorf("no sender: %+v", f)
	}
	// A long name is cut on a rune boundary.
	m = api.MessageSummary{From: []api.Address{{Name: strings.Repeat("ž", 200), Address: "a@b.cz"}}}
	if f = FromMessage(m, "acc", tr); len(f.Name) > api.MaxRuleNameBytes || !Clean(f.Name) {
		t.Errorf("long name: %d %q", len(f.Name), f.Name)
	}
}

func TestNeedsConfirm(t *testing.T) {
	fwd := api.RuleInput{Enabled: true, Actions: []api.RuleAction{{Kind: api.RuleActionForward, To: "a@b.cz"}}}
	local := api.RuleInput{Enabled: true, Actions: []api.RuleAction{{Kind: api.RuleActionMarkRead}}}
	if NeedsConfirm(nil, local) || !NeedsConfirm(nil, fwd) {
		t.Error("new rule")
	}
	off := fwd
	off.Enabled = false
	if NeedsConfirm(nil, off) {
		t.Error("disabled")
	}
	stored := &api.Rule{RuleInput: fwd}
	if NeedsConfirm(stored, fwd) {
		t.Error("unchanged")
	}
	renamed := fwd
	renamed.Name = "x"
	if NeedsConfirm(stored, renamed) {
		t.Error("renamed")
	}
	moved := api.RuleInput{Enabled: true, Actions: []api.RuleAction{{Kind: api.RuleActionForward, To: "c@d.cz"}}}
	if !NeedsConfirm(stored, moved) {
		t.Error("other address")
	}
	if !NeedsConfirm(&api.Rule{RuleInput: off}, fwd) {
		t.Error("turned on")
	}
	if !NeedsConfirm(&api.Rule{RuleInput: local}, fwd) {
		t.Error("became outgoing")
	}
	stored.Enabled = false
	if !NeedsConfirm(stored, Toggled(*stored)) {
		t.Error("switch on")
	}
}

func TestMoved(t *testing.T) {
	ids := []string{"a", "b", "c", "d"}
	cases := []struct {
		from, to int
		want     string
	}{
		{0, 3, "b c d a"}, {3, 0, "d a b c"}, {1, 2, "a c b d"}, {2, 2, "a b c d"}, {-1, 0, "a b c d"}, {0, 4, "a b c d"},
	}
	for _, c := range cases {
		if got := strings.Join(Moved(ids, c.from, c.to), " "); got != c.want {
			t.Errorf("%d→%d: %s", c.from, c.to, got)
		}
	}
	if strings.Join(ids, "") != "abcd" {
		t.Error("Moved changed its input")
	}
}

func TestAppliesAndOffers(t *testing.T) {
	rules := []api.Rule{{RuleInput: api.RuleInput{AccountID: "a"}}, {RuleInput: api.RuleInput{AccountID: "b", Enabled: true}}}
	if AppliesToAccount(rules, "a") || !AppliesToAccount(rules, "b") {
		t.Error("own rules")
	}
	rules = append(rules, api.Rule{RuleInput: api.RuleInput{Enabled: true}})
	if !AppliesToAccount(rules, "a") {
		t.Error("rule for every account")
	}
	if !CanApplyIn(api.Folder{Selectable: true, Role: api.RoleInbox}) || CanApplyIn(api.Folder{Selectable: true, Role: api.RoleOutbox}) ||
		CanApplyIn(api.Folder{Role: api.RoleNone}) || CanApplyIn(api.Folder{Selectable: true, Virtual: "open"}) {
		t.Error("CanApplyIn")
	}
	if !CanCreateFrom(api.RoleInbox) || !CanCreateFrom(api.RoleJunk) || CanCreateFrom(api.RoleSent) || CanCreateFrom(api.RoleDrafts) {
		t.Error("CanCreateFrom")
	}
	folders := []api.Folder{
		{ID: "in", Selectable: true, Role: api.RoleInbox}, {ID: "box", Role: api.RoleNone},
		{ID: "out", Selectable: true, Role: api.RoleOutbox}, {ID: "all", Selectable: true, Role: api.RoleAll},
		{ID: "news", Selectable: true}, {ID: "v", Selectable: true, Virtual: "open"},
	}
	var got []string
	for _, f := range MoveFolders(folders) {
		got = append(got, string(f.ID))
	}
	if strings.Join(got, " ") != "in news" {
		t.Errorf("MoveFolders: %v", got)
	}
}

func TestSummaries(t *testing.T) {
	names := Names{Folder: func(_ api.AccountID, id api.FolderID) string {
		if id == "f_news" {
			return "News"
		}
		return ""
	}}
	in := api.RuleInput{Match: api.RuleMatchAny, Stop: true,
		Conditions: []api.RuleCondition{{Field: api.RuleFieldFrom, Op: api.RuleOpDomainIs, Value: "acme.cz"},
			{Field: api.RuleFieldBulk, Op: api.RuleOpIs, Value: "newsletter"}},
		Actions: []api.RuleAction{{Kind: api.RuleActionMove, FolderID: "f_news"}, {Kind: api.RuleActionMarkRead}}}
	if got := ConditionsLine(in, tr); got != "From is from domain “acme.cz” or Bulk mail is newsletter" {
		t.Errorf("conditions: %s", got)
	}
	if got := ActionsLine(in, "acc", names, tr); got != "mark as read, move to “News”, stop processing more rules" {
		t.Errorf("actions: %s", got)
	}
	in.Actions[0].FolderID = "gone"
	if got := ActionsLine(in, "acc", names, tr); !strings.Contains(got, "move to a missing folder") {
		t.Errorf("gone folder: %s", got)
	}
	at := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	r := api.Rule{ID: "r_1", RuleInput: in, Applied: 2, LastAppliedAt: &at, Skipped: 1, Problem: api.RuleProblemFolderGone, Outgoing: true}
	rows := Rows([]api.Rule{r}, names, func(time.Time) string { return "today" }, tr)
	if len(rows) != 1 || rows[0].Status != "Used on 2 messages, last today · 1 outgoing message held back" ||
		rows[0].Scope != "All Mail Accounts" || rows[0].Problem == "" || !rows[0].Outgoing {
		t.Errorf("row: %+v", rows)
	}
	if ScopeText("acc", Names{Account: func(api.AccountID) string { return "" }}, tr) != "Removed account" {
		t.Error("removed account")
	}
	q := OutgoingQuestion(api.RuleInput{Actions: []api.RuleAction{{Kind: api.RuleActionForward, To: "boss@acme.cz"}}}, tr)
	if !strings.Contains(q.Body, "boss@acme.cz") || q.Confirm != "Turn On" {
		t.Errorf("outgoing question: %+v", q)
	}
	if got := ApplyResultText(api.RulesApplyResult{Changed: 1}, tr); got != "The rules changed 1 message." {
		t.Errorf("apply: %s", got)
	}
}
