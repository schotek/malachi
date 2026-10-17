// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarPreferences.swift;
// GTK: ui/internal/calendar/preferences.go (PrefsSectionKind, PrefsRow,
// PrefsSection, PreferencesLayout, TabTitle,
// DisplayGroup, NoCalendarsTitle, NoCalendarsBody, RemoveAccount,
// WeekStartSetting, WeekStarts, WeekStartTitle, DurationSetting,
// AlertSetting, DeclinedSetting, WorkingHoursSetting, WorkingHoursSubtitle,
// WorkingDaysText, sortedDays, WorkingHoursText, AccountsGroup, SyncSetting,
// SyncConsentNote, CalendarRowNote, DefaultCalendarSetting, Writable,
// KeepSetting, KeepChoices, KeepTitle, InvitationsGroup, CardSetting,
// TentativeSetting, TentativeSubtitle, AfterAnswerSetting, AfterAnswers,
// AfterAnswerTitle, NoCalendar, AddCalDAV, AddCalendar, SyncMicrosoft,
// MailAccountSetting, ReplacePassword, ReplacePasswordTitle, NoMailAccount)
// and backend/pkg/api's DefaultCalendarPreferences (Swift
// Cal.defaultPreferences).
//
// Preferences → Calendar, a tab of its own (the owner's decision of
// 2026-10-16, docs/calendar.md §5.8): the group Display with the daemon's
// calendar.preferences, then a section per account with the calendar
// capability (its CalendarConfig, and for a CalDAV account its connection),
// then Add CalDAV Calendar…. PreferencesLayout says what goes where; CalDAV
// accounts are listed on Preferences → Integrations (Model/Integrations.cs),
// whose Edit leads here, never on Accounts. The
// functions of preferences.go are Cal.Prefs (Swift's namespace), its types
// are Cal's (Go's names).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>
    /// api.DefaultCalendarPreferences (Swift <c>Cal.defaultPreferences</c>):
    /// the daemon's defaults: the locale's week, an hour, an alert 15 minutes
    /// before, declined events hidden, Monday to Friday 08:00–17:00.
    /// </summary>
    public static Api.CalendarPreferences DefaultPreferences { get; } = new()
    {
        WeekStart = Api.WeekStart.Locale,
        DefaultDurationMinutes = 60,
        DefaultAlertMinutes = 15,
        ShowDeclined = false,
        WorkingHours = new WorkingHours { Days = ["MO", "TU", "WE", "TH", "FR"], Start = "08:00", End = "17:00" },
    };

    /// <summary>preferences.go <c>PrefsSectionKind</c>: a section of the Calendar tab.</summary>
    public enum PrefsSectionKind
    {
        /// <summary>The group Display (<see cref="Prefs.DisplayGroup"/>).</summary>
        Display,

        /// <summary>One account's section, headed by its name and kind capsule (no msgid).</summary>
        Account,

        /// <summary>
        /// The empty state when no account has the calendar capability
        /// (<see cref="Prefs.NoCalendarsTitle"/>, <see cref="Prefs.NoCalendarsBody"/>).
        /// </summary>
        Empty,

        /// <summary>The Add CalDAV Calendar… button at the bottom.</summary>
        Add,
    }

    /// <summary>preferences.go <c>PrefsRow</c>: a row of a section.</summary>
    public enum PrefsRow
    {
        /// <summary>Display: <see cref="Prefs.WeekStartSetting"/>.</summary>
        WeekStart,

        /// <summary>Display: <see cref="Prefs.DurationSetting"/>.</summary>
        Duration,

        /// <summary>Display: <see cref="Prefs.AlertSetting"/>.</summary>
        Alert,

        /// <summary>Display: <see cref="Prefs.DeclinedSetting"/>.</summary>
        Declined,

        /// <summary>Display: <see cref="Prefs.WorkingHoursSetting"/>.</summary>
        WorkingHours,

        /// <summary>A CalDAV account's connection: <c>CalDAVField(Server)</c>.</summary>
        Server,

        /// <summary>A CalDAV account's connection: <c>CalDAVField(Username)</c>.</summary>
        Username,

        /// <summary>A CalDAV account's connection: <see cref="Prefs.ReplacePassword"/>.</summary>
        ReplacePassword,

        /// <summary>A CalDAV account's connection: <see cref="Prefs.MailAccountSetting"/>.</summary>
        MailAccount,

        /// <summary>A CalDAV account's connection: the certificate (with Forget for a pin).</summary>
        Certificate,

        /// <summary>Calendar rows: <see cref="Prefs.SyncSetting"/> (Graph only).</summary>
        Sync,

        /// <summary>Calendar rows: the calendars with checkbox and colour (<see cref="Prefs.CalendarRowNote(Api.Calendar)"/>).</summary>
        Calendars,

        /// <summary>Calendar rows: <see cref="Prefs.DefaultCalendarSetting"/>.</summary>
        DefaultCalendar,

        /// <summary>Calendar rows: <see cref="Prefs.KeepSetting"/>.</summary>
        Keep,

        /// <summary>Invitations rows (mail accounts only): <see cref="Prefs.CardSetting"/>.</summary>
        Card,

        /// <summary>Invitations rows (mail accounts only): <see cref="Prefs.TentativeSetting"/>.</summary>
        Tentative,

        /// <summary>Invitations rows (mail accounts only): <see cref="Prefs.AfterAnswerSetting"/>.</summary>
        AfterAnswer,

        /// <summary>A CalDAV account's <see cref="Prefs.RemoveAccount"/> button.</summary>
        Remove,
    }

    /// <summary>preferences.go <c>PrefsSection</c>: one section of the tab.</summary>
    public sealed record PrefsSection
    {
        /// <summary>What the section is.</summary>
        public required PrefsSectionKind Kind { get; init; }

        /// <summary>The account (<see cref="PrefsSectionKind.Account"/> only; null otherwise).</summary>
        public AccountId? AccountId { get; init; }

        /// <summary>The account's kind, <c>Config.Protocol()</c> (<see cref="PrefsSectionKind.Account"/> only).</summary>
        public AccountKind? AccountKind { get; init; }

        /// <summary>The rows, top to bottom.</summary>
        public IReadOnlyList<PrefsRow> Rows { get; init => field = value ?? []; } = [];
    }

    /// <summary>The texts and rules of Preferences → Calendar (Swift <c>Cal.Prefs</c>; Go's functions of preferences.go).</summary>
    public static partial class Prefs
    {
        /// <summary>
        /// preferences.go <c>PreferencesLayout</c>: the Calendar tab, top to
        /// bottom, for <paramref name="accounts"/> in their order: Display, a
        /// section per account with the calendar capability (or the empty
        /// state), Add CalDAV Calendar….
        /// </summary>
        public static IReadOnlyList<PrefsSection> PreferencesLayout(IReadOnlyList<Account>? accounts)
        {
            var output = new List<PrefsSection>
            {
                new()
                {
                    Kind = PrefsSectionKind.Display,
                    Rows = [PrefsRow.WeekStart, PrefsRow.Duration, PrefsRow.Alert, PrefsRow.Declined, PrefsRow.WorkingHours],
                },
            };
            var n = 0;
            foreach (var a in accounts ?? [])
            {
                if (a is null || !a.Can(Capability.Calendar))
                {
                    continue;
                }
                n++;
                var kind = a.Config.ProtocolKind;
                var caldav = kind == Api.AccountKind.CalDAV;
                var rows = new List<PrefsRow>();
                if (caldav)
                {
                    rows.AddRange([PrefsRow.Server, PrefsRow.Username, PrefsRow.ReplacePassword, PrefsRow.MailAccount, PrefsRow.Certificate]);
                }
                else
                {
                    rows.Add(PrefsRow.Sync);
                }
                rows.AddRange([PrefsRow.Calendars, PrefsRow.DefaultCalendar, PrefsRow.Keep]);
                // The invitation settings are a mail account's (the card in
                // Mail, archiving the message); a CalDAV account receives no
                // mail.
                if (caldav)
                {
                    rows.Add(PrefsRow.Remove);
                }
                else
                {
                    rows.AddRange([PrefsRow.Card, PrefsRow.Tentative, PrefsRow.AfterAnswer]);
                }
                output.Add(new PrefsSection { Kind = PrefsSectionKind.Account, AccountId = a.Id, AccountKind = kind, Rows = rows });
            }
            if (n == 0)
            {
                output.Add(new PrefsSection { Kind = PrefsSectionKind.Empty });
            }
            output.Add(new PrefsSection { Kind = PrefsSectionKind.Add });
            return output;
        }

        /// <summary>preferences.go <c>TabTitle</c>: the Preferences tab's title.</summary>
        public static string TabTitle => Text.ModeName;

        /// <summary>preferences.go <c>DisplayGroup</c>: heads the tab's first group.</summary>
        public static string DisplayGroup =>
            // TRANSLATORS: Preferences → Calendar: the group of how the calendar
            // shows (week start, default duration and alert, working hours).
            L10n.C("calendar preferences", "Display");

        /// <summary>preferences.go <c>NoCalendarsTitle</c>: the tab's empty state without calendar accounts.</summary>
        public static string NoCalendarsTitle => L10n.T("No calendars yet.");

        /// <summary>preferences.go <c>NoCalendarsBody</c>: explains the empty state.</summary>
        public static string NoCalendarsBody =>
            L10n.T("Add a CalDAV calendar, or switch on calendar sync for a Microsoft 365 account in its section.");

        /// <summary>preferences.go <c>RemoveAccount</c>: a CalDAV account's button that removes it (the Accounts tab's msgid).</summary>
        public static string RemoveAccount => L10n.T("Remove Account");

        /// <summary>preferences.go <c>WeekStartSetting</c>: the row of the week's first day.</summary>
        public static string WeekStartSetting => L10n.T("Week Starts On");

        /// <summary>preferences.go <c>WeekStarts</c>: its choices in order.</summary>
        public static IReadOnlyList<Api.WeekStart> WeekStarts { get; } =
            [Api.WeekStart.Locale, Api.WeekStart.Monday, Api.WeekStart.Sunday, Api.WeekStart.Saturday];

        /// <summary>preferences.go <c>WeekStartTitle</c>: a choice's title (the weekday's name, or System).</summary>
        public static string WeekStartTitle(Api.WeekStart w, Env env) => w.Value switch
        {
            Api.WeekStart.Monday => WeekdayName(Weekday.Monday, env),
            Api.WeekStart.Sunday => WeekdayName(Weekday.Sunday, env),
            Api.WeekStart.Saturday => WeekdayName(Weekday.Saturday, env),
            // TRANSLATORS: Week Starts On: the first day of the week of the
            // system's region.
            _ => L10n.T("System"),
        };

        /// <summary>preferences.go <c>DurationSetting</c>: the row of a new event's length.</summary>
        public static string DurationSetting => L10n.T("Default Duration");

        /// <summary>preferences.go <c>AlertSetting</c>: the row of a new event's alert.</summary>
        public static string AlertSetting => L10n.T("Default Alert");

        /// <summary>preferences.go <c>DeclinedSetting</c>: the switch that shows declined events (struck).</summary>
        public static string DeclinedSetting => L10n.T("Show Declined Events");

        /// <summary>preferences.go <c>WorkingHoursSetting</c>: the working hours' row.</summary>
        public static string WorkingHoursSetting => L10n.T("Working Hours");

        /// <summary>preferences.go <c>WorkingHoursSubtitle</c>: explains what the working hours do.</summary>
        public static string WorkingHoursSubtitle =>
            L10n.T("Where the day and week views open, and the hours Find a Free Slot looks in.");

        /// <summary>
        /// preferences.go <c>WorkingDaysText</c>: lists the working days
        /// ("Monday, Tuesday and Wednesday", "Every weekday" for Monday to
        /// Friday).
        /// </summary>
        public static string WorkingDaysText(WorkingHours wh, Env env)
        {
            ArgumentNullException.ThrowIfNull(wh);
            var days = SortedDays(wh.Days);
            if (days.SequenceEqual(["MO", "TU", "WE", "TH", "FR"]))
            {
                return L10n.T("Every weekday");
            }
            var names = new List<string>(days.Count);
            foreach (var c in days)
            {
                if (ParseDayCode(c) is { } d)
                {
                    names.Add(WeekdayName(d.Day, env));
                }
            }
            return ListText(names);
        }

        /// <summary>preferences.go <c>sortedDays</c>: the valid day codes without an ordinal and without duplicates, from Monday.</summary>
        internal static IReadOnlyList<string> SortedDays(IReadOnlyList<string>? codes)
        {
            var days = new List<Weekday>();
            foreach (var c in codes ?? [])
            {
                if (ParseDayCode(c) is { Ordinal: 0 } d && !days.Contains(d.Day))
                {
                    days.Add(d.Day);
                }
            }
            return [.. FromMonday(days).Select(DayCode)];
        }

        /// <summary>preferences.go <c>WorkingHoursText</c>: the row's value, "Every weekday · 08:00–17:00".</summary>
        public static string WorkingHoursText(WorkingHours wh, Env env)
        {
            ArgumentNullException.ThrowIfNull(wh);
            ArgumentNullException.ThrowIfNull(env);
            var days = WorkingDaysText(wh, env);
            if (ParseClock(wh.Start) is not { } from || ParseClock(wh.End) is not { } to)
            {
                return days;
            }
            // Any day without a clock change will do.
            DateTimeOffset At(int m) => Date(2026, 10, 5, m / 60, m % 60, env.Zone);
            return Text.Dots(days, Text.RangeText(Text.TimeText(At(from), env), Text.TimeText(At(to), env)));
        }

        /// <summary>preferences.go <c>AccountsGroup</c>: heads an account's Calendar rows.</summary>
        public static string AccountsGroup => Text.ModeName;

        /// <summary>preferences.go <c>SyncSetting</c>: the switch of a Graph account's calendar sync.</summary>
        public static string SyncSetting => L10n.T("Sync This Account’s Calendars");

        /// <summary>
        /// preferences.go <c>SyncConsentNote</c>: explains, before the
        /// sign-in, why switching sync on asks for a new sign-in.
        /// </summary>
        public static string SyncConsentNote =>
            L10n.T("Microsoft asks you to allow Malachi Mail to read and change your calendars. Your mail keeps working if you don’t.");

        /// <summary>
        /// preferences.go <c>CalendarRowNote</c>: the note after a calendar's
        /// name: "default", "read only", "shared · Jana Nováková"; "" for none.
        /// </summary>
        public static string CalendarRowNote(Api.Calendar c)
        {
            ArgumentNullException.ThrowIfNull(c);
            return CalendarRowNote(c.IsDefault == true, c.Access, c.Owner);
        }

        /// <summary>
        /// <see cref="CalendarRowNote(Api.Calendar)"/> of its parts (Swift's
        /// overload, for a found CalDAV calendar, which has no owner).
        /// </summary>
        public static string CalendarRowNote(bool isDefault, CalendarAccess access, Address? owner)
        {
            string? isDefaultNote = null;
            string? other = null;
            if (isDefault)
            {
                // TRANSLATORS: after the name of an account's calendar for new events.
                isDefaultNote = L10n.T("default");
            }
            if (owner is not null)
            {
                // TRANSLATORS: after the name of a calendar someone shared with the
                // user; %s is their name.
                other = L10n.T("shared · %s", NameOf(owner));
            }
            else if (access == CalendarAccess.Read)
            {
                // TRANSLATORS: after the name of a calendar the user cannot change.
                other = L10n.T("read only");
            }
            return Text.Dots(isDefaultNote, other);
        }

        /// <summary>preferences.go <c>DefaultCalendarSetting</c>: the popup of the calendar for new events.</summary>
        public static string DefaultCalendarSetting => L10n.T("Default Calendar for New Events");

        /// <summary>preferences.go <c>Writable</c>: a calendar new events may go to.</summary>
        public static bool Writable(Api.Calendar c)
        {
            ArgumentNullException.ThrowIfNull(c);
            return (c.Access == CalendarAccess.Owner || c.Access == CalendarAccess.Write)
                && c.Kind != CalendarKind.Birthdays && c.Kind != CalendarKind.Holidays;
        }

        /// <summary>preferences.go <c>KeepSetting</c>: the popup of how far back events are kept.</summary>
        public static string KeepSetting => L10n.T("Keep Events");

        /// <summary>preferences.go <c>KeepChoices</c>: its values in days (<see cref="CalendarConfig.KeepDays"/>).</summary>
        public static IReadOnlyList<int> KeepChoices { get; } = [30, 90, 180, 365, 730];

        /// <summary>
        /// preferences.go <c>KeepTitle</c>: a choice's title: "1 month", "3
        /// months", "6 months", "1 year", "2 years" (other values in days).
        /// </summary>
        public static string KeepTitle(int days)
        {
            if (days > 0 && days % 365 == 0)
            {
                // TRANSLATORS: Keep Events: how far back events are kept.
                return L10n.N("%d year", "%d years", days / 365);
            }
            if (days > 0 && days % 30 == 0)
            {
                // TRANSLATORS: Keep Events: how far back events are kept.
                return L10n.N("%d month", "%d months", days / 30);
            }
            return DaysText(days);
        }

        /// <summary>preferences.go <c>InvitationsGroup</c>: heads the account's Invitations group.</summary>
        public static string InvitationsGroup => UnansweredHeading;

        /// <summary>preferences.go <c>CardSetting</c>: the invitation card's switch.</summary>
        public static string CardSetting => L10n.T("Show the Invitation Card above the Message in Mail");

        /// <summary>preferences.go <c>TentativeSetting</c>: the switch that draws unanswered invitations.</summary>
        public static string TentativeSetting => L10n.T("Show Unanswered Invitations Tentatively in the Calendar");

        /// <summary>preferences.go <c>TentativeSubtitle</c>: its subtitle.</summary>
        public static string TentativeSubtitle =>
            L10n.T("They appear dashed until you answer, and are not in your calendar before you accept.");

        /// <summary>preferences.go <c>AfterAnswerSetting</c>: the popup of what happens to the message.</summary>
        public static string AfterAnswerSetting =>
            // TRANSLATORS: the row of what happens to an invitation message after
            // the user answered it.
            L10n.T("After Answering, Move the Invitation Message");

        /// <summary>preferences.go <c>AfterAnswers</c>: its choices in order.</summary>
        public static IReadOnlyList<Api.AfterAnswer> AfterAnswers { get; } = [Api.AfterAnswer.Keep, Api.AfterAnswer.Archive];

        /// <summary>preferences.go <c>AfterAnswerTitle</c>: a choice's title (null and an unknown value are Keep).</summary>
        public static string AfterAnswerTitle(Api.AfterAnswer? a) =>
            a == Api.AfterAnswer.Archive
                // TRANSLATORS: After Answering, Move the Invitation Message: to the
                // archive.
                ? L10n.T("Archive")
                // TRANSLATORS: After Answering, Move the Invitation Message: nowhere.
                : L10n.T("Keep in Inbox");

        /// <summary>preferences.go <c>NoCalendar</c>: the inset of an IMAP account.</summary>
        public static string NoCalendar => L10n.T("This account has no calendar.");

        /// <summary>preferences.go <c>AddCalDAV</c>: the menu item and button that open the Add CalDAV sheet.</summary>
        public static string AddCalDAV => L10n.T("Add CalDAV Calendar…");

        /// <summary>preferences.go <c>AddCalendar</c>: the sidebar's menu button at the bottom.</summary>
        public static string AddCalendar => L10n.T("Add Calendar…");

        /// <summary>preferences.go <c>SyncMicrosoft</c>: the Add Calendar menu's item that opens Preferences → Calendar.</summary>
        public static string SyncMicrosoft => L10n.T("Sync a Microsoft 365 Account’s Calendars…");

        /// <summary>preferences.go <c>MailAccountSetting</c>: a CalDAV account's popup of the mail account invitations leave from.</summary>
        public static string MailAccountSetting => L10n.T("Mail Account for Invitations");

        /// <summary>preferences.go <c>ReplacePassword</c>: the CalDAV account's button that asks for a new password.</summary>
        public static string ReplacePassword =>
            // TRANSLATORS: the button in Preferences → Calendar of a CalDAV account
            // that asks for a new password for the backend's keyring.
            L10n.T("Replace Password…");

        /// <summary>preferences.go <c>ReplacePasswordTitle</c>: the title of the alert <see cref="ReplacePassword"/> opens.</summary>
        public static string ReplacePasswordTitle =>
            // TRANSLATORS: the title of the alert that asks for a CalDAV account's
            // new password.
            L10n.T("Replace the Password?");

        /// <summary>preferences.go <c>NoMailAccount</c>: that popup's empty choice.</summary>
        public static string NoMailAccount =>
            // TRANSLATORS: Mail Account for Invitations: no account; invitations
            // cannot be sent.
            L10n.C("mail account", "None");
    }
}
