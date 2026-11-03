// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package rulesctl

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/rules"
)

// ctlFixture is a started controller over mailFixture; answer decides
// the reply to every question (nil: not asked), toasts collects the
// sentences.
type ctlFixture struct {
	d      *daemon
	l      *loop
	c      *Controller
	asked  []rules.Question
	answer func(rules.Question) bool
	toasts []string
	busy   []string
	shown  int
}

func newCtl(t *testing.T) *ctlFixture {
	t.Helper()
	f := &ctlFixture{d: mailFixture(), l: &loop{}}
	f.d.on(api.MethodRulesUpdate, func(raw json.RawMessage) (any, error) {
		var p api.RulesUpdateParams
		_ = json.Unmarshal(raw, &p)
		return api.RulesUpdateResult{Rule: api.Rule{ID: p.ID, RuleInput: p.Rule, Outgoing: rules.IsOutgoing(p.Rule)}}, nil
	})
	f.c = NewController(options(f.d, f.l))
	f.c.OnChange = func() { f.shown++ }
	f.c.OnToast = func(s string) { f.toasts = append(f.toasts, s) }
	f.c.OnBusy = func(s string) { f.busy = append(f.busy, s) }
	f.c.OnQuestion = func(q rules.Question, answer func(bool)) {
		f.asked = append(f.asked, q)
		answer(f.answer != nil && f.answer(q))
	}
	f.c.Start()
	f.l.run()
	return f
}

func TestControllerLoadsRulesAndNamesRows(t *testing.T) {
	f := newCtl(t)
	if !f.c.Loaded() || !f.c.Available() || len(f.c.Rules()) != 3 {
		t.Fatalf("loaded %v rules %d", f.c.Loaded(), len(f.c.Rules()))
	}
	rows := f.c.Rows()
	if rows[0].Title != "Newsletters" || rows[0].Scope != "Work" || rows[2].Scope != "All Mail Accounts" {
		t.Errorf("rows %+v", rows)
	}
	if !rows[1].Outgoing || rows[0].Outgoing {
		t.Error("outgoing flags")
	}
	// The directory holds the mail accounts only.
	if got := len(f.c.Dir.Accounts()); got != 2 {
		t.Errorf("%d accounts in the directory", got)
	}
	if f.d.count(api.MethodFolderList) != 2 {
		t.Errorf("folder.list called %d times", f.d.count(api.MethodFolderList))
	}
}

func TestControllerReloadsOnNotification(t *testing.T) {
	f := newCtl(t)
	f.d.answer(api.MethodRulesList, api.RulesListResult{})
	f.c.Handle(api.NotifyRulesChanged, nil)
	f.l.run()
	if len(f.c.Rules()) != 0 {
		t.Errorf("%d rules after the notification", len(f.c.Rules()))
	}
	n := f.d.count(api.MethodAccountList)
	f.c.Handle(api.NotifyAccountsChanged, nil)
	f.l.run()
	if f.d.count(api.MethodAccountList) != n+1 {
		t.Error("account.list not asked again")
	}
	f.c.Handle("notify.other", nil)
	f.l.run()
	if f.d.count(api.MethodRulesList) != 2 {
		t.Errorf("rules.list called %d times", f.d.count(api.MethodRulesList))
	}
}

func TestControllerWithoutRulesInTheDaemon(t *testing.T) {
	d, l := newDaemon(), &loop{}
	c := NewController(options(d, l))
	c.Refresh()
	l.run()
	if c.Available() || c.Loaded() || c.Error() != "" {
		t.Errorf("available %v loaded %v error %q", c.Available(), c.Loaded(), c.Error())
	}
	d.fail(api.MethodRulesList, &api.Error{Code: api.CodeInternalError})
	c.Refresh()
	l.run()
	if !c.Available() || c.Error() != "Rules failed" {
		t.Errorf("available %v error %q", c.Available(), c.Error())
	}
}

func TestToggleSendsTheFlippedRule(t *testing.T) {
	f := newCtl(t)
	f.c.Toggle("r1", false)
	f.l.run()
	var p api.RulesUpdateParams
	f.d.last(t, api.MethodRulesUpdate, &p)
	if p.ID != "r1" || p.Rule.Enabled || p.ConfirmOutgoing || len(f.asked) != 0 {
		t.Errorf("update %+v asked %d", p, len(f.asked))
	}
	if r, _ := f.c.Rule("r1"); r.Enabled {
		t.Error("still enabled")
	}
	if f.c.Busy("r1") {
		t.Error("still busy")
	}
}

func TestToggleOnAnOutgoingRuleAsksFirst(t *testing.T) {
	f := newCtl(t)
	f.c.Toggle("r2", true)
	f.l.run()
	if len(f.asked) != 1 || f.asked[0].Heading != "Send Mail Automatically?" || f.d.count(api.MethodRulesUpdate) != 0 {
		t.Fatalf("asked %+v updates %d", f.asked, f.d.count(api.MethodRulesUpdate))
	}
	if r, _ := f.c.Rule("r2"); r.Enabled {
		t.Error("turned on without the answer")
	}
	f.answer = func(rules.Question) bool { return true }
	f.c.Toggle("r2", true)
	f.l.run()
	var p api.RulesUpdateParams
	f.d.last(t, api.MethodRulesUpdate, &p)
	if !p.ConfirmOutgoing || !p.Rule.Enabled || p.Rule.Actions[0].To != "a@b.cz" {
		t.Errorf("update %+v", p)
	}
	if r, _ := f.c.Rule("r2"); !r.Enabled {
		t.Error("not turned on")
	}
	// Turning it off needs no question.
	f.asked = nil
	f.c.Toggle("r2", false)
	f.l.run()
	if len(f.asked) != 0 {
		t.Error("asked to turn off")
	}
}

func TestToggleRefusedPutsTheSwitchBack(t *testing.T) {
	f := newCtl(t)
	f.d.fail(api.MethodRulesUpdate, &api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"field": "actions", "index": 0}})
	f.c.Toggle("r1", false)
	f.l.run()
	if r, _ := f.c.Rule("r1"); !r.Enabled {
		t.Error("not put back")
	}
	if len(f.toasts) != 1 || f.toasts[0] != "An action of this rule cannot be used." {
		t.Errorf("toasts %v", f.toasts)
	}
	// A rule another client removed.
	f.d.fail(api.MethodRulesUpdate, &api.Error{Code: api.CodeRuleNotFound})
	f.d.answer(api.MethodRulesList, api.RulesListResult{})
	f.c.Toggle("r3", false)
	f.l.run()
	if f.toasts[len(f.toasts)-1] != "This rule no longer exists." || len(f.c.Rules()) != 0 {
		t.Errorf("toasts %v rules %d", f.toasts, len(f.c.Rules()))
	}
}

func TestMoveReordersAtOnceAndSavesTheWholeOrder(t *testing.T) {
	f := newCtl(t)
	f.d.on(api.MethodRulesReorder, func(raw json.RawMessage) (any, error) {
		var p api.RulesReorderParams
		_ = json.Unmarshal(raw, &p)
		var out []api.Rule
		for _, id := range p.IDs {
			r, _ := f.c.Rule(id)
			out = append(out, r)
		}
		return api.RulesReorderResult{Rules: out}, nil
	})
	f.c.Move(0, 2)
	if got := f.c.IDs(); !slices.Equal(got, []string{"r2", "r3", "r1"}) {
		t.Fatalf("shown order %v before the answer", got)
	}
	f.l.run()
	var p api.RulesReorderParams
	f.d.last(t, api.MethodRulesReorder, &p)
	if !slices.Equal(p.IDs, []string{"r2", "r3", "r1"}) {
		t.Errorf("sent %v", p.IDs)
	}
	if !f.c.MoveBy("r1", -1) || !slices.Equal(f.c.IDs(), []string{"r2", "r1", "r3"}) {
		t.Errorf("MoveBy: %v", f.c.IDs())
	}
	if f.c.MoveBy("r2", -1) || f.c.MoveBy("r3", 1) || f.c.MoveBy("nope", 1) {
		t.Error("moved past the ends or an unknown rule")
	}
	f.l.run()
}

func TestMoveRefusedGoesBack(t *testing.T) {
	f := newCtl(t)
	f.d.fail(api.MethodRulesReorder, &api.Error{Code: api.CodeInternalError})
	f.c.Move(2, 0)
	f.l.run()
	if got := f.c.IDs(); !slices.Equal(got, []string{"r1", "r2", "r3"}) {
		t.Errorf("order %v", got)
	}
	if len(f.toasts) != 1 || f.toasts[0] != "The rule could not be saved." {
		t.Errorf("toasts %v", f.toasts)
	}
}

func TestRemoveAsksThenDeletes(t *testing.T) {
	f := newCtl(t)
	f.d.answer(api.MethodRulesDelete, api.RulesDeleteResult{})
	f.c.Remove("r1")
	f.l.run()
	if len(f.asked) != 1 || f.asked[0].Heading != "Remove the Rule “Newsletters”?" || !f.asked[0].Destructive {
		t.Fatalf("asked %+v", f.asked)
	}
	if f.d.count(api.MethodRulesDelete) != 0 || len(f.c.Rules()) != 3 {
		t.Error("deleted without the answer")
	}
	f.answer = func(rules.Question) bool { return true }
	f.c.Remove("r1")
	f.l.run()
	var p api.RulesDeleteParams
	f.d.last(t, api.MethodRulesDelete, &p)
	if p.ID != "r1" || len(f.c.Rules()) != 2 {
		t.Errorf("delete %+v, %d rules left", p, len(f.c.Rules()))
	}
	f.d.fail(api.MethodRulesDelete, &api.Error{Code: api.CodeRuleNotFound})
	f.c.Remove("r2")
	f.l.run()
	if f.toasts[len(f.toasts)-1] != "This rule no longer exists." {
		t.Errorf("toasts %v", f.toasts)
	}
}

func TestApplyAsksRunsWithProgressAndToastsTheResult(t *testing.T) {
	f := newCtl(t)
	f.d.answer(api.MethodRulesApply, api.RulesApplyResult{Examined: 10, Matched: 3, Changed: 2})
	f.c.Apply("acc1", "acc1i", "INBOX")
	f.l.run()
	if len(f.asked) != 1 || !strings.Contains(f.asked[0].Heading, "INBOX") || f.d.count(api.MethodRulesApply) != 0 {
		t.Fatalf("asked %+v", f.asked)
	}
	f.answer = func(rules.Question) bool { return true }
	f.c.Apply("acc1", "acc1i", "INBOX")
	if !f.c.Applying() || !slices.Equal(f.busy, []string{"Applying rules"}) {
		t.Errorf("applying %v busy %v", f.c.Applying(), f.busy)
	}
	f.l.run()
	var p api.RulesApplyParams
	f.d.last(t, api.MethodRulesApply, &p)
	if p.AccountID != "acc1" || p.FolderID != "acc1i" || len(p.RuleIDs) != 0 || len(p.MessageIDs) != 0 {
		t.Errorf("apply %+v", p)
	}
	if f.c.Applying() || !slices.Equal(f.busy, []string{"Applying rules", ""}) {
		t.Errorf("applying %v busy %v", f.c.Applying(), f.busy)
	}
	if len(f.toasts) != 1 || f.toasts[0] != "The rules changed 2 messages." {
		t.Errorf("toasts %v", f.toasts)
	}
	f.d.fail(api.MethodRulesApply, &api.Error{Code: api.CodeInternalError})
	f.c.Apply("acc1", "acc1i", "INBOX")
	f.l.run()
	if f.toasts[len(f.toasts)-1] != "The rules could not be applied." {
		t.Errorf("toasts %v", f.toasts)
	}
}

func TestAppliesToTheAccountsEnabledRules(t *testing.T) {
	f := newCtl(t)
	if !f.c.AppliesTo("acc1") || !f.c.AppliesTo("acc2") { // r3 is for every account
		t.Error("an enabled rule for every account applies everywhere")
	}
	f.c.Toggle("r3", false)
	f.l.run()
	if !f.c.AppliesTo("acc1") || f.c.AppliesTo("acc2") {
		t.Error("only acc1 has an enabled rule left")
	}
}

func TestStopDropsAnswers(t *testing.T) {
	d, l := mailFixture(), &loop{}
	c := NewController(options(d, l))
	n := 0
	c.OnChange = func() { n++ }
	c.Start()
	c.Stop()
	l.run()
	if n != 0 || c.Loaded() {
		t.Errorf("%d changes after Stop, loaded %v", n, c.Loaded())
	}
}

func TestSaveErrorText(t *testing.T) {
	tr := en{}
	cases := []struct {
		e    *api.Error
		want string
	}{
		{nil, ""},
		{&api.Error{Code: api.CodeRuleNotFound}, "This rule no longer exists."},
		{&api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"field": "name"}}, "Enter a name for the rule."},
		{&api.Error{Code: api.CodeInvalidArgument, Data: api.RuleInvalidData{Field: "confirmOutgoing"}}, "Rules that send mail need your confirmation."},
		{&api.Error{Code: api.CodeInvalidArgument}, "The rule could not be saved."},
		{&api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"index": 1}}, "The rule could not be saved."},
		{&api.Error{Code: api.CodeUnavailable}, "The rule could not be saved."},
	}
	for _, c := range cases {
		if got := SaveErrorText(c.e, tr); got != c.want {
			t.Errorf("%+v: %q, want %q", c.e, got, c.want)
		}
	}
}

func TestSamplesAnswerTheWholeFlow(t *testing.T) {
	l := &loop{}
	s := NewSampleCaller(testNow)
	opts := Options{Caller: s, Loop: l, Tr: en{}, Spawn: func(f func()) { f() }}
	c := NewController(opts)
	var notified int
	s.Notify = func(method string, _ json.RawMessage) {
		if RulesChanged(method) {
			notified++
		}
	}
	c.OnQuestion = func(_ rules.Question, answer func(bool)) { answer(true) }
	c.Start()
	l.run()
	rows := c.Rows()
	if len(rows) != 4 || rows[0].Actions == "" || rows[2].Problem == "" || rows[3].Problem == "" || !rows[1].Outgoing {
		t.Fatalf("rows %+v", rows)
	}
	if rows[0].Scope != "Práce" || rows[3].Scope != "Doma" {
		t.Errorf("scopes %q %q", rows[0].Scope, rows[3].Scope)
	}
	// An outgoing rule turned on needs the question and the confirmation.
	c.Toggle(rows[2].ID, true)
	l.run()
	if r, _ := c.Rule(rows[2].ID); !r.Enabled || notified == 0 {
		t.Errorf("enabled %v notified %d", r.Enabled, notified)
	}
	c.Remove(rows[3].ID)
	l.run()
	if len(c.Rules()) != 3 {
		t.Errorf("%d rules", len(c.Rules()))
	}
}
