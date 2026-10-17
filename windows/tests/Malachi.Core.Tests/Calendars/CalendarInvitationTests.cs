// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarInvitationTests.swift; GTK:
// ui/internal/calendar/invitation_test.go (TestPillKind, TestSampleCards,
// TestInvitationLines, TestPreviousLines, TestOwnEvent), with Swift's
// stateStripWithoutRespondedAt and previousLine. TestSampleCards reads the
// vendor demo's cards of CalFixture.Events.cs (the samples' own values).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarInvitationTests
{
    [Fact]
    public void PillKind()
    {
        var kinds = new (string Kind, Cal.InvitationPill Want)[]
        {
            (InvitationKind.Invitation, Cal.InvitationPill.Invitation), (InvitationKind.Update, Cal.InvitationPill.Update),
            (InvitationKind.Cancelled, Cal.InvitationPill.Cancelled), (InvitationKind.Reply, Cal.InvitationPill.Reply),
            (InvitationKind.Other, Cal.InvitationPill.None), ("future", Cal.InvitationPill.None),
        };
        string[] methods =
        [
            InvitationMethod.Request, InvitationMethod.Reply, InvitationMethod.Cancel, InvitationMethod.Counter,
            InvitationMethod.DeclineCounter, InvitationMethod.Refresh, InvitationMethod.Add, InvitationMethod.Publish,
            InvitationMethod.Other,
        ];
        foreach (var (kind, want) in kinds)
        {
            foreach (var st in F.InvitationStates)
            {
                foreach (var m in methods)
                {
                    var got = Cal.PillKind(new InvitationSummary { Method = m, Kind = kind, State = st, Title = "" });
                    Assert.True(got == (st == InvitationState.Invalid ? Cal.InvitationPill.None : want), $"{m} {kind} {st}");
                }
            }
        }
        Assert.Equal(Cal.InvitationPill.None, Cal.PillKind(null));
    }

    [Theory]
    [InlineData(Cal.InvitationPill.None, "")]
    [InlineData(Cal.InvitationPill.Invitation, "Invitation")]
    [InlineData(Cal.InvitationPill.Update, "Update")]
    [InlineData(Cal.InvitationPill.Cancelled, "Cancelled")]
    [InlineData(Cal.InvitationPill.Reply, "Reply")]
    public void PillTitles(Cal.InvitationPill p, string want)
    {
        Assert.Equal(want, Cal.PillTitle(p));
        Assert.Equal(p == Cal.InvitationPill.Cancelled, Cal.StrikesSubject(p));
    }

    private const string Conflict = "Conflicts with Standup (15:30–16:00) in calendar Calendar";

    [Theory]
    [InlineData(InvitationState.Unanswered, Cal.CardMode.Full, true, false, false, "", "Petr Svoboda replied", Conflict)]
    [InlineData(InvitationState.Answered, Cal.CardMode.Full, false, true, false, "", "Petr Svoboda accepted", Conflict)]
    [InlineData(InvitationState.Outdated, Cal.CardMode.Full, false, false, false,
        "This invitation is out of date: a newer one arrived, or the event is over.", "Petr Svoboda replied", Conflict)]
    [InlineData(InvitationState.Cancelled, Cal.CardMode.Cancelled, false, false, false, "This event was cancelled.", "Petr Svoboda replied", Conflict)]
    [InlineData(InvitationState.Processed, Cal.CardMode.Reply, false, false, false, "", "Jana Nováková accepted (applied to your event)", "")]
    [InlineData(InvitationState.Invalid, Cal.CardMode.None, false, false, false, "The invitation in this message could not be read.", " replied", "")]
    public void SampleCards(
        string state, Cal.CardMode mode, bool responses, bool strip, bool propose, string stateLine, string reply, string conflict)
    {
        var c = F.DemoCard(state);
        Assert.Equal(mode, Cal.CardModeOf(c, true));
        Assert.Equal(responses, Cal.ShowsResponses(c));
        Assert.Equal(strip, Cal.ShowsStateStrip(c));
        Assert.Equal(propose, Cal.ShowsProposeNewTime(c));
        Assert.Equal(stateLine, Cal.StateLine(c));
        Assert.Equal(reply, Cal.ReplyCard(c));
        Assert.Equal(conflict, Cal.ConflictLine(c.Conflicts, F.SampleCalendarName, F.Env()));
        Assert.Equal(Cal.CardMode.None, Cal.CardModeOf(c, false));
    }

    [Fact]
    public void SampleCardLines()
    {
        var env = F.Env();
        var c = F.DemoCard(InvitationState.Unanswered);
        Assert.Equal(new Cal.DateTile("Fri", "16", "Oct"), Cal.DateTileOf(c, env));
        Assert.Equal("Friday 16 October 2026 · 15:00–16:00 · 1 h", Cal.CardTimeLine(c, env));
        Assert.Equal("Organiser Petr Svoboda · 3 attendees", Cal.CardPeopleLine(c));
        Assert.Equal("Vendor demo", Cal.CardTitle(c));
        Assert.Equal(["The reply leaves from account Gmail.", "The reply goes to Petr Svoboda <petr@example.org>."], Cal.ReplyNote(c, "Gmail"));
        Assert.Equal("Accepted · in calendar Personal", Cal.StateStrip(F.DemoCard(InvitationState.Answered), "Personal"));
        Assert.Equal(["Previously Friday 16 October 2026 · 14:00–15:00 · 1 h"], Cal.PreviousLines(F.DemoCard(InvitationState.Outdated), env));
        Assert.Equal(Cal.CardMode.None, Cal.CardModeOf(null, true));
        Assert.Equal(Cal.CardMode.None, Cal.CardModeOf(c with { Kind = InvitationKind.Other }, true));
        Assert.True(Cal.ShowsProposeNewTime(c with { CanProposeNewTime = true }));
        Assert.True(Cal.ShowsProposeNewTime(F.DemoCard(InvitationState.Answered) with { CanProposeNewTime = true }));
        Assert.False(Cal.ShowsProposeNewTime(F.DemoCard(InvitationState.Outdated) with { CanProposeNewTime = true }));
    }

    private static InvitationConflict ConflictOf(string? title, DateTimeOffset s, DateTimeOffset e) =>
        new() { Title = title, Start = s, End = e, CalendarId = new CalendarId(""), EventId = new CalendarEventId("") };

    [Fact]
    public void ConflictLines()
    {
        var env = F.Env();
        var s = F.At(2026, 10, 14, 10);
        var e = F.At(2026, 10, 14, 10, 30);
        static string Name(CalendarId id) => "Work · Calendar";
        Assert.Equal("", Cal.ConflictLine([], Name, env));
        Assert.Equal("", Cal.ConflictLine(null, Name, env));
        Assert.Equal("Conflicts with Standup (10:00–10:30) in calendar Work · Calendar", Cal.ConflictLine([ConflictOf("Standup", s, e)], Name, env));
        Assert.Equal(
            "Conflicts with a private event (10:00–10:30) in calendar Work · Calendar", Cal.ConflictLine([ConflictOf(null, s, e)], Name, env));
        var zero = DateTimeOffset.UnixEpoch;
        Assert.Equal(
            "Conflicts with 3 events",
            Cal.ConflictLine([ConflictOf(null, zero, zero), ConflictOf(null, zero, zero), ConflictOf(null, zero, zero)], Name, env));
        // Windows-only: a conflict's title and calendar name are display text.
        Assert.Equal(
            "Conflicts with a private event (10:00–10:30) in calendar Work",
            Cal.ConflictLine([ConflictOf("\u200B\u202E", s, e)], _ => "Wo\u202Erk", env));
    }

    [Fact]
    public void ReplyNotes()
    {
        var org = new Address { Email = "jana@example.org" };
        Assert.Equal(
            ["The reply goes to jana@example.org."],
            Cal.ReplyNote(F.Card() with { ReplyVia = InvitationsVia.Server, ReplyTo = org }, ""));
        Assert.Equal(["No reply will be sent."], Cal.ReplyNote(F.Card() with { ReplyVia = InvitationsVia.None }, ""));
        Assert.Equal(
            ["The reply leaves from account Work.",
                "The organiser’s address is not from the sender’s domain. Check the invitation before you answer."],
            Cal.ReplyNote(F.Card() with { ReplyVia = InvitationsVia.Mail, OrganizerMismatch = true }, "Work"));
        Assert.Empty(Cal.ReplyNote(F.Card() with { ReplyVia = InvitationsVia.Server }, "Work"));
    }

    [Theory]
    [InlineData(EventResponse.Accepted, "Work", "Accepted · in calendar Work")]
    [InlineData(EventResponse.Tentative, "", "Tentative")]
    [InlineData(EventResponse.Declined, "Work", "Declined")]
    public void StateStrips(string r, string cal, string want) =>
        Assert.Equal(want, Cal.StateStrip(F.Card() with { Response = new EventResponse(r) }, cal));

    [Theory]
    [InlineData(EventResponse.Accepted, "Jana accepted")]
    [InlineData(EventResponse.Tentative, "Jana tentatively accepted")]
    [InlineData(EventResponse.Declined, "Jana declined")]
    [InlineData(EventResponse.Delegated, "Jana replied")]
    public void ReplyCards(string r, string want) =>
        Assert.Equal(want, Cal.ReplyCard(F.Card() with
        {
            ReplyFrom = new Address { Name = "Jana", Email = "jana@example.org" },
            Response = new EventResponse(r),
        }));

    [Fact]
    public void AllDayAndBareCards()
    {
        var env = F.Env();
        var allDay = F.Card() with
        {
            AllDay = true,
            StartDate = new CalendarDate("2026-10-15"),
            EndDate = new CalendarDate("2026-10-17"),
            Previous = new InvitationPrevious { Start = new DateOrTime("2026-10-14"), End = new DateOrTime("2026-10-15") },
        };
        Assert.Equal("Thursday 15 October 2026–Friday 16 October 2026 · 2 days", Cal.CardTimeLine(allDay, env));
        Assert.Equal(["Previously Wednesday 14 October 2026 · All Day"], Cal.PreviousLines(allDay, env));
        Assert.Equal(new Cal.DateTile("Thu", "15", "Oct"), Cal.DateTileOf(allDay, env));
        Assert.Empty(Cal.PreviousLines(F.Card() with { Previous = new InvitationPrevious { Start = new DateOrTime("garbage") } }, env));
        // Swift's stand-in for garbage: a date that does not exist.
        Assert.Empty(Cal.PreviousLines(
            F.Card() with { Previous = new InvitationPrevious { Start = new DateOrTime("2026-02-30"), End = new DateOrTime("") } }, env));
        Assert.Null(Cal.DateTileOf(F.Card(), env));
        var unanswerable = F.Card() with { State = InvitationState.Unanswered };
        Assert.Equal("This invitation cannot be answered from here.", Cal.StateLine(unanswerable));
        Assert.False(Cal.ShowsResponses(unanswerable));
    }

    [Theory]
    [InlineData("time", true, false, false, "Previously Tuesday 13 October 2026 · 09:00–10:00 · 1 h")]
    [InlineData("location", false, true, false, "Previously at Room B")]
    [InlineData("recurrence", false, false, true, "Previously repeated: Every week on Tuesday, until 27 October 2026")]
    [InlineData("all three", true, true, true, "Previously Tuesday 13 October 2026 · 09:00–10:00 · 1 h", "Previously at Room B",
        "Previously repeated: Every week on Tuesday, until 27 October 2026")]
    [InlineData("nothing", false, false, false)]
    public void PreviousLines(string name, bool time, bool location, bool recurrence, params string[] want)
    {
        var p = new InvitationPrevious
        {
            Start = time ? new DateOrTime(F.At(2026, 10, 13, 9).ToString("yyyy-MM-dd'T'HH:mm:sszzz", System.Globalization.CultureInfo.InvariantCulture)) : default(DateOrTime?),
            End = time ? new DateOrTime(Rfc3339.Format(F.At(2026, 10, 13, 10))) : default(DateOrTime?),
            Location = location ? (name == "all three" ? " Room\nB " : "Room B") : null,
            Recurrence = recurrence ? Weekly : null,
        };
        Assert.Equal(want, Cal.PreviousLines(CardAt20() with { Previous = p }, F.Env()));
    }

    private static readonly Recurrence Weekly = new()
    {
        Frequency = RecurrenceFrequency.Weekly,
        Interval = 1,
        Until = new CalendarDate("2026-10-27"),
    };

    // A card on Tuesday 20 October 2026 10:00–11:00.
    private static InvitationCard CardAt20() =>
        F.Card() with { Start = F.At(2026, 10, 20, 10).ToUniversalTime(), End = F.At(2026, 10, 20, 11).ToUniversalTime() };

    [Fact]
    public void PreviousLinesEdges()
    {
        var env = F.Env();
        Assert.Empty(Cal.PreviousLines(CardAt20() with { Previous = new InvitationPrevious { Location = "\u200B " } }, env));
        Assert.Empty(Cal.PreviousLines(F.Card(), env));
        // Without a start the rule has no day to describe.
        Assert.Empty(Cal.PreviousLines(F.Card() with { Previous = new InvitationPrevious { Recurrence = Weekly } }, env));
        // The former rule reads from the former start: 13 October was a Tuesday too, 12 October a Monday.
        var moved = new InvitationPrevious
        {
            Start = new DateOrTime(Rfc3339.Format(F.At(2026, 10, 12, 9))),
            End = new DateOrTime(Rfc3339.Format(F.At(2026, 10, 12, 10))),
            Recurrence = Weekly,
        };
        Assert.Equal(
            ["Previously Monday 12 October 2026 · 09:00–10:00 · 1 h", "Previously repeated: Every week on Monday, until 27 October 2026"],
            Cal.PreviousLines(CardAt20() with { Previous = moved }, env));
        var both = new InvitationPrevious
        {
            Start = new DateOrTime(Rfc3339.Format(F.At(2026, 10, 13, 9))),
            End = new DateOrTime(Rfc3339.Format(F.At(2026, 10, 13, 10))),
            Location = "B",
        };
        Assert.Equal(
            "Previously Tuesday 13 October 2026 · 09:00–10:00 · 1 h\nPreviously at B", Cal.PreviousLine(CardAt20() with { Previous = both }, env));
    }

    // An answer given in another client arrives without respondedAt (D29):
    // the strip still shows the answer and the calendar.
    [Fact]
    public void StateStripWithoutRespondedAt()
    {
        var c = F.Card() with
        {
            Method = InvitationMethod.Request,
            Kind = InvitationKind.Invitation,
            State = InvitationState.Answered,
            Response = EventResponse.Tentative,
            RespondedAt = null,
        };
        Assert.True(Cal.ShowsStateStrip(c));
        Assert.Equal("Tentative · in calendar Work", Cal.StateStrip(c, "Work"));
    }

    private static readonly InvitationCard Own = F.Card() with
    {
        Method = InvitationMethod.Request,
        Kind = InvitationKind.Invitation,
        State = InvitationState.Processed,
        OwnEvent = true,
        EventId = new CalendarEventId("ev_1"),
    };

    [Fact]
    public void OwnEvent()
    {
        Assert.Equal("You organise this event.", Cal.StateLine(Own));
        Assert.Equal(Cal.CardMode.Full, Cal.CardModeOf(Own, true));
        Assert.False(Cal.ShowsResponses(Own));
        Assert.True(Cal.ShowsInCalendar(Own));
        Assert.Equal("You organise this event.", Cal.StateLine(Own with { Kind = InvitationKind.Update, State = InvitationState.Outdated }));
        Assert.NotEqual("You organise this event.", Cal.StateLine(Own with { Kind = InvitationKind.Reply }));
    }

    [Theory]
    [InlineData("own, linked", true)]
    [InlineData("own, not stored", false)]
    [InlineData("answered, linked", true)]
    [InlineData("answered, not linked", false)]
    [InlineData("unanswered, linked", false)]
    public void ShowsInCalendar(string name, bool shows)
    {
        var c = name switch
        {
            "own, linked" => Own,
            "own, not stored" => Own with { EventId = null },
            "answered, linked" => F.Card() with { State = InvitationState.Answered, EventId = new CalendarEventId("e") },
            "answered, not linked" => F.Card() with { State = InvitationState.Answered },
            _ => F.Card() with { State = InvitationState.Unanswered, EventId = new CalendarEventId("e") },
        };
        Assert.Equal(shows, Cal.ShowsInCalendar(c));
    }

    [Fact]
    public void Labels()
    {
        IReadOnlyList<string> labels =
        [
            Cal.NoConflict, Cal.AddTo, Cal.ChangeResponse, Cal.ShowInCalendar, Cal.ProposeNewTime, Cal.Propose, Cal.InvalidLine,
            Cal.CancelledLine,
        ];
        Assert.Equal(
            ["No conflicts in your calendars", "Add to:", "Change Response", "Show in Calendar", "Propose New Time…", "Propose",
                "The invitation in this message could not be read.", "This event was cancelled."],
            labels);
        Assert.Equal("Organiser Jana", Cal.CardPeopleLine(F.Card() with { Organizer = new Address { Name = "Jana", Email = "j@x.org" } }));
        Assert.Equal("2 attendees", Cal.CardPeopleLine(F.Card() with { AttendeeCount = 2 }));
        Assert.Equal("", Cal.CardPeopleLine(F.Card()));
        Assert.Equal("Untitled Event", Cal.CardTitle(F.Card()));
        Assert.True(F.InvitationStates.Select(s => s.Value).Distinct().Count() == 6);
    }
}
