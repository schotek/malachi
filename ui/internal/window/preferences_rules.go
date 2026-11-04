// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/graphene"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/rules"
	"github.com/schotek/malachi/ui/internal/rulesctl"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Preferences → Rules (docs/plans/rules.md R14, rules_page in
// preferences.blp) over rulesctl.Controller: the mail rules in the order
// they run, one row each — name, a line of conditions, a line of actions,
// the account, how often it acted and, when the daemon turned it off, why
// — with the switch (a rule that sends mail asks first), Edit… and Remove,
// the order changed by dragging the handle or by Ctrl+Up / Ctrl+Down (also
// Alt+Up / Alt+Down), and Add Rule…. The rows are built again on the next
// idle after each change (a change arrives inside a row's own handler).
// Every text is ui/internal/rules'; every name and value of a rule is
// plain text.

// rulesPrefs is the page's state while the dialog is open.
type rulesPrefs struct {
	d   *PreferencesDialog
	ctl *rulesctl.Controller

	page     *adw.PreferencesPage
	group    *adw.PreferencesGroup
	emptyRow *adw.ActionRow
	add      *gtk.Button

	rows []*ruleRow
	// key is what the rows were built for (rulesRowsKey).
	key string
	// focus is the rule whose row takes the keyboard after the next build
	// (a rule moved by the keyboard keeps walking).
	focus string
	// lines describe the rows for the smoke test.
	lines []string

	cancel  func()
	pending bool
	closed  bool
	lastErr string
}

// ruleRow is a rule of the list.
type ruleRow struct {
	*adw.ActionRow

	rule      api.Rule
	spec      rules.Row
	page      *rulesPrefs
	handle    *gtk.Image // drag handle; the whole row is the drop target
	toggle    *gtk.Switch
	edit      *gtk.Button
	remove    *gtk.Button
	reverting bool // the switch is being set programmatically
	hint      dropHint
}

// bindRules fills the Rules page and keeps it with the daemon. Without a
// daemon (and without the samples) the page is hidden, and for a daemon
// that does not know the rules after its first answer.
func (d *PreferencesDialog) bindRules(b *gtk.Builder, c *client.Client) (unbind func()) {
	get := func(id string) any { return b.GetObject(id).Cast() }
	page := get("rules_page").(*adw.PreferencesPage)
	caller := rulesCaller(c)
	if caller == nil {
		page.SetVisible(false)
		return func() {}
	}
	tr := i18n.Tr
	p := &rulesPrefs{d: d, ctl: rulesctl.NewController(rulesOptions(caller, d.log)),
		page:     page,
		group:    get("rules_group").(*adw.PreferencesGroup),
		emptyRow: get("rules_empty_row").(*adw.ActionRow),
		add:      get("rules_add_button").(*gtk.Button),
	}
	page.SetTitle(rules.PageTitle(tr))
	p.group.SetDescription(glib.MarkupEscapeText(rules.PageDescription(tr)))
	p.emptyRow.SetTitle(rules.EmptyTitle(tr))
	p.emptyRow.SetSubtitle(rules.EmptyDescription(tr))
	p.add.SetLabel(rules.AddRule(tr))
	p.add.ConnectClicked(func() { p.openEditor(nil) })

	p.ctl.OnChange = p.schedule
	p.ctl.OnToast = p.toast
	p.ctl.OnQuestion = func(q rules.Question, answer func(bool)) { askRulesQuestion(d, q, answer) }
	p.cancel = listenRules(func(method string, params json.RawMessage) { p.ctl.Handle(method, params) })
	d.rulesPage = p
	p.ctl.Start()
	p.render()
	return p.stop
}

func (p *rulesPrefs) stop() {
	p.closed = true
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.ctl.Stop()
}

// toast shows text on the dialog.
func (p *rulesPrefs) toast(text string) {
	if text != "" && !p.closed {
		p.d.AddToast(widget.PlainToast(text))
	}
}

// schedule renders on the next idle.
func (p *rulesPrefs) schedule() {
	if p.pending || p.closed {
		return
	}
	p.pending = true
	glib.IdleAdd(func() {
		p.pending = false
		if !p.closed {
			p.render()
		}
	})
}

// openEditor opens the editor of a new rule (old nil: for the first mail
// account) or of the stored one.
func (p *rulesPrefs) openEditor(old *api.Rule) {
	var form rules.Form
	if old != nil {
		form = rules.FromRule(*old)
	} else {
		var account api.AccountID
		if accounts := p.ctl.Dir.Accounts(); len(accounts) > 0 {
			account = accounts[0].ID
		}
		form = rules.NewForm(account)
	}
	ed := newRulesEditor(p.ctl.Opts, p.ctl.Dir, form, old, func(api.Rule) { p.ctl.Refresh() })
	ed.present(p.d)
}

// rulesRowsKey is everything the rows show.
func rulesRowsKey(rows []rules.Row, busy func(string) bool) string {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s|%v|%s|%s|%s|%s|%s|%s|%v|%v;", r.ID, r.Enabled, r.Title, r.Conditions, r.Actions, r.Scope,
			r.Status, r.Problem, r.Outgoing, busy(r.ID))
	}
	return b.String()
}

// render shows the controller's state: the rows when they changed, the
// switches always (a declined question puts one back), the empty state,
// the last error as a toast.
func (p *rulesPrefs) render() {
	if e := p.ctl.Error(); e != p.lastErr {
		p.lastErr = e
		p.toast(e)
	}
	p.page.SetVisible(p.ctl.Available())
	if !p.ctl.Loaded() {
		return
	}
	rows := p.ctl.Rows()
	key := rulesRowsKey(rows, p.ctl.Busy)
	if key == p.key && p.rows != nil {
		for _, r := range p.rows {
			r.showEnabled()
		}
		return
	}
	p.key = key
	for _, r := range p.rows {
		p.group.Remove(r)
	}
	p.rows = p.rows[:0]
	p.lines = p.lines[:0]
	for _, spec := range rows {
		rule, ok := p.ctl.Rule(spec.ID)
		if !ok {
			continue
		}
		row := p.newRow(rule, spec)
		p.rows = append(p.rows, row)
		p.group.Add(row)
		p.lines = append(p.lines, fmt.Sprintf("%s [%s] on=%v outgoing=%v problem=%v", spec.Title, spec.Scope, spec.Enabled, spec.Outgoing, spec.Problem != ""))
	}
	p.emptyRow.SetVisible(len(p.rows) == 0)
	if id := p.focus; id != "" {
		p.focus = ""
		if i := p.indexOf(id); i >= 0 {
			p.rows[i].GrabFocus()
		}
	}
}

// indexOf is the position of the rule among the rows, -1 when it has none.
func (p *rulesPrefs) indexOf(id string) int {
	return slices.IndexFunc(p.rows, func(r *ruleRow) bool { return r.rule.ID == id })
}

// newRow is a rule's row.
func (p *rulesPrefs) newRow(rule api.Rule, spec rules.Row) *ruleRow {
	tr := i18n.Tr
	row := &ruleRow{ActionRow: adw.NewActionRow(), rule: rule, spec: spec, page: p}
	row.SetUseMarkup(false)
	// The drag styling of the Accounts page's rows (internal/style).
	row.AddCSSClass("account-row")
	row.SetTitle(spec.Title)
	lines := []string{spec.Conditions, spec.Actions}
	// The scope and the status are not a sentence: a name and a count.
	lines = append(lines, spec.Scope+" · "+spec.Status)
	if spec.Problem != "" {
		lines = append(lines, spec.Problem)
	}
	row.SetSubtitle(strings.Join(lines, "\n"))
	row.SetSubtitleLines(0)

	row.handle = gtk.NewImageFromIconName("list-drag-handle-symbolic")
	row.handle.AddCSSClass("drag-handle")
	row.handle.SetCursorFromName("grab")
	row.handle.SetTooltipText(i18n.T("Drag to reorder, or press Ctrl+Up and Ctrl+Down"))
	// AdwActionRow prepends prefixes, so the last one added sits leftmost.
	if spec.Problem != "" {
		warn := gtk.NewImageFromIconName("dialog-warning-symbolic")
		warn.AddCSSClass("warning")
		warn.SetTooltipText(spec.Problem)
		row.AddPrefix(warn)
	}
	if spec.Outgoing {
		row.AddPrefix(gtk.NewImageFromIconName("mail-send-symbolic"))
	}
	row.AddPrefix(row.handle)

	row.toggle = gtk.NewSwitch()
	row.toggle.SetVAlign(gtk.AlignCenter)
	row.toggle.SetTooltipText(rules.EnabledTooltip(tr))
	row.AddSuffix(row.toggle)
	row.SetActivatableWidget(row.toggle)

	row.edit = gtk.NewButtonFromIconName("document-edit-symbolic")
	row.edit.SetVAlign(gtk.AlignCenter)
	row.edit.AddCSSClass("flat")
	row.edit.SetTooltipText(rules.EditRule(tr))
	row.AddSuffix(row.edit)

	row.remove = gtk.NewButtonFromIconName("user-trash-symbolic")
	row.remove.SetVAlign(gtk.AlignCenter)
	row.remove.AddCSSClass("flat")
	row.remove.SetTooltipText(rules.RemoveRule(tr))
	row.AddSuffix(row.remove)

	row.showEnabled()
	row.SetSensitive(!p.ctl.Busy(rule.ID))

	id := rule.ID
	row.toggle.NotifyProperty("active", func() {
		if !row.reverting {
			p.ctl.Toggle(id, row.toggle.Active())
		}
	})
	row.edit.ConnectClicked(func() {
		if r, ok := p.ctl.Rule(id); ok {
			p.openEditor(&r)
		}
	})
	row.remove.ConnectClicked(func() { p.ctl.Remove(id) })
	p.addReorder(row)
	return row
}

// showEnabled sets the switch to the controller's state of the rule.
func (r *ruleRow) showEnabled() {
	on := r.spec.Enabled
	if cur, ok := r.page.ctl.Rule(r.rule.ID); ok {
		on = cur.Enabled
	}
	if r.toggle.Active() != on {
		r.reverting = true
		r.toggle.SetActive(on)
		r.reverting = false
	}
}

// setDropHint shows or hides the insertion line (CSS in internal/style).
func (r *ruleRow) setDropHint(h dropHint) {
	if r.hint == h {
		return
	}
	r.hint = h
	r.RemoveCSSClass("drop-above")
	r.RemoveCSSClass("drop-below")
	switch h {
	case hintAbove:
		r.AddCSSClass("drop-above")
	case hintBelow:
		r.AddCSSClass("drop-below")
	}
}

func (p *rulesPrefs) clearDropHints() {
	for _, r := range p.rows {
		r.setDropHint(hintNone)
	}
}

// addReorder makes the row draggable by its handle and a drop target for
// the other rows, and binds Ctrl+Up / Ctrl+Down (and Alt+Up / Alt+Down),
// as the Accounts page's rows have (accounts_reorder.go): the drag payload
// is the rule's id, the positions are the rows' own, and the order is the
// daemon's (rules.reorder) once the controller has shown it.
func (p *rulesPrefs) addReorder(row *ruleRow) {
	// A plain click on the handle must not reach the row, which would flip
	// its switch (SetActivatableWidget); claim it on release, not on press,
	// which would deny the drag source below.
	click := gtk.NewGestureClick()
	click.ConnectReleased(func(int, float64, float64) { click.SetState(gtk.EventSequenceClaimed) })
	row.handle.AddController(click)

	// The drag source sits on the row so that its coordinates can be tested
	// against the handle's bounds; only a press on the handle drags.
	src := gtk.NewDragSource()
	src.SetActions(gdk.ActionMove)
	src.ConnectPrepare(func(x, y float64) *gdk.ContentProvider {
		if !ruleOnHandle(row, x, y) {
			return nil
		}
		return gdk.NewContentProviderForValue(coreglib.NewValue(row.rule.ID))
	})
	src.ConnectDragBegin(func(gdk.Dragger) {
		icon := gtk.NewWidgetPaintable(row)
		src.SetIcon(icon, 0, 0)
		row.AddCSSClass("dragging")
	})
	end := func() {
		row.RemoveCSSClass("dragging")
		p.clearDropHints()
	}
	src.ConnectDragEnd(func(gdk.Dragger, bool) { end() })
	src.ConnectDragCancel(func(gdk.Dragger, gdk.DragCancelReason) bool { end(); return false })
	row.AddController(src)

	dst := gtk.NewDropTarget(coreglib.TypeString, gdk.ActionMove)
	dst.SetPreload(true)
	dst.ConnectMotion(func(_, y float64) gdk.DragAction {
		from, to, ok := p.dropTarget(dst.Value(), row, y)
		if !ok || from == to {
			row.setDropHint(hintNone)
			return 0
		}
		if y < float64(row.Height())/2 {
			row.setDropHint(hintAbove)
		} else {
			row.setDropHint(hintBelow)
		}
		return gdk.ActionMove
	})
	dst.ConnectLeave(func() { row.setDropHint(hintNone) })
	dst.ConnectDrop(func(value *coreglib.Value, _, y float64) bool {
		p.clearDropHints()
		from, to, ok := p.dropTarget(value, row, y)
		if !ok || from == to {
			return false
		}
		// Never rebuild the rows from inside the drop handler: it would
		// destroy the widget whose controller is still running.
		glib.IdleAdd(func() {
			if !p.closed {
				p.ctl.Move(from, to)
			}
		})
		return true
	})
	row.AddController(dst)

	sc := gtk.NewShortcutController()
	sc.SetScope(gtk.ShortcutScopeLocal)
	for _, s := range []struct {
		trigger string
		delta   int
	}{{"<Control>Up", -1}, {"<Control>Down", 1}, {"<Alt>Up", -1}, {"<Alt>Down", 1}} {
		delta := s.delta
		sc.AddShortcut(gtk.NewShortcut(
			gtk.NewShortcutTriggerParseString(s.trigger),
			gtk.NewCallbackAction(func(gtk.Widgetter, *glib.Variant) bool {
				moved := p.ctl.MoveBy(row.rule.ID, delta)
				if moved {
					p.focus = row.rule.ID
				}
				return moved
			}),
		))
	}
	row.AddController(sc)
}

// ruleOnHandle reports whether the point, in the row's coordinates, is on
// the row's drag handle.
func ruleOnHandle(row *ruleRow, x, y float64) bool {
	bounds, ok := row.handle.ComputeBounds(row)
	if !ok {
		return false
	}
	// graphene.Point wraps a C allocation; its zero value has none.
	pt := graphene.NewPointAlloc()
	pt.Init(float32(x), float32(y))
	return bounds.ContainsPoint(pt)
}

// dropTarget resolves a drag payload against the current rows: the index
// it comes from and the index it would land on when dropped on row at
// height y. False for anything that is not one of the rules' ids (text
// dragged in from another application, a row of the Accounts page).
func (p *rulesPrefs) dropTarget(value *coreglib.Value, row *ruleRow, y float64) (from, to int, ok bool) {
	if value == nil || value.Type() != coreglib.TypeString {
		return 0, 0, false
	}
	from = p.indexOf(value.String())
	target := p.indexOf(row.rule.ID)
	if from < 0 || target < 0 {
		return 0, 0, false
	}
	return from, insertIndex(from, target, y < float64(row.Height())/2), true
}
