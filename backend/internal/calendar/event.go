// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calendar

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar/ics"
	"github.com/schotek/malachi/backend/internal/calendar/recur"
	"github.com/schotek/malachi/backend/internal/calendar/tz"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Kind is the kind of an event row (cal_events.kind).
type Kind string

// Kinds of Event.
const (
	KindSingle   Kind = "single"   // no recurrence
	KindMaster   Kind = "master"   // a series: RRULE, RDATE or overrides
	KindOverride Kind = "override" // a modified instance (RECURRENCE-ID)
)

// Scope is the edit scope of a recurring event (D19).
type Scope = api.EditScope

// Availability is one person's free/busy.
type Availability = api.Availability

// Key formats of RecurrenceID and Occurrence.Key: a timed instance is its
// UTC instant, an all-day instance its date, a floating instance its wall
// clock as if UTC (with a Z, as stored in cal_occurrences).
const (
	keyTime = "2006-01-02T15:04:05Z"
	keyDate = time.DateOnly
)

// Event is one VEVENT (or one Graph event) in the daemon's terms: the
// fields of a cal_events row. Every string is cleaned (text.go).
type Event struct {
	UID  string // ≤ MaxUIDBytes, cleaned
	Kind Kind
	// RecurrenceID of an override: the original start of the instance it
	// replaces, in the form of the master's start (Key formats).
	RecurrenceID api.DateOrTime
	Sequence     int
	DTStamp      time.Time // UTC; zero when absent

	Title     string
	Location  string
	Notes     string   // plain text (D11)
	Links     []string // https links of Notes and Location
	VideoCall *api.VideoCall
	// OnlineMeetingProvider asks the source to create a meeting
	// (EventInput.videoCall mode "create"; Graph onlineMeetingProvider).
	OnlineMeetingProvider string

	AllDay   bool
	Floating bool
	// Start and End: timed, UTC instants; all-day and floating, the wall
	// clock as if UTC (all-day: midnight of StartDate and EndDate).
	Start, End         time.Time
	StartDate, EndDate api.Date // all-day only; EndDate exclusive
	Zone               string   // IANA zone of Start ("UTC" for a UTC time; "" all-day and floating)
	EndZone            string   // IANA zone of End when it differs from Zone

	// Recurrence of a master: RRule is the normalised rule (recur.Normalise),
	// "" when absent or refused; RDates and EXDates are instants (timed) or
	// wall clocks as if UTC (all-day, floating).
	RRule      string
	RDates     []time.Time
	EXDates    []time.Time
	Recurrence *api.Recurrence // the editor's shape; Complex for what it cannot show
	// RecurOK is false when the series' rule was refused (recur.Parse or
	// CheckStart): only the first instance is shown.
	RecurOK bool

	Status  api.EventStatus
	ShowAs  api.ShowAs
	Private bool

	Organizer         *api.Address
	IAmOrganizer      bool              // the organiser is one of the user's addresses
	MyResponse        api.EventResponse // "" when the user is not an attendee
	ResponseRequested bool
	NewTimeOK         bool // Graph allowNewTimeProposals (never set from iCalendar)
	// Attendees, at most api.MaxEventAttendeesStored; AttendeeCount counts
	// every ATTENDEE, also those not listed (over the cap, not mailto:).
	Attendees     []api.Attendee
	AttendeeCount int
	// Alarms in minutes before start, sorted, ≤ api.MaxEventAlerts: only
	// DISPLAY and AUDIO alarms (D12).
	Alarms []int
	// Attachments are the names of ATTACH properties (never fetched).
	Attachments []string

	Created, Updated time.Time // UTC; zero when absent
}

// Attachment is a file of a new event (D21): the outgoing path only.
type Attachment struct {
	Name      string
	MediaType string
	Size      int64
	Data      []byte // ≤ api.MaxEventAttachmentBytes
}

// Change is an update of an event within a scope (ApplyChange).
type Change struct {
	Scope Scope
	// RecurrenceID names the instance for EditScopeThis and
	// EditScopeThisAndFuture; "" for a single event or EditScopeAll.
	RecurrenceID api.DateOrTime
	// Event holds the new values: of the series for EditScopeAll, of the
	// instance for EditScopeThis, of the new series that starts at the
	// instance for EditScopeThisAndFuture.
	Event Event
	// KeepRecurrence keeps the series' rule as it is (an update of a
	// complex rule, EventInput.keepRecurrence).
	KeepRecurrence bool
}

// Response is the user's answer as an attendee.
type Response struct {
	Scope         Scope
	Response      api.EventResponse
	ProposedStart *time.Time
	ProposedEnd   *time.Time
}

// Problem is something FromICS tolerated or dropped.
type Problem struct {
	Code   string
	Detail string // technical English, never text of the input
}

// Problem codes.
const (
	ProblemICS               = "ics"               // the parser noted problems (Detail: their count)
	ProblemMultipleCalendars = "multipleCalendars" // more than one VCALENDAR; the first is read
	ProblemMixedUIDs         = "mixedUIDs"         // VEVENTs of other UIDs, ignored
	ProblemDuplicate         = "duplicate"         // a second master or override with the same id, ignored
	ProblemNoStart           = "noStart"           // a VEVENT without a usable DTSTART
	ProblemZoneUnknown       = "zoneUnknown"       // a TZID resolved to nothing: read as floating
	ProblemZoneFixed         = "zoneFixed"         // a TZID resolved to a fixed offset (Etc/GMT∓N or UTC)
	ProblemEndBeforeStart    = "endBeforeStart"    // DTEND/DURATION before DTSTART: zero length (all-day: one day)
	ProblemMixedTimeKinds    = "mixedTimeKinds"    // DTSTART and DTEND of different kinds
	ProblemRecurrence        = "recurrence"        // the rule was refused: first instance only
	ProblemTooManyDates      = "tooManyDates"      // RDATE or EXDATE over the caps of recur
	ProblemAttendees         = "attendees"         // ATTENDEEs over the cap or not mailto:, counted not listed
	ProblemBadValue          = "badValue"          // a value that does not read (SEQUENCE, a time), ignored
)

// Object is one remote object: a CalDAV resource (the master and its
// overrides of one UID) or a Graph event with its exceptions.
type Object struct {
	RemoteID string // CalDAV href or Graph event id
	ETag     string
	// Master is the single event or the series master; nil when the
	// object holds only overrides (an invitation to one instance).
	Master    *Event
	Overrides []Event
	// Occurrences are the source's own expansion (Graph calendarView);
	// empty when the daemon expands (CalDAV): see Occurrences.
	Occurrences []Occurrence
	// Raw is the resource as fetched (≤ api.MaxICSBytes), kept so that a
	// rewrite (ToICS) preserves what the model does not hold; nil for
	// Graph.
	Raw      []byte
	Problems []Problem
}

// UID is the object's UID (the master's, else the first override's).
func (o *Object) UID() string {
	if o.Master != nil {
		return o.Master.UID
	}
	if len(o.Overrides) > 0 {
		return o.Overrides[0].UID
	}
	return ""
}

// Override returns the override with that recurrence id, or nil.
func (o *Object) Override(rid api.DateOrTime) *Event {
	for i := range o.Overrides {
		if o.Overrides[i].RecurrenceID == rid {
			return &o.Overrides[i]
		}
	}
	return nil
}

// Events returns the master (when present) and the overrides.
func (o *Object) Events() []*Event {
	var out []*Event
	if o.Master != nil {
		out = append(out, o.Master)
	}
	for i := range o.Overrides {
		out = append(out, &o.Overrides[i])
	}
	return out
}

func (o *Object) problem(code, detail string) {
	o.Problems = append(o.Problems, Problem{Code: code, Detail: detail})
}

// minYear is the earliest year a DTSTART may have.
const minYear = 1000

// Errors of the model.
var (
	// ErrNoEvent: the object has no usable VEVENT (of the UID asked for).
	ErrNoEvent = errors.New("calendar: no event")
	// ErrZone: a zone that is not an IANA name.
	ErrZone = errors.New("calendar: not an IANA time zone")
)

// FromOptions are the parameters of FromICS.
type FromOptions struct {
	// Me are the user's addresses (case-insensitive): they set
	// IAmOrganizer, MyResponse, ResponseRequested and Attendee.Me.
	Me []string
	// UID picks the VEVENTs read; "" = the UID of the first VEVENT.
	UID string
	// AllowNoStart keeps a VEVENT without DTSTART (iTIP REPLY, CANCEL and
	// REFRESH may omit it) with zero Start and End; otherwise it is dropped.
	AllowNoStart bool
}

// Parse reads one iCalendar object (bytes ≤ api.MaxICSBytes) with
// ics.Parse and FromICS, and keeps the bytes as Object.Raw.
func Parse(data []byte, opts FromOptions) (Object, error) {
	parsed, err := ics.Parse(data)
	if err != nil {
		return Object{}, err
	}
	obj, err := FromICS(parsed, opts)
	if err != nil {
		return obj, err
	}
	obj.Raw = slices.Clone(data)
	return obj, nil
}

// UIDs lists the distinct UIDs of the VEVENTs of the first VCALENDAR, in
// order (cleaned; empty UIDs skipped).
func UIDs(obj *ics.Object) []string {
	var out []string
	for _, c := range obj.Calendar().Children("VEVENT") {
		if u := CleanLine(c.Property("UID").Text(), MaxUIDBytes); u != "" && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}

// FromICS reads the VEVENTs of one UID of the first VCALENDAR into an
// Object: the master (no RECURRENCE-ID) and its overrides, with TZIDs
// resolved as D4 says (the object's VTIMEZONE offsets are only hints).
// What it tolerated is in Object.Problems; ErrNoEvent when nothing of
// the UID is usable. Raw is left nil (Parse sets it).
func FromICS(obj *ics.Object, opts FromOptions) (out Object, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = Object{}, fmt.Errorf("%w: %v", ics.ErrMalformed, "calendar: read failed")
		}
	}()
	res, err := read(obj, opts)
	if err != nil {
		return Object{}, err
	}
	return res.obj, nil
}

// readResult is an Object with the components its events came from.
type readResult struct {
	obj       Object
	cal       *ics.Component
	master    *ics.Component
	overrides []*ics.Component // parallel to obj.Overrides
}

func read(obj *ics.Object, opts FromOptions) (readResult, error) {
	var res readResult
	cal := obj.Calendar()
	if cal == nil {
		return res, ErrNoEvent
	}
	res.cal = cal
	if obj.HasProblems() {
		res.obj.problem(ProblemICS, strconv.Itoa(len(obj.Problems)))
	}
	if len(obj.Calendars) > 1 {
		res.obj.problem(ProblemMultipleCalendars, "")
	}
	r := newReader(cal, opts)
	uid := opts.UID
	if uid == "" {
		if u := UIDs(obj); len(u) > 0 {
			uid = u[0]
		}
	}
	mixed := false
	for _, c := range cal.Children("VEVENT") {
		if CleanLine(c.Property("UID").Text(), MaxUIDBytes) != uid {
			mixed = true
			continue
		}
		ev, rid, err := r.event(c)
		if err != nil {
			res.obj.problem(ProblemNoStart, "")
			continue
		}
		ev.UID = uid
		if rid == nil {
			if res.obj.Master != nil {
				res.obj.problem(ProblemDuplicate, "master")
				continue
			}
			e := ev
			res.obj.Master, res.master = &e, c
			continue
		}
		ev.Kind = KindOverride
		r.rids = append(r.rids, *rid)
		res.obj.Overrides = append(res.obj.Overrides, ev)
		res.overrides = append(res.overrides, c)
	}
	if mixed {
		res.obj.problem(ProblemMixedUIDs, "")
	}
	if res.obj.Master == nil && len(res.obj.Overrides) == 0 {
		return res, ErrNoEvent
	}
	// Recurrence ids in the master's form; duplicates dropped.
	seen := map[api.DateOrTime]bool{}
	ovs, comps := res.obj.Overrides[:0], res.overrides[:0]
	for i := range res.obj.Overrides {
		ev := res.obj.Overrides[i]
		ev.RecurrenceID = r.ridKey(r.rids[i], res.obj.Master)
		if ev.RecurrenceID == "" || seen[ev.RecurrenceID] {
			res.obj.problem(ProblemDuplicate, "override")
			continue
		}
		seen[ev.RecurrenceID] = true
		ovs = append(ovs, ev)
		comps = append(comps, res.overrides[i])
	}
	res.obj.Overrides, res.overrides = ovs, comps
	if m := res.obj.Master; m != nil {
		if m.Kind != KindMaster && len(res.obj.Overrides) > 0 {
			m.Kind = KindMaster
		}
		if m.Recurrence != nil {
			m.Recurrence.Exceptions = len(m.EXDates) + len(res.obj.Overrides)
		}
	}
	res.obj.Problems = append(res.obj.Problems, r.problems...)
	return res, nil
}

// reader reads VEVENTs of one VCALENDAR.
type reader struct {
	hints    map[string]tz.Offsets
	zones    map[string]zoneResult
	me       map[string]bool
	noStart  bool
	rids     []when
	problems []Problem
	noted    map[string]bool
}

type zoneResult struct {
	loc   *time.Location // nil: unknown (read as floating)
	name  string
	fixed bool
}

func newReader(cal *ics.Component, opts FromOptions) *reader {
	r := &reader{hints: map[string]tz.Offsets{}, zones: map[string]zoneResult{}, me: map[string]bool{}, noStart: opts.AllowNoStart, noted: map[string]bool{}}
	for _, a := range opts.Me {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
			r.me[a] = true
		}
	}
	for _, vtz := range cal.Children("VTIMEZONE") {
		id := propValue(vtz, "TZID")
		if id == "" {
			continue
		}
		var h tz.Offsets
		ok := false
		for _, std := range vtz.Children("STANDARD") {
			if off, err := firstOffset(std); err == nil {
				h.Standard, ok = off, true
			}
		}
		for _, dl := range vtz.Children("DAYLIGHT") {
			if off, err := firstOffset(dl); err == nil {
				h.Daylight, h.HasDaylight = off, true
				if !ok {
					h.Standard, ok = off, true
				}
			}
		}
		if ok {
			r.hints[id] = h
		}
	}
	return r
}

func firstOffset(c *ics.Component) (time.Duration, error) {
	for _, p := range c.Properties("TZOFFSETTO") {
		if off, err := p.UTCOffset(); err == nil {
			return time.Duration(off) * time.Second, nil
		}
	}
	return 0, ics.ErrSyntax
}

func (r *reader) note(code string) {
	if !r.noted[code] {
		r.noted[code] = true
		r.problems = append(r.problems, Problem{Code: code})
	}
}

// when is a resolved DATE or DATE-TIME.
type when struct {
	t    time.Time // instant (UTC, zoned) or wall clock as if UTC (date, floating)
	kind ics.Kind  // KindDate, KindFloating, KindUTC (zoned resolves to UTC with zone set)
	zone string    // IANA zone of a zoned or UTC time
	loc  *time.Location
}

func (w when) date() bool     { return w.kind == ics.KindDate }
func (w when) floating() bool { return w.kind == ics.KindFloating }
func (w when) timed() bool    { return w.kind == ics.KindUTC }

// wallIn is the wall clock of a timed value in its own zone as if UTC.
func (w when) wall() time.Time {
	if w.timed() && w.loc != nil {
		return wallOf(w.t.In(w.loc))
	}
	return w.t
}

func wallOf(t time.Time) time.Time {
	y, mo, d := t.Date()
	h, mi, s := t.Clock()
	return time.Date(y, mo, d, h, mi, s, 0, time.UTC)
}

func (r *reader) resolve(v ics.Time) when {
	switch v.Kind {
	case ics.KindDate:
		return when{t: time.Date(v.Year, v.Month, v.Day, 0, 0, 0, 0, time.UTC), kind: ics.KindDate}
	case ics.KindFloating:
		return when{t: v.Wall(), kind: ics.KindFloating}
	case ics.KindUTC:
		return when{t: v.Wall(), kind: ics.KindUTC, zone: "UTC", loc: time.UTC}
	}
	z := r.zone(v)
	if z.loc == nil {
		r.note(ProblemZoneUnknown)
		return when{t: v.Wall(), kind: ics.KindFloating}
	}
	if z.fixed {
		r.note(ProblemZoneFixed)
	}
	return when{t: localize(v.Wall(), z.loc).UTC(), kind: ics.KindUTC, zone: z.name, loc: z.loc}
}

func (r *reader) zone(v ics.Time) zoneResult {
	if z, ok := r.zones[v.TZID]; ok {
		return z
	}
	var hints []tz.Offsets
	if h, ok := r.hints[v.TZID]; ok {
		h.At = v.Wall()
		hints = append(hints, h)
	}
	var z zoneResult
	loc, kind, err := tz.Resolve(v.TZID, hints...)
	switch {
	case err != nil:
	case kind == tz.KindFixed:
		_, off := time.Date(2000, 1, 1, 0, 0, 0, 0, loc).Zone()
		z = fixedZone(off)
	default:
		z = zoneResult{loc: loc, name: loc.String()}
	}
	r.zones[v.TZID] = z
	return z
}

// fixedZone maps a fixed offset to the IANA zone Etc/GMT∓N when it is a
// whole hour in the range those zones have, else to UTC.
func fixedZone(off int) zoneResult {
	if off%3600 == 0 && off >= -12*3600 && off <= 14*3600 {
		h := off / 3600
		name := "UTC"
		switch {
		case h > 0:
			name = "Etc/GMT-" + strconv.Itoa(h)
		case h < 0:
			name = "Etc/GMT+" + strconv.Itoa(-h)
		}
		if loc, err := LoadZone(name); err == nil {
			return zoneResult{loc: loc, name: name, fixed: true}
		}
	}
	// Not a whole hour: the instants use the offset, the event is written
	// in UTC.
	return zoneResult{loc: time.FixedZone("", off), name: "UTC", fixed: true}
}

var zoneCache sync.Map // name → *time.Location

// LoadZone returns the location of an IANA zone name as Event.Zone holds
// it ("" and "UTC" are UTC); a name that is not an IANA zone is ErrZone.
func LoadZone(name string) (*time.Location, error) {
	if name == "" || name == "UTC" {
		return time.UTC, nil
	}
	if l, ok := zoneCache.Load(name); ok {
		return l.(*time.Location), nil
	}
	loc, kind, err := tz.Resolve(name)
	if err != nil || kind != tz.KindIANA {
		return nil, ErrZone
	}
	zoneCache.Store(name, loc)
	return loc, nil
}

// Loc returns the event's zone (UTC for all-day, floating, UTC
// times and a zone that does not load).
func (e *Event) Loc() *time.Location {
	if e.AllDay || e.Floating {
		return time.UTC
	}
	loc, err := LoadZone(e.Zone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// localize is the instant of a wall clock (UTC fields) in loc: a time in
// a spring-forward gap moves forward by the gap, a repeated one takes the
// earlier instant (RFC 5545 §3.3.5), as recur does.
func localize(w time.Time, loc *time.Location) time.Time {
	if loc == time.UTC {
		return w
	}
	u := w.Unix()
	var best time.Time
	for _, probe := range []int64{u - 86400, u, u + 86400} {
		_, off := time.Unix(probe, 0).In(loc).Zone()
		c := time.Unix(u-int64(off), 0).In(loc)
		if _, o := c.Zone(); o == off && (best.IsZero() || c.Before(best)) {
			best = c
		}
	}
	if !best.IsZero() {
		return best
	}
	_, before := time.Unix(u-86400, 0).In(loc).Zone()
	return time.Unix(u-int64(before), 0).In(loc)
}

func (r *reader) time(p *ics.Property) (when, bool) {
	if p == nil {
		return when{}, false
	}
	v, err := p.Time()
	if err != nil {
		r.note(ProblemBadValue)
		return when{}, false
	}
	return r.resolve(v), true
}

// event reads one VEVENT; rid is its RECURRENCE-ID (nil for a master).
func (r *reader) event(c *ics.Component) (Event, *when, error) {
	ev := Event{Kind: KindSingle, Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy, RecurOK: true}
	var rid *when
	if p := c.Property("RECURRENCE-ID"); p != nil {
		w, ok := r.time(p)
		if !ok {
			return ev, nil, ErrNoEvent
		}
		rid = &w
	}
	start, ok := r.time(c.Property("DTSTART"))
	if ok && start.wall().Year() < minYear {
		// Year 1 is the zero time.Time; nothing real starts before 1000.
		r.note(ProblemBadValue)
		ok = false
	}
	if !ok && !r.noStart {
		return ev, nil, ErrNoEvent
	}
	if ok {
		r.times(&ev, c, start)
	}
	if n, err := c.Property("SEQUENCE").Integer(); err == nil && n > 0 {
		ev.Sequence = int(n)
	} else if c.Property("SEQUENCE") != nil && err != nil {
		r.note(ProblemBadValue)
	}
	ev.DTStamp = r.stamp(c.Property("DTSTAMP"))
	ev.Created = r.stamp(c.Property("CREATED"))
	ev.Updated = r.stamp(c.Property("LAST-MODIFIED"))

	ev.Title = CleanLine(c.Property("SUMMARY").Text(), MaxTitleBytes)
	ev.Location = CleanLine(c.Property("LOCATION").Text(), MaxLocationBytes)
	switch {
	case c.Property("DESCRIPTION") != nil:
		ev.Notes = CleanBlock(c.Property("DESCRIPTION").Text(), MaxNotesBytes)
	case c.Property("STYLED-DESCRIPTION") != nil:
		sd := c.Property("STYLED-DESCRIPTION")
		if strings.Contains(strings.ToLower(sd.Param("FMTTYPE")), "html") {
			ev.Notes = NotesFromHTML(sd.Text())
		} else {
			ev.Notes = CleanBlock(sd.Text(), MaxNotesBytes)
		}
	}
	ev.Links = mergeLinks(Links(ev.Notes), Links(ev.Location))
	var conf []string
	for _, name := range []string{"CONFERENCE", "X-GOOGLE-CONFERENCE", "X-MICROSOFT-SKYPETEAMSMEETINGURL"} {
		for _, p := range c.Properties(name) {
			conf = append(conf, strings.TrimSpace(p.Text()))
		}
	}
	ev.VideoCall = DetectVideoCall(ev.Location, ev.Notes, conf, "")

	switch strings.ToUpper(strings.TrimSpace(propValue(c, "STATUS"))) {
	case "TENTATIVE":
		ev.Status = api.EventStatusTentative
	case "CANCELLED":
		ev.Status = api.EventStatusCancelled
	}
	ev.ShowAs = showAsOf(c)
	switch strings.ToUpper(strings.TrimSpace(propValue(c, "CLASS"))) {
	case "PRIVATE", "CONFIDENTIAL":
		ev.Private = true
	}
	r.people(&ev, c)
	ev.Alarms = alarms(c, ev.End.Sub(ev.Start))
	for _, p := range c.Properties("ATTACH") {
		if len(ev.Attachments) >= 50 {
			break
		}
		ev.Attachments = append(ev.Attachments, attachName(p))
	}
	return ev, rid, nil
}

func mergeLinks(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		for _, s := range l {
			if !slices.Contains(out, s) && len(out) < MaxLinks {
				out = append(out, s)
			}
		}
	}
	return out
}

func (r *reader) stamp(p *ics.Property) time.Time {
	if p == nil {
		return time.Time{}
	}
	v, err := p.Time()
	if err != nil || v.Kind == ics.KindDate {
		return time.Time{}
	}
	return v.Wall() // UTC by the standard; a local stamp is read as if UTC
}

// times sets the start, end and recurrence of ev from DTSTART (start).
func (r *reader) times(ev *Event, c *ics.Component, start when) {
	endP, durP := c.Property("DTEND"), c.Property("DURATION")
	switch {
	case start.date():
		ev.AllDay = true
		ev.Start = start.t
		end := start.t.AddDate(0, 0, 1)
		if e, ok := r.time(endP); ok {
			w := e.wall()
			if !e.date() {
				r.note(ProblemMixedTimeKinds)
				d := time.Date(w.Year(), w.Month(), w.Day(), 0, 0, 0, 0, time.UTC)
				if d.Before(w) {
					d = d.AddDate(0, 0, 1)
				}
				w = d
			}
			end = w
		} else if d, err := durP.Duration(); durP != nil && err == nil {
			days := d.Weeks*7 + d.Days
			if d.Hours+d.Minutes+d.Seconds > 0 {
				days++
			}
			if d.Negative {
				days = -days
			}
			end = start.t.AddDate(0, 0, days)
		}
		if !end.After(ev.Start) {
			if endP != nil || durP != nil {
				r.note(ProblemEndBeforeStart)
			}
			end = ev.Start.AddDate(0, 0, 1)
		}
		ev.End = end
		ev.StartDate, ev.EndDate = api.Date(ev.Start.Format(keyDate)), api.Date(ev.End.Format(keyDate))
	case start.floating():
		ev.Floating = true
		ev.Start, ev.End = start.t, start.t
		if e, ok := r.time(endP); ok {
			if !e.floating() {
				r.note(ProblemMixedTimeKinds)
			}
			ev.End = e.wall()
		} else if d, err := durP.Duration(); durP != nil && err == nil {
			ev.End = d.AddTo(start.t)
		}
	default:
		ev.Zone = start.zone
		ev.Start, ev.End = start.t, start.t
		if e, ok := r.time(endP); ok {
			switch {
			case e.timed():
				ev.End = e.t
				if e.zone != start.zone && e.zone != "" {
					ev.EndZone = e.zone
				}
			default:
				r.note(ProblemMixedTimeKinds)
				ev.End = localize(e.t, start.loc).UTC()
			}
		} else if d, err := durP.Duration(); durP != nil && err == nil {
			ev.End = d.AddTo(start.t.In(start.loc)).UTC()
		}
	}
	if !ev.AllDay && ev.End.Before(ev.Start) {
		r.note(ProblemEndBeforeStart)
		ev.End = ev.Start
	}
	r.recurrence(ev, c, start)
}

// recurrence reads RRULE, RDATE and EXDATE of a master.
func (r *reader) recurrence(ev *Event, c *ics.Component, start when) {
	if c.Property("RECURRENCE-ID") != nil {
		return
	}
	rr := c.Properties("RRULE")
	rd := c.Properties("RDATE")
	if len(rr) == 0 && len(rd) == 0 {
		return
	}
	ev.Kind = KindMaster
	loc := ev.Loc()
	if len(rr) > 0 {
		rule, err := recur.Parse(rr[0].Value)
		if err == nil {
			ev.RRule = recur.Normalise(rule)
			if ev.AllDay || ev.Floating {
				err = recur.CheckStart(rule, ev.Start, time.UTC)
			} else {
				err = recur.CheckStart(rule, ev.Start, loc)
			}
		}
		if err != nil {
			r.note(ProblemRecurrence)
			ev.RecurOK = false
		}
		if ev.RRule != "" {
			rec := toAPIRecurrence(recur.ToAPI(rule, loc))
			ev.Recurrence = &rec
		} else {
			ev.Recurrence = &api.Recurrence{Frequency: freqOf(rr[0].Value), Interval: 1, Complex: true}
		}
	} else {
		ev.Recurrence = &api.Recurrence{Frequency: api.FrequencyDaily, Interval: 1, Complex: true}
	}
	ev.RDates = r.dates(rd, ev, recur.MaxRDates)
	ev.EXDates = r.dates(c.Properties("EXDATE"), ev, recur.MaxEXDates)
	if len(ev.RDates) > 0 && ev.Recurrence != nil {
		ev.Recurrence.Complex = true
	}
}

func freqOf(rule string) api.RecurrenceFrequency {
	for part := range strings.SplitSeq(strings.ToUpper(rule), ";") {
		if v, ok := strings.CutPrefix(part, "FREQ="); ok {
			switch v {
			case "WEEKLY":
				return api.FrequencyWeekly
			case "MONTHLY":
				return api.FrequencyMonthly
			case "YEARLY":
				return api.FrequencyYearly
			}
		}
	}
	return api.FrequencyDaily
}

func toAPIRecurrence(r recur.Recurrence) api.Recurrence {
	return api.Recurrence{
		Frequency: api.RecurrenceFrequency(r.Frequency), Interval: r.Interval,
		ByDay: r.ByDay, ByMonthDay: r.ByMonthDay, ByMonth: r.ByMonth,
		Until: api.Date(r.Until), Count: r.Count, WeekStart: r.WeekStart, Complex: r.Complex,
	}
}

func fromAPIRecurrence(r api.Recurrence) recur.Recurrence {
	return recur.Recurrence{
		Frequency: string(r.Frequency), Interval: r.Interval,
		ByDay: r.ByDay, ByMonthDay: r.ByMonthDay, ByMonth: r.ByMonth,
		Until: string(r.Until), Count: r.Count, WeekStart: r.WeekStart, Complex: r.Complex,
	}
}

// dates reads RDATE or EXDATE values in the form of ev's start.
func (r *reader) dates(props []*ics.Property, ev *Event, max int) []time.Time {
	var out []time.Time
	add := func(v ics.Time) {
		w := r.resolve(v)
		out = append(out, ev.instantOf(w))
	}
	for _, p := range props {
		if strings.EqualFold(p.Param("VALUE"), "PERIOD") {
			periods, _ := p.Periods()
			for _, pe := range periods {
				add(pe.Start)
			}
			continue
		}
		ts, err := p.Times()
		if err != nil {
			r.note(ProblemBadValue)
		}
		for _, t := range ts {
			add(t)
		}
	}
	if len(out) > max {
		r.note(ProblemTooManyDates)
		out = out[:max]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return slices.CompactFunc(out, func(a, b time.Time) bool { return a.Equal(b) })
}

// instantOf converts a resolved time into the form of e's start: the date
// for all-day, the wall clock for floating, the instant for timed (a date
// gets the start's local time of day).
func (e *Event) instantOf(w when) time.Time {
	switch {
	case e.AllDay:
		t := w.wall()
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	case e.Floating:
		return w.wall()
	case w.date() || w.floating():
		loc := e.Loc()
		s := e.Start.In(loc)
		t := w.t
		if w.date() {
			t = time.Date(t.Year(), t.Month(), t.Day(), s.Hour(), s.Minute(), s.Second(), 0, time.UTC)
		}
		return localize(t, loc).UTC()
	}
	return w.t
}

// ridKey formats an override's RECURRENCE-ID in the master's form.
func (r *reader) ridKey(w when, master *Event) api.DateOrTime {
	if master != nil && master.Start.IsZero() {
		master = nil
	}
	if master != nil {
		return master.KeyOf(master.instantOf(w))
	}
	switch {
	case w.date():
		return api.DateOrTime(w.t.Format(keyDate))
	case w.floating():
		return api.DateOrTime(w.t.Format(keyTime))
	}
	return api.DateOrTime(w.t.UTC().Format(keyTime))
}

// KeyOf formats an instance start of e's series (in the form of
// e.Start) as a recurrence id / occurrence key.
func (e *Event) KeyOf(t time.Time) api.DateOrTime {
	if e.AllDay {
		return api.DateOrTime(t.UTC().Format(keyDate))
	}
	return api.DateOrTime(t.UTC().Format(keyTime))
}

// ParseKey reads a recurrence id: a Date (midnight as if UTC) or an
// RFC 3339 instant (a floating key is its wall clock as if UTC).
func ParseKey(k api.DateOrTime) (time.Time, bool) {
	s := string(k)
	if t, err := time.ParseInLocation(keyDate, s, time.UTC); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

// showAsOf reads the busy state: Outlook's intended status, its busy
// status, else TRANSP.
func showAsOf(c *ics.Component) api.ShowAs {
	for _, name := range []string{"X-MICROSOFT-CDO-INTENDEDSTATUS", "X-MICROSOFT-CDO-BUSYSTATUS"} {
		switch strings.ToUpper(strings.TrimSpace(propValue(c, name))) {
		case "FREE":
			return api.ShowAsFree
		case "TENTATIVE":
			return api.ShowAsTentative
		case "BUSY":
			return api.ShowAsBusy
		case "OOF":
			return api.ShowAsOOF
		case "WORKINGELSEWHERE":
			return api.ShowAsWorkingElsewhere
		}
	}
	if strings.EqualFold(strings.TrimSpace(propValue(c, "TRANSP")), "TRANSPARENT") {
		return api.ShowAsFree
	}
	return api.ShowAsBusy
}

func (r *reader) people(ev *Event, c *ics.Component) {
	if p := c.Property("ORGANIZER"); p != nil {
		if a := p.CalAddress(); a.IsEmail() {
			ev.Organizer = &api.Address{Name: CleanLine(p.Param("CN"), MaxNameBytes), Address: CleanLine(a.Email, MaxNameBytes)}
			ev.IAmOrganizer = r.me[strings.ToLower(a.Email)]
		}
	}
	dropped := false
	seen := map[string]bool{}
	for _, p := range c.Properties("ATTENDEE") {
		ev.AttendeeCount++
		a := p.CalAddress()
		lower := strings.ToLower(a.Email)
		if a.IsEmail() && seen[lower] {
			ev.AttendeeCount-- // the same address twice counts once
			continue
		}
		if !a.IsEmail() || len(ev.Attendees) >= api.MaxEventAttendeesStored {
			dropped = true
			continue
		}
		seen[lower] = true
		at := attendeeOf(p, a)
		at.Me = r.me[lower]
		at.Organizer = ev.Organizer != nil && strings.EqualFold(ev.Organizer.Address, a.Email)
		if at.Me {
			ev.MyResponse = at.Response
			ev.ResponseRequested = strings.EqualFold(p.Param("RSVP"), "TRUE") && at.Response == api.ResponseNeedsAction
		}
		ev.Attendees = append(ev.Attendees, at)
	}
	if dropped {
		r.note(ProblemAttendees)
	}
}

func attendeeOf(p *ics.Property, a ics.CalAddress) api.Attendee {
	at := api.Attendee{
		Name: CleanLine(p.Param("CN"), MaxNameBytes), Address: CleanLine(a.Email, MaxNameBytes),
		Role: api.AttendeeRequired, Response: api.ResponseNeedsAction, Type: api.AttendeeTypePerson,
	}
	if strings.EqualFold(at.Name, at.Address) {
		at.Name = "" // Google writes the address as CN
	}
	switch strings.ToUpper(p.Param("ROLE")) {
	case "OPT-PARTICIPANT":
		at.Role = api.AttendeeOptional
	case "CHAIR":
		at.Role = api.AttendeeChair
	case "NON-PARTICIPANT":
		at.Role = api.AttendeeNonParticipant
	}
	at.Response = PartstatResponse(p.Param("PARTSTAT"))
	switch strings.ToUpper(p.Param("CUTYPE")) {
	case "ROOM":
		at.Type = api.AttendeeTypeRoom
	case "RESOURCE":
		at.Type = api.AttendeeTypeResource
	case "GROUP":
		at.Type = api.AttendeeTypeGroup
	case "UNKNOWN":
		at.Type = api.AttendeeTypeUnknown
	}
	return at
}

// PartstatResponse maps a PARTSTAT value (absent or unknown: needsAction).
func PartstatResponse(v string) api.EventResponse {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "ACCEPTED":
		return api.ResponseAccepted
	case "TENTATIVE":
		return api.ResponseTentative
	case "DECLINED":
		return api.ResponseDeclined
	case "DELEGATED":
		return api.ResponseDelegated
	}
	return api.ResponseNeedsAction
}

// Partstat is the PARTSTAT value of a response.
func Partstat(r api.EventResponse) string {
	switch r {
	case api.ResponseAccepted:
		return "ACCEPTED"
	case api.ResponseTentative:
		return "TENTATIVE"
	case api.ResponseDeclined:
		return "DECLINED"
	case api.ResponseDelegated:
		return "DELEGATED"
	}
	return "NEEDS-ACTION"
}

func roleValue(r api.AttendeeRole) string {
	switch r {
	case api.AttendeeOptional:
		return "OPT-PARTICIPANT"
	case api.AttendeeChair:
		return "CHAIR"
	case api.AttendeeNonParticipant:
		return "NON-PARTICIPANT"
	}
	return "REQ-PARTICIPANT"
}

func cutypeValue(t api.AttendeeType) string {
	switch t {
	case api.AttendeeTypeRoom:
		return "ROOM"
	case api.AttendeeTypeResource:
		return "RESOURCE"
	case api.AttendeeTypeGroup:
		return "GROUP"
	case api.AttendeeTypeUnknown:
		return "UNKNOWN"
	}
	return "INDIVIDUAL"
}

// alarms reads the DISPLAY and AUDIO VALARMs as minutes before start.
func alarms(c *ics.Component, length time.Duration) []int {
	var out []int
	for _, a := range c.Children("VALARM") {
		switch strings.ToUpper(strings.TrimSpace(propValue(a, "ACTION"))) {
		case "DISPLAY", "AUDIO":
		default:
			continue
		}
		tp := a.Property("TRIGGER")
		if tp == nil {
			continue
		}
		var before time.Duration
		if d, err := tp.Duration(); err == nil {
			before = -d.Approx()
			if strings.EqualFold(tp.Param("RELATED"), "END") {
				before -= length
			}
		} else {
			continue // an absolute trigger has no "minutes before" of a series
		}
		m := int(before / time.Minute)
		if before < 0 || m > api.MaxAlertMinutes || slices.Contains(out, m) {
			continue
		}
		out = append(out, m)
	}
	sort.Ints(out)
	if len(out) > api.MaxEventAlerts {
		out = out[:api.MaxEventAlerts]
	}
	return out
}

func attachName(p *ics.Property) string {
	for _, k := range []string{"FILENAME", "X-FILENAME", "X-APPLE-FILENAME", "X-ORACLE-FILENAME"} {
		if v := CleanLine(p.Param(k), MaxNameBytes); v != "" {
			return v
		}
	}
	if strings.EqualFold(p.Param("ENCODING"), "BASE64") {
		return ""
	}
	v := p.Value
	if i := strings.IndexAny(v, "?#"); i >= 0 {
		v = v[:i]
	}
	if i := strings.LastIndexByte(v, '/'); i >= 0 {
		v = v[i+1:]
	}
	return CleanLine(v, MaxNameBytes)
}

// ---------------------------------------------------------------------------
// Writing (ToICS)
// ---------------------------------------------------------------------------

// ProdID is the PRODID of objects the daemon writes.
const ProdID = "-//Malachi Mail//Calendar//EN"

// WriteOptions are the parameters of ToICS.
type WriteOptions struct {
	// Now is written as DTSTAMP and LAST-MODIFIED; zero keeps the
	// original's DTSTAMP (or the event's DTStamp) and LAST-MODIFIED.
	Now time.Time
	// Method is the METHOD of an iTIP object; "" for a stored object (an
	// original METHOD is dropped: CalDAV resources must not have one).
	Method string
	// ProdID: "" keeps the original's, else ProdID.
	ProdID string
	// IgnoreRaw writes only what the model holds, even when Object.Raw is
	// set (iMIP messages).
	IgnoreRaw bool
}

// ToICS writes the object as one VCALENDAR (see ToComponent) and encodes it.
func ToICS(obj Object, opts WriteOptions) ([]byte, error) {
	c, err := ToComponent(obj, opts)
	if err != nil {
		return nil, err
	}
	return ics.EncodeComponent(c)
}

// ToComponent writes the object as one VCALENDAR: the master and the
// overrides as VEVENTs, with VTIMEZONEs generated from Go's zones for
// every IANA TZID used (tz.WriteVTIMEZONE, D4).
//
// When Object.Raw holds the resource the object was read from (a CalDAV
// PUT, D1), everything the model does not hold is preserved from it:
// calendar properties (but METHOD), components other than VEVENTs of the
// UID (VTODO, VEVENTs of other UIDs, X- components), and of each VEVENT
// its unknown properties (X-*, ATTACH, URL, CATEGORIES, …) and children
// (non-DISPLAY alarms). A field the model holds is written from the
// model only when it differs from what the raw resource reads as; an
// unchanged field keeps its original properties verbatim (with their
// parameters, an unknown TZID and that zone's original VTIMEZONE, a rule
// recur refused, non-mailto: attendees). An attendee list that changed is
// merged: original ATTENDEE properties of kept addresses keep their
// parameters (PARTSTAT, ROLE and CN updated), removed ones go, new ones
// are added; attendees the model never listed (over the cap, not mailto:)
// are kept. Overrides missing from the object are dropped. Properties of
// the raw resource that the encoder refuses are left out.
func ToComponent(obj Object, opts WriteOptions) (cal *ics.Component, err error) {
	defer func() {
		if r := recover(); r != nil {
			cal, err = nil, ics.ErrMalformed
		}
	}()
	var orig *readResult
	if len(obj.Raw) > 0 && !opts.IgnoreRaw {
		if parsed, perr := ics.Parse(obj.Raw); perr == nil {
			if res, rerr := read(parsed, FromOptions{UID: obj.UID(), AllowNoStart: true}); rerr == nil {
				orig = &res
			} else if parsed.Calendar() != nil {
				orig = &readResult{cal: parsed.Calendar()}
			}
		}
	}
	w := &writer{opts: opts, master: obj.Master}
	cal = &ics.Component{Name: "VCALENDAR"}
	prodID := opts.ProdID
	if prodID == "" && orig != nil {
		prodID = propValue(orig.cal, "PRODID")
	}
	if prodID == "" {
		prodID = ProdID
	}
	cal.Add(ics.NewProperty("PRODID", prodID))
	cal.Add(ics.NewProperty("VERSION", "2.0"))
	if opts.Method != "" {
		cal.Add(ics.NewProperty("METHOD", strings.ToUpper(opts.Method)))
	}
	if orig != nil {
		for _, p := range orig.cal.Props {
			switch p.Name {
			case "PRODID", "VERSION", "METHOD":
				continue
			}
			addEncodable(cal, p)
		}
	}

	var events, others []*ics.Component
	if obj.Master != nil {
		var oc *ics.Component
		var was *Event
		if orig != nil && orig.obj.Master != nil {
			oc, was = orig.master, orig.obj.Master
		}
		c, err := w.vevent(obj.Master, oc, was)
		if err != nil {
			return nil, err
		}
		events = append(events, c)
	}
	for i := range obj.Overrides {
		ev := &obj.Overrides[i]
		var oc *ics.Component
		var was *Event
		if orig != nil {
			for j := range orig.obj.Overrides {
				if orig.obj.Overrides[j].RecurrenceID == ev.RecurrenceID {
					oc, was = orig.overrides[j], &orig.obj.Overrides[j]
				}
			}
		}
		c, err := w.vevent(ev, oc, was)
		if err != nil {
			return nil, err
		}
		events = append(events, c)
	}
	origZones := map[string]*ics.Component{}
	if orig != nil {
		mine := map[*ics.Component]bool{orig.master: true}
		for _, c := range orig.overrides {
			mine[c] = true
		}
		uid := obj.UID()
		for _, c := range orig.cal.Components {
			switch {
			case c.Name == "VTIMEZONE":
				if id := propValue(c, "TZID"); id != "" && origZones[id] == nil {
					origZones[id] = c
				}
			case mine[c]:
			case c.Name == "VEVENT" && CleanLine(c.Property("UID").Text(), MaxUIDBytes) == uid:
				// an override of the UID the object no longer has
			default:
				if encodableComponent(c) {
					others = append(others, c)
				}
			}
		}
	}
	zones, err := w.timezones(append(append([]*ics.Component(nil), events...), others...), origZones)
	if err != nil {
		return nil, err
	}
	cal.Components = append(append(zones, events...), others...)
	return cal, nil
}

type writer struct {
	opts   WriteOptions
	master *Event
}

// managed are the VEVENT properties the model holds; everything else of
// an original VEVENT is preserved.
var managed = map[string]bool{
	"UID": true, "RECURRENCE-ID": true, "SEQUENCE": true, "DTSTAMP": true,
	"DTSTART": true, "DTEND": true, "DURATION": true,
	"RRULE": true, "RDATE": true, "EXDATE": true, "EXRULE": true,
	"SUMMARY": true, "LOCATION": true, "DESCRIPTION": true, "STYLED-DESCRIPTION": true, "X-ALT-DESC": true,
	"STATUS": true, "TRANSP": true, "X-MICROSOFT-CDO-BUSYSTATUS": true, "X-MICROSOFT-CDO-INTENDEDSTATUS": true,
	"CLASS": true, "ORGANIZER": true, "ATTENDEE": true, "CREATED": true, "LAST-MODIFIED": true,
	"CONFERENCE": true, "X-GOOGLE-CONFERENCE": true, "X-MICROSOFT-SKYPETEAMSMEETINGURL": true,
}

func addEncodable(c *ics.Component, p ics.Property) {
	if _, err := ics.EncodeComponent(&ics.Component{Name: "X", Props: []ics.Property{p}}); err == nil {
		c.Add(p)
	}
}

func encodableComponent(c *ics.Component) bool {
	_, err := ics.EncodeComponent(&ics.Component{Name: "VCALENDAR", Components: []*ics.Component{c}})
	return err == nil
}

// vevent writes one event; orig and was are its original component and
// what that reads as (both nil for a new event).
func (w *writer) vevent(ev *Event, orig *ics.Component, was *Event) (*ics.Component, error) {
	if ev.UID == "" {
		return nil, fmt.Errorf("%w: event without UID", ics.ErrInvalid)
	}
	out := &ics.Component{Name: "VEVENT"}
	copyProps := func(names ...string) {
		for _, n := range names {
			for _, p := range orig.Properties(n) {
				addEncodable(out, *p)
			}
		}
	}
	has := orig != nil && was != nil

	out.Add(ics.NewText("UID", ev.UID))
	if ev.Kind == KindOverride {
		if has && (was.RecurrenceID == ev.RecurrenceID) && orig.Property("RECURRENCE-ID") != nil {
			copyProps("RECURRENCE-ID")
		} else {
			t, ok := ParseKey(ev.RecurrenceID)
			if !ok {
				return nil, fmt.Errorf("%w: bad recurrence id", ics.ErrInvalid)
			}
			form := w.master
			if form == nil {
				form = ev
			}
			out.Add(timeProp("RECURRENCE-ID", t, form.AllDay, form.Floating, form.Zone))
		}
	}
	if ev.Sequence > 0 || (orig != nil && orig.Property("SEQUENCE") != nil) {
		out.Add(ics.NewProperty("SEQUENCE", strconv.Itoa(ev.Sequence)))
	}
	switch {
	case !w.opts.Now.IsZero():
		out.Add(ics.NewTime("DTSTAMP", ics.UTCOf(w.opts.Now)))
	case orig != nil && orig.Property("DTSTAMP") != nil:
		copyProps("DTSTAMP")
	case !ev.DTStamp.IsZero():
		out.Add(ics.NewTime("DTSTAMP", ics.UTCOf(ev.DTStamp)))
	}
	if !ev.Start.IsZero() {
		if has && (sameTime(was, ev)) {
			copyProps("DTSTART", "DTEND", "DURATION")
		} else {
			out.Add(timeProp("DTSTART", ev.Start, ev.AllDay, ev.Floating, ev.Zone))
			endZone := ev.Zone
			if ev.EndZone != "" {
				endZone = ev.EndZone
			}
			out.Add(timeProp("DTEND", ev.End, ev.AllDay, ev.Floating, endZone))
		}
	}
	if ev.Kind != KindOverride {
		if has && (sameRecurrence(was, ev)) {
			copyProps("RRULE", "EXRULE", "RDATE", "EXDATE")
		} else {
			if ev.RRule != "" {
				out.Add(ics.NewProperty("RRULE", ev.RRule))
			}
			if p, ok := dateList("RDATE", ev.RDates, ev); ok {
				out.Add(p)
			}
			if p, ok := dateList("EXDATE", ev.EXDates, ev); ok {
				out.Add(p)
			}
		}
	}
	textProp := func(name, value, wasValue string, extra ...string) {
		if has && (value == wasValue) {
			copyProps(append([]string{name}, extra...)...)
		} else if value != "" {
			out.Add(ics.NewText(name, value))
		}
	}
	textProp("SUMMARY", ev.Title, fieldOf(was, func(e *Event) string { return e.Title }))
	textProp("LOCATION", ev.Location, fieldOf(was, func(e *Event) string { return e.Location }))
	textProp("DESCRIPTION", ev.Notes, fieldOf(was, func(e *Event) string { return e.Notes }), "STYLED-DESCRIPTION", "X-ALT-DESC")
	if has && (was.Status == ev.Status) {
		copyProps("STATUS")
	} else if ev.Status != "" {
		out.Add(ics.NewProperty("STATUS", strings.ToUpper(string(ev.Status))))
	}
	if has && (was.ShowAs == ev.ShowAs) {
		copyProps("TRANSP", "X-MICROSOFT-CDO-BUSYSTATUS", "X-MICROSOFT-CDO-INTENDEDSTATUS")
	} else {
		transp := "OPAQUE"
		if ev.ShowAs == api.ShowAsFree {
			transp = "TRANSPARENT"
		}
		out.Add(ics.NewProperty("TRANSP", transp))
		switch ev.ShowAs {
		case api.ShowAsTentative, api.ShowAsOOF, api.ShowAsWorkingElsewhere:
			out.Add(ics.NewProperty("X-MICROSOFT-CDO-BUSYSTATUS", strings.ToUpper(string(ev.ShowAs))))
		}
	}
	if has && (was.Private == ev.Private) {
		copyProps("CLASS")
	} else if ev.Private {
		out.Add(ics.NewProperty("CLASS", "PRIVATE"))
	}
	if has && (sameAddress(was.Organizer, ev.Organizer)) {
		copyProps("ORGANIZER")
	} else if ev.Organizer != nil && ev.Organizer.Address != "" {
		p := ics.NewProperty("ORGANIZER", "mailto:"+ev.Organizer.Address)
		if ev.Organizer.Name != "" {
			p.SetParam("CN", paramSafe(ev.Organizer.Name))
		}
		out.Add(p)
	}
	w.attendees(out, ev, orig, was)
	if orig != nil && orig.Property("CREATED") != nil {
		copyProps("CREATED")
	} else if !ev.Created.IsZero() {
		out.Add(ics.NewTime("CREATED", ics.UTCOf(ev.Created)))
	}
	switch {
	case !w.opts.Now.IsZero():
		out.Add(ics.NewTime("LAST-MODIFIED", ics.UTCOf(w.opts.Now)))
	case orig != nil && orig.Property("LAST-MODIFIED") != nil:
		copyProps("LAST-MODIFIED")
	case !ev.Updated.IsZero():
		out.Add(ics.NewTime("LAST-MODIFIED", ics.UTCOf(ev.Updated)))
	}
	if has && (sameVideo(was.VideoCall, ev.VideoCall)) {
		copyProps("CONFERENCE", "X-GOOGLE-CONFERENCE", "X-MICROSOFT-SKYPETEAMSMEETINGURL")
	} else if vc := ev.VideoCall; vc != nil && !strings.Contains(ev.Notes, vc.URL) && !strings.Contains(ev.Location, vc.URL) {
		p := ics.NewProperty("CONFERENCE", vc.URL)
		p.SetParam("VALUE", "URI")
		p.SetParam("FEATURE", "VIDEO")
		out.Add(p)
	}
	if orig != nil {
		for _, p := range orig.Props {
			if !managed[p.Name] {
				addEncodable(out, p)
			}
		}
	}
	// Alarms: the model's DISPLAY/AUDIO alarms, or the original ones when
	// unchanged; other alarms and children are preserved.
	sameAlarms := has && (slices.Equal(was.Alarms, ev.Alarms))
	if orig != nil {
		for _, ch := range orig.Components {
			if ch.Name == "VALARM" && isShownAlarm(ch) && !sameAlarms {
				continue
			}
			if encodableComponent(ch) {
				out.Components = append(out.Components, ch)
			}
		}
	}
	if !sameAlarms {
		for _, m := range ev.Alarms {
			out.Components = append(out.Components, alarmComponent(m))
		}
	}
	return out, nil
}

func fieldOf(e *Event, f func(*Event) string) string {
	if e == nil {
		return ""
	}
	return f(e)
}

func isShownAlarm(c *ics.Component) bool {
	switch strings.ToUpper(strings.TrimSpace(propValue(c, "ACTION"))) {
	case "DISPLAY", "AUDIO":
		return true
	}
	return false
}

func alarmComponent(minutes int) *ics.Component {
	trigger := "PT0S"
	if minutes > 0 {
		trigger = "-PT" + strconv.Itoa(minutes) + "M"
	}
	return &ics.Component{Name: "VALARM", Props: []ics.Property{
		ics.NewProperty("ACTION", "DISPLAY"),
		ics.NewText("DESCRIPTION", "Reminder"),
		ics.NewProperty("TRIGGER", trigger),
	}}
}

// attendees writes ORGANIZER's attendee list, merged with the original
// when it changed.
func (w *writer) attendees(out *ics.Component, ev *Event, orig *ics.Component, was *Event) {
	if orig != nil && was != nil && sameAttendees(was, ev) {
		for _, p := range orig.Properties("ATTENDEE") {
			addEncodable(out, *p)
		}
		return
	}
	want := map[string]*api.Attendee{}
	for i := range ev.Attendees {
		want[strings.ToLower(ev.Attendees[i].Address)] = &ev.Attendees[i]
	}
	listed := map[string]bool{}
	if was != nil {
		for _, a := range was.Attendees {
			listed[strings.ToLower(a.Address)] = true
		}
	}
	used := map[string]bool{}
	if orig != nil {
		for _, p := range orig.Properties("ATTENDEE") {
			addr := strings.ToLower(p.CalAddress().Email)
			a, ok := want[addr]
			switch {
			case addr == "" || (!ok && !listed[addr]):
				addEncodable(out, *p) // never in the model: kept
			case ok && !used[addr]:
				used[addr] = true
				q := *p
				q.Params = slices.Clone(p.Params)
				q.SetParam("PARTSTAT", Partstat(a.Response))
				q.SetParam("ROLE", roleValue(a.Role))
				if a.Name != "" {
					q.SetParam("CN", paramSafe(a.Name))
				}
				addEncodable(out, q)
			}
		}
	}
	for i := range ev.Attendees {
		a := &ev.Attendees[i]
		if used[strings.ToLower(a.Address)] || a.Address == "" {
			continue
		}
		used[strings.ToLower(a.Address)] = true
		addEncodable(out, AttendeeProperty(*a))
	}
}

// AttendeeProperty is the ATTENDEE property of an attendee (CN, ROLE,
// PARTSTAT and a CUTYPE other than INDIVIDUAL).
func AttendeeProperty(a api.Attendee) ics.Property {
	p := ics.NewProperty("ATTENDEE", "mailto:"+a.Address)
	if a.Name != "" {
		p.SetParam("CN", paramSafe(a.Name))
	}
	p.SetParam("ROLE", roleValue(a.Role))
	p.SetParam("PARTSTAT", Partstat(a.Response))
	if a.Type != "" && a.Type != api.AttendeeTypePerson {
		p.SetParam("CUTYPE", cutypeValue(a.Type))
	}
	return p
}

// paramSafe makes cleaned text a valid parameter value (no quotes).
func paramSafe(s string) string { return strings.ReplaceAll(s, `"`, "'") }

// timeProp writes a DATE (allDay), a floating DATE-TIME, a UTC DATE-TIME
// (zone "" or "UTC", or a zone that does not load) or a DATE-TIME with an
// IANA TZID.
func timeProp(name string, t time.Time, allDay, floating bool, zone string) ics.Property {
	return ics.NewTime(name, icsTime(t, allDay, floating, zone))
}

func icsTime(t time.Time, allDay, floating bool, zone string) ics.Time {
	switch {
	case allDay:
		return ics.DateOf(t.UTC())
	case floating:
		u := t.UTC()
		return ics.Time{Kind: ics.KindFloating, Year: u.Year(), Month: u.Month(), Day: u.Day(), Hour: u.Hour(), Minute: u.Minute(), Second: u.Second()}
	}
	loc, err := LoadZone(zone)
	if err != nil || loc == time.UTC {
		return ics.UTCOf(t)
	}
	l := t.In(loc)
	return ics.Time{Kind: ics.KindZoned, Year: l.Year(), Month: l.Month(), Day: l.Day(), Hour: l.Hour(), Minute: l.Minute(), Second: l.Second(), TZID: loc.String()}
}

// dateList writes RDATE or EXDATE values in the form of ev's start.
func dateList(name string, ts []time.Time, ev *Event) (ics.Property, bool) {
	if len(ts) == 0 {
		return ics.Property{}, false
	}
	vals := make([]string, len(ts))
	var p ics.Property
	for i, t := range ts {
		it := icsTime(t, ev.AllDay, ev.Floating, ev.Zone)
		if i == 0 {
			p = ics.NewTime(name, it)
		}
		vals[i] = it.Value()
	}
	p.Value = strings.Join(vals, ",")
	return p, true
}

func sameTime(a, b *Event) bool {
	return a.AllDay == b.AllDay && a.Floating == b.Floating && a.Start.Equal(b.Start) && a.End.Equal(b.End) &&
		a.Zone == b.Zone && a.EndZone == b.EndZone
}

func sameRecurrence(a, b *Event) bool {
	eq := func(x, y []time.Time) bool {
		return slices.EqualFunc(x, y, func(s, t time.Time) bool { return s.Equal(t) })
	}
	return a.RRule == b.RRule && a.RecurOK == b.RecurOK && eq(a.RDates, b.RDates) && eq(a.EXDates, b.EXDates) &&
		sameTime(a, b)
}

func sameAddress(a, b *api.Address) bool {
	if a == nil || b == nil {
		return a == b
	}
	return strings.EqualFold(a.Address, b.Address) && a.Name == b.Name
}

func sameVideo(a, b *api.VideoCall) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameAttendees(a, b *Event) bool {
	if a.AttendeeCount != b.AttendeeCount {
		return false
	}
	return slices.EqualFunc(a.Attendees, b.Attendees, func(x, y api.Attendee) bool {
		return strings.EqualFold(x.Address, y.Address) && x.Name == y.Name && x.Role == y.Role &&
			x.Response == y.Response && x.Type == y.Type
	})
}

// timezones returns the VTIMEZONEs of the TZIDs the components use: a
// generated one for an IANA zone, the original one otherwise (a TZID of a
// preserved property that is not IANA); a TZID with neither is left as
// it is.
func (w *writer) timezones(comps []*ics.Component, orig map[string]*ics.Component) ([]*ics.Component, error) {
	type span struct {
		from, to time.Time
		open     bool
	}
	spans := map[string]*span{}
	var order []string
	var visit func(c *ics.Component)
	visit = func(c *ics.Component) {
		var startZone string
		var rule string
		for i := range c.Props {
			p := &c.Props[i]
			id := p.Param("TZID")
			if p.Name == "RRULE" {
				rule = p.Value
			}
			if id == "" {
				continue
			}
			s := spans[id]
			if s == nil {
				s = &span{}
				spans[id] = s
				order = append(order, id)
			}
			if p.Name == "DTSTART" {
				startZone = id
			}
			ts, _ := p.Times()
			for _, t := range ts {
				wt := t.Wall()
				if s.from.IsZero() || wt.Before(s.from) {
					s.from = wt
				}
				if wt.After(s.to) {
					s.to = wt
				}
			}
		}
		if rule != "" && startZone != "" {
			s := spans[startZone]
			if r, err := recur.Parse(rule); err == nil && r.UntilKind != recur.UntilNone {
				if r.Until.After(s.to) {
					s.to = r.Until
				}
			} else {
				s.open = true
			}
		}
		for _, ch := range c.Components {
			visit(ch)
		}
	}
	for _, c := range comps {
		visit(c)
	}
	var out []*ics.Component
	for _, id := range order {
		loc, err := LoadZone(id)
		if err != nil || loc == time.UTC {
			if c := orig[id]; c != nil && encodableComponent(c) {
				out = append(out, c)
			}
			continue
		}
		s := spans[id]
		to := s.to
		if s.open {
			ref := w.opts.Now
			if ref.IsZero() || ref.Before(s.from) {
				ref = s.from
			}
			if t := ref.AddDate(2, 0, 0); t.After(to) {
				to = t
			}
		}
		c, err := VTimezone(loc, s.from, to)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// VTimezone generates the VTIMEZONE of loc covering from..to as an
// ics component (tz.WriteVTIMEZONE); a span over
// tz.MaxVTIMEZONEYears gets the first year and the last years.
func VTimezone(loc *time.Location, from, to time.Time) (*ics.Component, error) {
	var lines []string
	if to.Year()-from.Year() < tz.MaxVTIMEZONEYears {
		l, err := tz.WriteVTIMEZONE(loc, from, to)
		if err != nil {
			return nil, err
		}
		lines = l
	} else {
		first, err := tz.WriteVTIMEZONE(loc, from, from)
		if err != nil {
			return nil, err
		}
		last, err := tz.WriteVTIMEZONE(loc, to.AddDate(-(tz.MaxVTIMEZONEYears-2), 0, 0), to)
		if err != nil {
			return nil, err
		}
		lines = append(first[:len(first)-1], last[2:]...)
	}
	parsed, err := ics.Parse([]byte("BEGIN:VCALENDAR\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VCALENDAR\r\n"))
	if err != nil {
		return nil, err
	}
	c := parsed.Calendar().Children("VTIMEZONE")
	if len(c) != 1 {
		return nil, fmt.Errorf("%w: VTIMEZONE", ics.ErrMalformed)
	}
	return c[0], nil
}

// ---------------------------------------------------------------------------
// Input and edits
// ---------------------------------------------------------------------------

// InputOptions are the parameters of FromInput.
type InputOptions struct {
	UID string // the event's UID (a new one for event.create)
	// Organizer is the account the event belongs to; it becomes ORGANIZER
	// when the event has attendees.
	Organizer *api.Address
	// DefaultAlertMinutes is calendar.preferences' default alert (-1 none),
	// used when EventInput.Alerts is nil.
	DefaultAlertMinutes int
}

func invalid(format string, args ...any) error {
	return api.NewError(api.CodeInvalidArgument, format, args...)
}

// ParseLocal reads a LocalDateTime ("2026-10-14T10:00:00", seconds
// optional) as the instant of that wall clock in loc (a time in a DST gap
// moves forward by the gap).
func ParseLocal(s api.LocalDateTime, loc *time.Location) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if w, err := time.ParseInLocation(layout, string(s), time.UTC); err == nil {
			if w.Year() < recur.MinYear || w.Year() > recur.MaxUntilYear {
				return time.Time{}, false
			}
			return localize(w, loc).UTC(), true
		}
	}
	return time.Time{}, false
}

// ParseDate reads a Date as its midnight as if UTC.
func ParseDate(d api.Date) (time.Time, bool) {
	t, err := time.ParseInLocation(keyDate, string(d), time.UTC)
	if err != nil || t.Year() < recur.MinYear || t.Year() > recur.MaxUntilYear {
		return time.Time{}, false
	}
	return t, true
}

// FromInput validates an EventInput (event.create, event.update) and
// returns the event it describes: every limit of §4.8 is invalidArgument
// (an *api.Error); strings are cleaned first and their caps apply to the
// cleaned text. Attachments are not read here (the core loads them into
// the write's options); a "create" video call sets
// OnlineMeetingProvider, a "link" one VideoCall.
func FromInput(in api.EventInput, opts InputOptions) (Event, error) {
	ev := Event{UID: CleanLine(opts.UID, MaxUIDBytes), Kind: KindSingle, Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy, RecurOK: true}
	if ev.UID == "" {
		return Event{}, invalid("event without uid")
	}
	if ev.Title = CleanLine(in.Title, 0); len(ev.Title) > MaxTitleBytes {
		return Event{}, invalid("title over %d bytes", MaxTitleBytes)
	}
	if ev.Location = CleanLine(in.Location, 0); len(ev.Location) > MaxLocationBytes {
		return Event{}, invalid("location over %d bytes", MaxLocationBytes)
	}
	if len(in.Notes) > 4*MaxNotesBytes {
		return Event{}, invalid("notes over %d bytes", MaxNotesBytes)
	}
	if ev.Notes = CleanBlock(in.Notes, 0); len(ev.Notes) > MaxNotesBytes {
		return Event{}, invalid("notes over %d bytes", MaxNotesBytes)
	}
	loc := time.UTC
	if in.AllDay {
		s, ok1 := ParseDate(in.StartDate)
		e, ok2 := ParseDate(in.EndDate)
		if !ok1 || !ok2 || !e.After(s) {
			return Event{}, invalid("bad all-day dates")
		}
		ev.AllDay, ev.Start, ev.End = true, s, e
		ev.StartDate, ev.EndDate = in.StartDate, in.EndDate
	} else {
		if in.TimeZone == "" {
			return Event{}, invalid("timeZone required")
		}
		l, err := LoadZone(string(in.TimeZone))
		if err != nil {
			return Event{}, invalid("timeZone is not an IANA zone")
		}
		loc = l
		s, ok1 := ParseLocal(in.Start, loc)
		e, ok2 := ParseLocal(in.End, loc)
		if !ok1 || !ok2 || e.Before(s) {
			return Event{}, invalid("bad start or end")
		}
		ev.Start, ev.End, ev.Zone = s, e, loc.String()
		if ev.Zone == "UTC" || ev.Zone == "Etc/UTC" {
			ev.Zone = "UTC"
		}
	}
	if in.Recurrence != nil && !in.KeepRecurrence {
		rec := *in.Recurrence
		if rec.Complex || rec.Exceptions != 0 {
			return Event{}, invalid("recurrence: complex and exceptions are read only")
		}
		if rec.Interval > api.MaxRecurrenceInterval || rec.Count > api.MaxRecurrenceCount || rec.Interval < 0 || rec.Count < 0 {
			return Event{}, invalid("recurrence interval or count out of range")
		}
		if rec.Until != "" && rec.Count != 0 {
			return Event{}, invalid("recurrence: until and count together")
		}
		dt := ev.Start
		if !ev.AllDay {
			dt = ev.Start.In(loc)
		}
		rule, err := recur.FromAPI(fromAPIRecurrence(rec), dt, loc, ev.AllDay)
		if err != nil {
			return Event{}, invalid("recurrence not supported")
		}
		ev.Kind, ev.RRule = KindMaster, recur.Normalise(rule)
		r := toAPIRecurrence(recur.ToAPI(rule, loc))
		ev.Recurrence = &r
	}
	switch {
	case in.Alerts == nil:
		if opts.DefaultAlertMinutes >= 0 {
			ev.Alarms = []int{min(opts.DefaultAlertMinutes, api.MaxAlertMinutes)}
		}
	default:
		if len(in.Alerts) > api.MaxEventAlerts {
			return Event{}, invalid("more than %d alerts", api.MaxEventAlerts)
		}
		for _, m := range in.Alerts {
			if m < 0 || m > api.MaxAlertMinutes {
				return Event{}, invalid("alert out of range")
			}
			if !slices.Contains(ev.Alarms, m) {
				ev.Alarms = append(ev.Alarms, m)
			}
		}
		sort.Ints(ev.Alarms)
	}
	switch in.ShowAs {
	case "":
	case api.ShowAsFree, api.ShowAsTentative, api.ShowAsBusy, api.ShowAsOOF, api.ShowAsWorkingElsewhere:
		ev.ShowAs = in.ShowAs
	default:
		return Event{}, invalid("bad showAs")
	}
	ev.Private = in.Private
	if len(in.Attendees) > api.MaxEventAttendeesIn {
		return Event{}, invalid("more than %d attendees", api.MaxEventAttendeesIn)
	}
	for _, a := range in.Attendees {
		addr := ics.ParseCalAddress("mailto:" + strings.TrimSpace(a.Address)).Email
		if addr == "" || len(addr) > MaxNameBytes {
			return Event{}, invalid("bad attendee address")
		}
		role := api.AttendeeRequired
		switch a.Role {
		case "", api.AttendeeRequired:
		case api.AttendeeOptional:
			role = api.AttendeeOptional
		default:
			return Event{}, invalid("bad attendee role")
		}
		if opts.Organizer != nil && strings.EqualFold(addr, opts.Organizer.Address) {
			continue
		}
		dup := false
		for _, x := range ev.Attendees {
			dup = dup || strings.EqualFold(x.Address, addr)
		}
		if dup {
			continue
		}
		ev.Attendees = append(ev.Attendees, api.Attendee{
			Name: CleanLine(a.Name, MaxNameBytes), Address: addr, Role: role,
			Response: api.ResponseNeedsAction, Type: api.AttendeeTypePerson,
		})
	}
	ev.AttendeeCount = len(ev.Attendees)
	if len(ev.Attendees) > 0 && opts.Organizer != nil {
		org := *opts.Organizer
		org.Name = CleanLine(org.Name, MaxNameBytes)
		ev.Organizer, ev.IAmOrganizer = &org, true
	}
	ev.Links = mergeLinks(Links(ev.Notes), Links(ev.Location))
	if vc := in.VideoCall; vc != nil {
		switch vc.Mode {
		case "", api.VideoCallNone:
		case api.VideoCallLink:
			u, ok := SafeLink(vc.URL)
			if !ok {
				return Event{}, invalid("video call link must be https")
			}
			ev.VideoCall = videoCall(u, true)
		case api.VideoCallCreate:
			if vc.Provider == "" || len(vc.Provider) > 64 {
				return Event{}, invalid("video call provider required")
			}
			ev.OnlineMeetingProvider = CleanLine(vc.Provider, 64)
		default:
			return Event{}, invalid("bad video call mode")
		}
	}
	if ev.VideoCall == nil && ev.OnlineMeetingProvider == "" {
		ev.VideoCall = DetectVideoCall(ev.Location, ev.Notes, nil, "")
	}
	return ev, nil
}

// Significant reports whether after changes before in a way that needs
// the attendees' new answer (RFC 5546 §2.1.4: SEQUENCE is incremented):
// time, recurrence, location, status or the attendee list.
func Significant(before, after *Event) bool {
	if before == nil || after == nil {
		return true
	}
	return !sameRecurrence(before, after) || before.Location != after.Location || before.Status != after.Status ||
		!sameAttendees(before, after) || !sameAddress(before.Organizer, after.Organizer)
}

// ErrScope: an edit scope the event cannot take (this or thisAndFuture of
// a single event is all; of a complex rule only all is allowed, D19).
var ErrScope = errors.New("calendar: scope not possible for this event")

// ApplyChange applies an edit to an object read from the server (D19)
// and returns the object to write back, the new series that
// EditScopeThisAndFuture splits off (nil otherwise; it has the UID newUID
// and SEQUENCE 0) and the number of overrides dropped.
//
//   - all: the master takes ch.Event (keeping its rule when
//     ch.KeepRecurrence); overrides whose recurrence id is no longer an
//     instance are dropped and counted. A single event is replaced.
//   - this: the instance's override is added or replaced.
//   - thisAndFuture: the old series ends one second (all-day: one day)
//     before the instance (UNTIL, COUNT removed), its RDATEs, EXDATEs and
//     overrides from the instance on go (the overrides counted), and
//     ch.Event starts the new series. At the first instance it is all.
//
// SEQUENCE is the caller's (Significant). A complex rule takes only all
// (ErrScope).
func ApplyChange(obj Object, ch Change, newUID string) (updated Object, split *Object, dropped int, err error) {
	updated = cloneObject(obj)
	ev := ch.Event
	ev.UID = obj.UID()
	m := updated.Master
	scope := ch.Scope
	if m == nil || m.Kind != KindMaster {
		if scope == api.EditScopeThis && m == nil {
			// an override without a master: replace that override
			if o := updated.Override(ch.RecurrenceID); o != nil {
				ev.Kind, ev.RecurrenceID = KindOverride, o.RecurrenceID
				*o = ev
				return updated, nil, 0, nil
			}
			return Object{}, nil, 0, ErrScope
		}
		scope = api.EditScopeAll
	}
	if scope != api.EditScopeAll && m != nil && (m.Recurrence != nil && m.Recurrence.Complex || !m.RecurOK) {
		return Object{}, nil, 0, ErrScope
	}
	if scope == api.EditScopeThisAndFuture {
		if t, ok := ParseKey(ch.RecurrenceID); !ok || !t.After(m.Start) {
			scope = api.EditScopeAll
		}
	}
	switch scope {
	case api.EditScopeAll:
		if m == nil {
			return Object{}, nil, 0, ErrScope
		}
		if ch.KeepRecurrence {
			ev.RRule, ev.RDates, ev.EXDates, ev.Recurrence, ev.RecurOK = m.RRule, m.RDates, m.EXDates, m.Recurrence, m.RecurOK
			if ev.Kind != KindMaster && m.Kind == KindMaster {
				ev.Kind = KindMaster
			}
		}
		if ev.RRule == "" && len(ev.RDates) == 0 {
			ev.Kind = KindSingle
			dropped = len(updated.Overrides)
			updated.Overrides = nil
		} else {
			ev.Kind = KindMaster
			kept := updated.Overrides[:0]
			for _, o := range updated.Overrides {
				if HasInstance(&ev, o.RecurrenceID) {
					kept = append(kept, o)
				} else {
					dropped++
				}
			}
			updated.Overrides = kept
		}
		if ev.Kind == KindMaster && len(updated.Overrides) == 0 && ev.RRule == "" && len(ev.RDates) == 0 {
			ev.Kind = KindSingle
		}
		updated.Master = &ev
		return updated, nil, dropped, nil
	case api.EditScopeThis:
		if !HasInstance(m, ch.RecurrenceID) && updated.Override(ch.RecurrenceID) == nil {
			return Object{}, nil, 0, ErrScope
		}
		ev.Kind, ev.RecurrenceID = KindOverride, ch.RecurrenceID
		ev.RRule, ev.RDates, ev.EXDates, ev.Recurrence = "", nil, nil, nil
		if o := updated.Override(ch.RecurrenceID); o != nil {
			*o = ev
		} else {
			updated.Overrides = append(updated.Overrides, ev)
		}
		return updated, nil, 0, nil
	case api.EditScopeThisAndFuture:
		t, _ := ParseKey(ch.RecurrenceID)
		dropped, err = cutSeries(&updated, t)
		if err != nil {
			return Object{}, nil, 0, err
		}
		ns := ev
		ns.UID = CleanLine(newUID, MaxUIDBytes)
		if ns.UID == "" {
			return Object{}, nil, 0, invalid("new series without uid")
		}
		ns.Sequence, ns.RecurrenceID = 0, ""
		if ns.RRule == "" && len(ns.RDates) == 0 {
			ns.Kind = KindSingle
		} else {
			ns.Kind = KindMaster
		}
		return updated, &Object{Master: &ns}, dropped, nil
	}
	return Object{}, nil, 0, ErrScope
}

// ApplyDelete removes instances of an object: this adds an EXDATE (and
// drops the instance's override), thisAndFuture ends the series before
// the instance; all — and thisAndFuture at the first instance — returns
// nil (delete the resource).
func ApplyDelete(obj Object, scope Scope, rid api.DateOrTime) (*Object, error) {
	updated := cloneObject(obj)
	m := updated.Master
	if scope == api.EditScopeAll || m == nil && scope != api.EditScopeThis {
		return nil, nil
	}
	if m == nil {
		ovs := updated.Overrides[:0]
		for _, o := range updated.Overrides {
			if o.RecurrenceID != rid {
				ovs = append(ovs, o)
			}
		}
		updated.Overrides = ovs
		if len(ovs) == 0 {
			return nil, nil
		}
		return &updated, nil
	}
	if m.Kind != KindMaster {
		return nil, nil
	}
	t, ok := ParseKey(rid)
	if !ok {
		return nil, invalid("bad recurrence id")
	}
	switch scope {
	case api.EditScopeThis:
		ovs := updated.Overrides[:0]
		for _, o := range updated.Overrides {
			if o.RecurrenceID != rid {
				ovs = append(ovs, o)
			}
		}
		updated.Overrides = ovs
		if !slices.ContainsFunc(m.EXDates, t.Equal) {
			m.EXDates = append(m.EXDates, t)
			sort.Slice(m.EXDates, func(i, j int) bool { return m.EXDates[i].Before(m.EXDates[j]) })
		}
		return &updated, nil
	case api.EditScopeThisAndFuture:
		if !t.After(m.Start) {
			return nil, nil
		}
		if _, err := cutSeries(&updated, t); err != nil {
			return nil, err
		}
		return &updated, nil
	}
	return nil, ErrScope
}

// cutSeries ends obj's series before t (the instance's original start).
func cutSeries(obj *Object, t time.Time) (dropped int, err error) {
	m := obj.Master
	if m.RRule != "" {
		rule, err := recur.Parse(m.RRule)
		if err != nil {
			return 0, ErrScope
		}
		rule.Count = 0
		if m.AllDay {
			rule.Until, rule.UntilKind = t.AddDate(0, 0, -1), recur.UntilDate
		} else if m.Floating {
			rule.Until, rule.UntilKind = t.Add(-time.Second), recur.UntilFloating
		} else {
			rule.Until, rule.UntilKind = t.Add(-time.Second).UTC(), recur.UntilUTC
		}
		m.RRule = recur.Normalise(rule)
		if m.Recurrence != nil {
			r := toAPIRecurrence(recur.ToAPI(rule, m.Loc()))
			r.Complex = r.Complex || m.Recurrence.Complex
			m.Recurrence = &r
		}
	}
	keep := func(ts []time.Time) []time.Time {
		var out []time.Time
		for _, x := range ts {
			if x.Before(t) {
				out = append(out, x)
			}
		}
		return out
	}
	m.RDates, m.EXDates = keep(m.RDates), keep(m.EXDates)
	ovs := obj.Overrides[:0]
	for _, o := range obj.Overrides {
		if ot, ok := ParseKey(o.RecurrenceID); ok && ot.Before(t) {
			ovs = append(ovs, o)
		} else {
			dropped++
		}
	}
	obj.Overrides = ovs
	return dropped, nil
}

// cloneObject copies an object deeply enough that edits of the copy's
// events and slices leave the original alone.
func cloneObject(o Object) Object {
	c := o
	if o.Master != nil {
		m := cloneEvent(*o.Master)
		c.Master = &m
	}
	c.Overrides = make([]Event, len(o.Overrides))
	for i, e := range o.Overrides {
		c.Overrides[i] = cloneEvent(e)
	}
	c.Occurrences = slices.Clone(o.Occurrences)
	c.Problems = slices.Clone(o.Problems)
	return c
}

func cloneEvent(e Event) Event {
	e.Links = slices.Clone(e.Links)
	e.RDates = slices.Clone(e.RDates)
	e.EXDates = slices.Clone(e.EXDates)
	e.Attendees = slices.Clone(e.Attendees)
	e.Alarms = slices.Clone(e.Alarms)
	e.Attachments = slices.Clone(e.Attachments)
	if e.Recurrence != nil {
		r := *e.Recurrence
		e.Recurrence = &r
	}
	if e.Organizer != nil {
		a := *e.Organizer
		e.Organizer = &a
	}
	if e.VideoCall != nil {
		v := *e.VideoCall
		e.VideoCall = &v
	}
	return e
}

// propValue is the raw value of c's first property named name, "" when
// absent.
func propValue(c *ics.Component, name string) string {
	if p := c.Property(name); p != nil {
		return p.Value
	}
	return ""
}
