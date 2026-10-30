// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// roundTrip encodes v, decodes it into a fresh T and requires the same
// value back; it returns the JSON.
func roundTrip[T any](t *testing.T, name string, v T) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var back T
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if !reflect.DeepEqual(back, v) {
		t.Fatalf("%s round trip:\n got %+v\nwant %+v\njson %s", name, back, v, raw)
	}
	return string(raw)
}

// The limits are the values of docs/calendar.md §4.8; the Windows and
// Swift mirrors count and compare them.
func TestCalendarLimits(t *testing.T) {
	limits := map[string]int{
		"MaxICSBytes": MaxICSBytes, "MaxEventTitleBytes": MaxEventTitleBytes,
		"MaxEventLocationBytes": MaxEventLocationBytes, "MaxEventNotesBytes": MaxEventNotesBytes,
		"MaxMailTextBytes": MaxMailTextBytes, "MaxCommentBytes": MaxCommentBytes,
		"MaxEventAttendeesIn": MaxEventAttendeesIn, "MaxEventAttendeesStored": MaxEventAttendeesStored,
		"MaxInvitationAttendees": MaxInvitationAttendees, "MaxEventAlerts": MaxEventAlerts,
		"MaxAlertMinutes": MaxAlertMinutes, "MaxEventAttachments": MaxEventAttachments,
		"MaxEventAttachmentBytes": MaxEventAttachmentBytes, "MaxEventListDays": MaxEventListDays,
		"MaxEventListResults": MaxEventListResults, "DefaultEventListResults": DefaultEventListResults,
		"MaxEventSearchResults": MaxEventSearchResults, "MaxSeriesOccurrences": MaxSeriesOccurrences,
		"MaxCalendarsPerAccount": MaxCalendarsPerAccount, "MaxEventsPerCalendar": MaxEventsPerCalendar,
		"MaxFreeBusyAttendees": MaxFreeBusyAttendees, "MaxFreeBusyDays": MaxFreeBusyDays,
		"MaxFreeSlots": MaxFreeSlots, "MaxInvitationList": MaxInvitationList,
		"CalendarFutureDays": CalendarFutureDays, "DefaultCalendarKeepDays": DefaultCalendarKeepDays,
		"MaxRecurrenceInterval": MaxRecurrenceInterval, "MaxRecurrenceCount": MaxRecurrenceCount,
	}
	want := map[string]int{
		"MaxICSBytes": 1 << 20, "MaxEventTitleBytes": 1024, "MaxEventLocationBytes": 1024,
		"MaxEventNotesBytes": 32768, "MaxMailTextBytes": 4096, "MaxCommentBytes": 2048,
		"MaxEventAttendeesIn": 100, "MaxEventAttendeesStored": 500, "MaxInvitationAttendees": 50,
		"MaxEventAlerts": 4, "MaxAlertMinutes": 40320, "MaxEventAttachments": 10,
		"MaxEventAttachmentBytes": 3 << 20, "MaxEventListDays": 400, "MaxEventListResults": 5000,
		"DefaultEventListResults": 2000, "MaxEventSearchResults": 200, "MaxSeriesOccurrences": 1000,
		"MaxCalendarsPerAccount": 200, "MaxEventsPerCalendar": 50000, "MaxFreeBusyAttendees": 20,
		"MaxFreeBusyDays": 62, "MaxFreeSlots": 50, "MaxInvitationList": 500, "CalendarFutureDays": 400,
		"DefaultCalendarKeepDays": 365, "MaxRecurrenceInterval": 99, "MaxRecurrenceCount": 999,
	}
	if len(limits) != 28 {
		t.Fatalf("%d calendar limits, want 28", len(limits))
	}
	if !reflect.DeepEqual(limits, want) {
		t.Fatalf("limits:\n got %v\nwant %v", limits, want)
	}
}

func TestCalendarEnumValues(t *testing.T) {
	sets := map[string][]string{
		"CalendarSource": {string(CalendarSourceGraph), string(CalendarSourceCalDAV), string(CalendarSourceGoogle)},
		"CalendarAccess": {string(CalendarAccessOwner), string(CalendarAccessWrite), string(CalendarAccessRead)},
		"CalendarKind": {string(CalendarKindEvents), string(CalendarKindBirthdays), string(CalendarKindHolidays),
			string(CalendarKindOther)},
		"EventResponse": {string(ResponseNeedsAction), string(ResponseAccepted), string(ResponseTentative),
			string(ResponseDeclined), string(ResponseDelegated)},
		"ShowAs": {string(ShowAsFree), string(ShowAsTentative), string(ShowAsBusy), string(ShowAsOOF),
			string(ShowAsWorkingElsewhere), string(ShowAsUnknown)},
		"EventStatus": {string(EventStatusConfirmed), string(EventStatusTentative), string(EventStatusCancelled)},
		"EditScope":   {string(EditScopeThis), string(EditScopeThisAndFuture), string(EditScopeAll)},
		"InvitationMethod": {string(InvitationMethodRequest), string(InvitationMethodReply),
			string(InvitationMethodCancel), string(InvitationMethodCounter), string(InvitationMethodDeclineCounter),
			string(InvitationMethodRefresh), string(InvitationMethodAdd), string(InvitationMethodPublish),
			string(InvitationMethodOther)},
		"InvitationKind": {string(InvitationKindInvitation), string(InvitationKindUpdate),
			string(InvitationKindCancelled), string(InvitationKindReply), string(InvitationKindOther)},
		"InvitationState": {string(InvitationUnanswered), string(InvitationAnswered), string(InvitationOutdated),
			string(InvitationCancelled), string(InvitationProcessed), string(InvitationInvalid)},
		"CalendarSyncStatus": {string(CalendarSyncIdle), string(CalendarSyncSyncing), string(CalendarSyncOffline),
			string(CalendarSyncAuthRequired), string(CalendarSyncConsentRequired), string(CalendarSyncError),
			string(CalendarSyncDisabled)},
		"InvitationsVia": {string(InvitationsViaServer), string(InvitationsViaMail), string(InvitationsViaNone)},
		"VideoProvider": {string(VideoProviderTeams), string(VideoProviderZoom), string(VideoProviderMeet),
			string(VideoProviderWebex), string(VideoProviderJitsi), string(VideoProviderWhereby),
			string(VideoProviderOther)},
		"CalDAVScheduling": {string(CalDAVSchedulingAuto), string(CalDAVSchedulingServer), string(CalDAVSchedulingClient)},
		"AfterAnswer":      {string(AfterAnswerKeep), string(AfterAnswerArchive)},
		"WeekStart":        {string(WeekStartLocale), string(WeekStartMonday), string(WeekStartSunday), string(WeekStartSaturday)},
		"RecurrenceFrequency": {string(FrequencyDaily), string(FrequencyWeekly), string(FrequencyMonthly),
			string(FrequencyYearly)},
		"AttendeeRole": {string(AttendeeRequired), string(AttendeeOptional), string(AttendeeChair),
			string(AttendeeNonParticipant)},
		"AttendeeType": {string(AttendeeTypePerson), string(AttendeeTypeRoom), string(AttendeeTypeResource),
			string(AttendeeTypeGroup), string(AttendeeTypeUnknown)},
		"EventAlertState": {string(AlertDue), string(AlertDismissed)},
		"VideoCallMode":   {string(VideoCallNone), string(VideoCallCreate), string(VideoCallLink)},
		"CalDAVFoundVia":  {string(CalDAVFoundWellKnown), string(CalDAVFoundSRV), string(CalDAVFoundURL)},
	}
	want := map[string]string{
		"CalendarSource":      "graph caldav google",
		"CalendarAccess":      "owner write read",
		"CalendarKind":        "events birthdays holidays other",
		"EventResponse":       "needsAction accepted tentative declined delegated",
		"ShowAs":              "free tentative busy oof workingElsewhere unknown",
		"EventStatus":         "confirmed tentative cancelled",
		"EditScope":           "this thisAndFuture all",
		"InvitationMethod":    "request reply cancel counter declineCounter refresh add publish other",
		"InvitationKind":      "invitation update cancelled reply other",
		"InvitationState":     "unanswered answered outdated cancelled processed invalid",
		"CalendarSyncStatus":  "idle syncing offline authRequired consentRequired error disabled",
		"InvitationsVia":      "server mail none",
		"VideoProvider":       "teams zoom meet webex jitsi whereby other",
		"CalDAVScheduling":    "auto server client",
		"AfterAnswer":         "keep archive",
		"WeekStart":           "locale monday sunday saturday",
		"RecurrenceFrequency": "daily weekly monthly yearly",
		"AttendeeRole":        "required optional chair nonParticipant",
		"AttendeeType":        "person room resource group unknown",
		"EventAlertState":     "due dismissed",
		"VideoCallMode":       "none create link",
		"CalDAVFoundVia":      "wellKnown srv url",
	}
	if len(sets) != len(want) {
		t.Fatalf("%d enums, want %d", len(sets), len(want))
	}
	for name, values := range sets {
		if got := strings.Join(values, " "); got != want[name] {
			t.Errorf("%s: %q, want %q", name, got, want[name])
		}
	}
	if AccountCalDAV != "caldav" || CapabilityCalendar != "calendar" {
		t.Errorf("kind %q, capability %q", AccountCalDAV, CapabilityCalendar)
	}
}

func TestCalendarErrorCodes(t *testing.T) {
	want := map[ErrorCode]string{
		1006: "readOnly", 1007: "invitationsUnavailable", 1107: "calendarNotFound", 1108: "eventNotFound",
		1204: "calendarConsentRequired", 1306: "eventGone", 1507: "malformedCalendar", 1508: "recurrenceUnsupported",
	}
	got := map[ErrorCode]string{
		CodeReadOnly: CodeReadOnly.String(), CodeInvitationsUnavailable: CodeInvitationsUnavailable.String(),
		CodeCalendarNotFound: CodeCalendarNotFound.String(), CodeEventNotFound: CodeEventNotFound.String(),
		CodeCalendarConsentRequired: CodeCalendarConsentRequired.String(), CodeEventGone: CodeEventGone.String(),
		CodeMalformedCalendar: CodeMalformedCalendar.String(), CodeRecurrenceUnsupported: CodeRecurrenceUnsupported.String(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codes:\n got %v\nwant %v", got, want)
	}
	data := roundTrip(t, "consent", CalendarConsentRequiredData{AccountID: "acc_1", SignIn: true})
	if data != `{"accountId":"acc_1","signIn":true}` {
		t.Fatalf("consent data %s", data)
	}
}

// The calendar methods follow the board's in AllMethods, in the order of
// docs/calendar.md §4.9, and only the CRM and assistant profile methods
// (crm_test.go, assistant_test.go) come after them; the notifications
// follow notify.draftsChanged, and only the assistant profile's comes
// after them.
func TestCalendarMethodsOrder(t *testing.T) {
	methods := []string{
		"account.discoverCalDAV", "account.listCalDAVCalendars",
		"calendar.list", "calendar.update", "calendar.sync", "calendar.preferences", "calendar.setPreferences",
		"calendar.freeBusy", "calendar.findFreeTime",
		"event.list", "event.get", "event.search", "event.create", "event.update", "event.delete",
		"event.respond", "event.snoozeAlert", "event.dismissAlert",
		"invitation.list", "invitation.respond", "invitation.applyCancel",
	}
	n := len(AllMethods) - len(crmMethods) - len(assistantMethods) - len(contactsBookMethods)
	if n < len(methods) || !reflect.DeepEqual(AllMethods[n-len(methods):n], methods) {
		t.Fatalf("AllMethods does not end with the calendar methods: %v", AllMethods[n-len(methods):n])
	}
	if AllMethods[n-len(methods)-1] != MethodBoardRunEnd {
		t.Fatalf("the calendar methods do not follow the board's")
	}
	notes := []string{"notify.calendarChanged", "notify.calendarState", "notify.eventAlert", "notify.invitationsChanged"}
	m := len(AllNotifications) - 2
	if !reflect.DeepEqual(AllNotifications[m-len(notes):m], notes) || AllNotifications[m-len(notes)-1] != NotifyDraftsChanged {
		t.Fatalf("AllNotifications: %v", AllNotifications)
	}
}

// Minimal values send the required fields only.
func TestCalendarMinimalJSON(t *testing.T) {
	start := time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	occ := Occurrence{AccountID: "acc_1", Title: "", Start: &start, End: &end, Status: EventStatusConfirmed, ShowAs: ShowAsBusy}
	if got, want := roundTrip(t, "occurrence", occ),
		`{"accountId":"acc_1","title":"","start":"2026-10-14T08:00:00Z","end":"2026-10-14T08:30:00Z",`+
			`"allDay":false,"status":"confirmed","showAs":"busy"}`; got != want {
		t.Fatalf("occurrence:\n got %s\nwant %s", got, want)
	}
	if got, want := roundTrip(t, "calendar config", CalendarConfig{}), `{"sync":false}`; got != want {
		t.Fatalf("calendar config %s", got)
	}
	if c := (CalendarConfig{}); !c.ShowsUnanswered() || !c.ShowsInvitationCard() {
		t.Fatalf("defaults of CalendarConfig")
	}
	if c := (CalendarConfig{ShowUnanswered: Ptr(false), InvitationCard: Ptr(false)}); c.ShowsUnanswered() || c.ShowsInvitationCard() {
		t.Fatalf("CalendarConfig false")
	}
	roundTrip(t, "account config", AccountConfig{Name: "Cal", Email: "jana@example.org", Kind: AccountCalDAV,
		CalDAV: &CalDAVConfig{ServerURL: "https://cloud.example.org/remote.php/dav", Username: "jana"}})
	if got := roundTrip(t, "update", CalendarPreferencesUpdate{}); got != `{}` {
		t.Fatalf("empty preferences update %s", got)
	}
	if got := roundTrip(t, "sync", CalendarSyncResult{}); got != `{}` {
		t.Fatalf("sync result %s", got)
	}
	if got := roundTrip(t, "changed", CalendarChangedNotification{}); got != `{}` {
		t.Fatalf("changed %s", got)
	}
	if got := roundTrip(t, "invitations changed", InvitationsChangedNotification{}); got != `{}` {
		t.Fatalf("invitations changed %s", got)
	}
}

// EventInput.Alerts tells absent (the default alert) from [] (none).
func TestEventInputAlerts(t *testing.T) {
	var in EventInput
	if err := json.Unmarshal([]byte(`{"title":"a","allDay":true,"startDate":"2026-10-14","endDate":"2026-10-15"}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.Alerts != nil {
		t.Fatalf("absent alerts decoded as %v", in.Alerts)
	}
	if err := json.Unmarshal([]byte(`{"title":"a","alerts":[]}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.Alerts == nil || len(in.Alerts) != 0 {
		t.Fatalf("[] decoded as %#v", in.Alerts)
	}
	raw, _ := json.Marshal(EventInput{Alerts: []int{}})
	if !strings.Contains(string(raw), `"alerts":[]`) {
		t.Fatalf("empty alerts not sent: %s", raw)
	}
	raw, _ = json.Marshal(EventInput{})
	if !strings.Contains(string(raw), `"alerts":null`) {
		t.Fatalf("nil alerts: %s", raw)
	}
}

// The invitation card of message.get takes the place of the summary of a
// listing under the same key.
func TestMessageInvitationShadowsSummary(t *testing.T) {
	sum := InvitationSummary{Method: InvitationMethodRequest, Kind: InvitationKindInvitation,
		State: InvitationUnanswered, Title: "Q4 planning"}
	ms := MessageSummary{ID: "m_1", AccountID: "acc_1", FolderID: "f_inbox", Subject: "Q4", Invitation: &sum}
	raw := roundTrip(t, "summary", ms)
	if !strings.Contains(raw, `"invitation":{"method":"request","kind":"invitation","state":"unanswered","title":"Q4 planning"}`) {
		t.Fatalf("summary: %s", raw)
	}
	m := Message{MessageSummary: ms, Invitation: &InvitationCard{InvitationSummary: sum, PartID: "2", UID: "u@x",
		Conflicts: []InvitationConflict{}, Targets: []InvitationTarget{}, Day: InvitationDay{Busy: []DayBusy{}},
		ReplyVia: InvitationsViaMail}}
	raw2, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw2), `"invitation":`) != 1 || !strings.Contains(string(raw2), `"partId":"2"`) {
		t.Fatalf("message: %s", raw2)
	}
	var back Message
	if err := json.Unmarshal(raw2, &back); err != nil {
		t.Fatal(err)
	}
	if back.Invitation == nil || back.Invitation.PartID != "2" || back.MessageSummary.Invitation != nil {
		t.Fatalf("decoded message: %+v / %+v", back.Invitation, back.MessageSummary.Invitation)
	}
}

// Every new type with its optional fields present survives a round trip.
func TestCalendarFullJSON(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	addr := &Address{Name: "Jana Nováková", Address: "jana@example.org"}
	state := CalendarSyncState{AccountID: "acc_1", Status: CalendarSyncConsentRequired, Progress: -1, LastSync: &t0,
		Error: &Error{Code: CodeCalendarConsentRequired, Message: "consent"}}
	cal := Calendar{ID: "cal_1", AccountID: "acc_1", Name: "Calendar", RemoteID: "/c/personal/", Disabled: true, Color: "#1f6feb", ServerColor: "#1f6feb",
		UserColor: "#e5534b", Visible: true, Access: CalendarAccessOwner, Kind: CalendarKindEvents, Owner: addr,
		IsDefault: true, TimeZone: "Europe/Prague", OnlineMeetingProviders: []string{"teamsForBusiness"}}
	rec := &Recurrence{Frequency: FrequencyMonthly, Interval: 2, ByDay: []string{"2TU", "-1FR"}, ByMonthDay: []int{14, -1},
		ByMonth: []int{10}, Until: "2027-10-14", WeekStart: "MO", Complex: true, Exceptions: 3}
	att := Attendee{Name: "Jana", Address: "jana@example.org", Role: AttendeeChair, Response: ResponseTentative,
		Type: AttendeeTypeRoom, Me: true, Organizer: true}
	video := &VideoCall{URL: "https://teams.microsoft.com/l/meetup-join/x", Provider: VideoProviderTeams, Host: "teams.microsoft.com"}
	occ := Occurrence{EventID: "ev_1", RecurrenceID: "2026-10-14T08:00:00Z", SeriesID: "ev_0", CalendarID: "cal_1",
		AccountID: "acc_1", Title: "Standup", Start: &t0, End: &t1, AllDay: false, Floating: true, TimeZone: "Europe/Prague",
		Location: "Room B", Status: EventStatusTentative, ShowAs: ShowAsWorkingElsewhere, MyResponse: ResponseNeedsAction,
		Organizer: true, AttendeeCount: 4, Recurring: true, VideoCall: true, Private: true, Alert: true, ReadOnly: true,
		Invitation: &MessageRef{AccountID: "acc_1", MessageID: "m_1"}}
	ev := Event{Occurrence: occ, ETag: "W/\"1\"", Notes: "Agenda\nhttps://example.org", Links: []string{"https://example.org"},
		VideoCallLink: video, OrganizerAddress: addr, Attendees: []Attendee{att}, AttendeesTruncated: true, Recurrence: rec,
		Alerts: []int{15, 0}, ResponseRequested: true, CanEdit: true, CanDelete: true, CanRespond: true, CanProposeNewTime: true,
		RemoveIsDelete: true, InvitationMessages: []MessageRef{{AccountID: "acc_1", MessageID: "m_1"}}, Created: &t0, Updated: &t1}
	input := EventInput{Title: "Review", AllDay: false, Start: "2026-10-14T10:00:00", End: "2026-10-14T11:00:00",
		TimeZone: "Europe/Prague", StartDate: "2026-10-14", EndDate: "2026-10-15", Location: "Room", Notes: "notes",
		Recurrence:     &Recurrence{Frequency: FrequencyWeekly, Interval: 1, ByDay: []string{"MO"}, Count: 10},
		KeepRecurrence: true, Alerts: []int{}, ShowAs: ShowAsFree, Private: true,
		Attendees:   []EventInputAttendee{{Name: "Jana", Address: "jana@example.org", Role: AttendeeOptional}},
		VideoCall:   &VideoCallInput{Mode: VideoCallCreate, Provider: "teamsForBusiness", URL: "https://x.example"},
		Attachments: []string{"att_1"}}
	mail := &MailText{Subject: "Invitation", Text: "Please come"}
	sum := InvitationSummary{Method: InvitationMethodRequest, Kind: InvitationKindUpdate, State: InvitationAnswered,
		Title: "Q4", Start: &t0, End: &t1, StartDate: "2026-10-14", AllDay: true, Organizer: addr, ReplyFrom: addr}
	card := InvitationCard{InvitationSummary: sum, PartID: "2", UID: "uid@x", RecurrenceID: "2026-10-14", Sequence: 2,
		EndDate: "2026-10-15", TimeZone: "Europe/Prague", Recurrence: rec, Location: "Room", VideoCall: video, Notes: "n",
		OrganizerMismatch: true, AttendeeCount: 4, Attendees: []Attendee{att},
		Me: &InvitationMe{Address: "me@example.org", Response: ResponseAccepted}, Response: ResponseAccepted,
		RespondedAt: &t1, EventID: "ev_1", CalendarID: "cal_1",
		Previous:  &InvitationPrevious{Start: "2026-10-13T08:00:00Z", End: "2026-10-13T09:00:00Z"},
		Conflicts: []InvitationConflict{{Title: "Standup", Start: t0, End: t1, CalendarID: "cal_1", EventID: "ev_2"}},
		Day:       InvitationDay{From: t0, To: t1, Busy: []DayBusy{{Start: t0, End: t1, CalendarID: "cal_1"}}},
		Targets:   []InvitationTarget{{CalendarID: "cal_1", IsDefault: true}}, FixedTarget: true, CanRespond: true,
		CanProposeNewTime: true, ReplyVia: InvitationsViaServer, ReplyAccountID: "acc_1", ReplyTo: addr, ReplyApplied: true}
	avail := Availability{Address: "jana@example.org", Known: true, Busy: []BusyInterval{{Start: t0, End: t1, ShowAs: ShowAsOOF}}}
	alert := EventAlert{AlertID: "al_1", State: AlertDismissed, EventID: "ev_1", RecurrenceID: "2026-10-14", AccountID: "acc_1",
		CalendarID: "cal_1", Title: "Standup", Start: &t0, End: &t1, StartDate: "2026-10-14", AllDay: true, Location: "B",
		VideoCall: true, MinutesBefore: 15}
	prefs := DefaultCalendarPreferences()
	prefs.ShowDeclined = true
	sent := InvitationsSent{Via: InvitationsViaMail, Count: 3}

	roundTrip(t, "account config", AccountConfig{Name: "Cal", Email: "jana@example.org", Kind: AccountCalDAV,
		CalDAV: &CalDAVConfig{ServerURL: "https://cloud.example.org/remote.php/dav", PrincipalURL: "https://p/",
			HomeSetURL: "https://h/", Username: "jana", CertificateSHA256: strings.Repeat("a", 64), MailAccountID: "acc_2",
			Scheduling: CalDAVSchedulingClient},
		Calendar: &CalendarConfig{Sync: true, DisabledCalendars: []string{"/a/"}, DefaultCalendar: "/b/", KeepDays: 730,
			ShowUnanswered: Ptr(false), InvitationCard: Ptr(true), AfterAnswer: AfterAnswerArchive}})
	roundTrip(t, "test result", AccountTestResult{CalDAV: &EndpointTestResult{OK: true, Capabilities: []string{"calendar-access"}}})
	roundTrip(t, "oauth start", AccountOAuthStartParams{AccountID: "acc_1", Calendar: true})
	roundTrip(t, "discover params", AccountDiscoverCalDAVParams{URL: "cloud.example.org", CertificateSHA256: strings.Repeat("b", 64)})
	if got := roundTrip(t, "discover params minimal", AccountDiscoverCalDAVParams{URL: "x"}); got != `{"url":"x"}` {
		t.Fatalf("discover params %s", got)
	}
	roundTrip(t, "discover result", AccountDiscoverCalDAVResult{ServerURL: "https://x/dav", FoundVia: CalDAVFoundWellKnown,
		NeedsCredentials: true, Certificate: &CertificateInfo{SHA256: strings.Repeat("0", 64), Issuer: "R11", NotBefore: t0, NotAfter: t1}})
	roundTrip(t, "list caldav params", AccountListCalDAVCalendarsParams{AccountID: "acc_1",
		Config: AccountConfig{Kind: AccountCalDAV}, Credentials: Credentials{Password: "p"}})
	roundTrip(t, "list caldav result", AccountListCalDAVCalendarsResult{PrincipalURL: "https://p/", HomeSetURL: "https://h/",
		DisplayName: "Jana", Addresses: []string{"jana@example.org"}, Scheduling: CalDAVSchedulingServer,
		Calendars: []CalDAVCalendar{{Href: "/c/", Name: "Personal", Color: "#0082c9", Access: CalendarAccessWrite, IsDefault: true}}})
	roundTrip(t, "calendar list params", CalendarListParams{AccountID: "acc_1"})
	roundTrip(t, "calendar list", CalendarListResult{Unanswered: 3, Accounts: []CalendarAccount{{AccountID: "acc_1",
		Source: CalendarSourceGraph, Sync: true, State: state,
		Features: CalendarFeatures{FreeBusy: true, ProposeNewTime: true, Invitations: InvitationsViaMail, MailAccountID: "acc_2", OnlineMeetings: true},
		Window:   CalendarWindow{From: "2025-10-14", To: "2027-11-18"}, Calendars: []Calendar{cal}}}})
	roundTrip(t, "calendar update", CalendarUpdateParams{CalendarID: "cal_1", Visible: Ptr(false), UserColor: Ptr("")})
	roundTrip(t, "calendar update result", CalendarUpdateResult{Calendar: cal})
	roundTrip(t, "calendar sync", CalendarSyncParams{AccountID: "acc_1", CalendarID: "cal_1"})
	roundTrip(t, "preferences", CalendarPreferencesResult{Preferences: prefs})
	roundTrip(t, "set preferences", CalendarSetPreferencesParams{Preferences: CalendarPreferencesUpdate{
		WeekStart: Ptr(WeekStartMonday), DefaultDurationMinutes: Ptr(30), DefaultAlertMinutes: Ptr(-1),
		ShowDeclined: Ptr(false), WorkingHours: &WorkingHours{Days: []string{"MO"}, Start: "09:00", End: "17:00"}}})
	roundTrip(t, "set preferences result", CalendarSetPreferencesResult{Preferences: prefs})
	roundTrip(t, "free busy", CalendarFreeBusyParams{AccountID: "acc_1", Attendees: []string{"jana@example.org"}, From: t0, To: t1, IntervalMinutes: 15})
	roundTrip(t, "free busy result", CalendarFreeBusyResult{Me: avail, Attendees: []Availability{{Address: "x@y", Known: false, Busy: []BusyInterval{}}}})
	roundTrip(t, "find free", CalendarFindFreeTimeParams{AccountID: "acc_1", Attendees: []string{"a@b"}, DurationMinutes: 30,
		From: t0, To: t1, WorkingHoursOnly: Ptr(false), Limit: 5})
	roundTrip(t, "find free result", CalendarFindFreeTimeResult{Slots: []TimeSlot{{Start: t0, End: t1}}, Unknown: []string{"a@b"}})
	roundTrip(t, "event list", EventListParams{From: t0, To: t1, TimeZone: "Europe/Prague", CalendarIDs: []CalendarID{"cal_1"},
		AccountIDs: []AccountID{"acc_1"}, IncludeHidden: true, IncludeDeclined: Ptr(true), IncludeInvitations: Ptr(false), Limit: 10})
	roundTrip(t, "event list result", EventListResult{Occurrences: []Occurrence{occ}, Truncated: true, Complete: true})
	roundTrip(t, "event get", EventGetParams{EventID: "ev_1", RecurrenceID: "2026-10-14", TimeZone: "UTC"})
	roundTrip(t, "event get result", EventGetResult{Event: ev})
	roundTrip(t, "event search", EventSearchParams{Query: "supplier", From: &t0, To: &t1, CalendarIDs: []CalendarID{"cal_1"}, Limit: 50})
	roundTrip(t, "event search result", EventSearchResult{Occurrences: []Occurrence{occ}, Total: 12})
	roundTrip(t, "event create", EventCreateParams{CalendarID: "cal_1", Event: input, SendInvitations: Ptr(false), Mail: mail})
	roundTrip(t, "event create result", EventCreateResult{Event: ev, Invitations: sent})
	roundTrip(t, "event update", EventUpdateParams{EventID: "ev_1", RecurrenceID: "2026-10-14T08:00:00Z", Scope: EditScopeThisAndFuture,
		ETag: "e", Event: input, SendUpdates: Ptr(true), Mail: mail})
	roundTrip(t, "event update result", EventUpdateResult{Event: ev, DroppedExceptions: 2, Invitations: sent})
	roundTrip(t, "event delete", EventDeleteParams{EventID: "ev_1", RecurrenceID: "2026-10-14", Scope: EditScopeAll, ETag: "e",
		Notify: Ptr(false), Comment: "sorry", Mail: mail})
	roundTrip(t, "event delete result", EventDeleteResult{Invitations: sent})
	roundTrip(t, "event respond", EventRespondParams{EventID: "ev_1", RecurrenceID: "2026-10-14", Scope: EditScopeThis, ETag: "e",
		Response: ResponseDeclined, SendResponse: Ptr(false), Comment: "c", ProposedStart: &t0, ProposedEnd: &t1, Mail: mail})
	roundTrip(t, "event respond result", EventRespondResult{Event: ev, Via: InvitationsViaNone})
	roundTrip(t, "snooze", EventSnoozeAlertParams{AlertID: "al_1", Minutes: 5})
	roundTrip(t, "dismiss", EventDismissAlertParams{AlertID: "al_1"})
	roundTrip(t, "invitation list", InvitationListParams{AccountIDs: []AccountID{"acc_1"}, State: InvitationUnanswered, Limit: 200})
	roundTrip(t, "invitation list result", InvitationListResult{Invitations: []InvitationListItem{{AccountID: "acc_1", MessageID: "m_1", Summary: sum}}})
	roundTrip(t, "invitation respond", InvitationRespondParams{AccountID: "acc_1", MessageID: "m_1", Response: ResponseAccepted,
		CalendarID: "cal_1", SendResponse: Ptr(true), Comment: "c", ProposedStart: &t0, ProposedEnd: &t1, Mail: mail})
	roundTrip(t, "invitation respond result", InvitationRespondResult{Invitation: card, EventID: "ev_1", MovedTo: "f_archive"})
	roundTrip(t, "apply cancel", InvitationApplyCancelParams{AccountID: "acc_1", MessageID: "m_1"})
	roundTrip(t, "apply cancel result", InvitationApplyCancelResult{Removed: true})
	roundTrip(t, "message", Message{MessageSummary: MessageSummary{ID: "m_1"}, Invitation: &card})
	roundTrip(t, "calendar changed", CalendarChangedNotification{AccountIDs: []AccountID{"acc_1"}, CalendarIDs: []CalendarID{"cal_1"}, From: &t0, To: &t1})
	roundTrip(t, "calendar state", CalendarStateNotification{State: state})
	roundTrip(t, "event alert", EventAlertNotification{Alert: alert})
	roundTrip(t, "invitations changed", InvitationsChangedNotification{AccountIDs: []AccountID{"acc_1"}})
	roundTrip(t, "conflict data", ConflictEventData{Event: ev})
}

func TestCalendarRemoteIDAlwaysSent(t *testing.T) {
	got := roundTrip(t, "calendar", Calendar{ID: "cal_1", AccountID: "acc_1", Name: "C", Color: "#000000"})
	if !strings.Contains(got, `"remoteId":""`) || strings.Contains(got, `"disabled"`) {
		t.Fatalf("calendar %s", got)
	}
}
