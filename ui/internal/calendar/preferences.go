// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"fmt"
	"slices"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Preferences → Calendar, a tab of its own (the owner's decision of
// 2026-10-16, replacing the placement of artboard 6): the group Display
// with the daemon's calendar.preferences, then a section per account with
// the calendar capability (its CalendarConfig, and for a CalDAV account
// its connection), then Add CalDAV Calendar…. PreferencesLayout says what
// goes where; CalDAV accounts are not in the Accounts tab at all.

// PrefsSectionKind is a section of the Calendar tab.
type PrefsSectionKind int

// The sections.
const (
	// SectionDisplay is the group Display (DisplayGroup).
	SectionDisplay PrefsSectionKind = iota
	// SectionAccount is one account's section, headed by its name and kind
	// capsule (no msgid).
	SectionAccount
	// SectionEmpty is the empty state when no account has the calendar
	// capability (NoCalendarsTitle, NoCalendarsBody).
	SectionEmpty
	// SectionAdd is the Add CalDAV Calendar… button at the bottom.
	SectionAdd
)

// PrefsRow is a row of a section.
type PrefsRow int

// The rows, by section.
const (
	// Display: WeekStartSetting, DurationSetting, AlertSetting,
	// DeclinedSetting, WorkingHoursSetting.
	RowWeekStart PrefsRow = iota
	RowDuration
	RowAlert
	RowDeclined
	RowWorkingHours
	// A CalDAV account's connection: CalDAVField(CalDAVServer),
	// CalDAVField(CalDAVUsername), ReplacePassword, MailAccountSetting,
	// CertificateLabel (with Forget for a pin).
	RowServer
	RowUsername
	RowReplacePassword
	RowMailAccount
	RowCertificate
	// Calendar rows: SyncSetting (Graph only), the calendars with checkbox
	// and colour (CalendarRowNote), DefaultCalendarSetting, KeepSetting.
	RowSync
	RowCalendars
	RowDefaultCalendar
	RowKeep
	// Invitations rows (under InvitationsGroup; mail accounts only):
	// CardSetting, TentativeSetting, AfterAnswerSetting.
	RowCard
	RowTentative
	RowAfterAnswer
	// RowRemove is a CalDAV account's RemoveAccount button.
	RowRemove
)

// PrefsSection is one section of the tab.
type PrefsSection struct {
	Kind PrefsSectionKind
	// AccountID and Kind of the account (SectionAccount only).
	AccountID   api.AccountID
	AccountKind api.AccountKind
	Rows        []PrefsRow
}

// PreferencesLayout is the Calendar tab, top to bottom, for the accounts
// in their order: Display, a section per account with the calendar
// capability (or the empty state), Add CalDAV Calendar….
func PreferencesLayout(accounts []api.Account) []PrefsSection {
	out := []PrefsSection{{Kind: SectionDisplay,
		Rows: []PrefsRow{RowWeekStart, RowDuration, RowAlert, RowDeclined, RowWorkingHours}}}
	invitations := []PrefsRow{RowCard, RowTentative, RowAfterAnswer}
	n := 0
	for _, a := range accounts {
		if !a.Can(api.CapabilityCalendar) {
			continue
		}
		n++
		s := PrefsSection{Kind: SectionAccount, AccountID: a.ID, AccountKind: a.Config.Protocol()}
		if s.AccountKind == api.AccountCalDAV {
			s.Rows = append(s.Rows, RowServer, RowUsername, RowReplacePassword, RowMailAccount, RowCertificate)
		} else {
			s.Rows = append(s.Rows, RowSync)
		}
		s.Rows = append(s.Rows, RowCalendars, RowDefaultCalendar, RowKeep)
		// The invitation settings are a mail account's (the card in Mail,
		// archiving the message); a CalDAV account receives no mail.
		if s.AccountKind == api.AccountCalDAV {
			s.Rows = append(s.Rows, RowRemove)
		} else {
			s.Rows = append(s.Rows, invitations...)
		}
		out = append(out, s)
	}
	if n == 0 {
		out = append(out, PrefsSection{Kind: SectionEmpty})
	}
	return append(out, PrefsSection{Kind: SectionAdd})
}

// ShowsInAccountsTab reports an account the Accounts tab lists: every
// kind but caldav (its section is in the Calendar tab).
func ShowsInAccountsTab(a api.Account) bool { return a.Config.Protocol() != api.AccountCalDAV }

// TabTitle is the Preferences tab's title.
func TabTitle(tr Translator) string { return ModeName(tr) }

// DisplayGroup heads the tab's first group.
func DisplayGroup(tr Translator) string {
	// TRANSLATORS: Preferences → Calendar: the group of how the calendar
	// shows (week start, default duration and alert, working hours).
	return tr.C("calendar preferences", "Display")
}

// NoCalendarsTitle is the tab's empty state without calendar accounts.
func NoCalendarsTitle(tr Translator) string { return tr.T("No calendars yet.") }

// NoCalendarsBody explains the empty state.
func NoCalendarsBody(tr Translator) string {
	return tr.T("Add a CalDAV calendar, or switch on calendar sync for a Microsoft 365 account in its section.")
}

// RemoveAccount is a CalDAV account's button that removes it (the
// Accounts tab's msgid).
func RemoveAccount(tr Translator) string { return tr.T("Remove Account") }

// WeekStartSetting is the row of the week's first day.
func WeekStartSetting(tr Translator) string { return tr.T("Week Starts On") }

// WeekStarts are its choices in order.
var WeekStarts = []api.WeekStart{api.WeekStartLocale, api.WeekStartMonday, api.WeekStartSunday, api.WeekStartSaturday}

// WeekStartTitle is a choice's title.
func WeekStartTitle(w api.WeekStart, env Env) string {
	switch w {
	case api.WeekStartMonday:
		return weekdayName(1, env)
	case api.WeekStartSunday:
		return weekdayName(0, env)
	case api.WeekStartSaturday:
		return weekdayName(6, env)
	}
	// TRANSLATORS: Week Starts On: the first day of the week of the
	// system's region.
	return env.Tr.T("System")
}

// DurationSetting is the row of a new event's length (DurationChoices).
func DurationSetting(tr Translator) string { return tr.T("Default Duration") }

// AlertSetting is the row of a new event's alert (AlertChoices).
func AlertSetting(tr Translator) string { return tr.T("Default Alert") }

// DeclinedSetting is the switch that shows declined events (struck).
func DeclinedSetting(tr Translator) string { return tr.T("Show Declined Events") }

// WorkingHoursSetting is the working hours' row.
func WorkingHoursSetting(tr Translator) string { return tr.T("Working Hours") }

// WorkingHoursSubtitle explains what the working hours do.
func WorkingHoursSubtitle(tr Translator) string {
	return tr.T("Where the day and week views open, and the hours Find a Free Slot looks in.")
}

// WorkingDaysText lists the working days ("Monday, Tuesday and
// Wednesday", "Every weekday" for Monday to Friday).
func WorkingDaysText(wh api.WorkingHours, env Env) string {
	days := sortedDays(wh.Days)
	if slices.Equal(days, []string{"MO", "TU", "WE", "TH", "FR"}) {
		return env.Tr.T("Every weekday")
	}
	names := make([]string, 0, len(days))
	for _, c := range days {
		if d, _, ok := ParseDayCode(c); ok {
			names = append(names, weekdayName(d, env))
		}
	}
	return ListText(names, env.Tr)
}

// sortedDays are the valid day codes without duplicates from Monday.
func sortedDays(codes []string) []string {
	var days []time.Weekday
	for _, c := range codes {
		if d, ord, ok := ParseDayCode(c); ok && ord == 0 && !slices.Contains(days, d) {
			days = append(days, d)
		}
	}
	slices.SortFunc(days, func(a, b time.Weekday) int { return (int(a)+6)%7 - (int(b)+6)%7 })
	out := make([]string, len(days))
	for i, d := range days {
		out[i] = DayCode(d)
	}
	return out
}

// WorkingHoursText is the row's value: "Every weekday · 08:00–17:00".
func WorkingHoursText(wh api.WorkingHours, env Env) string {
	from, ok1 := ParseClock(wh.Start)
	to, ok2 := ParseClock(wh.End)
	days := WorkingDaysText(wh, env)
	if !ok1 || !ok2 {
		return days
	}
	// Any day without a clock change will do.
	at := func(m int) time.Time { return time.Date(2026, time.October, 5, m/60, m%60, 0, 0, env.loc()) }
	return dots(env.Tr, days, RangeText(TimeText(at(from), env), TimeText(at(to), env), env.Tr))
}

// AccountsGroup heads an account's Calendar rows in Preferences →
// Calendar.
func AccountsGroup(tr Translator) string { return ModeName(tr) }

// SyncSetting is the switch of a Graph account's calendar sync.
func SyncSetting(tr Translator) string { return tr.T("Sync This Account’s Calendars") }

// SyncConsentNote explains, before the sign-in, why switching sync on
// asks for a new sign-in.
func SyncConsentNote(tr Translator) string {
	return tr.T("Microsoft asks you to allow Malachi Mail to read and change your calendars. Your mail keeps working if you don’t.")
}

// CalendarRowNote is the note after a calendar's name: "default", "read
// only", "shared · Jana Nováková"; "" for none.
func CalendarRowNote(c api.Calendar, tr Translator) string {
	var notes []string
	if c.IsDefault {
		// TRANSLATORS: after the name of an account's calendar for new events.
		notes = append(notes, tr.T("default"))
	}
	if c.Owner != nil {
		// TRANSLATORS: after the name of a calendar someone shared with the
		// user; %s is their name.
		notes = append(notes, fmt.Sprintf(tr.T("shared · %s"), nameOf(c.Owner)))
	} else if c.Access == api.CalendarAccessRead {
		// TRANSLATORS: after the name of a calendar the user cannot change.
		notes = append(notes, tr.T("read only"))
	}
	return dots(tr, notes...)
}

// DefaultCalendarSetting is the popup of the calendar for new events.
func DefaultCalendarSetting(tr Translator) string { return tr.T("Default Calendar for New Events") }

// Writable reports a calendar new events may go to.
func Writable(c api.Calendar) bool {
	return (c.Access == api.CalendarAccessOwner || c.Access == api.CalendarAccessWrite) &&
		c.Kind != api.CalendarKindBirthdays && c.Kind != api.CalendarKindHolidays
}

// KeepSetting is the popup of how far back events are kept.
func KeepSetting(tr Translator) string { return tr.T("Keep Events") }

// KeepChoices are its values in days (CalendarConfig.KeepDays).
var KeepChoices = []int{30, 90, 180, 365, 730}

// KeepTitle is a choice's title: "1 month", "3 months", "6 months", "1
// year", "2 years" (other values in days).
func KeepTitle(days int, tr Translator) string {
	switch {
	case days > 0 && days%365 == 0:
		n := days / 365
		// TRANSLATORS: Keep Events: how far back events are kept.
		return fmt.Sprintf(tr.N("%d year", "%d years", n), n)
	case days > 0 && days%30 == 0:
		n := days / 30
		// TRANSLATORS: Keep Events: how far back events are kept.
		return fmt.Sprintf(tr.N("%d month", "%d months", n), n)
	}
	return DaysText(days, tr)
}

// InvitationsGroup heads the account's Invitations group.
func InvitationsGroup(tr Translator) string { return UnansweredHeading(tr) }

// CardSetting is the invitation card's switch.
func CardSetting(tr Translator) string {
	return tr.T("Show the Invitation Card above the Message in Mail")
}

// TentativeSetting is the switch that draws unanswered invitations.
func TentativeSetting(tr Translator) string {
	return tr.T("Show Unanswered Invitations Tentatively in the Calendar")
}

// TentativeSubtitle is its subtitle.
func TentativeSubtitle(tr Translator) string {
	return tr.T("They appear dashed until you answer, and are not in your calendar before you accept.")
}

// AfterAnswerSetting is the popup of what happens to the message.
func AfterAnswerSetting(tr Translator) string {
	// TRANSLATORS: the row of what happens to an invitation message after
	// the user answered it.
	return tr.T("After Answering, Move the Invitation Message")
}

// AfterAnswers are its choices in order.
var AfterAnswers = []api.AfterAnswer{api.AfterAnswerKeep, api.AfterAnswerArchive}

// AfterAnswerTitle is a choice's title.
func AfterAnswerTitle(a api.AfterAnswer, tr Translator) string {
	if a == api.AfterAnswerArchive {
		// TRANSLATORS: After Answering, Move the Invitation Message: to the
		// archive.
		return tr.T("Archive")
	}
	// TRANSLATORS: After Answering, Move the Invitation Message: nowhere.
	return tr.T("Keep in Inbox")
}

// NoCalendar is the inset of an IMAP account.
func NoCalendar(tr Translator) string { return tr.T("This account has no calendar.") }

// AddCalDAV is the menu item and button that open the Add CalDAV sheet.
func AddCalDAV(tr Translator) string { return tr.T("Add CalDAV Calendar…") }

// AddCalendar is the sidebar's menu button at the bottom.
func AddCalendar(tr Translator) string { return tr.T("Add Calendar…") }

// SyncMicrosoft is the Add Calendar menu's item that opens Preferences →
// Calendar.
func SyncMicrosoft(tr Translator) string {
	return tr.T("Sync a Microsoft 365 Account’s Calendars…")
}

// MailAccountSetting is a CalDAV account's popup of the mail account
// invitations leave from.
func MailAccountSetting(tr Translator) string { return tr.T("Mail Account for Invitations") }

// ReplacePassword is the CalDAV Account group's button that asks for a
// new password.
func ReplacePassword(tr Translator) string {
	// TRANSLATORS: the button in Preferences → Calendar of a CalDAV account
	// that asks for a new password for the backend's keyring.
	return tr.T("Replace Password…")
}

// ReplacePasswordTitle is the title of the alert ReplacePassword opens.
func ReplacePasswordTitle(tr Translator) string {
	// TRANSLATORS: the title of the alert that asks for a CalDAV account's
	// new password.
	return tr.T("Replace the Password?")
}

// NoMailAccount is that popup's empty choice.
func NoMailAccount(tr Translator) string {
	// TRANSLATORS: Mail Account for Invitations: no account; invitations
	// cannot be sent.
	return tr.C("mail account", "None")
}
