// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarSampleData.swift; GTK:
// ui/internal/calendar/samples.go (SampleWork, SampleGmail, SampleNextcloud,
// the Sample* calendar ids, Samples, InvitationStates, SampleData,
// SampleAccountName, CalendarName), value for value.
//
// The invented sample data of MALACHI_CALENDAR_SAMPLES (docs/calendar.md
// §5.10), for layout checks without a daemon: three accounts (Work on
// Microsoft 365, Gmail, Nextcloud over CalDAV) with their calendars, the
// events of now's week, three unanswered invitations and one invitation
// card per state. Every name and address is invented (example.org,
// example.com). The data only; CalendarSamples.cs wraps it into a source.
// English and not for translation (Windows-only data, as macOS's
// macOS-only): a development aid and test data.
//
// Windows: Go's type Samples keeps Go's name, because Swift's name
// SampleData is Go's function's here (a nested type and a method of one
// name cannot share Cal). The calendars carry Swift's remote ids (Go leaves
// them empty; the C# record requires one). Go's e.Occurrence is
// OccurrencePart: a CalendarEvent derives from Occurrence, and the
// occurrences are records of their own, as Go's and Swift's copies are.
// The zone's name is the IANA one (TimeZoneInfo.Local carries a Windows id).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>samples.go <c>SampleWork</c>: the Work account (Microsoft 365).</summary>
    public static AccountId SampleWork { get; } = new("acc_sample_work");

    /// <summary>samples.go <c>SampleGmail</c>: the Gmail account.</summary>
    public static AccountId SampleGmail { get; } = new("acc_sample_gmail");

    /// <summary>samples.go <c>SampleNextcloud</c>: the Nextcloud account (CalDAV).</summary>
    public static AccountId SampleNextcloud { get; } = new("acc_sample_nextcloud");

    /// <summary>samples.go <c>SampleWorkCalendar</c>: Work's calendar.</summary>
    public static CalendarId SampleWorkCalendar { get; } = new("cal_sample_work");

    /// <summary>samples.go <c>SampleBirthdays</c>: Work's birthdays.</summary>
    public static CalendarId SampleBirthdays { get; } = new("cal_sample_birthdays");

    /// <summary>samples.go <c>SampleHolidays</c>: Work's holidays.</summary>
    public static CalendarId SampleHolidays { get; } = new("cal_sample_holidays");

    /// <summary>samples.go <c>SamplePersonal</c>: Gmail's own calendar.</summary>
    public static CalendarId SamplePersonal { get; } = new("cal_sample_personal");

    /// <summary>samples.go <c>SampleFamily</c>: a calendar shared with Gmail.</summary>
    public static CalendarId SampleFamily { get; } = new("cal_sample_family");

    /// <summary>samples.go <c>SampleSport</c>: Nextcloud's calendar.</summary>
    public static CalendarId SampleSport { get; } = new("cal_sample_sport");

    /// <summary>samples.go <c>InvitationStates</c>: the states of the sample cards, in their order.</summary>
    public static IReadOnlyList<InvitationState> InvitationStates { get; } =
    [
        InvitationState.Unanswered, InvitationState.Answered, InvitationState.Outdated,
        InvitationState.Cancelled, InvitationState.Processed, InvitationState.Invalid,
    ];

    /// <summary>samples.go <c>Samples</c> (Swift <c>Cal.SampleData</c>): the sample data of one moment.</summary>
    public sealed record Samples
    {
        /// <summary>The three accounts as account.list lists them.</summary>
        public IReadOnlyList<Account> Accounts { get; init => field = value ?? []; } = [];

        /// <summary>calendar.list's answer.</summary>
        public CalendarListResult Calendars { get; init => field = value ?? new() { Unanswered = 0 }; } = new() { Unanswered = 0 };

        /// <summary>The week's events (each instance of the standup on its own).</summary>
        public IReadOnlyList<CalendarEvent> Events { get; init => field = value ?? []; } = [];

        /// <summary>Their occurrences with the unanswered invitation that is not in a calendar.</summary>
        public IReadOnlyList<Occurrence> Occurrences { get; init => field = value ?? []; } = [];

        /// <summary>The three unanswered invitations (invitation.list).</summary>
        public IReadOnlyList<InvitationListItem> Invitations { get; init => field = value ?? []; } = [];

        /// <summary>One invitation card per state, in <see cref="InvitationStates"/>' order.</summary>
        public IReadOnlyList<InvitationCard> Cards { get; init => field = value ?? []; } = [];

        /// <summary>calendar.preferences: the daemon's defaults.</summary>
        public Api.CalendarPreferences Preferences { get; init => field = value ?? DefaultPreferences; } = DefaultPreferences;

        /// <summary>samples.go <c>SampleAccountName</c>: the name of a sample account ("" for another id).</summary>
        public string SampleAccountName(AccountId id) => Accounts.FirstOrDefault(a => a.Id == id)?.Config.Name ?? "";

        /// <summary>samples.go <c>CalendarName</c>: the name of a sample calendar ("" for another id).</summary>
        public string CalendarName(CalendarId id)
        {
            foreach (var a in Calendars.Accounts)
            {
                foreach (var c in a.Calendars)
                {
                    if (c.Id == id)
                    {
                        return c.Name;
                    }
                }
            }
            return "";
        }

        /// <summary>Whether both hold the same, the lists compared in order (Swift's Equatable).</summary>
        public bool Equals(Samples? other) =>
            other is not null && Board.SameList(Accounts, other.Accounts) && Calendars.Unanswered == other.Calendars.Unanswered
            && Board.SameList(Calendars.Accounts, other.Calendars.Accounts) && Board.SameList(Events, other.Events)
            && Board.SameList(Occurrences, other.Occurrences) && Board.SameList(Invitations, other.Invitations)
            && Board.SameList(Cards, other.Cards) && Preferences == other.Preferences;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Accounts.Count, Events.Count, Occurrences.Count, Cards.Count);
    }

    /// <summary>
    /// Go's <c>e.Occurrence</c> (Swift <c>event.occurrence</c>): the
    /// occurrence part of <paramref name="e"/> as an <see cref="Occurrence"/>
    /// record of its own, every member of <see cref="Occurrence"/> copied.
    /// </summary>
    public static Occurrence OccurrencePart(CalendarEvent e)
    {
        ArgumentNullException.ThrowIfNull(e);
        return new Occurrence
        {
            EventId = e.EventId,
            RecurrenceId = e.RecurrenceId,
            SeriesId = e.SeriesId,
            CalendarId = e.CalendarId,
            AccountId = e.AccountId,
            Title = e.Title,
            Start = e.Start,
            End = e.End,
            StartDate = e.StartDate,
            EndDate = e.EndDate,
            AllDay = e.AllDay,
            Floating = e.Floating,
            TimeZone = e.TimeZone,
            Location = e.Location,
            Status = e.Status,
            ShowAs = e.ShowAs,
            MyResponse = e.MyResponse,
            Organizer = e.Organizer,
            AttendeeCount = e.AttendeeCount,
            Recurring = e.Recurring,
            VideoCall = e.VideoCall,
            Private = e.Private,
            Alert = e.Alert,
            ReadOnly = e.ReadOnly,
            Invitation = e.Invitation,
        };
    }

    // The zone's IANA name (Go's loc.String()): a zone found by an IANA id
    // keeps it, a Windows one (TimeZoneInfo.Local) is converted.
    private static string SampleZoneName(TimeZoneInfo zone) =>
        zone.HasIanaId ? zone.Id : TimeZoneInfo.TryConvertWindowsIdToIanaId(zone.Id, out var iana) ? iana : zone.Id;

    /// <summary>
    /// samples.go <c>SampleData</c> (Swift <c>Cal.sampleData(now:zone:)</c>):
    /// the samples of the week of <paramref name="now"/> (from Monday) in
    /// <paramref name="zone"/>.
    /// </summary>
    public static Samples SampleData(DateTimeOffset now, TimeZoneInfo zone)
    {
        ArgumentNullException.ThrowIfNull(zone);
        // Windows-only strings: invented sample data, not for translation.
        var monday = StartOfWeek(now, Weekday.Monday, zone);
        DateTimeOffset At(int day, int h, int m)
        {
            var p = PartsOf(AddDays(monday, day, zone), zone);
            return Date(p.Year, p.Month, p.Day, h, m, zone).ToUniversalTime();
        }
        CalendarDate DayOf(int day) => DateOf(AddDays(monday, day, zone), zone);
        var synced = now.AddMinutes(-2).ToUniversalTime();
        var zoneName = new TimeZoneName(SampleZoneName(zone));

        var jana = new Address { Name = "Jana Nováková", Email = "jana@example.org" };
        var petr = new Address { Name = "Petr Svoboda", Email = "petr@example.org" };
        var club = new Address { Name = "Climbing Club", Email = "club@example.com" };
        const string me = "me@example.org";
        // Invented titles used more than once.
        const string demo = "Vendor demo";
        const string standup = "Standup";
        IReadOnlyList<Capability> mailAndCalendar = [.. API.MailCapabilities, Capability.Calendar];

        Account[] accounts =
        [
            new()
            {
                Id = SampleWork, Enabled = true, Capabilities = mailAndCalendar,
                State = new SyncState { AccountId = SampleWork, Status = SyncStatus.Idle },
                Config = new AccountConfig { Name = "Work", Email = me, Kind = AccountKind.Graph, Calendar = new CalendarConfig { Sync = true } },
            },
            new()
            {
                Id = SampleGmail, Enabled = true, Capabilities = mailAndCalendar,
                State = new SyncState { AccountId = SampleGmail, Status = SyncStatus.Idle },
                Config = new AccountConfig
                {
                    Name = "Gmail", Email = "me@example.com", Kind = AccountKind.Imap,
                    OAuth2 = new OAuth2Config { Provider = OAuth2Provider.Google },
                },
            },
            new()
            {
                Id = SampleNextcloud, Enabled = true, Capabilities = [Capability.Calendar],
                State = new SyncState { AccountId = SampleNextcloud, Status = SyncStatus.Idle },
                Config = new AccountConfig
                {
                    Name = "Nextcloud", Email = "me@example.com", Kind = AccountKind.CalDAV,
                    CalDAV = new CalDAVConfig { ServerUrl = "https://cloud.example.com/remote.php/dav", Username = "me" },
                    Calendar = new CalendarConfig { Sync = true },
                },
            },
        ];
        CalendarSyncState State(AccountId id) =>
            new() { AccountId = id, Status = CalendarSyncStatus.Idle, Progress = -1, LastSync = synced };
        var window = new CalendarWindow
        {
            From = DateOf(AddDays(monday, -365, zone), zone),
            To = DateOf(AddDays(monday, API.Limits.CalendarFutureDays, zone), zone),
        };
        var calendars = new CalendarListResult
        {
            Unanswered = 3,
            Accounts =
            [
                new()
                {
                    AccountId = SampleWork, Source = CalendarSource.Graph, Sync = true, State = State(SampleWork), Window = window,
                    Features = new CalendarFeatures
                    {
                        FreeBusy = true, ProposeNewTime = true, Invitations = InvitationsVia.Server, OnlineMeetings = true,
                    },
                    Calendars =
                    [
                        new()
                        {
                            Id = SampleWorkCalendar, AccountId = SampleWork, RemoteId = "AAMkSampleCalendar", Name = "Calendar",
                            Color = "#1f6feb", Visible = true, Access = CalendarAccess.Owner, Kind = CalendarKind.Events,
                            IsDefault = true, TimeZone = new TimeZoneName("Europe/Prague"), OnlineMeetingProviders = ["teamsForBusiness"],
                        },
                        new()
                        {
                            Id = SampleBirthdays, AccountId = SampleWork, RemoteId = "AAMkSampleBirthdays", Name = "Birthdays",
                            Color = "#8250df", Visible = true, Access = CalendarAccess.Read, Kind = CalendarKind.Birthdays,
                        },
                        new()
                        {
                            Id = SampleHolidays, AccountId = SampleWork, RemoteId = "AAMkSampleHolidays", Name = "Czech holidays",
                            Color = "#1a7f37", Visible = true, Access = CalendarAccess.Read, Kind = CalendarKind.Holidays,
                        },
                    ],
                },
                new()
                {
                    AccountId = SampleGmail, Source = CalendarSource.Google, Sync = true, State = State(SampleGmail), Window = window,
                    Features = new CalendarFeatures
                    {
                        FreeBusy = false, ProposeNewTime = false, Invitations = InvitationsVia.Server, OnlineMeetings = false,
                    },
                    Calendars =
                    [
                        new()
                        {
                            Id = SamplePersonal, AccountId = SampleGmail, RemoteId = "me@example.com", Name = "Personal",
                            Color = "#e5534b", Visible = true, Access = CalendarAccess.Owner, Kind = CalendarKind.Events, IsDefault = true,
                        },
                        new()
                        {
                            Id = SampleFamily, AccountId = SampleGmail, RemoteId = "family@group.example.com", Name = "Family",
                            Color = "#d4a72c", Visible = true, Access = CalendarAccess.Write, Kind = CalendarKind.Events, Owner = petr,
                        },
                    ],
                },
                new()
                {
                    AccountId = SampleNextcloud, Source = CalendarSource.CalDAV, Sync = true, State = State(SampleNextcloud), Window = window,
                    Features = new CalendarFeatures
                    {
                        FreeBusy = false, ProposeNewTime = false, Invitations = InvitationsVia.Mail, MailAccountId = SampleGmail,
                        OnlineMeetings = false,
                    },
                    Calendars =
                    [
                        new()
                        {
                            Id = SampleSport, AccountId = SampleNextcloud, RemoteId = "/remote.php/dav/calendars/me/sport/", Name = "Sport",
                            Color = "#0082c9", Visible = true, Access = CalendarAccess.Owner, Kind = CalendarKind.Events, IsDefault = true,
                        },
                    ],
                },
            ],
        };

        CalendarEvent Timed(string id, CalendarId cal, AccountId acc, string title, int day, int h, int m, int eh, int em) => new()
        {
            EventId = new CalendarEventId(id),
            CalendarId = cal,
            AccountId = acc,
            Title = title,
            Start = At(day, h, m),
            End = At(day, eh, em),
            AllDay = false,
            Status = EventStatus.Confirmed,
            ShowAs = ShowAs.Busy,
            TimeZone = zoneName,
            ETag = "\"1\"",
            CanEdit = true,
            CanDelete = true,
            CanRespond = false,
            CanProposeNewTime = false,
            RemoveIsDelete = true,
        };
        CalendarEvent AllDay(string id, CalendarId cal, AccountId acc, string title, int day, int days) => new()
        {
            EventId = new CalendarEventId(id),
            CalendarId = cal,
            AccountId = acc,
            Title = title,
            AllDay = true,
            StartDate = DayOf(day),
            EndDate = DayOf(day + days),
            Status = EventStatus.Confirmed,
            ShowAs = ShowAs.Free,
            ETag = "\"1\"",
            CanEdit = false,
            CanDelete = false,
            CanRespond = false,
            CanProposeNewTime = false,
            RemoveIsDelete = false,
        };
        IReadOnlyList<Attendee> Attendees(EventResponse mine) =>
        [
            new()
            {
                Name = jana.Name, Address = jana.Email, Role = AttendeeRole.Chair, Response = EventResponse.Accepted,
                Type = AttendeeType.Person, Organizer = true,
            },
            new() { Name = "Me", Address = me, Role = AttendeeRole.Required, Response = mine, Type = AttendeeType.Person, Me = true },
            new()
            {
                Name = petr.Name, Address = petr.Email, Role = AttendeeRole.Required, Response = EventResponse.Tentative,
                Type = AttendeeType.Person,
            },
            new() { Address = "eva@example.org", Role = AttendeeRole.Optional, Response = EventResponse.NeedsAction, Type = AttendeeType.Person },
        ];
        CalendarEvent Invited(CalendarEvent e, EventResponse mine) => e with
        {
            MyResponse = mine,
            Organizer = false,
            AttendeeCount = 4,
            Attendees = Attendees(mine),
            OrganizerAddress = jana,
            CanEdit = false,
            CanRespond = true,
            RemoveIsDelete = false,
            ResponseRequested = true,
        };

        var events = new List<CalendarEvent>();
        var standupRule = new Recurrence
        {
            Frequency = RecurrenceFrequency.Weekly,
            Interval = 1,
            ByDay = ["MO", "TU", "WE", "TH", "FR"],
        };
        for (var day = 0; day < 5; day++)
        {
            events.Add(Timed("ev_sample_standup", SampleWorkCalendar, SampleWork, standup, day, 9, 0, 9, 15) with
            {
                RecurrenceId = new DateOrTime(Rfc3339.Format(At(day, 9, 0))),
                Recurring = true,
                Recurrence = standupRule,
                Location = "Room B",
                Alert = true,
                Alerts = [5],
            });
        }

        var planning = Invited(Timed("ev_sample_planning", SampleWorkCalendar, SampleWork, "Q4 planning", 1, 10, 0, 11, 30), EventResponse.Accepted) with
        {
            VideoCall = true,
            Location = "Room A",
            VideoCallLink = new Api.VideoCall
            {
                Url = "https://teams.microsoft.com/l/meetup-join/sample",
                Provider = VideoProvider.Teams,
                Host = "teams.microsoft.com",
            },
            Notes = "Agenda: budget, hiring, the roadmap.\nSlides: https://example.org/q4-slides",
            Links = ["https://example.org/q4-slides"],
            CanProposeNewTime = true,
            InvitationMessages = [new MessageRef { AccountId = SampleWork, MessageId = new MessageId("msg_sample_planning") }],
        };
        var supplier = Timed("ev_sample_supplier", SamplePersonal, SampleGmail, "Call with the supplier", 1, 10, 30, 11, 0);
        var review = Invited(Timed("ev_sample_review", SampleWorkCalendar, SampleWork, "Design review", 1, 11, 0, 12, 0), EventResponse.Tentative) with
        {
            ShowAs = ShowAs.Tentative,
        };
        var budget = Invited(Timed("ev_sample_budget", SampleWorkCalendar, SampleWork, "Budget review", 2, 14, 0, 15, 0), EventResponse.NeedsAction);
        var allHands = Invited(Timed("ev_sample_allhands", SampleWorkCalendar, SampleWork, "All-hands", 2, 16, 0, 17, 0), EventResponse.Declined);
        var offsite = AllDay("ev_sample_offsite", SampleWorkCalendar, SampleWork, "Team offsite", 3, 2) with
        {
            CanEdit = true,
            CanDelete = true,
            RemoveIsDelete = true,
            Location = "Hotel Horizont",
        };
        var climbing = Timed("ev_sample_climbing", SampleSport, SampleNextcloud, "Climbing", 3, 18, 0, 19, 30) with { Location = "Boulder hall" };
        var birthday = AllDay("ev_sample_birthday", SampleBirthdays, SampleWork, "Petr Svoboda’s birthday", 5, 1) with { ReadOnly = true };
        var brunch = Timed("ev_sample_brunch", SampleFamily, SampleGmail, "Family brunch", 6, 10, 0, 12, 0);
        events.AddRange([planning, supplier, review, budget, allHands, offsite, climbing, birthday, brunch]);

        var occurrences = events.Select(OccurrencePart).ToList();
        var demoStart = At(4, 15, 0);
        var demoEnd = At(4, 16, 0);
        // The vendor demo is an invitation on the Gmail account not in any
        // calendar: drawn tentatively.
        occurrences.Add(new Occurrence
        {
            AccountId = SampleGmail,
            Title = demo,
            Start = demoStart,
            End = demoEnd,
            AllDay = false,
            Status = EventStatus.Confirmed,
            ShowAs = ShowAs.Tentative,
            MyResponse = EventResponse.NeedsAction,
            AttendeeCount = 3,
            Invitation = new MessageRef { AccountId = SampleGmail, MessageId = new MessageId("msg_sample_demo") },
        });

        var clubStart = At(9, 19, 0);
        var clubEnd = At(9, 21, 0);
        InvitationListItem[] invitations =
        [
            new()
            {
                AccountId = SampleWork, MessageId = new MessageId("msg_sample_budget"),
                Summary = new InvitationSummary
                {
                    Method = InvitationMethod.Request, Kind = InvitationKind.Invitation, State = InvitationState.Unanswered,
                    Title = budget.Title, Start = budget.Start, End = budget.End, Organizer = jana,
                },
            },
            new()
            {
                AccountId = SampleGmail, MessageId = new MessageId("msg_sample_demo"),
                Summary = new InvitationSummary
                {
                    Method = InvitationMethod.Request, Kind = InvitationKind.Invitation, State = InvitationState.Unanswered,
                    Title = demo, Start = demoStart, End = demoEnd, Organizer = petr,
                },
            },
            new()
            {
                AccountId = SampleGmail, MessageId = new MessageId("msg_sample_club"),
                Summary = new InvitationSummary
                {
                    Method = InvitationMethod.Request, Kind = InvitationKind.Update, State = InvitationState.Unanswered,
                    Title = "Club meeting", Start = clubStart, End = clubEnd, Organizer = club,
                },
            },
        ];

        InvitationCard Card(InvitationState state)
        {
            var c = new InvitationCard
            {
                Method = InvitationMethod.Request,
                Kind = InvitationKind.Invitation,
                State = state,
                Title = demo,
                Start = demoStart,
                End = demoEnd,
                Organizer = petr,
                PartId = "2",
                Uid = "sample-demo@example.org",
                Sequence = 0,
                TimeZone = zoneName,
                Location = "Room C",
                AttendeeCount = 3,
                Attendees =
                [
                    new()
                    {
                        Name = petr.Name, Address = petr.Email, Role = AttendeeRole.Chair, Response = EventResponse.Accepted,
                        Type = AttendeeType.Person, Organizer = true,
                    },
                    new()
                    {
                        Address = "me@example.com", Role = AttendeeRole.Required, Response = EventResponse.NeedsAction,
                        Type = AttendeeType.Person, Me = true,
                    },
                    new()
                    {
                        Name = jana.Name, Address = jana.Email, Role = AttendeeRole.Optional, Response = EventResponse.NeedsAction,
                        Type = AttendeeType.Person,
                    },
                ],
                Me = new InvitationMe { Address = "me@example.com", Response = EventResponse.NeedsAction },
                Conflicts =
                [
                    new()
                    {
                        Title = standup, Start = At(4, 15, 30), End = At(4, 16, 0), CalendarId = SampleWorkCalendar,
                        EventId = new CalendarEventId("ev_sample_conflict"),
                    },
                ],
                Day = new InvitationDay
                {
                    From = At(4, 8, 0),
                    To = At(4, 17, 0),
                    Busy =
                    [
                        new() { Start = At(4, 9, 0), End = At(4, 9, 15), CalendarId = SampleWorkCalendar },
                        new() { Start = At(4, 15, 30), End = At(4, 16, 0), CalendarId = SampleWorkCalendar },
                    ],
                },
                Targets = [new() { CalendarId = SamplePersonal, IsDefault = true }, new() { CalendarId = SampleFamily }],
                CanRespond = true,
                CanProposeNewTime = false,
                ReplyVia = InvitationsVia.Mail,
                ReplyAccountId = SampleGmail,
                ReplyTo = petr,
            };
            return state.Value switch
            {
                InvitationState.Answered => c with
                {
                    Response = EventResponse.Accepted,
                    CalendarId = SamplePersonal,
                    EventId = new CalendarEventId("ev_sample_demo"),
                    RespondedAt = now.AddHours(-1).ToUniversalTime(),
                    Me = new InvitationMe { Address = "me@example.com", Response = EventResponse.Accepted },
                },
                InvitationState.Outdated => c with
                {
                    Kind = InvitationKind.Update,
                    Sequence = 1,
                    CanRespond = false,
                    Previous = new InvitationPrevious
                    {
                        Start = new DateOrTime(Rfc3339.Format(At(4, 14, 0))),
                        End = new DateOrTime(Rfc3339.Format(At(4, 15, 0))),
                    },
                },
                InvitationState.Cancelled => c with
                {
                    Method = InvitationMethod.Cancel,
                    Kind = InvitationKind.Cancelled,
                    CanRespond = false,
                    Targets = [],
                    EventId = new CalendarEventId("ev_sample_demo"),
                    CalendarId = SamplePersonal,
                },
                InvitationState.Processed => c with
                {
                    Method = InvitationMethod.Reply,
                    Kind = InvitationKind.Reply,
                    CanRespond = false,
                    Targets = [],
                    Organizer = new Address { Name = "Me", Email = "me@example.com" },
                    ReplyFrom = jana,
                    Response = EventResponse.Accepted,
                    ReplyApplied = true,
                    Conflicts = [],
                },
                InvitationState.Invalid => new InvitationCard
                {
                    Method = InvitationMethod.Other,
                    Kind = InvitationKind.Other,
                    State = InvitationState.Invalid,
                    Title = "",
                    PartId = "2",
                    Uid = "",
                    Sequence = 0,
                    AttendeeCount = 0,
                    Conflicts = [],
                    Targets = [],
                    Day = new InvitationDay { From = DateTimeOffset.MinValue, To = DateTimeOffset.MinValue, Busy = [] },
                    CanRespond = false,
                    CanProposeNewTime = false,
                    ReplyVia = new InvitationsVia(""),
                },
                _ => c,
            };
        }

        return new Samples
        {
            Accounts = accounts,
            Calendars = calendars,
            Events = events,
            Occurrences = occurrences,
            Invitations = invitations,
            Cards = [.. InvitationStates.Select(Card)],
            Preferences = DefaultPreferences,
        };
    }
}
