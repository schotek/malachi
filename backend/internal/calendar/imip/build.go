// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imip

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calendar/ics"
	"github.com/schotek/malachi/backend/pkg/api"
)

// iTIP methods the daemon sends (smtp.CalendarPart.Method).
const (
	MethodRequest = "REQUEST"
	MethodReply   = "REPLY"
	MethodCancel  = "CANCEL"
)

// BuildReply builds the REPLY to an invitation (RFC 5546 §3.2.3): one
// VEVENT with UID, RECURRENCE-ID of an instance invitation, the
// invitation's SEQUENCE, DTSTAMP now, the ORGANIZER, only the user's
// ATTENDEE (me, the address it was invited as: "" = Invitation.Me) with
// PARTSTAT, DTSTART/DTEND and SUMMARY copied (Outlook needs them) and a
// COMMENT (cleaned, ≤ api.MaxCommentBytes). Times are copied verbatim
// with the organiser's VTIMEZONEs they use. The reply goes to the
// organiser only (Recipients); never to From or Reply-To (D10).
func BuildReply(inv Invitation, me string, response api.EventResponse, comment string, now time.Time) ([]byte, error) {
	switch response {
	case api.ResponseAccepted, api.ResponseTentative, api.ResponseDeclined:
	default:
		return nil, ErrResponse
	}
	if inv.Organizer == nil || inv.Organizer.Address == "" {
		return nil, ErrNoOrganizer
	}
	if me == "" && inv.Me != nil {
		me = inv.Me.Address
	}
	me = ics.ParseCalAddress("mailto:" + strings.TrimSpace(me)).Email
	if me == "" {
		return nil, ErrNoAttendee
	}
	ev := &inv.Event
	out := &ics.Component{Name: "VEVENT"}
	out.Add(ics.NewText("UID", inv.UID))
	src := inv.vevent
	copied := func(name string) bool {
		if src == nil {
			return false
		}
		p := src.Property(name)
		if p == nil {
			return false
		}
		if _, err := ics.EncodeComponent(&ics.Component{Name: "X", Props: []ics.Property{*p}}); err != nil {
			return false
		}
		out.Add(*p)
		return true
	}
	if inv.RecurrenceID != "" && !copied("RECURRENCE-ID") {
		t, ok := calendar.ParseKey(inv.RecurrenceID)
		if !ok {
			return nil, fmt.Errorf("imip: bad recurrence id")
		}
		out.Add(ics.NewTime("RECURRENCE-ID", ics.UTCOf(t)))
	}
	out.Add(ics.NewProperty("SEQUENCE", strconv.Itoa(inv.Sequence)))
	out.Add(ics.NewTime("DTSTAMP", ics.UTCOf(now)))
	org := ics.NewProperty("ORGANIZER", "mailto:"+inv.Organizer.Address)
	if inv.Organizer.Name != "" {
		org.SetParam("CN", strings.ReplaceAll(inv.Organizer.Name, `"`, "'"))
	}
	out.Add(org)
	at := ics.NewProperty("ATTENDEE", "mailto:"+me)
	at.SetParam("PARTSTAT", calendar.Partstat(response))
	out.Add(at)
	if !ev.Start.IsZero() {
		if !copied("DTSTART") {
			out.Add(timeOf("DTSTART", ev, ev.Start))
		}
		if !copied("DTEND") && !copied("DURATION") {
			out.Add(timeOf("DTEND", ev, ev.End))
		}
	}
	if ev.Title != "" {
		out.Add(ics.NewText("SUMMARY", ev.Title))
	}
	if c := calendar.CleanBlock(comment, api.MaxCommentBytes); c != "" {
		out.Add(ics.NewText("COMMENT", c))
	}
	cal := newCalendar(MethodReply)
	cal.Components = append(zonesFor(out, inv.timezones, ev), out)
	return ics.EncodeComponent(cal)
}

// timeOf writes a time of ev in UTC (or as a date / floating clock).
func timeOf(name string, ev *calendar.Event, t time.Time) ics.Property {
	switch {
	case ev.AllDay:
		return ics.NewTime(name, ics.DateOf(t))
	case ev.Floating:
		return ics.NewTime(name, ics.Time{Kind: ics.KindFloating, Year: t.Year(), Month: t.Month(), Day: t.Day(), Hour: t.Hour(), Minute: t.Minute(), Second: t.Second()})
	}
	return ics.NewTime(name, ics.UTCOf(t))
}

func newCalendar(method string) *ics.Component {
	cal := &ics.Component{Name: "VCALENDAR"}
	cal.Add(ics.NewProperty("PRODID", calendar.ProdID))
	cal.Add(ics.NewProperty("VERSION", "2.0"))
	cal.Add(ics.NewProperty("METHOD", method))
	return cal
}

// zonesFor returns the VTIMEZONEs of the TZIDs ev uses: the original
// ones, else generated for an IANA zone.
func zonesFor(c *ics.Component, orig []*ics.Component, ev *calendar.Event) []*ics.Component {
	var out []*ics.Component
	seen := map[string]bool{}
	for _, p := range c.Props {
		id := p.Param("TZID")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		found := false
		for _, z := range orig {
			if propValue(z, "TZID") == id {
				if _, err := ics.EncodeComponent(&ics.Component{Name: "VCALENDAR", Components: []*ics.Component{z}}); err == nil {
					out, found = append(out, z), true
				}
				break
			}
		}
		if found {
			continue
		}
		if loc, err := calendar.LoadZone(id); err == nil && loc != time.UTC {
			if z, err := calendar.VTimezone(loc, ev.Start, ev.End); err == nil {
				out = append(out, z)
			}
		}
	}
	return out
}

// BuildRequest builds the REQUEST of an event the user organises (RFC
// 5546 §3.2.2), from the object as stored after a successful write: the
// master and its overrides, ORGANIZER the sending account, every ATTENDEE
// with RSVP=TRUE;PARTSTAT=NEEDS-ACTION;SCHEDULE-AGENT=CLIENT (the
// organiser's own entry ACCEPTED, without RSVP), SEQUENCE as the event
// holds it (the caller increments it on a significant change:
// calendar.Significant), DTSTAMP now, VTIMEZONEs from tz. An event
// without attendees is ErrNoAttendee.
func BuildRequest(obj calendar.Object, organizer api.Address, now time.Time) ([]byte, error) {
	org := ics.ParseCalAddress("mailto:" + strings.TrimSpace(organizer.Address)).Email
	if org == "" {
		return nil, ErrNoOrganizer
	}
	organizer.Address = org
	o := withOrganizer(obj, organizer)
	if o.Master == nil && len(o.Overrides) == 0 {
		return nil, calendar.ErrNoEvent
	}
	any := false
	for _, e := range o.Events() {
		any = any || len(e.Attendees) > 0
	}
	if !any {
		return nil, ErrNoAttendee
	}
	cal, err := calendar.ToComponent(o, calendar.WriteOptions{Now: now, Method: MethodRequest, ProdID: calendar.ProdID, IgnoreRaw: true})
	if err != nil {
		return nil, err
	}
	for _, c := range cal.Children("VEVENT") {
		schedule(c, org, false)
	}
	return ics.EncodeComponent(cal)
}

// BuildCancel builds the CANCEL of an event the user organises (RFC 5546
// §3.2.5): for EditScopeAll the master (or every override of an object
// without one), for EditScopeThis the instance rid (its override, or the
// master's instance), each with STATUS:CANCELLED and SEQUENCE incremented
// by one (the caller stores the new SEQUENCE when the event lives on).
// The ATTENDEEs are those of the events given: to cancel for removed
// attendees only, pass the event with those attendees
// (RemovedAttendees). EditScopeThisAndFuture is ErrScope: the series is
// cut, and the cut master goes as a REQUEST.
func BuildCancel(obj calendar.Object, organizer api.Address, scope api.EditScope, rid api.DateOrTime, now time.Time) ([]byte, error) {
	org := ics.ParseCalAddress("mailto:" + strings.TrimSpace(organizer.Address)).Email
	if org == "" {
		return nil, ErrNoOrganizer
	}
	organizer.Address = org
	o := withOrganizer(obj, organizer)
	var out calendar.Object
	switch scope {
	case api.EditScopeAll, "":
		out = o
		out.Occurrences = nil
	case api.EditScopeThis:
		inst := o.Override(rid)
		var ev calendar.Event
		switch {
		case inst != nil:
			ev = *inst
		case o.Master != nil && calendar.HasInstance(o.Master, rid):
			t, _ := calendar.ParseKey(rid)
			ev = *o.Master
			length := ev.End.Sub(ev.Start)
			ev.Kind, ev.RecurrenceID = calendar.KindOverride, rid
			ev.RRule, ev.RDates, ev.EXDates, ev.Recurrence = "", nil, nil, nil
			ev.Start, ev.End = t, t.Add(length)
			if ev.AllDay {
				ev.End = t.AddDate(0, 0, int(length.Hours()+12)/24)
				ev.StartDate, ev.EndDate = api.Date(ev.Start.Format(time.DateOnly)), api.Date(ev.End.Format(time.DateOnly))
			}
		default:
			return nil, calendar.ErrNoEvent
		}
		if o.Master != nil && o.Master.Sequence > ev.Sequence {
			ev.Sequence = o.Master.Sequence
		}
		out = calendar.Object{Master: o.Master, Overrides: []calendar.Event{ev}}
	default:
		return nil, ErrScope
	}
	cancel := out
	if scope == api.EditScopeThis {
		cancel.Master = nil // the master gives the overrides' form, it is not written
	}
	for _, e := range cancel.Events() {
		e.Status = api.EventStatusCancelled
		e.Sequence++
	}
	var cal *ics.Component
	var err error
	if scope == api.EditScopeThis && out.Master != nil {
		// Write the master too so that the override's RECURRENCE-ID takes
		// the master's form, then drop the master's VEVENT.
		both := out
		m := *out.Master
		both.Master = &m
		both.Overrides = cancel.Overrides
		cal, err = calendar.ToComponent(both, calendar.WriteOptions{Now: now, Method: MethodCancel, ProdID: calendar.ProdID, IgnoreRaw: true})
		if err == nil {
			var keep []*ics.Component
			for _, c := range cal.Components {
				if c.Name == "VEVENT" && c.Property("RECURRENCE-ID") == nil {
					continue
				}
				keep = append(keep, c)
			}
			cal.Components = keep
		}
	} else {
		cal, err = calendar.ToComponent(cancel, calendar.WriteOptions{Now: now, Method: MethodCancel, ProdID: calendar.ProdID, IgnoreRaw: true})
	}
	if err != nil {
		return nil, err
	}
	for _, c := range cal.Children("VEVENT") {
		schedule(c, org, true)
	}
	return ics.EncodeComponent(cal)
}

func withOrganizer(obj calendar.Object, organizer api.Address) calendar.Object {
	o := obj
	o.Raw = nil
	if obj.Master != nil {
		m := *obj.Master
		m.Organizer, m.IAmOrganizer = &organizer, true
		o.Master = &m
	}
	o.Overrides = append([]calendar.Event(nil), obj.Overrides...)
	for i := range o.Overrides {
		o.Overrides[i].Organizer, o.Overrides[i].IAmOrganizer = &organizer, true
	}
	return o
}

// schedule sets the iMIP parameters of a VEVENT's ORGANIZER and ATTENDEEs
// and adds SEQUENCE and SUMMARY when absent (both required by RFC 5546).
func schedule(c *ics.Component, organizer string, cancel bool) {
	if c.Property("SEQUENCE") == nil {
		c.Add(ics.NewProperty("SEQUENCE", "0"))
	}
	if c.Property("SUMMARY") == nil {
		c.Add(ics.NewText("SUMMARY", ""))
	}
	for i := range c.Props {
		p := &c.Props[i]
		switch p.Name {
		case "ORGANIZER":
			p.SetParam("SCHEDULE-AGENT", "CLIENT")
		case "ATTENDEE":
			if p.CalAddress().SameEmail(organizer) {
				p.SetParam("PARTSTAT", "ACCEPTED")
			} else if !cancel {
				p.SetParam("RSVP", "TRUE")
				p.SetParam("PARTSTAT", "NEEDS-ACTION")
			}
			p.SetParam("SCHEDULE-AGENT", "CLIENT")
		}
	}
}

// RemovedAttendees returns the attendees of before that after no longer
// has (after nil: all of before's).
func RemovedAttendees(before, after *calendar.Event) []api.Attendee {
	if before == nil {
		return nil
	}
	var out []api.Attendee
	for _, a := range before.Attendees {
		if after == nil || !hasAttendee(after, a.Address) {
			out = append(out, a)
		}
	}
	return out
}

func hasAttendee(ev *calendar.Event, addr string) bool {
	for _, a := range ev.Attendees {
		if strings.EqualFold(a.Address, addr) {
			return true
		}
	}
	return false
}

// Recipients returns who an iMIP message goes to, lower-case, without
// duplicates and never one of the user's addresses (me):
//   - REPLY: the organiser only (of after, else before);
//   - REQUEST: the attendees of after (added and kept), not the organiser;
//   - CANCEL: the attendees before had and after has not (after nil: all
//     of before's), not the organiser.
func Recipients(method string, before, after *calendar.Event, me []string) []string {
	mine := map[string]bool{}
	for _, a := range me {
		mine[strings.ToLower(strings.TrimSpace(a))] = true
	}
	var out []string
	add := func(addr string) {
		addr = strings.ToLower(strings.TrimSpace(addr))
		if addr == "" || mine[addr] {
			return
		}
		for _, x := range out {
			if x == addr {
				return
			}
		}
		out = append(out, addr)
	}
	organizer := ""
	for _, e := range []*calendar.Event{after, before} {
		if e != nil && e.Organizer != nil {
			organizer = strings.ToLower(e.Organizer.Address)
			break
		}
	}
	switch strings.ToUpper(method) {
	case MethodReply:
		add(organizer)
	case MethodRequest:
		if after != nil {
			for _, a := range after.Attendees {
				if !strings.EqualFold(a.Address, organizer) {
					add(a.Address)
				}
			}
		}
	case MethodCancel:
		for _, a := range RemovedAttendees(before, after) {
			if !strings.EqualFold(a.Address, organizer) {
				add(a.Address)
			}
		}
	}
	return out
}
