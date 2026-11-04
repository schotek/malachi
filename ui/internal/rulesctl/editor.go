// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package rulesctl

import (
	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/rules"
)

// Editor is the editor of one rule: the form (rules.Form), its problems
// (rules.Validate) under the rows that have them, Save (rules.create or
// rules.update, asking OutgoingQuestion first when rules.NeedsConfirm
// says so and sending confirmOutgoing only after the answer) and Try It
// (rules.test over a folder). The view reads the state after OnChange and
// never keeps a rule of its own.
//
// Problems stay out of sight in a blank new rule until the user changes
// something (a message's prefilled rule and a stored one show them at
// once), so the first look is not a list of complaints.
type Editor struct {
	Opts Options
	// Dir names the accounts and folders the choices offer.
	Dir *Directory

	// OnChange: the state changed; the view reads it anew.
	OnChange func()
	// OnQuestion asks the user and calls answer with the reply; without it
	// a rule that sends mail cannot be saved.
	OnQuestion func(q rules.Question, answer func(ok bool))
	// OnSaved: the daemon stored the rule; the view closes.
	OnSaved func(api.Rule)

	form    rules.Form
	old     *api.Rule
	shown   bool
	saving  bool
	saveErr string

	testKey  testKey
	testSet  bool
	testBusy bool
	testText string
	testFail bool
	testGen  int

	stopDir func()
	closed  bool
}

type testKey struct {
	account api.AccountID
	folder  api.FolderID
}

// NewEditor is the editor of form; old is the stored rule being edited
// (nil for a new one), against which a change of an outgoing action asks
// for the confirmation again.
func NewEditor(opts Options, dir *Directory, form rules.Form, old *api.Rule) *Editor {
	e := &Editor{Opts: opts, Dir: dir, form: form, old: old}
	e.shown = form.ID != "" || form.Name != "" || len(form.Actions) > 0
	if dir != nil {
		e.stopDir = dir.Listen(e.changed)
	}
	return e
}

// Close ends the editor: answers still on their way are dropped.
func (e *Editor) Close() {
	e.closed = true
	if e.stopDir != nil {
		e.stopDir()
		e.stopDir = nil
	}
}

func (e *Editor) changed() {
	if e.OnChange != nil && !e.closed {
		e.OnChange()
	}
}

// edited marks a change by the user: problems show, the old test result
// and save error are stale.
func (e *Editor) edited() {
	e.shown = true
	e.testText, e.testFail = "", false
	e.testGen++
	e.testBusy = false // an answer on its way is about another rule
	e.saveErr = ""
	e.changed()
}

// Form is the rule as edited.
func (e *Editor) Form() rules.Form { return e.form }

// Title is the editor's title.
func (e *Editor) Title() string { return rules.EditorTitle(e.form, e.Opts.Tr) }

// ---------------------------------------------------------------------------
// The rule's own fields
// ---------------------------------------------------------------------------

// SetName sets the rule's name.
func (e *Editor) SetName(s string) {
	if e.form.Name != s {
		e.form.Name = s
		e.edited()
	}
}

// SetEnabled sets Rule is on.
func (e *Editor) SetEnabled(on bool) {
	if e.form.Enabled != on {
		e.form.Enabled = on
		e.edited()
	}
}

// SetStop sets Stop processing more rules.
func (e *Editor) SetStop(on bool) {
	if e.form.Stop != on {
		e.form.Stop = on
		e.edited()
	}
}

// SetMatch sets how the conditions combine.
func (e *Editor) SetMatch(m api.RuleMatch) {
	if e.form.Match != m {
		e.form.Match = m
		e.edited()
	}
}

// AccountChoice is an entry of the Apply to choice.
type AccountChoice struct {
	// ID is the account, "" for every mail account.
	ID   api.AccountID
	Name string
}

// AccountChoices are the mail accounts, the rule's account if it is gone
// (its name says so), then All Mail Accounts.
func (e *Editor) AccountChoices() []AccountChoice {
	var out []AccountChoice
	has := false
	if e.Dir != nil {
		for _, a := range e.Dir.Accounts() {
			out = append(out, AccountChoice{ID: a.ID, Name: e.Opts.accountName(a)})
			has = has || a.ID == e.form.AccountID
		}
	}
	if e.form.AccountID != "" && !has {
		out = append(out, AccountChoice{ID: e.form.AccountID, Name: rules.ScopeText(e.form.AccountID, rules.Names{}, e.Opts.Tr)})
	}
	return append(out, AccountChoice{Name: rules.AllAccounts(e.Opts.Tr)})
}

// SetAccount chooses the account the rule is for: Move To actions lose
// their folder (and go with All Mail Accounts, which has none).
func (e *Editor) SetAccount(id api.AccountID) {
	if e.form.AccountID == id {
		return
	}
	e.form.AccountID = id
	var actions []api.RuleAction
	for _, a := range e.form.Actions {
		if a.Kind == api.RuleActionMove {
			if id == "" {
				continue
			}
			a.FolderID = ""
		}
		actions = append(actions, a)
	}
	e.form.Actions = actions
	e.edited()
}

// ---------------------------------------------------------------------------
// Conditions
// ---------------------------------------------------------------------------

// AddCondition appends an empty From condition (at most api.MaxRuleConditions).
func (e *Editor) AddCondition() {
	if len(e.form.Conditions) >= api.MaxRuleConditions {
		return
	}
	e.form.Conditions = append(e.form.Conditions, rules.NewCondition(api.RuleFieldFrom, nil))
	e.edited()
}

// RemoveCondition removes row i.
func (e *Editor) RemoveCondition(i int) {
	if i < 0 || i >= len(e.form.Conditions) {
		return
	}
	e.form.Conditions = append(e.form.Conditions[:i:i], e.form.Conditions[i+1:]...)
	e.edited()
}

// SetConditionField changes the field of row i: the operator and a text
// value are kept when the new field takes them (rules.NewCondition).
func (e *Editor) SetConditionField(i int, f api.RuleField) {
	if i < 0 || i >= len(e.form.Conditions) || e.form.Conditions[i].Field == f {
		return
	}
	old := e.form.Conditions[i]
	e.form.Conditions[i] = rules.NewCondition(f, &old)
	e.edited()
}

// SetConditionOp changes the operator of row i.
func (e *Editor) SetConditionOp(i int, op api.RuleOperator) {
	if i < 0 || i >= len(e.form.Conditions) || e.form.Conditions[i].Op == op {
		return
	}
	e.form.Conditions[i].Op = op
	e.edited()
}

// SetConditionValue changes the value of row i.
func (e *Editor) SetConditionValue(i int, v string) {
	if i < 0 || i >= len(e.form.Conditions) || e.form.Conditions[i].Value == v {
		return
	}
	e.form.Conditions[i].Value = v
	e.edited()
}

// ---------------------------------------------------------------------------
// Actions
// ---------------------------------------------------------------------------

// AvailableActions are the kinds Add Action offers.
func (e *Editor) AvailableActions() []api.RuleActionKind { return rules.AvailableActions(e.form) }

// AddAction appends an action of the kind, if it is on offer.
func (e *Editor) AddAction(k api.RuleActionKind) {
	for _, a := range e.AvailableActions() {
		if a == k && len(e.form.Actions) < api.MaxRuleActions {
			e.form.Actions = append(e.form.Actions, api.RuleAction{Kind: k})
			e.edited()
			return
		}
	}
}

// RemoveAction removes row i.
func (e *Editor) RemoveAction(i int) {
	if i < 0 || i >= len(e.form.Actions) {
		return
	}
	e.form.Actions = append(e.form.Actions[:i:i], e.form.Actions[i+1:]...)
	e.edited()
}

// SetActionFolder chooses the folder of a Move To action ("" none).
func (e *Editor) SetActionFolder(i int, id api.FolderID) {
	if i >= 0 && i < len(e.form.Actions) && e.form.Actions[i].FolderID != id {
		e.form.Actions[i].FolderID = id
		e.edited()
	}
}

// SetActionTo sets the address of a Forward To action.
func (e *Editor) SetActionTo(i int, s string) {
	if i >= 0 && i < len(e.form.Actions) && e.form.Actions[i].To != s {
		e.form.Actions[i].To = s
		e.edited()
	}
}

// SetActionSubject sets the subject of an Auto Reply action.
func (e *Editor) SetActionSubject(i int, s string) {
	if i >= 0 && i < len(e.form.Actions) && e.form.Actions[i].Subject != s {
		e.form.Actions[i].Subject = s
		e.edited()
	}
}

// SetActionText sets the text of an Auto Reply action.
func (e *Editor) SetActionText(i int, s string) {
	if i >= 0 && i < len(e.form.Actions) && e.form.Actions[i].Text != s {
		e.form.Actions[i].Text = s
		e.edited()
	}
}

// MoveFolders are the folders a Move To action offers (the rule's
// account).
func (e *Editor) MoveFolders() []api.Folder {
	if e.Dir == nil || e.form.AccountID == "" {
		return nil
	}
	return e.Dir.MoveFolders(e.form.AccountID)
}

// ---------------------------------------------------------------------------
// Problems
// ---------------------------------------------------------------------------

// Problems are the problems Validate finds (they disable Save whether or
// not they are shown yet).
func (e *Editor) Problems() []rules.Problem { return rules.Validate(e.form) }

// first is the text of the first problem of the field whose kind is
// (or, with listLevel false, is not) one of the list-level kinds; "" when
// there is none or problems are not shown yet.
func (e *Editor) first(field string, index int, list bool) string {
	if !e.shown {
		return ""
	}
	for _, p := range e.Problems() {
		if p.Field != field || isListLevel(p.Kind) != list || (!list && p.Index != index) {
			continue
		}
		return rules.ProblemText(p, e.Opts.Tr)
	}
	return ""
}

func isListLevel(k rules.ProblemKind) bool {
	switch k {
	case rules.ProblemNoCondition, rules.ProblemTooManyConditions, rules.ProblemNoAction, rules.ProblemTooManyActions:
		return true
	}
	return false
}

// NameProblem is the problem under the name.
func (e *Editor) NameProblem() string { return e.first("name", 0, false) }

// ConditionsProblem is the problem of the conditions as a whole (none, too
// many).
func (e *Editor) ConditionsProblem() string { return e.first("conditions", 0, true) }

// ConditionProblem is the problem under row i of the conditions.
func (e *Editor) ConditionProblem(i int) string { return e.first("conditions", i, false) }

// ActionsProblem is the problem of the actions as a whole.
func (e *Editor) ActionsProblem() string { return e.first("actions", 0, true) }

// ActionProblem is the problem under row i of the actions.
func (e *Editor) ActionProblem(i int) string { return e.first("actions", i, false) }

// ---------------------------------------------------------------------------
// Save
// ---------------------------------------------------------------------------

// CanSave reports that Save is enabled: no problem, nothing in flight.
func (e *Editor) CanSave() bool { return !e.saving && len(e.Problems()) == 0 }

// Saving reports a running Save.
func (e *Editor) Saving() bool { return e.saving }

// SaveError is the sentence of the last failed Save ("" none).
func (e *Editor) SaveError() string { return e.saveErr }

// Save stores the rule: rules.create, or rules.update for a stored one,
// after OutgoingQuestion when the rule sends mail and it is new or changed
// in what it sends.
func (e *Editor) Save() {
	if !e.CanSave() || e.closed {
		return
	}
	in := e.form.Input()
	send := func(confirm bool) {
		e.saving, e.saveErr = true, ""
		e.changed()
		done := func(r *api.Rule, err *api.Error) {
			e.saving = false
			if e.closed {
				return
			}
			if err != nil {
				e.saveErr = SaveErrorText(err, e.Opts.Tr)
				e.changed()
				return
			}
			e.changed()
			if e.OnSaved != nil {
				e.OnSaved(*r)
			}
		}
		if e.form.ID == "" {
			call(e.Opts, api.MethodRulesCreate, api.RulesCreateParams{Rule: in, ConfirmOutgoing: confirm}, 0,
				func(res *api.RulesCreateResult, err *api.Error) {
					if err != nil {
						done(nil, err)
						return
					}
					done(&res.Rule, nil)
				})
			return
		}
		call(e.Opts, api.MethodRulesUpdate, api.RulesUpdateParams{ID: e.form.ID, Rule: in, ConfirmOutgoing: confirm}, 0,
			func(res *api.RulesUpdateResult, err *api.Error) {
				if err != nil {
					done(nil, err)
					return
				}
				done(&res.Rule, nil)
			})
	}
	if !rules.NeedsConfirm(e.old, in) {
		send(false)
		return
	}
	if e.OnQuestion == nil {
		return
	}
	e.OnQuestion(rules.OutgoingQuestion(in, e.Opts.Tr), func(ok bool) {
		if ok && !e.closed && !e.saving {
			send(true)
		}
	})
}

// ---------------------------------------------------------------------------
// Try It
// ---------------------------------------------------------------------------

// TestChoice is a folder Try It can look in.
type TestChoice struct {
	Account api.AccountID
	Folder  api.FolderID
	// Title is the folder's name, with the account's before it when the
	// rule is for every account.
	Title string
}

// TestChoices are the folders that hold messages (rules.CanApplyIn) of the
// rule's account, or of every mail account.
func (e *Editor) TestChoices() []TestChoice {
	if e.Dir == nil {
		return nil
	}
	var out []TestChoice
	for _, a := range e.Dir.Accounts() {
		if e.form.AccountID != "" && a.ID != e.form.AccountID {
			continue
		}
		for _, f := range e.Dir.Folders(a.ID) {
			if !rules.CanApplyIn(f) {
				continue
			}
			title := e.Opts.folderName(f)
			if e.form.AccountID == "" {
				// The names are not a sentence: an account and a folder.
				title = e.Opts.accountName(a) + " · " + title
			}
			out = append(out, TestChoice{Account: a.ID, Folder: f.ID, Title: title})
		}
	}
	return out
}

// TestSelected is the index in TestChoices of the folder Try It looks in
// (the choice if it is on offer, else the inbox of the rule's account or of
// the first account, else the first folder); -1 without a folder.
func (e *Editor) TestSelected() int {
	choices := e.TestChoices()
	if e.testSet {
		for i, c := range choices {
			if (testKey{c.Account, c.Folder}) == e.testKey {
				return i
			}
		}
	}
	for i, c := range choices {
		if f, ok := e.folder(c); ok && f.Role == api.RoleInbox {
			return i
		}
	}
	if len(choices) > 0 {
		return 0
	}
	return -1
}

func (e *Editor) folder(c TestChoice) (api.Folder, bool) {
	for _, f := range e.Dir.Folders(c.Account) {
		if f.ID == c.Folder {
			return f, true
		}
	}
	return api.Folder{}, false
}

// SetTest chooses the folder Try It looks in (an index of TestChoices).
func (e *Editor) SetTest(i int) {
	choices := e.TestChoices()
	if i < 0 || i >= len(choices) {
		return
	}
	e.testKey, e.testSet = testKey{choices[i].Account, choices[i].Folder}, true
	e.testText, e.testFail = "", false
	e.testBusy = false
	e.testGen++
	e.changed()
}

// CanTest reports that Try It is enabled: a rule without problems and a
// folder to look in.
func (e *Editor) CanTest() bool {
	return !e.testBusy && len(e.Problems()) == 0 && e.TestSelected() >= 0
}

// Testing reports a running Try It.
func (e *Editor) Testing() bool { return e.testBusy }

// TestText is the line under Try It: how many messages of the folder the
// rule matches, or why it could not tell ("" before the first try and
// after a change).
func (e *Editor) TestText() string { return e.testText }

// TestFailed reports that TestText is a failure.
func (e *Editor) TestFailed() bool { return e.testFail }

// Test asks the daemon which stored messages of the chosen folder the rule,
// as edited, would match (rules.test).
func (e *Editor) Test() {
	if !e.CanTest() || e.closed {
		return
	}
	c := e.TestChoices()[e.TestSelected()]
	e.testBusy, e.testText, e.testFail = true, "", false
	e.testGen++
	gen := e.testGen
	e.changed()
	call(e.Opts, api.MethodRulesTest, api.RulesTestParams{Rule: e.form.Input(), AccountID: c.Account, FolderID: c.Folder}, TestTimeout,
		func(res *api.RulesTestResult, err *api.Error) {
			if e.closed || gen != e.testGen {
				return // edited since: the answer is about another rule
			}
			e.testBusy = false
			switch {
			case err == nil:
				e.testText = rules.TestResultText(res.Count, e.Opts.Tr)
			default:
				e.testFail = true
				if d, ok := invalidData(err); ok {
					e.testText = rules.InvalidText(d, e.Opts.Tr)
				} else {
					e.testText = e.Opts.otherErrorText(err)
				}
			}
			e.changed()
		})
}
