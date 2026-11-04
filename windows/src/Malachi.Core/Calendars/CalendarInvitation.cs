// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarInvitation.swift; GTK:
// ui/internal/calendar/invitation.go (InvitationPill, PillKind, PillTitle,
// StrikesSubject, CardMode, CardModeOf, ShowsResponses, ShowsStateStrip,
// ShowsProposeNewTime, DateTile, cardStart, DateTileOf, occurrenceOf,
// CardTimeLine, CardTitle, CardPeopleLine, PreviousLines, ConflictLine,
// NoConflict, AddTo, ReplyNote, StateStrip, ChangeResponse,
// ShowsInCalendar, ShowInCalendar, ProposeNewTime, Propose, ReplyCard,
// StateLine, InvalidLine, CancelledLine) and Swift's bareOccurrence and
// previousLine.
//
// Invitations in Mail (docs/calendar.md §5.7): the list pill of a message
// with a calendar part, and the invitation card above the message header,
// built from the parsed part (never from HTML): the date tile, the time and
// people lines, an update's former values (D30), the conflict line, "Add
// to:", the reply note, the state strip after answering, a reply's
// one-line card and the lines of the user's own (D32), invalid, outdated
// and cancelled invitations. Every string of the part is cleaned for
// display (CleanTitle, CleanLine, NameOf). The Swift-first parts of
// InvitationReading.swift (the clock line with the repetition, the day
// strip) are in CalendarInvitationReading.cs.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>invitation.go <c>InvitationPill</c>: the list pill of a message.</summary>
    public enum InvitationPill
    {
        /// <summary>No pill.</summary>
        None,

        /// <summary>An invitation.</summary>
        Invitation,

        /// <summary>An updated invitation.</summary>
        Update,

        /// <summary>A cancelled event (the subject struck through).</summary>
        Cancelled,

        /// <summary>An attendee's answer.</summary>
        Reply,
    }

    /// <summary>
    /// invitation.go <c>PillKind</c>: the pill of a listed message: none
    /// without an invitation, for an unreadable one (state invalid) and for
    /// kind other.
    /// </summary>
    public static InvitationPill PillKind(InvitationSummary? s)
    {
        if (s is null || s.State == InvitationState.Invalid)
        {
            return InvitationPill.None;
        }
        return s.Kind.Value switch
        {
            InvitationKind.Invitation => InvitationPill.Invitation,
            InvitationKind.Update => InvitationPill.Update,
            InvitationKind.Cancelled => InvitationPill.Cancelled,
            InvitationKind.Reply => InvitationPill.Reply,
            _ => InvitationPill.None,
        };
    }

    /// <summary>invitation.go <c>PillTitle</c>: a pill's text ("" for none).</summary>
    public static string PillTitle(InvitationPill p) => p switch
    {
        // TRANSLATORS: the message list's pill of a message with an
        // invitation to an event.
        InvitationPill.Invitation => L10n.C("pill", "Invitation"),
        // TRANSLATORS: the message list's pill of an updated invitation.
        InvitationPill.Update => L10n.C("pill", "Update"),
        // TRANSLATORS: the message list's pill of a cancelled event.
        InvitationPill.Cancelled => L10n.C("pill", "Cancelled"),
        // TRANSLATORS: the message list's pill of an attendee's answer.
        InvitationPill.Reply => L10n.C("pill", "Reply"),
        _ => "",
    };

    /// <summary>invitation.go <c>StrikesSubject</c>: a row whose subject is struck through: a cancellation.</summary>
    public static bool StrikesSubject(InvitationPill p) => p == InvitationPill.Cancelled;

    /// <summary>invitation.go <c>CardMode</c>: which card a message shows.</summary>
    public enum CardMode
    {
        /// <summary>No card (no invitation, state invalid, the account's card switch off, kind other).</summary>
        None,

        /// <summary>The invitation card with its actions or state strip.</summary>
        Full,

        /// <summary>The one-line card of an attendee's answer.</summary>
        Reply,

        /// <summary>The card of a cancellation with Remove from Calendar.</summary>
        Cancelled,
    }

    /// <summary>invitation.go <c>CardModeOf</c>: the card of a message's invitation; <paramref name="cardOn"/> is the account's InvitationCard switch.</summary>
    public static CardMode CardModeOf(InvitationCard? c, bool cardOn)
    {
        if (c is null || !cardOn || c.State == InvitationState.Invalid)
        {
            return CardMode.None;
        }
        return c.Kind.Value switch
        {
            InvitationKind.Reply => CardMode.Reply,
            InvitationKind.Cancelled => CardMode.Cancelled,
            InvitationKind.Invitation or InvitationKind.Update => CardMode.Full,
            _ => CardMode.None,
        };
    }

    /// <summary>invitation.go <c>ShowsResponses</c>: the Accept / Tentative / Decline row: an unanswered invitation the daemon lets the user answer.</summary>
    public static bool ShowsResponses(InvitationCard c)
    {
        ArgumentNullException.ThrowIfNull(c);
        return c.State == InvitationState.Unanswered && c.CanRespond;
    }

    /// <summary>invitation.go <c>ShowsStateStrip</c>: the strip after answering (with Change Response and Show in Calendar).</summary>
    public static bool ShowsStateStrip(InvitationCard c)
    {
        ArgumentNullException.ThrowIfNull(c);
        return c.State == InvitationState.Answered && c.Response?.Value is { Length: > 0 };
    }

    /// <summary>invitation.go <c>ShowsProposeNewTime</c>: Propose New Time… (Graph events that allow it, docs/calendar.md D20).</summary>
    public static bool ShowsProposeNewTime(InvitationCard c)
    {
        ArgumentNullException.ThrowIfNull(c);
        return c.CanProposeNewTime && (ShowsResponses(c) || ShowsStateStrip(c));
    }

    /// <summary>invitation.go <c>DateTile</c>: the card's date tile.</summary>
    /// <param name="Weekday">The abbreviated weekday, "Fri".</param>
    /// <param name="Day">The day of the month, "16".</param>
    /// <param name="Month">The abbreviated month, "Oct".</param>
    public sealed record DateTile(string Weekday, string Day, string Month);

    /// <summary>Go's <c>cardStart</c>: the invitation's start in <paramref name="zone"/> (an all-day one's midnight); null without one.</summary>
    internal static DateTimeOffset? CardStart(InvitationSummary s, TimeZoneInfo zone)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (s.AllDay == true)
        {
            return ParseDate(s.StartDate, zone);
        }
        return s.Start is { } start ? ToZone(start, zone) : null;
    }

    /// <summary>invitation.go <c>DateTileOf</c>: the tile of the invitation's first day; null without a start.</summary>
    public static DateTile? DateTileOf(InvitationSummary s, Env env)
    {
        ArgumentNullException.ThrowIfNull(env);
        if (CardStart(s, env.Zone) is not { } t)
        {
            return null;
        }
        return new DateTile(Text.ShortWeekdayText(t, env), Text.DayText(t, env), Text.ShortMonthText(t, env));
    }

    /// <summary>Swift's <c>bareOccurrence</c>: an occurrence of only these times, for <see cref="TimeLine"/>.</summary>
    internal static Occurrence BareOccurrence(
        string title = "", DateTimeOffset? start = null, DateTimeOffset? end = null, CalendarDate? startDate = null,
        CalendarDate? endDate = null, bool allDay = false, string? location = null) => new()
        {
            AccountId = new AccountId(""),
            Title = title,
            Start = start,
            End = end,
            StartDate = startDate,
            EndDate = endDate,
            AllDay = allDay,
            Location = location,
            Status = EventStatus.Confirmed,
            ShowAs = ShowAs.Busy,
        };

    /// <summary>
    /// invitation.go <c>occurrenceOf</c> (Swift's public <c>Cal.occurrenceOf</c>):
    /// the card's time and place as an occurrence, for its time line and the
    /// texts of the reply mail.
    /// </summary>
    public static Occurrence OccurrenceOf(InvitationCard c) =>
        BareOccurrence(c.Title, c.Start, c.End, c.StartDate, c.EndDate, c.AllDay == true, c.Location);

    /// <summary>invitation.go <c>CardTimeLine</c>: the card's clock line (<see cref="TimeLine"/>).</summary>
    public static string CardTimeLine(InvitationCard c, Env env)
    {
        ArgumentNullException.ThrowIfNull(c);
        return TimeLine(OccurrenceOf(c), env);
    }

    /// <summary>invitation.go <c>CardTitle</c>: the card's bold title.</summary>
    public static string CardTitle(InvitationSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        return CleanTitle(s.Title);
    }

    /// <summary>invitation.go <c>CardPeopleLine</c>: "Organiser Jana Nováková · 4 attendees", the organiser or the count alone; "" without either.</summary>
    public static string CardPeopleLine(InvitationCard c)
    {
        ArgumentNullException.ThrowIfNull(c);
        var n = Math.Max(c.AttendeeCount, c.Attendees?.Count ?? 0);
        var org = NameOf(c.Organizer);
        if (org.Length > 0 && n > 0)
        {
            return L10n.N("Organiser %s · %d attendee", "Organiser %s · %d attendees", n, org, n);
        }
        if (org.Length > 0)
        {
            // TRANSLATORS: the invitation card's people line without attendees.
            return L10n.T("Organiser %s", org);
        }
        if (n > 0)
        {
            return L10n.N("%d attendee", "%d attendees", n);
        }
        return "";
    }

    /// <summary>
    /// invitation.go <c>PreviousLines</c>: an update's former values, each
    /// only when the daemon sent it (D30): the time ("Previously Tuesday 14
    /// October 2026 · 10:00–11:00 · 1 h"), the location ("Previously at Room
    /// B"), the recurrence ("Previously repeated: Every week on Tuesday",
    /// described from the former start, else the card's); empty without one.
    /// </summary>
    public static IReadOnlyList<string> PreviousLines(InvitationCard c, Env env)
    {
        ArgumentNullException.ThrowIfNull(c);
        ArgumentNullException.ThrowIfNull(env);
        if (c.Previous is not { } p)
        {
            return [];
        }
        var lines = new List<string>();
        var start = CardStart(c, env.Zone);
        var ps = p.Start?.Value ?? "";
        if (ps.Length > 0)
        {
            var o = Rfc3339.Parse(ps) is { } s
                ? BareOccurrence(start: s, end: Rfc3339.Parse(p.End?.Value ?? ""))
                : BareOccurrence(allDay: true, startDate: new CalendarDate(ps), endDate: new CalendarDate(p.End?.Value ?? ""));
            var line = TimeLine(o, env);
            if (line.Length > 0)
            {
                // TRANSLATORS: the invitation card of an update: the event's time
                // before the change.
                lines.Add(L10n.T("Previously %s", line));
                start = Span(o, env.Zone)?.Start;
            }
        }
        var where = CleanLine(p.Location);
        if (where.Length > 0)
        {
            // TRANSLATORS: the invitation card of an update: the event's location
            // before the change.
            lines.Add(L10n.T("Previously at %s", where));
        }
        if (p.Recurrence is { } r && start is { } first)
        {
            var rule = RecurrenceText(r, first, env);
            if (rule.Length > 0)
            {
                // TRANSLATORS: the invitation card of an update: the event's
                // recurrence before the change; %s is a recurrence description
                // such as "Every week on Tuesday, until 27 October 2026".
                lines.Add(L10n.T("Previously repeated: %s", rule));
            }
        }
        return lines;
    }

    /// <summary>Swift's <c>previousLine</c>: <see cref="PreviousLines"/> joined line by line, for a caller that shows one label.</summary>
    public static string PreviousLine(InvitationCard c, Env env) => string.Join('\n', PreviousLines(c, env));

    /// <summary>
    /// invitation.go <c>ConflictLine</c>: the card's conflict line: one
    /// conflict "Conflicts with Standup (10:00–10:30) in calendar Work ·
    /// Calendar" (a private one without its title), several "Conflicts with
    /// 2 events"; "" without conflicts. <paramref name="calendarName"/> names
    /// a calendar by id.
    /// </summary>
    public static string ConflictLine(IReadOnlyList<InvitationConflict>? conflicts, Func<CalendarId, string> calendarName, Env env)
    {
        ArgumentNullException.ThrowIfNull(calendarName);
        ArgumentNullException.ThrowIfNull(env);
        conflicts ??= [];
        switch (conflicts.Count)
        {
            case 0:
                return "";
            case 1:
                {
                    var c = conflicts[0];
                    var when = Text.TimeRangeText(c.Start, c.End, env);
                    var cal = CleanLine(calendarName(c.CalendarId));
                    var title = CleanLine(c.Title);
                    if (title.Length > 0)
                    {
                        // TRANSLATORS: the invitation card: the event overlaps one in the
                        // user's calendar; its title, time and calendar.
                        return L10n.T("Conflicts with %s (%s) in calendar %s", title, when, cal);
                    }
                    // TRANSLATORS: the invitation card: the event overlaps a private
                    // event of a shared calendar; its time and calendar.
                    return L10n.T("Conflicts with a private event (%s) in calendar %s", when, cal);
                }
            default:
                return L10n.N("Conflicts with %d event", "Conflicts with %d events", conflicts.Count);
        }
    }

    /// <summary>invitation.go <c>NoConflict</c>: the card's line when the time is free.</summary>
    public static string NoConflict => L10n.T("No conflicts in your calendars");

    /// <summary>invitation.go <c>AddTo</c>: the label of the target-calendar popup.</summary>
    public static string AddTo =>
        // TRANSLATORS: the invitation card's popup of the calendar the event
        // goes to when accepted.
        L10n.T("Add to:");

    /// <summary>
    /// invitation.go <c>ReplyNote</c>: the note under the response buttons:
    /// where the reply leaves from and goes to, and the warning when the
    /// organiser's domain is not the sender's. <paramref name="accountName"/>
    /// names the card's ReplyAccountId.
    /// </summary>
    public static IReadOnlyList<string> ReplyNote(InvitationCard c, string accountName)
    {
        ArgumentNullException.ThrowIfNull(c);
        var lines = new List<string>();
        var to = NameOf(c.ReplyTo);
        if (to.Length > 0 && c.ReplyTo is { } r && CleanLine(r.Name).Length > 0)
        {
            // TRANSLATORS: an organiser in a note: the name and the address.
            to = L10n.T("%s <%s>", CleanLine(r.Name), CleanLine(r.Email));
        }
        switch (c.ReplyVia.Value)
        {
            case InvitationsVia.Mail:
                {
                    var a = CleanLine(accountName);
                    if (a.Length > 0)
                    {
                        lines.Add(L10n.T("The reply leaves from account %s.", a));
                    }
                    if (to.Length > 0)
                    {
                        lines.Add(L10n.T("The reply goes to %s.", to));
                    }
                    break;
                }
            case InvitationsVia.Server:
                if (to.Length > 0)
                {
                    lines.Add(L10n.T("The reply goes to %s.", to));
                }
                break;
            default:
                lines.Add(L10n.T("No reply will be sent."));
                break;
        }
        if (c.OrganizerMismatch == true)
        {
            lines.Add(L10n.T("The organiser’s address is not from the sender’s domain. Check the invitation before you answer."));
        }
        return lines;
    }

    /// <summary>
    /// invitation.go <c>StateStrip</c>: the strip after answering: "Accepted
    /// · in calendar Work · Calendar"; without a calendar, or declined, the
    /// answer alone.
    /// </summary>
    public static string StateStrip(InvitationCard c, string calendarName)
    {
        ArgumentNullException.ThrowIfNull(c);
        var answer = ResponseLabel(c.Response);
        var cal = CleanLine(calendarName);
        if (cal.Length > 0 && c.Response != EventResponse.Declined)
        {
            // TRANSLATORS: the invitation card after answering: the answer and
            // the calendar the event is in, "Accepted · in calendar Work".
            return L10n.T("%s · in calendar %s", answer, cal);
        }
        return answer;
    }

    /// <summary>invitation.go <c>ChangeResponse</c>: the state strip's menu button.</summary>
    public static string ChangeResponse => L10n.T("Change Response");

    /// <summary>
    /// invitation.go <c>ShowsInCalendar</c>: Show in Calendar on the card's
    /// state strip or state line: the invitation is linked to a stored event,
    /// and it is the user's own event or one the user answered.
    /// </summary>
    public static bool ShowsInCalendar(InvitationCard c)
    {
        ArgumentNullException.ThrowIfNull(c);
        return c.EventId?.Value is { Length: > 0 } && (c.OwnEvent == true || c.State == InvitationState.Answered);
    }

    /// <summary>invitation.go <c>ShowInCalendar</c>: the state strip's button that shows the event in Calendar mode.</summary>
    public static string ShowInCalendar => L10n.T("Show in Calendar");

    /// <summary>invitation.go <c>ProposeNewTime</c>: opens the popover with a start and an end.</summary>
    public static string ProposeNewTime => L10n.T("Propose New Time…");

    /// <summary>invitation.go <c>Propose</c>: the popover's button that sends the proposal.</summary>
    public static string Propose => L10n.T("Propose");

    /// <summary>
    /// invitation.go <c>ReplyCard</c>: a reply's one-line card: "Jana
    /// Nováková accepted", with "(applied to your event)" when the daemon
    /// updated the user's event.
    /// </summary>
    public static string ReplyCard(InvitationCard c)
    {
        ArgumentNullException.ThrowIfNull(c);
        var who = NameOf(c.ReplyFrom);
        if (who.Length == 0)
        {
            who = NameOf(c.Organizer);
        }
        var line = c.Response?.Value switch
        {
            // TRANSLATORS: the card of an attendee's answer: their name.
            EventResponse.Accepted => L10n.T("%s accepted", who),
            // TRANSLATORS: the card of an attendee's answer: their name.
            EventResponse.Tentative => L10n.T("%s tentatively accepted", who),
            // TRANSLATORS: the card of an attendee's answer: their name.
            EventResponse.Declined => L10n.T("%s declined", who),
            // TRANSLATORS: the card of an attendee's answer that is none of
            // the usual three.
            _ => L10n.T("%s replied", who),
        };
        // TRANSLATORS: after the card of an attendee's answer: the user's
        // event shows it now.
        return c.ReplyApplied == true ? L10n.T("%s (applied to your event)", line) : line;
    }

    /// <summary>
    /// invitation.go <c>StateLine</c>: the card's line of an invitation that
    /// cannot be answered: the user's own event (first, D32), invalid,
    /// outdated, cancelled, unanswered without a response path; "" otherwise.
    /// </summary>
    public static string StateLine(InvitationCard c)
    {
        ArgumentNullException.ThrowIfNull(c);
        if (c.OwnEvent == true && (c.Kind == InvitationKind.Invitation || c.Kind == InvitationKind.Update))
        {
            // TRANSLATORS: the invitation card of an invitation the user sent
            // themself (the user organises the event).
            return L10n.T("You organise this event.");
        }
        return c.State.Value switch
        {
            InvitationState.Invalid => InvalidLine,
            InvitationState.Outdated => L10n.T("This invitation is out of date: a newer one arrived, or the event is over."),
            InvitationState.Cancelled => CancelledLine,
            InvitationState.Unanswered when !c.CanRespond => L10n.T("This invitation cannot be answered from here."),
            _ => "",
        };
    }

    /// <summary>invitation.go <c>InvalidLine</c>: the calendar part could not be read (shown where a client says why there is no card).</summary>
    public static string InvalidLine => L10n.T("The invitation in this message could not be read.");

    /// <summary>invitation.go <c>CancelledLine</c>: the cancellation card's line.</summary>
    public static string CancelledLine => L10n.T("This event was cancelled.");
}
