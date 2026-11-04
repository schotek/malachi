// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The invented sample data of MALACHI_CALENDAR_SAMPLES (docs/calendar.md
// §5.10), for layout checks without a daemon: three accounts (Work on
// Microsoft 365, Gmail, Nextcloud over CalDAV) with their calendars, the
// events of now's week, three unanswered invitations and one invitation
// card per state. Every name and address is invented (example.org,
// example.com). The values are API types, so that the Swift and C# ports
// copy them as they are.

// Sample account ids.
const (
	SampleWork      api.AccountID = "acc_sample_work"
	SampleGmail     api.AccountID = "acc_sample_gmail"
	SampleNextcloud api.AccountID = "acc_sample_nextcloud"
)

// Sample calendar ids.
const (
	SampleWorkCalendar api.CalendarID = "cal_sample_work"
	SampleBirthdays    api.CalendarID = "cal_sample_birthdays"
	SampleHolidays     api.CalendarID = "cal_sample_holidays"
	SamplePersonal     api.CalendarID = "cal_sample_personal"
	SampleFamily       api.CalendarID = "cal_sample_family"
	SampleSport        api.CalendarID = "cal_sample_sport"
)

// Samples is the sample data of one moment.
type Samples struct {
	// Accounts are the three accounts as account.list lists them.
	Accounts []api.Account
	// Calendars is calendar.list's answer.
	Calendars api.CalendarListResult
	// Events are the week's events (each instance of the standup on its
	// own); Occurrences their occurrences with the unanswered invitation
	// that is not in a calendar.
	Events      []api.Event
	Occurrences []api.Occurrence
	// Invitations are the three unanswered invitations (invitation.list).
	Invitations []api.InvitationListItem
	// Cards are one invitation card per state, in InvitationStates' order.
	Cards       []api.InvitationCard
	Preferences api.CalendarPreferences
}

// InvitationStates lists the states of the sample cards.
var InvitationStates = []api.InvitationState{api.InvitationUnanswered, api.InvitationAnswered,
	api.InvitationOutdated, api.InvitationCancelled, api.InvitationProcessed, api.InvitationInvalid}

// SampleData builds the samples of the week of now (from Monday) in loc.
func SampleData(now time.Time, loc *time.Location) Samples {
	monday := StartOfWeek(now, time.Monday, loc)
	at := func(day, h, m int) *time.Time {
		d := AddDays(monday, day)
		t := time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, loc).UTC()
		return &t
	}
	date := func(day int) api.Date { return DateOf(AddDays(monday, day), loc) }
	synced := now.Add(-2 * time.Minute).UTC()

	jana := &api.Address{Name: "Jana Nováková", Address: "jana@example.org"}
	petr := &api.Address{Name: "Petr Svoboda", Address: "petr@example.org"}
	club := &api.Address{Name: "Climbing Club", Address: "club@example.com"}
	me := "me@example.org"

	s := Samples{Preferences: api.DefaultCalendarPreferences()}
	s.Accounts = []api.Account{
		{ID: SampleWork, Enabled: true, Capabilities: append(append([]api.AccountCapability{}, api.MailCapabilities...), api.CapabilityCalendar),
			Config: api.AccountConfig{Name: "Work", Email: me, Kind: api.AccountGraph,
				Calendar: &api.CalendarConfig{Sync: true}}},
		{ID: SampleGmail, Enabled: true, Capabilities: append(append([]api.AccountCapability{}, api.MailCapabilities...), api.CapabilityCalendar),
			Config: api.AccountConfig{Name: "Gmail", Email: "me@example.com", Kind: api.AccountIMAP,
				OAuth2: &api.OAuth2Config{Provider: "google"}}},
		{ID: SampleNextcloud, Enabled: true, Capabilities: []api.AccountCapability{api.CapabilityCalendar},
			Config: api.AccountConfig{Name: "Nextcloud", Email: "me@example.com", Kind: api.AccountCalDAV,
				CalDAV:   &api.CalDAVConfig{ServerURL: "https://cloud.example.com/remote.php/dav", Username: "me"},
				Calendar: &api.CalendarConfig{Sync: true}}},
	}
	state := func(id api.AccountID) api.CalendarSyncState {
		return api.CalendarSyncState{AccountID: id, Status: api.CalendarSyncIdle, Progress: -1, LastSync: &synced}
	}
	window := api.CalendarWindow{From: DateOf(AddDays(monday, -365), loc), To: DateOf(AddDays(monday, api.CalendarFutureDays), loc)}
	s.Calendars = api.CalendarListResult{Unanswered: 3, Accounts: []api.CalendarAccount{
		{AccountID: SampleWork, Source: api.CalendarSourceGraph, Sync: true, State: state(SampleWork), Window: window,
			Features: api.CalendarFeatures{FreeBusy: true, ProposeNewTime: true, Invitations: api.InvitationsViaServer, OnlineMeetings: true},
			Calendars: []api.Calendar{
				{ID: SampleWorkCalendar, AccountID: SampleWork, Name: "Calendar", Color: "#1f6feb", Visible: true,
					Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents, IsDefault: true, TimeZone: "Europe/Prague",
					OnlineMeetingProviders: []string{"teamsForBusiness"}},
				{ID: SampleBirthdays, AccountID: SampleWork, Name: "Birthdays", Color: "#8250df", Visible: true,
					Access: api.CalendarAccessRead, Kind: api.CalendarKindBirthdays},
				{ID: SampleHolidays, AccountID: SampleWork, Name: "Czech holidays", Color: "#1a7f37", Visible: true,
					Access: api.CalendarAccessRead, Kind: api.CalendarKindHolidays},
			}},
		{AccountID: SampleGmail, Source: api.CalendarSourceGoogle, Sync: true, State: state(SampleGmail), Window: window,
			Features: api.CalendarFeatures{Invitations: api.InvitationsViaServer},
			Calendars: []api.Calendar{
				{ID: SamplePersonal, AccountID: SampleGmail, Name: "Personal", Color: "#e5534b", Visible: true,
					Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents, IsDefault: true},
				{ID: SampleFamily, AccountID: SampleGmail, Name: "Family", Color: "#d4a72c", Visible: true,
					Access: api.CalendarAccessWrite, Kind: api.CalendarKindEvents, Owner: petr},
			}},
		{AccountID: SampleNextcloud, Source: api.CalendarSourceCalDAV, Sync: true, State: state(SampleNextcloud), Window: window,
			Features: api.CalendarFeatures{Invitations: api.InvitationsViaMail, MailAccountID: SampleGmail},
			Calendars: []api.Calendar{
				{ID: SampleSport, AccountID: SampleNextcloud, Name: "Sport", Color: "#0082c9", Visible: true,
					Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents, IsDefault: true},
			}},
	}}

	timed := func(id string, cal api.CalendarID, acc api.AccountID, title string, day, h, m, eh, em int) api.Event {
		return api.Event{Occurrence: api.Occurrence{EventID: api.EventID(id), CalendarID: cal, AccountID: acc, Title: title,
			Start: at(day, h, m), End: at(day, eh, em), Status: api.EventStatusConfirmed, ShowAs: api.ShowAsBusy,
			TimeZone: api.TimeZone(loc.String())},
			ETag: "\"1\"", CanEdit: true, CanDelete: true, RemoveIsDelete: true}
	}
	allDay := func(id string, cal api.CalendarID, acc api.AccountID, title string, day, days int) api.Event {
		return api.Event{Occurrence: api.Occurrence{EventID: api.EventID(id), CalendarID: cal, AccountID: acc, Title: title,
			AllDay: true, StartDate: date(day), EndDate: date(day + days), Status: api.EventStatusConfirmed, ShowAs: api.ShowAsFree},
			ETag: "\"1\""}
	}
	attendees := func(mine api.EventResponse) []api.Attendee {
		return []api.Attendee{
			{Name: jana.Name, Address: jana.Address, Role: api.AttendeeChair, Response: api.ResponseAccepted, Type: api.AttendeeTypePerson, Organizer: true},
			{Name: "Me", Address: me, Role: api.AttendeeRequired, Response: mine, Type: api.AttendeeTypePerson, Me: true},
			{Name: petr.Name, Address: petr.Address, Role: api.AttendeeRequired, Response: api.ResponseTentative, Type: api.AttendeeTypePerson},
			{Address: "eva@example.org", Role: api.AttendeeOptional, Response: api.ResponseNeedsAction, Type: api.AttendeeTypePerson},
		}
	}
	invited := func(e api.Event, mine api.EventResponse) api.Event {
		e.MyResponse, e.Organizer, e.AttendeeCount = mine, false, 4
		e.Attendees, e.OrganizerAddress = attendees(mine), jana
		e.CanEdit, e.CanRespond, e.RemoveIsDelete, e.ResponseRequested = false, true, false, true
		return e
	}

	standupRule := &api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, ByDay: []string{"MO", "TU", "WE", "TH", "FR"}}
	for day := range 5 {
		e := timed("ev_sample_standup", SampleWorkCalendar, SampleWork, "Standup", day, 9, 0, 9, 15)
		e.RecurrenceID = api.DateOrTime(at(day, 9, 0).Format(time.RFC3339))
		e.Recurring, e.Recurrence, e.Location, e.Alert, e.Alerts = true, standupRule, "Room B", true, []int{5}
		s.Events = append(s.Events, e)
	}

	planning := invited(timed("ev_sample_planning", SampleWorkCalendar, SampleWork, "Q4 planning", 1, 10, 0, 11, 30), api.ResponseAccepted)
	planning.VideoCall, planning.Location = true, "Room A"
	planning.VideoCallLink = &api.VideoCall{URL: "https://teams.microsoft.com/l/meetup-join/sample", Provider: api.VideoProviderTeams,
		Host: "teams.microsoft.com"}
	planning.Notes = "Agenda: budget, hiring, the roadmap.\nSlides: https://example.org/q4-slides"
	planning.Links = []string{"https://example.org/q4-slides"}
	planning.CanProposeNewTime = true
	planning.InvitationMessages = []api.MessageRef{{AccountID: SampleWork, MessageID: "msg_sample_planning"}}

	supplier := timed("ev_sample_supplier", SamplePersonal, SampleGmail, "Call with the supplier", 1, 10, 30, 11, 0)
	review := invited(timed("ev_sample_review", SampleWorkCalendar, SampleWork, "Design review", 1, 11, 0, 12, 0), api.ResponseTentative)
	review.ShowAs = api.ShowAsTentative

	budget := invited(timed("ev_sample_budget", SampleWorkCalendar, SampleWork, "Budget review", 2, 14, 0, 15, 0), api.ResponseNeedsAction)
	allHands := invited(timed("ev_sample_allhands", SampleWorkCalendar, SampleWork, "All-hands", 2, 16, 0, 17, 0), api.ResponseDeclined)
	offsite := allDay("ev_sample_offsite", SampleWorkCalendar, SampleWork, "Team offsite", 3, 2)
	offsite.CanEdit, offsite.CanDelete, offsite.RemoveIsDelete, offsite.Location = true, true, true, "Hotel Horizont"
	climbing := timed("ev_sample_climbing", SampleSport, SampleNextcloud, "Climbing", 3, 18, 0, 19, 30)
	climbing.Location = "Boulder hall"
	birthday := allDay("ev_sample_birthday", SampleBirthdays, SampleWork, "Petr Svoboda’s birthday", 5, 1)
	birthday.ReadOnly = true
	brunch := timed("ev_sample_brunch", SampleFamily, SampleGmail, "Family brunch", 6, 10, 0, 12, 0)
	s.Events = append(s.Events, planning, supplier, review, budget, allHands, offsite, climbing, birthday, brunch)

	for _, e := range s.Events {
		s.Occurrences = append(s.Occurrences, e.Occurrence)
	}
	demoStart, demoEnd := at(4, 15, 0), at(4, 16, 0)
	// The vendor demo is an invitation on the Gmail account not in any
	// calendar: drawn tentatively.
	s.Occurrences = append(s.Occurrences, api.Occurrence{AccountID: SampleGmail, Title: "Vendor demo", Start: demoStart, End: demoEnd,
		Status: api.EventStatusConfirmed, ShowAs: api.ShowAsTentative, MyResponse: api.ResponseNeedsAction, AttendeeCount: 3,
		Invitation: &api.MessageRef{AccountID: SampleGmail, MessageID: "msg_sample_demo"}})

	clubStart, clubEnd := at(9, 19, 0), at(9, 21, 0)
	s.Invitations = []api.InvitationListItem{
		{AccountID: SampleWork, MessageID: "msg_sample_budget", Summary: api.InvitationSummary{Method: api.InvitationMethodRequest,
			Kind: api.InvitationKindInvitation, State: api.InvitationUnanswered, Title: "Budget review",
			Start: budget.Start, End: budget.End, Organizer: jana}},
		{AccountID: SampleGmail, MessageID: "msg_sample_demo", Summary: api.InvitationSummary{Method: api.InvitationMethodRequest,
			Kind: api.InvitationKindInvitation, State: api.InvitationUnanswered, Title: "Vendor demo",
			Start: demoStart, End: demoEnd, Organizer: petr}},
		{AccountID: SampleGmail, MessageID: "msg_sample_club", Summary: api.InvitationSummary{Method: api.InvitationMethodRequest,
			Kind: api.InvitationKindUpdate, State: api.InvitationUnanswered, Title: "Club meeting",
			Start: clubStart, End: clubEnd, Organizer: club}},
	}

	card := func(state api.InvitationState) api.InvitationCard {
		c := api.InvitationCard{
			InvitationSummary: api.InvitationSummary{Method: api.InvitationMethodRequest, Kind: api.InvitationKindInvitation,
				State: state, Title: "Vendor demo", Start: demoStart, End: demoEnd, Organizer: petr},
			PartID: "2", UID: "sample-demo@example.org", TimeZone: api.TimeZone(loc.String()),
			Location: "Room C", AttendeeCount: 3,
			Attendees: []api.Attendee{
				{Name: petr.Name, Address: petr.Address, Role: api.AttendeeChair, Response: api.ResponseAccepted, Type: api.AttendeeTypePerson, Organizer: true},
				{Address: "me@example.com", Role: api.AttendeeRequired, Response: api.ResponseNeedsAction, Type: api.AttendeeTypePerson, Me: true},
				{Name: jana.Name, Address: jana.Address, Role: api.AttendeeOptional, Response: api.ResponseNeedsAction, Type: api.AttendeeTypePerson},
			},
			Me: &api.InvitationMe{Address: "me@example.com", Response: api.ResponseNeedsAction},
			Conflicts: []api.InvitationConflict{{Title: "Standup", Start: *at(4, 15, 30), End: *at(4, 16, 0),
				CalendarID: SampleWorkCalendar, EventID: "ev_sample_conflict"}},
			Day: api.InvitationDay{From: *at(4, 8, 0), To: *at(4, 17, 0), Busy: []api.DayBusy{
				{Start: *at(4, 9, 0), End: *at(4, 9, 15), CalendarID: SampleWorkCalendar},
				{Start: *at(4, 15, 30), End: *at(4, 16, 0), CalendarID: SampleWorkCalendar}}},
			Targets:    []api.InvitationTarget{{CalendarID: SamplePersonal, IsDefault: true}, {CalendarID: SampleFamily}},
			CanRespond: true, ReplyVia: api.InvitationsViaMail, ReplyAccountID: SampleGmail, ReplyTo: petr,
		}
		switch state {
		case api.InvitationAnswered:
			c.Response, c.CalendarID, c.EventID = api.ResponseAccepted, SamplePersonal, "ev_sample_demo"
			responded := now.Add(-time.Hour).UTC()
			c.RespondedAt, c.Me.Response = &responded, api.ResponseAccepted
		case api.InvitationOutdated:
			c.Kind, c.Sequence, c.CanRespond = api.InvitationKindUpdate, 1, false
			c.Previous = &api.InvitationPrevious{Start: api.DateOrTime(at(4, 14, 0).Format(time.RFC3339)),
				End: api.DateOrTime(at(4, 15, 0).Format(time.RFC3339))}
		case api.InvitationCancelled:
			c.Method, c.Kind, c.CanRespond, c.Targets = api.InvitationMethodCancel, api.InvitationKindCancelled, false, []api.InvitationTarget{}
			c.EventID, c.CalendarID = "ev_sample_demo", SamplePersonal
		case api.InvitationProcessed:
			c.Method, c.Kind, c.CanRespond, c.Targets = api.InvitationMethodReply, api.InvitationKindReply, false, []api.InvitationTarget{}
			c.Organizer, c.ReplyFrom, c.Response, c.ReplyApplied = &api.Address{Name: "Me", Address: "me@example.com"}, jana, api.ResponseAccepted, true
			c.Conflicts = []api.InvitationConflict{}
		case api.InvitationInvalid:
			c = api.InvitationCard{InvitationSummary: api.InvitationSummary{Method: api.InvitationMethodOther,
				Kind: api.InvitationKindOther, State: api.InvitationInvalid}, PartID: "2",
				Conflicts: []api.InvitationConflict{}, Targets: []api.InvitationTarget{},
				Day: api.InvitationDay{Busy: []api.DayBusy{}}}
		}
		return c
	}
	for _, st := range InvitationStates {
		s.Cards = append(s.Cards, card(st))
	}
	return s
}

// SampleAccountName is the name of a sample account ("" for another id).
func (s Samples) SampleAccountName(id api.AccountID) string {
	for _, a := range s.Accounts {
		if a.ID == id {
			return a.Config.Name
		}
	}
	return ""
}

// CalendarName is the name of a sample calendar ("" for another id).
func (s Samples) CalendarName(id api.CalendarID) string {
	for _, a := range s.Calendars.Accounts {
		for _, c := range a.Calendars {
			if c.ID == id {
				return c.Name
			}
		}
	}
	return ""
}
