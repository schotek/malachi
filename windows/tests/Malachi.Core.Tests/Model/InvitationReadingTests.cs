// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/InvitationReadingTests.swift
// (InvitationReadingTests, InvitationAnsweredElsewhereTests,
// InvitationOwnEventTests); Swift-first, no GTK test yet. What Mail shows of
// a message with a calendar part: the row's pill, whether a card shows,
// the clock line with the repetition and the mini day strip's geometry,
// also for hostile spans. The cards are CalFixture's vendor demo (the
// samples' own values); Swift's two InvitationController cases check here
// what that controller reads of Cal (the strip, the state line, Show in
// Calendar), the controller's port being separate.

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Model;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Model;

public sealed class InvitationReadingTests
{
    // Swift's calNow: Wednesday 14 October 2026, 10:17 in Prague.
    private static readonly DateTimeOffset Now = F.At(2026, 10, 14, 10, 17);

    private static InvitationCard CardOf(string state) => F.DemoCard(state, Now);

    private static MessageSummary Summary(InvitationSummary? invitation = null) => new()
    {
        Id = "m1",
        AccountId = "a",
        FolderId = "f",
        Subject = "Vendor demo",
        Date = DateTimeOffset.MinValue,
        Snippet = "",
        HasAttachments = false,
        Size = 0,
        Invitation = invitation,
    };

    private static InvitationSummary Inv(string kind, string state) =>
        new() { Method = InvitationMethod.Request, Kind = kind, State = state, Title = "" };

    [Theory]
    [InlineData(InvitationKind.Invitation, Cal.InvitationPill.Invitation)]
    [InlineData(InvitationKind.Update, Cal.InvitationPill.Update)]
    [InlineData(InvitationKind.Cancelled, Cal.InvitationPill.Cancelled)]
    [InlineData(InvitationKind.Reply, Cal.InvitationPill.Reply)]
    [InlineData(InvitationKind.Other, Cal.InvitationPill.None)]
    public void PillOfARow(string kind, Cal.InvitationPill want)
    {
        Assert.Equal(Cal.InvitationPill.None, InvitationReading.Pill(Summary()));
        Assert.Equal(want, InvitationReading.Pill(Summary(Inv(kind, InvitationState.Unanswered))));
        Assert.Equal(Cal.InvitationPill.None, InvitationReading.Pill(Summary(Inv(kind, InvitationState.Invalid))));
    }

    // Windows-only overload: a row's pill is its message's, on a
    // conversation row the newest folder member's.
    [Fact]
    public void PillOfAConversationRow()
    {
        var row = new ListRow
        {
            Key = new ListKey(Thread: new ThreadId("t1")),
            Thread = true,
            Message = Summary(Inv(InvitationKind.Cancelled, InvitationState.Cancelled)),
        };
        Assert.Equal(Cal.InvitationPill.Cancelled, InvitationReading.Pill(row));
        Assert.Equal(Cal.InvitationPill.None, InvitationReading.Pill(row with { Message = Summary() }));
    }

    [Fact]
    public void CardOnlyFromMessageGet()
    {
        var c = CardOf(InvitationState.Unanswered);
        static Message With(InvitationCard? card) => new() { Summary = Summary(), Invitation = card };
        Assert.Null(InvitationReading.Read(null, true));
        Assert.Null(InvitationReading.Read(With(null), true));
        Assert.Same(c, InvitationReading.Read(With(c), true));
        Assert.Null(InvitationReading.Read(With(c), false)); // the account's switch
        Assert.Null(InvitationReading.Read(With(CardOf(InvitationState.Invalid)), true));
        Assert.NotNull(InvitationReading.Read(With(CardOf(InvitationState.Processed)), true)); // a reply
        Assert.NotNull(InvitationReading.Read(With(CardOf(InvitationState.Cancelled)), true));
        Assert.Null(InvitationReading.Read(With(c with { Kind = InvitationKind.Other }), true));
    }

    [Fact]
    public void AccountSwitch()
    {
        Assert.True(InvitationReading.ShowsInvitationCard(null));
        Account Account(CalendarConfig? calendar) => new()
        {
            Id = new AccountId("a"),
            Config = new AccountConfig { Name = "Gmail", Email = "me@example.com", Calendar = calendar },
            Enabled = true,
            State = new SyncState { AccountId = new AccountId("a"), Status = SyncStatus.Idle },
        };
        Assert.True(InvitationReading.ShowsInvitationCard(Account(null)));
        Assert.True(InvitationReading.ShowsInvitationCard(Account(new CalendarConfig { Sync = true })));
        Assert.True(InvitationReading.ShowsInvitationCard(Account(new CalendarConfig { Sync = false, InvitationCard = true })));
        Assert.False(InvitationReading.ShowsInvitationCard(Account(new CalendarConfig { Sync = true, InvitationCard = false })));
    }

    [Fact]
    public void ClockLineWithTheRepetition()
    {
        var env = F.Env();
        var c = CardOf(InvitationState.Unanswered);
        var plain = Cal.CardTimeLine(c, env);
        Assert.Equal("Friday 16 October 2026 · 15:00–16:00 · 1 h", plain);
        Assert.Equal(plain, Cal.CardClockLine(c, env));
        var line = Cal.CardClockLine(c with { Recurrence = new Recurrence { Frequency = RecurrenceFrequency.Weekly, Interval = 2 } }, env);
        Assert.Equal(plain + " · Every 2 weeks on Friday", line);
        // Without a start there is no day to describe the rule from.
        Assert.Equal("", Cal.CardClockLine(F.Card() with { Recurrence = new Recurrence { Frequency = RecurrenceFrequency.Daily, Interval = 1 } }, env));
    }

    [Fact]
    public void DayStripOfTheSample()
    {
        var s = Cal.DayStripOf(CardOf(InvitationState.Unanswered), F.Env());
        Assert.NotNull(s);
        // 8:00–17:00: ten whole hours, both ends.
        Assert.Equal(10, s.Ticks.Count);
        Assert.Equal(new Cal.DayStrip.Tick(0, "08:00"), s.Ticks[0]);
        Assert.Equal(new Cal.DayStrip.Tick(1, "17:00"), s.Ticks[^1]);
        Assert.Equal(2, s.Busy.Count);
        Assert.Equal(1.0 / 9, s.Busy[0].From, 9);
        Assert.Equal(1.25 / 9, s.Busy[0].To, 9);
        Assert.NotNull(s.Proposed);
        Assert.Equal(7.0 / 9, s.Proposed.From, 9);
        Assert.Equal(8.0 / 9, s.Proposed.To, 9);
        Assert.Equal("15:00–16:00", s.ProposedLabel);
        Assert.Equal(s, Cal.DayStripOf(CardOf(InvitationState.Unanswered), F.Env()));
    }

    [Fact]
    public void DayStripOfHostileSpans()
    {
        var env = F.Env();
        var c = CardOf(InvitationState.Unanswered);
        Assert.Null(Cal.DayStripOf(CardOf(InvitationState.Invalid), env)); // the zero span of an unreadable part
        Assert.Null(Cal.DayStripOf(c with { Day = c.Day with { From = c.Day.To, To = c.Day.From } }, env)); // backwards
        Assert.Null(Cal.DayStripOf(c with { Day = new InvitationDay { From = Now, To = Now.AddDays(30) } }, env)); // not a day

        // Busy spans outside, backwards or many: clipped, dropped, capped.
        var from = Now;
        var to = Now.AddHours(10);
        DayBusy[] busy =
        [
            new() { Start = from.AddHours(-2), End = from.AddMinutes(30) },
            new() { Start = to.AddHours(1), End = to.AddHours(2) },
            new() { Start = from.AddHours(1), End = from.AddMinutes(30) },
            .. Enumerable.Range(0, 500).Select(i => new DayBusy { Start = from.AddMinutes(i), End = from.AddMinutes(i).AddSeconds(30) }),
        ];
        var hostile = c with
        {
            Day = new InvitationDay { From = from, To = to, Busy = busy },
            Start = from.AddHours(-1),
            End = from.AddMinutes(-1),
        };
        var s = Cal.DayStripOf(hostile, env);
        Assert.NotNull(s);
        Assert.True(s.Busy.Count <= Cal.DayStrip.MaxBusy);
        Assert.All(s.Busy, b => Assert.True(b.From >= 0 && b.To <= 1 && b.From < b.To));
        Assert.Equal(new Cal.DayStrip.Span(0, 0.05), s.Busy[0]); // clipped at the start
        Assert.Null(s.Proposed); // a slot off the axis
        Assert.Equal("", s.ProposedLabel);
        Assert.True(s.Ticks.Count <= Cal.DayStrip.MaxTicks);

        // A long axis labels fewer hours.
        var longer = Cal.DayStripOf(c with { Day = new InvitationDay { From = from, To = from.AddHours(40) } }, env);
        Assert.NotNull(longer);
        Assert.True(longer.Ticks.Count <= Cal.DayStrip.MaxTicks);
        // An all-day invitation has no slot.
        Assert.Null(Cal.DayStripOf(c with { AllDay = true }, env)!.Proposed);
    }

    // Windows-only: the ticks are the zone's whole hours, also in a zone
    // half an hour off UTC, and a day of the autumn change has 25 hours.
    [Fact]
    public void DayStripTicksAreTheZonesHours()
    {
        var india = TimeZoneInfo.FindSystemTimeZoneById("Asia/Kolkata");
        var from = new DateTimeOffset(2026, 10, 16, 2, 0, 0, TimeSpan.Zero); // 7:30 in Kolkata
        var c = CardOf(InvitationState.Unanswered) with { Day = new InvitationDay { From = from, To = from.AddHours(3) } };
        var s = Cal.DayStripOf(c, F.Env(india));
        Assert.NotNull(s);
        Assert.Equal(["08:00", "09:00", "10:00"], s.Ticks.Select(t => t.Label));
        Assert.Equal(0.5 / 3, s.Ticks[0].At, 9);
        var autumn = c with { Day = new InvitationDay { From = F.At(2026, 10, 25, 0), To = F.At(2026, 10, 26, 0) } };
        var a = Cal.DayStripOf(autumn, F.Env());
        Assert.NotNull(a);
        // 26 whole hours from midnight to midnight (both ends), every second labelled.
        Assert.Equal(13, a.Ticks.Count);
        Assert.Equal(["00:00", "02:00", "03:00"], a.Ticks.Take(3).Select(t => t.Label));
        Assert.Equal(new Cal.DayStrip.Tick(24.0 / 25, "23:00"), a.Ticks[^1]);
    }

    // An answer given in another client (Outlook, a phone; D29): the daemon
    // sends the response and the linked event without respondedAt.
    [Fact]
    public void AnsweredElsewhereKeepsItsPillAndShowsTheStrip()
    {
        Assert.Equal(Cal.InvitationPill.Invitation, InvitationReading.Pill(Summary(Inv(InvitationKind.Invitation, InvitationState.Answered))));
        var c = CardOf(InvitationState.Unanswered) with
        {
            State = InvitationState.Answered,
            Response = EventResponse.Accepted,
            RespondedAt = null,
            EventId = new CalendarEventId("ev_sample_demo"),
            CalendarId = new CalendarId("cal_sample_personal"),
        };
        Assert.Equal(Cal.CardMode.Full, Cal.CardModeOf(c, true));
        Assert.True(Cal.ShowsStateStrip(c));
        Assert.False(Cal.ShowsResponses(c)); // the strip, no Add to: row
        Assert.StartsWith("Accepted · in calendar ", Cal.StateStrip(c, F.SampleCalendarName(c.CalendarId!.Value)), StringComparison.Ordinal);
    }

    // The user's own request (ownEvent, D32): no answer, the state line with
    // Show in Calendar when the event is stored.
    [Fact]
    public void OwnEventCard()
    {
        var c = CardOf(InvitationState.Unanswered) with
        {
            Kind = InvitationKind.Update,
            State = InvitationState.Processed,
            OwnEvent = true,
            CanRespond = false,
            Targets = [],
            EventId = new CalendarEventId("ev_sample_demo"),
        };
        Assert.Equal(Cal.CardMode.Full, Cal.CardModeOf(c, true));
        Assert.Equal(Cal.InvitationPill.Update, Cal.PillKind(c)); // the pill as the daemon's kind says
        Assert.False(Cal.ShowsResponses(c) || Cal.ShowsStateStrip(c) || Cal.ShowsProposeNewTime(c));
        Assert.Equal("You organise this event.", Cal.StateLine(c));
        Assert.True(Cal.ShowsInCalendar(c));
        Assert.False(Cal.ShowsInCalendar(c with { EventId = null })); // not stored: no Show in Calendar
    }
}
