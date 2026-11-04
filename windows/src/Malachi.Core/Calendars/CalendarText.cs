// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarText.swift; GTK:
// ui/internal/calendar/text.go (ModeName, ViewTitle, Today, Previous, Next,
// NewEvent, EditEvent, SearchEvents, Untitled, AllDay, SyncedLine,
// SyncingLine, OfflineLine, UnansweredCount, UnansweredRow, ConsentLine,
// SignIn, ConsentOnline, StatusLine, EmptyKind, EmptyTitle, EmptyBody,
// OpenPreferences, PreviewOnly, ReloadEvent, ErrorText, TimeText,
// LongDateText, WeekdayDateText, DayMonthText, DayMonthYearText, DayText,
// MonthYearText, MonthText, WeekdayText, ShortWeekdayText, ShortMonthText,
// DateTimeText, RangeText, Dot, dots, TimeRangeText).
//
// The calendar's general texts: the mode, the views, the toolbar, the
// status bar, the empty states, the errors by code, and the date formats
// every other calendar file writes with (strftime msgids, so that a locale
// orders them its own way). Keys are the GTK msgids; the functions keep
// Go's names, and Cal.Text is partial, so the other calendar files add
// their texts beside these. Strings from servers are formatted in, never
// concatenated into sentences.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.I18n;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>text.go <c>EmptyKind</c>: why the calendar page has nothing to show.</summary>
    public enum EmptyKind
    {
        /// <summary>No account has the calendar capability.</summary>
        NoAccounts,

        /// <summary>Accounts could have calendars, none syncs them.</summary>
        SyncOff,

        /// <summary>The first calendar.list has not answered.</summary>
        Loading,

        /// <summary>The daemon is not reachable.</summary>
        NoBackend,
    }

    /// <summary>The calendar's texts (Swift <c>Cal.Text</c>; Go's functions with the translator).</summary>
    public static partial class Text
    {
        /// <summary>
        /// The mode switch's segment, the View menu item and the window's
        /// title in Calendar mode (Go <c>board.CalendarName</c>).
        /// </summary>
        public static string ModeName => Board.Text.CalendarName;

        /// <summary>A view's segment in the toolbar and its View menu item.</summary>
        public static string ViewTitle(View v) => v switch
        {
            // TRANSLATORS: a view of the calendar: one day.
            View.Day => L10n.T("Day"),
            // TRANSLATORS: a view of the calendar: the month's grid.
            View.Month => L10n.T("Month"),
            // TRANSLATORS: a view of the calendar: a list of the coming events
            // by day.
            View.Agenda => L10n.T("Agenda"),
            // TRANSLATORS: a view of the calendar: seven days side by side.
            _ => L10n.T("Week"),
        };

        /// <summary>The toolbar button that shows today.</summary>
        public static string Today =>
            // TRANSLATORS: the calendar's toolbar button that goes to today.
            L10n.T("Today");

        /// <summary>The tooltip of ‹ (the previous day, week or month).</summary>
        public static string Previous =>
            // TRANSLATORS: the calendar's toolbar button ‹ and its key.
            L10n.T("Previous");

        /// <summary>The tooltip of › (the next day, week or month).</summary>
        public static string Next =>
            // TRANSLATORS: the calendar's toolbar button › and its key.
            L10n.T("Next");

        /// <summary>The "+" button, the File menu item and the editor window's title for a new event.</summary>
        public static string NewEvent => L10n.T("New Event");

        /// <summary>The editor window's title for an existing event.</summary>
        public static string EditEvent => L10n.T("Edit Event");

        /// <summary>The placeholder of the toolbar's search field.</summary>
        public static string SearchEvents => L10n.T("Search Events");

        /// <summary>Stands for an event without a title (a private one of a shared calendar, or one nobody named).</summary>
        public static string Untitled =>
            // TRANSLATORS: an event without a title.
            L10n.T("Untitled Event");

        /// <summary>The all-day row's label, the editor's switch and the time of an all-day event in lists.</summary>
        public static string AllDay =>
            // TRANSLATORS: an event that lasts whole days, without a time.
            L10n.T("All Day");

        /// <summary>
        /// The status bar's text when every account is synced:
        /// <paramref name="last"/> is the oldest lastSync of the accounts,
        /// the relative time counted in days of <paramref name="zone"/>.
        /// </summary>
        public static string SyncedLine(DateTimeOffset last, DateTimeOffset now, TimeZoneInfo zone) =>
            // TRANSLATORS: the status bar in Calendar mode; %s is a relative time
            // such as "2 minutes ago".
            L10n.T("Calendars synced %s", Board.Text.RelativeTime(last, now, zone));

        /// <summary>The status bar's text while a pass runs.</summary>
        public static string SyncingLine => L10n.T("Syncing calendars…");

        /// <summary>The status bar's text with every account offline.</summary>
        public static string OfflineLine => L10n.T("Calendars are offline");

        /// <summary>The status bar's and the sidebar's count of the invitations waiting for an answer.</summary>
        public static string UnansweredCount(int n) => L10n.N("%d unanswered invitation", "%d unanswered invitations", n);

        /// <summary>The sidebar's row under the Invitations heading, with the count badge (Swift <c>unanswered</c>).</summary>
        public static string UnansweredRow =>
            // TRANSLATORS: the sidebar's row under the Invitations heading that
            // lists the invitations without an answer.
            L10n.T("Unanswered");

        /// <summary>Says that a Microsoft account's sign-in lacks calendar access (state consentRequired).</summary>
        public static string ConsentLine => L10n.T("Calendar access needs a new sign-in");

        /// <summary>The button beside <see cref="ConsentLine"/>.</summary>
        public static string SignIn => L10n.T("Sign In…");

        /// <summary><see cref="ConsentLine"/>'s advice for a GOA account (no sign-in of the daemon's).</summary>
        public static string ConsentOnline => L10n.T("Allow calendar access in the desktop’s Online Accounts settings.");

        /// <summary>
        /// text.go <c>StatusLine</c>: the status bar's left text from the
        /// accounts' states: consent wins, then syncing, then all offline,
        /// then the synced line with the oldest last sync; "" when there is
        /// nothing to say.
        /// </summary>
        public static string StatusLine(IReadOnlyList<CalendarSyncState> states, DateTimeOffset now, TimeZoneInfo zone)
        {
            ArgumentNullException.ThrowIfNull(states);
            DateTimeOffset? oldest = null;
            var syncing = false;
            var offline = 0;
            foreach (var s in states)
            {
                switch (s.Status.Value)
                {
                    case CalendarSyncStatus.ConsentRequired:
                        return ConsentLine;
                    case CalendarSyncStatus.Syncing:
                        syncing = true;
                        break;
                    case CalendarSyncStatus.Offline:
                        offline++;
                        break;
                }
                if (s.LastSync is { } l && (oldest is null || l < oldest.Value))
                {
                    oldest = l;
                }
            }
            if (syncing)
            {
                return SyncingLine;
            }
            if (states.Count > 0 && offline == states.Count)
            {
                return OfflineLine;
            }
            return oldest is { } o ? SyncedLine(o, now, zone) : "";
        }

        /// <summary>An empty state's heading.</summary>
        public static string EmptyTitle(EmptyKind k) => k switch
        {
            EmptyKind.SyncOff => L10n.T("Calendar Sync Is Off"),
            EmptyKind.Loading => L10n.T("Loading Calendars…"),
            EmptyKind.NoBackend => L10n.T("Calendar Unavailable"),
            _ => L10n.T("No Calendars"),
        };

        /// <summary>An empty state's explanation; "" while loading.</summary>
        public static string EmptyBody(EmptyKind k) => k switch
        {
            EmptyKind.SyncOff => L10n.T("Turn on Sync This Account’s Calendars for a Microsoft 365 account in Preferences → Calendar."),
            EmptyKind.Loading => "",
            EmptyKind.NoBackend => L10n.T("The Malachi Mail backend is not running."),
            _ => L10n.T("Add a CalDAV calendar, or sync the calendars of a Microsoft 365 account in Preferences → Calendar."),
        };

        /// <summary>The empty state's button.</summary>
        public static string OpenPreferences => L10n.T("Open Preferences");

        /// <summary>What a write says over the sample data (MALACHI_CALENDAR_SAMPLES).</summary>
        public static string PreviewOnly =>
            // TRANSLATORS: the calendar shows invented sample data for a layout
            // check and an action that would change something was chosen.
            L10n.T("The preview cannot do that.");

        /// <summary>
        /// The button shown after a conflict error in the event editor: it
        /// reloads the event as the server has it, dropping the edits.
        /// </summary>
        public static string ReloadEvent =>
            // TRANSLATORS: the event editor's button after the event was changed
            // elsewhere: reload the event as the server has it, dropping the edits.
            L10n.T("Reload Event");

        /// <summary>
        /// text.go <c>ErrorText</c>: the sentence of a calendar error code;
        /// null for a code the calendar has no sentence of (the caller's
        /// usual RPC text then).
        /// </summary>
        public static string? ErrorText(ErrorCode code) => code.Value switch
        {
            ErrorCode.ReadOnly => L10n.T("This calendar or event cannot be changed."),
            ErrorCode.EventGone or ErrorCode.EventNotFound => L10n.T("The event no longer exists."),
            ErrorCode.CalendarNotFound => L10n.T("The calendar no longer exists."),
            ErrorCode.Conflict => L10n.T("The event was changed elsewhere. It has been reloaded."),
            ErrorCode.InvitationsUnavailable => L10n.T(
                "No account can send the invitations of this calendar. Choose a mail account for invitations in Preferences → Calendar."),
            ErrorCode.CalendarConsentRequired => ConsentLine,
            ErrorCode.RecurrenceUnsupported => L10n.T(
                "The event repeats in a way Malachi Mail cannot show. Only its first occurrence is shown, and it can be changed only as a whole series."),
            ErrorCode.MalformedCalendar => L10n.T("The calendar data could not be read."),
            ErrorCode.Offline => L10n.T("The calendar cannot be changed while offline."),
            _ => null,
        };

        // ------------------------------------------------------------------
        // Date formats
        // ------------------------------------------------------------------

        /// <summary>A time of day, "10:00".</summary>
        public static string TimeText(DateTimeOffset t, Env env) =>
            // TRANSLATORS: strftime format for a time of day, e.g. "15:04".
            Write(env, t, L10n.T("%H:%M"));

        /// <summary>A full date with the weekday, "Tuesday 14 October 2026".</summary>
        public static string LongDateText(DateTimeOffset t, Env env) =>
            // TRANSLATORS: strftime format for a full date with the weekday, e.g.
            // "Tuesday 14 October 2026".
            Write(env, t, L10n.T("%A %-d %B %Y"));

        /// <summary>A date with the weekday and without the year, "Tuesday 14 October" (the agenda's headings).</summary>
        public static string WeekdayDateText(DateTimeOffset t, Env env) =>
            // TRANSLATORS: strftime format for a date with the weekday, without the
            // year, e.g. "Tuesday 14 October".
            Write(env, t, L10n.T("%A %-d %B"));

        /// <summary>"14 October".</summary>
        public static string DayMonthText(DateTimeOffset t, Env env) =>
            // TRANSLATORS: strftime format for a day and month, e.g. "14 October".
            Write(env, t, L10n.T("%-d %B"));

        /// <summary>"14 October 2026".</summary>
        public static string DayMonthYearText(DateTimeOffset t, Env env) =>
            // TRANSLATORS: strftime format for a date, e.g. "14 October 2026".
            Write(env, t, L10n.T("%-d %B %Y"));

        /// <summary>The day of the month alone, "14" (the start of "12–18 October", the month grid's cells, the date tile).</summary>
        public static string DayText(DateTimeOffset t, Env env) =>
            // TRANSLATORS: strftime format for the day of the month alone, e.g.
            // "14" (the start of "12–18 October", a day of the month grid).
            Write(env, t, L10n.T("%-d"));

        /// <summary>"October 2026" (%OB, the standalone month name).</summary>
        public static string MonthYearText(DateTimeOffset t, Env env) =>
            // TRANSLATORS: strftime format for a month and year, e.g. "October
            // 2026" (the calendar's title). %OB is the standalone (nominative)
            // month name, "říjen 2026", not the genitive of %B.
            Write(env, t, L10n.T("%OB %Y"));

        /// <summary>The month's name, "October" (%OB).</summary>
        public static string MonthText(DateTimeOffset t, Env env) =>
            // TRANSLATORS: strftime format for a month's name alone, e.g. "October"
            // (the start of "October–November 2026"). %OB is the standalone
            // (nominative) month name, "říjen", not the genitive of %B.
            Write(env, t, L10n.T("%OB"));

        /// <summary>The weekday's name, "Tuesday".</summary>
        public static string WeekdayText(DateTimeOffset t, Env env) =>
            // TRANSLATORS: strftime format for a weekday's name, e.g. "Tuesday".
            Write(env, t, L10n.T("%A"));

        /// <summary>The abbreviated weekday, "Tue" (the week's day headers, the month's header, the date tile).</summary>
        public static string ShortWeekdayText(DateTimeOffset t, Env env) =>
            // TRANSLATORS: strftime format for an abbreviated weekday, e.g. "Tue".
            Write(env, t, L10n.T("%a"));

        /// <summary>The abbreviated month, "Oct" (the date tile).</summary>
        public static string ShortMonthText(DateTimeOffset t, Env env) =>
            // TRANSLATORS: strftime format for an abbreviated month, e.g. "Oct"
            // (the invitation card's date tile).
            Write(env, t, L10n.T("%b"));

        /// <summary>A short date with a time, "Tue 14 Oct 2026 10:00" (the two ends of an event over several days).</summary>
        public static string DateTimeText(DateTimeOffset t, Env env) =>
            // TRANSLATORS: strftime format for a short date with the time, e.g.
            // "Tue 14 Oct 2026 10:00".
            Write(env, t, L10n.T("%a %-d %b %Y %H:%M"));

        /// <summary>Joins the two ends of a range of dates or times, "12–18 October", "10:00–11:30".</summary>
        public static string RangeText(string from, string to) =>
            // TRANSLATORS: a range of two dates or times: "12–18 October",
            // "10:00–11:30", "28 September–4 October".
            L10n.T("%s–%s", from, to);

        /// <summary>Joins two parts of one line, "Work · Calendar", "10:00–11:30 · Room B".</summary>
        public static string Dot(string a, string b) =>
            // TRANSLATORS: two parts of one line of the calendar, e.g. "Work ·
            // Calendar" (account and calendar) or "10:00–11:30 · Room B".
            L10n.T("%s · %s", a, b);

        /// <summary>Go's <c>dots</c>: the non-empty parts joined with <see cref="Dot"/>.</summary>
        internal static string Dots(params ReadOnlySpan<string?> parts)
        {
            var output = "";
            foreach (var p in parts)
            {
                if (string.IsNullOrEmpty(p))
                {
                    continue;
                }
                output = output.Length == 0 ? p : Dot(output, p);
            }
            return output;
        }

        /// <summary>A timed span's times in the env's zone, "10:00–11:30".</summary>
        public static string TimeRangeText(DateTimeOffset start, DateTimeOffset end, Env env) =>
            RangeText(TimeText(start, env), TimeText(end, env));

        // Writes t with a translated strftime format in the env's zone.
        private static string Write(Env env, DateTimeOffset t, string format)
        {
            ArgumentNullException.ThrowIfNull(env);
            return env.Date(t, format);
        }
    }
}
