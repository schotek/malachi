// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"slices"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/rules"
	"github.com/schotek/malachi/ui/internal/rulesctl"
)

// The editor of a mail rule (docs/plans/rules.md R14, rules_editor.blp)
// over rulesctl.Editor: an Adw.Dialog with the name, the account the rule
// is for ("Apply to"), how the conditions combine and one row per
// condition (field, operator, value), one row per action (folder, address,
// subject and text of a reply, the hint under an outgoing one), Stop
// processing more rules, Rule is on, and Try It over a folder. The rules
// (what is valid, what each choice offers, what Save asks) are the
// controller's and ui/internal/rules'; this file only shows them. The rows
// of the conditions and of the actions are rebuilt, on the next idle,
// when their structure changed (a field, an action kind, the accounts and
// folders to choose from) and updated in place otherwise, so typing never
// loses the cursor. Everything from mail is plain text.

// rulesEditor is one open editor.
type rulesEditor struct {
	ctl    *rulesctl.Editor
	dialog *adw.Dialog

	cancel, save, addCondition, testButton *gtk.Button
	addAction                              *gtk.MenuButton
	name                                   *adw.EntryRow
	account, match, testFolder             *adw.ComboRow
	stopRow, testRow                       *adw.ActionRow
	stop                                   *gtk.CheckButton
	enabled                                *adw.SwitchRow
	conditions, actions                    *gtk.ListBox
	nameProblem, conditionsProblem         *gtk.Label
	actionsProblem, errorLabel             *gtk.Label
	testSpinner                            *adw.Spinner
	title                                  *adw.WindowTitle
	addConditionContent, addActionContent  *adw.ButtonContent
	actionsHeading                         *gtk.Label

	condRows   []*ruleCondRow
	actionRows []*ruleActionRow

	// key is the structure the rows were built for (structureKey).
	key string
	// setting guards the handlers against the editor's own writes.
	setting bool
	pending bool
	closed  bool
	// afterBuild runs once after the next rebuild (a focus).
	afterBuild func()
	// sayings are the choices of the combo rows as built.
	accountChoices []rulesctl.AccountChoice
	testChoices    []rulesctl.TestChoice
}

// ruleCondRow is a row of the conditions.
type ruleCondRow struct {
	row            *gtk.ListBoxRow
	field, op      *gtk.DropDown
	entry          *gtk.Entry    // a text value
	choice         *gtk.DropDown // a bulk or a yes/no value
	remove         *gtk.Button
	problem        *gtk.Label
	valueKind      rules.ValueKind
	choiceValues   []string
	currentField   api.RuleField
	currentOperand []api.RuleOperator
}

// ruleActionRow is a row of the actions.
type ruleActionRow struct {
	row         *gtk.ListBoxRow
	kind        api.RuleActionKind
	folder      *gtk.DropDown
	folderIDs   []api.FolderID // behind the choices after the placeholder
	to, subject *gtk.Entry
	text        *gtk.TextView
	textHint    *gtk.Label
	remove      *gtk.Button
	problem     *gtk.Label
}

// newRulesEditor builds the editor of form over dir; old is the stored
// rule being edited (nil for a new one). onSaved runs once the daemon
// stored the rule, after the dialog closed.
func newRulesEditor(opts rulesctl.Options, dir *rulesctl.Directory, form rules.Form, old *api.Rule, onSaved func(api.Rule)) *rulesEditor {
	s := &rulesEditor{ctl: rulesctl.NewEditor(opts, dir, form, old)}
	s.bind(data.Builder("rules_editor.ui"))
	s.texts()
	s.wire(onSaved)
	s.rebuild()
	s.sync()
	return s
}

// present shows the editor over parent.
func (s *rulesEditor) present(parent gtk.Widgetter) { s.dialog.Present(parent) }

func (s *rulesEditor) bind(b *gtk.Builder) {
	get := func(id string) any { return b.GetObject(id).Cast() }
	s.dialog = get("rules_editor").(*adw.Dialog)
	s.cancel = get("rules_cancel").(*gtk.Button)
	s.save = get("rules_save").(*gtk.Button)
	s.addCondition = get("rules_add_condition").(*gtk.Button)
	s.addAction = get("rules_add_action").(*gtk.MenuButton)
	s.testButton = get("rules_test_button").(*gtk.Button)
	s.name = get("rules_name").(*adw.EntryRow)
	s.account = get("rules_account").(*adw.ComboRow)
	s.match = get("rules_match").(*adw.ComboRow)
	s.testFolder = get("rules_test_folder").(*adw.ComboRow)
	s.stopRow = get("rules_stop_row").(*adw.ActionRow)
	s.testRow = get("rules_test_row").(*adw.ActionRow)
	s.stop = get("rules_stop").(*gtk.CheckButton)
	s.enabled = get("rules_enabled").(*adw.SwitchRow)
	s.conditions = get("rules_conditions").(*gtk.ListBox)
	s.actions = get("rules_actions").(*gtk.ListBox)
	s.nameProblem = get("rules_name_problem").(*gtk.Label)
	s.conditionsProblem = get("rules_conditions_problem").(*gtk.Label)
	s.actionsProblem = get("rules_actions_problem").(*gtk.Label)
	s.errorLabel = get("rules_error").(*gtk.Label)
	s.testSpinner = get("rules_test_spinner").(*adw.Spinner)
	s.title = get("rules_editor_title").(*adw.WindowTitle)
	s.addConditionContent = get("rules_add_condition_content").(*adw.ButtonContent)
	s.addActionContent = get("rules_add_action_content").(*adw.ButtonContent)
	s.actionsHeading = get("rules_actions_heading").(*gtk.Label)
}

// texts sets the fixed texts (ui/internal/rules/text.go).
func (s *rulesEditor) texts() {
	tr := i18n.Tr
	s.title.SetTitle(s.ctl.Title())
	s.cancel.SetLabel(i18n.T("_Cancel"))
	s.save.SetLabel(i18n.T("_Save"))
	s.name.SetTitle(rules.NameLabel(tr))
	s.account.SetTitle(rules.AccountLabel(tr))
	s.match.SetTitle(rules.MatchLabel(tr))
	s.match.SetModel(gtk.NewStringList([]string{rules.MatchText(api.RuleMatchAll, tr), rules.MatchText(api.RuleMatchAny, tr)}))
	s.addConditionContent.SetLabel(rules.AddCondition(tr))
	s.actionsHeading.SetText(rules.ActionsHeading(tr))
	s.addActionContent.SetLabel(rules.AddAction(tr))
	s.stopRow.SetTitle(rules.StopLabel(tr))
	s.stopRow.SetSubtitle(rules.StopHint(tr))
	s.enabled.SetTitle(rules.EnabledLabel(tr))
	s.testFolder.SetTitle(rules.TestFolderLabel(tr))
	s.testButton.SetLabel(rules.TestButton(tr))
}

// wire connects the controls to the controller.
func (s *rulesEditor) wire(onSaved func(api.Rule)) {
	s.ctl.OnChange = s.render
	s.ctl.OnQuestion = func(q rules.Question, answer func(bool)) { askRulesQuestion(s.dialog, q, answer) }
	s.ctl.OnSaved = func(r api.Rule) {
		s.dialog.Close()
		if onSaved != nil {
			onSaved(r)
		}
	}
	s.dialog.ConnectClosed(func() {
		s.closed = true
		s.ctl.Close()
	})
	s.cancel.ConnectClicked(func() { s.dialog.Close() })
	s.save.ConnectClicked(s.ctl.Save)
	s.name.ConnectChanged(func() {
		if !s.setting {
			s.ctl.SetName(s.name.Text())
		}
	})
	s.account.NotifyProperty("selected", func() {
		if s.setting {
			return
		}
		if i, choices := int(s.account.Selected()), s.accountChoices; i >= 0 && i < len(choices) {
			s.ctl.SetAccount(choices[i].ID)
		}
	})
	s.match.NotifyProperty("selected", func() {
		if s.setting {
			return
		}
		m := api.RuleMatchAll
		if s.match.Selected() == 1 {
			m = api.RuleMatchAny
		}
		s.ctl.SetMatch(m)
	})
	s.stop.ConnectToggled(func() {
		if !s.setting {
			s.ctl.SetStop(s.stop.Active())
		}
	})
	s.enabled.NotifyProperty("active", func() {
		if !s.setting {
			s.ctl.SetEnabled(s.enabled.Active())
		}
	})
	s.addCondition.ConnectClicked(func() {
		s.ctl.AddCondition()
		s.afterBuild = func() {
			if n := len(s.condRows); n > 0 && s.condRows[n-1].entry != nil {
				s.condRows[n-1].entry.GrabFocus()
			}
		}
	})
	s.testFolder.NotifyProperty("selected", func() {
		if !s.setting {
			s.ctl.SetTest(int(s.testFolder.Selected()))
		}
	})
	s.testButton.ConnectClicked(s.ctl.Test)

	group := gio.NewSimpleActionGroup()
	add := gio.NewSimpleAction("add-action", glib.NewVariantType("s"))
	add.ConnectActivate(func(v *glib.Variant) {
		if v == nil {
			return
		}
		s.ctl.AddAction(api.RuleActionKind(v.String()))
		s.afterBuild = func() {
			if n := len(s.actionRows); n > 0 {
				s.actionRows[n-1].focus()
			}
		}
	})
	group.AddAction(add)
	s.dialog.InsertActionGroup("editor", group)
}

// ---------------------------------------------------------------------------
// Showing the state
// ---------------------------------------------------------------------------

// structureKey is everything the rows are built from: when it changes the
// rows are rebuilt, otherwise they are updated in place.
func (s *rulesEditor) structureKey() string {
	f := s.ctl.Form()
	var b strings.Builder
	for _, c := range f.Conditions {
		fmt.Fprintf(&b, "c:%s;", c.Field)
	}
	for _, a := range f.Actions {
		fmt.Fprintf(&b, "a:%s;", a.Kind)
	}
	fmt.Fprintf(&b, "acc:%s;", f.AccountID)
	for _, c := range s.ctl.AccountChoices() {
		fmt.Fprintf(&b, "ac:%s|%s;", c.ID, c.Name)
	}
	for _, k := range s.ctl.AvailableActions() {
		fmt.Fprintf(&b, "av:%s;", k)
	}
	for _, fo := range s.ctl.MoveFolders() {
		fmt.Fprintf(&b, "mf:%s|%s;", fo.ID, rulesFolderName(fo))
	}
	for _, c := range s.ctl.TestChoices() {
		fmt.Fprintf(&b, "t:%s|%s|%s;", c.Account, c.Folder, c.Title)
	}
	return b.String()
}

// render shows the controller's state: in place at once, the rows rebuilt
// on the next idle when their structure changed (a change arrives inside
// a row's own handler, which a rebuild would destroy).
func (s *rulesEditor) render() {
	if s.closed {
		return
	}
	if s.structureKey() != s.key && !s.pending {
		s.pending = true
		glib.IdleAdd(func() {
			s.pending = false
			if s.closed {
				return
			}
			s.rebuild()
			s.sync()
			if f := s.afterBuild; f != nil {
				s.afterBuild = nil
				f()
			}
		})
	}
	s.sync()
}

// rebuild builds the choices and the rows of the conditions and the actions
// for the controller's form as it is now.
func (s *rulesEditor) rebuild() {
	s.setting = true
	defer func() { s.setting = false }()
	tr := i18n.Tr
	f := s.ctl.Form()

	s.accountChoices = s.ctl.AccountChoices()
	names := make([]string, len(s.accountChoices))
	for i, c := range s.accountChoices {
		names[i] = c.Name
	}
	s.account.SetModel(gtk.NewStringList(names))

	s.testChoices = s.ctl.TestChoices()
	titles := make([]string, len(s.testChoices))
	for i, c := range s.testChoices {
		titles[i] = c.Title
	}
	s.testFolder.SetModel(gtk.NewStringList(titles))
	s.testFolder.SetSensitive(len(titles) > 0)

	for _, r := range s.condRows {
		s.conditions.Remove(r.row)
	}
	s.condRows = s.condRows[:0]
	for i, c := range f.Conditions {
		r := s.newCondRow(i, c)
		s.condRows = append(s.condRows, r)
		s.conditions.Append(r.row)
	}

	for _, r := range s.actionRows {
		s.actions.Remove(r.row)
	}
	s.actionRows = s.actionRows[:0]
	for i, a := range f.Actions {
		r := s.newActionRow(i, a)
		s.actionRows = append(s.actionRows, r)
		s.actions.Append(r.row)
	}

	menu := gio.NewMenu()
	for _, k := range s.ctl.AvailableActions() {
		item := gio.NewMenuItem(rules.ActionText(k, tr), "")
		item.SetActionAndTargetValue("editor.add-action", glib.NewVariantString(string(k)))
		menu.AppendItem(item)
	}
	s.addAction.SetMenuModel(menu)
	s.key = s.structureKey()
}

// sync updates the shown values, the problems and the buttons in place.
func (s *rulesEditor) sync() {
	s.setting = true
	defer func() { s.setting = false }()
	f := s.ctl.Form()

	if s.name.Text() != f.Name {
		s.name.SetText(f.Name)
	}
	setProblem(s.nameProblem, s.ctl.NameProblem())
	for i, c := range s.accountChoices {
		if c.ID == f.AccountID {
			s.account.SetSelected(uint(i))
		}
	}
	if f.Match == api.RuleMatchAny {
		s.match.SetSelected(1)
	} else {
		s.match.SetSelected(0)
	}
	if s.stop.Active() != f.Stop {
		s.stop.SetActive(f.Stop)
	}
	if s.enabled.Active() != f.Enabled {
		s.enabled.SetActive(f.Enabled)
	}
	setProblem(s.conditionsProblem, s.ctl.ConditionsProblem())
	setProblem(s.actionsProblem, s.ctl.ActionsProblem())
	s.addCondition.SetSensitive(len(f.Conditions) < api.MaxRuleConditions)
	s.addAction.SetSensitive(len(s.ctl.AvailableActions()) > 0 && len(f.Actions) < api.MaxRuleActions)

	if s.structureKey() == s.key {
		for i, r := range s.condRows {
			if i < len(f.Conditions) {
				r.sync(f.Conditions[i], s.ctl.ConditionProblem(i))
			}
		}
		for i, r := range s.actionRows {
			if i < len(f.Actions) {
				r.sync(f.Actions[i], s.ctl.ActionProblem(i))
			}
		}
	}

	s.save.SetSensitive(s.ctl.CanSave())
	if e := s.ctl.SaveError(); e != "" {
		s.errorLabel.SetText(e)
		s.errorLabel.SetVisible(true)
	} else {
		s.errorLabel.SetVisible(false)
	}

	if i := s.ctl.TestSelected(); i >= 0 {
		s.testFolder.SetSelected(uint(i))
	}
	s.testButton.SetSensitive(s.ctl.CanTest())
	s.testSpinner.SetVisible(s.ctl.Testing())
	s.testRow.SetTitle(s.ctl.TestText())
	if s.ctl.TestFailed() {
		s.testRow.AddCSSClass("error")
	} else {
		s.testRow.RemoveCSSClass("error")
	}
}

// setProblem shows a problem's sentence under its row, or hides the line.
func setProblem(l *gtk.Label, text string) {
	l.SetText(text)
	l.SetVisible(text != "")
}

// summary describes the editor for the smoke test.
func (s *rulesEditor) summary() string {
	return fmt.Sprintf("conditions=%d actions=%d save=%v test=%v", len(s.condRows), len(s.actionRows),
		s.save.IsSensitive(), s.testButton.IsSensitive())
}

// ---------------------------------------------------------------------------
// The rows
// ---------------------------------------------------------------------------

// rulesLabel is a label of plain text.
func rulesLabel(text string, classes ...string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetUseMarkup(false)
	l.SetXAlign(0)
	l.SetWrap(true)
	for _, c := range classes {
		l.AddCSSClass(c)
	}
	return l
}

// rowBox is the vertical box a row of the editor is made of.
func rowBox() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 4)
	box.SetMarginTop(8)
	box.SetMarginBottom(8)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)
	return box
}

func newEditorRow(child gtk.Widgetter) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	row.SetActivatable(false)
	row.SetChild(child)
	return row
}

func rulesRemoveButton() *gtk.Button {
	b := gtk.NewButtonFromIconName("list-remove-symbolic")
	b.SetVAlign(gtk.AlignCenter)
	b.AddCSSClass("flat")
	b.SetTooltipText(rules.RemoveRow(i18n.Tr))
	return b
}

// newCondRow is the row of condition i: field, operator, value, remove.
func (s *rulesEditor) newCondRow(i int, c api.RuleCondition) *ruleCondRow {
	tr := i18n.Tr
	r := &ruleCondRow{valueKind: rules.ValueKindOf(c.Field), currentField: c.Field}

	fields := make([]string, len(rules.Fields))
	for k, f := range rules.Fields {
		fields[k] = rules.FieldText(f, tr)
	}
	r.field = gtk.NewDropDownFromStrings(fields)
	r.field.SetSelected(uint(max(slices.Index(rules.Fields, c.Field), 0)))
	r.field.NotifyProperty("selected", func() {
		if s.setting {
			return
		}
		if k := int(r.field.Selected()); k >= 0 && k < len(rules.Fields) {
			s.ctl.SetConditionField(i, rules.Fields[k])
		}
	})

	r.currentOperand = rules.OperatorsFor(c.Field)
	ops := make([]string, len(r.currentOperand))
	for k, op := range r.currentOperand {
		ops[k] = rules.OperatorText(c.Field, op, tr)
	}
	r.op = gtk.NewDropDownFromStrings(ops)
	r.op.SetSelected(uint(max(slices.Index(r.currentOperand, c.Op), 0)))
	r.op.NotifyProperty("selected", func() {
		if s.setting {
			return
		}
		if k := int(r.op.Selected()); k >= 0 && k < len(r.currentOperand) {
			s.ctl.SetConditionOp(i, r.currentOperand[k])
		}
	})

	line := gtk.NewBox(gtk.OrientationHorizontal, 6)
	line.Append(r.field)
	line.Append(r.op)
	switch r.valueKind {
	case rules.ValueBulk, rules.ValueBool:
		r.choiceValues = rules.BulkValues
		if r.valueKind == rules.ValueBool {
			r.choiceValues = []string{rules.ValueTrue, rules.ValueFalse}
		}
		texts := make([]string, len(r.choiceValues))
		for k, v := range r.choiceValues {
			texts[k] = rules.ValueText(c.Field, v, tr)
		}
		r.choice = gtk.NewDropDownFromStrings(texts)
		r.choice.SetHExpand(true)
		r.choice.SetSelected(uint(max(slices.Index(r.choiceValues, c.Value), 0)))
		r.choice.NotifyProperty("selected", func() {
			if s.setting {
				return
			}
			if k := int(r.choice.Selected()); k >= 0 && k < len(r.choiceValues) {
				s.ctl.SetConditionValue(i, r.choiceValues[k])
			}
		})
		line.Append(r.choice)
	default:
		r.entry = gtk.NewEntry()
		r.entry.SetHExpand(true)
		r.entry.SetText(c.Value)
		r.entry.SetPlaceholderText(rules.ValuePlaceholder(c.Field, c.Op, tr))
		r.entry.ConnectChanged(func() {
			if !s.setting {
				s.ctl.SetConditionValue(i, r.entry.Text())
			}
		})
		line.Append(r.entry)
	}
	r.remove = rulesRemoveButton()
	r.remove.ConnectClicked(func() { s.ctl.RemoveCondition(i) })
	line.Append(r.remove)

	r.problem = rulesLabel("", "error", "caption")
	r.problem.SetVisible(false)
	box := rowBox()
	box.Append(line)
	box.Append(r.problem)
	r.row = newEditorRow(box)
	return r
}

// sync updates the row in place: the operator, the value and the problem.
func (r *ruleCondRow) sync(c api.RuleCondition, problem string) {
	if k := slices.Index(r.currentOperand, c.Op); k >= 0 && int(r.op.Selected()) != k {
		r.op.SetSelected(uint(k))
	}
	if r.entry != nil {
		if r.entry.Text() != c.Value {
			r.entry.SetText(c.Value)
		}
		r.entry.SetPlaceholderText(rules.ValuePlaceholder(c.Field, c.Op, i18n.Tr))
	}
	if r.choice != nil {
		if k := slices.Index(r.choiceValues, c.Value); k >= 0 && int(r.choice.Selected()) != k {
			r.choice.SetSelected(uint(k))
		}
	}
	setProblem(r.problem, problem)
}

// newActionRow is the row of action i: its name and remove button, its
// inputs, the hint under an outgoing one.
func (s *rulesEditor) newActionRow(i int, a api.RuleAction) *ruleActionRow {
	tr := i18n.Tr
	r := &ruleActionRow{kind: a.Kind}
	box := rowBox()
	head := gtk.NewBox(gtk.OrientationHorizontal, 6)
	name := rulesLabel(rules.ActionText(a.Kind, tr), "heading")
	name.SetHExpand(true)
	name.SetVAlign(gtk.AlignCenter)
	head.Append(name)
	r.remove = rulesRemoveButton()
	r.remove.ConnectClicked(func() { s.ctl.RemoveAction(i) })
	head.Append(r.remove)
	box.Append(head)

	switch a.Kind {
	case api.RuleActionMove:
		texts := []string{rules.FolderPlaceholder(tr)}
		for _, f := range s.ctl.MoveFolders() {
			texts = append(texts, rulesFolderName(f))
			r.folderIDs = append(r.folderIDs, f.ID)
		}
		r.folder = gtk.NewDropDownFromStrings(texts)
		r.folder.SetSelected(uint(slices.Index(r.folderIDs, a.FolderID) + 1))
		r.folder.NotifyProperty("selected", func() {
			if s.setting {
				return
			}
			var id api.FolderID
			if k := int(r.folder.Selected()); k >= 1 && k <= len(r.folderIDs) {
				id = r.folderIDs[k-1]
			}
			s.ctl.SetActionFolder(i, id)
		})
		box.Append(r.folder)
	case api.RuleActionForward:
		r.to = gtk.NewEntry()
		r.to.SetInputPurpose(gtk.InputPurposeEmail)
		r.to.SetPlaceholderText(rules.ForwardPlaceholder(tr))
		r.to.SetText(a.To)
		r.to.ConnectChanged(func() {
			if !s.setting {
				s.ctl.SetActionTo(i, r.to.Text())
			}
		})
		box.Append(r.to)
	case api.RuleActionAutoReply:
		r.subject = gtk.NewEntry()
		r.subject.SetPlaceholderText(rules.ReplySubjectPlaceholder(tr))
		r.subject.SetText(a.Subject)
		r.subject.ConnectChanged(func() {
			if !s.setting {
				s.ctl.SetActionSubject(i, r.subject.Text())
			}
		})
		box.Append(r.subject)

		r.text = gtk.NewTextView()
		r.text.SetWrapMode(gtk.WrapWordChar)
		r.text.SetAcceptsTab(false)
		r.text.SetTopMargin(6)
		r.text.SetBottomMargin(6)
		r.text.SetLeftMargin(8)
		r.text.SetRightMargin(8)
		buf := r.text.Buffer()
		buf.SetText(a.Text)
		scroll := gtk.NewScrolledWindow()
		scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
		scroll.SetMinContentHeight(96)
		scroll.SetChild(r.text)
		scroll.AddCSSClass("card")
		overlay := gtk.NewOverlay()
		overlay.SetChild(scroll)
		r.textHint = rulesLabel(rules.ReplyTextPlaceholder(tr), "dim-label")
		r.textHint.SetCanTarget(false)
		r.textHint.SetVAlign(gtk.AlignStart)
		r.textHint.SetMarginTop(6)
		r.textHint.SetMarginStart(8)
		r.textHint.SetVisible(a.Text == "")
		overlay.AddOverlay(r.textHint)
		buf.ConnectChanged(func() {
			text := buf.Text(buf.StartIter(), buf.EndIter(), false)
			r.textHint.SetVisible(text == "")
			if !s.setting {
				s.ctl.SetActionText(i, text)
			}
		})
		box.Append(overlay)
	}
	if a.Kind.IsOutgoing() {
		box.Append(rulesLabel(rules.OutgoingHint(a.Kind, tr), "dim-label", "caption"))
	}
	r.problem = rulesLabel("", "error", "caption")
	r.problem.SetVisible(false)
	box.Append(r.problem)
	r.row = newEditorRow(box)
	return r
}

// sync updates the row in place: the inputs and the problem.
func (r *ruleActionRow) sync(a api.RuleAction, problem string) {
	if r.folder != nil {
		if k := uint(slices.Index(r.folderIDs, a.FolderID) + 1); r.folder.Selected() != k {
			r.folder.SetSelected(k)
		}
	}
	if r.to != nil && r.to.Text() != a.To {
		r.to.SetText(a.To)
	}
	if r.subject != nil && r.subject.Text() != a.Subject {
		r.subject.SetText(a.Subject)
	}
	if r.text != nil {
		buf := r.text.Buffer()
		if buf.Text(buf.StartIter(), buf.EndIter(), false) != a.Text {
			buf.SetText(a.Text)
		}
	}
	setProblem(r.problem, problem)
}

// focus puts the keyboard in the row's first input.
func (r *ruleActionRow) focus() {
	switch {
	case r.folder != nil:
		r.folder.GrabFocus()
	case r.to != nil:
		r.to.GrabFocus()
	case r.subject != nil:
		r.subject.GrabFocus()
	}
}

// ---------------------------------------------------------------------------
// Questions
// ---------------------------------------------------------------------------

// askRulesQuestion shows a rules.Question over parent as plain text: the
// button that goes ahead is destructive when the question says so, else
// the suggested one; Cancel is the default and the answer of closing.
func askRulesQuestion(parent gtk.Widgetter, q rules.Question, answer func(ok bool)) {
	d := adw.NewAlertDialog(q.Heading, q.Body)
	d.SetHeadingUseMarkup(false)
	d.SetBodyUseMarkup(false)
	d.AddResponse("cancel", i18n.T("_Cancel"))
	d.AddResponse("confirm", q.Confirm)
	if q.Destructive {
		d.SetResponseAppearance("confirm", adw.ResponseDestructive)
	} else {
		d.SetResponseAppearance("confirm", adw.ResponseSuggested)
	}
	d.SetDefaultResponse("cancel")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) { answer(response == "confirm") })
	d.Present(parent)
}
