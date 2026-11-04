// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"log/slog"
	"os"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendardata"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/toolbar"
)

// Run in a dedicated test process with a display (gtk.sh sh -c with
// xvfb-run): MALACHI_GTK_SMOKE=1 go test ./internal/window -run
// '^TestToolbarGTKSmoke$' -count=1 -v. Builds the real Blueprints over an
// in-memory settings store, no daemon: the untouched header bars of Mail,
// the Board (List and Columns) and the Calendar item by item as today, a
// custom arrangement for each bar, the display modes, the overflow with
// More Actions, the empty-spot test of the context menu, the editor
// dialog (move, remove, add, Restore Defaults, the bar chooser) and the
// Toolbar group of Preferences → General.
func TestToolbarGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	st := settings.NewMemory()
	wb := data.Builder("window.ui")
	w := &Window{
		ApplicationWindow: wb.GetObject("main_window").Cast().(*adw.ApplicationWindow),
		log:               slog.Default(), mode: board.ModeMail,
		settings:          st,
		actions:           make(map[string]*gio.SimpleAction),
		modeSwitchBoardOn: true,
		toasts:            adw.NewToastOverlay(),
		dateWeekStart:     time.Monday,
		modeStack:         wb.GetObject("mode_stack").Cast().(*gtk.Stack),
		modeSwitchMail:    wb.GetObject("mode_switch").Cast().(*adw.ToggleGroup),
		boardPageBin:      wb.GetObject("board_page_bin").Cast().(*adw.Bin),
		calendarPageBin:   wb.GetObject("calendar_page_bin").Cast().(*adw.Bin),
		outerSplit:        wb.GetObject("outer_split").Cast().(*adw.NavigationSplitView),
		innerSplit:        wb.GetObject("inner_split").Cast().(*adw.NavigationSplitView),
		refreshButton:     wb.GetObject("refresh_button").Cast().(*gtk.Button),
		searchButton:      wb.GetObject("search_button").Cast().(*gtk.ToggleButton),
		messageList:       wb.GetObject("message_list").Cast().(*gtk.ListBox),
		starButton:        wb.GetObject("star_button").Cast().(*gtk.ToggleButton),
		archiveButton:     wb.GetObject("archive_button").Cast().(*gtk.Button),
		trashButton:       wb.GetObject("trash_button").Cast().(*gtk.Button),
		messageMenu:       wb.GetObject("message_menu").Cast().(*gtk.MenuButton),
		assistButton:      wb.GetObject("assistant_button").Cast().(*gtk.MenuButton),
		replyButton:       wb.GetObject("reply_button").Cast().(*gtk.Button),
		replyAllButton:    wb.GetObject("reply_all_button").Cast().(*gtk.Button),
		forwardButton:     wb.GetObject("forward_button").Cast().(*gtk.Button),
	}
	w.bindStatusBar(wb)
	defer w.Destroy()
	w.registerActions()
	w.setupMailToolbar(wb)
	w.SetDefaultSize(1400, 900)
	w.Present()
	flush := func() {
		ctx := glib.MainContextDefault()
		for i := 0; i < 300 && ctx.Pending(); i++ {
			ctx.Iteration(false)
		}
	}
	wait := func(d time.Duration) {
		end := time.Now().Add(d)
		ctx := glib.MainContextDefault()
		for time.Now().Before(end) {
			ctx.Iteration(false)
			time.Sleep(5 * time.Millisecond)
		}
		flush()
	}
	wait(300 * time.Millisecond)

	mt := w.mailBar
	mailHeaders := map[toolbar.Section]*tbHeader{
		toolbar.SectionSidebar: mt.folders, toolbar.SectionList: mt.list, toolbar.SectionMessage: mt.message,
	}
	// The untouched toolbar: today's header bars, fixed widgets at the end.
	checkHeader(t, "mail folders", mt.set, mt.folders, "mode newMessage", "")
	checkHeader(t, "mail list", mt.set, mt.list, "refresh", "search")
	checkHeader(t, "mail message", mt.set, mt.message, "reply replyAll forward", "archive trash crm star assistant moreActions")
	if got := w.trashButton.MarginEnd(); got != toolbarGap {
		t.Errorf("trash's gap is %d, want %d (the Space of the default)", got, toolbarGap)
	}
	for _, id := range tbIDs("contacts markRead junk moveTo undo loadPictures") {
		if mt.set.packed(id) {
			t.Errorf("%s is packed in the untouched toolbar", id)
		}
	}
	checkLastIsFixed(t, "mail message", mt.message, mt.set)

	// A custom arrangement moves items between the header bars; what it
	// leaves out has no parent and its action lives on.
	st.SetToolbarLayout(toolbar.BarMail, []string{
		"mode", "contacts", "flexibleSpace", "newMessage",
		"|list", "search", "refresh", "markRead",
		"|message", "reply", "junk", "space", "moveTo", "flexibleSpace", "undo", "loadPictures", "star", "moreActions",
	})
	flush()
	checkHeader(t, "custom folders", mt.set, mt.folders, "mode contacts", "newMessage")
	// Mark as Read may not stand in the list: Normalize puts it at the end
	// of its home, the message section.
	checkHeader(t, "custom list", mt.set, mt.list, "search refresh", "")
	checkHeader(t, "custom message", mt.set, mt.message, "reply junk moveTo", "undo loadPictures star moreActions markRead")
	for _, id := range tbIDs("archive trash replyAll forward crm assistant") {
		if mt.set.packed(id) {
			t.Errorf("%s left out is still packed", id)
		}
	}
	if gtk.BaseWidget(w.archiveButton).Parent() != nil || w.actions["archive"] == nil {
		t.Error("Archive left out has a parent or lost its action")
	}
	if got := mt.junk.MarginEnd(); got != toolbarGap {
		t.Errorf("junk's gap is %d", got)
	}
	if got := w.trashButton.MarginEnd(); got != 0 {
		t.Errorf("the unpacked trash kept a gap of %d", got)
	}
	// More Actions adds what is missing: Reply All, Forward, Archive, Trash
	// (eWay-CRM has its own section).
	w.fillMoreActions()
	if got, want := menuActions(mt.extra), []string{"win.reply-all", "win.forward", "win.archive", "win.trash"}; !reflect.DeepEqual(got, want) {
		t.Errorf("More Actions adds %v, want %v", got, want)
	}

	// Display modes on a faced button.
	st.SetToolbarDisplayMode(toolbar.DisplayIconAndLabel)
	flush()
	if c := w.refreshButton.Child(); c == nil || gtk.BaseWidget(c).CSSName() != "buttoncontent" {
		t.Errorf("Icons and Text: refresh's child is %v", c)
	}
	setStar(w.starButton, true)
	if c := w.starButton.Child(); c == nil || gtk.BaseWidget(c).CSSName() != "buttoncontent" ||
		toolbarFaces[faceKey(w.starButton)].icon != "starred-symbolic" {
		t.Error("setStar lost the label of Icons and Text")
	}
	st.SetToolbarDisplayMode(toolbar.DisplayLabel)
	flush()
	if got := w.refreshButton.Label(); got != toolbar.ItemLabel(i18n.Tr, toolbar.BarMail, toolbar.Refresh) {
		t.Errorf("Text Only: refresh shows %q", got)
	}
	st.SetToolbarDisplayMode(toolbar.DisplayIcon)
	flush()
	if got := w.refreshButton.IconName(); got != "view-refresh-symbolic" {
		t.Errorf("Icons Only: refresh shows icon %q", got)
	}

	// The overflow: a narrow message column leaves the low priorities out
	// and More Actions offers them; room again brings them back.
	st.SetToolbarLayout(toolbar.BarMail, nil)
	flush()
	wait(100 * time.Millisecond)
	min, _, _, _ := mt.message.header.Measure(gtk.OrientationHorizontal, -1)
	mt.message.column = func() int { return min - 30 }
	mt.set.fit(mt.message)
	flush()
	over := mt.set.overflowed()
	if len(over) == 0 {
		t.Fatalf("a message column of %d px (min %d) overflowed nothing", min-30, min)
	}
	for _, id := range over {
		if mt.set.packed(id) {
			t.Errorf("overflowed %s is packed", id)
		}
	}
	if mt.set.packed(toolbar.MoreActions) != true {
		t.Error("More Actions (high priority) went before the others")
	}
	w.fillMoreActions()
	if len(menuActions(mt.extra)) == 0 {
		t.Error("More Actions does not offer the overflowed actions")
	}
	t.Logf("LAYOUT: overflow at %d px: %v, More Actions adds %v", min-30, over, menuActions(mt.extra))
	mt.message.column = func() int { return 5000 }
	mt.set.fit(mt.message)
	flush()
	if over := mt.set.overflowed(); len(over) != 0 {
		t.Errorf("a wide column still overflows %v", over)
	}
	checkHeader(t, "mail message after the overflow", mt.set, mt.message, "reply replyAll forward", "archive trash crm star assistant moreActions")
	mt.message.column = func() int { _, _, m := w.mailColumnWidths(); return m }

	// The context menu's empty spots (T15).
	wait(100 * time.Millisecond)
	smokeEmptySpot(t, mt.list.header, w.refreshButton)

	smokeToolbarBoard(t, w, st, flush, wait)
	smokeToolbarCalendar(t, w, st, flush, wait)
	smokeToolbarContactsMoves(t, w, st, flush, wait)
	smokeToolbarEditor(t, w, st, flush, mailHeaders)
	smokeToolbarPreferences(t, st, flush)
}

// checkHeader checks the item widgets of h's header bar: those of start
// in order in one box, those of end in order in another.
func checkHeader(t *testing.T, name string, s *toolbarSet, h *tbHeader, start, end string) {
	t.Helper()
	gotStart, gotEnd := headerOrder(s, h)
	if !reflect.DeepEqual(gotStart, tbIDs(start)) || !reflect.DeepEqual(gotEnd, tbIDs(end)) {
		t.Errorf("%s: start %v end %v, want %q | %q", name, gotStart, gotEnd, start, end)
	}
}

// headerOrder reads the items packed in h's header bar in screen order,
// per box of the header bar (the start box first: the one that holds the
// plan's first start item).
func headerOrder(s *toolbarSet, h *tbHeader) (start, end []toolbar.ID) {
	owner := map[uintptr]toolbar.ID{}
	for id, it := range s.items {
		for _, w := range it.widgets {
			owner[faceKey(w)] = id
		}
	}
	read := func(box gtk.Widgetter) []toolbar.ID {
		var out []toolbar.ID
		for c := gtk.BaseWidget(box).FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
			if id, ok := owner[faceKey(c)]; ok && (len(out) == 0 || out[len(out)-1] != id) {
				out = append(out, id)
			}
		}
		return out
	}
	boxOf := func(ids []toolbar.ID) gtk.Widgetter {
		for _, id := range ids {
			if it := s.items[id]; it != nil && s.packed(id) && gtk.BaseWidget(it.widgets[0]).IsAncestor(h.header) {
				return gtk.BaseWidget(it.widgets[0]).Parent()
			}
		}
		return nil
	}
	sb, eb := boxOf(h.pack.Start), boxOf(h.pack.End)
	if sb != nil {
		start = read(sb)
	}
	if eb != nil {
		if sb != nil && faceKey(sb) == faceKey(eb) {
			return start, nil
		}
		end = read(eb)
	}
	return start, end
}

// checkLastIsFixed checks that the last child of h's end box is a widget
// the catalog does not have (the fixed end of the bar).
func checkLastIsFixed(t *testing.T, name string, h *tbHeader, s *toolbarSet) {
	t.Helper()
	if len(h.pack.End) == 0 {
		return
	}
	it := s.items[h.pack.End[len(h.pack.End)-1]]
	box := gtk.BaseWidget(it.widgets[0]).Parent()
	last := gtk.BaseWidget(box).LastChild()
	for _, x := range s.items {
		for _, w := range x.widgets {
			if faceKey(w) == faceKey(last) {
				t.Errorf("%s: the end box ends with item %s, not a fixed widget", name, x.id)
			}
		}
	}
}

// menuActions are the action names of m's items.
func menuActions(m *gio.Menu) []string {
	var out []string
	for i := 0; i < m.NItems(); i++ {
		if v := m.ItemAttributeValue(i, "action", glib.NewVariantType("s")); v != nil {
			out = append(out, v.String())
		} else {
			out = append(out, "")
		}
	}
	return out
}

func smokeEmptySpot(t *testing.T, hb *adw.HeaderBar, button gtk.Widgetter) {
	t.Helper()
	b, ok := gtk.BaseWidget(button).ComputeBounds(hb)
	if !ok {
		bw := gtk.BaseWidget(button)
		t.Errorf("the button has no bounds in its header bar (mapped %v, %dx%d, header mapped %v %dx%d, ancestor %v)",
			bw.Mapped(), bw.Width(), bw.Height(), hb.Mapped(), hb.Width(), hb.Height(), bw.IsAncestor(hb))
		return
	}
	x, y := float64(b.X()+b.Width()/2), float64(b.Y()+b.Height()/2)
	if onEmptySpot(hb.Pick(x, y, gtk.PickDefault), hb) {
		t.Error("a click on a button counts as an empty spot")
	}
	// The middle of the header bar is its title.
	mx, my := float64(hb.Width())/2, float64(hb.Height())/2
	if !onEmptySpot(hb.Pick(mx, my, gtk.PickDefault), hb) {
		t.Errorf("the header bar's title at %.0f,%.0f is no empty spot", mx, my)
	}
}

func smokeToolbarBoard(t *testing.T, w *Window, st *settings.Store, flush func(), wait func(time.Duration)) {
	now := time.Now()
	src := board.NewDummySource(true, now, time.Local, i18n.Tr)
	p := &boardPage{w: w, src: src}
	p.ctl = board.NewController(src, board.ControllerOptions{
		Env: board.Env{Tr: i18n.Tr, Dates: boardEnv{}, Loc: time.Local},
		Now: func() time.Time { return now },
	})
	b := data.Builder("board_page.ui")
	p.bind(b)
	p.bindColumns(b)
	p.bindToday(b)
	p.bindPanel(b)
	w.boardPage = p
	w.registerBoardActions()
	p.wire()
	p.wireDetailHeader()
	p.wireMenuButton()
	p.wireToday()
	p.wireKeys()
	p.wirePanel()
	p.wireTriage()
	p.wireToolbar(b)
	p.ctl.OnChange = p.onChange
	w.boardPageBin.SetChild(p.root)
	w.mode = board.ModeBoard
	w.modeStack.SetVisibleChildName(board.ModeBoard.Nick())
	p.ctl.SetStyle(board.StyleList)
	p.applyInlineDetail()
	p.applyAll()
	wait(200 * time.Millisecond)
	bt := p.toolbar
	checkHeader(t, "board list", bt.set, bt.header, "mode boardStyle", "boardTriage boardAccount")
	checkHeader(t, "board detail", bt.set, bt.detail, "boardDone boardRemind boardArchive boardTrash", "boardReply")
	checkLastIsFixed(t, "board list", bt.header, bt.set)
	checkLastIsFixed(t, "board detail", bt.detail, bt.set)
	if got := p.doneButton.MarginStart(); got != 18 {
		t.Errorf("Done's inset is %d", got)
	}
	if w.currentToolbarBar() != toolbar.BarBoardList {
		t.Error("the List's bar is not the window's")
	}
	// Columns: board-flow in the shared header, the detail stays List's.
	p.ctl.SetStyle(board.StyleColumns)
	p.applyInlineDetail()
	p.applyAll()
	flush()
	checkHeader(t, "board flow", bt.set, bt.header, "mode boardStyle", "boardTriage boardAccount")
	if w.currentToolbarBar() != toolbar.BarBoardFlow {
		t.Error("the Columns' bar is not board-flow")
	}
	st.SetToolbarLayout(toolbar.BarBoardFlow, []string{"mode", "boardAccount", "contacts", "flexibleSpace", "boardStyle"})
	flush()
	checkHeader(t, "custom board flow", bt.set, bt.header, "mode boardAccount contacts", "boardStyle")
	if bt.set.packed(toolbar.BoardTriage) {
		t.Error("Triage left out of board-flow is packed")
	}
	st.SetToolbarLayout(toolbar.BarBoardList, []string{"mode", "flexibleSpace", "|list", "boardStyle",
		"|detail", "boardReply", "boardRemind", "flexibleSpace", "boardDone"})
	flush()
	checkHeader(t, "custom detail in Columns", bt.set, bt.detail, "boardReply boardRemind", "boardDone")
	if got, want := p.replyButton.MarginStart(), 18; got != want {
		t.Errorf("the first detail item's inset is %d, want %d", got, want)
	}
	if got := p.doneButton.MarginStart(); got != 0 {
		t.Errorf("Done not first kept an inset of %d", got)
	}
	p.ctl.SetStyle(board.StyleList)
	p.applyInlineDetail()
	p.applyAll()
	flush()
	checkHeader(t, "custom board list", bt.set, bt.header, "mode", "boardStyle")
	st.SetToolbarLayout(toolbar.BarBoardList, nil)
	st.SetToolbarLayout(toolbar.BarBoardFlow, nil)
	flush()
	checkHeader(t, "board list again", bt.set, bt.header, "mode boardStyle", "boardTriage boardAccount")
	w.mode = board.ModeMail
	w.modeStack.SetVisibleChildName(board.ModeMail.Nick())
	flush()
}

func smokeToolbarCalendar(t *testing.T, w *Window, st *settings.Store, flush func(), wait func(time.Duration)) {
	w.cal.source = calendardata.NewSampleSource(time.Now(), time.Local, glibLoop{})
	w.ensureCalendar()
	p := w.cal.page
	w.mode = board.ModeCalendar
	w.modeStack.SetVisibleChildName(board.ModeCalendar.Nick())
	wait(200 * time.Millisecond)
	ct := p.toolbar
	checkHeader(t, "calendar", ct.set, ct.header, "mode toggleSidebar calendarToday calendarStep calendarView", "calendarSearch calendarNewEvent calendarAssistant")
	checkLastIsFixed(t, "calendar", ct.header, ct.set)
	st.SetToolbarLayout(toolbar.BarCalendar, []string{"mode", "|main", "calendarNewEvent", "calendarToday", "flexibleSpace", "contacts", "space", "calendarStep"})
	flush()
	checkHeader(t, "custom calendar", ct.set, ct.header, "mode calendarNewEvent calendarToday", "contacts calendarStep")
	if got := ct.contacts.MarginEnd(); got != toolbarGap {
		t.Errorf("contacts' gap is %d", got)
	}
	if ct.set.packed(toolbar.CalendarView) || gtk.BaseWidget(p.viewMenu).Parent() != nil {
		t.Error("the view switch left out is packed")
	}
	st.SetToolbarLayout(toolbar.BarCalendar, nil)
	flush()
	checkHeader(t, "calendar again", ct.set, ct.header, "mode toggleSidebar calendarToday calendarStep calendarView", "calendarSearch calendarNewEvent calendarAssistant")
	w.mode = board.ModeMail
	w.modeStack.SetVisibleChildName(board.ModeMail.Nick())
	flush()
}

func smokeToolbarEditor(t *testing.T, w *Window, st *settings.Store, flush func(), headers map[toolbar.Section]*tbHeader) {
	e := newToolbarEditor(st, w.toolbarFeatures, toolbar.BarMail)
	e.dialog.Present(w)
	flush()
	defer e.dialog.ForceClose()
	if len(e.groups) != 3 {
		t.Fatalf("the Mail editor has %d sections", len(e.groups))
	}
	if e.restore.Sensitive() {
		t.Error("Restore Defaults is sensitive over the default")
	}
	index := func(id toolbar.ID) int {
		for i, x := range e.ed.Layout() {
			if x == id {
				return i
			}
		}
		return -1
	}
	key := func() []string { return st.ToolbarLayout(toolbar.BarMail) }
	// Ctrl+↓ moves Refresh past the flexible space: the key follows.
	if !e.step(index(toolbar.Refresh), false) {
		t.Fatal("Refresh did not move down")
	}
	flush()
	want := toolbar.StoredFor(gtkVariant, toolbar.BarMail, nil, e.ed.Layout())
	if got := key(); !reflect.DeepEqual(got, want) || len(got) == 0 {
		t.Errorf("after the move the key is %v, want %v", got, want)
	}
	checkHeader(t, "list after the editor's move", w.mailBar.set, headers[toolbar.SectionList], "", "refresh search")
	// Remove Archive, add Contacts to the list and a space to the message.
	if !e.remove(index(toolbar.Archive)) {
		t.Fatal("Archive was not removed")
	}
	if !e.add(toolbar.Contacts, toolbar.SectionList) || !e.add(toolbar.Space, toolbar.SectionMessage) {
		t.Fatal("Contacts or a space was not added")
	}
	flush()
	stored := key()
	l := toolbar.NormalizeFor(gtkVariant, toolbar.BarMail, stored)
	if index(toolbar.Archive) >= 0 || toolbar.SectionAt(toolbar.BarMail, l, indexIn(l, toolbar.Contacts)) != toolbar.SectionList {
		t.Errorf("the key after remove and add: %v", stored)
	}
	// The items GTK lacks survive the edit (T12).
	for _, id := range []string{"filter", "toggleSidebar"} {
		if indexIn(toolbar.Layout(tbIDs(joinTokens(stored))), toolbar.ID(id)) < 0 {
			t.Errorf("%s of the shared key did not survive the edit: %v", id, stored)
		}
	}
	if w.mailBar.set.packed(toolbar.Archive) || !w.mailBar.set.packed(toolbar.Contacts) {
		t.Error("the window did not follow the editor")
	}
	if !e.restore.Sensitive() {
		t.Error("Restore Defaults is insensitive over a custom toolbar")
	}
	// A fixed item never moves; a drop where an item may not stand does nothing.
	if e.step(index(toolbar.Mode), false) {
		t.Error("the mode switch moved")
	}
	if e.ed.CanDrop(index(toolbar.Reply), e.ed.DropAtEnd(index(toolbar.Reply), toolbar.SectionList)) {
		t.Error("Reply may be dropped into the list section")
	}
	// A change of the key from elsewhere reloads the rows.
	st.SetToolbarLayout(toolbar.BarMail, []string{"mode", "|list", "refresh", "|message", "reply"})
	flush()
	if index(toolbar.Archive) >= 0 || index(toolbar.Reply) < 0 || index(toolbar.Contacts) >= 0 {
		t.Errorf("the editor did not follow the key: %v", e.ed.Layout())
	}
	// Restore Defaults writes the empty list.
	e.restoreDefaults()
	flush()
	if got := key(); len(got) != 0 {
		t.Errorf("Restore Defaults wrote %v", got)
	}
	checkHeader(t, "mail message after Restore Defaults", w.mailBar.set, headers[toolbar.SectionMessage], "reply replyAll forward", "archive trash crm star assistant moreActions")
	// The bar chooser.
	e.bars.SetActiveName(string(toolbar.BarCalendar))
	flush()
	if e.ed.Bar() != toolbar.BarCalendar || len(e.groups) != 2 {
		t.Errorf("the chooser shows %s with %d sections", e.ed.Bar(), len(e.groups))
	}
	if !e.add(toolbar.Contacts, toolbar.SectionMain) {
		t.Error("Contacts could not be added to the Calendar")
	}
	flush()
	if got := st.ToolbarLayout(toolbar.BarCalendar); len(got) == 0 || len(st.ToolbarLayout(toolbar.BarMail)) != 0 {
		t.Errorf("the Calendar's edit wrote %v (mail %v)", got, st.ToolbarLayout(toolbar.BarMail))
	}
	st.SetToolbarLayout(toolbar.BarCalendar, nil)
	flush()
}

func smokeToolbarPreferences(t *testing.T, st *settings.Store, flush func()) {
	b := data.Builder("preferences.ui")
	d := &PreferencesDialog{PreferencesDialog: b.GetObject("preferences_dialog").Cast().(*adw.PreferencesDialog)}
	unbind := d.bindToolbar(b, st)
	defer unbind()
	if d.toolbarGroup == nil || d.toolbarGroup.Title() != toolbar.GroupTitle(i18n.Tr) {
		t.Fatal("no Toolbar group on the General page")
	}
	page := b.GetObject("general_page").Cast().(*adw.PreferencesPage)
	if !gtk.BaseWidget(d.toolbarGroup).IsAncestor(page) {
		t.Error("the Toolbar group is not on the General page")
	}
	combo := d.toolbarMode
	combo.SetSelected(1)
	flush()
	if got := st.ToolbarDisplayMode(); got != toolbar.DisplayIconAndLabel {
		t.Errorf("the row wrote %q", got)
	}
	st.SetToolbarDisplayMode(toolbar.DisplayIcon)
	flush()
	if combo.Selected() != 0 {
		t.Error("the row did not follow the key")
	}
}

func indexIn(l toolbar.Layout, id toolbar.ID) int {
	for i, x := range l {
		if x == id {
			return i
		}
	}
	return -1
}

func joinTokens(s []string) string {
	out := ""
	for _, x := range s {
		out += x + " "
	}
	return out
}

// smokeToolbarContactsMoves is the owner's crash of 2026-11-04 (moving
// Contacts within the first part of the Mail toolbar in the editor): the
// item walks through the folders' and the list's sections by Ctrl+↓ and
// Ctrl+↑ run through the rows' own shortcut controllers as a key press
// runs them, then by every drop the editor allows, and such layouts are
// also applied straight to the key, with frames in between so the
// overflow's tick runs too. Run it with G_DEBUG=fatal-criticals.
func smokeToolbarContactsMoves(t *testing.T, w *Window, st *settings.Store, flush func(), wait func(time.Duration)) {
	t.Helper()
	st.SetToolbarLayout(toolbar.BarMail, []string{"mode", "contacts", "newMessage", "flexibleSpace",
		"|list", "refresh", "flexibleSpace", "search", "|message", "reply", "flexibleSpace", "archive", "moreActions"})
	flush()
	e := newToolbarEditor(st, w.toolbarFeatures, toolbar.BarMail)
	e.dialog.Present(w)
	defer e.dialog.ForceClose()
	wait(100 * time.Millisecond)
	at := func() int { return indexIn(e.ed.Layout(), toolbar.Contacts) }
	press := func(up bool) bool {
		i := at()
		sc := e.keys[i]
		if sc == nil {
			t.Fatalf("Contacts' row %d has no keys", i)
		}
		row := e.rows[i]
		n := 1
		if up {
			n = 0
		}
		sh := sc.Item(uint(n)).Cast().(*gtk.Shortcut)
		ok := gtk.BaseShortcutAction(sh.Action()).Activate(0, row, nil)
		wait(30 * time.Millisecond)
		return ok
	}
	sec := func() toolbar.Section { return toolbar.SectionAt(toolbar.BarMail, e.ed.Layout(), at()) }
	check := func(step string) {
		t.Helper()
		l := toolbar.NormalizeFor(gtkVariant, toolbar.BarMail, st.ToolbarLayout(toolbar.BarMail))
		if indexIn(l, toolbar.Contacts) != at() {
			t.Fatalf("%s: the key %v is not the editor's %v", step, l, e.ed.Layout())
		}
		h := w.mailBar.list
		if sec() == toolbar.SectionSidebar {
			h = w.mailBar.folders
		}
		if !gtk.BaseWidget(w.mailBar.contacts).IsAncestor(h.header) {
			t.Fatalf("%s: Contacts in %s is not in its header bar", step, sec())
		}
	}
	for i := 0; i < 12 && press(false); i++ {
		check("Ctrl+Down")
	}
	if sec() != toolbar.SectionList {
		t.Errorf("Ctrl+Down did not take Contacts into the list (%s)", sec())
	}
	for i := 0; i < 12 && press(true); i++ {
		check("Ctrl+Up")
	}
	if sec() != toolbar.SectionSidebar {
		t.Errorf("Ctrl+Up did not take Contacts back to the folders (%s)", sec())
	}
	// Every drop the editor allows, as the drop target does it (idle).
	for to := 0; to < len(e.ed.Layout()); to++ {
		from := at()
		if !e.ed.CanDrop(from, to) {
			continue
		}
		idle(func() { e.drop(from, to) })
		wait(30 * time.Millisecond)
		check("drop")
	}
	for _, s := range []toolbar.Section{toolbar.SectionList, toolbar.SectionSidebar, toolbar.SectionList} {
		from := at()
		to := e.ed.DropAtEnd(from, s)
		idle(func() { e.drop(from, to) })
		wait(30 * time.Millisecond)
		check("drop at the end")
	}
	// The same layouts straight into the key, the editor open.
	for _, l := range [][]string{
		{"mode", "flexibleSpace", "|list", "contacts", "refresh", "search"},
		{"mode", "contacts", "|list", "refresh", "flexibleSpace", "search"},
		{"mode", "newMessage", "contacts", "flexibleSpace", "|list", "contacts", "refresh"},
		{"mode", "|list", "refresh", "flexibleSpace", "search", "contacts"},
		{"mode", "contacts", "flexibleSpace", "|list", "search"},
	} {
		st.SetToolbarLayout(toolbar.BarMail, l)
		wait(30 * time.Millisecond)
		check("direct")
	}
	st.SetToolbarLayout(toolbar.BarMail, nil)
	wait(30 * time.Millisecond)
}
