// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendarctl"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The event detail popover (docs/calendar.md §5.5, §6.5 G13;
// event_detail.blp), the port of macOS's EventDetailPopover,
// EventDetailViewController and EventDetailViews: one Gtk.Popover per
// window, parented to the calendar page's content and pointing at the
// block, bar, pill or row the view passed (its bounds in the content), over
// a calendarctl.EventDetailController on the window's calendar source.
// Showing another event closes the one shown. The rows: the calendar's
// colour, "Work · Calendar" and the account's kind capsule; the bold
// title; the time in two lines for a timed event within one day; the
// recurrence; the place; Join Video Call with its host; the people line,
// the attendees (collapsed beyond calendar.MaxAttendeesShown) and the
// "You: …" MenuButton; the alerts; the notes in a non-editable TextView
// whose only tags are the daemon's links; Show Invitation in Mail; the
// footer Edit, Remove from Calendar or Delete, ✦ (Ask about This Event,
// Find Another Time); the inline error. The questions are Adw.AlertDialogs
// on the window, asked with the popover down (an autohide popover above
// an in-window dialog would cover it) and shown again after the answer.
// Every string of the server goes in with markup off; every link through
// openEventLink, which always says where it leads.

// eventDetail is the window's popover and the controller it shows.
type eventDetail struct {
	w       *Window
	popover *gtk.Popover
	box     *gtk.Box

	header, timeRow, recurrenceRow, locationRow, videoRow, peopleRow  *gtk.Box
	alertRow, notesRow, invitationRow, footer, attendees, swatch      *gtk.Box
	calendarLabel, kind, title, time1, time2, recurrence, location    *gtk.Label
	videoHost, people, alert, errLabel                                *gtk.Label
	video, showAll, invitation, edit, remove                          *gtk.Button
	response, assistantMenu                                           *gtk.MenuButton
	notes                                                             *gtk.TextView
	spinner                                                           *gtk.Spinner
	respondAction                                                     *gio.SimpleAction
	swatchClass, attendeesShown, notesShown, assistantShown, alertKey string

	ctl *calendarctl.EventDetailController
	// questions counts the questions up (the popover is down meanwhile);
	// findingTime: Edit was Find Another Time.
	questions   int
	findingTime bool
	// noteLinks maps a link tag's name to its URL; noteTags the tags.
	noteLinks map[string]string
	noteTags  []*gtk.TextTag
}

// showEventDetail opens the detail of occ pointing at anchor (a block, a
// bar, a row; the content when no view shows it). An occurrence with no
// event (an invitation not in a calendar) has none: its card is in Mail.
func (w *Window) showEventDetail(occ api.Occurrence, anchor gtk.Widgetter) {
	if w.cal.page == nil {
		return
	}
	ctl := calendarctl.NewEventDetailFor(w.calendarSource(), w.calendarOptions(), occ, w.calendarConfirmDelete)
	if ctl == nil {
		return
	}
	d := w.ensureEventDetail()
	d.open(ctl, anchor)
}

// closeEventDetail closes the detail, if open (Escape, leaving the
// Calendar).
func (w *Window) closeEventDetail() {
	if d := w.cal.detail; d != nil {
		d.close()
	}
}

// eventDetailShown reports the detail open (calendar.EscapeFor's
// popoverOpen), a question it asked included.
func (w *Window) eventDetailShown() bool {
	d := w.cal.detail
	return d != nil && d.ctl != nil && (d.popover.IsVisible() || d.questions > 0)
}

// removeCalendarOccurrence is Delete on the selection: the detail's
// remove or delete flow for occ, with its questions. The open detail of
// occ runs it itself; otherwise a controller without a popover loads the
// event and runs it, a failure as a toast.
func (w *Window) removeCalendarOccurrence(occ api.Occurrence) {
	if d := w.cal.detail; d != nil && d.ctl != nil && d.ctl.EventID() == occ.EventID &&
		(occ.RecurrenceID == "" || d.ctl.RecurrenceID() == occ.RecurrenceID) {
		d.ctl.Remove()
		return
	}
	c := calendarctl.NewEventDetailFor(w.calendarSource(), w.calendarOptions(), occ, w.calendarConfirmDelete)
	if c == nil {
		return
	}
	asked, pending, done := false, 0, false
	finish := func() {
		if done {
			return
		}
		done = true
		if e := c.Error(); e != "" {
			w.Toast(e)
		}
		c.Stop()
	}
	settle := func() {
		if asked && pending == 0 && !c.Busy() {
			finish()
		}
	}
	c.OnQuestion = func(q calendar.Question, answer func(calendarctl.Answer)) {
		pending++
		w.askEventQuestion(q, func(a calendarctl.Answer) {
			answer(a)
			pending--
			settle()
		})
	}
	c.OnScope = func(q calendar.ScopeQuestion, answer func(*api.EditScope)) {
		pending++
		w.askEventScope(q, func(s *api.EditScope) {
			answer(s)
			pending--
			settle()
		})
	}
	c.OnClose = finish
	c.OnChange = func() {
		if done || c.Loading() {
			return
		}
		if !asked {
			e := c.Event()
			if e == nil || !e.CanDelete {
				finish()
				return
			}
			asked = true
			c.Remove()
		}
		settle()
	}
	c.Start()
}

// calendarConfirmDelete is the Mail preference "Ask before moving to the
// Trash" (confirm-delete), which the detail's plain removal follows.
func (w *Window) calendarConfirmDelete() bool {
	return w.settings == nil || w.settings.ConfirmDelete()
}

// ensureEventDetail builds the window's popover on its first show.
func (w *Window) ensureEventDetail() *eventDetail {
	if w.cal.detail != nil {
		return w.cal.detail
	}
	b := data.Builder("event_detail.ui")
	get := func(id string) any { return b.GetObject(id).Cast() }
	d := &eventDetail{w: w, noteLinks: map[string]string{}}
	d.popover = get("event_detail_popover").(*gtk.Popover)
	d.box = get("event_detail_box").(*gtk.Box)
	boxes := map[string]**gtk.Box{
		"event_detail_header": &d.header, "event_detail_time_row": &d.timeRow,
		"event_detail_recurrence_row": &d.recurrenceRow, "event_detail_location_row": &d.locationRow,
		"event_detail_video_row": &d.videoRow, "event_detail_people_row": &d.peopleRow,
		"event_detail_alert_row": &d.alertRow, "event_detail_notes_row": &d.notesRow,
		"event_detail_invitation_row": &d.invitationRow, "event_detail_footer": &d.footer,
		"event_detail_attendees": &d.attendees, "event_detail_swatch": &d.swatch,
	}
	for id, p := range boxes {
		*p = get(id).(*gtk.Box)
	}
	labels := map[string]**gtk.Label{
		"event_detail_calendar": &d.calendarLabel, "event_detail_kind": &d.kind, "event_detail_title": &d.title,
		"event_detail_time": &d.time1, "event_detail_time2": &d.time2, "event_detail_recurrence": &d.recurrence,
		"event_detail_location": &d.location, "event_detail_video_host": &d.videoHost,
		"event_detail_people": &d.people, "event_detail_alert": &d.alert, "event_detail_error": &d.errLabel,
	}
	for id, p := range labels {
		*p = get(id).(*gtk.Label)
		(*p).SetUseMarkup(false)
	}
	buttons := map[string]**gtk.Button{
		"event_detail_video": &d.video, "event_detail_show_all": &d.showAll,
		"event_detail_invitation": &d.invitation, "event_detail_edit": &d.edit, "event_detail_remove": &d.remove,
	}
	for id, p := range buttons {
		*p = get(id).(*gtk.Button)
	}
	d.response = get("event_detail_response").(*gtk.MenuButton)
	d.assistantMenu = get("event_detail_assistant").(*gtk.MenuButton)
	d.notes = get("event_detail_notes").(*gtk.TextView)
	d.spinner = get("event_detail_spinner").(*gtk.Spinner)

	tr := i18n.Tr
	d.video.SetLabel(calendar.JoinVideoCall(tr))
	d.invitation.SetLabel(calendar.ShowInvitation(tr))
	d.edit.SetLabel(calendar.Edit(tr))
	d.assistantMenu.SetTooltipText(assistant.Texts(tr).Assistant)
	d.wireActions()
	d.wireNotes()
	d.wireKeys()

	d.video.ConnectClicked(func() { d.withCtl((*calendarctl.EventDetailController).JoinVideoCall) })
	d.showAll.ConnectClicked(func() { d.withCtl((*calendarctl.EventDetailController).ExpandAttendees) })
	d.invitation.ConnectClicked(func() { d.withCtl((*calendarctl.EventDetailController).ShowInvitation) })
	d.edit.ConnectClicked(func() {
		d.findingTime = false
		d.withCtl((*calendarctl.EventDetailController).Edit)
	})
	d.remove.ConnectClicked(func() { d.withCtl((*calendarctl.EventDetailController).Remove) })
	d.popover.ConnectClosed(func() {
		// A question's popdown keeps the controller; so does the closing
		// of the one a new show replaced.
		if d.questions > 0 || d.popover.IsVisible() {
			return
		}
		d.stop()
	})
	if p := w.cal.page; p != nil {
		d.popover.SetParent(p.split.Content())
	}
	w.cal.detail = d
	return d
}

// withCtl runs f on the controller shown, if any.
func (d *eventDetail) withCtl(f func(*calendarctl.EventDetailController)) {
	if d.ctl != nil {
		f(d.ctl)
	}
}

// wireActions are the menus' actions (group "detail"): the answers, Decline
// Without Reply, Ask about This Event and Find Another Time.
func (d *eventDetail) wireActions() {
	tr := i18n.Tr
	g := gio.NewSimpleActionGroup()
	d.respondAction = gio.NewSimpleActionStateful("respond", glib.NewVariantType("s"), glib.NewVariantString(""))
	d.respondAction.ConnectActivate(func(p *glib.Variant) {
		if p != nil && d.ctl != nil {
			d.ctl.Respond(api.EventResponse(p.String()), false)
		}
	})
	g.AddAction(d.respondAction)
	without := gio.NewSimpleAction("decline-without-reply", nil)
	without.ConnectActivate(func(*glib.Variant) {
		if d.ctl != nil {
			d.ctl.Respond(api.ResponseDeclined, true)
		}
	})
	g.AddAction(without)
	ask := gio.NewSimpleAction("ask", nil)
	ask.ConnectActivate(func(*glib.Variant) { d.withCtl((*calendarctl.EventDetailController).AskAssistant) })
	g.AddAction(ask)
	find := gio.NewSimpleAction("find-time", nil)
	find.ConnectActivate(func(*glib.Variant) {
		// The editor's Next Free Slot: Edit, with the series question
		// first (macOS's findingTime).
		if d.ctl != nil {
			d.findingTime = true
			d.ctl.Edit()
		}
	})
	g.AddAction(find)
	d.popover.InsertActionGroup("detail", g)

	answers := gio.NewMenu()
	for _, r := range calendar.ResponseChoices {
		item := gio.NewMenuItem(calendar.ResponseAction(r, tr), "")
		item.SetActionAndTargetValue("detail.respond", glib.NewVariantString(string(r)))
		answers.AppendItem(item)
	}
	m := gio.NewMenu()
	m.AppendSection("", answers)
	rest := gio.NewMenu()
	rest.Append(calendar.DeclineWithoutReply(tr), "detail.decline-without-reply")
	m.AppendSection("", rest)
	d.response.SetMenuModel(m)
}

// wireNotes makes the notes' link tags open their links (through the
// controller's OpenLink, which takes only the daemon's links), with the
// pointer over them.
func (d *eventDetail) wireNotes() {
	click := gtk.NewGestureClick()
	click.SetButton(gdk.BUTTON_PRIMARY)
	click.ConnectReleased(func(n int, x, y float64) {
		if n != 1 || d.ctl == nil {
			return
		}
		if buf := d.notes.Buffer(); buf.HasSelection() {
			return
		}
		if url := d.noteLinkAt(x, y); url != "" {
			d.ctl.OpenLink(url)
		}
	})
	d.notes.AddController(click)
	motion := gtk.NewEventControllerMotion()
	motion.ConnectMotion(func(x, y float64) {
		if d.noteLinkAt(x, y) != "" {
			d.notes.SetCursorFromName("pointer")
		} else {
			d.notes.SetCursorFromName("text")
		}
	})
	d.notes.AddController(motion)
}

// noteLinkAt is the URL of the link under (x, y) of the notes, "" none.
func (d *eventDetail) noteLinkAt(x, y float64) string {
	bx, by := d.notes.WindowToBufferCoords(gtk.TextWindowWidget, int(x), int(y))
	iter, ok := d.notes.IterAtLocation(bx, by)
	if !ok {
		return ""
	}
	for _, t := range iter.Tags() {
		if name, ok := t.ObjectProperty("name").(string); ok {
			if url, ok := d.noteLinks[name]; ok {
				return url
			}
		}
	}
	return ""
}

// wireKeys: Delete is Remove from Calendar / Delete and Return Edit, as
// on macOS (calendar.KeyFor with an occurrence selected), where the focused
// widget leaves them; Escape is the popover's own.
func (d *eventDetail) wireKeys() {
	keys := gtk.NewEventControllerKey()
	keys.ConnectKeyPressed(func(val, _ uint, state gdk.ModifierType) bool {
		if d.ctl == nil || state&gtk.AcceleratorGetDefaultModMask() != 0 {
			return false
		}
		det, ok := d.ctl.Detail()
		if !ok {
			return false
		}
		switch val {
		case gdk.KEY_Delete, gdk.KEY_KP_Delete, gdk.KEY_BackSpace:
			if det.CanRemove && !d.ctl.Busy() {
				d.ctl.Remove()
				return true
			}
		case gdk.KEY_Return, gdk.KEY_KP_Enter, gdk.KEY_ISO_Enter:
			if det.CanEdit && !d.ctl.Busy() {
				d.findingTime = false
				d.ctl.Edit()
				return true
			}
		}
		return false
	})
	d.popover.AddController(keys)
}

// open shows ctl's event pointing at anchor; the event shown before
// closes.
func (d *eventDetail) open(ctl *calendarctl.EventDetailController, anchor gtk.Widgetter) {
	d.stop()
	if d.popover.IsVisible() {
		d.popover.Popdown()
	}
	d.ctl = ctl
	d.findingTime = false
	d.wire(ctl)
	ctl.Start()
	d.update()
	if p := d.w.cal.page; p != nil {
		rect := detailPointing(anchor, p.split.Content(), p.stack)
		d.popover.SetPointingTo(&rect)
	}
	d.popover.Popup()
	d.focusFooter()
}

// focusFooter gives the keyboard to the first footer button there is, not
// to the first focusable row (a link of the notes, Join Video Call).
func (d *eventDetail) focusFooter() {
	for _, b := range []*gtk.Button{d.edit, d.remove} {
		if b.IsVisible() && b.IsSensitive() && d.footer.IsVisible() {
			b.GrabFocus()
			return
		}
	}
}

// wire connects ctl's continuations to the popover and the window.
func (d *eventDetail) wire(ctl *calendarctl.EventDetailController) {
	w := d.w
	ctl.OnChange = func() {
		if d.ctl == ctl {
			d.update()
		}
	}
	ctl.OnQuestion = func(q calendar.Question, answer func(calendarctl.Answer)) {
		d.down()
		w.askEventQuestion(q, func(a calendarctl.Answer) {
			answer(a)
			d.up()
		})
	}
	ctl.OnScope = func(q calendar.ScopeQuestion, answer func(*api.EditScope)) {
		d.down()
		w.askEventScope(q, func(s *api.EditScope) {
			answer(s)
			d.up()
		})
	}
	ctl.OnClose = d.close
	ctl.OnEdit = func(e api.Event, scope api.EditScope) {
		find := d.findingTime
		d.close()
		w.editEvent(e, scope, find)
	}
	ctl.OnShowInvitation = func(m api.MessageRef) {
		d.close()
		w.showInvitationInMail(m)
	}
	ctl.OnOpenLink = func(url string) {
		d.close()
		w.openEventLink(url)
	}
	ctl.OnAsk = func(e api.Event) {
		d.close()
		w.askAboutEvent(e)
	}
}

// down takes the popover down for a question (the controller stays).
func (d *eventDetail) down() {
	d.questions++
	if d.popover.IsVisible() {
		d.popover.Popdown()
	}
}

// up is a question answered: the popover comes back when no other
// question followed and the answer did not close it.
func (d *eventDetail) up() {
	if d.questions > 0 {
		d.questions--
	}
	if d.questions == 0 && d.ctl != nil && !d.popover.IsVisible() {
		d.popover.Popup()
		d.focusFooter()
	}
}

// close closes the popover and stops its controller.
func (d *eventDetail) close() {
	d.questions = 0
	d.stop()
	if d.popover.IsVisible() {
		d.popover.Popdown()
	}
}

// stop stops the controller shown, if any.
func (d *eventDetail) stop() {
	if d.ctl != nil {
		d.ctl.Stop()
		d.ctl = nil
	}
}

// update shows the controller's state.
func (d *eventDetail) update() {
	c := d.ctl
	if c == nil {
		return
	}
	w, tr := d.w, i18n.Tr
	env := w.calendarEnv()
	snap := w.calendarSource().Snapshot()
	det, loaded := c.Detail()
	var o api.Occurrence
	if e := c.Event(); e != nil {
		o = e.Occurrence
	} else if p := c.Occurrence(); p != nil {
		o = *p
	}

	// The header: colour, "Work · Calendar", kind capsule.
	cls := ""
	if p := w.cal.page; p != nil && o.CalendarID != "" {
		cls = p.colorClass(o.CalendarID)
	}
	if cls != d.swatchClass {
		if d.swatchClass != "" {
			d.swatch.RemoveCSSClass(d.swatchClass)
		}
		if cls != "" {
			d.swatch.AddCSSClass(cls)
		}
		d.swatchClass = cls
	}
	d.swatch.SetVisible(cls != "")
	line := ""
	if loaded {
		line = det.CalendarLine
	}
	d.calendarLabel.SetText(line)
	badge := ""
	if a, ok := snap.Account(o.AccountID); ok {
		badge = calendarKindBadge(a)
	}
	d.kind.SetText(badge)
	d.kind.SetVisible(badge != "")

	title := ""
	if loaded {
		title = det.Title
	} else if title = board.CleanLine(o.Title, api.MaxEventTitleBytes); title == "" {
		title = calendar.Untitled(tr)
	}
	d.title.SetText(title)

	fallback := calendar.TimeLine(o, env)
	if loaded && det.TimeLine != "" {
		fallback = det.TimeLine
	}
	lines := detailTimeLines(o, fallback, env)
	d.time1.SetText(lines[0])
	d.time2.SetText(lines[1])
	d.time2.SetVisible(lines[1] != "")
	d.timeRow.SetVisible(lines[0] != "")

	d.spinner.SetVisible(!loaded && c.Loading())
	d.spinner.SetSpinning(!loaded && c.Loading())
	d.errLabel.SetText(c.Error())
	d.errLabel.SetVisible(c.Error() != "")

	rows := []*gtk.Box{d.recurrenceRow, d.locationRow, d.videoRow, d.peopleRow, d.alertRow, d.notesRow, d.invitationRow, d.footer}
	if !loaded {
		for _, r := range rows {
			r.SetVisible(false)
		}
		return
	}
	busy := c.Busy()
	setRow(d.recurrenceRow, d.recurrence, det.Recurrence)
	setRow(d.locationRow, d.location, det.Location)
	d.videoRow.SetVisible(det.VideoCall != "")
	d.videoHost.SetText(det.VideoHost)
	d.videoHost.SetVisible(det.VideoHost != "")

	d.peopleRow.SetVisible(det.PeopleLine != "" || det.CanRespond)
	d.people.SetText(det.PeopleLine)
	d.people.SetVisible(det.PeopleLine != "")
	d.fillAttendees(c.ShownAttendees())
	more := c.ShowAllAttendees()
	d.showAll.SetLabel(more)
	d.showAll.SetVisible(more != "")
	d.response.SetVisible(det.CanRespond)
	d.response.SetLabel(calendar.MyResponseText(det.MyResponse, tr))
	d.response.SetSensitive(!busy)
	d.respondAction.SetState(glib.NewVariantString(string(det.MyResponse)))

	alert := ""
	if e := c.Event(); e != nil {
		alert = detailAlertLine(e.Alerts, e.AllDay, tr)
	}
	setRow(d.alertRow, d.alert, alert)
	d.fillNotes(det.Notes, det.Links)
	d.invitationRow.SetVisible(det.ShowInvitation)

	d.footer.SetVisible(true)
	d.edit.SetVisible(det.CanEdit)
	d.edit.SetSensitive(!busy)
	d.remove.SetLabel(det.RemoveTitle)
	d.remove.SetVisible(det.CanRemove)
	d.remove.SetSensitive(!busy)
	canAsk := w.assist != nil && w.assist.panelShown()
	d.fillAssistant(canAsk, det.CanEdit)
	d.assistantMenu.SetVisible(canAsk || det.CanEdit)
	d.assistantMenu.SetSensitive(!busy)
}

// setRow shows text in a row's label, the row only with text.
func setRow(row *gtk.Box, l *gtk.Label, text string) {
	l.SetText(text)
	row.SetVisible(text != "")
}

// fillAttendees builds the attendee list again when it changed: the
// response's icon, the name (the address as its tooltip; the address when
// there is no name), "organiser" and "optional".
func (d *eventDetail) fillAttendees(rows []calendar.AttendeeRow) {
	var key strings.Builder
	for _, a := range rows {
		key.WriteString(a.Name + "\x00" + a.Address + "\x00" + string(a.Response) + "\x00")
		if a.Organizer {
			key.WriteByte('o')
		}
		if a.Optional {
			key.WriteByte('p')
		}
		key.WriteByte('\n')
	}
	if key.String() == d.attendeesShown {
		return
	}
	d.attendeesShown = key.String()
	for c := d.attendees.FirstChild(); c != nil; c = d.attendees.FirstChild() {
		d.attendees.Remove(c)
	}
	tr := i18n.Tr
	for _, a := range rows {
		row := gtk.NewBox(gtk.OrientationHorizontal, 6)
		icon, class := responseIcon(a.Response)
		img := gtk.NewImageFromIconName(icon)
		img.AddCSSClass("cal-detail-response")
		img.AddCSSClass(class)
		img.SetVAlign(gtk.AlignCenter)
		img.SetTooltipText(a.ResponseText)
		row.Append(img)
		name := gtk.NewLabel("")
		name.SetUseMarkup(false)
		if a.Name != "" {
			name.SetText(a.Name)
		} else {
			name.SetText(a.Address)
		}
		if a.Address != "" {
			name.SetTooltipText(a.Address)
		}
		name.SetXAlign(0)
		name.SetEllipsize(pango.EllipsizeEnd)
		name.SetHExpand(false)
		row.Append(name)
		var extra []string
		if a.Organizer {
			extra = append(extra, calendar.OrganizerLabel(tr))
		}
		if a.Optional {
			extra = append(extra, calendar.OptionalLabel(tr))
		}
		if len(extra) > 0 {
			l := gtk.NewLabel("")
			l.SetUseMarkup(false)
			l.SetText(strings.Join(extra, ", "))
			l.AddCSSClass("caption")
			l.AddCSSClass("dim-label")
			row.Append(l)
		}
		d.attendees.Append(row)
	}
	d.attendees.SetVisible(len(rows) > 0)
}

// responseIcon is an attendee's response as an icon and its colour class
// (macOS EventDetailAttendeeRow.symbol): ✓ green, ✕ red, ? orange for
// tentative, an arrow grey for delegated, a grey ? for no reply.
func responseIcon(r api.EventResponse) (icon, class string) {
	switch r {
	case api.ResponseAccepted:
		return "object-select-symbolic", "accepted"
	case api.ResponseDeclined:
		return "window-close-symbolic", "declined"
	case api.ResponseTentative:
		return "dialog-question-symbolic", "tentative"
	case api.ResponseDelegated:
		return "go-next-symbolic", "other"
	}
	return "dialog-question-symbolic", "other"
}

// fillNotes shows the notes as plain text with a tag on each place a link
// of the daemon's (links) appears; built again only when they changed.
func (d *eventDetail) fillNotes(notes string, links []string) {
	key := notes + "\x00" + strings.Join(links, "\x00")
	d.notesRow.SetVisible(notes != "")
	if key == d.notesShown {
		return
	}
	d.notesShown = key
	buf := d.notes.Buffer()
	table := buf.TagTable()
	for _, t := range d.noteTags {
		table.Remove(t)
	}
	d.noteTags = nil
	d.noteLinks = map[string]string{}
	buf.SetText(notes)
	accent := linkColor()
	for _, r := range noteLinkRanges(notes, links) {
		name := "link" + itoa10(len(d.noteTags))
		t := gtk.NewTextTag(name)
		if accent != "" {
			t.SetObjectProperty("foreground", accent)
		}
		table.Add(t)
		d.noteTags = append(d.noteTags, t)
		d.noteLinks[name] = r.url
		buf.ApplyTag(t, buf.IterAtOffset(r.start), buf.IterAtOffset(r.end))
	}
}

// noteLink is a link's place in the notes, in characters.
type noteLink struct {
	start, end int
	url        string
}

// noteLinkRanges are the places of each link in notes (every occurrence,
// longer links first where two overlap), in characters.
func noteLinkRanges(notes string, links []string) []noteLink {
	var out []noteLink
	taken := func(s, e int) bool {
		for _, r := range out {
			if s < r.end && r.start < e {
				return true
			}
		}
		return false
	}
	sorted := append([]string(nil), links...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && len(sorted[j]) > len(sorted[j-1]); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	for _, l := range sorted {
		if l == "" {
			continue
		}
		for from := 0; from < len(notes); {
			i := strings.Index(notes[from:], l)
			if i < 0 {
				break
			}
			b := from + i
			s := utf8.RuneCountInString(notes[:b])
			e := s + utf8.RuneCountInString(l)
			if !taken(s, e) {
				out = append(out, noteLink{start: s, end: e, url: l})
			}
			from = b + len(l)
		}
	}
	return out
}

// fillAssistant builds ✦'s menu when what it offers changed: Ask about
// This Event while the assistant panel exists, Find Another Time for an
// editable event.
func (d *eventDetail) fillAssistant(canAsk, canEdit bool) {
	key := ""
	if canAsk {
		key += "a"
	}
	if canEdit {
		key += "f"
	}
	if key == d.assistantShown && d.assistantMenu.MenuModel() != nil {
		return
	}
	d.assistantShown = key
	tr := i18n.Tr
	m := gio.NewMenu()
	if canAsk {
		m.Append(calendar.AskAboutEvent(tr), "detail.ask")
	}
	if canEdit {
		m.Append(calendar.FindAnotherTime(tr), "detail.find-time")
	}
	d.assistantMenu.SetMenuModel(m)
}

// detailTimeLines are the time row's two lines: a timed event within one
// day as its date and "10:00–11:30 · 1 h 30 min", anything else as
// fallback alone (the second line "").
func detailTimeLines(o api.Occurrence, fallback string, env calendar.Env) [2]string {
	loc := env.Loc
	if loc == nil {
		loc = time.Local
	}
	s, e, ok := calendar.Span(o, loc)
	if o.AllDay || !ok || (!s.Equal(e) && !calendar.StartOfDay(s, loc).Equal(calendar.StartOfDay(e.Add(-time.Second), loc))) {
		return [2]string{fallback, ""}
	}
	tr := env.Tr
	return [2]string{calendar.LongDateText(s, env),
		calendar.Dot(calendar.TimeRangeText(s, e, env), calendar.DurationText(e.Sub(s), tr), tr)}
}

// detailAlertLine is the alerts' row: each alert's title (the editor's,
// calendar.AlertTitleFor) joined by " · "; "" without one.
func detailAlertLine(alerts []int, allDay bool, tr calendar.Translator) string {
	line := ""
	for _, m := range alerts {
		if m < 0 {
			continue
		}
		t := calendar.AlertTitleFor(m, allDay, tr)
		if line == "" {
			line = t
		} else {
			line = calendar.Dot(line, t, tr)
		}
	}
	return line
}

// calendarKindBadge is the detail's kind capsule: "CALDAV" for a CalDAV
// account (calendar_sidebar.go's caldavBadge), else the mail
// sidebar's (accountHeaderBadge).
// Protocol and brand names, never translated.
func calendarKindBadge(a api.Account) string {
	if a.Config.Kind == api.AccountCalDAV {
		return caldavBadge
	}
	return accountHeaderBadge(a)
}

// detailPointing is the rectangle in parent the popover points at: the
// anchor's bounds; the middle of parent's upper third when there is no
// anchor, it is the content's stack (fallback) or it is not laid out in
// parent.
func detailPointing(anchor, parent, fallback gtk.Widgetter) gdk.Rectangle {
	pw := gtk.BaseWidget(parent)
	center := gdk.NewRectangle(pw.Width()/2, pw.Height()/3, 1, 1)
	if anchor == nil {
		return center
	}
	id := glib.BaseObject(anchor).Native()
	if id == glib.BaseObject(parent).Native() || (fallback != nil && id == glib.BaseObject(fallback).Native()) {
		return center
	}
	b, ok := gtk.BaseWidget(anchor).ComputeBounds(parent)
	if !ok || b.Width() <= 0 || b.Height() <= 0 {
		return center
	}
	return gdk.NewRectangle(int(b.X()), int(b.Y()), max(1, int(b.Width())), max(1, int(b.Height())))
}

// askEventQuestion asks a calendar.Question on the window: Cancel the
// default and the close response, the confirmation destructive, the
// alternative last. answer runs once.
func (w *Window) askEventQuestion(q calendar.Question, answer func(calendarctl.Answer)) {
	dlg := adw.NewAlertDialog(q.Heading, q.Body)
	dlg.SetHeadingUseMarkup(false)
	dlg.SetBodyUseMarkup(false)
	dlg.AddResponse("cancel", i18n.T("_Cancel"))
	dlg.AddResponse("confirm", q.Confirm)
	dlg.SetResponseAppearance("confirm", adw.ResponseDestructive)
	if q.Alternative != "" {
		dlg.AddResponse("alternative", q.Alternative)
	}
	dlg.SetDefaultResponse("cancel")
	dlg.SetCloseResponse("cancel")
	dlg.ConnectResponse(func(r string) {
		switch r {
		case "confirm":
			answer(calendarctl.AnswerConfirm)
		case "alternative":
			answer(calendarctl.AnswerAlternative)
		default:
			answer(calendarctl.AnswerCancel)
		}
	})
	dlg.Present(w)
}

// askEventScope asks the series question (This Event Only, This and
// Future Events, All Events): Cancel leads and closes, the first choice is
// the default (macOS's Return); answer gets nil for Cancel.
func (w *Window) askEventScope(q calendar.ScopeQuestion, answer func(*api.EditScope)) {
	dlg := adw.NewAlertDialog(q.Heading, q.Body)
	dlg.SetHeadingUseMarkup(false)
	dlg.SetBodyUseMarkup(false)
	dlg.AddResponse("cancel", i18n.T("_Cancel"))
	for i, c := range q.Choices {
		dlg.AddResponse("scope"+itoa10(i), c.Title)
	}
	if len(q.Choices) > 0 {
		dlg.SetDefaultResponse("scope0")
	} else {
		dlg.SetDefaultResponse("cancel")
	}
	dlg.SetCloseResponse("cancel")
	dlg.ConnectResponse(func(r string) {
		for i, c := range q.Choices {
			if r == "scope"+itoa10(i) {
				s := c.Scope
				answer(&s)
				return
			}
		}
		answer(nil)
	})
	dlg.Present(w)
}

// openEventLink opens Join Video Call or a link of the notes: a mailto:
// as a new message (openLink), anything else only after "Open This
// Link?" has shown where it leads (the event's text is the organiser's,
// and no visible text of the link is known).
func (w *Window) openEventLink(url string) {
	if strings.HasPrefix(strings.ToLower(url), "mailto:") {
		w.openLink(&w.ApplicationWindow.Window, url, nil)
		return
	}
	w.openAssistantAnswerLink(url)
}

// showInvitationInMail is Show Invitation in Mail: the window in Mail with
// the message selected in its folder, as the board's Show in Mail does
// (message.get for the folder, then the reveal's wait for the listing).
func (w *Window) showInvitationInMail(ref api.MessageRef) {
	if calendarSamples || w.client == nil {
		// The samples' ids are invented: never asked of the daemon.
		w.Toast(calendar.PreviewOnly(i18n.Tr))
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.MessageGetResult
		err := w.client.Call(ctx, api.MethodMessageGet, api.MessageGetParams{AccountID: ref.AccountID, MessageID: ref.MessageID}, &res)
		glib.IdleAdd(func() {
			if err != nil {
				w.log.Warn("message.get (show invitation)", "err", err)
				if revealGone(err) {
					w.Toast(board.ShowInMailGone(i18n.Tr))
				} else {
					w.Toast(board.ShowInMailFailed(i18n.Tr))
				}
				return
			}
			r := &w.cal.reveal
			if w.boardPage != nil {
				// Leaving Mail drops the board's wait (setMode).
				r = &w.boardPage.reveal
			}
			r.start(w, folderKey{Account: ref.AccountID, Folder: res.Message.FolderID}, ref.MessageID, res.Message.ThreadID)
		})
	}()
}
