// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendardata

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/client"
)

// The seam of CalendarSource.swift: the snapshot's helpers, the changes,
// the preview's error and the keys (the cases of the Swift controllers'
// tests that read the snapshot, DaemonCalendarSourceTests' keys).

type en struct{}

func (en) T(s string) string { return s }
func (en) N(s, p string, n int) string {
	if n == 1 {
		return s
	}
	return p
}
func (en) C(_, s string) string { return s }

// manualLoop holds what is posted until run.
type manualLoop struct{ queue []func() }

func (l *manualLoop) Post(f func())                   { l.queue = append(l.queue, f) }
func (l *manualLoop) After(_ time.Duration, f func()) { l.queue = append(l.queue, f) }

// run runs everything posted, also what that posts; the number run.
func (l *manualLoop) run() int {
	n := 0
	for len(l.queue) > 0 {
		f := l.queue[0]
		l.queue = l.queue[1:]
		f()
		n++
	}
	return n
}

var (
	prague = mustLoad("Europe/Prague")
	calNow = time.Date(2026, 10, 14, 12, 0, 0, 0, prague)
)

func mustLoad(n string) *time.Location {
	l, err := time.LoadLocation(n)
	if err != nil {
		panic(err)
	}
	return l
}

func samples() calendar.Samples { return calendar.SampleData(calNow, prague).WithRemoteIDs() }

func ids(cs []api.Calendar) []api.CalendarID {
	var out []api.CalendarID
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func TestSnapshotHelpers(t *testing.T) {
	s := SampleSnapshot(samples())
	if s.Phase != PhaseReady || len(s.Accounts) != 3 || len(s.CalendarAccounts) != 3 || s.Unanswered != 3 || len(s.Invitations) != 3 {
		t.Fatalf("sample snapshot: %+v", s)
	}
	if got := s.AccountName(calendar.SampleNextcloud); got != "Nextcloud" {
		t.Errorf("AccountName = %q", got)
	}
	if s.AccountName("x") != "" || s.CalendarName("x") != "" || s.MyAddress("x") != "" {
		t.Error("unknown ids name something")
	}
	if got := s.CalendarName(calendar.SampleSport); got != "Sport" {
		t.Errorf("CalendarName = %q", got)
	}
	if got := s.MyAddress(calendar.SampleWork); got != "me@example.org" {
		t.Errorf("MyAddress = %q", got)
	}
	if got := s.MyName(calendar.SampleWork); got != "me@example.org" {
		t.Errorf("MyName without a display name = %q", got)
	}
	if v := s.VisibleCalendars(); len(v) != 6 || !v[calendar.SampleFamily] {
		t.Errorf("VisibleCalendars = %v", v)
	}
	// Writable: each account's default first, read-only ones left out.
	want := []api.CalendarID{calendar.SampleWorkCalendar, calendar.SamplePersonal, calendar.SampleFamily, calendar.SampleSport}
	if got := ids(s.WritableCalendars()); !slices.Equal(got, want) {
		t.Errorf("WritableCalendars = %v, want %v", got, want)
	}
	if n := len(s.States()); n != 3 {
		t.Errorf("States = %d", n)
	}
	for _, a := range s.CalendarAccounts {
		for _, c := range a.Calendars {
			if c.RemoteID == "" {
				t.Errorf("%s has no remote id", c.ID)
			}
		}
	}
}

// disabledCalendarsAreOnlyInPreferences: a disabled calendar is neither
// visible, writable nor named; CalendarAccount still lists it.
func TestDisabledCalendarsAreOnlyInPreferences(t *testing.T) {
	s := SampleSnapshot(samples())
	s.CalendarAccounts[1].Calendars[1].Disabled = true
	if s.VisibleCalendars()[calendar.SampleFamily] {
		t.Error("a disabled calendar is visible")
	}
	if slices.Contains(ids(s.WritableCalendars()), calendar.SampleFamily) {
		t.Error("a disabled calendar is writable")
	}
	if _, ok := s.Calendar(calendar.SampleFamily); ok || s.CalendarName(calendar.SampleFamily) != "" {
		t.Error("a disabled calendar is named")
	}
	if got := ids(s.ActiveCalendarAccounts()[1].Calendars); !slices.Equal(got, []api.CalendarID{calendar.SamplePersonal}) {
		t.Errorf("active = %v", got)
	}
	a, _ := s.CalendarAccount(calendar.SampleGmail)
	if got := ids(a.Calendars); !slices.Equal(got, []api.CalendarID{calendar.SamplePersonal, calendar.SampleFamily}) {
		t.Errorf("Preferences' account = %v", got)
	}
}

// Server text is one cleaned line.
const (
	rlo  = rune(0x202e) // RIGHT-TO-LEFT OVERRIDE
	zwsp = rune(0x200b) // ZERO WIDTH SPACE
)

func TestSnapshotCleansNames(t *testing.T) {
	s := SampleSnapshot(samples())
	s.Accounts[0].Config.Name = "Wo" + string(rlo) + "rk\nX"
	s.Accounts[0].Config.DisplayName = " Me\u0000 "
	s.CalendarAccounts[0].Calendars[0].Name = "Cal" + string(zwsp) + "endar"
	for _, got := range []string{s.AccountName(calendar.SampleWork), s.MyName(calendar.SampleWork), s.CalendarName(calendar.SampleWorkCalendar)} {
		for _, r := range got {
			if r == rlo || r == '\n' || r == 0 || r == zwsp {
				t.Errorf("%q keeps %U", got, r)
			}
		}
	}
}

func TestSnapshotClone(t *testing.T) {
	s := SampleSnapshot(samples())
	c := s.clone()
	c.CalendarAccounts[0].Calendars[0].Visible = false
	c.Accounts[0].Config.Name = "x"
	if !s.CalendarAccounts[0].Calendars[0].Visible || s.Accounts[0].Config.Name != "Work" {
		t.Error("clone shares its slices")
	}
	if s.Equal(c) || !s.Equal(s.clone()) {
		t.Error("Equal")
	}
}

func TestChangeOverlaps(t *testing.T) {
	at := func(d int) *time.Time { v := calNow.AddDate(0, 0, d); return &v }
	from, to := *at(0), *at(7)
	cases := []struct {
		c    Change
		want bool
	}{
		{Change{Kind: ChangeOccurrences}, true},
		{Change{Kind: ChangeOccurrences, From: at(3), To: at(4)}, true},
		{Change{Kind: ChangeOccurrences, From: at(7), To: at(9)}, false},
		{Change{Kind: ChangeOccurrences, From: at(-3), To: at(0)}, false},
		{Change{Kind: ChangeOccurrences, From: at(6)}, true},
		{Change{Kind: ChangeOccurrences, To: at(1)}, true},
		{Change{Kind: ChangeOccurrences, To: at(0)}, false},
		{Change{Kind: ChangeSnapshot}, false},
	}
	for i, c := range cases {
		if got := c.c.Overlaps(from, to); got != c.want {
			t.Errorf("%d: Overlaps = %v", i, got)
		}
	}
}

func TestErrors(t *testing.T) {
	if got, ok := ErrorMessage(ErrPreview, en{}); !ok || got != calendar.PreviewOnly(en{}) {
		t.Errorf("preview = %q %v", got, ok)
	}
	if got, ok := ErrorMessage(&api.Error{Code: api.CodeConflict}, en{}); !ok || got == "" {
		t.Errorf("conflict = %q", got)
	}
	if _, ok := ErrorMessage(&api.Error{Code: api.CodeInternalError, Message: "secret detail"}, en{}); ok {
		t.Error("an internal error has a calendar sentence")
	}
	if _, ok := ErrorMessage(nil, en{}); ok {
		t.Error("nil has a sentence")
	}
	if !IsPreview(ErrPreview) || IsPreview(&api.Error{Code: api.CodeConflict}) || IsPreview(nil) {
		t.Error("IsPreview")
	}
	daemon := &api.Error{Code: api.CodeReadOnly}
	cases := []struct {
		err  error
		code api.ErrorCode
	}{
		{daemon, api.CodeReadOnly},
		{fmt.Errorf("event.update: %w", daemon), api.CodeReadOnly},
		{client.ErrDisconnected, api.CodeUnavailable},
		{context.DeadlineExceeded, api.CodeCancelled},
		{context.Canceled, api.CodeCancelled},
		{errors.New("other"), api.CodeInternalError},
	}
	for _, c := range cases {
		if got := AsError(c.err); got == nil || got.Code != c.code {
			t.Errorf("AsError(%v) = %v", c.err, got)
		}
	}
	if AsError(nil) != nil {
		t.Error("AsError(nil)")
	}
}

func TestKeys(t *testing.T) {
	r := RangeIn(calendar.ViewRange(calendar.ViewWeek, calNow, time.Monday, prague), prague)
	if r.Zone() != "Europe/Prague" {
		t.Errorf("Zone = %q", r.Zone())
	}
	r2 := r
	r2.Loc = time.UTC
	if r.Key() == r2.Key() || r.Key() != RangeIn(calendar.ViewRange(calendar.ViewWeek, calNow, time.Monday, prague), prague).Key() {
		t.Error("Key ignores the zone or is unstable")
	}
	if EventKey("e", "") == EventKey("e", "2026-10-14") {
		t.Error("EventKey ignores the recurrence id")
	}
	if ZoneName(time.UTC) != "UTC" {
		t.Errorf("UTC = %q", ZoneName(time.UTC))
	}
	t.Setenv("TZ", ":Europe/Prague")
	if got := ZoneName(time.Local); got != "Europe/Prague" {
		t.Errorf("TZ name = %q", got)
	}
	t.Setenv("TZ", "/usr/share/zoneinfo/America/New_York")
	if got := ZoneName(nil); got != "America/New_York" {
		t.Errorf("TZ path = %q", got)
	}
	t.Setenv("TZ", "")
	if got := ZoneName(time.Local); got != "UTC" {
		t.Errorf("empty TZ = %q", got)
	}
	for _, bad := range []string{"../../etc/passwd", "Not/AZone", "Local"} {
		if zoneOfPath(bad) != "" {
			t.Errorf("zoneOfPath(%q) names a zone", bad)
		}
	}
}

func TestIsCalendarNotification(t *testing.T) {
	for _, m := range []string{api.NotifyCalendarChanged, api.NotifyCalendarState, api.NotifyEventAlert, api.NotifyInvitationsChanged} {
		if !IsCalendarNotification(m) {
			t.Errorf("%s", m)
		}
	}
	if IsCalendarNotification(api.NotifyAccountsChanged) {
		t.Error("accountsChanged is the calendar's")
	}
}

// An observer that causes another change hears it; the rest of the outer
// round still hears the outer one.
func TestObserversNest(t *testing.T) {
	var o observers
	var got []string
	o.add(func(c Change) {
		got = append(got, fmt.Sprintf("a%d", c.Kind))
		if c.Kind == ChangeSnapshot {
			o.notify(Change{Kind: ChangeEvent})
		}
	})
	tok := o.add(func(c Change) { got = append(got, fmt.Sprintf("b%d", c.Kind)) })
	o.notify(Change{Kind: ChangeSnapshot})
	want := []string{"a0", "a2", "b2", "b0"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	tok.Cancel()
	got = nil
	o.notify(Change{Kind: ChangeAlert})
	if !slices.Equal(got, []string{"a3"}) {
		t.Errorf("after Cancel %v", got)
	}
}
