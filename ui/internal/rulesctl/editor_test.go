// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package rulesctl

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/rules"
)

var testNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

type edFixture struct {
	d      *daemon
	l      *loop
	dir    *Directory
	e      *Editor
	asked  []rules.Question
	answer bool
	saved  []api.Rule
	shown  int
}

func newEditor(t *testing.T, form rules.Form, old *api.Rule) *edFixture {
	t.Helper()
	f := &edFixture{d: mailFixture(), l: &loop{}}
	opts := options(f.d, f.l)
	f.dir = NewDirectory(opts)
	f.dir.Load()
	f.l.run()
	f.d.on(api.MethodRulesCreate, func(raw json.RawMessage) (any, error) {
		var p api.RulesCreateParams
		_ = json.Unmarshal(raw, &p)
		return api.RulesCreateResult{Rule: api.Rule{ID: "new", RuleInput: p.Rule}}, nil
	})
	f.d.on(api.MethodRulesUpdate, func(raw json.RawMessage) (any, error) {
		var p api.RulesUpdateParams
		_ = json.Unmarshal(raw, &p)
		return api.RulesUpdateResult{Rule: api.Rule{ID: p.ID, RuleInput: p.Rule}}, nil
	})
	f.e = NewEditor(opts, f.dir, form, old)
	f.e.OnChange = func() { f.shown++ }
	f.e.OnSaved = func(r api.Rule) { f.saved = append(f.saved, r) }
	f.e.OnQuestion = func(q rules.Question, answer func(bool)) {
		f.asked = append(f.asked, q)
		answer(f.answer)
	}
	return f
}

// fill makes the new rule of the fixture valid: a name, a value, one action.
func fill(e *Editor) {
	e.SetName(" Bills ")
	e.SetConditionValue(0, "billing@example.com")
	e.AddAction(api.RuleActionMarkRead)
}

func TestBlankFormHidesItsProblemsUntilEdited(t *testing.T) {
	f := newEditor(t, rules.NewForm("acc1"), nil)
	if f.e.CanSave() || len(f.e.Problems()) == 0 {
		t.Fatal("a blank rule can be saved")
	}
	if f.e.NameProblem() != "" || f.e.ConditionProblem(0) != "" || f.e.ActionsProblem() != "" {
		t.Error("problems shown in a blank form")
	}
	f.e.SetName("")
	if f.e.NameProblem() != "" {
		t.Error("an unchanged name counts as an edit")
	}
	f.e.SetName("x")
	f.e.SetName("")
	if f.e.NameProblem() != "Enter a name for the rule." || f.e.ConditionProblem(0) != "Enter a value." ||
		f.e.ActionsProblem() != "Add at least one action." {
		t.Errorf("problems %q %q %q", f.e.NameProblem(), f.e.ConditionProblem(0), f.e.ActionsProblem())
	}
	if f.e.Title() != "New Rule" {
		t.Errorf("title %q", f.e.Title())
	}
}

func TestPrefilledFormShowsItsProblemsAtOnce(t *testing.T) {
	m := api.MessageSummary{From: []api.Address{{Name: "Billing", Address: "billing@example.com"}}}
	f := newEditor(t, rules.FromMessage(m, "acc1", en{}), nil)
	if f.e.Form().Name != "Messages from Billing" {
		t.Errorf("name %q", f.e.Form().Name)
	}
	if got := f.e.ActionProblem(0); got != "Choose a folder." {
		t.Errorf("action problem %q", got)
	}
	if f.e.CanSave() {
		t.Error("saved without a folder")
	}
	folders := f.e.MoveFolders()
	if len(folders) != 3 { // INBOX, Archive, Invoices: not the outbox and drafts
		t.Fatalf("%d folders to move to", len(folders))
	}
	f.e.SetActionFolder(0, folders[2].ID)
	if !f.e.CanSave() || f.e.ActionProblem(0) != "" {
		t.Errorf("problems %v", f.e.Problems())
	}
}

func TestConditionRowsKeepWhatTheNewFieldTakes(t *testing.T) {
	f := newEditor(t, rules.NewForm("acc1"), nil)
	f.e.SetConditionOp(0, api.RuleOpStartsWith)
	f.e.SetConditionValue(0, "abc")
	f.e.SetConditionField(0, api.RuleFieldSubject)
	if c := f.e.Form().Conditions[0]; c.Field != api.RuleFieldSubject || c.Op != api.RuleOpStartsWith || c.Value != "abc" {
		t.Errorf("subject condition %+v", c)
	}
	f.e.SetConditionField(0, api.RuleFieldBulk)
	if c := f.e.Form().Conditions[0]; c.Op != api.RuleOpIs || c.Value != api.RuleBulkAny {
		t.Errorf("bulk condition %+v", c)
	}
	f.e.AddCondition()
	f.e.AddCondition()
	f.e.RemoveCondition(1)
	f.e.RemoveCondition(9)
	if len(f.e.Form().Conditions) != 2 {
		t.Errorf("%d conditions", len(f.e.Form().Conditions))
	}
	for range api.MaxRuleConditions {
		f.e.AddCondition()
	}
	if len(f.e.Form().Conditions) != api.MaxRuleConditions {
		t.Errorf("%d conditions", len(f.e.Form().Conditions))
	}
}

func TestActionsFollowTheAccount(t *testing.T) {
	f := newEditor(t, rules.NewForm("acc1"), nil)
	f.e.AddAction(api.RuleActionMove)
	f.e.AddAction(api.RuleActionTrash) // one place only
	f.e.AddAction(api.RuleActionMove)  // each kind once
	f.e.AddAction(api.RuleActionFlag)
	if got := len(f.e.Form().Actions); got != 2 {
		t.Fatalf("%d actions", got)
	}
	f.e.SetActionFolder(0, "acc1x")
	f.e.SetAccount("acc2")
	if a := f.e.Form().Actions[0]; a.Kind != api.RuleActionMove || a.FolderID != "" {
		t.Errorf("move after changing the account %+v", a)
	}
	f.e.SetAccount("")
	for _, a := range f.e.Form().Actions {
		if a.Kind == api.RuleActionMove {
			t.Error("a move action left in a rule for every account")
		}
	}
	for _, k := range f.e.AvailableActions() {
		if k == api.RuleActionMove {
			t.Error("Move To offered without an account")
		}
	}
	f.e.RemoveAction(0)
	f.e.RemoveAction(5)
	if len(f.e.Form().Actions) != 0 {
		t.Errorf("%d actions", len(f.e.Form().Actions))
	}
}

func TestAccountChoices(t *testing.T) {
	f := newEditor(t, rules.NewForm("acc1"), nil)
	names := func() []string {
		var out []string
		for _, c := range f.e.AccountChoices() {
			out = append(out, c.Name)
		}
		return out
	}
	got := names()
	if len(got) != 3 || got[0] != "Work" || got[1] != "Home" || got[2] != "All Mail Accounts" {
		t.Errorf("choices %v", got)
	}
	f.e.SetAccount("gone")
	got = names()
	if len(got) != 4 || got[2] != "Removed account" || f.e.AccountChoices()[2].ID != "gone" {
		t.Errorf("choices with a removed account %v", got)
	}
}

func TestSaveCreatesTheTrimmedRule(t *testing.T) {
	f := newEditor(t, rules.NewForm("acc1"), nil)
	if f.e.Save(); f.d.count(api.MethodRulesCreate) != 0 {
		t.Fatal("saved a blank rule")
	}
	fill(f.e)
	f.e.Save()
	if !f.e.Saving() || f.e.CanSave() {
		t.Error("not saving")
	}
	f.l.run()
	var p api.RulesCreateParams
	f.d.last(t, api.MethodRulesCreate, &p)
	if p.Rule.Name != "Bills" || p.ConfirmOutgoing || len(f.asked) != 0 || len(f.saved) != 1 || f.saved[0].ID != "new" {
		t.Errorf("create %+v asked %d saved %v", p, len(f.asked), f.saved)
	}
	if f.e.Saving() {
		t.Error("still saving")
	}
}

func TestSaveOfAnOutgoingRuleAsksAndSendsTheConfirmation(t *testing.T) {
	f := newEditor(t, rules.NewForm("acc1"), nil)
	fill(f.e)
	f.e.AddAction(api.RuleActionForward)
	f.e.SetActionTo(1, "me@example.org")
	f.e.Save()
	f.l.run()
	if len(f.asked) != 1 || f.d.count(api.MethodRulesCreate) != 0 || f.e.Saving() {
		t.Fatalf("asked %d creates %d", len(f.asked), f.d.count(api.MethodRulesCreate))
	}
	f.answer = true
	f.e.Save()
	f.l.run()
	var p api.RulesCreateParams
	f.d.last(t, api.MethodRulesCreate, &p)
	if !p.ConfirmOutgoing || p.Rule.Actions[1].To != "me@example.org" {
		t.Errorf("create %+v", p)
	}
}

func TestSaveOfAStoredRuleAsksAgainOnlyWhenWhatItSendsChanges(t *testing.T) {
	old := storedRule("r2", "Forward bills", "acc1", true, api.RuleAction{Kind: api.RuleActionForward, To: "a@b.cz"})
	f := newEditor(t, rules.FromRule(old), &old)
	f.e.SetName("Forward invoices")
	f.e.Save()
	f.l.run()
	var p api.RulesUpdateParams
	f.d.last(t, api.MethodRulesUpdate, &p)
	if p.ID != "r2" || p.ConfirmOutgoing || len(f.asked) != 0 || f.d.count(api.MethodRulesCreate) != 0 {
		t.Errorf("update %+v asked %d", p, len(f.asked))
	}
	if f.e.Title() != "Edit Rule" {
		t.Errorf("title %q", f.e.Title())
	}
	f.e.SetActionTo(0, "other@b.cz")
	f.answer = true
	f.e.Save()
	f.l.run()
	f.d.last(t, api.MethodRulesUpdate, &p)
	if len(f.asked) != 1 || !p.ConfirmOutgoing || p.Rule.Actions[0].To != "other@b.cz" {
		t.Errorf("update %+v asked %d", p, len(f.asked))
	}
}

func TestSaveFailuresAreMappedToSentences(t *testing.T) {
	cases := []struct {
		e    *api.Error
		want string
	}{
		{&api.Error{Code: api.CodeRuleNotFound}, "This rule no longer exists."},
		{&api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"field": "accountId"}}, "The account of this rule no longer exists."},
		{&api.Error{Code: api.CodeInternalError}, "The rule could not be saved."},
	}
	for _, c := range cases {
		old := storedRule("r1", "n", "acc1", true, api.RuleAction{Kind: api.RuleActionFlag})
		f := newEditor(t, rules.FromRule(old), &old)
		f.d.fail(api.MethodRulesUpdate, c.e)
		f.e.SetName("renamed")
		f.e.Save()
		f.l.run()
		if f.e.SaveError() != c.want || f.e.Saving() || len(f.saved) != 0 || !f.e.CanSave() {
			t.Errorf("%+v: error %q saving %v", c.e, f.e.SaveError(), f.e.Saving())
		}
		f.e.SetName("again")
		if f.e.SaveError() != "" {
			t.Error("the error outlives an edit")
		}
	}
}

func TestTestChoicesFollowTheRulesAccount(t *testing.T) {
	f := newEditor(t, rules.NewForm("acc1"), nil)
	choices := f.e.TestChoices()
	// The inbox, the archive and Invoices: not the outbox and the drafts.
	if len(choices) != 3 || choices[0].Title != "INBOX" || choices[0].Account != "acc1" {
		t.Fatalf("choices %+v", choices)
	}
	if f.e.TestSelected() != 0 {
		t.Errorf("selected %d", f.e.TestSelected())
	}
	f.e.SetTest(2)
	if f.e.TestSelected() != 2 {
		t.Errorf("selected %d after SetTest", f.e.TestSelected())
	}
	f.e.SetAccount("")
	choices = f.e.TestChoices()
	if len(choices) != 6 || choices[0].Title != "Work · INBOX" || choices[3].Account != "acc2" {
		t.Fatalf("choices for every account %+v", choices)
	}
	// The choice (acc1, Invoices) is still on offer.
	if f.e.TestSelected() != 2 {
		t.Errorf("selected %d", f.e.TestSelected())
	}
	f.e.SetAccount("acc2")
	if f.e.TestSelected() != 0 || f.e.TestChoices()[0].Account != "acc2" {
		t.Errorf("selected %d", f.e.TestSelected())
	}
	f.e.SetAccount("gone")
	if f.e.TestSelected() != -1 || f.e.CanTest() {
		t.Error("a test without a folder")
	}
}

func TestTryItAsksTheDaemonAndShowsTheCount(t *testing.T) {
	f := newEditor(t, rules.NewForm("acc1"), nil)
	if f.e.CanTest() {
		t.Fatal("Try It is on for a blank rule")
	}
	fill(f.e)
	f.d.answer(api.MethodRulesTest, api.RulesTestResult{Count: 4})
	f.e.SetTest(1)
	f.e.Test()
	if !f.e.Testing() {
		t.Error("not testing")
	}
	f.l.run()
	var p api.RulesTestParams
	f.d.last(t, api.MethodRulesTest, &p)
	if p.AccountID != "acc1" || p.FolderID != "acc1a" || p.Rule.Name != "Bills" {
		t.Errorf("test %+v", p)
	}
	if f.e.TestText() != "4 messages in this folder match." || f.e.TestFailed() || f.e.Testing() {
		t.Errorf("text %q failed %v", f.e.TestText(), f.e.TestFailed())
	}
	f.e.SetConditionValue(0, "other")
	if f.e.TestText() != "" {
		t.Error("the result outlives an edit")
	}
	f.d.answer(api.MethodRulesTest, api.RulesTestResult{})
	f.e.Test()
	f.l.run()
	if f.e.TestText() != "No message in this folder matches." {
		t.Errorf("text %q", f.e.TestText())
	}
	f.d.fail(api.MethodRulesTest, &api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"field": "conditions"}})
	f.e.Test()
	f.l.run()
	if f.e.TestText() != "A condition of this rule cannot be used." || !f.e.TestFailed() {
		t.Errorf("text %q failed %v", f.e.TestText(), f.e.TestFailed())
	}
	f.d.fail(api.MethodRulesTest, &api.Error{Code: api.CodeUnavailable})
	f.e.Test()
	f.l.run()
	if f.e.TestText() != "Rules failed" {
		t.Errorf("text %q", f.e.TestText())
	}
}

func TestEditDuringTryItDropsTheAnswer(t *testing.T) {
	f := newEditor(t, rules.NewForm("acc1"), nil)
	fill(f.e)
	f.d.answer(api.MethodRulesTest, api.RulesTestResult{Count: 9})
	f.e.Test()
	f.e.SetName("renamed") // before the answer is delivered
	f.l.run()
	if f.e.TestText() != "" || f.e.Testing() {
		t.Errorf("text %q testing %v", f.e.TestText(), f.e.Testing())
	}
}

func TestEditorFollowsTheDirectoryAndStops(t *testing.T) {
	f := newEditor(t, rules.NewForm("acc1"), nil)
	n := f.shown
	f.dir.Seed(nil, nil)
	if f.shown != n+1 || len(f.e.AccountChoices()) != 2 { // the rule's own account is gone, plus All
		t.Errorf("changes %d choices %d", f.shown-n, len(f.e.AccountChoices()))
	}
	f.e.Close()
	f.dir.Seed(nil, nil)
	f.e.SetName("x")
	if f.shown != n+1 {
		t.Error("a closed editor still reports")
	}
}
