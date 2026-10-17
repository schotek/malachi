// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarDetailTests.swift; GTK:
// ui/internal/calendar/detail_test.go (TestTimeLine, TestDurationText,
// TestResponseLabels, TestDetailRules, TestDetailOf). TestDetailOf reads
// the planning and standup events of CalFixture.Events.cs, the samples'
// own values, not Cal's sample data.

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarDetailTests
{
    [Theory]
    [InlineData("timed", "2026-10-13T10:00", "2026-10-13T11:30", 0, "Tuesday 13 October 2026 · 10:00–11:30 · 1 h 30 min")]
    [InlineData("timed", "2026-10-13T22:00", "2026-10-14T00:00", 0, "Tuesday 13 October 2026 · 22:00–00:00 · 2 h")]
    [InlineData("timed", "2026-10-13T10:00", "2026-10-15T12:00", 0, "Tue 13 Oct 2026 10:00–Thu 15 Oct 2026 12:00 · 50 h")]
    [InlineData("allDay", "2026-10-13", null, 1, "Tuesday 13 October 2026 · All Day")]
    [InlineData("allDay", "2026-10-13", null, 2, "Tuesday 13 October 2026–Wednesday 14 October 2026 · 2 days")]
    // The autumn change: 3 real hours, 2 on the clock.
    [InlineData("timed", "2026-10-25T01:00", "2026-10-25T03:00", 0, "Sunday 25 October 2026 · 01:00–03:00 · 3 h")]
    [InlineData("none", null, null, 0, "")]
    public void TimeLine(string kind, string? a, string? b, int days, string want)
    {
        var o = kind switch
        {
            "timed" => F.Timed("a", F.Local(a!), F.Local(b!)),
            "allDay" => F.AllDayOcc("a", F.Local(a!), days),
            _ => F.Occ(),
        };
        Assert.Equal(want, Cal.TimeLine(o, F.Env()));
    }

    [Theory]
    [InlineData(0, "0 min")]
    [InlineData(30, "0 min")]
    [InlineData(60, "1 min")]
    [InlineData(45 * 60, "45 min")]
    [InlineData(3600, "1 h")]
    [InlineData(90 * 60, "1 h 30 min")]
    [InlineData(24 * 3600, "1 day")]
    [InlineData(25 * 3600, "25 h")]
    [InlineData(48 * 3600, "2 days")]
    [InlineData(-3600, "0 min")]
    public void DurationText(int seconds, string want) => Assert.Equal(want, Cal.DurationText(TimeSpan.FromSeconds(seconds)));

    [Theory]
    [InlineData(EventResponse.Accepted, "Accepted")]
    [InlineData(EventResponse.Tentative, "Tentative")]
    [InlineData(EventResponse.Declined, "Declined")]
    [InlineData(EventResponse.NeedsAction, "No Reply")]
    [InlineData(EventResponse.Delegated, "Delegated")]
    [InlineData("x", "No Reply")]
    [InlineData(null, "No Reply")]
    public void ResponseLabels(string? r, string want) =>
        Assert.Equal(want, Cal.ResponseLabel(r is null ? default(EventResponse?) : new EventResponse(r)));

    [Fact]
    public void ResponseTexts()
    {
        Assert.Equal("You: Accepted", Cal.MyResponseText(EventResponse.Accepted));
        Assert.Equal("Accept", Cal.ResponseAction(EventResponse.Accepted));
        Assert.Equal("Decline", Cal.ResponseAction(EventResponse.Declined));
        Assert.Equal("Tentative", Cal.ResponseAction(EventResponse.Tentative));
        Assert.Equal(["accepted", "tentative", "declined"], Cal.ResponseChoices.Select(r => r.Value));
    }

    private static readonly CalendarEvent Attendee = F.Event() with
    {
        Title = "Planning",
        MyResponse = EventResponse.Accepted,
        AttendeeCount = 4,
        CanRespond = true,
        CanDelete = true,
        OrganizerAddress = new Address { Name = "Jana Nováková", Email = "jana@example.org" },
    };

    private static CalendarEvent RuleCase(string name) => name switch
    {
        "attendee" => Attendee,
        "declined attendee" => Attendee with { MyResponse = EventResponse.Declined },
        "cancelled" => Attendee with { Status = EventStatus.Cancelled },
        "daemon says no" => Attendee with { CanRespond = false },
        "organiser with attendees" => F.Event() with
        {
            Title = "Mine",
            Organizer = true,
            AttendeeCount = 1,
            RemoveIsDelete = true,
            CanRespond = true,
            InvitationMessages = [new MessageRef { AccountId = new AccountId(""), MessageId = new MessageId("") }],
        },
        "own event" => F.Event() with { Title = "Dentist", RemoveIsDelete = true },
        "no organiser address" => F.Event() with { AttendeeCount = 2 },
        _ => throw new ArgumentOutOfRangeException(nameof(name)),
    };

    [Theory]
    [InlineData("attendee", true, "Remove from Calendar", true, "Also tell the organiser you won’t attend?", "Organiser Jana Nováková · 4 attendees", false)]
    [InlineData("declined attendee", true, "Remove from Calendar", false, "", "Organiser Jana Nováková · 4 attendees", false)]
    [InlineData("cancelled", false, "Remove from Calendar", false, "", "Organiser Jana Nováková · 4 attendees", false)]
    [InlineData("daemon says no", false, "Remove from Calendar", false, "", "Organiser Jana Nováková · 4 attendees", false)]
    [InlineData("organiser with attendees", false, "Delete", true, "Delete the Event?", "You organise · 1 attendee", true)]
    [InlineData("own event", false, "Delete", false, "", "", false)]
    [InlineData("no organiser address", false, "Remove from Calendar", false, "", "2 attendees", false)]
    public void DetailRules(string name, bool respond, string remove, bool ask, string heading, string people, bool invitation)
    {
        var e = RuleCase(name);
        Assert.Equal(respond, Cal.CanRespond(e));
        Assert.Equal(remove, Cal.RemoveTitle(e));
        var q = Cal.RemoveQuestion(e);
        Assert.Equal(ask, q is not null);
        Assert.Equal(heading, q?.Heading ?? "");
        Assert.Equal(people, Cal.PeopleLine(e));
        Assert.Equal(invitation, Cal.ShowsInvitation(e));
    }

    [Fact]
    public void Questions()
    {
        var q = Cal.RemoveQuestion(Attendee);
        Assert.NotNull(q);
        Assert.Equal("Remove and Decline", q.Confirm);
        Assert.Equal("Remove Only", q.Alternative);
        Assert.Equal("“Planning” will be removed from your calendar.", q.Body);
        var mine = Cal.RemoveQuestion(RuleCase("organiser with attendees"));
        Assert.Equal("“Mine” will be cancelled for its 1 attendee.", mine?.Body);
        Assert.Equal("Delete", mine?.Confirm);
        var d = Cal.DeleteQuestion(F.Event());
        Assert.Equal("Remove “Untitled Event”?", d.Heading);
        Assert.Equal("Remove from Calendar", d.Confirm);
    }

    [Fact]
    public void DetailOf()
    {
        var env = F.Env();
        var planning = F.Planning();
        planning = planning with { Attendees = [.. planning.Attendees!, planning.Attendees![3], planning.Attendees[3], planning.Attendees[3]] };
        var d = Cal.DetailOf(planning, "Work", "Calendar", env);
        Assert.Equal("Work · Calendar", d.CalendarLine);
        Assert.Equal("Q4 planning", d.Title);
        Assert.Equal("Tuesday 13 October 2026 · 10:00–11:30 · 1 h 30 min", d.TimeLine);
        Assert.Equal("teams.microsoft.com", d.VideoHost);
        Assert.Equal("https://teams.microsoft.com/l/meetup-join/sample", d.VideoCall);
        Assert.Equal("Organiser Jana Nováková · 7 attendees", d.PeopleLine);
        Assert.True(d.Collapsed && d.CanRespond && !d.CanEdit && d.CanRemove);
        Assert.Equal("Remove from Calendar", d.RemoveTitle);
        Assert.True(d.ShowInvitation);
        Assert.Equal("Accepted", d.Attendees[0].ResponseText);
        Assert.True(d.Attendees[0].Organizer && d.Attendees[1].Me && d.Attendees[3].Optional);
        Assert.Equal("", d.Recurrence);
        Assert.Equal("Room A", d.Location);
        Assert.Equal(EventResponse.Accepted, d.MyResponse);
        Assert.Equal("Agenda: budget, hiring, the roadmap.\nSlides: https://example.org/q4-slides", d.Notes);
        Assert.Equal(["https://example.org/q4-slides"], d.Links);
        var standup = Cal.DetailOf(F.Standup(), "Work", "", env);
        Assert.Equal("Every weekday", standup.Recurrence);
        Assert.Equal("Work", standup.CalendarLine);
        Assert.Equal("", standup.PeopleLine);
        Assert.False(standup.Collapsed);
        Assert.Equal("Delete", standup.RemoveTitle);
    }

    // Windows-only: the event's strings are hostile display text; the
    // detail shows them cleaned, the title never empty.
    [Fact]
    public void DetailCleansTheEventsStrings()
    {
        var e = F.Planning() with
        {
            Title = "\u202Eevil\u0000 title",
            Location = "Room\nB\u200B",
            Notes = "line\u202E one\r\n\r\n\r\nline two",
            OrganizerAddress = new Address { Name = "\u2067Jana\u2069", Email = "jana@example.org" },
            VideoCallLink = new VideoCall { Url = "https://x.example/", Provider = VideoProvider.Other, Host = "x\u0007.example" },
        };
        var d = Cal.DetailOf(e, "Wo\u202Erk", "Cal\nendar", F.Env());
        Assert.Equal("evil title", d.Title);
        Assert.Equal("Room B", d.Location);
        Assert.Equal("line one\n\nline two", d.Notes);
        Assert.Equal("Organiser Jana · 4 attendees", d.PeopleLine);
        Assert.Equal("x.example", d.VideoHost);
        Assert.Equal("Work · Cal endar", d.CalendarLine);
        Assert.Equal("Untitled Event", Cal.DetailOf(F.Event() with { Title = "\u200B " }, "", "", F.Env()).Title);
    }

    [Fact]
    public void Labels()
    {
        Assert.Equal("organiser", Cal.OrganizerLabel);
        Assert.Equal("optional", Cal.OptionalLabel);
        Assert.Equal("Show All 7 Attendees", Cal.ShowAllAttendees(7));
        Assert.Equal("Show Invitation in Mail", Cal.ShowInvitation);
        Assert.Equal("Decline Without Reply", Cal.DeclineWithoutReply);
        Assert.Equal("3 days", Cal.DaysText(3));
        Assert.Equal(6, Cal.MaxAttendeesShown);
    }
}
