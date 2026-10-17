// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/cairo"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendarctl"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/recipients"
	"github.com/schotek/malachi/ui/internal/style"
)

// The New Event / Edit Event window (docs/calendar.md §5.6, §6.5 G14;
// event_editor.blp) over calendarctl.EventEditorController; port of
// EventEditor/*.swift. An Adw.Window of 760×800, transient for the main
// window but not modal; an edit of an event already open (same event,
// scope and, below All Events, occurrence) raises its window. The form is
// a grid with a 110 px label column; every text comes from
// ui/internal/calendar (and the assistant's menu texts), every label that
// shows the server's text has markup off. Nothing is stored before Save
// (D6): closing with changes asks Save / Discard / Cancel.

// eventEditorLabelWidth is the form's label column.
const eventEditorLabelWidth = 110

// eventEditors are each window's open editors (the registry that raises
// an edit already open). Main loop only.
var eventEditors = map[*Window][]*eventEditor{}

// eventEditor is one window and its controller.
type eventEditor struct {
	w   *Window
	ctl *calendarctl.EventEditorController
	win *adw.Window
	env calendar.Env

	titleWidget *adw.WindowTitle
	attach      *gtk.Button
	assistant   *gtk.MenuButton
	grid        *gtk.Grid

	title      *gtk.Entry
	calendar   *gtk.DropDown
	allDayText *gtk.Label
	allDay     *gtk.Switch

	startDate, endDate         *gtk.MenuButton
	startCalendar, endCalendar *gtk.Calendar
	startTime, endTime         *gtk.Box
	startHour, startMinute     *gtk.SpinButton
	endHour, endMinute         *gtk.SpinButton
	zone                       *gtk.MenuButton
	zonePopover                *gtk.Popover
	zoneSearch                 *gtk.SearchEntry
	zoneList                   *gtk.ListBox

	repeat        *gtk.DropDown
	repeatPopover *gtk.Popover

	location *gtk.Entry
	video    *gtk.MenuButton

	attendees *compose.AttendeeField

	availabilityLabel *gtk.Label
	availability      *gtk.Box
	axis              *gtk.DrawingArea
	summary           *gtk.Label
	nextSlot          *gtk.Button
	freeSlot          *gtk.Label

	alert *gtk.DropDown
	notes *gtk.TextView
	chips *adw.WrapBox

	errorRow   *gtk.Box
	errorLabel *gtk.Label
	reload     *gtk.Button
	deleteBtn  *gtk.Button
	footer     *gtk.Label
	cancel     *gtk.Button
	primary    *gtk.Button

	actions    *gio.SimpleActionGroup
	findAction *gio.SimpleAction

	// syncing: the widgets are being set from the controller; their
	// signals are not edits.
	syncing bool
	// closing: the window closes without the question.
	closing bool
	// asking: the close question is open.
	asking bool

	calendars    []calendarctl.EditorCalendar
	calendarKey  string
	alertValues  []int
	alertsAllDay bool
	videoKey     string
	chipIDs      []string
	// rows are the availability rows the axis draws.
	rows []calendar.AvailabilityRow
}

// openEventEditor is New Event: defaults nil are the controller's of now,
// calendarID a preset calendar ("" the first writable)
// (calendarctl.NewEventEditor).
func (w *Window) openEventEditor(defaults *calendar.Defaults, calendarID api.CalendarID) {
	ctl := calendarctl.NewEventEditor(w.calendarSource(), w.calendarOptions(), defaults, calendarID)
	newEventEditor(w, ctl).present()
}

// editEvent is Edit Event of e with the scope the series question chose;
// findFreeSlot (Find Another Time) runs Next Free Slot at once
// (calendarctl.EditEventEditor). An open editor of e is raised instead.
func (w *Window) editEvent(e api.Event, scope api.EditScope, findFreeSlot bool) {
	for _, ed := range eventEditors[w] {
		if ed.edits(e, scope) {
			ed.win.Present()
			if findFreeSlot {
				ed.ctl.NextFreeSlot()
			}
			return
		}
	}
	ctl := calendarctl.EditEventEditor(w.calendarSource(), w.calendarOptions(), e, scope, findFreeSlot)
	newEventEditor(w, ctl).present()
}

// Called by the detail (G13) once it exists.
var _ = (*Window).editEvent

// edits reports that the editor edits e with scope.
func (ed *eventEditor) edits(e api.Event, scope api.EditScope) bool {
	ev, s, ok := ed.ctl.Editing()
	return ok && ev.EventID != "" && ev.EventID == e.EventID && s == scope &&
		(scope == api.EditScopeAll || ev.RecurrenceID == e.RecurrenceID)
}

// newEventEditor builds the window from event_editor.blp over ctl.
func newEventEditor(w *Window, ctl *calendarctl.EventEditorController) *eventEditor {
	style.EnsureCalendarCSS()
	b := data.Builder("event_editor.ui")
	get := func(id string) any { return b.GetObject(id).Cast() }
	ed := &eventEditor{w: w, ctl: ctl, env: w.calendarEnv()}
	ed.win = get("event_editor_window").(*adw.Window)
	ed.titleWidget = get("ee_title_widget").(*adw.WindowTitle)
	ed.attach = get("ee_attach").(*gtk.Button)
	ed.assistant = get("ee_assistant").(*gtk.MenuButton)
	ed.grid = get("ee_grid").(*gtk.Grid)
	ed.title = get("ee_title").(*gtk.Entry)
	ed.calendar = get("ee_calendar").(*gtk.DropDown)
	ed.allDayText = get("ee_all_day_label").(*gtk.Label)
	ed.allDay = get("ee_all_day").(*gtk.Switch)
	ed.startDate = get("ee_start_date").(*gtk.MenuButton)
	ed.endDate = get("ee_end_date").(*gtk.MenuButton)
	ed.startCalendar = get("ee_start_calendar").(*gtk.Calendar)
	ed.endCalendar = get("ee_end_calendar").(*gtk.Calendar)
	ed.startTime = get("ee_start_time").(*gtk.Box)
	ed.endTime = get("ee_end_time").(*gtk.Box)
	ed.startHour = get("ee_start_hour").(*gtk.SpinButton)
	ed.startMinute = get("ee_start_minute").(*gtk.SpinButton)
	ed.endHour = get("ee_end_hour").(*gtk.SpinButton)
	ed.endMinute = get("ee_end_minute").(*gtk.SpinButton)
	ed.zone = get("ee_zone").(*gtk.MenuButton)
	ed.zonePopover = get("ee_zone_popover").(*gtk.Popover)
	ed.zoneSearch = get("ee_zone_search").(*gtk.SearchEntry)
	ed.zoneList = get("ee_zone_list").(*gtk.ListBox)
	ed.repeat = get("ee_repeat").(*gtk.DropDown)
	ed.location = get("ee_location").(*gtk.Entry)
	ed.video = get("ee_video").(*gtk.MenuButton)
	ed.availabilityLabel = get("ee_availability_label").(*gtk.Label)
	ed.availability = get("ee_availability").(*gtk.Box)
	ed.axis = get("ee_availability_axis").(*gtk.DrawingArea)
	ed.summary = get("ee_availability_summary").(*gtk.Label)
	ed.nextSlot = get("ee_next_slot").(*gtk.Button)
	ed.freeSlot = get("ee_free_slot").(*gtk.Label)
	ed.alert = get("ee_alert").(*gtk.DropDown)
	ed.notes = get("ee_notes").(*gtk.TextView)
	ed.chips = get("ee_chips").(*adw.WrapBox)
	ed.errorRow = get("ee_error_row").(*gtk.Box)
	ed.errorLabel = get("ee_error").(*gtk.Label)
	ed.reload = get("ee_reload").(*gtk.Button)
	ed.deleteBtn = get("ee_delete").(*gtk.Button)
	ed.footer = get("ee_footer_note").(*gtk.Label)
	ed.cancel = get("ee_cancel").(*gtk.Button)
	ed.primary = get("ee_primary").(*gtk.Button)

	if w.ApplicationWindow != nil {
		ed.win.SetTransientFor(&w.ApplicationWindow.ApplicationWindow.Window)
	}
	if w.app != nil {
		ed.win.SetApplication(&w.app.Application)
	}
	ed.win.SetModal(false)

	ed.setTexts(b)
	ed.wire()
	ed.wireAttendees(b)
	ed.wirePopovers()
	ed.wireAvailability()
	ed.wireKeys()
	ed.wireController()

	in := ctl.Input()
	ed.syncing = true
	ed.title.SetText(in.Title)
	ed.location.SetText(in.Location)
	ed.notes.Buffer().SetText(in.Notes)
	ed.syncing = false
	ed.render()
	return ed
}

// present shows the window, adds it to the registry and starts the
// controller (availability, Find Another Time).
func (ed *eventEditor) present() {
	eventEditors[ed.w] = append(eventEditors[ed.w], ed)
	ed.win.Present()
	ed.title.GrabFocus()
	ed.ctl.Start()
}

// setTexts writes the fixed texts (all from ui/internal/calendar).
func (ed *eventEditor) setTexts(b *gtk.Builder) {
	tr := i18n.Tr
	title := ed.ctl.WindowTitle()
	ed.win.SetTitle(title)
	ed.titleWidget.SetTitle(title)
	labels := map[string]calendar.Field{
		"ee_calendar_label": calendar.FieldCalendar, "ee_all_day_label": calendar.FieldAllDay,
		"ee_start_label": calendar.FieldStart, "ee_end_label": calendar.FieldEnd,
		"ee_repeat_label": calendar.FieldRepeat, "ee_location_label": calendar.FieldLocation,
		"ee_attendees_label": calendar.FieldAttendees, "ee_availability_label": calendar.FieldAvailability,
		"ee_alert_label": calendar.FieldAlert, "ee_notes_label": calendar.FieldNotes,
	}
	for id, f := range labels {
		l := b.GetObject(id).Cast().(*gtk.Label)
		l.SetUseMarkup(false)
		l.SetLabel(calendar.FieldLabel(f, tr))
		if id != "ee_all_day_label" {
			l.SetSizeRequest(eventEditorLabelWidth, -1)
		}
	}
	ed.title.SetPlaceholderText(calendar.TitlePlaceholder(tr))
	ed.attach.SetTooltipText(calendar.Attach(tr))
	ed.assistant.SetTooltipText(assistant.Texts(tr).Assistant)
	m := gio.NewMenu()
	m.Append(assistant.CalendarLabel(tr, assistant.FindFreeSlot), "editor.find-free-slot")
	m.Append(assistant.Texts(tr).AskFile, "editor.ask-assistant")
	ed.assistant.SetMenuModel(m)
	ed.zoneSearch.SetPlaceholderText(calendar.ZoneSearch(tr))
	ed.nextSlot.SetLabel(calendar.NextFreeSlotButton(tr))
	ed.cancel.SetLabel(calendar.Cancel(tr))
	ed.reload.SetLabel(ed.ctl.ReloadTitle())
	ed.footer.SetUseMarkup(false)
	ed.errorLabel.SetUseMarkup(false)
	ed.summary.SetUseMarkup(false)
	ed.freeSlot.SetUseMarkup(false)
	for _, s := range []*gtk.SpinButton{ed.startHour, ed.startMinute, ed.endHour, ed.endMinute} {
		s := s
		s.ConnectOutput(func() bool {
			s.SetText(fmt.Sprintf("%02d", s.ValueAsInt()))
			return true
		})
	}
	repeats := make([]string, 0, len(ed.ctl.RepeatChoices()))
	for _, r := range ed.ctl.RepeatChoices() {
		repeats = append(repeats, ed.ctl.RepeatTitle(r))
	}
	ed.repeat.SetModel(gtk.NewStringList(repeats))
	ed.calendar.SetFactory(&calendarChoiceFactory(ed).ListItemFactory)
}

// wire connects the form's widgets to the controller's setters.
func (ed *eventEditor) wire() {
	ed.title.SetActivatesDefault(true)
	ed.location.SetActivatesDefault(true)
	ed.win.SetDefaultWidget(ed.primary)
	ed.title.ConnectChanged(func() {
		if !ed.syncing {
			ed.ctl.SetTitle(ed.title.Text())
		}
	})
	ed.location.ConnectChanged(func() {
		if !ed.syncing {
			ed.ctl.SetLocation(ed.location.Text())
		}
	})
	buf := ed.notes.Buffer()
	buf.ConnectChanged(func() {
		if ed.syncing {
			return
		}
		start, end := buf.Bounds()
		ed.ctl.SetNotes(buf.Text(start, end, false))
	})
	ed.calendar.NotifyProperty("selected", func() {
		i := int(ed.calendar.Selected())
		if ed.syncing || i < 0 || i >= len(ed.calendars) {
			return
		}
		ed.ctl.SetCalendar(ed.calendars[i].ID)
	})
	ed.allDay.NotifyProperty("active", func() {
		if !ed.syncing {
			ed.ctl.SetAllDay(ed.allDay.Active())
		}
	})
	ed.startCalendar.ConnectDaySelected(func() { ed.dayPicked(true) })
	ed.endCalendar.ConnectDaySelected(func() { ed.dayPicked(false) })
	for _, s := range []*gtk.SpinButton{ed.startHour, ed.startMinute} {
		s.ConnectValueChanged(func() { ed.timeChanged(true) })
	}
	for _, s := range []*gtk.SpinButton{ed.endHour, ed.endMinute} {
		s.ConnectValueChanged(func() { ed.timeChanged(false) })
	}
	ed.repeat.NotifyProperty("selected", func() {
		if ed.syncing {
			return
		}
		choices := ed.ctl.RepeatChoices()
		i := int(ed.repeat.Selected())
		if i < 0 || i >= len(choices) {
			return
		}
		if choices[i] == calendar.RepeatCustom {
			ed.showCustomRepeat()
			return
		}
		ed.ctl.SetRepeat(choices[i])
	})
	ed.alert.NotifyProperty("selected", func() {
		i := int(ed.alert.Selected())
		if ed.syncing || i < 0 || i >= len(ed.alertValues) {
			return
		}
		ed.ctl.SetAlert(ed.alertValues[i])
	})
	ed.nextSlot.ConnectClicked(ed.ctl.NextFreeSlot)
	ed.reload.ConnectClicked(ed.ctl.Reload)
	ed.cancel.ConnectClicked(func() { ed.win.Close() })
	ed.primary.ConnectClicked(ed.save)
	ed.deleteBtn.ConnectClicked(func() { ed.ctl.Delete(nil) })
	ed.attach.ConnectClicked(ed.attachFiles)

	ed.actions = gio.NewSimpleActionGroup()
	find := gio.NewSimpleAction("find-free-slot", nil)
	find.ConnectActivate(func(*glib.Variant) { ed.ctl.NextFreeSlot() })
	ed.actions.AddAction(find)
	ed.findAction = find
	ask := gio.NewSimpleAction("ask-assistant", nil)
	ask.ConnectActivate(func(*glib.Variant) {
		if ed.w.ApplicationWindow != nil {
			ed.w.Present()
		}
		ed.w.revealCalendarAssistant()
	})
	ed.actions.AddAction(ask)
	ed.wireVideoActions()
	ed.win.InsertActionGroup("editor", ed.actions)

	ed.win.ConnectCloseRequest(func() bool {
		if ed.closing || !ed.ctl.HasChanges() {
			ed.closing = true
			ed.teardown()
			return false
		}
		if !ed.asking {
			ed.asking = true
			ed.ctl.Close(func(bool) { ed.asking = false })
		}
		return true
	})
}

// wireAttendees builds the attendee field over the compose window's
// recipient field and suggestions.
func (ed *eventEditor) wireAttendees(b *gtk.Builder) {
	label := b.GetObject("ee_attendees_label").Cast().(*gtk.Label)
	ed.attendees = compose.NewAttendeeField(
		b.GetObject("ee_attendees_scroll").Cast().(*gtk.ScrolledWindow),
		b.GetObject("ee_attendees_row").Cast().(*adw.WrapBox),
		label,
		compose.AttendeeOptions{
			RemoveText: editorRemoveText(),
			Search:     ed.searchContacts,
			Active:     ed.win.IsActive,
			OnChanged:  ed.attendeesEdited,
		})
	var list []api.Address
	for _, t := range ed.ctl.Attendees().Items() {
		if t.Address != nil {
			list = append(list, *t.Address)
		}
	}
	ed.attendees.SetAddresses(list)
}

// searchContacts is contact.search under the calendar's account (its mail
// account for invitations by mail).
func (ed *eventEditor) searchContacts(query string, done func([]api.Contact, error)) {
	w := ed.w
	account := ed.mailAccount()
	if w.client == nil || account == "" {
		done(nil, nil)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.ContactSearchResult
		err := w.client.Call(ctx, api.MethodContactSearch,
			api.ContactSearchParams{AccountID: account, Query: query, Limit: 8}, &res)
		glib.IdleAdd(func() {
			if ed.closing {
				return
			}
			done(res.Contacts, err)
		})
	}()
}

// mailAccount is the account whose address books suggest attendees.
func (ed *eventEditor) mailAccount() api.AccountID {
	s := ed.w.calendarSource().Snapshot()
	cal, ok := s.Calendar(ed.ctl.Input().CalendarID)
	if !ok {
		return ""
	}
	if a, ok := s.CalendarAccount(cal.AccountID); ok && a.Features.MailAccountID != "" {
		return a.Features.MailAccountID
	}
	return cal.AccountID
}

// attendeesEdited feeds the field's badges and typed text into the
// controller's model as they resolve (what a commit would make of them).
func (ed *eventEditor) attendeesEdited() {
	if ed.syncing || ed.attendees == nil {
		return
	}
	addrs, invalid := ed.attendees.Resolved()
	ed.ctl.EditAttendees(func(t *recipients.Tokens) {
		n := recipients.New()
		for _, a := range addrs {
			n.Add(a)
		}
		for _, raw := range invalid {
			n.SetPending(raw)
			n.Commit()
		}
		*t = *n
	})
}

// wireKeys: Ctrl+S saves when there are no attendees, Ctrl+Return is the
// primary, Escape closes (with the question) when no popup is open.
func (ed *eventEditor) wireKeys() {
	sc := gtk.NewShortcutController()
	sc.SetPropagationPhase(gtk.PhaseCapture)
	sc.AddShortcut(gtk.NewShortcut(gtk.NewShortcutTriggerParseString("<Control>s"),
		gtk.NewCallbackAction(func(gtk.Widgetter, *glib.Variant) bool {
			if ed.ctl.SaveKeyAllowed() {
				ed.save()
			}
			return true
		})))
	sc.AddShortcut(gtk.NewShortcut(gtk.NewShortcutTriggerParseString("<Control>Return|<Control>KP_Enter"),
		gtk.NewCallbackAction(func(gtk.Widgetter, *glib.Variant) bool {
			ed.save()
			return true
		})))
	ed.win.AddController(sc)
	esc := gtk.NewShortcutController()
	esc.AddShortcut(gtk.NewShortcut(gtk.NewShortcutTriggerParseString("Escape"),
		gtk.NewCallbackAction(func(gtk.Widgetter, *glib.Variant) bool {
			if ed.attendees.SuggestionsShown() {
				return false
			}
			ed.win.Close()
			return true
		})))
	ed.win.AddController(esc)
}

// wireController connects the controller's callbacks.
func (ed *eventEditor) wireController() {
	c := ed.ctl
	c.OnChange = ed.render
	c.OnSaved = func(api.Event) { ed.closeUnasked() }
	c.OnClose = ed.closeUnasked
	c.OnReload = func(fresh api.Event, scope api.EditScope) {
		w := ed.w
		ed.closeUnasked()
		w.editEvent(fresh, scope, false)
	}
	c.OnError = func(string) {}
	c.OnQuestion = ed.askClose
	c.OnDeleteQuestion = ed.askDelete
	c.ConfirmDelete = func() bool {
		if ed.w.settings == nil {
			return true
		}
		return ed.w.settings.ConfirmDelete()
	}
}

// save is the primary, Ctrl+Return and Ctrl+S: the typed attendee counts
// as the field would commit it.
func (ed *eventEditor) save() {
	if ed.ctl.Saving() {
		return
	}
	ed.attendees.CommitPending()
	ed.attendeesEdited()
	ed.ctl.Save(nil)
}

// closeUnasked closes the window without the question (saved, deleted,
// discarded, reloaded).
func (ed *eventEditor) closeUnasked() {
	if ed.closing {
		return
	}
	ed.closing = true
	ed.teardown()
	ed.win.Close()
}

// teardown stops the controller and leaves the registry (once).
func (ed *eventEditor) teardown() {
	list := eventEditors[ed.w]
	i := slices.Index(list, ed)
	if i < 0 {
		return
	}
	eventEditors[ed.w] = slices.Delete(list, i, i+1)
	if len(eventEditors[ed.w]) == 0 {
		delete(eventEditors, ed.w)
	}
	ed.ctl.OnChange = nil
	ed.ctl.Stop()
	ed.attendees.Close()
	if ed.repeatPopover != nil {
		ed.repeatPopover.Unparent()
		ed.repeatPopover = nil
	}
}

// askClose is the close question (Save / Discard / Cancel).
func (ed *eventEditor) askClose(q calendar.Question, answer func(calendarctl.Answer)) {
	d := adw.NewAlertDialog(q.Heading, q.Body)
	d.AddResponse("cancel", calendar.Cancel(i18n.Tr))
	if q.Alternative != "" {
		d.AddResponse("alternative", q.Alternative)
		d.SetResponseAppearance("alternative", adw.ResponseDestructive)
	}
	d.AddResponse("confirm", q.Confirm)
	d.SetResponseAppearance("confirm", adw.ResponseSuggested)
	d.SetDefaultResponse("confirm")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(r string) { answer(editorAnswer(r)) })
	d.Present(ed.win)
}

// askDelete is Delete's question: Cancel the default, the confirmation
// destructive, the alternative (remove without a reply) plain.
func (ed *eventEditor) askDelete(q calendar.Question, answer func(calendarctl.Answer)) {
	d := adw.NewAlertDialog(q.Heading, q.Body)
	d.AddResponse("cancel", calendar.Cancel(i18n.Tr))
	if q.Alternative != "" {
		d.AddResponse("alternative", q.Alternative)
	}
	d.AddResponse("confirm", q.Confirm)
	d.SetResponseAppearance("confirm", adw.ResponseDestructive)
	d.SetDefaultResponse("cancel")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(r string) { answer(editorAnswer(r)) })
	d.Present(ed.win)
}

func editorAnswer(r string) calendarctl.Answer {
	switch r {
	case "confirm":
		return calendarctl.AnswerConfirm
	case "alternative":
		return calendarctl.AnswerAlternative
	}
	return calendarctl.AnswerCancel
}

// attachFiles is Attach: files picked in the file dialog, imported under
// the calendar's account.
func (ed *eventEditor) attachFiles() {
	if len(ed.ctl.Attachments()) >= api.MaxEventAttachments {
		return
	}
	dlg := gtk.NewFileDialog()
	dlg.SetTitle(calendar.Attach(i18n.Tr))
	dlg.OpenMultiple(context.Background(), &ed.win.Window, func(res gio.AsyncResulter) {
		files, err := dlg.OpenMultipleFinish(res)
		if err != nil || ed.closing {
			return
		}
		for i := uint(0); i < files.NItems(); i++ {
			f, ok := files.Item(i).Cast().(*gio.File)
			if !ok {
				continue
			}
			if p := f.Path(); p != "" {
				ed.ctl.Attach(p)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Dates and times
// ---------------------------------------------------------------------------

// zoneEnv is the env with the event's zone (the date buttons).
func (ed *eventEditor) zoneEnv() calendar.Env {
	e := ed.env
	e.Loc = ed.ctl.Zone()
	return e
}

// shownEnd is the end the form shows: an all-day event's last day.
func (ed *eventEditor) shownEnd() time.Time {
	in, z := ed.ctl.Input(), ed.ctl.Zone()
	if in.AllDay {
		return calendar.AddDays(calendar.StartOfDay(in.End, z), -1)
	}
	return in.End.In(z)
}

// dayPicked is a day chosen in a date popover.
func (ed *eventEditor) dayPicked(start bool) {
	if ed.syncing {
		return
	}
	in, z := ed.ctl.Input(), ed.ctl.Zone()
	cal, btn := ed.endCalendar, ed.endDate
	if start {
		cal, btn = ed.startCalendar, ed.startDate
	}
	y, m, d := cal.Year(), time.Month(cal.Month()+1), cal.Day()
	switch {
	case start:
		s := in.Start.In(z)
		h, mi := s.Hour(), s.Minute()
		if in.AllDay {
			h, mi = 0, 0
		}
		ed.ctl.SetStart(time.Date(y, m, d, h, mi, 0, 0, z))
	case in.AllDay:
		ed.ctl.SetEnd(calendar.AddDays(time.Date(y, m, d, 0, 0, 0, 0, z), 1))
	default:
		e := in.End.In(z)
		ed.ctl.SetEnd(time.Date(y, m, d, e.Hour(), e.Minute(), 0, 0, z))
	}
	btn.Popdown()
	ed.render()
}

// timeChanged is an hour or minute spin button.
func (ed *eventEditor) timeChanged(start bool) {
	if ed.syncing {
		return
	}
	in, z := ed.ctl.Input(), ed.ctl.Zone()
	if start {
		s := in.Start.In(z)
		ed.ctl.SetStart(time.Date(s.Year(), s.Month(), s.Day(), ed.startHour.ValueAsInt(), ed.startMinute.ValueAsInt(), 0, 0, z))
	} else {
		e := in.End.In(z)
		ed.ctl.SetEnd(time.Date(e.Year(), e.Month(), e.Day(), ed.endHour.ValueAsInt(), ed.endMinute.ValueAsInt(), 0, 0, z))
	}
	ed.render()
}

// setDate shows t on a date button and its calendar.
func (ed *eventEditor) setDate(btn *gtk.MenuButton, cal *gtk.Calendar, t time.Time) {
	btn.SetLabel(calendar.DayMonthYearText(t, ed.zoneEnv()))
	if cal.Year() != t.Year() || cal.Month() != int(t.Month())-1 || cal.Day() != t.Day() {
		cal.SelectDay(glib.NewDateTimeLocal(t.Year(), int(t.Month()), t.Day(), 12, 0, 0))
	}
}

func editorSetSpin(s *gtk.SpinButton, v int) {
	if s.ValueAsInt() != v {
		s.SetValue(float64(v))
	}
}

// ---------------------------------------------------------------------------
// Showing the state
// ---------------------------------------------------------------------------

// render writes the controller's state into the widgets.
func (ed *eventEditor) render() {
	if ed.closing {
		return
	}
	c := ed.ctl
	in := c.Input()
	z := c.Zone()
	ed.syncing = true
	defer func() { ed.syncing = false }()

	// The Calendar popup: the writable calendars (Edit: the event's own).
	cals := c.Calendars()
	key := ""
	for _, k := range cals {
		key += string(k.ID) + "\x00" + k.Title + "\x00" + k.Color + "\x01"
	}
	if key != ed.calendarKey {
		ed.calendarKey, ed.calendars = key, cals
		titles := make([]string, 0, len(cals))
		for _, k := range cals {
			titles = append(titles, k.Title)
		}
		ed.calendar.SetModel(gtk.NewStringList(titles))
	}
	if i := slices.IndexFunc(ed.calendars, func(k calendarctl.EditorCalendar) bool { return k.ID == in.CalendarID }); i >= 0 &&
		int(ed.calendar.Selected()) != i {
		ed.calendar.SetSelected(uint(i))
	}
	ed.calendar.SetSensitive(len(ed.calendars) > 1)

	if ed.allDay.Active() != in.AllDay {
		ed.allDay.SetActive(in.AllDay)
	}
	start := in.Start.In(z)
	ed.setDate(ed.startDate, ed.startCalendar, start)
	ed.setDate(ed.endDate, ed.endCalendar, ed.shownEnd())
	end := in.End.In(z)
	editorSetSpin(ed.startHour, start.Hour())
	editorSetSpin(ed.startMinute, start.Minute())
	editorSetSpin(ed.endHour, end.Hour())
	editorSetSpin(ed.endMinute, end.Minute())
	ed.startTime.SetVisible(!in.AllDay)
	ed.endTime.SetVisible(!in.AllDay)
	ed.zone.SetVisible(!in.AllDay)
	ed.zone.SetLabel(c.TimeZoneLink())

	if i := slices.Index(c.RepeatChoices(), c.RepeatChoice()); i >= 0 && int(ed.repeat.Selected()) != i {
		ed.repeat.SetSelected(uint(i))
	}
	if ed.alertsAllDay != in.AllDay || ed.alertValues == nil || !slices.Contains(ed.alertValues, c.Alert()) {
		ed.fillAlerts()
	}
	if i := slices.Index(ed.alertValues, c.Alert()); i >= 0 && int(ed.alert.Selected()) != i {
		ed.alert.SetSelected(uint(i))
	}
	ed.renderVideo()

	shows := c.ShowsAvailability()
	ed.availabilityLabel.SetVisible(shows)
	ed.availability.SetVisible(shows)
	if shows {
		ed.rows = c.AvailabilityRows()
		ed.axis.SetContentHeight(availabilityAxisHeight + len(ed.rows)*availabilityRowHeight)
		ed.axis.QueueDraw()
		s := strings.Join(c.AvailabilitySummary(), "\n")
		ed.summary.SetLabel(s)
		ed.summary.SetVisible(s != "")
	}
	ed.freeSlot.SetLabel(c.FreeSlotText())
	ed.nextSlot.SetSensitive(!in.AllDay)
	if ed.findAction != nil {
		ed.findAction.SetEnabled(!in.AllDay && len(ed.calendars) > 0)
	}

	ed.renderChips()

	errs := c.ProblemTexts()
	if e := c.Error(); e != "" {
		errs = append(errs, e)
	}
	ed.errorLabel.SetLabel(strings.Join(errs, "\n"))
	ed.errorRow.SetVisible(len(errs) > 0)
	_, editing := c.DeleteTitle()
	ed.reload.SetVisible(c.Conflict() && editing)

	ed.footer.SetLabel(strings.Join(c.Footer(), "\n"))
	ed.primary.SetLabel(c.PrimaryLabel())
	ed.primary.SetSensitive(!c.Saving())
	if t, ok := c.DeleteTitle(); ok {
		ed.deleteBtn.SetLabel(t)
		ed.deleteBtn.SetVisible(true)
	} else {
		ed.deleteBtn.SetVisible(false)
	}
	ed.deleteBtn.SetSensitive(!c.Saving())
	ed.attach.SetSensitive(len(c.Attachments()) < api.MaxEventAttachments)
}

// fillAlerts rebuilds the Alert popup for the current All Day state
// (AlertChoicesFor), plus a stored value outside the list.
func (ed *eventEditor) fillAlerts() {
	c := ed.ctl
	ed.alertsAllDay = c.Input().AllDay
	ed.alertValues = slices.Clone(c.AlertChoices())
	if !slices.Contains(ed.alertValues, c.Alert()) {
		ed.alertValues = append(ed.alertValues, c.Alert())
	}
	titles := make([]string, 0, len(ed.alertValues))
	for _, m := range ed.alertValues {
		titles = append(titles, c.AlertTitle(m))
	}
	ed.alert.SetModel(gtk.NewStringList(titles))
}

// renderChips shows the attachments as chips with ×.
func (ed *eventEditor) renderChips() {
	atts := ed.ctl.Attachments()
	ids := make([]string, 0, len(atts))
	for _, a := range atts {
		ids = append(ids, a.ID)
	}
	if slices.Equal(ids, ed.chipIDs) {
		return
	}
	ed.chipIDs = ids
	for child := ed.chips.FirstChild(); child != nil; child = ed.chips.FirstChild() {
		ed.chips.Remove(child)
	}
	for _, a := range atts {
		id := a.ID
		box := gtk.NewBox(gtk.OrientationHorizontal, 4)
		box.AddCSSClass("event-editor-chip")
		icon := gtk.NewImageFromIconName("mail-attachment-symbolic")
		l := gtk.NewLabel(editorClip(a.Filename, 200))
		l.SetUseMarkup(false)
		l.SetEllipsize(pango.EllipsizeMiddle)
		l.SetMaxWidthChars(28)
		l.SetTooltipText(editorClip(a.Filename, 1000))
		x := gtk.NewButtonFromIconName("window-close-symbolic")
		x.AddCSSClass("flat")
		x.AddCSSClass("circular")
		x.SetTooltipText(editorRemoveText())
		x.ConnectClicked(func() { ed.ctl.RemoveAttachment(id) })
		box.Append(icon)
		box.Append(l)
		box.Append(x)
		ed.chips.Append(box)
	}
	ed.chips.SetVisible(len(atts) > 0)
}

// editorClip caps text handed to a widget.
func editorClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// calendarChoiceFactory renders a Calendar popup's choice: the calendar's
// colour square and "Account · Calendar" as plain text.
func calendarChoiceFactory(ed *eventEditor) *gtk.SignalListItemFactory {
	f := gtk.NewSignalListItemFactory()
	f.ConnectSetup(func(obj *coreglib.Object) {
		item, ok := obj.Cast().(*gtk.ListItem)
		if !ok {
			return
		}
		box := gtk.NewBox(gtk.OrientationHorizontal, 8)
		sw := gtk.NewDrawingArea()
		sw.SetContentWidth(10)
		sw.SetContentHeight(10)
		sw.SetVAlign(gtk.AlignCenter)
		l := gtk.NewLabel("")
		l.SetUseMarkup(false)
		l.SetXAlign(0)
		l.SetEllipsize(pango.EllipsizeEnd)
		l.SetMaxWidthChars(40)
		box.Append(sw)
		box.Append(l)
		item.SetChild(box)
	})
	f.ConnectBind(func(obj *coreglib.Object) {
		item, ok := obj.Cast().(*gtk.ListItem)
		if !ok {
			return
		}
		box, ok := item.Child().(*gtk.Box)
		if !ok {
			return
		}
		sw, _ := box.FirstChild().(*gtk.DrawingArea)
		l, _ := box.LastChild().(*gtk.Label)
		if s, ok := item.Item().Cast().(*gtk.StringObject); ok && l != nil {
			l.SetLabel(s.String())
		}
		pos := int(item.Position())
		color := ""
		if pos >= 0 && pos < len(ed.calendars) {
			color = style.CalendarColor(ed.calendars[pos].Color, pos)
		}
		if sw != nil {
			sw.SetDrawFunc(func(_ *gtk.DrawingArea, cr *cairo.Context, w, h int) {
				rgba := gdk.NewRGBA(0, 0, 0, 0)
				if color == "" || !rgba.Parse(color) {
					return
				}
				gdk.CairoSetSourceRGBA(cr, &rgba)
				editorRoundedRect(cr, 0, 0, float64(w), float64(h), 2.5)
				cr.Fill()
			})
		}
	})
	return f
}
