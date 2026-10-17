// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"hash/fnv"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Calendars serves calendar.* (docs/api.md §4.14, docs/calendar.md §4.4):
// the calendars of accounts with the calendar capability, their state,
// the user's settings of a calendar and the calendar preferences. Every
// method reads the store and the engine; only calendar.freeBusy
// (calendar_freebusy.go) may ask a server.
func (b *Backend) Calendars() api.CalendarService { return &calendarService{b} }

type calendarService struct{ b *Backend }

var _ api.CalendarService = (*calendarService)(nil)

// calendarPalette colours a calendar that has no colour of the server's
// or the user's, by a hash of its id (stable across restarts).
var calendarPalette = []string{
	"#1f6feb", "#e5534b", "#2da44e", "#bf8700", "#8250df", "#d0457f", "#1b7c83", "#cf6c00",
}

func paletteColor(id string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return calendarPalette[int(h.Sum32()%uint32(len(calendarPalette)))]
}

var hexColor = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// calendarReadOnly: the user cannot change the calendar's events (a
// read-only or shared calendar, birthdays, holidays).
func calendarReadOnly(c store.Calendar) bool {
	return c.Access == api.CalendarAccessRead || c.Kind == api.CalendarKindBirthdays || c.Kind == api.CalendarKindHolidays
}

// calendarSource is the account's calendar source ("" = none).
func calendarSource(a store.Account) api.CalendarSource {
	switch a.Config.Protocol() {
	case api.AccountGraph:
		return api.CalendarSourceGraph
	case api.AccountCalDAV:
		return api.CalendarSourceCalDAV
	}
	return ""
}

// calendarAccounts are the accounts with the calendar capability, in the
// accounts' order.
func (b *Backend) calendarAccounts(ctx context.Context) ([]store.Account, error) {
	list, err := b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	out := list[:0:0]
	for _, a := range list {
		if can(a.Config, api.CapabilityCalendar) {
			out = append(out, a)
		}
	}
	return out, nil
}

// readableCalendarAccounts are the accounts whose stored calendars count:
// enabled, with the calendar capability and its sync on.
func (b *Backend) readableCalendarAccounts(ctx context.Context) ([]store.Account, error) {
	list, err := b.calendarAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := list[:0:0]
	for _, a := range list {
		if calsync.HasCalendars(a) {
			out = append(out, a)
		}
	}
	return out, nil
}

// calendarSyncOn: the account's calendars are synchronised (caldav
// always, graph with CalendarConfig.sync), paused or not.
func calendarSyncOn(a store.Account) bool {
	switch a.Config.Protocol() {
	case api.AccountCalDAV:
		return true
	case api.AccountGraph:
		return a.Config.Calendar != nil && a.Config.Calendar.Sync
	}
	return false
}

// calendarState is the account's calendar state: the engine's while its
// syncer runs, disabled for a paused account or a Graph account with the
// calendar off, else idle with unknown progress (not started yet).
func (b *Backend) calendarState(a store.Account) api.CalendarSyncState {
	if !calsync.HasCalendars(a) {
		return api.CalendarSyncState{AccountID: api.AccountID(a.ID), Status: api.CalendarSyncDisabled, Progress: -1}
	}
	if eng := b.calendarEngine(); eng != nil {
		if st, ok := eng.CalendarState(a.ID); ok {
			return st
		}
	}
	return api.CalendarSyncState{AccountID: api.AccountID(a.ID), Status: api.CalendarSyncIdle, Progress: -1}
}

// calendarFeatures are what the account's source can do: the running
// provider's, else what the configuration says (no request is made).
func (b *Backend) calendarFeatures(a store.Account) api.CalendarFeatures {
	if eng := b.calendarEngine(); eng != nil {
		if p, ok := eng.Provider(a.ID); ok {
			return p.Features()
		}
	}
	switch a.Config.Protocol() {
	case api.AccountGraph:
		return api.CalendarFeatures{ProposeNewTime: true, Invitations: api.InvitationsViaServer}
	case api.AccountCalDAV:
		cd := a.Config.CalDAV
		if cd != nil && cd.Scheduling == api.CalDAVSchedulingClient {
			if cd.MailAccountID != "" {
				return api.CalendarFeatures{Invitations: api.InvitationsViaMail, MailAccountID: cd.MailAccountID}
			}
			return api.CalendarFeatures{Invitations: api.InvitationsViaNone}
		}
		return api.CalendarFeatures{Invitations: api.InvitationsViaServer}
	}
	return api.CalendarFeatures{Invitations: api.InvitationsViaNone}
}

// calendarDisabled: the calendar is in the account's disabledCalendars or
// the engine marked its row so (calsync.Disabled; the row is kept without
// contents and calendar.list shows it as disabled).
func calendarDisabled(a store.Account, c store.Calendar) bool {
	return calsync.Disabled(c) || a.Config.Calendar != nil && slices.Contains(a.Config.Calendar.DisabledCalendars, c.RemoteID)
}

// listedCalendars are every stored calendar of an account with its sync
// on, disabled ones included (calendar.list), in server order.
func (b *Backend) listedCalendars(ctx context.Context, a store.Account) ([]store.Calendar, error) {
	if !calendarSyncOn(a) {
		return nil, nil
	}
	rows, err := b.store.ListCalendars(ctx, a.ID)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	return rows, nil
}

// accountCalendars are the stored calendars of an account that count (its
// sync on, not in disabledCalendars), in server order: what event.list,
// free/busy, search and the writes see.
func (b *Backend) accountCalendars(ctx context.Context, a store.Account) ([]store.Calendar, error) {
	if !calendarSyncOn(a) {
		return nil, nil
	}
	rows, err := b.store.ListCalendars(ctx, a.ID)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	var disabled []string
	if a.Config.Calendar != nil {
		disabled = a.Config.Calendar.DisabledCalendars
	}
	out := rows[:0]
	for _, c := range rows {
		if !slices.Contains(disabled, c.RemoteID) && !calsync.Disabled(c) {
			out = append(out, c)
		}
	}
	return out, nil
}

// defaultRemote is the remote id of the account's default calendar among
// cals: CalendarConfig.defaultCalendar when it names one of them, else
// the server's default ("" = none).
func defaultRemote(a store.Account, cals []store.Calendar) string {
	if c := a.Config.Calendar; c != nil && c.DefaultCalendar != "" {
		for _, x := range cals {
			if x.RemoteID == c.DefaultCalendar {
				return x.RemoteID
			}
		}
	}
	for _, x := range cals {
		if x.ServerDefault {
			return x.RemoteID
		}
	}
	return ""
}

// apiCalendar is a stored calendar as the API shows it; disabled ones are
// never the default.
func apiCalendar(c store.Calendar, def string, disabled bool) api.Calendar {
	color := c.UserColor
	if !hexColor.MatchString(color) {
		color = c.ServerColor
	}
	if !hexColor.MatchString(color) {
		color = paletteColor(c.ID)
	}
	out := api.Calendar{
		ID: api.CalendarID(c.ID), AccountID: api.AccountID(c.AccountID),
		Name:  calendar.CleanLine(c.Name, calendar.MaxNameBytes),
		Color: color, Visible: c.Visible, Access: c.Access, Kind: c.Kind,
		IsDefault: !disabled && def != "" && c.RemoteID == def,
		RemoteID:  c.RemoteID, Disabled: disabled,
		TimeZone: api.TimeZone(calendar.CleanLine(c.TimeZone, 64)),
	}
	if hexColor.MatchString(c.ServerColor) {
		out.ServerColor = c.ServerColor
	}
	if hexColor.MatchString(c.UserColor) {
		out.UserColor = c.UserColor
	}
	switch out.Kind {
	case api.CalendarKindEvents, api.CalendarKindBirthdays, api.CalendarKindHolidays, api.CalendarKindOther:
	default:
		out.Kind = api.CalendarKindEvents
	}
	if c.OwnerAddress != "" {
		out.Owner = &api.Address{Name: calendar.CleanLine(c.OwnerName, calendar.MaxNameBytes),
			Address: calendar.CleanLine(c.OwnerAddress, calendar.MaxNameBytes)}
	}
	return out
}

func (s *calendarService) List(ctx context.Context, p api.CalendarListParams) (*api.CalendarListResult, error) {
	b := s.b
	var accounts []store.Account
	if p.AccountID != "" {
		a, err := b.requireAccount(ctx, string(p.AccountID))
		if err != nil {
			return nil, err
		}
		if !can(a.Config, api.CapabilityCalendar) {
			return nil, api.NewError(api.CodeInvalidArgument, "account %q has no calendars", a.ID)
		}
		accounts = []store.Account{a}
	} else {
		var err error
		if accounts, err = b.calendarAccounts(ctx); err != nil {
			return nil, err
		}
	}
	res := &api.CalendarListResult{Accounts: []api.CalendarAccount{}}
	loc := b.calendarLocation()
	for _, a := range accounts {
		ca := api.CalendarAccount{
			AccountID: api.AccountID(a.ID), Source: calendarSource(a),
			Sync:     calendarSyncOn(a),
			State:    b.calendarState(a),
			Features: b.calendarFeatures(a),
			Window:   calsync.APIWindow(calsync.WindowFor(time.Now(), calsync.KeepDays(a), loc), loc),
		}
		if eng := b.calendarEngine(); eng != nil {
			ca.Window = calsync.APIWindow(eng.Window(a), loc)
		}
		ca.Calendars = []api.Calendar{}
		if ca.Sync {
			cals, err := b.accountCalendars(ctx, a)
			if err != nil {
				return nil, err
			}
			all, err := b.listedCalendars(ctx, a)
			if err != nil {
				return nil, err
			}
			def := defaultRemote(a, cals)
			for _, c := range all {
				ca.Calendars = append(ca.Calendars, apiCalendar(c, def, calendarDisabled(a, c)))
			}
		}
		res.Accounts = append(res.Accounts, ca)
	}
	n, err := b.unansweredInvitations(ctx)
	if err != nil {
		return nil, err
	}
	res.Unanswered = n
	return res, nil
}

// unansweredInvitations counts the invitations of enabled accounts waiting
// for an answer (invitation.list with state unanswered).
func (b *Backend) unansweredInvitations(ctx context.Context) (int, error) {
	list, err := b.store.ListAccounts(ctx)
	if err != nil {
		return 0, api.NewError(api.CodeStorageError, "%v", err)
	}
	var off []string
	for _, a := range list {
		if !a.Enabled {
			off = append(off, a.ID)
		}
	}
	invs, err := b.store.ListInvitations(ctx, store.InvitationQuery{ExcludeAccountIDs: off,
		State: api.InvitationUnanswered, Limit: api.MaxInvitationList}, time.Now())
	if err != nil {
		return 0, api.NewError(api.CodeStorageError, "%v", err)
	}
	return len(invs), nil
}

// calendarLocation is the daemon's zone the engine works in.
func (b *Backend) calendarLocation() *time.Location {
	if eng := b.calendarEngine(); eng != nil {
		return eng.Location()
	}
	return time.Local
}

// notifyCalendarChanged sends notify.calendarChanged (no accounts and no
// calendars: the preferences changed).
func (b *Backend) notifyCalendarChanged(accounts []api.AccountID, calendars []api.CalendarID) {
	if cn, ok := b.getNotifier().(api.CalendarNotifier); ok {
		cn.CalendarChanged(api.CalendarChangedNotification{AccountIDs: accounts, CalendarIDs: calendars})
	}
}

// requireCalendar resolves a calendar id: calendarNotFound when unknown,
// when its account is gone or its sync is off or it is disabled.
func (b *Backend) requireCalendar(ctx context.Context, id string) (store.Calendar, store.Account, error) {
	c, a, err := b.listedCalendar(ctx, id)
	if err == nil && calendarDisabled(a, c) {
		return store.Calendar{}, store.Account{}, api.NewError(api.CodeCalendarNotFound, "the calendar is not synchronised")
	}
	return c, a, err
}

// listedCalendar resolves a calendar calendar.list shows, disabled ones
// included (calendar.update).
func (b *Backend) listedCalendar(ctx context.Context, id string) (store.Calendar, store.Account, error) {
	if id == "" {
		return store.Calendar{}, store.Account{}, api.NewError(api.CodeInvalidArgument, "calendarId is required")
	}
	c, err := b.store.GetCalendar(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.Calendar{}, store.Account{}, api.NewError(api.CodeCalendarNotFound, "unknown calendar %q", id)
	case err != nil:
		return store.Calendar{}, store.Account{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	a, err := b.store.GetAccount(ctx, c.AccountID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.Calendar{}, store.Account{}, api.NewError(api.CodeCalendarNotFound, "the calendar's account is gone")
	case err != nil:
		return store.Calendar{}, store.Account{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	if !calendarSyncOn(a) {
		return store.Calendar{}, store.Account{}, api.NewError(api.CodeCalendarNotFound, "the calendar is not synchronised")
	}
	return c, a, nil
}

func (s *calendarService) Update(ctx context.Context, p api.CalendarUpdateParams) (*api.CalendarUpdateResult, error) {
	b := s.b
	// A disabled calendar takes the user's columns too: they wait for it
	// to be enabled again and change nothing meanwhile.
	c, a, err := b.listedCalendar(ctx, string(p.CalendarID))
	if err != nil {
		return nil, err
	}
	disabled := calendarDisabled(a, c)
	var color *string
	if p.UserColor != nil {
		v := strings.ToLower(strings.TrimSpace(*p.UserColor))
		if v != "" && !hexColor.MatchString(v) {
			return nil, api.NewError(api.CodeInvalidArgument, "userColor must be #rrggbb")
		}
		color = &v
	}
	if p.Visible == nil && color == nil {
		cals, err := b.accountCalendars(ctx, a)
		if err != nil {
			return nil, err
		}
		return &api.CalendarUpdateResult{Calendar: apiCalendar(c, defaultRemote(a, cals), disabled)}, nil
	}
	c, err = b.store.UpdateCalendarUser(ctx, c.ID, p.Visible, color)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeCalendarNotFound, "unknown calendar %q", p.CalendarID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if p.Visible != nil && !disabled {
		// Alerts belong to visible calendars only.
		if eng := b.calendarEngine(); eng != nil {
			eng.Reload()
		}
	}
	cals, err := b.accountCalendars(ctx, a)
	if err != nil {
		return nil, err
	}
	b.notifyCalendarChanged([]api.AccountID{api.AccountID(a.ID)}, []api.CalendarID{api.CalendarID(c.ID)})
	return &api.CalendarUpdateResult{Calendar: apiCalendar(c, defaultRemote(a, cals), disabled)}, nil
}

func (s *calendarService) Sync(ctx context.Context, p api.CalendarSyncParams) (*api.CalendarSyncResult, error) {
	b := s.b
	eng := b.calendarEngine()
	accountID := string(p.AccountID)
	calendarID := string(p.CalendarID)
	if calendarID != "" {
		c, _, err := b.requireCalendar(ctx, calendarID)
		if err != nil {
			return nil, err
		}
		if accountID != "" && accountID != c.AccountID {
			return nil, api.NewError(api.CodeCalendarNotFound, "calendar %q is not the account's", calendarID)
		}
		accountID = c.AccountID
	} else if accountID != "" {
		a, err := b.requireAccount(ctx, accountID)
		if err != nil {
			return nil, err
		}
		if !can(a.Config, api.CapabilityCalendar) {
			return nil, api.NewError(api.CodeInvalidArgument, "account %q has no calendars", a.ID)
		}
	}
	if eng != nil {
		eng.SyncNow(accountID, calendarID)
	}
	return &api.CalendarSyncResult{}, nil
}

func (s *calendarService) Preferences(ctx context.Context, _ api.CalendarPreferencesParams) (*api.CalendarPreferencesResult, error) {
	p, err := s.b.store.CalendarPreferences(ctx)
	if err != nil {
		s.b.log.Warn("calendar preferences unreadable, using the defaults", "err", err)
		p = api.DefaultCalendarPreferences()
	}
	return &api.CalendarPreferencesResult{Preferences: p}, nil
}

// weekdays are the iCalendar day names working hours use.
var weekdays = []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}

// clockMinutes reads "HH:MM" (00:00–24:00).
func clockMinutes(s string) (int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	h, m := 0, 0
	for i, c := range s {
		if i == 2 {
			continue
		}
		if c < '0' || c > '9' {
			return 0, false
		}
		if i < 2 {
			h = h*10 + int(c-'0')
		} else {
			m = m*10 + int(c-'0')
		}
	}
	if m > 59 || h > 24 || h == 24 && m != 0 {
		return 0, false
	}
	return h*60 + m, true
}

// validWorkingHours: at least one known day, no repeats, "HH:MM" times,
// end after start.
func validWorkingHours(w api.WorkingHours) error {
	if len(w.Days) == 0 || len(w.Days) > len(weekdays) {
		return api.NewError(api.CodeInvalidArgument, "workingHours: one to seven days")
	}
	for i, d := range w.Days {
		if !slices.Contains(weekdays, d) || slices.Contains(w.Days[:i], d) {
			return api.NewError(api.CodeInvalidArgument, "workingHours: bad day %q", calendar.CleanLine(d, 8))
		}
	}
	s, ok1 := clockMinutes(w.Start)
	e, ok2 := clockMinutes(w.End)
	if !ok1 || !ok2 || e <= s {
		return api.NewError(api.CodeInvalidArgument, "workingHours: start and end are HH:MM, end after start")
	}
	return nil
}

func (s *calendarService) SetPreferences(ctx context.Context, p api.CalendarSetPreferencesParams) (*api.CalendarSetPreferencesResult, error) {
	b := s.b
	cur, err := b.store.CalendarPreferences(ctx)
	if err != nil {
		cur = api.DefaultCalendarPreferences()
	}
	u := p.Preferences
	if u.WeekStart != nil {
		switch *u.WeekStart {
		case api.WeekStartLocale, api.WeekStartMonday, api.WeekStartSunday, api.WeekStartSaturday:
			cur.WeekStart = *u.WeekStart
		default:
			return nil, api.NewError(api.CodeInvalidArgument, "bad weekStart")
		}
	}
	if u.DefaultDurationMinutes != nil {
		if v := *u.DefaultDurationMinutes; v < 5 || v > 1440 {
			return nil, api.NewError(api.CodeInvalidArgument, "defaultDurationMinutes out of 5–1440")
		}
		cur.DefaultDurationMinutes = *u.DefaultDurationMinutes
	}
	if u.DefaultAlertMinutes != nil {
		if v := *u.DefaultAlertMinutes; v < -1 || v > api.MaxAlertMinutes {
			return nil, api.NewError(api.CodeInvalidArgument, "defaultAlertMinutes out of -1–%d", api.MaxAlertMinutes)
		}
		cur.DefaultAlertMinutes = *u.DefaultAlertMinutes
	}
	if u.ShowDeclined != nil {
		cur.ShowDeclined = *u.ShowDeclined
	}
	if u.WorkingHours != nil {
		if err := validWorkingHours(*u.WorkingHours); err != nil {
			return nil, err
		}
		cur.WorkingHours = api.WorkingHours{Days: slices.Clone(u.WorkingHours.Days), Start: u.WorkingHours.Start, End: u.WorkingHours.End}
	}
	if err := b.store.SetCalendarPreferences(ctx, cur); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if eng := b.calendarEngine(); eng != nil {
		eng.Reload()
	}
	b.notifyCalendarChanged(nil, nil)
	return &api.CalendarSetPreferencesResult{Preferences: cur}, nil
}
