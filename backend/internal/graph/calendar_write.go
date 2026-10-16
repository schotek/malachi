// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calendar/recur"
	"github.com/schotek/malachi/backend/internal/calendar/tz"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Writes are server-first (D6): each call answers when Graph answered.
// Results carry the object as the server now has it (master and its
// instances in the account's window), refetched after the write; a
// failed refetch leaves Objects empty (the calendar's next pass brings
// it). A failed precondition (412) is conflict, and the result then
// carries the current object so that the core can put it into
// error.data (ConflictEventData).

const localLayout = "2006-01-02T15:04:05"

// graphZone is the zone name written to Graph: the Windows name of an
// IANA zone (tz.WindowsName), else the IANA name, which Graph also takes.
func graphZone(iana string) string {
	if iana == "" {
		return "UTC"
	}
	if w, ok := tz.WindowsName(iana); ok {
		return w
	}
	return iana
}

func graphDateTime(t time.Time, zone string) map[string]string {
	loc, err := calendar.LoadZone(zone)
	if err != nil {
		loc, zone = time.UTC, "UTC"
	}
	return map[string]string{"dateTime": t.In(loc).Format(localLayout), "timeZone": graphZone(zone)}
}

// bodyMode says which fields of an event a write sends.
type bodyMode struct {
	// attendeeOnly: the user does not organise the event; only alerts,
	// show-as and privacy are theirs to change (§4.5).
	attendeeOnly bool
	// recurrence: write the event's recurrence (a master or a new
	// series); clearRecurrence: the series becomes a single event.
	recurrence      bool
	clearRecurrence bool
	// attendees: send the attendee list.
	attendees bool
}

// eventBody is the Graph JSON of ev for a write.
func eventBody(ev *calendar.Event, mode bodyMode) (map[string]any, error) {
	b := map[string]any{}
	if len(ev.Alarms) > 0 {
		b["isReminderOn"], b["reminderMinutesBeforeStart"] = true, ev.Alarms[0]
	} else {
		b["isReminderOn"] = false
	}
	if ev.ShowAs != "" && ev.ShowAs != api.ShowAsUnknown {
		b["showAs"] = string(ev.ShowAs)
	}
	if ev.Private {
		b["sensitivity"] = "private"
	} else {
		b["sensitivity"] = "normal"
	}
	if mode.attendeeOnly {
		return b, nil
	}
	b["subject"] = ev.Title
	notes := ev.Notes
	if vc := ev.VideoCall; vc != nil && vc.URL != "" && !strings.Contains(notes+" "+ev.Location, vc.URL) {
		// Graph has no field for a typed link: it goes with the notes.
		if notes != "" {
			notes += "\n\n"
		}
		notes += vc.URL
	}
	b["body"] = map[string]string{"contentType": "text", "content": notes}
	b["location"] = map[string]string{"displayName": ev.Location}
	if ev.AllDay {
		b["isAllDay"] = true
		b["start"] = map[string]string{"dateTime": string(ev.StartDate) + "T00:00:00", "timeZone": "UTC"}
		b["end"] = map[string]string{"dateTime": string(ev.EndDate) + "T00:00:00", "timeZone": "UTC"}
	} else {
		b["isAllDay"] = false
		b["start"] = graphDateTime(ev.Start, ev.Zone)
		ez := ev.EndZone
		if ez == "" {
			ez = ev.Zone
		}
		b["end"] = graphDateTime(ev.End, ez)
	}
	if mode.attendees {
		list := make([]map[string]any, 0, len(ev.Attendees))
		for _, a := range ev.Attendees {
			typ := "required"
			switch {
			case a.Type == api.AttendeeTypeResource || a.Type == api.AttendeeTypeRoom:
				typ = "resource"
			case a.Role == api.AttendeeOptional:
				typ = "optional"
			}
			list = append(list, map[string]any{"type": typ, "emailAddress": map[string]string{"address": a.Address, "name": a.Name}})
		}
		b["attendees"] = list
	}
	if ev.OnlineMeetingProvider != "" {
		b["isOnlineMeeting"], b["onlineMeetingProvider"] = true, ev.OnlineMeetingProvider
	}
	switch {
	case mode.recurrence && ev.RRule != "":
		rule, err := recur.Parse(ev.RRule)
		if err != nil {
			return nil, api.NewError(api.CodeRecurrenceUnsupported, "graph: recurrence: %s", err.Error())
		}
		loc := ev.Loc()
		pr, err := patternFromRule(rule, ev.Start, loc, ev.AllDay, graphZone(ev.Zone))
		if err != nil {
			return nil, api.NewError(api.CodeRecurrenceUnsupported, "graph: recurrence Graph cannot hold")
		}
		if len(ev.RDates) > 0 {
			return nil, api.NewError(api.CodeRecurrenceUnsupported, "graph: extra dates Graph cannot hold")
		}
		b["recurrence"] = pr
	case mode.clearRecurrence:
		b["recurrence"] = nil
	}
	return b, nil
}

// transactionID is the idempotence key of a create (Graph dedupes a
// repeated POST with the same transactionId).
func transactionID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "malachi-" + hex.EncodeToString(h[:16])
}

func readOnlyCalendar(cal calsync.StoredCalendar) error {
	if cal.Access == api.CalendarAccessRead {
		return api.NewError(api.CodeReadOnly, "graph: calendar is read-only")
	}
	return nil
}

// result refetches the object id after a write.
func (p *CalendarProvider) result(ctx context.Context, cal calsync.StoredCalendar, id string) calsync.WriteResult {
	obj, gone, err := p.seriesObject(ctx, p.sharedCalendar(cal), id, p.window(), PriorityHigh)
	switch {
	case err != nil:
		return calsync.WriteResult{}
	case gone:
		return calsync.WriteResult{Removed: []string{id}}
	}
	return calsync.WriteResult{Objects: []calendar.Object{obj}}
}

// conflict answers a failed precondition with the current object.
func (p *CalendarProvider) conflict(ctx context.Context, cal calsync.StoredCalendar, id string, err error) (calsync.WriteResult, error) {
	res := p.result(ctx, cal, id)
	if len(res.Removed) > 0 {
		return res, api.NewError(api.CodeEventGone, "graph: the event is gone")
	}
	return res, api.NewError(api.CodeConflict, "graph: the event changed on the server")
}

// fail maps a write failure, with the current object for a conflict.
func (p *CalendarProvider) fail(ctx context.Context, cal calsync.StoredCalendar, id string, err error) (calsync.WriteResult, error) {
	if isPrecondition(err) && id != "" {
		return p.conflict(ctx, cal, id, err)
	}
	return calsync.WriteResult{}, p.writeError(err)
}

func invitationsOf(n int, notify bool) api.InvitationsSent {
	if n == 0 || !notify {
		return api.InvitationsSent{Via: api.InvitationsViaNone}
	}
	return api.InvitationsSent{Via: api.InvitationsViaServer, Count: n}
}

// checkAttachments applies D21's limits before anything is sent.
func checkAttachments(atts []calendar.Attachment) error {
	if len(atts) > api.MaxEventAttachments {
		return api.NewError(api.CodeInvalidArgument, "graph: more than %d attachments", api.MaxEventAttachments)
	}
	for _, a := range atts {
		if len(a.Data) > api.MaxEventAttachmentBytes || a.Size > api.MaxEventAttachmentBytes {
			e := api.NewError(api.CodeAttachmentTooBig, "graph: attachment over %d bytes", api.MaxEventAttachmentBytes)
			e.Data = map[string]int64{"limit": api.MaxEventAttachmentBytes, "size": max(a.Size, int64(len(a.Data)))}
			return e
		}
	}
	return nil
}

func attachmentBody(a calendar.Attachment) map[string]any {
	name := calendar.CleanLine(a.Name, calendar.MaxNameBytes)
	if name == "" {
		name = "attachment"
	}
	ct := calendar.CleanLine(a.MediaType, 128)
	if ct == "" {
		ct = "application/octet-stream"
	}
	return map[string]any{
		"@odata.type":  "#microsoft.graph.fileAttachment",
		"name":         name,
		"contentType":  ct,
		"contentBytes": base64.StdEncoding.EncodeToString(a.Data),
	}
}

// Create writes a new event into cal (POST …/events with a transactionId
// from the UID). With attachments and attendees the event is created
// without attendees first, the files added, then the attendees: Exchange
// sends the invitations when attendees appear, and they then carry the
// files. A failed attachment deletes the new event again (nobody was
// invited yet), so a create is all or nothing.
func (p *CalendarProvider) Create(ctx context.Context, cal calsync.StoredCalendar, ev calsync.Event, opts calsync.WriteOptions) (calsync.WriteResult, error) {
	if err := readOnlyCalendar(cal); err != nil {
		return calsync.WriteResult{}, err
	}
	if err := checkAttachments(opts.Attachments); err != nil {
		return calsync.WriteResult{}, err
	}
	late := len(opts.Attachments) > 0 && len(ev.Attendees) > 0
	body, err := eventBody(&ev, bodyMode{recurrence: true, attendees: !late})
	if err != nil {
		return calsync.WriteResult{}, err
	}
	body["transactionId"] = transactionID("create", cal.RemoteID, ev.UID)
	var created graphEvent
	if err := p.c.calDo(ctx, calReq{method: http.MethodPost, url: calendarPath(cal.RemoteID) + "/events", body: body, out: &created, prio: PriorityHigh}); err != nil {
		return calsync.WriteResult{}, p.writeError(err)
	}
	if created.ID == "" {
		return calsync.WriteResult{}, api.NewError(api.CodeServerError, "graph: created event without an id")
	}
	undo := func(err error) (calsync.WriteResult, error) {
		_ = p.c.calDo(context.WithoutCancel(ctx), calReq{method: http.MethodDelete, url: eventPath(created.ID), prio: PriorityHigh})
		return calsync.WriteResult{}, p.writeError(err)
	}
	for _, a := range opts.Attachments {
		if err := p.c.calDo(ctx, calReq{method: http.MethodPost, url: eventPath(created.ID) + "/attachments", body: attachmentBody(a), prio: PriorityHigh}); err != nil {
			return undo(err)
		}
	}
	if late {
		ab, _ := eventBody(&ev, bodyMode{attendees: true})
		if err := p.c.calDo(ctx, calReq{method: http.MethodPatch, url: eventPath(created.ID), body: map[string]any{"attendees": ab["attendees"]}, prio: PriorityHigh}); err != nil {
			return undo(err)
		}
	}
	res := p.result(ctx, cal, created.ID)
	res.Invitations = invitationsOf(len(ev.Attendees), true)
	return res, nil
}

// recurrenceIDFor is the instance a write is about.
func recurrenceIDFor(ev store.Event, rid ...api.DateOrTime) api.DateOrTime {
	for _, r := range rid {
		if r != "" {
			return r
		}
	}
	return api.DateOrTime(ev.RecurrenceID)
}

// seriesModel is the part of a stored series the instance lookup needs.
func seriesModel(ev store.Event) *calendar.Event {
	return &calendar.Event{AllDay: ev.AllDay, Zone: ev.TimeZone, Kind: calendar.KindMaster, UID: ev.UID}
}

// findInstance finds the occurrence or exception of series id whose
// recurrence id is rid (an exception may have moved: ±31 days are
// searched).
func (p *CalendarProvider) findInstance(ctx context.Context, ev store.Event, rid api.DateOrTime) (*graphEvent, error) {
	t, ok := calendar.ParseKey(rid)
	if !ok {
		return nil, api.NewError(api.CodeInvalidArgument, "graph: bad recurrence id")
	}
	insts, err := p.instances(ctx, ev.RemoteID, calsync.Window{From: t.AddDate(0, 0, -31), To: t.AddDate(0, 0, 32)}, PriorityHigh)
	if err != nil {
		return nil, err
	}
	m := seriesModel(ev)
	for _, gi := range insts {
		inst, ok := p.eventOf(false, gi, m)
		if !ok {
			continue
		}
		if recurrenceIDOf(gi, m, inst) == rid {
			return gi, nil
		}
	}
	return nil, api.NewError(api.CodeEventGone, "graph: no such instance on the server")
}

// instanceMatch returns the If-Match of an instance write: the client's
// version may be the instance's own or the series'.
func (p *CalendarProvider) instanceMatch(ctx context.Context, ev store.Event, inst *graphEvent, ifMatch string) (string, error) {
	if ifMatch == "" || ifMatch == inst.version() {
		return inst.version(), nil
	}
	m, err := p.getEvent(ctx, ev.RemoteID, PriorityHigh)
	if err != nil {
		return "", err
	}
	if m.version() != ifMatch {
		return "", &StatusError{Status: http.StatusPreconditionFailed, Code: "PreconditionFailed", Message: "the series changed"}
	}
	return inst.version(), nil
}

// cutBefore ends the series id before the instance starting at t (D19:
// range endDate = the day before, in the series' zone) and returns how
// many of its exceptions from t on Graph drops with them.
func (p *CalendarProvider) cutBefore(ctx context.Context, ev store.Event, t time.Time, ifMatch string) (int, error) {
	m, err := p.getEvent(ctx, ev.RemoteID, PriorityHigh)
	if err != nil {
		return 0, err
	}
	if ifMatch != "" && m.version() != ifMatch {
		return 0, &StatusError{Status: http.StatusPreconditionFailed, Code: "PreconditionFailed", Message: "the series changed"}
	}
	if m.Recurrence == nil {
		return 0, api.NewError(api.CodeInvalidArgument, "graph: the event is not a series")
	}
	loc := time.UTC
	if !ev.AllDay {
		if l, err := calendar.LoadZone(firstNonEmpty(zoneName(m.Recurrence.Range.RecurrenceTimeZone), ev.TimeZone)); err == nil {
			loc = l
		}
	}
	day := t.In(loc)
	end := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	dropped := 0
	w := p.window()
	if t.Before(w.To) {
		insts, err := p.instances(ctx, ev.RemoteID, calsync.Window{From: maxTime(t.AddDate(0, 0, -31), w.From), To: w.To}, PriorityHigh)
		if err == nil {
			mm := seriesModel(ev)
			for _, gi := range insts {
				if gi.Type != "exception" {
					continue
				}
				if inst, ok := p.eventOf(false, gi, mm); ok {
					if k, ok := calendar.ParseKey(recurrenceIDOf(gi, mm, inst)); ok && !k.Before(t) {
						dropped++
					}
				}
			}
		}
	}
	pr := *m.Recurrence
	pr.Range.Type, pr.Range.EndDate, pr.Range.NumberOfOccurrences = "endDate", end.Format(time.DateOnly), 0
	if err := p.c.calDo(ctx, calReq{method: http.MethodPatch, url: eventPath(ev.RemoteID), body: map[string]any{"recurrence": pr}, ifMatch: m.version(), prio: PriorityHigh}); err != nil {
		return 0, err
	}
	return dropped, nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// Update writes a change within its scope (D19): all patches the series
// (or single event) with If-Match; this patches the occurrence (Graph
// makes it an exception); thisAndFuture ends the series the day before
// the instance (range endDate) and creates the rest as a new series. An
// attendee's change carries only alerts, show-as and privacy.
func (p *CalendarProvider) Update(ctx context.Context, cal calsync.StoredCalendar, ev calsync.StoredEvent, ch calsync.Change, opts calsync.WriteOptions) (calsync.WriteResult, error) {
	if err := readOnlyCalendar(cal); err != nil {
		return calsync.WriteResult{}, err
	}
	id := ev.RemoteID
	attendeeOnly := !ev.IAmOrganizer
	series := ev.Kind == store.EventMaster || ev.Kind == store.EventOverride
	scope := ch.Scope
	if !series {
		scope = api.EditScopeAll
	}
	rid := recurrenceIDFor(ev, ch.RecurrenceID, opts.RecurrenceID)
	if scope == api.EditScopeThisAndFuture {
		if t, ok := calendar.ParseKey(rid); !ok || !t.After(ev.StartAt) && ev.Kind == store.EventMaster {
			scope = api.EditScopeAll
		}
	}
	if scope != api.EditScopeAll && ev.Recurrence != nil && ev.Recurrence.Complex {
		return calsync.WriteResult{}, api.NewError(api.CodeRecurrenceUnsupported, "graph: a complex rule is edited as a whole only")
	}
	attendees := len(ch.Event.Attendees)
	switch scope {
	case api.EditScopeThis:
		inst, err := p.findInstance(ctx, ev, rid)
		if err != nil {
			return p.fail(ctx, cal, id, err)
		}
		match, err := p.instanceMatch(ctx, ev, inst, opts.IfMatch)
		if err != nil {
			return p.fail(ctx, cal, id, err)
		}
		body, err := eventBody(&ch.Event, bodyMode{attendeeOnly: attendeeOnly, attendees: !attendeeOnly})
		if err != nil {
			return calsync.WriteResult{}, err
		}
		if err := p.c.calDo(ctx, calReq{method: http.MethodPatch, url: eventPath(inst.ID), body: body, ifMatch: match, prio: PriorityHigh}); err != nil {
			return p.fail(ctx, cal, id, err)
		}
		res := p.result(ctx, cal, id)
		res.Invitations = invitationsOf(attendees*boolInt(!attendeeOnly), true)
		return res, nil
	case api.EditScopeThisAndFuture:
		if attendeeOnly {
			return calsync.WriteResult{}, api.NewError(api.CodeReadOnly, "graph: only the organiser splits a series")
		}
		t, _ := calendar.ParseKey(rid)
		dropped, err := p.cutBefore(ctx, ev, t, opts.IfMatch)
		if err != nil {
			return p.fail(ctx, cal, id, err)
		}
		nev := ch.Event
		body, err := eventBody(&nev, bodyMode{recurrence: true, attendees: true})
		if err != nil {
			return calsync.WriteResult{}, err
		}
		body["transactionId"] = transactionID("split", cal.RemoteID, id, string(rid), nev.UID)
		var created graphEvent
		if err := p.c.calDo(ctx, calReq{method: http.MethodPost, url: calendarPath(cal.RemoteID) + "/events", body: body, out: &created, prio: PriorityHigh}); err != nil {
			return calsync.WriteResult{}, p.writeError(err)
		}
		res := p.result(ctx, cal, id)
		if created.ID != "" {
			more := p.result(ctx, cal, created.ID)
			res.Objects = append(res.Objects, more.Objects...)
		}
		res.DroppedExceptions = dropped
		res.Invitations = invitationsOf(attendees, true)
		return res, nil
	}
	mode := bodyMode{attendeeOnly: attendeeOnly, attendees: !attendeeOnly}
	dropped := 0
	if !attendeeOnly && !ch.KeepRecurrence {
		switch {
		case ch.Event.RRule != "":
			mode.recurrence = true
		case ev.Kind == store.EventMaster:
			mode.clearRecurrence = true
		}
		if ev.Kind == store.EventMaster && ch.Event.RRule != ev.RRule {
			if insts, err := p.instances(ctx, id, p.window(), PriorityHigh); err == nil {
				for _, gi := range insts {
					if gi.Type == "exception" {
						dropped++
					}
				}
			}
		}
	}
	body, err := eventBody(&ch.Event, mode)
	if err != nil {
		return calsync.WriteResult{}, err
	}
	if err := p.c.calDo(ctx, calReq{method: http.MethodPatch, url: eventPath(id), body: body, ifMatch: opts.IfMatch, prio: PriorityHigh}); err != nil {
		return p.fail(ctx, cal, id, err)
	}
	res := p.result(ctx, cal, id)
	res.DroppedExceptions = dropped
	res.Invitations = invitationsOf(attendees*boolInt(!attendeeOnly), true)
	return res, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Delete removes an event within its scope. The organiser's delete of an
// event with attendees cancels it for them (POST …/cancel with the
// comment) when Notify; an attendee's delete with Notify declines first
// (with the comment). thisAndFuture ends the series before the instance.
func (p *CalendarProvider) Delete(ctx context.Context, cal calsync.StoredCalendar, ev calsync.StoredEvent, scope calsync.Scope, opts calsync.WriteOptions) (calsync.WriteResult, error) {
	if err := readOnlyCalendar(cal); err != nil {
		return calsync.WriteResult{}, err
	}
	id := ev.RemoteID
	series := ev.Kind == store.EventMaster || ev.Kind == store.EventOverride
	if !series {
		scope = api.EditScopeAll
	}
	rid := recurrenceIDFor(ev, opts.RecurrenceID)
	cancels := ev.IAmOrganizer && ev.AttendeeCount > 0 && opts.Notify
	declines := !ev.IAmOrganizer && opts.Notify && ev.MyResponse != api.ResponseDeclined
	comment := capString(opts.Comment, api.MaxCommentBytes)
	inv := api.InvitationsSent{Via: api.InvitationsViaNone}
	if cancels {
		inv = invitationsOf(ev.AttendeeCount, true)
	} else if declines {
		inv = api.InvitationsSent{Via: api.InvitationsViaServer, Count: 1}
	}
	target, match := id, opts.IfMatch
	switch scope {
	case api.EditScopeThisAndFuture:
		t, ok := calendar.ParseKey(rid)
		if !ok || !t.After(ev.StartAt) {
			break
		}
		if !ev.IAmOrganizer {
			return calsync.WriteResult{}, api.NewError(api.CodeReadOnly, "graph: only the organiser ends a series")
		}
		if _, err := p.cutBefore(ctx, ev, t, opts.IfMatch); err != nil {
			return p.fail(ctx, cal, id, err)
		}
		res := p.result(ctx, cal, id)
		res.Invitations = invitationsOf(ev.AttendeeCount, true)
		return res, nil
	case api.EditScopeThis:
		inst, err := p.findInstance(ctx, ev, rid)
		if err != nil {
			return p.fail(ctx, cal, id, err)
		}
		if match, err = p.instanceMatch(ctx, ev, inst, opts.IfMatch); err != nil {
			return p.fail(ctx, cal, id, err)
		}
		target = inst.ID
	}
	switch {
	case cancels:
		body := map[string]any{"comment": comment}
		if err := p.c.calDo(ctx, calReq{method: http.MethodPost, url: eventPath(target) + "/cancel", body: body, ifMatch: match, prio: PriorityHigh}); err != nil {
			return p.fail(ctx, cal, id, err)
		}
	default:
		if declines {
			body := map[string]any{"comment": comment, "sendResponse": true}
			if err := p.c.calDo(ctx, calReq{method: http.MethodPost, url: eventPath(target) + "/decline", body: body, ifMatch: match, prio: PriorityHigh}); err != nil && !isItemGone(err) {
				return p.fail(ctx, cal, id, err)
			}
			match = "" // the decline changed the version
		}
		if err := p.c.calDo(ctx, calReq{method: http.MethodDelete, url: eventPath(target), ifMatch: match, prio: PriorityHigh}); err != nil && !(isItemGone(err) && declines) {
			return p.fail(ctx, cal, id, err)
		}
	}
	var res calsync.WriteResult
	if target == id {
		res = calsync.WriteResult{Removed: []string{id}}
	} else {
		res = p.result(ctx, cal, id)
	}
	res.Invitations = inv
	return res, nil
}

// Respond answers as an attendee: accept, tentativelyAccept or decline on
// the instance (scope this) or the series, with the comment, sendResponse
// = Notify and, for tentative or declined, proposedNewTime (D20).
func (p *CalendarProvider) Respond(ctx context.Context, cal calsync.StoredCalendar, ev calsync.StoredEvent, r calsync.Response, opts calsync.WriteOptions) (calsync.WriteResult, error) {
	var action string
	switch r.Response {
	case api.ResponseAccepted:
		action = "accept"
	case api.ResponseTentative:
		action = "tentativelyAccept"
	case api.ResponseDeclined:
		action = "decline"
	default:
		return calsync.WriteResult{}, api.NewError(api.CodeInvalidArgument, "graph: bad response")
	}
	if ev.IAmOrganizer {
		return calsync.WriteResult{}, api.NewError(api.CodeInvalidArgument, "graph: the organiser does not respond")
	}
	body := map[string]any{"sendResponse": opts.Notify}
	if c := capString(opts.Comment, api.MaxCommentBytes); c != "" {
		body["comment"] = c
	}
	if r.ProposedStart != nil || r.ProposedEnd != nil {
		switch {
		case r.ProposedStart == nil || r.ProposedEnd == nil || r.ProposedEnd.Before(*r.ProposedStart):
			return calsync.WriteResult{}, api.NewError(api.CodeInvalidArgument, "graph: a proposed time needs both ends")
		case action == "accept":
			return calsync.WriteResult{}, api.NewError(api.CodeInvalidArgument, "graph: a new time goes with tentative or declined")
		case !ev.NewTimeOK:
			return calsync.WriteResult{}, api.NewError(api.CodeInvalidArgument, "graph: the organiser allows no new time")
		case !opts.Notify:
			return calsync.WriteResult{}, api.NewError(api.CodeInvalidArgument, "graph: a new time is sent with the response")
		}
		body["proposedNewTime"] = map[string]any{
			"start": map[string]string{"dateTime": r.ProposedStart.UTC().Format(localLayout), "timeZone": "UTC"},
			"end":   map[string]string{"dateTime": r.ProposedEnd.UTC().Format(localLayout), "timeZone": "UTC"},
		}
	}
	id := ev.RemoteID
	target, match := id, opts.IfMatch
	if r.Scope == api.EditScopeThis && (ev.Kind == store.EventMaster || ev.Kind == store.EventOverride) {
		inst, err := p.findInstance(ctx, ev, recurrenceIDFor(ev, opts.RecurrenceID))
		if err != nil {
			return p.fail(ctx, cal, id, err)
		}
		if match, err = p.instanceMatch(ctx, ev, inst, opts.IfMatch); err != nil {
			return p.fail(ctx, cal, id, err)
		}
		target = inst.ID
	}
	if err := p.c.calDo(ctx, calReq{method: http.MethodPost, url: eventPath(target) + "/" + action, body: body, ifMatch: match, prio: PriorityHigh}); err != nil {
		return p.fail(ctx, cal, id, err)
	}
	res := p.result(ctx, cal, id)
	if opts.Notify {
		res.Invitations = api.InvitationsSent{Via: api.InvitationsViaServer, Count: 1}
	} else {
		res.Invitations = api.InvitationsSent{Via: api.InvitationsViaNone}
	}
	return res, nil
}
