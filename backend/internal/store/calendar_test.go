// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

var calTables = []string{"cal_calendars", "cal_events", "cal_occurrences", "cal_resources", "cal_alerts",
	"cal_invitations", "cal_responses", "cal_events_fts", "cal_events_fts_docs"}

// calAccount adds a mail account and one calendar of it.
func calAccount(t *testing.T, s *Store, email string) (Account, Calendar) {
	t.Helper()
	ctx := context.Background()
	a := Account{Name: "Work", Enabled: true, Config: testAccountConfig(email)}
	if err := s.AddAccount(ctx, &a); err != nil {
		t.Fatal(err)
	}
	cals, err := s.UpsertCalendars(ctx, a.ID, []Calendar{{RemoteID: "/cal/" + email + "/", Name: "Calendar", Access: api.CalendarAccessOwner}})
	if err != nil {
		t.Fatal(err)
	}
	return a, cals[0]
}

func calRows(t *testing.T, s *Store, table, where string, args ...any) int {
	t.Helper()
	q := `SELECT COUNT(*) FROM ` + table
	if where != "" {
		q += ` WHERE ` + where
	}
	var n int
	if err := s.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestCalendarMigrationFresh(t *testing.T) {
	s := openTestStore(t)
	for _, name := range append(calTables, "cal_events_fts_ai", "cal_events_fts_au", "cal_events_fts_ad", "cal_event_search_text") {
		if calRows(t, s, "sqlite_master", "name = ?", name) != 1 {
			t.Errorf("%s missing", name)
		}
	}
	// No trigger of 0020 on a table of the mail or the board.
	rows, err := s.db.Query(`SELECT name, tbl_name FROM sqlite_master WHERE type = 'trigger' AND name LIKE 'cal%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, tbl string
		if err := rows.Scan(&name, &tbl); err != nil {
			t.Fatal(err)
		}
		if tbl != "cal_events" {
			t.Errorf("trigger %s on %s", name, tbl)
		}
	}
}

// A store migrated through 0019 by an older build, with mail and board
// rows and the board's triggers in place, takes 0020 and keeps them.
func TestCalendarMigrationFrom0019(t *testing.T) {
	path := migrateRawTo(t, 19,
		`INSERT INTO accounts (id, email, name, config) VALUES ('acc', 'a@example.invalid', 'A', '{"name":"A","email":"a@example.invalid"}')`,
		`INSERT INTO folders (id, account_id, mailbox, name, path, role) VALUES ('f', 'acc', 'INBOX', 'INBOX', 'INBOX', 'inbox')`,
		`INSERT INTO messages (id, account_id, folder_id, uid, subject, thread_id) VALUES ('m_1', 'acc', 'f', 1, 'Hello', 't_1')`)
	s, err := Open(context.Background(), path, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil || v < 20 {
		t.Fatalf("version %d %v", v, err)
	}
	for _, trig := range []string{"messages_board_ai", "messages_board_au", "messages_board_ad", "messages_search_ai", "meta_board_me_ai"} {
		if calRows(t, s, "sqlite_master", "type = 'trigger' AND name = ?", trig) != 1 {
			t.Errorf("trigger %s gone", trig)
		}
	}
	for _, tbl := range calTables {
		if calRows(t, s, "sqlite_master", "name = ?", tbl) != 1 {
			t.Errorf("%s missing", tbl)
		}
	}
	if calRows(t, s, "messages", "id = 'm_1'") != 1 {
		t.Fatal("message lost")
	}
	// The invitation of a message stored before 0020 can be written.
	if err := s.PutInvitation(context.Background(), Invitation{MessageID: "m_1", AccountID: "acc", PartID: "2",
		Method: api.InvitationMethodRequest, UID: "u", Card: []byte(`{}`), ParserVersion: 1}); err != nil {
		t.Fatal(err)
	}
}

// Writing every cal_* table, the calendar meta keys included, marks no
// thread of the board dirty.
func TestCalendarWritesLeaveBoardAlone(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	a, cal := calAccount(t, s, "me@example.invalid")
	inbox := seedFolder(t, s, a.ID, "INBOX", api.RoleInbox)
	m := seedMessage(t, s, inbox, 1, "Invite", threadBase)
	clearDirty(t, s)

	evs, err := s.ReplaceResource(ctx, cal.ID, "/e/1.ics", []Event{
		{Kind: EventMaster, UID: "u1", Title: "Weekly", StartAt: threadBase, EndAt: threadBase.Add(time.Hour), RRule: "FREQ=WEEKLY",
			Attendees: []api.Attendee{{Name: "Jana", Address: "jana@example.invalid"}}, Alarms: []int{15}, Raw: []byte("BEGIN:VCALENDAR")},
		{Kind: EventOverride, UID: "u1", RecurrenceID: FormatCalendarTime(threadBase.AddDate(0, 0, 7)), Title: "Moved",
			StartAt: threadBase.AddDate(0, 0, 7).Add(time.Hour), EndAt: threadBase.AddDate(0, 0, 7).Add(2 * time.Hour)},
	}, func(stored []Event) []Occurrence {
		return []Occurrence{{EventID: stored[0].ID, OccurrenceKey: FormatCalendarTime(threadBase), CalendarID: cal.ID, AccountID: a.ID,
			StartAt: threadBase, EndAt: threadBase.Add(time.Hour), SeriesID: stored[0].ID}}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateCalendarUser(ctx, cal.ID, ptr(false), ptr("#ff0000")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCalendarSyncState(ctx, cal.ID, CalendarSyncState{SyncToken: "t", LastSync: threadBase}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutResources(ctx, cal.ID, []Resource{{Href: "/e/1.ics", ETag: "1", State: ResourceStored}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SyncAlerts(ctx, nil, []Alert{{EventID: evs[0].ID, OccurrenceKey: FormatCalendarTime(threadBase), MinutesBefore: 15, DueAt: threadBase}}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutInvitation(ctx, Invitation{MessageID: m.ID, AccountID: a.ID, PartID: "2", Method: api.InvitationMethodRequest,
		UID: "u1", Card: []byte(`{"title":"Weekly"}`), ParserVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutResponse(ctx, Response{AccountID: a.ID, UID: "u1", Response: api.ResponseAccepted, Via: "imip"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCalendarPreferences(ctx, api.DefaultCalendarPreferences()); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{MetaCalendarInvitesScan, MetaCalendarExpand} {
		if err := s.SetMeta(ctx, k, "1:done"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RemoveResource(ctx, cal.ID, "/e/1.ics"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInvitation(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCalendar(ctx, cal.ID); err != nil {
		t.Fatal(err)
	}
	if got := dirtyThreads(t, s); len(got) != 0 {
		t.Fatalf("board dirty after calendar writes: %v", got)
	}
}

func ptr[T any](v T) *T { return &v }

// No calendar statement is an INSERT OR REPLACE (cal_events has delete
// triggers it would bypass; docs/calendar.md §2.6).
func TestCalendarNoInsertOrReplace(t *testing.T) {
	files, err := filepath.Glob("calendar*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "migrations/0020_calendar.sql")
	re := regexp.MustCompile(`(?i)insert\s+or\s+replace|replace\s+into`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if l := strings.TrimSpace(line); strings.HasPrefix(l, "--") || strings.HasPrefix(l, "//") {
				continue // the rule's own explanation
			}
			if re.MatchString(line) {
				t.Errorf("%s:%d: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

func TestRealmOfCalDAV(t *testing.T) {
	for url, want := range map[string]string{
		"https://Cloud.Example.org/remote.php/dav/": "cloud.example.org/remote.php/dav",
		"https://cloud.example.org:443/dav":         "cloud.example.org/dav",
		"https://cloud.example.org:8443/dav":        "cloud.example.org:8443/dav",
		"http://127.0.0.1:5232":                     "127.0.0.1:5232",
		"not a url":                                 "",
	} {
		cfg := api.AccountConfig{Kind: api.AccountCalDAV, CalDAV: &api.CalDAVConfig{ServerURL: url}}
		if got := RealmOf(cfg); got != want {
			t.Errorf("RealmOf(%q) = %q, want %q", url, got, want)
		}
	}
	// Two caldav accounts of one address on two servers are two accounts;
	// on one server, one.
	ctx := context.Background()
	s := openTestStore(t)
	add := func(server string) error {
		a := Account{Name: "Cal", Enabled: true, Config: api.AccountConfig{Name: "Cal", Email: "jana@example.invalid",
			Kind: api.AccountCalDAV, CalDAV: &api.CalDAVConfig{ServerURL: server, Username: "jana"}}}
		return s.AddAccount(ctx, &a)
	}
	if err := add("https://a.example.invalid/dav"); err != nil {
		t.Fatal(err)
	}
	if err := add("https://b.example.invalid/dav"); err != nil {
		t.Fatal(err)
	}
	if err := add("https://A.example.invalid/dav/"); err != ErrExists {
		t.Fatalf("same server: %v", err)
	}
	if err := add(""); err == nil {
		t.Fatal("caldav account without a server URL stored")
	}
}

func TestUpsertCalendarsKeepsUserColumns(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	a, cal := calAccount(t, s, "me@example.invalid")
	if !cal.Visible || cal.Access != api.CalendarAccessOwner || cal.Kind != api.CalendarKindEvents || !strings.HasPrefix(cal.ID, "cal_") {
		t.Fatalf("new calendar: %+v", cal)
	}
	if _, err := s.UpdateCalendarUser(ctx, cal.ID, ptr(false), ptr("#e5534b")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCalendarSyncState(ctx, cal.ID, CalendarSyncState{SyncMode: "delta", SyncToken: "tok", WindowFrom: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	again, err := s.UpsertCalendars(ctx, a.ID, []Calendar{{RemoteID: cal.RemoteID, Name: "Renamed", ServerColor: "#1f6feb", Access: api.CalendarAccessRead, Position: 3},
		{RemoteID: "/other/", Name: "Other"}})
	if err != nil {
		t.Fatal(err)
	}
	got := again[0]
	if got.ID != cal.ID || got.Visible || got.UserColor != "#e5534b" || got.Name != "Renamed" || got.ServerColor != "#1f6feb" ||
		got.Access != api.CalendarAccessRead || got.SyncToken != "tok" || got.SyncMode != "delta" || got.Position != 3 {
		t.Fatalf("resync: %+v", got)
	}
	if _, err := s.UpdateCalendarUser(ctx, cal.ID, nil, ptr("")); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListCalendars(ctx, a.ID)
	if err != nil || len(list) != 2 || list[0].Name != "Other" || list[1].UserColor != "" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if _, err := s.UpdateCalendarUser(ctx, "cal_none", ptr(true), nil); err != ErrNotFound {
		t.Fatalf("unknown: %v", err)
	}
	p, err := s.CalendarPreferences(ctx)
	if err != nil || p.DefaultDurationMinutes != 60 {
		t.Fatalf("default prefs: %+v %v", p, err)
	}
	p.ShowDeclined = true
	if err := s.SetCalendarPreferences(ctx, p); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.CalendarPreferences(ctx); !p.ShowDeclined {
		t.Fatal("prefs not stored")
	}
}

// DeleteAccount removes every cal_* row of the account and nothing of
// another's; DeleteAccountCalendars keeps invitations and answers.
func TestDeleteAccountRemovesCalendar(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	fill := func(email string) (Account, Calendar) {
		a, cal := calAccount(t, s, email)
		inbox := seedFolder(t, s, a.ID, "INBOX", api.RoleInbox)
		m := seedMessage(t, s, inbox, 1, "Invite "+email, threadBase)
		evs, err := s.ReplaceResource(ctx, cal.ID, "/e/1.ics", []Event{{Kind: EventSingle, UID: "u", Title: "Lunch " + email,
			StartAt: threadBase, EndAt: threadBase.Add(time.Hour)}}, func(st []Event) []Occurrence {
			return []Occurrence{{EventID: st[0].ID, CalendarID: cal.ID, AccountID: a.ID, StartAt: threadBase, EndAt: threadBase.Add(time.Hour)}}
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.PutResources(ctx, cal.ID, []Resource{{Href: "/e/1.ics", State: ResourceStored}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SyncAlerts(ctx, []string{evs[0].ID}, []Alert{{EventID: evs[0].ID, MinutesBefore: 5, DueAt: threadBase}}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutInvitation(ctx, Invitation{MessageID: m.ID, AccountID: a.ID, PartID: "2", Method: api.InvitationMethodRequest, UID: "u", Card: []byte(`{}`), ParserVersion: 1}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutResponse(ctx, Response{AccountID: a.ID, UID: "u", Response: api.ResponseDeclined, Via: "none"}); err != nil {
			t.Fatal(err)
		}
		return a, cal
	}
	a, _ := fill("me@example.invalid")
	b, _ := fill("other@example.invalid")

	if err := s.DeleteAccountCalendars(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"cal_calendars", "cal_events", "cal_occurrences", "cal_resources", "cal_alerts", "cal_events_fts_docs"} {
		if n := calRows(t, s, tbl, ""); n != 1 {
			t.Errorf("after sync off of b: %s has %d rows", tbl, n)
		}
	}
	if calRows(t, s, "cal_invitations", "account_id = ?", b.ID) != 1 || calRows(t, s, "cal_responses", "account_id = ?", b.ID) != 1 {
		t.Error("sync off dropped invitations or answers")
	}

	if err := s.DeleteAccount(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"cal_calendars", "cal_events", "cal_occurrences", "cal_resources", "cal_alerts", "cal_events_fts_docs"} {
		if n := calRows(t, s, tbl, ""); n != 0 {
			t.Errorf("after delete of a: %s has %d rows", tbl, n)
		}
	}
	for _, tbl := range []string{"cal_invitations", "cal_responses"} {
		if calRows(t, s, tbl, "account_id = ?", a.ID) != 0 || calRows(t, s, tbl, "account_id = ?", b.ID) != 1 {
			t.Errorf("%s after delete of a", tbl)
		}
	}
	if ids, _, err := s.SearchEvents(ctx, EventSearch{Query: "lunch"}); err != nil || len(ids) != 0 {
		t.Fatalf("search after delete: %v %v", ids, err)
	}
}

func TestDeleteCalendarContentsKeepsRow(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	a, cal := calAccount(t, s, "me@example.invalid")
	evs, err := s.ReplaceResource(ctx, cal.ID, "/e/1.ics", []Event{{Kind: EventSingle, UID: "u", Title: "Lunch",
		StartAt: threadBase, EndAt: threadBase.Add(time.Hour)}}, func(st []Event) []Occurrence {
		return []Occurrence{{EventID: st[0].ID, CalendarID: cal.ID, AccountID: a.ID, StartAt: threadBase, EndAt: threadBase.Add(time.Hour)}}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutResources(ctx, cal.ID, []Resource{{Href: "/e/1.ics", State: ResourceStored}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SyncAlerts(ctx, []string{evs[0].ID}, []Alert{{EventID: evs[0].ID, MinutesBefore: 5, DueAt: threadBase}}); err != nil {
		t.Fatal(err)
	}
	hidden := false
	if _, err := s.UpdateCalendarUser(ctx, cal.ID, &hidden, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCalendarContents(ctx, cal.ID); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"cal_events", "cal_occurrences", "cal_resources", "cal_alerts", "cal_events_fts_docs"} {
		if n := calRows(t, s, tbl, ""); n != 0 {
			t.Errorf("%s has %d rows", tbl, n)
		}
	}
	got, err := s.GetCalendar(ctx, cal.ID)
	if err != nil || got.Visible {
		t.Errorf("row %+v %v", got, err)
	}
	if err := s.DeleteCalendarContents(ctx, "cal_unknown"); err != nil {
		t.Error(err)
	}
}
