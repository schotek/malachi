// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/accountwizard"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
)

// A development aid, not a feature (the macOS client's MALACHI_START,
// MainWindowController+DevStart.swift): MALACHI_GTK_START is a script of
// steps separated by ";" that the main window runs once it shows, with
// MALACHI_GTK_START_INTERVAL seconds between them (default 3), so that a
// layout can be checked under Broadway without clicking. After each step
// it prints "MALACHI_GTK_START step <n> <name>" and LAYOUT: lines to
// stderr. Without the variable nothing runs. Only for an isolated
// instance (MALACHI_DATA_DIR, MALACHI_SOCKET, the samples), never over a
// real store.
//
// Steps: mail, board, calendar, calendar:day|week|month|agenda,
// event-popover (the detail of the shown range's first occurrence),
// event-editor (New Event with today's defaults), event-editor:edit (Edit
// of the first occurrence), caldav-dialog, prefs-calendar (Preferences on
// the Calendar page), prefs-integrations (Preferences on the Integrations
// page), prefs-ai (Preferences on the AI page, About You on top),
// contacts (the Contacts window), contacts:edit (the window with the
// first listed contact's form open), prefs-contacts (Preferences on the
// Contacts page), crm-dialog (the Save to eWay-CRM dialog over invented
// suggestions, no daemon), eway-wizard (the eWay-CRM account assistant;
// Next asks the daemon), size=WxH, quit.
//
// The scene steps of docs/screenshots.md §3, with the macOS names and
// meaning: group=on|off and chat=on|off (the two settings), folder=<text>
// (the first folder, in the sidebar's account order, whose server name
// contains the text, as a click in the sidebar), row=<text> (the first
// row of the list whose subject contains the text, case-insensitively, as
// a click), board:list, board:columns, board:today (the Board in that
// style; ":select" after it selects the case with the most conversation
// text), compose-open (app.compose), crm-save (win.crm-save over the
// selected message) and shot=<path>: a line "MALACHI_GTK_START shot
// <path>" on stderr that a driver answers with the X server's image of
// the window (scripts/screenshots/gtk.sh).

const (
	devStartEnv         = "MALACHI_GTK_START"
	devStartIntervalEnv = "MALACHI_GTK_START_INTERVAL"
	devStartInterval    = 3 * time.Second
)

// devStepKind is what a step does.
type devStepKind int

const (
	devMail devStepKind = iota
	devBoard
	devCalendar
	devCalendarView
	devEventPopover
	devEventEditor
	devEventEdit
	devCalDAV
	devPrefsCalendar
	devPrefsIntegrations
	devPrefsAI
	devContacts
	devContactsEdit
	devPrefsContacts
	devCrmDialog
	devEwayWizard
	devSize
	devQuit
	devGroup
	devChat
	devFolder
	devRow
	devBoardStyle
	devComposeOpen
	devCrmSave
	devShot
)

// devBusy counts the steps still at work after they returned (the
// conversations board:<style>:select waits for); the next step waits for
// it to drop to 0. Main loop only.
var devBusy int

// devStep is one parsed step; Name is how it was written.
type devStep struct {
	Kind          devStepKind
	Name          string
	View          calendar.View
	Width, Height int
	// On is group= and chat=; Text is folder=, row= and shot=.
	On   bool
	Text string
	// Style and Select are board:<style>[:select].
	Style  board.Style
	Select bool
}

// parseDevStart parses a MALACHI_GTK_START script; empty steps are
// skipped, an unknown one is an error naming it.
func parseDevStart(script string) ([]devStep, error) {
	var out []devStep
	for _, raw := range strings.Split(script, ";") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		st := devStep{Name: name}
		switch {
		case name == "mail":
			st.Kind = devMail
		case name == "board":
			st.Kind = devBoard
		case name == "calendar":
			st.Kind = devCalendar
		case strings.HasPrefix(name, "calendar:"):
			v, ok := calendar.ParseView(strings.TrimPrefix(name, "calendar:"))
			if !ok {
				return nil, fmt.Errorf("unknown calendar view in step %q", name)
			}
			st.Kind, st.View = devCalendarView, v
		case name == "event-popover":
			st.Kind = devEventPopover
		case name == "event-editor":
			st.Kind = devEventEditor
		case name == "event-editor:edit":
			st.Kind = devEventEdit
		case name == "caldav-dialog":
			st.Kind = devCalDAV
		case name == "prefs-calendar":
			st.Kind = devPrefsCalendar
		case name == "prefs-integrations":
			st.Kind = devPrefsIntegrations
		case name == "prefs-ai":
			st.Kind = devPrefsAI
		case name == "contacts":
			st.Kind = devContacts
		case name == "contacts:edit":
			st.Kind = devContactsEdit
		case name == "prefs-contacts":
			st.Kind = devPrefsContacts
		case name == "crm-dialog":
			st.Kind = devCrmDialog
		case name == "eway-wizard":
			st.Kind = devEwayWizard
		case strings.HasPrefix(name, "size="):
			wh := strings.SplitN(strings.TrimPrefix(name, "size="), "x", 2)
			if len(wh) != 2 {
				return nil, fmt.Errorf("step %q: want size=WxH", name)
			}
			w, err1 := strconv.Atoi(wh[0])
			h, err2 := strconv.Atoi(wh[1])
			if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
				return nil, fmt.Errorf("step %q: want size=WxH", name)
			}
			st.Kind, st.Width, st.Height = devSize, w, h
		case name == "quit":
			st.Kind = devQuit
		case strings.HasPrefix(name, "group="), strings.HasPrefix(name, "chat="):
			key, val, _ := strings.Cut(name, "=")
			if val != "on" && val != "off" {
				return nil, fmt.Errorf("step %q: want %s=on or %s=off", name, key, key)
			}
			st.Kind, st.On = devGroup, val == "on"
			if key == "chat" {
				st.Kind = devChat
			}
		case strings.HasPrefix(name, "folder="), strings.HasPrefix(name, "row="), strings.HasPrefix(name, "shot="):
			key, val, _ := strings.Cut(name, "=")
			val = strings.TrimSpace(val)
			if val == "" {
				return nil, fmt.Errorf("step %q: want %s=<text>", name, key)
			}
			st.Text = val
			switch key {
			case "folder":
				st.Kind = devFolder
			case "row":
				st.Kind = devRow
			default:
				st.Kind = devShot
			}
		case strings.HasPrefix(name, "board:"):
			parts := strings.Split(strings.TrimPrefix(name, "board:"), ":")
			s := board.ParseStyle(parts[0])
			if s.Nick() != parts[0] || len(parts) > 2 || (len(parts) == 2 && parts[1] != "select") {
				return nil, fmt.Errorf("step %q: want board:list|columns|today[:select]", name)
			}
			st.Kind, st.Style, st.Select = devBoardStyle, s, len(parts) == 2
		case name == "compose-open":
			st.Kind = devComposeOpen
		case name == "crm-save":
			st.Kind = devCrmSave
		default:
			return nil, fmt.Errorf("unknown step %q", name)
		}
		out = append(out, st)
	}
	return out, nil
}

// parseDevInterval is MALACHI_GTK_START_INTERVAL in seconds (fractions
// allowed); the default when empty or not a positive number.
func parseDevInterval(s string) time.Duration {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 {
		return devStartInterval
	}
	return time.Duration(f * float64(time.Second))
}

// developmentPrint writes one line of the script's output to stderr.
func developmentPrint(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// startDevelopmentScript runs MALACHI_GTK_START, when set, once the window
// first shows. Called once from New.
func (w *Window) startDevelopmentScript() {
	script, ok := os.LookupEnv(devStartEnv)
	if !ok {
		return
	}
	steps, err := parseDevStart(script)
	if err != nil {
		developmentPrint("MALACHI_GTK_START: %v", err)
		return
	}
	interval := parseDevInterval(os.Getenv(devStartIntervalEnv))
	started := false
	w.ConnectMap(func() {
		if started {
			return
		}
		started = true
		var next func(i int)
		next = func(i int) {
			if i >= len(steps) {
				return
			}
			glib.TimeoutAdd(uint(interval/time.Millisecond), func() bool {
				if devBusy > 0 {
					// A full interval again once the work is done.
					glib.TimeoutAdd(100, func() bool {
						if devBusy > 0 {
							return true
						}
						next(i)
						return false
					})
					return false
				}
				w.runDevStep(i+1, steps[i])
				next(i + 1)
				return false
			})
		}
		next(0)
	})
}

// runDevStep runs step n and prints what it shows.
func (w *Window) runDevStep(n int, st devStep) {
	developmentPrint("MALACHI_GTK_START step %d %s", n, st.Name)
	switch st.Kind {
	case devMail:
		w.setMode(board.ModeMail)
	case devBoard:
		w.setMode(board.ModeBoard)
	case devCalendar:
		w.setMode(board.ModeCalendar)
	case devCalendarView:
		w.setMode(board.ModeCalendar)
		if p := w.cal.page; p != nil {
			p.ctl.SetView(st.View)
		}
	case devEventPopover:
		if o, p, ok := w.devFirstOccurrence(); ok {
			anchor := p.anchorFor(calendar.OccurrenceKey(o))
			p.ctl.Select(calendar.OccurrenceKey(o))
			w.showEventDetail(o, anchor)
			devPrintWidget("anchor", anchor)
		}
	case devEventEditor:
		w.setMode(board.ModeCalendar)
		if p := w.cal.page; p != nil {
			d := p.ctl.NewEventDefaults(time.Now(), calendar.OriginNow)
			w.openEventEditor(&d, "")
		}
	case devEventEdit:
		if o, _, ok := w.devFirstOccurrence(); ok {
			scope := api.EditScopeAll
			if o.RecurrenceID != "" {
				scope = api.EditScopeThis
			}
			w.calendarSource().Event(o.EventID, o.RecurrenceID, false, func(e *api.Event, err *api.Error) {
				if e == nil {
					developmentPrint("LAYOUT: event-editor:edit no event (%v)", err)
					return
				}
				w.editEvent(*e, scope, false)
				glib.IdleAdd(w.devPrintToplevels)
			})
		}
	case devCalDAV:
		w.showAddCalDAV(w, nil)
	case devPrefsCalendar:
		d := NewPreferences(w.settings, w.client, w.assist, w.log, w.calendarSource)
		d.SetVisiblePageName("calendar")
		d.Present(w)
	case devPrefsIntegrations:
		d := NewPreferences(w.settings, w.client, w.assist, w.log, w.calendarSource)
		d.SetVisiblePageName("integrations")
		d.Present(w)
	case devPrefsAI:
		d := NewPreferences(w.settings, w.client, w.assist, w.log, w.calendarSource)
		d.SetVisiblePageName("ai")
		d.Present(w)
	case devContacts:
		w.OpenContacts()
	case devContactsEdit:
		w.devEditFirstContact()
	case devPrefsContacts:
		d := NewPreferences(w.settings, w.client, w.assist, w.log, w.calendarSource)
		d.SetVisiblePageName("contacts")
		d.Present(w)
	case devCrmDialog:
		w.showCrmSample()
	case devEwayWizard:
		accountwizard.NewEway(w.client, w.log).Present(w)
	case devSize:
		w.SetDefaultSize(st.Width, st.Height)
	case devQuit:
		if app := w.Application(); app != nil {
			app.Quit()
		}
		return
	case devGroup:
		w.settings.SetGroupByConversation(st.On)
	case devChat:
		w.settings.SetConversationChatLayout(st.On)
	case devFolder:
		w.devSelectFolder(st.Text)
	case devRow:
		w.devSelectRow(st.Text)
	case devBoardStyle:
		w.devBoard(st.Style, st.Select)
	case devComposeOpen:
		if app := w.Application(); app != nil {
			app.ActivateAction("compose", nil)
		}
	case devCrmSave:
		if a := w.LookupAction(crmActionSave); a != nil && a.Enabled() {
			a.Activate(nil)
		} else {
			developmentPrint("MALACHI_GTK_START crm-save: the action is off")
		}
	case devShot:
		// Answered from outside once the step has been drawn.
		glib.IdleAdd(func() { developmentPrint("MALACHI_GTK_START shot %s", st.Text) })
		return
	}
	// After the step has been laid out.
	glib.IdleAdd(func() {
		if p := w.cal.page; p != nil && w.mode == board.ModeCalendar {
			developmentPrint("%s", p.layoutLine(st.Name))
			developmentPrint("%s", p.minLine())
			developmentPrint("%s", w.modeMinLine())
		}
		w.devPrintToplevels()
	})
}

// devFirstOccurrence is the first occurrence of the shown range, the
// Calendar shown.
func (w *Window) devFirstOccurrence() (api.Occurrence, *calendarPage, bool) {
	w.setMode(board.ModeCalendar)
	p := w.cal.page
	if p == nil {
		return api.Occurrence{}, nil, false
	}
	occ := p.ctl.ShownOccurrences()
	if len(occ) == 0 {
		developmentPrint("LAYOUT: no occurrence in the shown range")
		return api.Occurrence{}, p, false
	}
	return occ[0], p, true
}

// layoutLine is the page's LAYOUT: line (as calendar_gtk_smoke_test.go
// logs it): the allocations, the header's minimum and texts.
func (p *calendarPage) layoutLine(step string) string {
	m, _, _, _ := p.header.Measure(gtk.OrientationHorizontal, -1)
	return fmt.Sprintf("LAYOUT: %s page=%dx%d sidebar=%dx%d content=%dx%d header=%d(min %d) stack=%q view=%q title=%q subtitle=%q status=%q note=%q",
		step, p.root.Width(), p.root.Height(), p.sidebarBin.Width(), p.sidebarBin.Height(),
		p.stack.Width(), p.stack.Height(), p.header.Width(), m, p.stack.VisibleChildName(), p.viewSwitch.ActiveName(),
		p.title.Title(), p.title.Subtitle(), p.statusLabel.Label(), p.statusNote.Label())
}

// devPrintWidget prints a widget's bounds in its window.
func devPrintWidget(name string, wd gtk.Widgetter) {
	if wd == nil {
		developmentPrint("LAYOUT: %s none", name)
		return
	}
	b := gtk.BaseWidget(wd)
	developmentPrint("LAYOUT: %s %s %dx%d", name, b.Name(), b.Width(), b.Height())
}

// devPrintToplevels prints every window and popover with its size (the
// main window, the editor, a dialog's window, Preferences) and the
// popovers open in the main window (the detail).
func (w *Window) devPrintToplevels() {
	list := gtk.WindowGetToplevels()
	for i := uint(0); i < list.NItems(); i++ {
		obj := list.Item(i)
		if obj == nil {
			continue
		}
		if tw, ok := obj.Cast().(gtk.Widgetter); ok {
			b := gtk.BaseWidget(tw)
			developmentPrint("LAYOUT: toplevel %s visible=%v %dx%d", b.Name(), b.IsVisible(), b.Width(), b.Height())
		}
	}
	w.devPrintDialog()
}

// devPrintDialog prints the dialog presented in the main window, if any:
// libadwaita 1.7's Adw.Dialog and Adw.PreferencesDialog live inside the
// window, not as toplevels. Its size, content size, title, and for
// Preferences the visible page.
func (w *Window) devPrintDialog() {
	d := w.VisibleDialog()
	if d == nil {
		return
	}
	page := ""
	if pd, ok := d.Object.Cast().(*adw.PreferencesDialog); ok {
		page = pd.VisiblePageName()
	}
	developmentPrint("LAYOUT: dialog %s %dx%d content=%dx%d title=%q page=%q", d.Name(), d.Width(), d.Height(),
		d.ContentWidth(), d.ContentHeight(), d.Title(), page)
}

// devMinWidth is wd's minimum width.
func devMinWidth(wd gtk.Widgetter) int {
	if wd == nil {
		return -1
	}
	m, _, _, _ := gtk.BaseWidget(wd).Measure(gtk.OrientationHorizontal, -1)
	return m
}

// minLine is the page's LAYOUT: min line: the minimum widths of the page,
// its split, sidebar, header bar, content stack and each stack child.
func (p *calendarPage) minLine() string {
	return fmt.Sprintf("LAYOUT: min page=%d split=%d sidebar=%d header=%d stack=%d week=%d day=%d month=%d agenda=%d search=%d empty=%d status=%d banner=%d",
		devMinWidth(p.root), devMinWidth(p.split), devMinWidth(p.sidebarBin), devMinWidth(p.header), devMinWidth(p.stack),
		devMinWidth(p.weekBin), devMinWidth(p.dayBin), devMinWidth(p.monthBin), devMinWidth(p.agendaBin), devMinWidth(p.searchBin),
		devMinWidth(p.empty), devMinWidth(p.statusButton), devMinWidth(p.banner))
}

// modeMinLine is the minimum widths of the window's mode stack and each
// of its pages (mail, board, calendar).
func (w *Window) modeMinLine() string {
	if w.modeStack == nil {
		return "LAYOUT: min modes none"
	}
	out := fmt.Sprintf("LAYOUT: min modes stack=%d", devMinWidth(w.modeStack))
	for _, m := range board.ModesWithCalendar {
		if c := w.modeStack.ChildByName(m.Nick()); c != nil {
			out += fmt.Sprintf(" %s=%d", m.Nick(), devMinWidth(c))
		}
	}
	return out
}

// devFindFolder is the first folder, accounts in the sidebar's order,
// whose server name contains text, case-insensitively.
func devFindFolder(accounts []api.Account, folders map[api.AccountID][]api.Folder, text string) (api.Folder, bool) {
	needle := strings.ToLower(text)
	for _, a := range accounts {
		for _, f := range folders[a.ID] {
			if strings.Contains(strings.ToLower(f.Name), needle) {
				return f, true
			}
		}
	}
	return api.Folder{}, false
}

// devFindRow is the index of the first list row whose subject contains
// text, case-insensitively; -1 without one.
func devFindRow(rows []listRow, text string) int {
	needle := strings.ToLower(text)
	for i, r := range rows {
		if strings.Contains(strings.ToLower(r.Message.Subject), needle) {
			return i
		}
	}
	return -1
}

// devRichestCase is the case whose loaded conversation has the most text
// (the first of equals); "" without cases.
func devRichestCase(cases []board.Case) board.CaseID {
	var best board.CaseID
	most := -1
	for _, c := range cases {
		n := 0
		for _, m := range c.Messages {
			n += len(m.Text)
		}
		if n > most {
			best, most = c.ID, n
		}
	}
	return best
}

// devSelectFolder is folder=: the folder shown as a click in the sidebar
// would.
func (w *Window) devSelectFolder(text string) {
	f, ok := devFindFolder(w.model.accounts, w.model.folders, text)
	if !ok {
		developmentPrint("MALACHI_GTK_START folder: no folder with %q", text)
		return
	}
	w.setMode(board.ModeMail)
	k := folderKey{Account: f.AccountID, Folder: f.ID}
	if r := w.folderRows[rowKey{folderKey: k}]; r != nil {
		// The sidebar's row-selected handler takes it from here.
		w.folderList.SelectRow(&r.ListBoxRow)
	} else {
		w.selectFolder(k)
	}
	developmentPrint("MALACHI_GTK_START folder: selected %q", f.Name)
}

// devSelectRow is row=: the row selected as a click would.
func (w *Window) devSelectRow(text string) {
	rows := make([]listRow, 0, w.model.rowCount())
	for i := 0; i < w.model.rowCount(); i++ {
		if r, ok := w.model.rowAt(i); ok {
			rows = append(rows, r)
		}
	}
	i := devFindRow(rows, text)
	if i < 0 {
		developmentPrint("MALACHI_GTK_START row: no row with %q among %d", text, len(rows))
		return
	}
	r := w.rows[rows[i].Key]
	if r == nil {
		developmentPrint("MALACHI_GTK_START row: %q has no widget", rows[i].Message.Subject)
		return
	}
	w.selectOnly(r.ListBoxRow)
	developmentPrint("MALACHI_GTK_START row: selected %q", rows[i].Message.Subject)
}

// devBoard is board:<style>[:select]: the Board in the style, as the
// style switch would, and with select the case with the most
// conversation text, once every conversation has loaded (or after 10 s).
func (w *Window) devBoard(s board.Style, sel bool) {
	w.setMode(board.ModeBoard)
	p := w.boardPage
	if p == nil {
		developmentPrint("MALACHI_GTK_START board: no board")
		return
	}
	p.styleSwitch.SetActiveName(styleNick(s))
	if !sel {
		return
	}
	// The board may not have its cases yet (the page was just built):
	// every round asks for the conversations not asked for so far.
	asked := map[board.CaseID]bool{}
	start := time.Now()
	devBusy++
	glib.TimeoutAdd(250, func() bool {
		cases := p.src.Snapshot().Cases
		waiting := len(cases) == 0
		for _, c := range cases {
			if c.MessagesLoaded || c.MessagesFailed {
				continue
			}
			waiting = true
			if !asked[c.ID] {
				asked[c.ID] = true
				p.src.LoadMessages(c.ID)
			}
		}
		if waiting && time.Since(start) < 10*time.Second {
			return true
		}
		loaded := 0
		for _, c := range cases {
			if c.MessagesLoaded {
				loaded++
			}
		}
		id := devRichestCase(cases)
		p.ctl.Select(id)
		devBusy--
		developmentPrint("MALACHI_GTK_START board: selected %s of %d (%d conversations loaded)", id, len(cases), loaded)
		return false
	})
}

// devEditFirstContact opens the Contacts window, selects the first listed
// contact once the list is there and opens its form once its card is
// (contacts:edit); the next step waits for it.
func (w *Window) devEditFirstContact() {
	cw := w.OpenContacts()
	devBusy++
	deadline := time.Now().Add(10 * time.Second)
	selected := false
	glib.TimeoutAdd(100, func() bool {
		if cw.closed || time.Now().After(deadline) {
			developmentPrint("LAYOUT: contacts:edit gave up")
			devBusy--
			return false
		}
		if !selected {
			for _, c := range cw.ctl.Contacts() {
				cw.ctl.Select(c.ID)
				selected = true
				break
			}
			return true
		}
		if !cw.contact.CanEdit() {
			return true
		}
		cw.contact.BeginEdit()
		cw.inner.SetShowContent(true)
		glib.IdleAdd(w.devPrintToplevels)
		devBusy--
		return false
	})
}
