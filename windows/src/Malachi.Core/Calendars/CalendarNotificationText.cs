// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarNotificationText.swift;
// GTK: ui/internal/calendar/notifications.go (SnoozeMinutes, Notification,
// AlertWhen, AlertNotification, Join, Snooze, InvitationNotification,
// summaryBody, Show, CancellationNotification).
//
// The calendar's notifications: an event's alert (notify.eventAlert) with
// Join and Snooze, a new invitation with Accept (ResponseAction) and Show,
// a cancellation with Remove from Calendar (RemoveFromCalendar). Every
// string from the event or the part is cleaned for display; a caller
// still caps a notification's title as the mail notifications do.

using System;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>notifications.go <c>SnoozeMinutes</c>: how long Snooze puts an alert off.</summary>
    public const int SnoozeMinutes = 5;

    /// <summary>notifications.go <c>Notification</c>: a notification's title and body.</summary>
    /// <param name="Title">The title.</param>
    /// <param name="Body">The body ("" none).</param>
    public sealed record Notification(string Title, string Body);

    /// <summary>
    /// notifications.go <c>AlertWhen</c>: when an alerted event starts, seen
    /// from <paramref name="now"/>: "now" when it has begun or starts within
    /// the half minute, "in 5 minutes" under an hour, "at 12:00" later
    /// today, else the date and time; an all-day event "today" or its date.
    /// The minutes are rounded half away from zero, as Go's
    /// <c>Duration.Round</c>.
    /// </summary>
    public static string AlertWhen(EventAlert a, DateTimeOffset now, Env env)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(env);
        var zone = env.Zone;
        if (a.AllDay)
        {
            if (ParseDate(a.StartDate, zone) is not { } d)
            {
                return "";
            }
            if (SameDay(d, now, zone))
            {
                // TRANSLATORS: an alert of an all-day event of today.
                return L10n.T("today");
            }
            return Text.WeekdayDateText(d, env);
        }
        if (a.Start is not { } start)
        {
            return "";
        }
        var m = RoundedMinutes(start - now);
        if (m <= 0)
        {
            // TRANSLATORS: an alert of an event that starts now.
            return L10n.C("alert", "now");
        }
        if (m < 60)
        {
            // TRANSLATORS: an alert of an event that starts in a few minutes.
            return L10n.N("in %d minute", "in %d minutes", m);
        }
        if (SameDay(start, now, zone))
        {
            // TRANSLATORS: an alert of an event later today; %s is its time.
            return L10n.T("at %s", Text.TimeText(start, env));
        }
        return Text.Dots(Text.WeekdayDateText(start, env), Text.TimeText(start, env));
    }

    // Go's int(d.Round(time.Minute) / time.Minute): the nearest whole
    // minute, a half minute away from zero.
    private static long RoundedMinutes(TimeSpan d)
    {
        var q = d.Ticks / TimeSpan.TicksPerMinute;
        var r = d.Ticks % TimeSpan.TicksPerMinute;
        if (Math.Abs(r) * 2 >= TimeSpan.TicksPerMinute)
        {
            q += Math.Sign(d.Ticks);
        }
        return q;
    }

    /// <summary>
    /// notifications.go <c>AlertNotification</c>: an alert's notification:
    /// the event's title, and when it starts with the place, "in 5 minutes ·
    /// Room B".
    /// </summary>
    public static Notification AlertNotification(EventAlert a, DateTimeOffset now, Env env)
    {
        ArgumentNullException.ThrowIfNull(a);
        return new Notification(CleanTitle(a.Title), Text.Dots(AlertWhen(a, now, env), CleanLine(a.Location)));
    }

    /// <summary>notifications.go <c>Join</c>: an alert's action for an event with a video call.</summary>
    public static string Join =>
        // TRANSLATORS: the action of an event's notification that joins its
        // video call.
        L10n.T("Join");

    /// <summary>notifications.go <c>Snooze</c>: an alert's action that puts it off <see cref="SnoozeMinutes"/>.</summary>
    public static string Snooze =>
        // TRANSLATORS: the action of an event's notification that shows it
        // again in a few minutes.
        L10n.T("Snooze");

    /// <summary>
    /// notifications.go <c>InvitationNotification</c>: a new invitation's
    /// notification: "New invitation from Jana Nováková" (an update "Jana
    /// Nováková updated the event"), the event's title and time.
    /// </summary>
    public static Notification InvitationNotification(InvitationSummary s, Env env)
    {
        ArgumentNullException.ThrowIfNull(s);
        var from = NameOf(s.Organizer);
        string title;
        if (s.Kind == InvitationKind.Update && from.Length > 0)
        {
            title = L10n.T("%s updated the event", from);
        }
        else if (s.Kind == InvitationKind.Update)
        {
            title = L10n.T("An event was updated");
        }
        else if (from.Length > 0)
        {
            title = L10n.T("New invitation from %s", from);
        }
        else
        {
            title = L10n.T("New invitation");
        }
        return new Notification(title, SummaryBody(s, env));
    }

    // Go's summaryBody: "Q4 planning · Tuesday 14 October 2026 · 10:00–11:30
    // · 1 h 30 min" (an all-day invitation one day long).
    private static string SummaryBody(InvitationSummary s, Env env)
    {
        ArgumentNullException.ThrowIfNull(env);
        var allDay = s.AllDay == true;
        CalendarDate? endDate = null;
        if (allDay && ParseDate(s.StartDate, env.Zone) is { } d)
        {
            endDate = DateOf(AddDays(d, 1, env.Zone), env.Zone);
        }
        var o = BareOccurrence(start: s.Start, end: s.End, startDate: s.StartDate, endDate: endDate, allDay: allDay);
        return Text.Dots(CleanTitle(s.Title), TimeLine(o, env));
    }

    /// <summary>notifications.go <c>Show</c>: an invitation's action that opens the message.</summary>
    public static string Show =>
        // TRANSLATORS: the action of an invitation's notification that opens
        // the message.
        L10n.T("Show");

    /// <summary>
    /// notifications.go <c>CancellationNotification</c>: a cancellation's
    /// notification: "Jana Nováková cancelled the event", the event's title
    /// and time.
    /// </summary>
    public static Notification CancellationNotification(InvitationSummary s, Env env)
    {
        ArgumentNullException.ThrowIfNull(s);
        var from = NameOf(s.Organizer);
        var title = from.Length > 0 ? L10n.T("%s cancelled the event", from) : L10n.T("An event was cancelled");
        return new Notification(title, SummaryBody(s, env));
    }
}
