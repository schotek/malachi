// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

func mainCal(remote string) calsync.RemoteCalendar {
	return calsync.RemoteCalendar{RemoteID: remote, Name: remote, Access: api.CalendarAccessOwner, Kind: api.CalendarKindEvents}
}

// seedInvitation stores an invitation message of a new IMAP account in
// a folder of the role (an unanswered request of uid at start).
func seedInvitation(t *testing.T, b *Backend, email string, role api.FolderRole, uid, title string, start time.Time) (string, string) {
	t.Helper()
	ctx := context.Background()
	acc := seedAccount(t, b, email)
	folders := seedFolders(t, b, acc, []store.Folder{{Mailbox: "Box", Name: "Box", Path: "Box", Role: role, Subscribed: true, Selectable: true}})
	m := &store.Message{AccountID: acc, FolderID: folders["Box"].ID, UID: 1, Subject: "Invitation: " + title, Date: time.Now(),
		RFCMessageID: "<" + uid + "@x>"}
	if err := b.store.UpsertMessages(ctx, []*store.Message{m}); err != nil {
		t.Fatal(err)
	}
	if err := b.store.PutInvitation(ctx, store.Invitation{MessageID: m.ID, AccountID: acc, PartID: "2", Method: api.InvitationMethodRequest,
		UID: uid, Organizer: "boss@example.test", StartAt: start, EndAt: start.Add(time.Hour), Title: title,
		Card: []byte(`{"title":"` + title + `","location":"Room 1"}`), ParserVersion: 1}); err != nil {
		t.Fatal(err)
	}
	return acc, m.ID
}

func listAll(t *testing.T, b *Backend, p api.EventListParams) *api.EventListResult {
	t.Helper()
	if p.TimeZone == "" {
		p.TimeZone = "Europe/Prague"
	}
	res, err := b.Events().List(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func titles(occs []api.Occurrence) []string {
	out := make([]string, len(occs))
	for i, o := range occs {
		out[i] = o.Title
	}
	return out
}

func TestEventList(t *testing.T) {
	h := newCalHarness(t)
	ctx, b := h.ctx, h.b
	d := day0().AddDate(0, 0, 3)
	g, gf := h.account(api.AccountGraph, "g@example.test", func(c *api.AccountConfig) { c.Calendar.ShowUnanswered = ptr(false) },
		mainCal("main"), mainCal("other"))
	allDay := calendar.Event{UID: "ad", Kind: calendar.KindSingle, Title: "Holiday", AllDay: true, Start: d, End: d.AddDate(0, 0, 1),
		StartDate: api.Date(d.Format(time.DateOnly)), EndDate: api.Date(d.AddDate(0, 0, 1).Format(time.DateOnly)),
		Status: api.EventStatusConfirmed, ShowAs: api.ShowAsFree, RecurOK: true}
	daily := timed("daily", "Standup", d.Add(8*time.Hour), 15*time.Minute)
	daily.Kind, daily.RRule = calendar.KindMaster, "FREQ=DAILY;COUNT=5"
	private := timed("priv", "Secret", d.Add(12*time.Hour), time.Hour)
	private.Private = true
	gf.put("main", obj("o1", "e", timed("late", "Late", d.Add(20*time.Hour), time.Hour)))
	gf.put("main", obj("o2", "e", allDay))
	gf.put("main", obj("o3", "e", daily))
	gf.put("main", obj("o4", "e", invitedTo(timed("decl", "Declined", d.Add(10*time.Hour), time.Hour), api.ResponseDeclined)))
	gf.put("main", obj("o5", "e", invitedTo(timed("ask", "Asked", d.Add(11*time.Hour), time.Hour), api.ResponseNeedsAction)))
	gf.put("other", obj("o6", "e", private))
	h.start(g)
	other := api.CalendarID(h.calendarID(g, "other"))

	from, to := d, d.AddDate(0, 0, 1)
	res := listAll(t, b, api.EventListParams{From: from, To: to, TimeZone: "UTC"})
	if got := titles(res.Occurrences); !slices.Equal(got, []string{"Holiday", "Standup", "Secret", "Late"}) || !res.Complete || res.Truncated {
		t.Fatalf("list %v %+v", got, res)
	}
	ad := res.Occurrences[0]
	if !ad.AllDay || ad.StartDate != api.Date(d.Format(time.DateOnly)) || ad.Start != nil || ad.ShowAs != api.ShowAsFree {
		t.Errorf("all-day %+v", ad)
	}
	st := res.Occurrences[1]
	if !st.Recurring || st.RecurrenceID == "" || st.SeriesID == "" || st.EventID == "" || st.TimeZone != "UTC" || st.CalendarID == "" {
		t.Errorf("series instance %+v", st)
	}
	// The whole series in a week; the declined one with the flag (or the preference).
	week := listAll(t, b, api.EventListParams{From: from, To: from.AddDate(0, 0, 7), TimeZone: "UTC"})
	n := 0
	for _, o := range week.Occurrences {
		if o.Title == "Standup" {
			n++
		}
	}
	if n != 5 {
		t.Errorf("standups %d", n)
	}
	withDecl := listAll(t, b, api.EventListParams{From: from, To: to, TimeZone: "UTC", IncludeDeclined: ptr(true)})
	if !slices.Contains(titles(withDecl.Occurrences), "Declined") {
		t.Errorf("declined not included: %v", titles(withDecl.Occurrences))
	}
	if _, err := b.Calendars().SetPreferences(ctx, api.CalendarSetPreferencesParams{Preferences: api.CalendarPreferencesUpdate{ShowDeclined: ptr(true)}}); err != nil {
		t.Fatal(err)
	}
	if got := titles(listAll(t, b, api.EventListParams{From: from, To: to}).Occurrences); !slices.Contains(got, "Declined") {
		t.Errorf("preference showDeclined: %v", got)
	}
	// needsAction hidden while the account's showUnanswered is off.
	if slices.Contains(titles(withDecl.Occurrences), "Asked") {
		t.Errorf("needsAction shown: %v", titles(withDecl.Occurrences))
	}

	// Hidden calendars: out unless asked.
	if _, err := b.Calendars().Update(ctx, api.CalendarUpdateParams{CalendarID: other, Visible: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if got := titles(listAll(t, b, api.EventListParams{From: from, To: to}).Occurrences); slices.Contains(got, "Secret") {
		t.Errorf("hidden shown: %v", got)
	}
	if got := titles(listAll(t, b, api.EventListParams{From: from, To: to, IncludeHidden: true}).Occurrences); !slices.Contains(got, "Secret") {
		t.Errorf("includeHidden: %v", got)
	}
	if got := titles(listAll(t, b, api.EventListParams{From: from, To: to, CalendarIDs: []api.CalendarID{other}}).Occurrences); !slices.Equal(got, []string{"Secret"}) {
		t.Errorf("calendarIds: %v", got)
	}
	// Limit.
	lim := listAll(t, b, api.EventListParams{From: from, To: to, Limit: 2, IncludeHidden: true})
	if len(lim.Occurrences) != 2 || !lim.Truncated {
		t.Errorf("limit %+v", lim)
	}

	for name, p := range map[string]api.EventListParams{
		"reversed": {From: to, To: from, TimeZone: "UTC"},
		"tooLong":  {From: from, To: from.AddDate(0, 0, api.MaxEventListDays+1), TimeZone: "UTC"},
		"zone":     {From: from, To: to, TimeZone: "Central Europe Standard Time"},
		"zone2":    {From: from, To: to, TimeZone: "Mars/Olympus"},
		"calendar": {From: from, To: to, TimeZone: "UTC", CalendarIDs: []api.CalendarID{"cal_none"}},
		"account":  {From: from, To: to, TimeZone: "UTC", AccountIDs: []api.AccountID{"acc_none"}},
		"limit":    {From: from, To: to, TimeZone: "UTC", Limit: api.MaxEventListResults + 1},
	} {
		if _, err := b.Events().List(ctx, p); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}

	// A paused account contributes nothing.
	if err := b.store.SetAccountEnabled(ctx, g.ID, false); err != nil {
		t.Fatal(err)
	}
	if res := listAll(t, b, api.EventListParams{From: from, To: to}); len(res.Occurrences) != 0 {
		t.Errorf("paused account listed %v", titles(res.Occurrences))
	}
}

func TestEventListInvitationsAndOutsideWindow(t *testing.T) {
	h := newCalHarness(t)
	ctx, b := h.ctx, h.b
	d := day0().AddDate(0, 0, 2)
	g, gf := h.account(api.AccountCalDAV, "g@example.test", func(c *api.AccountConfig) { c.Calendar.KeepDays = 10 }, mainCal("main"))
	gf.put("main", obj("o1", "e", timed("known", "Known", d.Add(9*time.Hour), time.Hour)))
	old := day0().AddDate(0, 0, -30)
	gf.put("main", obj("o2", "e", timed("old", "Old", old.Add(9*time.Hour), time.Hour)))
	h.start(g)

	_, inviteMsg := seedInvitation(t, b, "mail@example.test", api.RoleInbox, "inv-1", "Planning", d.Add(14*time.Hour))
	seedInvitation(t, b, "junk@example.test", api.RoleJunk, "inv-2", "Spam meeting", d.Add(15*time.Hour))
	seedInvitation(t, b, "mail2@example.test", api.RoleInbox, "known", "Known again", d.Add(9*time.Hour))
	res := listAll(t, b, api.EventListParams{From: d, To: d.AddDate(0, 0, 1), TimeZone: "UTC"})
	if got := titles(res.Occurrences); !slices.Equal(got, []string{"Known", "Planning"}) {
		t.Fatalf("with invitations %v", got)
	}
	inv := res.Occurrences[1]
	if inv.Invitation == nil || string(inv.Invitation.MessageID) != inviteMsg || inv.MyResponse != api.ResponseNeedsAction ||
		inv.EventID != "" || inv.Location != "Room 1" || inv.ShowAs != api.ShowAsTentative {
		t.Errorf("invitation %+v", inv)
	}
	if got := titles(listAll(t, b, api.EventListParams{From: d, To: d.AddDate(0, 0, 1), IncludeInvitations: ptr(false)}).Occurrences); !slices.Equal(got, []string{"Known"}) {
		t.Errorf("without invitations %v", got)
	}
	if cl, err := b.Calendars().List(ctx, api.CalendarListParams{}); err != nil || cl.Unanswered != 3 {
		t.Errorf("unanswered %+v %v", cl, err)
	}

	// Outside the window (keepDays 10): fetched from the source once.
	if evs := h.events(g); len(evs) != 1 {
		t.Fatalf("stored %d", len(evs))
	}
	res = listAll(t, b, api.EventListParams{From: old, To: old.AddDate(0, 0, 1), TimeZone: "UTC"})
	if got := titles(res.Occurrences); !res.Complete || len(got) != 1 {
		t.Errorf("outside %v %+v", got, res)
	}
	if len(h.events(g)) != 1 {
		t.Error("outside occurrence stored")
	}
	gf.rangeErr = api.NewError(api.CodeOffline, "offline")
	res = listAll(t, b, api.EventListParams{From: old, To: d.AddDate(0, 0, 1), TimeZone: "UTC", IncludeInvitations: ptr(false)})
	if res.Complete || !slices.Equal(titles(res.Occurrences), []string{"Known"}) {
		t.Errorf("offline outside %v %+v", titles(res.Occurrences), res)
	}
	b.calSync.Stop(g.ID)
	gf.rangeErr = nil
	if res = listAll(t, b, api.EventListParams{From: old, To: old.AddDate(0, 0, 1)}); res.Complete {
		t.Errorf("no provider: %+v", res)
	}
}

func TestEventGetAndSearch(t *testing.T) {
	h := newCalHarness(t)
	ctx, b := h.ctx, h.b
	d := day0().AddDate(0, 0, 1)
	g, gf := h.account(api.AccountGraph, calMe, nil, mainCal("main"),
		calsync.RemoteCalendar{RemoteID: "shared", Name: "Shared", Access: api.CalendarAccessRead, Kind: api.CalendarKindEvents,
			Owner: &api.Address{Name: "Petr", Address: "petr@example.test"}})
	gf.feats.ProposeNewTime = true
	single := timed("single", "Supplier call", d.Add(9*time.Hour), time.Hour)
	single.Notes, single.Location = "Agenda\nhttps://example.test/doc", "Room B"
	single.Links = []string{"https://example.test/doc", "javascript:alert(1)"}
	single.VideoCall = &api.VideoCall{URL: "https://teams.microsoft.com/l/meetup-join/1", Provider: api.VideoProviderTeams}
	single.Alarms = []int{10}
	weekly := timed("weekly", "Supplier sync", d.Add(-7*24*time.Hour+10*time.Hour), time.Hour)
	weekly.Kind, weekly.RRule = calendar.KindMaster, "FREQ=WEEKLY;COUNT=4"
	weekly.Recurrence = &api.Recurrence{Frequency: api.FrequencyWeekly, Interval: 1, Count: 4}
	weekly = invitedTo(weekly, api.ResponseAccepted)
	weekly.NewTimeOK = true
	moved := weekly
	moved.Kind, moved.RRule, moved.Recurrence = calendar.KindOverride, "", nil
	moved.RecurrenceID = api.DateOrTime(d.Add(10 * time.Hour).Format(time.RFC3339))
	moved.Start, moved.End = d.Add(15*time.Hour), d.Add(16*time.Hour)
	moved.Title = "Supplier sync (moved)"
	past := timed("past", "Supplier retro", day0().AddDate(0, 0, -20), time.Hour)
	secret := timed("secret", "Supplier secret", d.Add(18*time.Hour), time.Hour)
	secret.Private = true
	gf.put("main", obj("o1", "etag-1", single))
	gf.put("main", obj("o2", "etag-2", weekly, moved))
	gf.put("main", obj("o3", "etag-3", past))
	gf.put("shared", obj("o4", "etag-4", secret))
	h.start(g)

	id := h.eventID(g, "single")
	got, err := b.Events().Get(ctx, api.EventGetParams{EventID: api.EventID(id)})
	if err != nil {
		t.Fatal(err)
	}
	e := got.Event
	if e.ETag != "etag-1" || e.Title != "Supplier call" || e.Notes == "" || len(e.Links) != 1 || e.VideoCallLink == nil ||
		e.VideoCallLink.Host != "teams.microsoft.com" || !e.CanEdit || !e.CanDelete || e.CanRespond || !e.RemoveIsDelete ||
		!slices.Equal(e.Alerts, []int{10}) || !e.Alert {
		t.Errorf("single %+v", e)
	}

	series := h.eventID(g, "weekly")
	got, err = b.Events().Get(ctx, api.EventGetParams{EventID: api.EventID(series)})
	if err != nil {
		t.Fatal(err)
	}
	if e := got.Event; e.Recurrence == nil || e.Recurrence.Count != 4 || !e.Recurring || !e.CanRespond || e.RemoveIsDelete ||
		!e.CanProposeNewTime || e.OrganizerAddress == nil || len(e.Attendees) != 2 || e.Start == nil || !e.Start.Equal(weekly.Start) {
		t.Errorf("series master %+v", e)
	}
	got, err = b.Events().Get(ctx, api.EventGetParams{EventID: api.EventID(series), RecurrenceID: moved.RecurrenceID})
	if err != nil {
		t.Fatal(err)
	}
	if e := got.Event; e.Title != "Supplier sync (moved)" || e.Recurrence == nil || e.SeriesID != api.EventID(series) ||
		e.EventID == api.EventID(series) || !e.Start.Equal(moved.Start) {
		t.Errorf("override %+v", e)
	}
	third := api.DateOrTime(d.Add(7*24*time.Hour + 10*time.Hour).Format(time.RFC3339))
	if got, err = b.Events().Get(ctx, api.EventGetParams{EventID: api.EventID(series), RecurrenceID: third}); err != nil ||
		got.Event.EventID != api.EventID(series) || !got.Event.Start.Equal(d.Add(7*24*time.Hour+10*time.Hour)) {
		t.Errorf("plain instance %+v %v", got, err)
	}
	if _, err := b.Events().Get(ctx, api.EventGetParams{EventID: api.EventID(series), RecurrenceID: api.DateOrTime(d.Add(11 * time.Hour).Format(time.RFC3339))}); errCode(t, err) != api.CodeEventNotFound {
		t.Errorf("no such instance: %v", err)
	}
	if _, err := b.Events().Get(ctx, api.EventGetParams{EventID: "ev_none"}); errCode(t, err) != api.CodeEventNotFound {
		t.Errorf("unknown: %v", err)
	}
	got, err = b.Events().Get(ctx, api.EventGetParams{EventID: api.EventID(h.eventID(g, "secret"))})
	if err != nil {
		t.Fatal(err)
	}
	if e := got.Event; e.Title != "" || !e.ReadOnly || e.CanEdit || e.CanDelete || !e.Private {
		t.Errorf("private of a shared read-only calendar %+v", e)
	}

	// Search: upcoming by start, then past latest first; one per event.
	sr, err := b.Events().Search(ctx, api.EventSearchParams{Query: "suppli"})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(sr.Occurrences); sr.Total != 4 || len(got) != 4 || got[0] != "Supplier call" || got[3] != "Supplier retro" {
		t.Errorf("search %v total %d", got, sr.Total)
	}
	from := d.Add(12 * time.Hour)
	sr, err = b.Events().Search(ctx, api.EventSearchParams{Query: "supplier", From: &from, Limit: 1})
	if err != nil || sr.Total != 2 || len(sr.Occurrences) != 1 {
		t.Errorf("search from %+v %v", sr, err)
	}
	for name, q := range map[string]api.EventSearchParams{
		"empty": {Query: "  "}, "long": {Query: strings.Repeat("a", 257)}, "limit": {Query: "x", Limit: 201},
		"calendar": {Query: "x", CalendarIDs: []api.CalendarID{"cal_none"}},
	} {
		if _, err := b.Events().Search(ctx, q); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestEventCreate(t *testing.T) {
	h := newCalHarness(t)
	ctx, b := h.ctx, h.b
	d := day0().AddDate(0, 0, 2)
	g, gf := h.account(api.AccountGraph, calMe, nil, mainCal("main"),
		calsync.RemoteCalendar{RemoteID: "ro", Name: "RO", Access: api.CalendarAccessRead, Kind: api.CalendarKindEvents})
	h.start(g)
	cal := api.CalendarID(h.calendarID(g, "main"))
	in := api.EventInput{Title: "Review", Start: api.LocalDateTime(d.Format("2006-01-02") + "T10:00:00"),
		End: api.LocalDateTime(d.Format("2006-01-02") + "T11:00:00"), TimeZone: "Europe/Prague",
		Attendees: []api.EventInputAttendee{{Address: "jana@example.test"}}}
	res, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: cal, Event: in})
	if err != nil {
		t.Fatal(err)
	}
	e := res.Event
	if e.EventID == "" || e.Title != "Review" || e.ETag == "" || !e.Organizer || len(e.Attendees) != 1 ||
		!slices.Equal(e.Alerts, []int{15}) || res.Invitations.Via != api.InvitationsViaServer || res.Invitations.Count != 1 {
		t.Errorf("created %+v %+v", e, res.Invitations)
	}
	if o := gf.lastOpts(); !o.Notify {
		t.Errorf("opts %+v", o)
	}
	if got, err := b.Events().Get(ctx, api.EventGetParams{EventID: e.EventID}); err != nil || got.Event.Title != "Review" {
		t.Errorf("stored %+v %v", got, err)
	}

	// Graph sends invitations always; readOnly; validation; video calls.
	if _, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: cal, Event: in, SendInvitations: ptr(false)}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("graph without invitations: %v", err)
	}
	if _, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: api.CalendarID(h.calendarID(g, "ro")), Event: in}); errCode(t, err) != api.CodeReadOnly {
		t.Errorf("read-only: %v", err)
	}
	if _, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: "cal_none", Event: in}); errCode(t, err) != api.CodeCalendarNotFound {
		t.Errorf("unknown calendar: %v", err)
	}
	bad := in
	bad.TimeZone = ""
	if _, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: cal, Event: bad}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("no zone: %v", err)
	}
	if _, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: cal, Event: in, Mail: &api.MailText{Text: strings.Repeat("x", api.MaxMailTextBytes+1)}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("long mail: %v", err)
	}
	vc := in
	vc.VideoCall = &api.VideoCallInput{Mode: api.VideoCallCreate, Provider: "teamsForBusiness"}
	if _, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: cal, Event: vc}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("video call without onlineMeetings: %v", err)
	}

	// Attachments: by attachment.import id, ≤ 10, the server's errors through.
	imp, err := b.Attachments().Import(ctx, api.AttachmentImportParams{AccountID: api.AccountID(g.ID), Data: []byte("agenda"), Filename: "agenda.txt"})
	if err != nil {
		t.Fatal(err)
	}
	withFile := in
	withFile.Attachments = []string{string(imp.Attachment.ID)}
	if _, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: cal, Event: withFile}); err != nil {
		t.Fatal(err)
	}
	if o := gf.lastOpts(); len(o.Attachments) != 1 || string(o.Attachments[0].Data) != "agenda" || o.Attachments[0].Name != "agenda.txt" {
		t.Errorf("attachments %+v", o.Attachments)
	}
	withFile.Attachments = []string{"att_none"}
	if _, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: cal, Event: withFile}); errCode(t, err) != api.CodeAttachmentNotFound {
		t.Errorf("unknown attachment: %v", err)
	}
	withFile.Attachments = make([]string, 11)
	if _, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: cal, Event: withFile}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("11 attachments: %v", err)
	}
	gf.writeErr = api.NewError(api.CodeOffline, "offline")
	if _, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: cal, Event: in}); errCode(t, err) != api.CodeOffline {
		t.Errorf("offline: %v", err)
	}
}

// The iMIP of client scheduling must fit the outbox (§4.8): too big is
// attachmentTooBig before the server is asked.
func TestEventCreateIMIPSize(t *testing.T) {
	h := newCalHarness(t)
	ctx, b := h.ctx, h.b
	d := day0().AddDate(0, 0, 2)
	c, cf := h.account(api.AccountCalDAV, calMe, nil, mainCal("main"))
	cf.feats = api.CalendarFeatures{Invitations: api.InvitationsViaMail, MailAccountID: "acc_mail"}
	h.start(c)
	imp, err := b.Attachments().Import(ctx, api.AttachmentImportParams{AccountID: api.AccountID(c.ID), Data: []byte(strings.Repeat("z", 64<<10)), Filename: "big.bin"})
	if err != nil {
		t.Fatal(err)
	}
	old := outgoingLimit
	outgoingLimit = 64 << 10
	t.Cleanup(func() { outgoingLimit = old })
	in := api.EventInput{Title: "Big", Start: api.LocalDateTime(d.Format("2006-01-02") + "T10:00"), End: api.LocalDateTime(d.Format("2006-01-02") + "T11:00"),
		TimeZone: "UTC", Attendees: []api.EventInputAttendee{{Address: "jana@example.test"}}, Attachments: []string{string(imp.Attachment.ID)}}
	if _, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: api.CalendarID(h.calendarID(c, "main")), Event: in}); errCode(t, err) != api.CodeAttachmentTooBig {
		t.Fatalf("too big: %v", err)
	}
	if len(cf.recorded()) != 0 {
		t.Errorf("the server was asked: %v", cf.recorded())
	}
	// Without attendees no mail: the file goes.
	in.Attendees = nil
	if _, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: api.CalendarID(h.calendarID(c, "main")), Event: in}); err != nil {
		t.Errorf("no attendees: %v", err)
	}
}

func TestEventUpdateScopesAndRules(t *testing.T) {
	h := newCalHarness(t)
	ctx, b := h.ctx, h.b
	d := day0().AddDate(0, 0, 1)
	c, cf := h.account(api.AccountCalDAV, calMe, nil, mainCal("main"))
	single := timed("single", "One", d.Add(9*time.Hour), time.Hour)
	daily := timed("daily", "Daily", d.Add(8*time.Hour), 30*time.Minute)
	daily.Kind, daily.RRule = calendar.KindMaster, "FREQ=DAILY;COUNT=5"
	daily.Recurrence = &api.Recurrence{Frequency: api.FrequencyDaily, Interval: 1, Count: 5}
	invited := invitedTo(timed("inv", "Their meeting", d.Add(13*time.Hour), time.Hour), api.ResponseAccepted)
	cf.put("main", obj("o1", "e-1", single))
	cf.put("main", obj("o2", "e-2", daily))
	cf.put("main", obj("o3", "e-3", invited))
	h.start(c)
	day := d.Format("2006-01-02")
	input := func(title, start, end string) api.EventInput {
		return api.EventInput{Title: title, Start: api.LocalDateTime(day + "T" + start), End: api.LocalDateTime(day + "T" + end), TimeZone: "UTC", Alerts: []int{}}
	}

	// A single event: scope all; this is refused; the ETag is the server's.
	one := api.EventID(h.eventID(c, "single"))
	res, err := b.Events().Update(ctx, api.EventUpdateParams{EventID: one, Scope: api.EditScopeAll, ETag: "e-1", Event: input("One more", "09:00", "10:30")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Event.Title != "One more" || res.Event.EventID != one || res.Event.ETag == "e-1" || !res.Event.End.Equal(d.Add(10*time.Hour+30*time.Minute)) {
		t.Errorf("updated %+v", res.Event)
	}
	if _, err := b.Events().Update(ctx, api.EventUpdateParams{EventID: one, Scope: api.EditScopeThis, ETag: res.Event.ETag, Event: input("x", "09:00", "10:00")}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("this of a single: %v", err)
	}
	// Conflict: the current event comes in the data.
	_, err = b.Events().Update(ctx, api.EventUpdateParams{EventID: one, Scope: api.EditScopeAll, ETag: "e-1", Event: input("Stale", "09:00", "10:00")})
	if errCode(t, err) != api.CodeConflict {
		t.Fatalf("stale etag: %v", err)
	}
	if data, ok := err.(*api.Error).Data.(api.ConflictEventData); !ok || data.Event.Title != "One more" || data.Event.EventID != one || data.Event.ETag != res.Event.ETag {
		t.Errorf("conflict data %+v", err.(*api.Error).Data)
	}
	if _, err := b.Events().Update(ctx, api.EventUpdateParams{EventID: one, Scope: api.EditScopeAll, Event: input("x", "09:00", "10:00")}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("no etag: %v", err)
	}
	cf.writeErr = api.NewError(api.CodeEventGone, "gone")
	if _, err := b.Events().Update(ctx, api.EventUpdateParams{EventID: one, Scope: api.EditScopeAll, ETag: res.Event.ETag, Event: input("x", "09:00", "10:00")}); errCode(t, err) != api.CodeEventGone {
		t.Errorf("gone: %v", err)
	}

	// A series: this makes an override, thisAndFuture splits.
	series := api.EventID(h.eventID(c, "daily"))
	second := api.DateOrTime(d.AddDate(0, 0, 1).Add(8 * time.Hour).Format(time.RFC3339))
	in := input("Daily (moved)", "08:00", "08:30")
	in.Start, in.End = api.LocalDateTime(d.AddDate(0, 0, 1).Format("2006-01-02")+"T09:00"), api.LocalDateTime(d.AddDate(0, 0, 1).Format("2006-01-02")+"T09:30")
	res, err = b.Events().Update(ctx, api.EventUpdateParams{EventID: series, RecurrenceID: second, Scope: api.EditScopeThis, ETag: "e-2", Event: in})
	if err != nil {
		t.Fatal(err)
	}
	if res.Event.Title != "Daily (moved)" || res.Event.SeriesID != series || !res.Event.Start.Equal(d.AddDate(0, 0, 1).Add(9*time.Hour)) {
		t.Errorf("this %+v", res.Event)
	}
	if _, err := b.Events().Update(ctx, api.EventUpdateParams{EventID: series, Scope: api.EditScopeThis, ETag: res.Event.ETag, Event: in}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("this without recurrenceId: %v", err)
	}
	third := d.AddDate(0, 0, 2)
	fut := input("Daily later", "08:00", "08:30")
	fut.Start, fut.End = api.LocalDateTime(third.Format("2006-01-02")+"T07:00"), api.LocalDateTime(third.Format("2006-01-02")+"T07:30")
	fut.Recurrence = &api.Recurrence{Frequency: api.FrequencyDaily, Interval: 1, Count: 3}
	res, err = b.Events().Update(ctx, api.EventUpdateParams{EventID: series, RecurrenceID: api.DateOrTime(third.Add(8 * time.Hour).Format(time.RFC3339)),
		Scope: api.EditScopeThisAndFuture, ETag: res.Event.ETag, Event: fut})
	if err != nil {
		t.Fatal(err)
	}
	if res.Event.Title != "Daily later" || res.Event.EventID == series || res.Event.EventID == "" {
		t.Errorf("split %+v", res.Event)
	}
	if calls := cf.recorded(); !slices.Contains(calls, "update:thisAndFuture") || !slices.Contains(calls, "update:this") {
		t.Errorf("calls %v", calls)
	}

	// An attendee changes only alerts, show as and private.
	inv := api.EventID(h.eventID(c, "inv"))
	theirs := input("Their meeting", "13:00", "14:00")
	theirs.Alerts, theirs.ShowAs = []int{5}, api.ShowAsFree
	res, err = b.Events().Update(ctx, api.EventUpdateParams{EventID: inv, Scope: api.EditScopeAll, ETag: "e-3", Event: theirs})
	if err != nil {
		t.Fatal(err)
	}
	if res.Event.ShowAs != api.ShowAsFree || !slices.Equal(res.Event.Alerts, []int{5}) || len(res.Event.Attendees) != 2 || res.Event.OrganizerAddress == nil {
		t.Errorf("attendee's change %+v", res.Event)
	}
	theirs.Title = "Renamed"
	if _, err := b.Events().Update(ctx, api.EventUpdateParams{EventID: inv, Scope: api.EditScopeAll, ETag: res.Event.ETag, Event: theirs}); errCode(t, err) != api.CodeReadOnly {
		t.Errorf("attendee renames: %v", err)
	}
}

func TestEventDeleteAndRespond(t *testing.T) {
	h := newCalHarness(t)
	ctx, b := h.ctx, h.b
	d := day0().AddDate(0, 0, 1)
	g, gf := h.account(api.AccountGraph, calMe, nil, mainCal("main"))
	gf.feats.ProposeNewTime = true
	mine := timed("mine", "Mine", d.Add(9*time.Hour), time.Hour)
	mine.Organizer, mine.IAmOrganizer = &api.Address{Address: calMe}, true
	mine.Attendees = []api.Attendee{{Address: "jana@example.test", Role: api.AttendeeRequired, Response: api.ResponseNeedsAction, Type: api.AttendeeTypePerson}}
	mine.AttendeeCount = 1
	theirs := invitedTo(timed("theirs", "Theirs", d.Add(11*time.Hour), time.Hour), api.ResponseNeedsAction)
	other := invitedTo(timed("other", "Other", d.Add(13*time.Hour), time.Hour), api.ResponseNeedsAction)
	gf.put("main", obj("o1", "e-1", mine))
	gf.put("main", obj("o2", "e-2", theirs))
	gf.put("main", obj("o3", "e-3", other))
	h.start(g)
	mailAcc, _ := seedInvitation(t, b, "mail@example.test", api.RoleInbox, "theirs", "Theirs", d.Add(11*time.Hour))

	// event.respond: not an attendee; a proposed time; the answer stored for the invitation.
	if _, err := b.Events().Respond(ctx, api.EventRespondParams{EventID: api.EventID(h.eventID(g, "mine")), Scope: api.EditScopeAll, ETag: "e-1",
		Response: api.ResponseAccepted}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("organiser responds: %v", err)
	}
	ps := d.Add(15 * time.Hour)
	pe := ps.Add(time.Hour)
	if _, err := b.Events().Respond(ctx, api.EventRespondParams{EventID: api.EventID(h.eventID(g, "theirs")), Scope: api.EditScopeAll, ETag: "e-2",
		Response: api.ResponseTentative, ProposedStart: &ps, ProposedEnd: &pe}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("proposal without allowNewTimeProposals: %v", err)
	}
	if _, err := b.Events().Respond(ctx, api.EventRespondParams{EventID: api.EventID(h.eventID(g, "theirs")), Scope: api.EditScopeAll, ETag: "e-2",
		Response: api.ResponseTentative, ProposedStart: &ps}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("one end of a proposal: %v", err)
	}
	rr, err := b.Events().Respond(ctx, api.EventRespondParams{EventID: api.EventID(h.eventID(g, "theirs")), Scope: api.EditScopeAll, ETag: "e-2",
		Response: api.ResponseAccepted, Comment: "See you"})
	if err != nil {
		t.Fatal(err)
	}
	if rr.Event.MyResponse != api.ResponseAccepted || rr.Via != api.InvitationsViaServer || gf.lastOpts().Comment != "See you" {
		t.Errorf("respond %+v %v", rr, gf.lastOpts())
	}
	if r, err := b.store.GetResponse(ctx, mailAcc, "theirs", ""); err != nil || r.Response != api.ResponseAccepted || r.Via != "graph" {
		t.Errorf("cal_responses %+v %v", r, err)
	}
	h.rec.mu.Lock()
	gotInv := len(h.rec.invitations)
	h.rec.mu.Unlock()
	if gotInv == 0 {
		t.Error("no invitationsChanged")
	}

	// event.delete: the organiser cancels unless notify is false.
	if _, err := b.Events().Delete(ctx, api.EventDeleteParams{EventID: api.EventID(h.eventID(g, "mine")), Scope: api.EditScopeAll, ETag: "e-1", Notify: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if o := gf.lastOpts(); o.Notify {
		t.Errorf("notify false sent: %+v", o)
	}
	if len(h.events(g)) != 2 {
		t.Errorf("deleted event still stored: %d", len(h.events(g)))
	}
	// An attendee with notify declines first, then removes the copy.
	before := len(gf.recorded())
	del, err := b.Events().Delete(ctx, api.EventDeleteParams{EventID: api.EventID(h.eventID(g, "other")), Scope: api.EditScopeAll, ETag: "e-3", Comment: "Sorry"})
	if err != nil {
		t.Fatal(err)
	}
	if calls := gf.recorded()[before:]; !slices.Equal(calls, []string{"respond:declined", "delete:all"}) {
		t.Errorf("attendee delete calls %v", calls)
	}
	if o := gf.lastOpts(); o.Notify {
		t.Errorf("attendee's removal notified: %+v", o)
	}
	if del.Invitations.Via != api.InvitationsViaServer {
		t.Errorf("attendee delete result %+v", del)
	}
	if _, err := b.Events().Delete(ctx, api.EventDeleteParams{EventID: "ev_none", Scope: api.EditScopeAll, ETag: "x"}); errCode(t, err) != api.CodeEventNotFound {
		t.Errorf("unknown: %v", err)
	}
	if _, err := b.Events().Delete(ctx, api.EventDeleteParams{EventID: api.EventID(h.eventID(g, "theirs")), Scope: "some", ETag: "x"}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("bad scope: %v", err)
	}
}

func TestEventAlerts(t *testing.T) {
	h := newCalHarness(t)
	ctx, b := h.ctx, h.b
	g, gf := h.account(api.AccountGraph, calMe, nil, mainCal("main"))
	soon := timed("soon", "Soon", time.Now().UTC().Add(5*time.Minute).Truncate(time.Second), time.Hour)
	soon.Alarms = []int{10}
	gf.put("main", obj("o1", "e", soon))
	h.start(g)
	var alertID api.AlertID
	waitLong(t, "the alert fired", func() bool {
		h.rec.mu.Lock()
		defer h.rec.mu.Unlock()
		for _, a := range h.rec.alerts {
			if a.State == api.AlertDue {
				alertID = a.AlertID
				return true
			}
		}
		return false
	})
	if _, err := b.Events().SnoozeAlert(ctx, api.EventSnoozeAlertParams{AlertID: alertID, Minutes: 0}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("0 minutes: %v", err)
	}
	if _, err := b.Events().SnoozeAlert(ctx, api.EventSnoozeAlertParams{AlertID: "al_none", Minutes: 5}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("unknown snooze: %v", err)
	}
	if _, err := b.Events().SnoozeAlert(ctx, api.EventSnoozeAlertParams{AlertID: alertID, Minutes: 5}); err != nil {
		t.Errorf("snooze: %v", err)
	}
	if al, err := b.store.GetAlert(ctx, string(alertID)); err != nil || al.SnoozedUntil.IsZero() {
		t.Errorf("snoozed %+v %v", al, err)
	}
	if _, err := b.Events().DismissAlert(ctx, api.EventDismissAlertParams{AlertID: alertID}); err != nil {
		t.Errorf("dismiss: %v", err)
	}
	if _, err := b.Events().DismissAlert(ctx, api.EventDismissAlertParams{AlertID: "al_none"}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("unknown dismiss: %v", err)
	}
	waitLong(t, "dismissed sent", func() bool {
		h.rec.mu.Lock()
		defer h.rec.mu.Unlock()
		for _, a := range h.rec.alerts {
			if a.State == api.AlertDismissed && a.AlertID == alertID {
				return true
			}
		}
		return false
	})
}

// calDAVEndToEnd is a backend with a mail account and a caldav account
// over the fake server srv, synchronising in the real engine.
func calDAVEndToEnd(t *testing.T, srv *caldavtest.Server, edit ...func(*api.AccountConfig)) (*Backend, string, store.Account) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	b := newTestBackend(t, config.Default())
	b.Keyring = newMemKeyring()
	b.CalDAVHTTP = srv.HTTPClient()
	b.CalDAVResolver = srv.Resolver()
	b.Supervisor = newKindSupervisor(map[api.AccountKind]SyncSupervisor{
		api.AccountIMAP: newFakeSupervisor(), api.AccountCalDAV: b.calSync.Mail(),
	})
	b.Delivery = newFakeOutbox()
	done := b.StartSync(ctx)
	t.Cleanup(func() { cancel(); <-done })
	mail, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: validConfig(), Credentials: api.Credentials{Password: "pw"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := calDAVConfig(srv.ServerURL())
	cfg.CalDAV.HomeSetURL = srv.HomeSetURL()
	cfg.CalDAV.MailAccountID = mail.AccountID
	for _, f := range edit {
		f(&cfg)
	}
	add, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{Password: caldavtest.Password}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := b.store.GetAccount(ctx, string(add.AccountID))
	if err != nil {
		t.Fatal(err)
	}
	waitLong(t, "the caldav provider", func() bool {
		cals, _ := b.store.ListCalendars(ctx, a.ID)
		st, ok := b.calendarEngine().CalendarState(a.ID)
		_, prov := b.calendarEngine().Provider(a.ID)
		return len(cals) > 0 && ok && prov && st.LastSync != nil
	})
	return b, string(mail.AccountID), a
}

func writableCalendar(t *testing.T, b *Backend, a store.Account) api.CalendarID {
	t.Helper()
	cals, err := b.store.ListCalendars(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cals {
		if !calendarReadOnly(c) {
			return api.CalendarID(c.ID)
		}
	}
	t.Fatal("no writable calendar")
	return ""
}

func outboxSubjects(t *testing.T, b *Backend, account string) []string {
	t.Helper()
	ctx := context.Background()
	entries, err := b.store.ListOutbox(ctx, account, store.OutboxQueued, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		m, err := b.store.GetMessage(ctx, account, e.MessageID)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m.Subject)
	}
	return out
}

// Client scheduling (Radicale advertises none): the REQUEST is queued in
// the outbox of the mail account with the client's subject; updates
// queue again; the server's newer version is a conflict with its event;
// the organiser's delete queues a CANCEL.
func TestEventCalDAVClientScheduling(t *testing.T) {
	ctx := context.Background()
	srv := caldavtest.New(t, caldavtest.Radicale)
	b, mail, a := calDAVEndToEnd(t, srv)
	cal := writableCalendar(t, b, a)
	day := day0().AddDate(0, 0, 3).Format("2006-01-02")
	in := api.EventInput{Title: "Plan", Start: api.LocalDateTime(day + "T10:00"), End: api.LocalDateTime(day + "T11:00"), TimeZone: "Europe/Prague",
		Attendees: []api.EventInputAttendee{{Name: "Petr", Address: "petr@example.test"}}}
	res, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: cal, Event: in, Mail: &api.MailText{Subject: "Invitation: Plan", Text: "Come."}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Invitations.Via != api.InvitationsViaMail || res.Invitations.Count != 1 || res.Event.EventID == "" || res.Event.ETag == "" {
		t.Fatalf("created %+v", res)
	}
	if got := outboxSubjects(t, b, mail); !slices.Equal(got, []string{"Invitation: Plan"}) {
		t.Errorf("outbox %v", got)
	}
	if raw := outboxRaw(t, b, mail, 0); !strings.Contains(raw, "Come.") || !strings.Contains(raw, "text/calendar") ||
		!strings.Contains(raw, "method=REQUEST") {
		t.Errorf("iMIP message:\n%s", raw)
	}
	// A file of the event goes with the REQUEST (D21).
	imp, err := b.Attachments().Import(ctx, api.AttachmentImportParams{AccountID: api.AccountID(mail), Data: []byte("agenda"), Filename: "agenda.txt"})
	if err != nil {
		t.Fatal(err)
	}
	withFile := in
	withFile.Attachments = []string{string(imp.Attachment.ID)}
	if _, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: cal, Event: withFile}); err != nil {
		t.Fatal(err)
	}
	if raw := outboxRaw(t, b, mail, 1); !strings.Contains(raw, "agenda.txt") || !strings.Contains(raw, "Subject: Plan") {
		t.Errorf("iMIP with the file:\n%s", raw)
	}
	// Without invitations: nothing queued, the attendees stored.
	if r2, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: cal, Event: in, SendInvitations: ptr(false)}); err != nil ||
		r2.Invitations.Via != api.InvitationsViaNone || len(r2.Event.Attendees) != 1 {
		t.Errorf("without invitations %+v %v", r2, err)
	}
	if got := outboxSubjects(t, b, mail); len(got) != 2 {
		t.Errorf("outbox after sendInvitations false %v", got)
	}

	in.Title = "Plan v2"
	up, err := b.Events().Update(ctx, api.EventUpdateParams{EventID: res.Event.EventID, Scope: api.EditScopeAll, ETag: res.Event.ETag, Event: in})
	if err != nil {
		t.Fatal(err)
	}
	if up.Event.Title != "Plan v2" || up.Invitations.Via != api.InvitationsViaMail {
		t.Errorf("updated %+v", up)
	}
	if got := outboxSubjects(t, b, mail); len(got) != 3 || got[2] != "Plan v2" {
		t.Errorf("outbox after update %v", got)
	}

	// Another client changes the event: conflict with the server's event.
	row, err := b.store.GetEvent(ctx, string(res.Event.EventID))
	if err != nil {
		t.Fatal(err)
	}
	data, _, ok := srv.Object(row.RemoteID)
	if !ok {
		t.Fatalf("no object at %s", row.RemoteID)
	}
	i := strings.LastIndex(row.RemoteID, "/")
	srv.PutObject(row.RemoteID[:i+1], row.RemoteID[i+1:], []byte(strings.Replace(string(data), "SUMMARY:Plan v2", "SUMMARY:Theirs", 1)))
	in.Title = "Plan v3"
	_, err = b.Events().Update(ctx, api.EventUpdateParams{EventID: res.Event.EventID, Scope: api.EditScopeAll, ETag: up.Event.ETag, Event: in})
	if errCode(t, err) != api.CodeConflict {
		t.Fatalf("conflict: %v", err)
	}
	cd, ok := err.(*api.Error).Data.(api.ConflictEventData)
	if !ok || cd.Event.Title != "Theirs" || cd.Event.EventID != res.Event.EventID || cd.Event.ETag == up.Event.ETag {
		t.Fatalf("conflict data %+v", err.(*api.Error).Data)
	}
	// Gone from the server.
	srv.RemoveObject(row.RemoteID)
	if _, err := b.Events().Update(ctx, api.EventUpdateParams{EventID: res.Event.EventID, Scope: api.EditScopeAll, ETag: cd.Event.ETag, Event: in}); errCode(t, err) != api.CodeEventGone {
		t.Errorf("gone: %v", err)
	}
}

// Server scheduling (Nextcloud): no mail; the delete with attendees
// cancels by the server.
func TestEventCalDAVServerScheduling(t *testing.T) {
	ctx := context.Background()
	srv := caldavtest.New(t, caldavtest.Nextcloud)
	b, mail, a := calDAVEndToEnd(t, srv)
	cal := writableCalendar(t, b, a)
	day := day0().AddDate(0, 0, 3).Format("2006-01-02")
	in := api.EventInput{Title: "Sync", Start: api.LocalDateTime(day + "T10:00"), End: api.LocalDateTime(day + "T11:00"), TimeZone: "UTC",
		Attendees: []api.EventInputAttendee{{Address: "petr@example.test"}}}
	res, err := b.Events().Create(ctx, api.EventCreateParams{CalendarID: cal, Event: in})
	if err != nil {
		t.Fatal(err)
	}
	if res.Invitations.Via != api.InvitationsViaServer || res.Invitations.Count != 1 {
		t.Errorf("created %+v", res.Invitations)
	}
	if got := outboxSubjects(t, b, mail); len(got) != 0 {
		t.Errorf("outbox %v", got)
	}
	del, err := b.Events().Delete(ctx, api.EventDeleteParams{EventID: res.Event.EventID, Scope: api.EditScopeAll, ETag: res.Event.ETag})
	if err != nil {
		t.Fatal(err)
	}
	if del.Invitations.Via != api.InvitationsViaServer {
		t.Errorf("delete %+v", del)
	}
	if _, err := b.Events().Get(ctx, api.EventGetParams{EventID: res.Event.EventID}); errCode(t, err) != api.CodeEventNotFound {
		t.Errorf("deleted event: %v", err)
	}
}

// outboxRaw is the i-th queued message of the account as built.
func outboxRaw(t *testing.T, b *Backend, account string, i int) string {
	t.Helper()
	ctx := context.Background()
	entries, err := b.store.ListOutbox(ctx, account, store.OutboxQueued, time.Now().Add(time.Hour))
	if err != nil || len(entries) <= i {
		t.Fatalf("outbox %d entries, %v", len(entries), err)
	}
	r, err := b.store.OpenMessageRaw(ctx, account, entries[i].MessageID)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// graphLike is a fake source with Graph's RangeObjects (the events of a
// range outside the window come with their fields).
type graphLike struct{ *fakeCal }

func (g graphLike) RangeObjects(_ context.Context, cal calsync.StoredCalendar, from, to time.Time) ([]calendar.Object, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rangeErr != nil {
		return nil, g.rangeErr
	}
	var out []calendar.Object
	for _, o := range g.objs[cal.RemoteID] {
		o.Occurrences, _, _ = calendar.Occurrences(o, calendar.Window{From: from, To: to}, time.UTC, 0)
		if len(o.Occurrences) > 0 {
			out = append(out, o)
		}
	}
	return out, nil
}

func TestEventListOutsideWindowGraph(t *testing.T) {
	h := newCalHarness(t)
	g, gf := h.account(api.AccountGraph, calMe, func(c *api.AccountConfig) { c.Calendar.KeepDays = 10 }, mainCal("main"))
	h.provs[g.ID] = graphLike{gf}
	old := day0().AddDate(0, 0, -40)
	ev := invitedTo(timed("old", "Old review", old.Add(9*time.Hour), time.Hour), api.ResponseDeclined)
	gf.put("main", obj("o1", "e", timed("older", "Older", old.Add(12*time.Hour), time.Hour)))
	gf.put("main", obj("o2", "e", ev))
	h.start(g)
	res := listAll(t, h.b, api.EventListParams{From: old, To: old.AddDate(0, 0, 1), TimeZone: "UTC"})
	if got := titles(res.Occurrences); !res.Complete || !slices.Equal(got, []string{"Older"}) {
		t.Fatalf("outside %v %+v", got, res)
	}
	o := res.Occurrences[0]
	if o.EventID != "" || o.CalendarID == "" || o.Start == nil || !o.Start.Equal(old.Add(12*time.Hour)) || o.TimeZone != "UTC" {
		t.Errorf("occurrence %+v", o)
	}
	if got := titles(listAll(t, h.b, api.EventListParams{From: old, To: old.AddDate(0, 0, 1), IncludeDeclined: ptr(true)}).Occurrences); len(got) != 2 {
		t.Errorf("with declined %v", got)
	}
	if len(h.events(g)) != 0 {
		t.Error("stored")
	}
}

// event.list of a range outside a caldav account's window asks the
// server (RangeObjects) and shows the events with their fields; a failing
// server leaves the list incomplete.
func TestEventListOutsideWindowCalDAV(t *testing.T) {
	srv := caldavtest.New(t, caldavtest.Radicale)
	old := day0().AddDate(0, 0, -60)
	stamp := old.Add(9 * time.Hour).Format("20060102T150405Z")
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:old-1\r\nDTSTAMP:" + stamp +
		"\r\nDTSTART:" + stamp + "\r\nDURATION:PT1H\r\nSUMMARY:Old retro\r\nLOCATION:Room 7\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	srv.PutObject(srv.CalendarHref("calendar.ics"), "old-1.ics", []byte(ics))
	b, _, a := calDAVEndToEnd(t, srv, func(c *api.AccountConfig) { c.Calendar = &api.CalendarConfig{KeepDays: 30} })
	if evs, _ := b.store.EventsByUID(context.Background(), a.ID, "old-1"); len(evs) != 0 {
		t.Fatalf("the old event was stored: %d", len(evs))
	}
	p := api.EventListParams{From: old, To: old.AddDate(0, 0, 1), TimeZone: "UTC"}
	res := listAll(t, b, p)
	if !res.Complete || len(res.Occurrences) != 1 {
		t.Fatalf("outside %+v", res)
	}
	o := res.Occurrences[0]
	if o.Title != "Old retro" || o.Location != "Room 7" || o.Start == nil || !o.Start.Equal(old.Add(9*time.Hour)) || o.EventID != "" || o.CalendarID == "" {
		t.Errorf("occurrence %+v", o)
	}
	srv.FailNext(caldavtest.Failure{Method: "REPORT", Status: 500, Persist: true})
	if res := listAll(t, b, p); res.Complete || len(res.Occurrences) != 0 {
		t.Errorf("failing server %+v", res)
	}
}
