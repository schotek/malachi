// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"math"
	"reflect"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotk4/pkg/pangocairo"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendarctl"
	"github.com/schotek/malachi/ui/internal/calendardata"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The invitation card of a message on display (docs/calendar.md §5.7,
// §6.5 G15): above the headers of the reading pane and of a message
// window (message_view.go, the invitation_card_slot), and under the
// header row of a conversation card, inside what folds
// (conversation_card.go); never in an attached message. One per display,
// built from invitation_card.blp through data.Builder like html_card, over
// a calendarctl.InvitationController and the window's calendar source
// (w.calendarSource). The card shows the parsed calendar part of
// message.get (Message.Invitation), never HTML: every text is a plain
// label set from ui/internal/calendar; the title of a cancelled event is
// struck through with a Pango attribute. Join Video Call goes through the
// link confirmation (w.openLink), Show in Calendar to the main window's
// Calendar mode (w.showInCalendar). A port of the macOS client's
// InvitationCardView.swift, InvitationDayStripView.swift and
// InvitationCardPresenter (MessageViewController+Invitation.swift).
type invitationCard struct {
	win    *Window
	parent *gtk.Window

	root *gtk.Box

	tile                    *gtk.Box
	weekday, day, month     *gtk.Label
	pill, title             *gtk.Label
	clock                   *gtk.Label
	previous                *gtk.Box
	locationRow             *gtk.Box
	location                *gtk.Label
	join                    *gtk.Button
	people                  *gtk.Label
	conflictRow             *gtk.Box
	conflictIcon            *gtk.Image
	conflict                *gtk.Label
	strip                   *gtk.DrawingArea
	stateRow                *gtk.Box
	state                   *gtk.Label
	stateShow               *gtk.Button
	actions                 *gtk.Box
	addTo                   *gtk.Label
	target                  *gtk.DropDown
	targetLabel             *gtk.Label
	accept, tentative       *gtk.Button
	decline                 *adw.SplitButton
	spinner                 *adw.Spinner
	stripRow                *gtk.Box
	stripLabel              *gtk.Label
	change                  *gtk.MenuButton
	changeChecks            []*gtk.Image
	show                    *gtk.Button
	cancelRow               *gtk.Box
	remove                  *gtk.Button
	notes                   *gtk.Box
	proposeRow              *gtk.Box
	propose                 *gtk.MenuButton
	errLabel                *gtk.Label
	previousLines, noteText []string

	// The controller of the message on display (nil: hidden), the card
	// message.get gave last (an answer's card stays until message.get
	// brings another), the account's switch it was made with, and its
	// observer of the calendar source (the calendars' names).
	ctl    *calendarctl.InvitationController
	fed    *api.InvitationCard
	cardOn bool
	token  *board.ObserverToken

	// The "Add to:" choices the dropdown lists, in its order; settingTarget
	// while render sets its selection.
	targetIDs     []api.CalendarID
	settingTarget bool

	// dayStrip is what the strip draws (nil: hidden).
	dayStrip *calendar.DayStrip

	// source overrides w.calendarSource (tests).
	source calendardata.DataSource
}

// newInvitationCard builds a hidden card of main window w shown in parent
// (the main window or a message window).
func newInvitationCard(w *Window, parent *gtk.Window) *invitationCard {
	b := data.Builder("invitation_card.ui")
	label := func(id string) *gtk.Label {
		l := b.GetObject(id).Cast().(*gtk.Label)
		l.SetUseMarkup(false)
		return l
	}
	box := func(id string) *gtk.Box { return b.GetObject(id).Cast().(*gtk.Box) }
	button := func(id string) *gtk.Button { return b.GetObject(id).Cast().(*gtk.Button) }
	c := &invitationCard{
		win: w, parent: parent,
		root:         box("invitation_card"),
		tile:         box("invitation_tile"),
		weekday:      label("invitation_tile_weekday"),
		day:          label("invitation_tile_day"),
		month:        label("invitation_tile_month"),
		pill:         label("invitation_pill"),
		title:        label("invitation_title"),
		clock:        label("invitation_clock"),
		previous:     box("invitation_previous"),
		locationRow:  box("invitation_location_row"),
		location:     label("invitation_location"),
		join:         button("invitation_join"),
		people:       label("invitation_people"),
		conflictRow:  box("invitation_conflict_row"),
		conflictIcon: b.GetObject("invitation_conflict_icon").Cast().(*gtk.Image),
		conflict:     label("invitation_conflict"),
		strip:        b.GetObject("invitation_day_strip").Cast().(*gtk.DrawingArea),
		stateRow:     box("invitation_state_row"),
		state:        label("invitation_state"),
		stateShow:    button("invitation_state_show"),
		actions:      box("invitation_actions"),
		addTo:        label("invitation_add_to"),
		target:       b.GetObject("invitation_target").Cast().(*gtk.DropDown),
		targetLabel:  label("invitation_target_label"),
		accept:       button("invitation_accept"),
		tentative:    button("invitation_tentative"),
		decline:      b.GetObject("invitation_decline").Cast().(*adw.SplitButton),
		spinner:      b.GetObject("invitation_spinner").Cast().(*adw.Spinner),
		stripRow:     box("invitation_strip_row"),
		stripLabel:   label("invitation_strip"),
		change:       b.GetObject("invitation_change").Cast().(*gtk.MenuButton),
		show:         button("invitation_show"),
		cancelRow:    box("invitation_cancel_row"),
		remove:       button("invitation_remove"),
		notes:        box("invitation_notes"),
		proposeRow:   box("invitation_propose_row"),
		propose:      b.GetObject("invitation_propose").Cast().(*gtk.MenuButton),
		errLabel:     label("invitation_error"),
	}
	tr := i18n.Tr
	c.join.SetLabel(calendar.JoinVideoCall(tr))
	c.addTo.SetLabel(calendar.AddTo(tr))
	c.accept.SetLabel(calendar.ResponseAction(api.ResponseAccepted, tr))
	c.tentative.SetLabel(calendar.ResponseAction(api.ResponseTentative, tr))
	c.decline.SetLabel(calendar.ResponseAction(api.ResponseDeclined, tr))
	c.show.SetLabel(calendar.ShowInCalendar(tr))
	c.stateShow.SetLabel(calendar.ShowInCalendar(tr))
	c.change.SetLabel(calendar.ChangeResponse(tr))
	c.remove.SetLabel(calendar.RemoveFromCalendar(tr))
	c.propose.SetLabel(calendar.ProposeNewTime(tr))
	c.weekday.SetXAlign(0.5)

	c.join.ConnectClicked(func() { c.act(func(k *calendarctl.InvitationController) { k.JoinVideoCall() }) })
	c.accept.ConnectClicked(func() { c.respond(api.ResponseAccepted, false) })
	c.tentative.ConnectClicked(func() { c.respond(api.ResponseTentative, false) })
	c.decline.ConnectClicked(func() { c.respond(api.ResponseDeclined, false) })
	c.decline.SetPopover(c.menuPopover([]menuEntry{{label: calendar.DeclineWithoutReply(tr), run: func() {
		c.respond(api.ResponseDeclined, true)
	}}}, nil))
	c.change.SetPopover(c.changePopover())
	c.show.ConnectClicked(func() { c.act(func(k *calendarctl.InvitationController) { k.ShowInCalendar() }) })
	c.stateShow.ConnectClicked(func() { c.act(func(k *calendarctl.InvitationController) { k.ShowInCalendar() }) })
	c.remove.ConnectClicked(func() { c.act(func(k *calendarctl.InvitationController) { k.ApplyCancel() }) })
	c.propose.SetCreatePopupFunc(func(*gtk.MenuButton) { c.propose.SetPopover(c.proposePopover()) })
	c.target.NotifyProperty("selected", func() {
		if c.settingTarget || c.ctl == nil {
			return
		}
		if i := int(c.target.Selected()); i >= 0 && i < len(c.targetIDs) {
			c.ctl.SetTarget(c.targetIDs[i])
		}
	})
	c.strip.SetDrawFunc(c.drawStrip)
	c.strip.SetVisible(false)
	// The observer lives while the card is in a window: a conversation
	// card let go by its pane takes it away with it.
	c.root.ConnectUnrealize(func() {
		if c.token != nil {
			c.token.Cancel()
			c.token = nil
		}
	})
	c.root.ConnectRealize(func() {
		if c.ctl != nil && c.token == nil {
			c.observe()
			c.render()
		}
	})
	return c
}

// widget is the card's root, to put in a slot.
func (c *invitationCard) widget() *gtk.Box { return c.root }

// calendarSource is the source the card works over.
func (c *invitationCard) calendarSource() calendardata.DataSource {
	if c.source != nil {
		return c.source
	}
	return c.win.calendarSource()
}

// cardOnFor is the account's "Show the Invitation Card" switch
// (calendar.ShowsInvitationCard; an unknown account shows it).
func (c *invitationCard) cardOnFor(acc api.AccountID) bool {
	if c.win == nil {
		return true
	}
	if a, ok := c.win.model.account(acc); ok {
		return calendar.ShowsInvitationCard(&a)
	}
	return calendar.ShowsInvitationCard(nil)
}

// showMessage shows the card of m (message.get; nil while it has not
// answered) for message s, or hides it: no calendar part, an unreadable
// one, kind other or the account's switch off (calendar.InvitationReading,
// CardModeOf).
func (c *invitationCard) showMessage(s api.MessageSummary, m *api.Message) {
	c.showCard(s, m, c.cardOnFor(s.AccountID))
}

// showCard is showMessage with the account's switch given.
func (c *invitationCard) showCard(s api.MessageSummary, m *api.Message, cardOn bool) {
	if m == nil && c.ctl != nil && c.ctl.MessageID() == s.ID && c.ctl.AccountID() == s.AccountID {
		// A render before message.get answered again: the card stays.
		return
	}
	card := calendar.InvitationReading(m, cardOn)
	if card == nil || m.ID != s.ID {
		c.clear()
		return
	}
	if k := c.ctl; k != nil && k.MessageID() == s.ID && k.AccountID() == s.AccountID && c.cardOn == cardOn {
		if c.fed == nil || !sameInvitationCard(*c.fed, *card) {
			fed := *card
			c.fed = &fed
			k.Replace(*card)
		}
		return
	}
	c.clear()
	fed := *card
	c.fed, c.cardOn = &fed, cardOn
	src := c.calendarSource()
	k := calendarctl.NewInvitationController(src, c.win.calendarOptions(), s.AccountID, s.ID, *card, cardOn)
	k.OnChange = c.render
	k.OnOpenLink = func(url string) {
		if c.win != nil {
			c.win.openLink(c.parent, url, nil)
		}
	}
	k.OnShowInCalendar = func(eventID api.EventID, rec api.DateOrTime, day *time.Time) {
		if c.win == nil {
			return
		}
		var d time.Time
		if day != nil {
			d = *day
		}
		c.win.showInCalendar(eventID, rec, d)
	}
	c.ctl = k
	c.observe()
	c.render()
}

// sameInvitationCard compares two cards as message.get sent them.
func sameInvitationCard(a, b api.InvitationCard) bool {
	return reflect.DeepEqual(a, b)
}

// observe follows the source's snapshot (the calendars' and accounts'
// names the card shows).
func (c *invitationCard) observe() {
	if c.token != nil {
		c.token.Cancel()
	}
	c.token = c.calendarSource().Observe(func(calendardata.Change) { c.render() })
}

// clear hides the card and lets its controller go.
func (c *invitationCard) clear() {
	if c.token != nil {
		c.token.Cancel()
		c.token = nil
	}
	if c.ctl != nil {
		c.ctl.Stop()
	}
	c.ctl, c.fed = nil, nil
	c.root.SetVisible(false)
}

// act runs f on the controller, if any.
func (c *invitationCard) act(f func(*calendarctl.InvitationController)) {
	if c.ctl != nil {
		f(c.ctl)
	}
}

// respond answers the invitation.
func (c *invitationCard) respond(r api.EventResponse, withoutReply bool) {
	c.act(func(k *calendarctl.InvitationController) { k.Respond(r, withoutReply) })
}

// setText sets a label and hides it when empty.
func setText(l *gtk.Label, text string) {
	l.SetText(text)
	l.SetVisible(text != "")
}

// render shows the controller's state.
func (c *invitationCard) render() {
	k := c.ctl
	if k == nil || k.Mode() == calendar.CardNone {
		c.root.SetVisible(false)
		return
	}
	c.root.SetVisible(true)
	mode := k.Mode()
	card := k.Card()
	full := mode == calendar.CardFull
	cancelled := mode == calendar.CardCancelled
	reply := mode == calendar.CardReply
	busy := k.Busy()

	pill := k.Pill()
	widget.SetInvitationPill(c.pill, pill)
	// A reply's card is its one line beside the pill.
	title := k.Title()
	if reply {
		title = k.ReplyCard()
		c.title.RemoveCSSClass("heading")
	} else {
		c.title.AddCSSClass("heading")
	}
	setText(c.title, title)
	c.title.SetAttributes(widget.StrikeAttrs(cancelled))

	tile, hasTile := k.DateTile()
	if reply {
		hasTile = false
	}
	c.tile.SetVisible(hasTile)
	if hasTile {
		c.weekday.SetText(tile.Weekday)
		c.day.SetText(tile.Day)
		c.month.SetText(tile.Month)
		tilePill := pill
		if tilePill == calendar.PillNone {
			tilePill = calendar.PillInvitation
		}
		widget.SetInvitationClass(c.tile, tilePill)
	}

	clock := ""
	if !reply {
		clock = k.ClockLine()
	}
	setText(c.clock, clock)
	var previous []string
	if full {
		previous = k.PreviousLines()
	}
	c.previousLines = fillLines(c.previous, c.previousLines, previous, nil)

	location := ""
	if full {
		location = k.Location()
	}
	setText(c.location, location)
	hasCall := full && card.VideoCall != nil && card.VideoCall.URL != ""
	c.join.SetVisible(hasCall)
	c.join.SetTooltipText(k.VideoHost())
	c.locationRow.SetVisible(location != "" || hasCall)
	people := ""
	if full {
		people = k.PeopleLine()
	}
	setText(c.people, people)

	live := full && k.ShowsResponses()
	conflicts := live || (full && k.ShowsStateStrip())
	c.conflictRow.SetVisible(conflicts)
	if conflicts {
		c.conflict.SetText(k.ConflictLine())
		has := len(card.Conflicts) > 0
		if has {
			c.conflictIcon.SetFromIconName("dialog-warning-symbolic")
			c.conflict.AddCSSClass("invitation-conflict")
			c.conflictIcon.AddCSSClass("invitation-conflict")
			c.conflict.RemoveCSSClass("dim-label")
			c.conflictIcon.RemoveCSSClass("dim-label")
		} else {
			c.conflictIcon.SetFromIconName("object-select-symbolic")
			c.conflict.RemoveCSSClass("invitation-conflict")
			c.conflictIcon.RemoveCSSClass("invitation-conflict")
			c.conflict.AddCSSClass("dim-label")
			c.conflictIcon.AddCSSClass("dim-label")
		}
	}
	c.dayStrip = nil
	if conflicts {
		c.dayStrip = k.DayStrip()
	}
	c.strip.SetVisible(c.dayStrip != nil)
	c.strip.QueueDraw()

	// The line of an invitation that cannot be answered ("You organise
	// this event.", outdated, cancelled), with Show in Calendar on its row
	// for the user's own event.
	stateLine := ""
	if !reply {
		stateLine = k.StateLine()
	}
	setText(c.state, stateLine)
	c.stateShow.SetVisible(stateLine != "" && !k.ShowsStateStrip() && k.ShowsInCalendar())
	c.stateRow.SetVisible(stateLine != "")

	c.actions.SetVisible(live)
	if live {
		c.renderTargets(k)
	}
	c.accept.SetSensitive(!busy)
	c.tentative.SetSensitive(!busy)
	c.decline.SetSensitive(!busy)
	c.spinner.SetVisible(busy)

	strip := full && k.ShowsStateStrip()
	c.stripRow.SetVisible(strip)
	if strip {
		c.stripLabel.SetText(k.StateStrip())
		c.change.SetVisible(card.CanRespond)
		c.change.SetSensitive(!busy)
		for i, r := range calendar.ResponseChoices {
			if i < len(c.changeChecks) {
				c.changeChecks[i].SetOpacity(boolOpacity(card.Response == r))
			}
		}
		c.show.SetVisible(k.ShowsInCalendar())
	}

	c.cancelRow.SetVisible(cancelled && card.EventID != "" && !k.CancelApplied())
	c.remove.SetSensitive(!busy)

	var notes []string
	if live {
		notes = k.ReplyNote()
	}
	warn := -1
	if live && card.OrganizerMismatch && len(notes) > 0 {
		warn = len(notes) - 1
	}
	c.noteText = fillLines(c.notes, c.noteText, notes, func(i int, l *gtk.Label) {
		if i == warn {
			l.AddCSSClass("invitation-warning")
			l.RemoveCSSClass("dim-label")
		}
	})
	c.proposeRow.SetVisible(full && k.ShowsProposeNewTime() && card.Start != nil && card.End != nil)
	c.propose.SetSensitive(!busy)

	setText(c.errLabel, k.Error())
}

// fillLines fills box with one grey caption per line when lines differ
// from have (the lines it shows), hides it for none, and returns the
// lines shown; style adjusts line i.
func fillLines(box *gtk.Box, have, lines []string, style func(int, *gtk.Label)) []string {
	same := len(have) == len(lines)
	for i := 0; same && i < len(lines); i++ {
		same = have[i] == lines[i]
	}
	if !same {
		for ch := box.FirstChild(); ch != nil; ch = box.FirstChild() {
			box.Remove(ch)
		}
		for i, line := range lines {
			l := plainLabel(line, "caption", "dim-label")
			l.SetXAlign(0)
			l.SetWrap(true)
			l.SetWrapMode(pango.WrapWordChar)
			l.SetSelectable(true)
			if style != nil {
				style(i, l)
			}
			box.Append(l)
		}
	}
	box.SetVisible(len(lines) > 0)
	return append([]string(nil), lines...)
}

// renderTargets shows "Add to:": a dropdown of the targets, a label when
// the server chose the calendar (or there is one), nothing without targets.
func (c *invitationCard) renderTargets(k *calendarctl.InvitationController) {
	choices := k.Targets()
	c.addTo.SetVisible(len(choices) > 0)
	if k.TargetIsPopup() {
		c.target.SetVisible(true)
		c.targetLabel.SetVisible(false)
		ids := make([]api.CalendarID, len(choices))
		titles := make([]string, len(choices))
		for i, t := range choices {
			ids[i], titles[i] = t.ID, t.Title
		}
		c.settingTarget = true
		if !sameIDs(ids, c.targetIDs) {
			c.target.SetModel(gtk.NewStringList(titles))
			c.targetIDs = ids
		}
		for i, id := range ids {
			if id == k.Target() {
				c.target.SetSelected(uint(i))
			}
		}
		c.settingTarget = false
		c.target.SetSensitive(!k.Busy())
		return
	}
	c.target.SetVisible(false)
	title := ""
	for _, t := range choices {
		if t.ID == k.Target() {
			title = t.Title
		}
	}
	if title == "" && len(choices) > 0 {
		title = choices[0].Title
	}
	setText(c.targetLabel, title)
	c.targetLabel.SetTooltipText(title)
}

// sameIDs compares two lists of calendar ids.
func sameIDs(a, b []api.CalendarID) bool {
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

// menuEntry is one button of a small menu popover.
type menuEntry struct {
	label string
	run   func()
	// check is the entry's check mark (nil: none).
	check *gtk.Image
}

// menuPopover is a popover of flat buttons; separatorBefore marks the
// entries a separator precedes.
func (c *invitationCard) menuPopover(entries []menuEntry, separatorBefore map[int]bool) *gtk.Popover {
	p := gtk.NewPopover()
	p.AddCSSClass("menu")
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	for i, e := range entries {
		if separatorBefore[i] {
			box.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
		}
		b := gtk.NewButton()
		b.AddCSSClass("flat")
		row := gtk.NewBox(gtk.OrientationHorizontal, 6)
		if e.check != nil {
			row.Append(e.check)
		}
		l := plainLabel(e.label)
		l.SetXAlign(0)
		row.Append(l)
		b.SetChild(row)
		run := e.run
		b.ConnectClicked(func() {
			p.Popdown()
			run()
		})
		box.Append(b)
	}
	p.SetChild(box)
	return p
}

// changePopover is Change Response's menu: Accept, Tentative, Decline
// (the current answer checked), then Decline Without Reply.
func (c *invitationCard) changePopover() *gtk.Popover {
	tr := i18n.Tr
	var entries []menuEntry
	for _, r := range calendar.ResponseChoices {
		check := gtk.NewImageFromIconName("object-select-symbolic")
		check.SetOpacity(0)
		c.changeChecks = append(c.changeChecks, check)
		entries = append(entries, menuEntry{label: calendar.ResponseAction(r, tr), check: check,
			run: func() { c.respond(r, false) }})
	}
	blank := gtk.NewImageFromIconName("object-select-symbolic")
	blank.SetOpacity(0)
	entries = append(entries, menuEntry{label: calendar.DeclineWithoutReply(tr), check: blank,
		run: func() { c.respond(api.ResponseDeclined, true) }})
	return c.menuPopover(entries, map[int]bool{len(calendar.ResponseChoices): true})
}

// timePicker is a start or end of Propose New Time…: the day with ‹ ›
// and the hour and minute.
type timePicker struct {
	box          *gtk.Box
	day          time.Time // midnight of the day, in the zone
	dayLabel     *gtk.Label
	hour, minute *gtk.SpinButton
	changed      func()
}

func newTimePicker(t time.Time, env calendar.Env, changed func()) *timePicker {
	loc := env.Loc
	if loc == nil {
		loc = time.Local
	}
	t = t.In(loc)
	p := &timePicker{box: gtk.NewBox(gtk.OrientationHorizontal, 4), changed: changed,
		day: time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)}
	prev := gtk.NewButtonFromIconName("go-previous-symbolic")
	next := gtk.NewButtonFromIconName("go-next-symbolic")
	prev.AddCSSClass("flat")
	next.AddCSSClass("flat")
	p.dayLabel = plainLabel("")
	p.dayLabel.SetWidthChars(14)
	p.hour = gtk.NewSpinButtonWithRange(0, 23, 1)
	p.minute = gtk.NewSpinButtonWithRange(0, 59, 5)
	p.hour.SetWrap(true)
	p.minute.SetWrap(true)
	p.hour.SetValue(float64(t.Hour()))
	p.minute.SetValue(float64(t.Minute()))
	step := func(n int) {
		p.day = calendar.AddDays(p.day, n)
		p.showDay(env)
		p.changed()
	}
	prev.ConnectClicked(func() { step(-1) })
	next.ConnectClicked(func() { step(1) })
	p.hour.ConnectValueChanged(func() { p.changed() })
	p.minute.ConnectValueChanged(func() { p.changed() })
	p.box.Append(prev)
	p.box.Append(p.dayLabel)
	p.box.Append(next)
	p.box.Append(p.hour)
	p.box.Append(plainLabel(":"))
	p.box.Append(p.minute)
	p.showDay(env)
	return p
}

func (p *timePicker) showDay(env calendar.Env) {
	p.dayLabel.SetText(calendar.DayMonthYearText(p.day, env))
}

// value is the picked moment.
func (p *timePicker) value() time.Time {
	return time.Date(p.day.Year(), p.day.Month(), p.day.Day(), p.hour.ValueAsInt(), p.minute.ValueAsInt(), 0, 0, p.day.Location())
}

// proposePopover is Propose New Time…'s popover, made on each opening:
// Starts and Ends from the invitation's time, Cancel and Propose (only
// with the end after the start); Propose answers Tentative with the
// proposal (calendarctl ProposeNewTime).
func (c *invitationCard) proposePopover() *gtk.Popover {
	tr := i18n.Tr
	pop := gtk.NewPopover()
	k := c.ctl
	if k == nil || k.Card().Start == nil || k.Card().End == nil {
		return pop
	}
	env := c.win.calendarEnv()
	card := k.Card()
	propose := gtk.NewButtonWithLabel(calendar.Propose(tr))
	propose.AddCSSClass("suggested-action")
	var start, end *timePicker
	check := func() {
		if start != nil && end != nil {
			propose.SetSensitive(end.value().After(start.value()))
		}
	}
	start = newTimePicker(*card.Start, env, check)
	end = newTimePicker(*card.End, env, check)
	check()
	heading := plainLabel(calendar.ProposeNewTime(tr), "heading")
	heading.SetXAlign(0)
	grid := gtk.NewGrid()
	grid.SetRowSpacing(6)
	grid.SetColumnSpacing(8)
	for i, row := range []struct {
		label string
		p     *timePicker
	}{{calendar.FieldLabel(calendar.FieldStart, tr), start}, {calendar.FieldLabel(calendar.FieldEnd, tr), end}} {
		l := plainLabel(row.label)
		l.SetXAlign(1)
		grid.Attach(l, 0, i, 1, 1)
		grid.Attach(row.p.box, 1, i, 1, 1)
	}
	cancel := gtk.NewButtonWithLabel(calendar.Cancel(tr))
	cancel.ConnectClicked(func() { pop.Popdown() })
	propose.ConnectClicked(func() {
		s, e := start.value(), end.value()
		if !e.After(s) {
			return
		}
		pop.Popdown()
		c.act(func(k *calendarctl.InvitationController) { k.ProposeNewTime(s, e) })
	})
	buttons := gtk.NewBox(gtk.OrientationHorizontal, 8)
	buttons.SetHAlign(gtk.AlignEnd)
	buttons.Append(cancel)
	buttons.Append(propose)
	box := gtk.NewBox(gtk.OrientationVertical, 10)
	box.SetMarginStart(6)
	box.SetMarginEnd(6)
	box.SetMarginTop(6)
	box.SetMarginBottom(6)
	box.Append(heading)
	box.Append(grid)
	box.Append(buttons)
	pop.SetChild(box)
	pop.SetDefaultWidget(propose)
	return pop
}

// The day strip's geometry (InvitationDayStripView.swift).
const (
	stripBarHeight   = 14.0
	stripLabelHeight = 13.0
)

// stripBlue is the slot's colour (#3584e4, the GNOME blue).
var stripBlue = [3]float64{0x35 / 255.0, 0x84 / 255.0, 0xe4 / 255.0}

// drawStrip draws the mini day strip: the axis in the faintest fill, the
// whole hours as hairlines with their times under the bar, the busy spans
// grey, the invitation's slot dashed blue with its time above. Only
// geometry and the app's own time strings; nothing of the part.
func (c *invitationCard) drawStrip(da *gtk.DrawingArea, cr *cairo.Context, width, _ int) {
	s := c.dayStrip
	if s == nil {
		return
	}
	const inset = 1.0
	w := float64(width) - 2*inset
	if w <= 20 {
		return
	}
	fg := da.Color()
	fr, fgG, fb := float64(fg.Red()), float64(fg.Green()), float64(fg.Blue())
	barTop := stripLabelHeight + 2
	x := func(f float64) float64 { return inset + f*w }

	roundRect(cr, inset, barTop, w, stripBarHeight, 3)
	cr.SetSourceRGBA(fr, fgG, fb, 0.06)
	cr.Fill()

	// The hours.
	lastEnd := math.Inf(-1)
	for _, t := range s.Ticks {
		px := x(t.At)
		cr.SetSourceRGBA(fr, fgG, fb, 0.15)
		cr.Rectangle(px-0.5, barTop, 1, stripBarHeight)
		cr.Fill()
		layout := da.CreatePangoLayout(t.Label)
		setSmallFont(layout)
		lw, _ := layout.PixelSize()
		lx := min(max(px-float64(lw)/2, 0), float64(width-lw))
		if lx <= lastEnd+4 {
			continue
		}
		cr.SetSourceRGBA(fr, fgG, fb, 0.45)
		cr.MoveTo(lx, barTop+stripBarHeight+2)
		pangocairo.ShowLayout(cr, layout)
		lastEnd = lx + float64(lw)
	}

	// What is busy already.
	cr.SetSourceRGBA(0.6, 0.6, 0.6, 0.45)
	for _, b := range s.Busy {
		roundRect(cr, x(b.From), barTop+2, max(2, x(b.To)-x(b.From)), stripBarHeight-4, 2)
		cr.Fill()
	}

	// The invitation's slot, dashed, and its time above.
	p := s.Proposed
	if p == nil {
		return
	}
	rx, rw := x(p.From), max(3, x(p.To)-x(p.From))
	roundRect(cr, rx, barTop+0.5, rw, stripBarHeight-1, 3)
	cr.SetSourceRGBA(stripBlue[0], stripBlue[1], stripBlue[2], 0.15)
	cr.FillPreserve()
	cr.SetSourceRGBA(stripBlue[0], stripBlue[1], stripBlue[2], 1)
	cr.SetLineWidth(1.5)
	cr.SetDash([]float64{4, 2}, 0)
	cr.Stroke()
	cr.SetDash(nil, 0)
	if s.ProposedLabel == "" {
		return
	}
	layout := da.CreatePangoLayout(s.ProposedLabel)
	setSmallFont(layout)
	lw, _ := layout.PixelSize()
	lx := min(max(rx+rw/2-float64(lw)/2, 0), float64(width-lw))
	cr.MoveTo(lx, 0)
	pangocairo.ShowLayout(cr, layout)
}

// setSmallFont makes a strip label small and with tabular digits.
func setSmallFont(layout *pango.Layout) {
	attrs := pango.NewAttrList()
	attrs.Insert(pango.NewAttrScale(0.75))
	attrs.Insert(pango.NewAttrFontFeatures("tnum"))
	layout.SetAttributes(attrs)
}

// roundRect adds a rounded rectangle to cr's path.
func roundRect(cr *cairo.Context, x, y, w, h, r float64) {
	r = min(r, w/2, h/2)
	cr.NewSubPath()
	cr.Arc(x+w-r, y+r, r, -math.Pi/2, 0)
	cr.Arc(x+w-r, y+h-r, r, 0, math.Pi/2)
	cr.Arc(x+r, y+h-r, r, math.Pi/2, math.Pi)
	cr.Arc(x+r, y+r, r, math.Pi, 3*math.Pi/2)
	cr.ClosePath()
}
