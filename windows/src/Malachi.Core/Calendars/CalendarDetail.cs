// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarDetail.swift; GTK:
// ui/internal/calendar/detail.go (MaxAttendeesShown, AttendeeRow, Detail,
// DetailOf, TimeLine, spanLine, DaysText, DurationText, ResponseLabel,
// MyResponseText, ResponseChoices, ResponseAction, DeclineWithoutReply,
// PeopleLine, OrganizerLabel, OptionalLabel, ShowAllAttendees, CanRespond,
// ShowsInvitation, ShowInvitation, Edit, JoinVideoCall, AskAboutEvent,
// FindAnotherTime, RemoveTitle, RemoveFromCalendar, Question,
// RemoveQuestion, DeleteQuestion).
//
// The event detail popover (docs/calendar.md §5.5): its rows from an
// event, the time line with the duration, the people and their responses,
// and which actions it offers (respond, remove or delete with its
// question, Show Invitation in Mail, Edit). Every string of the event is
// cleaned for display (CleanTitle, CleanLine, Board.CleanBlock); the
// links and the video call's URL stay as the daemon sent them, for the
// "Open this link?" confirmation. RemoveQuestion returns null where Go
// returns false (Swift's optional).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.I18n;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>detail.go <c>MaxAttendeesShown</c>: how many attendees the list shows before it collapses behind "Show All".</summary>
    public const int MaxAttendeesShown = 6;

    /// <summary>detail.go <c>AttendeeRow</c>: one person of the detail's list.</summary>
    /// <param name="Name">The cleaned name ("" none).</param>
    /// <param name="Address">The cleaned address.</param>
    /// <param name="Response">The attendee's answer.</param>
    /// <param name="ResponseText">The answer's label (<see cref="ResponseLabel"/>).</param>
    /// <param name="Organizer">The attendee organises the event.</param>
    /// <param name="Me">The attendee is the user.</param>
    /// <param name="Optional">The attendee's role is optional.</param>
    public sealed record AttendeeRow(
        string Name, string Address, EventResponse Response, string ResponseText, bool Organizer, bool Me, bool Optional);

    /// <summary>detail.go <c>Detail</c>: everything the popover shows of an event.</summary>
    public sealed record Detail
    {
        /// <summary>"Work · Calendar" (account · calendar).</summary>
        public string CalendarLine { get; init; } = "";

        /// <summary>The cleaned title (<see cref="Text.Untitled"/> when empty).</summary>
        public string Title { get; init; } = "";

        /// <summary>"Tuesday 14 October 2026 · 10:00–11:30 · 1 h 30 min".</summary>
        public string TimeLine { get; init; } = "";

        /// <summary>The rule's text; "" for a single event.</summary>
        public string Recurrence { get; init; } = "";

        /// <summary>The cleaned location.</summary>
        public string Location { get; init; } = "";

        /// <summary>The call's link ("" none).</summary>
        public string VideoCall { get; init; } = "";

        /// <summary>The call's host, cleaned.</summary>
        public string VideoHost { get; init; } = "";

        /// <summary>"Organiser Jana Nováková · 4 attendees"; "" without attendees.</summary>
        public string PeopleLine { get; init; } = "";

        /// <summary>The attendees in the daemon's order.</summary>
        public IReadOnlyList<AttendeeRow> Attendees { get; init; } = [];

        /// <summary>More than <see cref="MaxAttendeesShown"/> attendees.</summary>
        public bool Collapsed { get; init; }

        /// <summary>The user's answer when the user is an attendee.</summary>
        public EventResponse? MyResponse { get; init; }

        /// <summary>The cleaned notes, line breaks kept.</summary>
        public string Notes { get; init; } = "";

        /// <summary>The notes' links, as the daemon sent them.</summary>
        public IReadOnlyList<string> Links { get; init; } = [];

        /// <summary>The popover offers the user's answer (<see cref="Cal.CanRespond"/>).</summary>
        public bool CanRespond { get; init; }

        /// <summary>The event can be edited.</summary>
        public bool CanEdit { get; init; }

        /// <summary>The event can be removed or deleted.</summary>
        public bool CanRemove { get; init; }

        /// <summary>Show Invitation in Mail is offered.</summary>
        public bool ShowInvitation { get; init; }

        /// <summary>The footer's destructive button (<see cref="Cal.RemoveTitle"/>).</summary>
        public string RemoveTitle { get; init; } = "";
    }

    /// <summary>detail.go <c>DetailOf</c>: the detail of <paramref name="e"/>, with its account's and calendar's names.</summary>
    public static Detail DetailOf(CalendarEvent e, string accountName, string calendarName, Env env)
    {
        ArgumentNullException.ThrowIfNull(e);
        ArgumentNullException.ThrowIfNull(env);
        var recurrence = "";
        if (e.Recurrence is { } r && Span(e, env.Zone) is { } span)
        {
            recurrence = RecurrenceText(r, span.Start, env);
        }
        var attendees = (e.Attendees ?? []).Select(a => new AttendeeRow(
            CleanLine(a.Name), CleanLine(a.Address), a.Response, ResponseLabel(a.Response), a.Organizer == true, a.Me == true,
            a.Role == AttendeeRole.Optional)).ToList();
        return new Detail
        {
            CalendarLine = Text.Dots(CleanLine(accountName), CleanLine(calendarName)),
            Title = CleanTitle(e.Title),
            TimeLine = TimeLine(e, env),
            Recurrence = recurrence,
            Location = CleanLine(e.Location),
            VideoCall = e.VideoCallLink?.Url ?? "",
            VideoHost = CleanLine(e.VideoCallLink?.Host),
            PeopleLine = PeopleLine(e),
            Attendees = attendees,
            Collapsed = attendees.Count > MaxAttendeesShown,
            MyResponse = e.MyResponse,
            Notes = Board.CleanBlock(e.Notes, API.Limits.MaxEventNotesBytes),
            Links = e.Links ?? [],
            CanRespond = CanRespond(e),
            CanEdit = e.CanEdit,
            CanRemove = e.CanDelete,
            ShowInvitation = ShowsInvitation(e),
            RemoveTitle = RemoveTitle(e),
        };
    }

    /// <summary>
    /// detail.go <c>TimeLine</c>: an occurrence's date and time with its
    /// duration: "Tuesday 14 October 2026 · 10:00–11:30 · 1 h 30 min"; an
    /// all-day day "Tuesday 14 October 2026 · All Day"; several days with
    /// both ends and the length. "" without a usable time.
    /// </summary>
    public static string TimeLine(Occurrence o, Env env)
    {
        ArgumentNullException.ThrowIfNull(env);
        return Span(o, env.Zone) is { } span ? SpanLine(span.Start, span.End, o.AllDay, env) : "";
    }

    /// <summary>Go's <c>spanLine</c>: <see cref="TimeLine"/> of a span in the env's zone.</summary>
    internal static string SpanLine(DateTimeOffset s, DateTimeOffset e, bool allDay, Env env)
    {
        var zone = env.Zone;
        if (allDay)
        {
            var days = Math.Max(1, DaysBetween(s, e, zone));
            if (days == 1)
            {
                return Text.Dot(Text.LongDateText(s, env), Text.AllDay);
            }
            return Text.Dot(Text.RangeText(Text.LongDateText(s, env), Text.LongDateText(AddDays(e, -1, zone), env)), DaysText(days));
        }
        if (OccurrenceDays(s, e, zone) <= 1)
        {
            return Text.Dots(Text.LongDateText(s, env), Text.TimeRangeText(s, e, env), DurationText(e - s));
        }
        return Text.Dot(Text.RangeText(Text.DateTimeText(s, env), Text.DateTimeText(e, env)), DurationText(e - s));
    }

    /// <summary>detail.go <c>DaysText</c>: a number of days, "2 days".</summary>
    public static string DaysText(int n) => L10n.N("%d day", "%d days", n);

    /// <summary>
    /// detail.go <c>DurationText</c>: a length of time in hours and minutes,
    /// "1 h 30 min", "45 min", "2 h"; a whole number of days of 24 hours is
    /// "2 days". Rounded down to the minute; under a minute (or negative) is
    /// "0 min".
    /// </summary>
    public static string DurationText(TimeSpan d)
    {
        var total = Math.Clamp(d.Ticks / TimeSpan.TicksPerMinute, 0, int.MaxValue);
        if (total >= 1440 && total % 1440 == 0)
        {
            return DaysText((int)(total / 1440));
        }
        var h = total / 60;
        var m = total % 60;
        // TRANSLATORS: a duration's hours, abbreviated: "1 h", "2 h".
        var hours = L10n.N("%d h", "%d h", h);
        // TRANSLATORS: a duration's minutes, abbreviated: "30 min".
        var minutes = L10n.N("%d min", "%d min", m);
        if (h == 0)
        {
            return minutes;
        }
        if (m == 0)
        {
            return hours;
        }
        // TRANSLATORS: a duration of hours and minutes, "1 h 30 min".
        return L10n.T("%s %s", hours, minutes);
    }

    /// <summary>
    /// detail.go <c>ResponseLabel</c>: an attendee's answer: Accepted,
    /// Tentative, Declined, Delegated; No Reply for needsAction, an unknown
    /// value and none.
    /// </summary>
    public static string ResponseLabel(EventResponse? r) => r?.Value switch
    {
        // TRANSLATORS: an attendee's answer to an invitation.
        EventResponse.Accepted => L10n.T("Accepted"),
        // TRANSLATORS: an attendee's answer to an invitation: maybe.
        EventResponse.Tentative => L10n.T("Tentative"),
        // TRANSLATORS: an attendee's answer to an invitation.
        EventResponse.Declined => L10n.T("Declined"),
        // TRANSLATORS: an attendee passed the invitation to someone else.
        EventResponse.Delegated => L10n.T("Delegated"),
        // TRANSLATORS: an attendee has not answered the invitation yet.
        _ => L10n.T("No Reply"),
    };

    /// <summary>detail.go <c>MyResponseText</c>: the popup's title of the user's answer, "You: Accepted".</summary>
    public static string MyResponseText(EventResponse? r) =>
        // TRANSLATORS: the event detail's popup of the user's own answer; %s is
        // Accepted, Tentative, Declined or No Reply.
        L10n.T("You: %s", ResponseLabel(r));

    /// <summary>detail.go <c>ResponseChoices</c>: the answers the popup and the card offer.</summary>
    public static IReadOnlyList<EventResponse> ResponseChoices { get; } =
        [new(EventResponse.Accepted), new(EventResponse.Tentative), new(EventResponse.Declined)];

    /// <summary>detail.go <c>ResponseAction</c>: a response button's title: Accept, Tentative, Decline.</summary>
    public static string ResponseAction(EventResponse r) => r.Value switch
    {
        // TRANSLATORS: the button that answers an invitation with maybe.
        EventResponse.Tentative => L10n.C("respond", "Tentative"),
        // TRANSLATORS: the button that declines an invitation.
        EventResponse.Declined => L10n.T("Decline"),
        // TRANSLATORS: the button that accepts an invitation.
        _ => L10n.T("Accept"),
    };

    /// <summary>detail.go <c>DeclineWithoutReply</c>: the Decline menu's item that declines without telling the organiser.</summary>
    public static string DeclineWithoutReply => L10n.T("Decline Without Reply");

    /// <summary>
    /// detail.go <c>PeopleLine</c>: "Organiser Jana Nováková · 4 attendees",
    /// "You organise · 4 attendees", or the attendee count alone; "" without
    /// attendees.
    /// </summary>
    public static string PeopleLine(CalendarEvent e)
    {
        ArgumentNullException.ThrowIfNull(e);
        var n = AttendeesOf(e);
        if (n == 0)
        {
            return "";
        }
        if (e.Organizer == true)
        {
            // TRANSLATORS: the event detail's people line of an event the user
            // organises; %d is the number of attendees.
            return L10n.N("You organise · %d attendee", "You organise · %d attendees", n);
        }
        var org = NameOf(e.OrganizerAddress);
        if (org.Length > 0)
        {
            // TRANSLATORS: the event detail's people line: the organiser's name
            // and the number of attendees.
            return L10n.N("Organiser %s · %d attendee", "Organiser %s · %d attendees", n, org, n);
        }
        return L10n.N("%d attendee", "%d attendees", n);
    }

    // The attendee count: the daemon's count, or the list when it is longer.
    private static int AttendeesOf(CalendarEvent e) => Math.Max(e.AttendeeCount ?? 0, e.Attendees?.Count ?? 0);

    /// <summary>detail.go <c>OrganizerLabel</c>: marks the organiser in the attendee list.</summary>
    public static string OrganizerLabel =>
        // TRANSLATORS: beside the organiser's name in an event's attendee list.
        L10n.T("organiser");

    /// <summary>detail.go <c>OptionalLabel</c>: marks an optional attendee.</summary>
    public static string OptionalLabel =>
        // TRANSLATORS: beside an optional attendee's name.
        L10n.T("optional");

    /// <summary>detail.go <c>ShowAllAttendees</c>: expands a collapsed attendee list.</summary>
    public static string ShowAllAttendees(int n) => L10n.N("Show All %d Attendee", "Show All %d Attendees", n);

    /// <summary>
    /// detail.go <c>CanRespond</c>: whether the popover offers the user's
    /// answer: the daemon allows it, the user is an attendee and not the
    /// organiser, and the event is not cancelled.
    /// </summary>
    public static bool CanRespond(CalendarEvent e)
    {
        ArgumentNullException.ThrowIfNull(e);
        return e.CanRespond && e.Organizer != true && e.MyResponse?.Value is { Length: > 0 } && e.Status != EventStatus.Cancelled;
    }

    /// <summary>detail.go <c>ShowsInvitation</c>: Show Invitation in Mail is offered (the event has invitation messages).</summary>
    public static bool ShowsInvitation(CalendarEvent e)
    {
        ArgumentNullException.ThrowIfNull(e);
        return e.InvitationMessages is { Count: > 0 };
    }

    /// <summary>detail.go <c>ShowInvitation</c>: the detail's button that reveals the invitation message in Mail.</summary>
    public static string ShowInvitation => L10n.T("Show Invitation in Mail");

    /// <summary>detail.go <c>Edit</c>: the detail's Edit button.</summary>
    public static string Edit => L10n.T("Edit");

    /// <summary>detail.go <c>JoinVideoCall</c>: the video-call row's link.</summary>
    public static string JoinVideoCall => L10n.T("Join Video Call");

    /// <summary>detail.go <c>AskAboutEvent</c>: the detail's ✦ menu item that asks the assistant.</summary>
    public static string AskAboutEvent => L10n.T("Ask about This Event");

    /// <summary>detail.go <c>FindAnotherTime</c>: the ✦ menu item that opens the editor with Next Free Slot.</summary>
    public static string FindAnotherTime => L10n.T("Find Another Time");

    /// <summary>
    /// detail.go <c>RemoveTitle</c>: the footer's destructive button:
    /// Delete for an event the user organises (it is deleted for everyone),
    /// else Remove from Calendar.
    /// </summary>
    public static string RemoveTitle(CalendarEvent e)
    {
        ArgumentNullException.ThrowIfNull(e);
        return e.RemoveIsDelete ? L10n.T("Delete") : RemoveFromCalendar;
    }

    /// <summary>detail.go <c>RemoveFromCalendar</c>: removes an event from the user's calendar (an invitation's event, a cancelled one).</summary>
    public static string RemoveFromCalendar => L10n.T("Remove from Calendar");

    /// <summary>
    /// detail.go <c>Question</c>: a confirmation: its heading, its text, the
    /// button that goes ahead and, when there is one, the alternative button
    /// (Cancel is always there).
    /// </summary>
    public sealed record Question
    {
        /// <summary>The heading.</summary>
        public string Heading { get; init; } = "";

        /// <summary>The text under the heading ("" none).</summary>
        public string Body { get; init; } = "";

        /// <summary>The button that goes ahead.</summary>
        public string Confirm { get; init; } = "";

        /// <summary>A second way to go ahead ("" none).</summary>
        public string Alternative { get; init; } = "";
    }

    /// <summary>
    /// detail.go <c>RemoveQuestion</c>: what removing <paramref name="e"/>
    /// asks: an attendee who has not declined is asked whether to tell the
    /// organiser (Confirm declines and removes, Alternative removes without a
    /// reply); the organiser of an event with attendees is told they will
    /// hear of it; otherwise null, and only the Mail preference
    /// confirm-delete decides whether to ask (<see cref="DeleteQuestion"/>).
    /// </summary>
    public static Question? RemoveQuestion(CalendarEvent e)
    {
        ArgumentNullException.ThrowIfNull(e);
        var title = CleanTitle(e.Title);
        if (!e.RemoveIsDelete && CanRespond(e) && e.MyResponse != EventResponse.Declined)
        {
            return new Question
            {
                Heading = L10n.T("Also tell the organiser you won’t attend?"),
                Body = L10n.T("“%s” will be removed from your calendar.", title),
                Confirm = L10n.T("Remove and Decline"),
                Alternative = L10n.T("Remove Only"),
            };
        }
        var n = AttendeesOf(e);
        if (e.RemoveIsDelete && n > 0)
        {
            return new Question
            {
                Heading = L10n.T("Delete the Event?"),
                Body = L10n.N("“%s” will be cancelled for its %d attendee.", "“%s” will be cancelled for its %d attendees.", n, title, n),
                Confirm = L10n.T("Delete"),
            };
        }
        return null;
    }

    /// <summary>detail.go <c>DeleteQuestion</c>: the plain confirmation of removing <paramref name="e"/>, asked only with the Mail preference confirm-delete.</summary>
    public static Question DeleteQuestion(CalendarEvent e)
    {
        ArgumentNullException.ThrowIfNull(e);
        return new Question { Heading = L10n.T("Remove “%s”?", CleanTitle(e.Title)), Confirm = RemoveTitle(e) };
    }
}
