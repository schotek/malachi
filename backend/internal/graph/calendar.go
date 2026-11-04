// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calendar/recur"
	"github.com/schotek/malachi/backend/internal/calendar/tz"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The subset of Microsoft Graph's calendar resources the provider reads.
// Every string in them is hostile input: it is cleaned and capped when it
// becomes a calendar.Event.

type graphCalendar struct {
	ID                            string        `json:"id"`
	Name                          string        `json:"name"`
	Color                         string        `json:"color"`
	HexColor                      string        `json:"hexColor"`
	CanEdit                       bool          `json:"canEdit"`
	IsDefaultCalendar             bool          `json:"isDefaultCalendar"`
	Owner                         *emailAddress `json:"owner"`
	AllowedOnlineMeetingProviders []string      `json:"allowedOnlineMeetingProviders"`
	DefaultOnlineMeetingProvider  string        `json:"defaultOnlineMeetingProvider"`
	ChangeKey                     string        `json:"changeKey"`
}

type dateTimeTimeZone struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

type itemBody struct {
	ContentType string `json:"contentType"` // text | html
	Content     string `json:"content"`
}

type graphLocation struct {
	DisplayName string `json:"displayName"`
}

type responseStatus struct {
	Response string `json:"response"` // none|organizer|tentativelyAccepted|accepted|declined|notResponded
}

type graphAttendee struct {
	Type         string          `json:"type"` // required|optional|resource
	Status       *responseStatus `json:"status"`
	EmailAddress emailAddress    `json:"emailAddress"`
}

type onlineMeetingInfo struct {
	JoinURL string `json:"joinUrl"`
}

type recurrencePattern struct {
	Type           string   `json:"type"`
	Interval       int      `json:"interval"`
	Month          int      `json:"month"`
	DayOfMonth     int      `json:"dayOfMonth"`
	DaysOfWeek     []string `json:"daysOfWeek,omitempty"`
	FirstDayOfWeek string   `json:"firstDayOfWeek,omitempty"`
	Index          string   `json:"index,omitempty"`
}

type recurrenceRange struct {
	Type                string `json:"type"` // endDate|noEnd|numbered
	StartDate           string `json:"startDate"`
	EndDate             string `json:"endDate,omitempty"`
	RecurrenceTimeZone  string `json:"recurrenceTimeZone,omitempty"`
	NumberOfOccurrences int    `json:"numberOfOccurrences,omitempty"`
}

type patternedRecurrence struct {
	Pattern recurrencePattern `json:"pattern"`
	Range   recurrenceRange   `json:"range"`
}

// graphEvent is an event as calendarView, delta, instances and GET
// return it; Removed is set for a delta tombstone.
type graphEvent struct {
	ID                         string               `json:"id"`
	ETag                       string               `json:"@odata.etag"`
	ChangeKey                  string               `json:"changeKey"`
	Type                       string               `json:"type"` // singleInstance|occurrence|exception|seriesMaster
	SeriesMasterID             string               `json:"seriesMasterId"`
	ICalUID                    string               `json:"iCalUId"`
	OccurrenceID               string               `json:"occurrenceId"`
	Subject                    string               `json:"subject"`
	BodyPreview                string               `json:"bodyPreview"`
	Body                       *itemBody            `json:"body"`
	Location                   *graphLocation       `json:"location"`
	Locations                  []graphLocation      `json:"locations"`
	Start                      *dateTimeTimeZone    `json:"start"`
	End                        *dateTimeTimeZone    `json:"end"`
	OriginalStart              string               `json:"originalStart"`
	OriginalStartTimeZone      string               `json:"originalStartTimeZone"`
	OriginalEndTimeZone        string               `json:"originalEndTimeZone"`
	IsAllDay                   *bool                `json:"isAllDay"`
	ShowAs                     string               `json:"showAs"`
	Sensitivity                string               `json:"sensitivity"`
	IsCancelled                bool                 `json:"isCancelled"`
	ResponseStatus             *responseStatus      `json:"responseStatus"`
	ResponseRequested          bool                 `json:"responseRequested"`
	Organizer                  *recipient           `json:"organizer"`
	Attendees                  []graphAttendee      `json:"attendees"`
	IsOrganizer                bool                 `json:"isOrganizer"`
	IsOnlineMeeting            bool                 `json:"isOnlineMeeting"`
	OnlineMeeting              *onlineMeetingInfo   `json:"onlineMeeting"`
	OnlineMeetingURL           string               `json:"onlineMeetingUrl"`
	OnlineMeetingProvider      string               `json:"onlineMeetingProvider"`
	IsReminderOn               bool                 `json:"isReminderOn"`
	ReminderMinutesBeforeStart int                  `json:"reminderMinutesBeforeStart"`
	AllowNewTimeProposals      bool                 `json:"allowNewTimeProposals"`
	Recurrence                 *patternedRecurrence `json:"recurrence"`
	CreatedDateTime            string               `json:"createdDateTime"`
	LastModifiedDateTime       string               `json:"lastModifiedDateTime"`
	Removed                    *removed             `json:"@removed"`
}

// version is what If-Match carries: the @odata.etag, else the change key
// as a weak tag.
func (e *graphEvent) version() string {
	if e.ETag != "" {
		return e.ETag
	}
	if e.ChangeKey != "" {
		return `W/"` + e.ChangeKey + `"`
	}
	return ""
}

// ---------------------------------------------------------------------------
// Calendars
// ---------------------------------------------------------------------------

// colorPalette maps Graph's calendarColor names to the hex colours
// Outlook shows (the two the spike saw are the service's own hexColor
// values). "auto", "maxColor" and anything else: no server colour.
var colorPalette = map[string]string{
	"lightBlue":   "#0078d4",
	"lightGreen":  "#13a10e",
	"lightOrange": "#e67e22",
	"lightGray":   "#7a7574",
	"lightYellow": "#c19c00",
	"lightTeal":   "#038387",
	"lightPink":   "#e3008c",
	"lightBrown":  "#8e562e",
	"lightRed":    "#d13438",
}

var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// calendarColor is the calendar's colour as #rrggbb, "" for none.
func calendarColor(gc graphCalendar) string {
	if h := strings.TrimSpace(gc.HexColor); hexColorRE.MatchString(h) {
		return strings.ToLower(h)
	}
	return colorPalette[gc.Color]
}

// Calendars lists the account's calendars (at most
// api.MaxCalendarsPerAccount). Access: canEdit false is read; a writable
// calendar whose owner is not one of Me is write, else owner. Kind is
// always events: Graph has no property that tells the birthdays or a
// holidays calendar apart reliably (both are just canEdit false, and
// their names are localised), so they are read-only events calendars.
func (p *CalendarProvider) Calendars(ctx context.Context) ([]calsync.RemoteCalendar, error) {
	var out []calsync.RemoteCalendar
	next := "me/calendars?$top=200"
	online := false
	for pages := 0; next != "" && pages < 50; pages++ {
		var pg page[graphCalendar]
		if err := p.c.calDo(ctx, calReq{method: "GET", url: next, out: &pg, prio: PriorityLow}); err != nil {
			return nil, p.readError(err)
		}
		for _, gc := range pg.Value {
			if gc.ID == "" || len(out) >= api.MaxCalendarsPerAccount {
				continue
			}
			rc := calsync.RemoteCalendar{
				RemoteID:  gc.ID,
				Name:      calendar.CleanLine(gc.Name, calendar.MaxNameBytes),
				Color:     calendarColor(gc),
				Access:    api.CalendarAccessOwner,
				Kind:      api.CalendarKindEvents,
				IsDefault: gc.IsDefaultCalendar,
			}
			if gc.Owner != nil && gc.Owner.Address != "" && len(p.me) > 0 && !p.isMe(gc.Owner.Address) {
				rc.Owner = &api.Address{Name: calendar.CleanLine(gc.Owner.Name, calendar.MaxNameBytes), Address: calendar.CleanLine(gc.Owner.Address, calendar.MaxNameBytes)}
				rc.Access = api.CalendarAccessWrite
			}
			if !gc.CanEdit {
				rc.Access = api.CalendarAccessRead
			}
			for _, prov := range gc.AllowedOnlineMeetingProviders {
				if prov = calendar.CleanLine(prov, 64); prov != "" && !strings.EqualFold(prov, "unknown") && len(rc.OnlineMeetingProviders) < 8 {
					rc.OnlineMeetingProviders = append(rc.OnlineMeetingProviders, prov)
				}
			}
			if gc.IsDefaultCalendar && len(rc.OnlineMeetingProviders) > 0 {
				online = true
			}
			out = append(out, rc)
		}
		next = pg.NextLink
	}
	p.mu.Lock()
	p.onlineMeetings = online
	p.mu.Unlock()
	return out, nil
}

// ---------------------------------------------------------------------------
// Events → the model
// ---------------------------------------------------------------------------

// parseGraphTime reads a dateTimeTimeZone ("2022-04-12T08:00:00.0000000"
// with seven fractional digits, any zone name tz resolves). An unknown
// zone reads as UTC (the Prefer header asks for UTC, so that is what the
// service sends).
func parseGraphTime(v *dateTimeTimeZone) (wall time.Time, inst time.Time, ok bool) {
	if v == nil {
		return time.Time{}, time.Time{}, false
	}
	s := strings.TrimSpace(v.DateTime)
	if len(s) > 40 {
		return time.Time{}, time.Time{}, false
	}
	s = strings.TrimSuffix(s, "Z")
	w, err := time.ParseInLocation("2006-01-02T15:04:05", s, time.UTC) // fractions are accepted when parsing
	if err != nil {
		if w, err = time.ParseInLocation("2006-01-02T15:04", s, time.UTC); err != nil {
			return time.Time{}, time.Time{}, false
		}
	}
	if w.Year() < recur.MinYear || w.Year() > recur.MaxUntilYear {
		return time.Time{}, time.Time{}, false
	}
	w = w.Truncate(time.Second)
	loc := time.UTC
	if name := strings.TrimSpace(v.TimeZone); name != "" && !strings.EqualFold(name, "UTC") {
		if l, _, err := tz.Resolve(name); err == nil {
			loc = l
		}
	}
	return w, wallIn(w, loc), true
}

// wallIn is the instant of wall clock w (UTC fields) in loc.
func wallIn(w time.Time, loc *time.Location) time.Time {
	if loc == time.UTC {
		return w
	}
	return time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), 0, loc).UTC()
}

// zoneName resolves a Graph zone name (Windows or IANA) to the IANA name
// Event.Zone holds; "" when it does not resolve.
func zoneName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.EqualFold(name, "UTC") || strings.EqualFold(name, "tzone://Microsoft/Utc") {
		return "UTC"
	}
	loc, kind, err := tz.Resolve(name)
	if err != nil || kind == tz.KindFixed || kind == tz.KindMatched {
		return ""
	}
	return loc.String()
}

func parseStamp(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 40 {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC()
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", strings.TrimSuffix(s, "Z"), time.UTC); err == nil {
		return t
	}
	return time.Time{}
}

func showAsOf(s string) api.ShowAs {
	switch s {
	case "free":
		return api.ShowAsFree
	case "tentative":
		return api.ShowAsTentative
	case "busy":
		return api.ShowAsBusy
	case "oof":
		return api.ShowAsOOF
	case "workingElsewhere":
		return api.ShowAsWorkingElsewhere
	case "unknown":
		return api.ShowAsUnknown
	}
	return api.ShowAsBusy
}

func responseOf(s string) api.EventResponse {
	switch s {
	case "accepted":
		return api.ResponseAccepted
	case "tentativelyAccepted":
		return api.ResponseTentative
	case "declined":
		return api.ResponseDeclined
	case "notResponded":
		return api.ResponseNeedsAction
	}
	return ""
}

func (p *CalendarProvider) addressOf(e emailAddress) (api.Address, bool) {
	addr := calendar.CleanLine(e.Address, calendar.MaxNameBytes)
	if addr == "" || !strings.Contains(addr, "@") {
		return api.Address{}, false
	}
	return api.Address{Name: calendar.CleanLine(e.Name, calendar.MaxNameBytes), Address: addr}, true
}

// eventOf turns one Graph event into the model. master, when given, is
// the series the instance belongs to: its UID and zone are inherited.
// eventOf turns one Graph event into the model. shared: the event is in a
// calendar the user does not own, where responseStatus, isOrganizer and
// responseRequested are those of that mailbox's owner: the user's answer
// is then only that of the attendee who is one of Me.
func (p *CalendarProvider) eventOf(shared bool, ge *graphEvent, master *calendar.Event) (calendar.Event, bool) {
	ev := calendar.Event{Kind: calendar.KindSingle, Status: api.EventStatusConfirmed, RecurOK: true}
	switch ge.Type {
	case "seriesMaster":
		ev.Kind = calendar.KindMaster
	case "occurrence", "exception":
		ev.Kind = calendar.KindOverride
	}
	ev.UID = calendar.CleanLine(ge.ICalUID, calendar.MaxUIDBytes)
	if master != nil && master.UID != "" {
		ev.UID = master.UID
	}
	if ev.UID == "" {
		ev.UID = calendar.CleanLine("graph-"+ge.ID, calendar.MaxUIDBytes)
	}
	ev.Title = calendar.CleanLine(ge.Subject, calendar.MaxTitleBytes)
	ev.Location = locationOf(ge)
	switch {
	case ge.Body != nil && strings.EqualFold(ge.Body.ContentType, "html"):
		ev.Notes = calendar.NotesFromHTML(capString(ge.Body.Content, 4*calendar.MaxNotesBytes))
	case ge.Body != nil && strings.TrimSpace(ge.Body.Content) != "":
		ev.Notes = calendar.CleanBlock(capString(ge.Body.Content, 4*calendar.MaxNotesBytes), calendar.MaxNotesBytes)
	default:
		ev.Notes = calendar.CleanBlock(capString(ge.BodyPreview, 4*calendar.MaxNotesBytes), calendar.MaxNotesBytes)
	}
	if len(ev.Notes) > calendar.MaxNotesBytes {
		ev.Notes = truncateUTF8(ev.Notes, calendar.MaxNotesBytes)
	}
	ev.Links = mergeLinkLists(calendar.Links(ev.Notes), calendar.Links(ev.Location))
	join := ""
	if ge.OnlineMeeting != nil {
		join = ge.OnlineMeeting.JoinURL
	}
	if join == "" {
		join = ge.OnlineMeetingURL
	}
	ev.VideoCall = calendar.DetectVideoCall(ev.Location, ev.Notes, nil, capString(join, 4*calendar.MaxLinkBytes))

	// Times.
	zone := zoneName(ge.OriginalStartTimeZone)
	if zone == "" && ge.Recurrence != nil {
		zone = zoneName(ge.Recurrence.Range.RecurrenceTimeZone)
	}
	if zone == "" && master != nil {
		zone = master.Zone
	}
	sw, si, ok1 := parseGraphTime(ge.Start)
	ew, ei, ok2 := parseGraphTime(ge.End)
	if !ok1 {
		return calendar.Event{}, false
	}
	if !ok2 {
		ew, ei = sw, si
	}
	if allDayOf(ge, master, sw, ew) {
		s := allDayDate(si, ge.OriginalStartTimeZone)
		e := allDayDate(ei, firstNonEmpty(ge.OriginalEndTimeZone, ge.OriginalStartTimeZone))
		if !e.After(s) {
			e = s.AddDate(0, 0, 1)
		}
		ev.AllDay, ev.Start, ev.End = true, s, e
		ev.StartDate, ev.EndDate = api.Date(s.Format(time.DateOnly)), api.Date(e.Format(time.DateOnly))
	} else {
		if ei.Before(si) {
			ei = si
		}
		ev.Start, ev.End = si.UTC(), ei.UTC()
		if zone == "" {
			zone = "UTC"
		}
		ev.Zone = zone
		if ez := zoneName(ge.OriginalEndTimeZone); ez != "" && ez != zone {
			ev.EndZone = ez
		}
	}

	ev.ShowAs = showAsOf(ge.ShowAs)
	ev.Private = ge.Sensitivity == "private" || ge.Sensitivity == "confidential"
	if ge.IsCancelled {
		ev.Status = api.EventStatusCancelled
	}
	if ge.Organizer != nil {
		if a, ok := p.addressOf(ge.Organizer.EmailAddress); ok {
			ev.Organizer = &a
		}
	}
	ev.IAmOrganizer = ge.IsOrganizer
	if shared {
		ev.IAmOrganizer = ev.Organizer != nil && p.isMe(ev.Organizer.Address)
	}
	if !shared && !ge.IsOrganizer && ge.ResponseStatus != nil {
		ev.MyResponse = responseOf(ge.ResponseStatus.Response)
		if ev.MyResponse == "" && len(ge.Attendees) > 0 {
			ev.MyResponse = api.ResponseNeedsAction
		}
	}
	ev.ResponseRequested = ge.ResponseRequested && !ev.IAmOrganizer
	ev.NewTimeOK = ge.AllowNewTimeProposals && !ev.IAmOrganizer
	for _, ga := range ge.Attendees {
		a, ok := p.addressOf(ga.EmailAddress)
		if !ok {
			ev.AttendeeCount++
			continue
		}
		ev.AttendeeCount++
		if len(ev.Attendees) >= api.MaxEventAttendeesStored {
			continue
		}
		at := api.Attendee{Name: a.Name, Address: a.Address, Role: api.AttendeeRequired, Response: api.ResponseNeedsAction, Type: api.AttendeeTypePerson}
		switch ga.Type {
		case "optional":
			at.Role = api.AttendeeOptional
		case "resource":
			at.Role, at.Type = api.AttendeeNonParticipant, api.AttendeeTypeResource
		}
		if ga.Status != nil {
			if r := responseOf(ga.Status.Response); r != "" {
				at.Response = r
			}
		}
		at.Me = p.isMe(at.Address)
		if shared && at.Me && !ev.IAmOrganizer && ev.MyResponse == "" {
			ev.MyResponse = at.Response
		}
		if ev.Organizer != nil && strings.EqualFold(ev.Organizer.Address, at.Address) {
			at.Organizer = true
		}
		ev.Attendees = append(ev.Attendees, at)
	}
	if shared && ev.MyResponse == "" {
		// Not the user's invitation: whatever the owner was asked.
		ev.ResponseRequested, ev.NewTimeOK = false, false
	}
	if ge.IsReminderOn && ge.ReminderMinutesBeforeStart >= 0 && ge.ReminderMinutesBeforeStart <= api.MaxAlertMinutes {
		ev.Alarms = []int{ge.ReminderMinutesBeforeStart}
	}
	ev.Created, ev.Updated = parseStamp(ge.CreatedDateTime), parseStamp(ge.LastModifiedDateTime)

	if ev.Kind == calendar.KindMaster && ge.Recurrence != nil {
		p.applyRecurrence(&ev, ge.Recurrence)
	}
	return ev, true
}

// sharedCalendar: a calendar the user does not own (another mailbox's,
// or one the user may only read, such as a holidays calendar).
func (p *CalendarProvider) sharedCalendar(cal calsync.StoredCalendar) bool {
	if cal.Access != "" && cal.Access != api.CalendarAccessOwner {
		return true
	}
	return cal.OwnerAddress != "" && len(p.me) > 0 && !p.isMe(cal.OwnerAddress)
}

// locationOf is location.displayName with the locations[] names it does
// not already hold: Exchange's displayName is often the list joined, so
// every part is kept once (case-insensitive), in order.
func locationOf(ge *graphEvent) string {
	var parts []string
	seen := map[string]bool{}
	add := func(s string) {
		s = calendar.CleanLine(s, calendar.MaxLocationBytes)
		if k := strings.ToLower(s); s != "" && !seen[k] && len(parts) < 20 {
			seen[k] = true
			parts = append(parts, s)
		}
	}
	if ge.Location != nil {
		for _, s := range strings.Split(capString(ge.Location.DisplayName, 4*calendar.MaxLocationBytes), ";") {
			add(s)
		}
	}
	for i, l := range ge.Locations {
		if i >= 20 {
			break
		}
		add(l.DisplayName)
	}
	return calendar.CleanLine(strings.Join(parts, "; "), calendar.MaxLocationBytes)
}

// allDayOf: the event is all-day. isAllDay decides when the service sent
// it; an instance of an all-day series is all-day; without the field an
// event from midnight to midnight (UTC or its own zone) is.
func allDayOf(ge *graphEvent, master *calendar.Event, sw, ew time.Time) bool {
	if ge.IsAllDay != nil && *ge.IsAllDay {
		return true
	}
	if master != nil && master.AllDay && ge.Type != "seriesMaster" {
		return true
	}
	if ge.IsAllDay != nil {
		return false
	}
	midnight := func(t time.Time) bool { return t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 }
	return midnight(sw) && midnight(ew) && ew.After(sw)
}

// allDayDate is the date of an all-day boundary. With the UTC Prefer
// header Graph sends midnight UTC of the date, or the event zone's
// midnight in UTC ("2026-10-27T22:00:00" with Central Europe Standard
// Time): the instant is read in the event's zone (tz.Resolve; UTC when
// unknown) and rounded to the nearest midnight.
func allDayDate(inst time.Time, zone string) time.Time {
	loc := time.UTC
	if z := strings.TrimSpace(zone); z != "" {
		if l, _, err := tz.Resolve(z); err == nil {
			loc = l
		}
	}
	l := inst.In(loc).Add(12 * time.Hour)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

// applyRecurrence sets the series' rule from Graph's pattern; one it
// cannot express is kept as a complex shape (Graph still expands it).
func (p *CalendarProvider) applyRecurrence(ev *calendar.Event, pr *patternedRecurrence) {
	loc := ev.Loc()
	if z := zoneName(pr.Range.RecurrenceTimeZone); z != "" && !ev.AllDay {
		if l, err := calendar.LoadZone(z); err == nil {
			loc = l
		}
	}
	rule, err := ruleFromPattern(*pr, ev.AllDay, loc)
	if err != nil {
		ev.RecurOK = false
		ev.Recurrence = &api.Recurrence{Frequency: frequencyOfPattern(pr.Pattern.Type), Interval: max(1, min(pr.Pattern.Interval, 1000)), Complex: true}
		return
	}
	ev.RRule = recur.Normalise(rule)
	r := recur.ToAPI(rule, loc)
	ev.Recurrence = &api.Recurrence{
		Frequency: api.RecurrenceFrequency(r.Frequency), Interval: r.Interval,
		ByDay: r.ByDay, ByMonthDay: r.ByMonthDay, ByMonth: r.ByMonth,
		Until: api.Date(r.Until), Count: r.Count, WeekStart: r.WeekStart, Complex: r.Complex,
	}
}

// recurrenceIDOf is an instance's recurrence id in the form of its
// series: the original start (UTC instant for timed series, the original
// date for all-day ones: occurrenceId's date, else originalStart in the
// series' zone).
func recurrenceIDOf(ge *graphEvent, master *calendar.Event, inst calendar.Event) api.DateOrTime {
	allDay := inst.AllDay
	if master != nil {
		allDay = master.AllDay
	}
	orig := parseStamp(ge.OriginalStart)
	if allDay {
		if i := strings.LastIndexByte(ge.OccurrenceID, '.'); i >= 0 {
			if d, err := time.ParseInLocation(time.DateOnly, ge.OccurrenceID[i+1:], time.UTC); err == nil {
				return api.DateOrTime(d.Format(time.DateOnly))
			}
		}
		if !orig.IsZero() {
			loc := time.UTC
			if master != nil {
				if l, err := calendar.LoadZone(firstNonEmpty(zoneName(ge.OriginalStartTimeZone), master.Zone)); err == nil {
					loc = l
				}
			}
			return api.DateOrTime(orig.In(loc).Format(time.DateOnly))
		}
		return api.DateOrTime(inst.Start.Format(time.DateOnly))
	}
	if orig.IsZero() {
		orig = inst.Start
	}
	return api.DateOrTime(orig.UTC().Format("2006-01-02T15:04:05Z"))
}

// objectOf builds the Object of one series (or single event): the master
// (nil when it could not be fetched), its exceptions as overrides, and
// Graph's expansion as Occurrences. instances are the series' occurrence
// and exception items in the window; for a single event, the event itself.
func (p *CalendarProvider) objectOf(shared bool, masterItem *graphEvent, instances []*graphEvent) (calendar.Object, bool) {
	var obj calendar.Object
	var master *calendar.Event
	if masterItem != nil {
		m, ok := p.eventOf(shared, masterItem, nil)
		if !ok && masterItem.Type != "seriesMaster" {
			return obj, false
		}
		if ok {
			master = &m
		}
		obj.RemoteID, obj.ETag = masterItem.ID, masterItem.version()
	}
	if masterItem != nil && masterItem.Type != "seriesMaster" {
		// A single event.
		obj.Master = master
		if master.Status != api.EventStatusCancelled {
			obj.Occurrences = []calendar.Occurrence{{Start: master.Start, End: master.End, AllDay: master.AllDay, Override: -1, RemoteID: masterItem.ID}}
		}
		return obj, true
	}
	obj.Master = master
	seen := map[api.DateOrTime]bool{}
	for _, gi := range instances {
		if len(obj.Occurrences) >= api.MaxSeriesOccurrences {
			break
		}
		inst, ok := p.eventOf(shared, gi, master)
		if !ok {
			continue
		}
		key := recurrenceIDOf(gi, master, inst)
		if seen[key] {
			continue
		}
		seen[key] = true
		if obj.RemoteID == "" {
			obj.RemoteID = gi.SeriesMasterID
		}
		occ := calendar.Occurrence{Key: key, RecurrenceID: key, Start: inst.Start, End: inst.End, AllDay: inst.AllDay, Override: -1, Series: true, RemoteID: gi.ID}
		if gi.Type == "exception" || master == nil {
			inst.Kind, inst.RecurrenceID = calendar.KindOverride, key
			obj.Overrides = append(obj.Overrides, inst)
			occ.Override = len(obj.Overrides) - 1
		}
		if inst.Status == api.EventStatusCancelled {
			continue
		}
		obj.Occurrences = append(obj.Occurrences, occ)
	}
	if obj.RemoteID == "" {
		return obj, false
	}
	if master != nil && master.Recurrence != nil {
		master.Recurrence.Exceptions = len(obj.Overrides)
	}
	return obj, true
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func capString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return truncateUTF8(s, n)
}

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

func mergeLinkLists(lists ...[]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range lists {
		for _, x := range l {
			if !seen[x] && len(out) < calendar.MaxLinks {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	return out
}

// eventPath is the URL path of one event id (escaped: ids are opaque).
func eventPath(id string) string { return "me/events/" + url.PathEscape(id) }

func calendarPath(id string) string { return "me/calendars/" + url.PathEscape(id) }
