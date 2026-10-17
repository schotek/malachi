// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package imip reads iTIP messages (RFC 5546) carried by mail (iMIP, RFC
// 6047) into an Invitation, and builds the REPLY, REQUEST and CANCEL the
// daemon sends (docs/calendar.md §3.4, D9, D10).
//
// Every part is hostile input. ParseMessage decides nothing about trust:
// it reports what the part says (the organiser, the replying attendee)
// and the core applies D10 with the mail's From (ReplyMatchesSender,
// OrganizerMismatch). Nothing here sends mail, fetches a URL or replies
// automatically.
package imip

import (
	"errors"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calendar/ics"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Errors of ParseMessage and the builders.
var (
	// ErrMultipleCalendars: the part holds more than one VCALENDAR; iTIP
	// carries exactly one, so it is refused (never guessed).
	ErrMultipleCalendars = errors.New("imip: more than one VCALENDAR")
	// ErrNoOrganizer: a REPLY or CANCEL cannot be built without one.
	ErrNoOrganizer = errors.New("imip: no organizer")
	// ErrNoAttendee: the user is not an attendee to answer as, or a
	// REQUEST has nobody to invite.
	ErrNoAttendee = errors.New("imip: no attendee")
	// ErrResponse: a response other than accepted, tentative, declined.
	ErrResponse = errors.New("imip: bad response")
	// ErrScope: CANCEL of thisAndFuture (the cut series goes as a REQUEST).
	ErrScope = errors.New("imip: scope not possible")
)

// Problem codes of an Invitation beside the calendar.Problem* ones.
const (
	ProblemNoMethod       = "noMethod"       // METHOD absent: read as publish
	ProblemReplyAttendees = "replyAttendees" // a REPLY with other than one ATTENDEE
)

// Invitation is one iTIP message read from a calendar part.
type Invitation struct {
	Method api.InvitationMethod
	// RawMethod is the METHOD as written (upper case, cleaned), "" absent.
	RawMethod    string
	UID          string
	RecurrenceID api.DateOrTime // an instance message: the instance
	Sequence     int
	DTStamp      time.Time
	// Event is the master of the object, or its first override when it
	// holds only instances (an instance invitation, a CANCEL of one
	// occurrence): the summary fields of the card.
	Event calendar.Event
	// Object is everything of the UID (master and overrides).
	Object calendar.Object
	// Organizer is the ORGANIZER (mailto: only); SentBy its SENT-BY.
	Organizer *api.Address
	SentBy    string
	// Me is the user on the attendee list (the first ATTENDEE that is one
	// of the user's addresses, looked for among all ATTENDEEs, not only
	// the listed ones), with the response it carries; nil when absent.
	Me *api.InvitationMe
	// RSVP: the organiser asks the user to answer.
	RSVP bool
	// A REPLY: the replying attendee (the first mailto: ATTENDEE), its
	// response and comment, and how many ATTENDEEs the reply has (RFC
	// 5546 allows one; more is suspicious, ProblemReplyAttendees).
	ReplyFrom      *api.Address
	ReplyResponse  api.EventResponse
	ReplyComment   string
	ReplyAttendees int
	Problems       []calendar.Problem

	vevent    *ics.Component   // the VEVENT Event was read from
	timezones []*ics.Component // the object's VTIMEZONEs
}

// ParseMessage reads one calendar part (≤ api.MaxICSBytes): the METHOD
// (absent: publish; unknown: other), the first VEVENT's UID (VEVENTs of
// other UIDs are ignored, ProblemMixedUIDs), the master or the instance,
// SEQUENCE, DTSTAMP, ORGANIZER, the attendees (≤ MaxEventAttendeesStored,
// all counted), the attendee that is the user (me: the user's addresses,
// case-insensitive) and, for a reply, the replying attendee and its
// PARTSTAT. A VEVENT without DTSTART is kept (REPLY and CANCEL may omit
// it). Several VCALENDARs are ErrMultipleCalendars; no VEVENT is
// calendar.ErrNoEvent; over the size cap ics.ErrTooBig.
func ParseMessage(data []byte, me []string) (inv Invitation, err error) {
	defer func() {
		if r := recover(); r != nil {
			inv, err = Invitation{}, ics.ErrMalformed
		}
	}()
	parsed, err := ics.Parse(data)
	if err != nil {
		return Invitation{}, err
	}
	if len(parsed.Calendars) > 1 {
		return Invitation{}, ErrMultipleCalendars
	}
	cal := parsed.Calendar()
	obj, err := calendar.FromICS(parsed, calendar.FromOptions{Me: me, AllowNoStart: true})
	if err != nil {
		return Invitation{}, err
	}
	obj.Raw = append([]byte(nil), data...)
	inv.Object, inv.Problems = obj, obj.Problems
	inv.UID = obj.UID()
	inv.RawMethod = strings.ToUpper(calendar.CleanLine(propValue(cal, "METHOD"), 64))
	inv.Method = methodOf(inv.RawMethod)
	if inv.RawMethod == "" {
		inv.Problems = append(inv.Problems, calendar.Problem{Code: ProblemNoMethod})
	}
	if obj.Master != nil {
		inv.Event = *obj.Master
	} else {
		inv.Event = obj.Overrides[0]
		inv.RecurrenceID = inv.Event.RecurrenceID
	}
	inv.Sequence, inv.DTStamp = inv.Event.Sequence, inv.Event.DTStamp
	inv.Organizer = inv.Event.Organizer
	inv.timezones = cal.Children("VTIMEZONE")
	for _, c := range cal.Children("VEVENT") {
		if calendar.CleanLine(c.Property("UID").Text(), calendar.MaxUIDBytes) != inv.UID {
			continue
		}
		rid := c.Property("RECURRENCE-ID") != nil
		if rid == (inv.RecurrenceID != "") {
			inv.vevent = c
			break
		}
	}
	if inv.vevent == nil {
		return Invitation{}, calendar.ErrNoEvent
	}
	if p := inv.vevent.Property("ORGANIZER"); p != nil {
		if a := ics.ParseCalAddress(p.Param("SENT-BY")); a.IsEmail() {
			inv.SentBy = calendar.CleanLine(a.Email, calendar.MaxNameBytes)
		}
	}
	mine := map[string]bool{}
	for _, a := range me {
		mine[strings.ToLower(strings.TrimSpace(a))] = true
	}
	for _, p := range inv.vevent.Properties("ATTENDEE") {
		a := p.CalAddress()
		if !a.IsEmail() {
			continue
		}
		if inv.Me == nil && mine[strings.ToLower(a.Email)] {
			inv.Me = &api.InvitationMe{Address: calendar.CleanLine(a.Email, calendar.MaxNameBytes), Response: calendar.PartstatResponse(p.Param("PARTSTAT"))}
			inv.RSVP = strings.EqualFold(p.Param("RSVP"), "TRUE")
		}
	}
	if inv.Method == api.InvitationMethodReply {
		props := inv.vevent.Properties("ATTENDEE")
		inv.ReplyAttendees = len(props)
		if len(props) != 1 {
			inv.Problems = append(inv.Problems, calendar.Problem{Code: ProblemReplyAttendees})
		}
		for _, p := range props {
			if a := p.CalAddress(); a.IsEmail() {
				inv.ReplyFrom = &api.Address{Name: calendar.CleanLine(p.Param("CN"), calendar.MaxNameBytes), Address: calendar.CleanLine(a.Email, calendar.MaxNameBytes)}
				inv.ReplyResponse = calendar.PartstatResponse(p.Param("PARTSTAT"))
				break
			}
		}
		inv.ReplyComment = calendar.CleanBlock(inv.vevent.Property("COMMENT").Text(), api.MaxCommentBytes)
	}
	return inv, nil
}

func methodOf(m string) api.InvitationMethod {
	switch m {
	case "":
		return api.InvitationMethodPublish
	case "REQUEST":
		return api.InvitationMethodRequest
	case "REPLY":
		return api.InvitationMethodReply
	case "CANCEL":
		return api.InvitationMethodCancel
	case "COUNTER":
		return api.InvitationMethodCounter
	case "DECLINECOUNTER":
		return api.InvitationMethodDeclineCounter
	case "REFRESH":
		return api.InvitationMethodRefresh
	case "ADD":
		return api.InvitationMethodAdd
	case "PUBLISH":
		return api.InvitationMethodPublish
	}
	return api.InvitationMethodOther
}

// KindOf is the list pill of a method as far as the part alone tells it:
// a request is an invitation (the core makes it an update when it changes
// a known event), cancel cancelled, reply reply, anything else other.
func KindOf(m api.InvitationMethod) api.InvitationKind {
	switch m {
	case api.InvitationMethodRequest:
		return api.InvitationKindInvitation
	case api.InvitationMethodCancel:
		return api.InvitationKindCancelled
	case api.InvitationMethodReply:
		return api.InvitationKindReply
	}
	return api.InvitationKindOther
}

// ReplyMatchesSender reports whether a REPLY's attendee is the mail's
// From address (D10: only then may the core apply it).
func (inv *Invitation) ReplyMatchesSender(from string) bool {
	return inv.ReplyFrom != nil && strings.EqualFold(strings.TrimSpace(from), inv.ReplyFrom.Address)
}

// OrganizerMismatch reports whether the organiser's domain is not the
// From address's domain (neither equal nor one a subdomain of the
// other); InvitationCard.organizerMismatch. No organiser: false.
func (inv *Invitation) OrganizerMismatch(from string) bool {
	if inv.Organizer == nil {
		return false
	}
	a, b := domain(inv.Organizer.Address), domain(from)
	if a == "" || b == "" {
		return true
	}
	return a != b && !strings.HasSuffix(a, "."+b) && !strings.HasSuffix(b, "."+a)
}

func domain(addr string) string {
	i := strings.LastIndexByte(addr, '@')
	if i < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(addr[i+1:]), "."))
}

// Summary returns the InvitationSummary fields the part gives (State is
// the core's; Kind as KindOf).
func (inv *Invitation) Summary() api.InvitationSummary {
	ev := &inv.Event
	s := api.InvitationSummary{Method: inv.Method, Kind: KindOf(inv.Method), Title: ev.Title, AllDay: ev.AllDay, Organizer: inv.Organizer, ReplyFrom: inv.ReplyFrom}
	switch {
	case ev.Start.IsZero():
	case ev.AllDay:
		s.StartDate = ev.StartDate
	default:
		st, en := ev.Start.UTC(), ev.End.UTC()
		s.Start, s.End = &st, &en
	}
	return s
}

// Card returns the static fields of the InvitationCard (the derived ones
// — state, response, conflicts, day, targets, replyVia — are the core's).
// Floating times are read as if UTC (the card has no zone for them).
func (inv *Invitation) Card(partID string) api.InvitationCard {
	ev := &inv.Event
	c := api.InvitationCard{
		InvitationSummary: inv.Summary(),
		PartID:            partID, UID: inv.UID, RecurrenceID: inv.RecurrenceID, Sequence: inv.Sequence,
		Recurrence: ev.Recurrence, Location: ev.Location, VideoCall: ev.VideoCall, Notes: ev.Notes,
		AttendeeCount: ev.AttendeeCount, Me: inv.Me,
		Conflicts: []api.InvitationConflict{}, Targets: []api.InvitationTarget{},
		Day: api.InvitationDay{Busy: []api.DayBusy{}},
	}
	if ev.AllDay {
		c.EndDate = ev.EndDate
	}
	if !ev.AllDay && !ev.Floating {
		c.TimeZone = api.TimeZone(ev.Zone)
	}
	n := min(len(ev.Attendees), api.MaxInvitationAttendees)
	c.Attendees = append([]api.Attendee(nil), ev.Attendees[:n]...)
	return c
}

func propValue(c *ics.Component, name string) string {
	if p := c.Property(name); p != nil {
		return p.Value
	}
	return ""
}
