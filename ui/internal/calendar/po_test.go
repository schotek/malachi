// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// msgKey is a msgid of the template with its context and plural.
type msgKey struct{ ctx, msgid, plural string }

// recorder is a translator that notes every msgid asked for.
type recorder map[msgKey]bool

func (r recorder) T(msgid string) string { r[msgKey{msgid: msgid}] = true; return msgid }

func (r recorder) N(msgid, plural string, n int) string {
	r[msgKey{msgid: msgid, plural: plural}] = true
	if n == 1 {
		return msgid
	}
	return plural
}

func (r recorder) C(ctx, msgid string) string { r[msgKey{ctx: ctx, msgid: msgid}] = true; return msgid }

// exercise calls every function of the package that translates, down
// every branch that picks a msgid.
func exercise(tr Translator) {
	loc := time.UTC
	env := Env{Tr: tr, Format: EnglishStrftime, Loc: loc}
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, loc)
	s := SampleData(now, loc)
	exerciseText(tr, env, now)
	exerciseViews(tr, env, now, s)
	exerciseDetail(tr, env, now, s)
	exerciseRecurrence(tr, env, now)
	exerciseEditor(tr, env, now, s)
	exerciseInvitation(tr, env, now, s)
	exercisePreferences(tr, env, s)
	exerciseCalDAV(tr)
	exerciseKeys(tr)
}

func exerciseText(tr Translator, env Env, now time.Time) {
	ModeName(tr)
	for _, v := range Views {
		ViewTitle(v, tr)
	}
	Today(tr)
	Previous(tr)
	Next(tr)
	NewEvent(tr)
	EditEvent(tr)
	SearchEvents(tr)
	Untitled(tr)
	AllDay(tr)
	ago := now.Add(-time.Hour)
	for _, st := range []api.CalendarSyncStatus{api.CalendarSyncIdle, api.CalendarSyncSyncing, api.CalendarSyncOffline,
		api.CalendarSyncConsentRequired} {
		StatusLine([]api.CalendarSyncState{{Status: st, LastSync: &ago}}, now, tr)
	}
	UnansweredCount(1, tr)
	UnansweredRow(tr)
	ReloadEvent(tr)
	ReplacePassword(tr)
	ReplacePasswordTitle(tr)
	SignIn(tr)
	ConsentOnline(tr)
	for k := EmptyNoAccounts; k <= EmptyNoBackend; k++ {
		EmptyTitle(k, tr)
		EmptyBody(k, tr)
	}
	OpenPreferences(tr)
	PreviewOnly(tr)
	for _, c := range []api.ErrorCode{api.CodeReadOnly, api.CodeEventGone, api.CodeCalendarNotFound, api.CodeConflict,
		api.CodeInvitationsUnavailable, api.CodeCalendarConsentRequired, api.CodeRecurrenceUnsupported,
		api.CodeMalformedCalendar, api.CodeOffline} {
		ErrorText(c, tr)
	}
	for _, f := range []func(time.Time, Env) string{TimeText, LongDateText, WeekdayDateText, DayMonthText,
		DayMonthYearText, DayText, MonthYearText, MonthText, WeekdayText, ShortWeekdayText, ShortMonthText, DateTimeText} {
		f(now, env)
	}
	TimeRangeText(now, now.Add(time.Hour), env)
}

func exerciseViews(tr Translator, env Env, now time.Time, s Samples) {
	for _, v := range Views {
		for _, anchor := range []time.Time{now, time.Date(2026, 9, 30, 0, 0, 0, 0, env.Loc), time.Date(2026, 12, 30, 0, 0, 0, 0, env.Loc)} {
			r := ViewRange(v, anchor, time.Monday, env.Loc)
			Title(v, r, env)
			Subtitle(v, r, time.Monday, env)
		}
	}
	WeekdayHeaders(time.Monday, env)
	More(2, tr)
	for b := BlockNormal; b <= BlockCancelled; b++ {
		BlockStateText(b, tr)
	}
	for _, o := range s.Occurrences {
		BlockLabel(o, env)
	}
	BlockLabel(api.Occurrence{}, env)
	for _, d := range Agenda(s.Occurrences, AddDays(StartOfDay(now, env.Loc), -3), 10, now, env, true) {
		for _, it := range d.Items {
			ItemTime(it, env)
		}
	}
	ItemTime(AgendaItem{Occurrence: api.Occurrence{}, Days: 3, DayOf: 1}, env)
	ItemTime(AgendaItem{Occurrence: api.Occurrence{}, Days: 3, DayOf: 2}, env)
	ItemTime(AgendaItem{Occurrence: api.Occurrence{}, Days: 3, DayOf: 3}, env)
	DayHeading(AddDays(now, -1), now, env)
	DayHeading(AddDays(now, 1), now, env)
	DayHeading(AddDays(now, 5), now, env)
	DayHeading(AddDays(now, 400), now, env)
	DayOfText(1, 2, tr)
	AgendaEmpty(tr)
	ResultsHeading(tr)
	NoResults(tr)
	UnansweredHeading(tr)
	TodayRows(s.Occurrences, now, env, true)
	TodayHeading(tr)
	OpenCalendar(tr)
	TookPlace(tr)
	Respond(tr)
	NothingToday(tr)
	start := now.Add(time.Minute)
	for _, a := range []api.EventAlert{{Start: &now}, {Start: &start}, {Start: ptr(now.Add(3 * time.Hour))},
		{Start: ptr(now.Add(48 * time.Hour))}, {AllDay: true, StartDate: DateOf(now, env.Loc)},
		{AllDay: true, StartDate: DateOf(AddDays(now, 2), env.Loc)}} {
		AlertNotification(a, now, env)
	}
	Join(tr)
	Snooze(tr)
	org := &api.Address{Name: "O", Address: "o@example.org"}
	for _, k := range []api.InvitationKind{api.InvitationKindInvitation, api.InvitationKindUpdate} {
		InvitationNotification(api.InvitationSummary{Kind: k, Start: &now, End: &start, Organizer: org}, env)
		InvitationNotification(api.InvitationSummary{Kind: k, AllDay: true, StartDate: DateOf(now, env.Loc)}, env)
	}
	Show(tr)
	CancellationNotification(api.InvitationSummary{Organizer: org}, env)
	CancellationNotification(api.InvitationSummary{}, env)
}

func exerciseDetail(tr Translator, env Env, now time.Time, s Samples) {
	for _, e := range s.Events {
		DetailOf(e, "Work", "Calendar", env)
		RemoveQuestion(e, tr)
		DeleteQuestion(e, tr)
		PeopleLine(e, tr)
	}
	PeopleLine(api.Event{Occurrence: api.Occurrence{AttendeeCount: 2}}, tr)
	RemoveQuestion(api.Event{Occurrence: api.Occurrence{AttendeeCount: 2}, RemoveIsDelete: true}, tr)
	PeopleLine(api.Event{Occurrence: api.Occurrence{AttendeeCount: 2, Organizer: true}}, tr)
	TimeLine(api.Occurrence{Start: &now, End: ptr(now.Add(50 * time.Hour))}, env)
	TimeLine(allDayOcc("a", now, 1), env)
	TimeLine(allDayOcc("a", now, 3), env)
	DurationText(90*time.Minute, tr)
	DurationText(48*time.Hour, tr)
	for _, r := range []api.EventResponse{api.ResponseAccepted, api.ResponseTentative, api.ResponseDeclined,
		api.ResponseDelegated, api.ResponseNeedsAction} {
		ResponseLabel(r, tr)
		MyResponseText(r, tr)
		ResponseAction(r, tr)
	}
	DeclineWithoutReply(tr)
	OrganizerLabel(tr)
	OptionalLabel(tr)
	ShowAllAttendees(8, tr)
	ShowInvitation(tr)
	Edit(tr)
	JoinVideoCall(tr)
	AskAboutEvent(tr)
	FindAnotherTime(tr)
	RemoveFromCalendar(tr)
	res := api.CalendarFreeBusyResult{Attendees: []api.Availability{{Address: "a@example.org", Known: true},
		{Address: "b@example.org", Known: true, Busy: []api.BusyInterval{{Start: now, End: now.Add(time.Hour), ShowAs: api.ShowAsBusy}}},
		{Address: "c@example.org"}}}
	AvailabilityRows(res, nil, now, now.Add(8*time.Hour), tr)
	Summary(res, now, now.Add(time.Hour), env)
	Summary(res, now.Add(2*time.Hour), now.Add(3*time.Hour), env)
	Unknown(tr)
	NextFreeSlotButton(tr)
	NextFreeSlotText(api.TimeSlot{Start: now, End: now.Add(time.Hour)}, env)
	NoFreeSlot(tr)
}

func exerciseRecurrence(tr Translator, env Env, now time.Time) {
	rules := []api.Recurrence{
		{Frequency: api.FrequencyDaily, Interval: 1},
		{Frequency: api.FrequencyDaily, Interval: 2, Count: 5},
		{Frequency: api.FrequencyWeekly, Interval: 1, ByDay: []string{"MO", "TU", "WE", "TH", "FR"}},
		{Frequency: api.FrequencyWeekly, Interval: 1, ByDay: []string{"MO", "WE", "FR"}, Until: "2027-03-01"},
		{Frequency: api.FrequencyWeekly, Interval: 2, ByDay: []string{"MO", "WE"}},
		{Frequency: api.FrequencyMonthly, Interval: 1, ByDay: []string{"1MO"}},
		{Frequency: api.FrequencyMonthly, Interval: 1, ByDay: []string{"2TU"}},
		{Frequency: api.FrequencyMonthly, Interval: 1, ByDay: []string{"3TU"}},
		{Frequency: api.FrequencyMonthly, Interval: 1, ByDay: []string{"4TU"}},
		{Frequency: api.FrequencyMonthly, Interval: 1, ByDay: []string{"5TU"}},
		{Frequency: api.FrequencyMonthly, Interval: 1, ByDay: []string{"-1FR"}},
		{Frequency: api.FrequencyMonthly, Interval: 1, ByDay: []string{"-2FR"}},
		{Frequency: api.FrequencyMonthly, Interval: 1, ByMonthDay: []int{-1}},
		{Frequency: api.FrequencyMonthly, Interval: 3, ByMonthDay: []int{14}},
		{Frequency: api.FrequencyYearly, Interval: 1},
		{Frequency: api.FrequencyYearly, Interval: 2, ByMonth: []int{10}, ByDay: []string{"2TU"}},
		{Frequency: api.FrequencyYearly, Interval: 1, Complex: true},
	}
	for _, r := range rules {
		RecurrenceText(&r, now, env)
	}
	for _, c := range Repeats {
		RepeatTitle(c, tr)
	}
	for _, f := range []api.RecurrenceFrequency{api.FrequencyDaily, api.FrequencyWeekly, api.FrequencyMonthly, api.FrequencyYearly} {
		IntervalUnit(f, 2, tr)
	}
	EveryLabel(tr)
	ListText([]string{"a", "b", "c"}, tr)
}

func exerciseEditor(tr Translator, env Env, now time.Time, s Samples) {
	for p := ProblemNoCalendar; p <= ProblemTooManyAlerts; p++ {
		ProblemText(p, tr)
	}
	PrimaryLabel(0, api.CalendarSourceCalDAV, api.CalendarFeatures{}, tr)
	PrimaryLabel(1, api.CalendarSourceGraph, api.CalendarFeatures{}, tr)
	Cancel(tr)
	for _, via := range []api.InvitationsVia{api.InvitationsViaMail, api.InvitationsViaServer, api.InvitationsViaNone} {
		Footer(1, api.CalendarFeatures{Invitations: via}, "Gmail", "me@example.com", []string{"a.pdf"}, tr)
	}
	for _, m := range append(AlertChoices, 120, 2880, 7) {
		AlertTitle(m, tr)
	}
	for _, m := range []int{900, 9540} {
		AlertTitleFor(m, true, tr)
	}
	for _, m := range DurationChoices {
		DurationTitle(m, tr)
	}
	TimeZoneLink("Europe/Prague", tr)
	ZoneSearch(tr)
	TitlePlaceholder(tr)
	for _, f := range Fields {
		FieldLabel(f, tr)
	}
	AddVideoCall(tr)
	PasteLink(tr)
	VideoProviderName("teamsForBusiness", tr)
	VideoProviderName("x", tr)
	Attach(tr)
	CloseQuestion(tr)
	for k := MailInvitation; k <= MailDeclined; k++ {
		MailTexts(k, s.Events[5].Occurrence, "Me", env)
	}
	for a := ScopeEdit; a <= ScopeRespond; a++ {
		ScopeQuestionFor(a, false, tr)
		ScopeQuestionFor(a, true, tr)
	}
}

func exerciseInvitation(tr Translator, env Env, now time.Time, s Samples) {
	for p := PillNone; p <= PillReply; p++ {
		PillTitle(p, tr)
	}
	name := func(api.CalendarID) string { return "Work" }
	for _, c := range s.Cards {
		DateTileOf(c.InvitationSummary, env)
		CardTimeLine(c, env)
		CardTitle(c.InvitationSummary, tr)
		CardPeopleLine(c, tr)
		PreviousLines(c, env)
		ConflictLine(c.Conflicts, name, env)
		ReplyNote(c, "Gmail", tr)
		StateStrip(c, "Personal", tr)
		ReplyCard(c, tr)
		StateLine(c, tr)
	}
	CardPeopleLine(api.InvitationCard{InvitationSummary: api.InvitationSummary{Organizer: &api.Address{Address: "o@example.org"}}}, tr)
	CardPeopleLine(api.InvitationCard{AttendeeCount: 2}, tr)
	ConflictLine([]api.InvitationConflict{{Start: now, End: now}}, name, env)
	ConflictLine([]api.InvitationConflict{{}, {}}, name, env)
	PreviousLines(api.InvitationCard{InvitationSummary: api.InvitationSummary{Start: &now}, Previous: &api.InvitationPrevious{Start: "2026-10-14", End: "2026-10-15",
		Location: "B", Recurrence: &api.Recurrence{Frequency: api.FrequencyDaily, Interval: 1}}}, env)
	org := &api.Address{Name: "O", Address: "o@example.org"}
	for _, via := range []api.InvitationsVia{api.InvitationsViaServer, api.InvitationsViaNone} {
		ReplyNote(api.InvitationCard{ReplyVia: via, ReplyTo: org, OrganizerMismatch: true}, "", tr)
	}
	for _, r := range []api.EventResponse{api.ResponseTentative, api.ResponseDeclined, api.ResponseDelegated} {
		ReplyCard(api.InvitationCard{Response: r}, tr)
	}
	StateLine(api.InvitationCard{InvitationSummary: api.InvitationSummary{State: api.InvitationUnanswered}}, tr)
	NoConflict(tr)
	StateLine(api.InvitationCard{InvitationSummary: api.InvitationSummary{Kind: api.InvitationKindInvitation, OwnEvent: true}}, tr)
	AddTo(tr)
	ChangeResponse(tr)
	ShowInCalendar(tr)
	ProposeNewTime(tr)
	Propose(tr)
	InvalidLine(tr)
	CancelledLine(tr)
}

func exercisePreferences(tr Translator, env Env, s Samples) {
	TabTitle(tr)
	DisplayGroup(tr)
	NoCalendarsTitle(tr)
	NoCalendarsBody(tr)
	RemoveAccount(tr)
	WeekStartSetting(tr)
	for _, w := range WeekStarts {
		WeekStartTitle(w, env)
	}
	DurationSetting(tr)
	AlertSetting(tr)
	DeclinedSetting(tr)
	WorkingHoursSetting(tr)
	WorkingHoursSubtitle(tr)
	WorkingHoursText(s.Preferences.WorkingHours, env)
	WorkingHoursText(api.WorkingHours{Days: []string{"MO", "WE"}, Start: "09:00", End: "12:00"}, env)
	AccountsGroup(tr)
	SyncSetting(tr)
	SyncConsentNote(tr)
	for _, a := range s.Calendars.Accounts {
		for _, c := range a.Calendars {
			CalendarRowNote(c, tr)
		}
	}
	DefaultCalendarSetting(tr)
	KeepSetting(tr)
	for _, d := range append(KeepChoices, 7) {
		KeepTitle(d, tr)
	}
	InvitationsGroup(tr)
	CardSetting(tr)
	TentativeSetting(tr)
	TentativeSubtitle(tr)
	AfterAnswerSetting(tr)
	for _, a := range AfterAnswers {
		AfterAnswerTitle(a, tr)
	}
	NoCalendar(tr)
	AddCalDAV(tr)
	AddCalendar(tr)
	SyncMicrosoft(tr)
	MailAccountSetting(tr)
	NoMailAccount(tr)
}

func exerciseCalDAV(tr Translator) {
	CalDAVTitle(tr)
	CalDAVSubtitle(tr)
	for f := CalDAVName; f <= CalDAVPassword; f++ {
		CalDAVField(f, tr)
	}
	PasswordNote(tr)
	DiscoverButton(tr)
	w := NewWizard("")
	w.Edit("", "cloud.example.org", "me", "pw")
	w.BeginDiscover()
	WizardProgress(w, tr)
	w.DiscoverDone(&api.AccountDiscoverCalDAVResult{ServerURL: "https://dav.example.net", FoundVia: api.CalDAVFoundWellKnown,
		Certificate: &api.CertificateInfo{Issuer: "R11"}}, nil)
	ServerChanged(w, tr)
	CertificateText(w, tr)
	w.Discovered.Certificate.Issuer = ""
	CertificateText(w, tr)
	w.Discovered.ServerURL = "http://127.0.0.1:5232"
	w.Discovered.Certificate = nil
	CertificateText(w, tr)
	w.ListDone(&api.AccountListCalDAVCalendarsResult{Calendars: []api.CalDAVCalendar{{Href: "/a/"}}}, nil)
	FoundLine(w, tr)
	w.BeginAdd()
	WizardProgress(w, tr)
	w.Pin = "x"
	CertificateText(w, tr)
	for _, v := range []api.CalDAVFoundVia{api.CalDAVFoundSRV, api.CalDAVFoundURL} {
		FoundVia(v, tr)
	}
	for _, c := range []api.ErrorCode{api.CodeAuthFailed, api.CodeTLSError, api.CodeNetworkError, api.CodeServerError,
		api.CodeInvalidArgument, api.CodeKeyringError, api.CodeInternalError} {
		WizardErrorText(&Wizard{Err: &api.Error{Code: c}}, tr)
	}
	CertificateLabel(tr)
	TrustButton(tr)
	CalendarsHeading(tr)
	ListedNote(api.CalDAVCalendar{IsDefault: true, Access: api.CalendarAccessRead}, tr)
	CalDAVFooter(tr)
	AddButton(tr)
}

func exerciseKeys(tr Translator) {
	CalendarKeys(tr)
	KeysGroup(tr)
}

// template reads po/malachi.pot: every entry, and whether it names a file
// of this package among its references.
func template(t *testing.T) map[msgKey]bool {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "..", "po", "malachi.pot"))
	if err != nil {
		t.Fatalf("open the template: %v", err)
	}
	defer f.Close()
	field := regexp.MustCompile(`^(msgctxt|msgid_plural|msgid|msgstr(?:\[\d\])?) (".*")$`)
	out := map[msgKey]bool{}
	var cur msgKey
	var ours bool
	var last *string
	flush := func() {
		if cur.msgid != "" {
			out[cur] = ours
		}
		cur, ours, last = msgKey{}, false, nil
	}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		switch {
		case strings.TrimSpace(line) == "":
			flush()
		case strings.HasPrefix(line, "#:"):
			ours = ours || strings.Contains(line, "ui/internal/calendar/")
		case strings.HasPrefix(line, "\""):
			if last != nil {
				*last += unquote(t, line)
			}
		default:
			m := field.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			last = nil
			switch m[1] {
			case "msgctxt":
				last = &cur.ctx
			case "msgid":
				last = &cur.msgid
			case "msgid_plural":
				last = &cur.plural
			}
			if last != nil {
				*last = unquote(t, m[2])
			}
		}
	}
	flush()
	if err := s.Err(); err != nil {
		t.Fatalf("read the template: %v", err)
	}
	return out
}

func unquote(t *testing.T, s string) string {
	t.Helper()
	u, err := strconv.Unquote(s)
	if err != nil {
		t.Fatalf("template string %s: %v", s, err)
	}
	return u
}

// TestMsgidsInTemplate keeps po/malachi.pot in step with the package on a
// machine without make lint: every msgid it translates (its own and the
// ones it reuses) is in the template with its context and plural, and
// every entry that names a file of the package is one it translates.
func TestMsgidsInTemplate(t *testing.T) {
	used := recorder{}
	exercise(used)
	pot := template(t)
	for k := range used {
		if _, ok := pot[k]; !ok {
			t.Errorf("po/malachi.pot lacks msgid %q (context %q, plural %q)", k.msgid, k.ctx, k.plural)
		}
	}
	own := 0
	for k, ours := range pot {
		if !ours {
			continue
		}
		own++
		if !used[k] {
			t.Errorf("po/malachi.pot names ui/internal/calendar for msgid %q (context %q), which the package does not translate", k.msgid, k.ctx)
		}
	}
	if own == 0 {
		t.Error("po/malachi.pot names no file of ui/internal/calendar")
	}
}
