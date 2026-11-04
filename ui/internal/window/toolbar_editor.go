// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/toolbar"
	"github.com/schotek/malachi/ui/internal/toolbaredit"
)

// The toolbar editor (T13): an Adw.Dialog over toolbaredit.Editor, opened
// from Preferences → General → Toolbar and from a header bar's context
// menu (T15). The chosen bar's sections are groups of rows; a row moves by
// its handle (drag and drop) or with Ctrl+↑/↓, only where the editor
// allows; Remove takes it off, Add Item… offers the items and the spaces
// a section may hold, Restore Defaults asks and writes the empty list.
// Every change is written at once to the bar's key; a change of the key
// from elsewhere (the window, another process) reloads the rows.

// toolbarDragPrefix starts the drag payload: the bar and the row's index.
const toolbarDragPrefix = "malachi-toolbar:"

// toolbarEditor is one open editor dialog.
type toolbarEditor struct {
	dialog   *adw.Dialog
	st       *settings.Store
	features func() toolbar.Features
	ed       *toolbaredit.Editor
	page     *adw.PreferencesPage
	bars     *adw.ToggleGroup
	restore  *gtk.Button
	groups   []*adw.PreferencesGroup
	// rows are the shown rows by their layout index, keys their Ctrl+↑/↓
	// controllers (the smoke test runs them as a key press would).
	rows    map[int]*adw.ActionRow
	keys    map[int]*gtk.ShortcutController
	handles map[int]*gtk.Image
	// dragging is set while a row of the editor is being dragged; a drop
	// (or a reload) then waits in pending until the drag has ended, so the
	// rows and their drag and drop controllers are never destroyed while
	// GTK still runs the drag (the owner's crash of 2026-11-04 under
	// XQuartz, where the source's drag-end comes after the drop);
	// pendingTimer runs it anyway should no drag-end come.
	dragging     bool
	pending      func()
	pendingTimer glib.SourceHandle
	// rebuildQueued: the rows are drawn again on the next idle round
	// (commit); until then a key press on a stale row does nothing.
	rebuildQueued bool
	closed        bool
	// onRebuilt, when set, runs after the rows were drawn again (the
	// development step toolbar-drag reports their places).
	onRebuilt func()
	// written is what the editor wrote last, so its own change does not
	// rebuild the rows a second time.
	written  []string
	writing  bool
	removers []func()
	// focusIndex is the row that takes the keyboard after a rebuild, -1
	// for none.
	focusIndex int
	settingBar bool
}

// openToolbarEditor opens the editor on bar over the main window.
func (w *Window) openToolbarEditor(bar toolbar.Bar) {
	e := newToolbarEditor(w.settings, w.toolbarFeatures, bar)
	e.dialog.Present(w)
}

// currentToolbarBar is the bar the window shows now.
func (w *Window) currentToolbarBar() toolbar.Bar {
	switch w.mode {
	case board.ModeBoard:
		if w.boardPage != nil {
			return boardToolbarBar(w.boardPage.ctl.State().Style)
		}
		return toolbar.BarBoardList
	case board.ModeCalendar:
		return toolbar.BarCalendar
	}
	return toolbar.BarMail
}

// newToolbarEditor builds the dialog on bar; features are the window's
// (T7: a row without its feature says so).
func newToolbarEditor(st *settings.Store, features func() toolbar.Features, bar toolbar.Bar) *toolbarEditor {
	if features == nil {
		features = func() toolbar.Features { return toolbar.Features{} }
	}
	tr := i18n.Tr
	e := &toolbarEditor{
		dialog:     adw.NewDialog(),
		st:         st,
		features:   features,
		rows:       map[int]*adw.ActionRow{},
		focusIndex: -1,
	}
	e.dialog.SetTitle(toolbar.DialogTitle(tr))
	e.dialog.SetContentWidth(520)
	e.dialog.SetContentHeight(680)

	view := adw.NewToolbarView()
	view.AddTopBar(adw.NewHeaderBar())

	e.bars = adw.NewToggleGroup()
	for _, b := range toolbar.Bars {
		t := adw.NewToggle()
		t.SetName(string(b))
		t.SetLabel(toolbar.BarTitle(tr, b))
		e.bars.Add(t)
	}
	e.bars.SetHomogeneous(false)
	e.bars.SetHAlign(gtk.AlignCenter)
	e.bars.SetMarginStart(12)
	e.bars.SetMarginEnd(12)
	e.bars.SetMarginBottom(6)
	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyNever)
	scroll.SetChild(e.bars)
	view.AddTopBar(scroll)

	e.page = adw.NewPreferencesPage()
	view.SetContent(e.page)

	e.restore = gtk.NewButtonWithLabel(toolbar.RestoreTitle(tr))
	e.restore.AddCSSClass("destructive-action")
	e.restore.SetHAlign(gtk.AlignCenter)
	e.restore.SetMarginTop(6)
	e.restore.SetMarginBottom(12)
	e.restore.ConnectClicked(e.askRestore)
	view.AddBottomBar(e.restore)
	e.dialog.SetChild(view)

	e.bars.NotifyProperty("active-name", func() {
		if e.settingBar {
			return
		}
		e.show(toolbar.Bar(e.bars.ActiveName()))
	})
	if st != nil {
		for _, b := range toolbar.Bars {
			b := b
			e.removers = append(e.removers, st.OnChanged(toolbar.Key(b), func() {
				if e.ed != nil && e.ed.Bar() == b {
					e.keyChanged()
				}
			}))
		}
	}
	e.dialog.ConnectClosed(func() {
		e.closed = true
		if e.pendingTimer != 0 {
			glib.SourceRemove(e.pendingTimer)
			e.pendingTimer = 0
		}
		e.pending = nil
		for _, f := range e.removers {
			f()
		}
		e.removers = nil
	})
	e.show(bar)
	return e
}

// stored is bar's key.
func (e *toolbarEditor) stored(bar toolbar.Bar) []string {
	if e.st == nil {
		return nil
	}
	return e.st.ToolbarLayout(bar)
}

// show edits bar.
func (e *toolbarEditor) show(bar toolbar.Bar) {
	e.ed = toolbaredit.New(gtkVariant, bar, e.stored(bar), e.features())
	e.written = nil
	e.settingBar = true
	e.bars.SetActiveName(string(bar))
	e.settingBar = false
	e.focusIndex = -1
	e.rebuild()
}

// keyChanged follows a change of the edited bar's key.
func (e *toolbarEditor) keyChanged() {
	if e.writing {
		return
	}
	stored := e.stored(e.ed.Bar())
	if e.written != nil && equalTokens(stored, e.written) {
		return
	}
	bar := e.ed.Bar()
	e.afterDrag(func() {
		if e.ed.Bar() != bar {
			return
		}
		e.ed.SetFeatures(e.features())
		e.ed.Reload(e.stored(bar))
		e.queueRebuild()
	})
}

// afterDrag runs f on the next idle round, or after the running drag of
// a row has ended (dragging).
func (e *toolbarEditor) afterDrag(f func()) {
	if !e.dragging {
		idle(func() {
			if !e.closed {
				f()
			}
		})
		return
	}
	e.pending = f
	if e.pendingTimer == 0 {
		e.pendingTimer = glib.TimeoutAdd(2000, func() bool {
			e.pendingTimer = 0
			e.dragging = false
			e.runPending()
			return false
		})
	}
}

// runPending runs what waited for the drag's end.
func (e *toolbarEditor) runPending() {
	if e.pendingTimer != 0 {
		glib.SourceRemove(e.pendingTimer)
		e.pendingTimer = 0
	}
	f := e.pending
	e.pending = nil
	if f != nil && !e.closed {
		f()
	}
}

// dragEnded follows the end of a row's drag: what the drop asked for runs
// on the next idle round, once GTK is done with the drag.
func (e *toolbarEditor) dragEnded() {
	e.dragging = false
	idle(e.runPending)
}

func equalTokens(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// commit writes the arrangement after a change and draws it, the row at
// focus (a layout index, -1 for none) taking the keyboard.
func (e *toolbarEditor) commit(focus int) {
	e.write(e.ed.Tokens())
	e.focusIndex = focus
	e.queueRebuild()
}

// queueRebuild draws the rows again on the next idle round, never inside
// the handler of one of them (a row's shortcut, button or drop target).
func (e *toolbarEditor) queueRebuild() {
	if e.rebuildQueued {
		return
	}
	e.rebuildQueued = true
	idle(func() {
		e.rebuildQueued = false
		if !e.closed {
			e.rebuild()
		}
	})
}

// write stores tokens in the edited bar's key and reads it back, so the
// editor goes on from what the key keeps.
func (e *toolbarEditor) write(tokens []string) {
	bar := e.ed.Bar()
	if e.st != nil {
		e.writing = true
		e.st.SetToolbarLayout(bar, tokens)
		e.writing = false
		e.written = e.st.ToolbarLayout(bar)
		e.ed.Reload(e.written)
		return
	}
	e.written = append([]string(nil), tokens...)
	e.ed.Reload(e.written)
}

// rebuild draws the rows again.
func (e *toolbarEditor) rebuild() {
	for _, g := range e.groups {
		e.page.Remove(g)
	}
	e.groups = nil
	e.rows = map[int]*adw.ActionRow{}
	e.keys = map[int]*gtk.ShortcutController{}
	e.handles = map[int]*gtk.Image{}
	tr := i18n.Tr
	bar := e.ed.Bar()
	for _, sec := range e.ed.Sections() {
		g := adw.NewPreferencesGroup()
		g.SetTitle(toolbar.SectionTitle(tr, bar, sec.Section))
		for _, r := range sec.Rows {
			g.Add(e.newRow(r))
		}
		g.Add(e.newAddRow(sec.Section))
		e.page.Add(g)
		e.groups = append(e.groups, g)
	}
	e.restore.SetSensitive(!e.ed.IsDefault())
	if r := e.rows[e.focusIndex]; r != nil {
		idle(func() { r.GrabFocus() })
	}
	e.focusIndex = -1
	if e.onRebuilt != nil {
		e.onRebuilt()
	}
}

// newRow is the row of one item or space.
func (e *toolbarEditor) newRow(r toolbaredit.Row) *adw.ActionRow {
	tr := i18n.Tr
	row := adw.NewActionRow()
	row.SetUseMarkup(false)
	row.SetTitle(toolbar.ItemLabel(tr, e.ed.Bar(), r.ID))
	if r.Unavailable {
		row.SetSubtitle(toolbar.UnavailableSubtitle(tr))
	}
	e.rows[r.Index] = row
	if r.Fixed {
		row.SetSensitive(false)
		return row
	}
	handle := gtk.NewImageFromIconName("list-drag-handle-symbolic")
	handle.AddCSSClass("dim-label")
	row.AddPrefix(handle)
	e.handles[r.Index] = handle
	remove := gtk.NewButtonFromIconName("list-remove-symbolic")
	remove.SetTooltipText(toolbar.RemoveTitle(tr))
	remove.SetVAlign(gtk.AlignCenter)
	remove.AddCSSClass("flat")
	index := r.Index
	remove.ConnectClicked(func() { e.afterDrag(func() { e.remove(index) }) })
	row.AddSuffix(remove)
	e.addRowKeys(row, index)
	e.addRowDrag(row, handle, index)
	return row
}

// addRowKeys binds Ctrl+↑/↓ on a row.
func (e *toolbarEditor) addRowKeys(row *adw.ActionRow, index int) {
	sc := gtk.NewShortcutController()
	sc.SetScope(gtk.ShortcutScopeLocal)
	for _, k := range []struct {
		trigger string
		up      bool
	}{{"<Control>Up", true}, {"<Control>Down", false}} {
		up := k.up
		sc.AddShortcut(gtk.NewShortcut(
			gtk.NewShortcutTriggerParseString(k.trigger),
			gtk.NewCallbackAction(func(gtk.Widgetter, *glib.Variant) bool { return e.step(index, up) }),
		))
	}
	row.AddController(sc)
	e.keys[index] = sc
}

// step moves the row at index one place (Ctrl+↑/↓) and reports whether it
// moved; the moved row keeps the keyboard.
func (e *toolbarEditor) step(index int, up bool) bool {
	if e.rebuildQueued || e.dragging {
		return true // the rows are stale until they are drawn again
	}
	var to int
	var ok bool
	if up {
		to, ok = e.ed.MoveUp(index)
	} else {
		to, ok = e.ed.MoveDown(index)
	}
	if ok {
		e.commit(to)
	}
	return ok
}

// addRowDrag makes the row draggable by its handle and a drop target.
func (e *toolbarEditor) addRowDrag(row *adw.ActionRow, handle *gtk.Image, index int) {
	bar := e.ed.Bar()
	src := gtk.NewDragSource()
	src.SetActions(gdk.ActionMove)
	src.ConnectPrepare(func(x, y float64) *gdk.ContentProvider {
		if !onWidget(handle, row, x, y) {
			return nil
		}
		return gdk.NewContentProviderForValue(coreglib.NewValue(toolbarDragPrefix + string(bar) + ":" + strconv.Itoa(index)))
	})
	src.ConnectDragBegin(func(gdk.Dragger) {
		e.dragging = true
		src.SetIcon(gtk.NewWidgetPaintable(row), 0, 0)
	})
	src.ConnectDragEnd(func(gdk.Dragger, bool) { e.dragEnded() })
	src.ConnectDragCancel(func(gdk.Dragger, gdk.DragCancelReason) bool {
		e.dragEnded()
		return false
	})
	row.AddController(src)

	dst := gtk.NewDropTarget(coreglib.TypeString, gdk.ActionMove)
	dst.SetPreload(true)
	target := func(v *coreglib.Value, y float64) (from, to int, ok bool) {
		from, ok = e.dragged(v)
		if !ok {
			return 0, 0, false
		}
		to = insertIndex(from, index, y < float64(row.Height())/2)
		return from, to, e.ed.CanDrop(from, to)
	}
	dst.ConnectMotion(func(_, y float64) gdk.DragAction {
		if _, _, ok := target(dst.Value(), y); ok {
			return gdk.ActionMove
		}
		return 0
	})
	dst.ConnectDrop(func(v *coreglib.Value, _, y float64) bool {
		from, to, ok := target(v, y)
		if !ok {
			return false
		}
		e.afterDrag(func() { e.drop(from, to) })
		return true
	})
	row.AddController(dst)
}

// dragged reads a drag payload of this editor's bar: the row's index.
func (e *toolbarEditor) dragged(v *coreglib.Value) (int, bool) {
	if v == nil || v.Type() != coreglib.TypeString {
		return 0, false
	}
	s, ok := strings.CutPrefix(v.String(), toolbarDragPrefix+string(e.ed.Bar())+":")
	if !ok {
		return 0, false
	}
	i, err := strconv.Atoi(s)
	return i, err == nil
}

// remove takes the row at index off the toolbar.
func (e *toolbarEditor) remove(index int) bool {
	if e.rebuildQueued || !e.ed.Remove(index) {
		return false
	}
	e.commit(-1)
	return true
}

// add puts id (an item or a space) at the end of section s.
func (e *toolbarEditor) add(id toolbar.ID, s toolbar.Section) bool {
	if !e.ed.Add(id, s) {
		return false
	}
	e.commit(-1)
	return true
}

// drop moves the row at from to insertion index to.
func (e *toolbarEditor) drop(from, to int) {
	if at, ok := e.ed.Drop(from, to); ok {
		e.commit(at)
	}
}

// onWidget reports whether (x, y) of parent is on w.
func onWidget(w, parent gtk.Widgetter, x, y float64) bool {
	bounds, ok := gtk.BaseWidget(w).ComputeBounds(parent)
	if !ok {
		return false
	}
	return x >= float64(bounds.X()) && x <= float64(bounds.X()+bounds.Width()) &&
		y >= float64(bounds.Y()) && y <= float64(bounds.Y()+bounds.Height())
}

// newAddRow is a section's last row: Add Item…, a menu of what the section
// may take and the two spaces; a row dropped on it goes to the section's end.
func (e *toolbarEditor) newAddRow(s toolbar.Section) *adw.ActionRow {
	tr := i18n.Tr
	bar := e.ed.Bar()
	row := adw.NewActionRow()
	row.SetUseMarkup(false)
	add := gtk.NewMenuButton()
	content := adw.NewButtonContent()
	content.SetIconName("list-add-symbolic")
	content.SetLabel(toolbar.AddItemTitle(tr))
	content.SetUseUnderline(false)
	add.SetChild(content)
	add.AddCSSClass("flat")
	add.SetVAlign(gtk.AlignCenter)
	row.AddPrefix(add)
	row.SetActivatableWidget(add)

	group := gio.NewSimpleActionGroup()
	a := gio.NewSimpleAction("add", glib.NewVariantType("s"))
	a.ConnectActivate(func(v *glib.Variant) {
		if v == nil {
			return
		}
		id := toolbar.ID(v.String())
		e.afterDrag(func() { e.add(id, s) })
	})
	group.AddAction(a)
	add.InsertActionGroup("tbedit", group)
	add.SetCreatePopupFunc(func(*gtk.MenuButton) {
		m := gio.NewMenu()
		items := gio.NewMenu()
		for _, id := range e.ed.Available(s) {
			it := gio.NewMenuItem(toolbar.ItemLabel(tr, bar, id), "")
			it.SetActionAndTargetValue("tbedit.add", glib.NewVariantString(string(id)))
			items.AppendItem(it)
		}
		m.AppendSection("", items)
		spaces := gio.NewMenu()
		for _, id := range []toolbar.ID{toolbar.Space, toolbar.FlexibleSpace} {
			it := gio.NewMenuItem(toolbar.ItemLabel(tr, bar, id), "")
			it.SetActionAndTargetValue("tbedit.add", glib.NewVariantString(string(id)))
			spaces.AppendItem(it)
		}
		m.AppendSection("", spaces)
		add.SetMenuModel(m)
	})

	dst := gtk.NewDropTarget(coreglib.TypeString, gdk.ActionMove)
	dst.SetPreload(true)
	target := func(v *coreglib.Value) (from, to int, ok bool) {
		from, ok = e.dragged(v)
		if !ok {
			return 0, 0, false
		}
		to = e.ed.DropAtEnd(from, s)
		return from, to, to >= 0 && e.ed.CanDrop(from, to)
	}
	dst.ConnectMotion(func(_, _ float64) gdk.DragAction {
		if _, _, ok := target(dst.Value()); ok {
			return gdk.ActionMove
		}
		return 0
	})
	dst.ConnectDrop(func(v *coreglib.Value, _, _ float64) bool {
		from, to, ok := target(v)
		if !ok {
			return false
		}
		e.afterDrag(func() { e.drop(from, to) })
		return true
	})
	row.AddController(dst)
	return row
}

// askRestore asks before Restore Defaults and writes the empty list.
func (e *toolbarEditor) askRestore() {
	tr := i18n.Tr
	bar := e.ed.Bar()
	q := adw.NewAlertDialog(toolbar.RestoreQuestion(tr), toolbar.RestoreBody(tr, bar))
	q.AddResponse("cancel", toolbar.RestoreCancel(tr))
	q.AddResponse("restore", toolbar.RestoreTitle(tr))
	q.SetResponseAppearance("restore", adw.ResponseDestructive)
	q.SetDefaultResponse("cancel")
	q.SetCloseResponse("cancel")
	q.ConnectResponse(func(r string) {
		if r == "restore" && e.ed.Bar() == bar {
			e.restoreDefaults()
		}
	})
	q.Present(e.dialog)
}

// restoreDefaults is the confirmed Restore Defaults.
func (e *toolbarEditor) restoreDefaults() {
	e.write(e.ed.Reset())
	e.queueRebuild()
}
