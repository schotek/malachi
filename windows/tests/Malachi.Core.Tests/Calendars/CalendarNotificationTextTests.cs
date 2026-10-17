// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The rules of macos/Sources/MalachiCore/Calendar/CalendarNotificationText.swift
// (no Swift suite of its own); GTK: ui/internal/calendar/notifications_test.go
// (TestAlertNotification, TestInvitationNotifications), with the rounding
// of the minutes at the half minute (Go's Duration.Round).

using System;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarNotificationTextTests
{
    // Wednesday 14 October 2026, 9:50 in Prague.
    private static readonly DateTimeOffset Now = F.At(2026, 10, 14, 9, 50);

    private static EventAlert Alert(string title = "", DateTimeOffset? start = null, bool allDay = false, string? startDate = null, string? location = null) =>
        new()
        {
            AlertId = new CalendarAlertId(""),
            State = EventAlertState.Due,
            EventId = new CalendarEventId(""),
            AccountId = new AccountId(""),
            CalendarId = new CalendarId(""),
            Title = title,
            Start = start?.ToUniversalTime(),
            AllDay = allDay,
            StartDate = startDate is null ? default(CalendarDate?) : new CalendarDate(startDate),
            Location = location,
            MinutesBefore = 0,
        };

    [Theory]
    [InlineData("Standup", 600, null, "Room B", "Standup", "in 10 minutes · Room B")]
    [InlineData("Standup", 60, null, null, "Standup", "in 1 minute")]
    [InlineData("Standup", 20, null, null, "Standup", "now")]
    [InlineData("Standup", -300, null, null, "Standup", "now")]
    [InlineData("Lunch", 7800, null, null, "Lunch", "at 12:00")]
    [InlineData("Trip", 86400, null, null, "Trip", "Thursday 15 October · 09:50")]
    [InlineData("Offsite", null, "2026-10-14", null, "Offsite", "today")]
    [InlineData("Offsite", null, "2026-10-15", "Hotel", "Offsite", "Thursday 15 October · Hotel")]
    [InlineData("", null, null, null, "Untitled Event", "")]
    // Go's Duration.Round: a half minute rounds away from zero.
    [InlineData("Standup", 30, null, null, "Standup", "in 1 minute")]
    [InlineData("Standup", 29, null, null, "Standup", "now")]
    [InlineData("Standup", 3569, null, null, "Standup", "in 59 minutes")]
    [InlineData("Standup", 3570, null, null, "Standup", "at 10:49")]
    // Windows-only: a bad date of an all-day alert says nothing.
    [InlineData("Offsite", null, "2026-13-01", null, "Offsite", "")]
    public void AlertNotification(string title, int? seconds, string? startDate, string? location, string wantTitle, string wantBody)
    {
        var a = Alert(title, seconds is { } s ? Now.AddSeconds(s) : null, startDate is not null, startDate, location);
        Assert.Equal(new Cal.Notification(wantTitle, wantBody), Cal.AlertNotification(a, Now, F.Env()));
    }

    [Fact]
    public void InvitationNotifications()
    {
        var env = F.Env();
        var s = F.At(2026, 10, 14, 10).ToUniversalTime();
        var jana = new Address { Name = "Jana Nováková", Email = "jana@example.org" };
        var sum = new InvitationSummary
        {
            Method = new InvitationMethod(""),
            Kind = InvitationKind.Invitation,
            State = new InvitationState(""),
            Title = "Q4 planning",
            Start = s,
            End = s.AddHours(1),
            Organizer = jana,
        };
        Assert.Equal(
            new Cal.Notification("New invitation from Jana Nováková", "Q4 planning · Wednesday 14 October 2026 · 10:00–11:00 · 1 h"),
            Cal.InvitationNotification(sum, env));
        sum = sum with { Kind = InvitationKind.Update };
        Assert.Equal("Jana Nováková updated the event", Cal.InvitationNotification(sum, env).Title);
        sum = sum with { Organizer = null };
        Assert.Equal("An event was updated", Cal.InvitationNotification(sum, env).Title);
        sum = sum with { Kind = InvitationKind.Invitation };
        Assert.Equal("New invitation", Cal.InvitationNotification(sum, env).Title);
        var allDay = new InvitationSummary
        {
            Method = new InvitationMethod(""),
            Kind = new InvitationKind(""),
            State = new InvitationState(""),
            Title = "Offsite",
            AllDay = true,
            StartDate = new CalendarDate("2026-10-15"),
            Organizer = jana,
        };
        Assert.Equal(
            new Cal.Notification("Jana Nováková cancelled the event", "Offsite · Thursday 15 October 2026 · All Day"),
            Cal.CancellationNotification(allDay, env));
        var bare = new InvitationSummary
        {
            Method = new InvitationMethod(""),
            Kind = new InvitationKind(""),
            State = new InvitationState(""),
            Title = "",
        };
        Assert.Equal(new Cal.Notification("An event was cancelled", "Untitled Event"), Cal.CancellationNotification(bare, env));
    }

    // Windows-only: names and titles from the part are display text.
    [Fact]
    public void NotificationsCleanTheirStrings()
    {
        var sum = new InvitationSummary
        {
            Method = new InvitationMethod(""),
            Kind = InvitationKind.Invitation,
            State = new InvitationState(""),
            Title = "Pay\u202Etxt.exe",
            Organizer = new Address { Name = "\u0000\u200B", Email = "boss@example.org" },
        };
        Assert.Equal(new Cal.Notification("New invitation from boss@example.org", "Paytxt.exe"), Cal.InvitationNotification(sum, F.Env()));
        Assert.Equal("Stand up", Cal.AlertNotification(Alert("Stand\nup"), Now, F.Env()).Title);
    }

    [Fact]
    public void Actions()
    {
        Assert.Equal("Join", Cal.Join);
        Assert.Equal("Snooze", Cal.Snooze);
        Assert.Equal("Show", Cal.Show);
        Assert.Equal("Accept", Cal.ResponseAction(EventResponse.Accepted));
        Assert.Equal("Remove from Calendar", Cal.RemoveFromCalendar);
        Assert.Equal(5, Cal.SnoozeMinutes);
    }
}
