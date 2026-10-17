// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarHelpersTests.swift (CalFixture
// event, card); GTK: the zero values api.Event{} and api.InvitationCard{} of
// detail_test.go, editor_test.go and invitation_test.go, and the events and
// cards of ui/internal/calendar/samples.go those tests read (Q4 planning,
// the standup, the vendor demo's card per state).
//
// The detail, editor, invitation and invitation-reading tests build on a
// bare event and a bare card and change what a case needs with `with`. The
// sample-like values are written here rather than taken from Cal's sample
// data, whose port is separate; they are the samples' own (the week of
// Wednesday 14 October 2026 in Prague).

using System;
using Malachi.Core.Api;
using Malachi.Core.Calendars;

namespace Malachi.Core.Tests.Calendars;

internal static partial class CalFixture
{
    /// <summary>A bare event (Go's api.Event{}, Swift's F.event()).</summary>
    public static CalendarEvent Event() => new()
    {
        AccountId = new AccountId(""),
        Title = "",
        AllDay = false,
        Status = new EventStatus(""),
        ShowAs = new ShowAs(""),
        ETag = "",
        CanEdit = false,
        CanDelete = false,
        CanRespond = false,
        CanProposeNewTime = false,
        RemoveIsDelete = false,
    };

    /// <summary>A bare invitation card (Go's api.InvitationCard{}, Swift's F.card()).</summary>
    public static InvitationCard Card() => new()
    {
        Method = new InvitationMethod(""),
        Kind = new InvitationKind(""),
        State = new InvitationState(""),
        Title = "",
        PartId = "",
        Uid = "",
        Sequence = 0,
        AttendeeCount = 0,
        Day = new InvitationDay { From = DateTimeOffset.UnixEpoch, To = DateTimeOffset.UnixEpoch },
        CanRespond = false,
        CanProposeNewTime = false,
        ReplyVia = new InvitationsVia(""),
    };

    private static readonly Address Jana = new() { Name = "Jana Nováková", Email = "jana@example.org" };

    private static readonly Address Petr = new() { Name = "Petr Svoboda", Email = "petr@example.org" };

    /// <summary>The samples' "Q4 planning": Tuesday 13 October 2026 10:00–11:30, the user an attendee who accepted.</summary>
    public static CalendarEvent Planning() => Event() with
    {
        EventId = new CalendarEventId("ev_sample_planning"),
        CalendarId = new CalendarId("cal_sample_work"),
        AccountId = new AccountId("acc_sample_work"),
        Title = "Q4 planning",
        Start = At(2026, 10, 13, 10).ToUniversalTime(),
        End = At(2026, 10, 13, 11, 30).ToUniversalTime(),
        Status = EventStatus.Confirmed,
        ShowAs = ShowAs.Busy,
        TimeZone = new TimeZoneName("Europe/Prague"),
        ETag = "\"1\"",
        CanDelete = true,
        MyResponse = EventResponse.Accepted,
        AttendeeCount = 4,
        Attendees =
        [
            new Attendee
            {
                Name = Jana.Name, Address = Jana.Email, Role = AttendeeRole.Chair, Response = EventResponse.Accepted,
                Type = AttendeeType.Person, Organizer = true,
            },
            new Attendee
            {
                Name = "Me", Address = "me@example.org", Role = AttendeeRole.Required, Response = EventResponse.Accepted,
                Type = AttendeeType.Person, Me = true,
            },
            new Attendee
            {
                Name = Petr.Name, Address = Petr.Email, Role = AttendeeRole.Required, Response = EventResponse.Tentative,
                Type = AttendeeType.Person,
            },
            new Attendee
            {
                Address = "eva@example.org", Role = AttendeeRole.Optional, Response = EventResponse.NeedsAction,
                Type = AttendeeType.Person,
            },
        ],
        OrganizerAddress = Jana,
        CanRespond = true,
        ResponseRequested = true,
        VideoCall = true,
        Location = "Room A",
        VideoCallLink = new VideoCall
        {
            Url = "https://teams.microsoft.com/l/meetup-join/sample",
            Provider = VideoProvider.Teams,
            Host = "teams.microsoft.com",
        },
        Notes = "Agenda: budget, hiring, the roadmap.\nSlides: https://example.org/q4-slides",
        Links = ["https://example.org/q4-slides"],
        CanProposeNewTime = true,
        InvitationMessages = [new MessageRef { AccountId = new AccountId("acc_sample_work"), MessageId = new MessageId("msg_sample_planning") }],
    };

    /// <summary>The samples' first standup: Monday 12 October 2026 9:00–9:15, every weekday, the user's own.</summary>
    public static CalendarEvent Standup() => Event() with
    {
        EventId = new CalendarEventId("ev_sample_standup"),
        RecurrenceId = new DateOrTime(Rfc3339.Format(At(2026, 10, 12, 9))),
        CalendarId = new CalendarId("cal_sample_work"),
        AccountId = new AccountId("acc_sample_work"),
        Title = "Standup",
        Start = At(2026, 10, 12, 9).ToUniversalTime(),
        End = At(2026, 10, 12, 9, 15).ToUniversalTime(),
        Status = EventStatus.Confirmed,
        ShowAs = ShowAs.Busy,
        TimeZone = new TimeZoneName("Europe/Prague"),
        ETag = "\"1\"",
        CanEdit = true,
        CanDelete = true,
        RemoveIsDelete = true,
        Recurring = true,
        Recurrence = new Recurrence { Frequency = RecurrenceFrequency.Weekly, Interval = 1, ByDay = ["MO", "TU", "WE", "TH", "FR"] },
        Location = "Room B",
        Alert = true,
        Alerts = [5],
    };

    /// <summary>The samples' card states, in their order (Go's InvitationStates).</summary>
    public static readonly InvitationState[] InvitationStates =
    [
        InvitationState.Unanswered, InvitationState.Answered, InvitationState.Outdated, InvitationState.Cancelled,
        InvitationState.Processed, InvitationState.Invalid,
    ];

    /// <summary>
    /// The samples' card of the vendor demo in <paramref name="state"/>
    /// (Friday 16 October 2026 15:00–16:00 from Petr Svoboda, a conflict
    /// with the standup, the day strip 8:00–17:00); <paramref name="now"/>
    /// is the samples' moment (an answer was given an hour before).
    /// </summary>
    public static InvitationCard DemoCard(InvitationState state, DateTimeOffset? now = null)
    {
        DateTimeOffset Fri(int h, int mi = 0) => At(2026, 10, 16, h, mi).ToUniversalTime();
        var c = Card() with
        {
            Method = InvitationMethod.Request,
            Kind = InvitationKind.Invitation,
            State = state,
            Title = "Vendor demo",
            Start = Fri(15),
            End = Fri(16),
            Organizer = Petr,
            PartId = "2",
            Uid = "sample-demo@example.org",
            TimeZone = new TimeZoneName("Europe/Prague"),
            Location = "Room C",
            AttendeeCount = 3,
            Attendees =
            [
                new Attendee
                {
                    Name = Petr.Name, Address = Petr.Email, Role = AttendeeRole.Chair, Response = EventResponse.Accepted,
                    Type = AttendeeType.Person, Organizer = true,
                },
                new Attendee
                {
                    Address = "me@example.com", Role = AttendeeRole.Required, Response = EventResponse.NeedsAction,
                    Type = AttendeeType.Person, Me = true,
                },
                new Attendee
                {
                    Name = Jana.Name, Address = Jana.Email, Role = AttendeeRole.Optional, Response = EventResponse.NeedsAction,
                    Type = AttendeeType.Person,
                },
            ],
            Me = new InvitationMe { Address = "me@example.com", Response = EventResponse.NeedsAction },
            Conflicts =
            [
                new InvitationConflict
                {
                    Title = "Standup", Start = Fri(15, 30), End = Fri(16), CalendarId = new CalendarId("cal_sample_work"),
                    EventId = new CalendarEventId("ev_sample_conflict"),
                },
            ],
            Day = new InvitationDay
            {
                From = Fri(8),
                To = Fri(17),
                Busy =
                [
                    new DayBusy { Start = Fri(9), End = Fri(9, 15), CalendarId = new CalendarId("cal_sample_work") },
                    new DayBusy { Start = Fri(15, 30), End = Fri(16), CalendarId = new CalendarId("cal_sample_work") },
                ],
            },
            Targets =
            [
                new InvitationTarget { CalendarId = new CalendarId("cal_sample_personal"), IsDefault = true },
                new InvitationTarget { CalendarId = new CalendarId("cal_sample_family") },
            ],
            CanRespond = true,
            ReplyVia = InvitationsVia.Mail,
            ReplyAccountId = new AccountId("acc_sample_gmail"),
            ReplyTo = Petr,
        };
        switch (state.Value)
        {
            case InvitationState.Answered:
                return c with
                {
                    Response = EventResponse.Accepted,
                    CalendarId = new CalendarId("cal_sample_personal"),
                    EventId = new CalendarEventId("ev_sample_demo"),
                    RespondedAt = (now ?? At(2026, 10, 14, 12)).AddHours(-1).ToUniversalTime(),
                    Me = c.Me! with { Response = EventResponse.Accepted },
                };
            case InvitationState.Outdated:
                return c with
                {
                    Kind = InvitationKind.Update,
                    Sequence = 1,
                    CanRespond = false,
                    Previous = new InvitationPrevious
                    {
                        Start = new DateOrTime(Rfc3339.Format(Fri(14))),
                        End = new DateOrTime(Rfc3339.Format(Fri(15))),
                    },
                };
            case InvitationState.Cancelled:
                return c with
                {
                    Method = InvitationMethod.Cancel,
                    Kind = InvitationKind.Cancelled,
                    CanRespond = false,
                    Targets = [],
                    EventId = new CalendarEventId("ev_sample_demo"),
                    CalendarId = new CalendarId("cal_sample_personal"),
                };
            case InvitationState.Processed:
                return c with
                {
                    Method = InvitationMethod.Reply,
                    Kind = InvitationKind.Reply,
                    CanRespond = false,
                    Targets = [],
                    Organizer = new Address { Name = "Me", Email = "me@example.com" },
                    ReplyFrom = Jana,
                    Response = EventResponse.Accepted,
                    ReplyApplied = true,
                    Conflicts = [],
                };
            case InvitationState.Invalid:
                return Card() with
                {
                    Method = InvitationMethod.Other,
                    Kind = InvitationKind.Other,
                    State = InvitationState.Invalid,
                    PartId = "2",
                    Day = new InvitationDay { From = default, To = default },
                };
            default:
                return c;
        }
    }

    /// <summary>The samples' calendar names by id ("" for another).</summary>
    public static string SampleCalendarName(CalendarId id) => id.Value switch
    {
        "cal_sample_work" => "Calendar",
        "cal_sample_birthdays" => "Birthdays",
        "cal_sample_holidays" => "Czech holidays",
        "cal_sample_personal" => "Personal",
        "cal_sample_family" => "Family",
        "cal_sample_sport" => "Sport",
        _ => "",
    };
}
