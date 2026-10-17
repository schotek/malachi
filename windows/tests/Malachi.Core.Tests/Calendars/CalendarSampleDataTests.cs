// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarSampleDataTests.swift
// (sampleData); GTK: ui/internal/calendar/samples_test.go (TestSampleData).
// Windows-only beside it: OccurrencePart copies every member, and the
// snapshot of the samples (CalendarSamples.cs).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarSampleDataTests
{
    public static TheoryData<string> Moments => ["2026-10-14T12:00", "2026-10-18T23:00", "2026-03-30T08:00"];

    [Theory]
    [MemberData(nameof(Moments))]
    public void SampleData(string moment)
    {
        var p = F.Prague;
        var now = F.Local(moment);
        var s = Cal.SampleData(now, p);
        Assert.Equal(3, s.Accounts.Count);
        Assert.Equal(3, s.Calendars.Accounts.Count);
        Assert.Equal(3, s.Invitations.Count);
        Assert.Equal(3, s.Calendars.Unanswered);
        Assert.Equal(Cal.InvitationStates.Count, s.Cards.Count);
        var week = Cal.ViewRange(Cal.View.Week, now, Cal.Weekday.Monday, p);
        var states = new Dictionary<Cal.BlockState, int>();
        int video = 0, recurring = 0, twoDay = 0, invitations = 0;
        foreach (var o in s.Occurrences)
        {
            Assert.True(Cal.Overlaps(o, week.From, week.To, p), $"{o.Title} is outside the week");
            var st = Cal.StateOf(o);
            states[st] = states.GetValueOrDefault(st) + 1;
            video += o.VideoCall == true ? 1 : 0;
            recurring += o.Recurring == true ? 1 : 0;
            invitations += o.Invitation is not null ? 1 : 0;
            if (o.AllDay && Cal.Span(o, p) is { } span && Cal.DaysBetween(span.Start, span.End, p) == 2)
            {
                twoDay++;
            }
            if (o.CalendarId is { } c)
            {
                Assert.True(s.CalendarName(c).Length > 0, $"{o.Title} names no sample calendar");
            }
        }
        Assert.True(states.GetValueOrDefault(Cal.BlockState.Tentative) > 0);
        Assert.True(states.GetValueOrDefault(Cal.BlockState.Unanswered) >= 2);
        Assert.Equal(1, states.GetValueOrDefault(Cal.BlockState.Declined));
        Assert.Equal(1, video);
        Assert.Equal(5, recurring);
        Assert.Equal(1, twoDay);
        Assert.Equal(1, invitations);
        var tue = Cal.LayoutDay(s.Occurrences, Cal.AddDays(week.From, 1, p), p, showDeclined: false);
        Assert.True(tue.Count(b => b.Columns > 1) >= 3, "overlapping blocks on Tuesday");
        for (var i = 0; i < s.Cards.Count; i++)
        {
            var card = s.Cards[i];
            Assert.Equal(Cal.InvitationStates[i], card.State);
            Assert.NotNull(card.Conflicts);
            Assert.NotNull(card.Targets);
            Assert.NotNull(card.Day.Busy);
        }
        Assert.Equal("Nextcloud", s.SampleAccountName(Cal.SampleNextcloud));
        Assert.Equal("", s.SampleAccountName(new AccountId("x")));
        Assert.Equal("", s.CalendarName(new CalendarId("x")));
        foreach (var a in s.Accounts)
        {
            Assert.True(a.Can(Capability.Calendar), a.Config.Name);
        }
    }

    [Fact]
    public void TimesAreTheWeeksInTheZone()
    {
        var s = Cal.SampleData(F.At(2026, 10, 14, 12), F.Prague);
        // Monday 12 October: the standup at 09:00 Prague, stored in UTC.
        var standup = s.Events[0];
        Assert.Equal(F.At(2026, 10, 12, 9, 0), standup.Start);
        Assert.Equal(TimeSpan.Zero, standup.Start?.Offset);
        Assert.Equal(new DateOrTime("2026-10-12T07:00:00Z"), standup.RecurrenceId);
        Assert.Equal(new TimeZoneName("Europe/Prague"), standup.TimeZone);
        var offsite = s.Events.Single(e => e.EventId == new CalendarEventId("ev_sample_offsite"));
        Assert.Equal(new CalendarDate("2026-10-15"), offsite.StartDate);
        Assert.Equal(new CalendarDate("2026-10-17"), offsite.EndDate);
        // Synced two minutes before now.
        Assert.Equal(F.At(2026, 10, 14, 11, 58), s.Calendars.Accounts[0].State.LastSync);
        Assert.Equal(new CalendarDate("2025-10-12"), s.Calendars.Accounts[0].Window.From);
        Assert.Equal(Cal.DefaultPreferences, s.Preferences);
    }

    [Fact]
    public void OccurrencePartCopiesEveryMember()
    {
        var s = Cal.SampleData(F.At(2026, 10, 14, 12), F.Prague);
        foreach (var e in s.Events)
        {
            var o = Cal.OccurrencePart(e);
            Assert.Equal(typeof(Occurrence), o.GetType());
            foreach (var property in typeof(Occurrence).GetProperties().Where(p => p.DeclaringType == typeof(Occurrence)))
            {
                Assert.Equal(property.GetValue(e), property.GetValue(o));
            }
        }
        Assert.Equal(s.Events.Count + 1, s.Occurrences.Count);
        Assert.All(s.Occurrences, o => Assert.Equal(typeof(Occurrence), o.GetType()));
    }

    [Fact]
    public void SampleSnapshot()
    {
        var d = Cal.SampleData(F.At(2026, 10, 14, 12), F.Prague);
        var s = Cal.SampleSnapshot(d);
        Assert.Equal(Cal.Phase.Ready, s.Phase);
        Assert.Same(d.Accounts, s.Accounts);
        Assert.Same(d.Calendars.Accounts, s.CalendarAccounts);
        Assert.Equal(3, s.Unanswered);
        Assert.Same(d.Invitations, s.Invitations);
        Assert.Equal(d.Preferences, s.Preferences);
        // The lists compare by content.
        Assert.Equal(d, d with { Accounts = [.. d.Accounts], Cards = [.. d.Cards] });
        Assert.NotEqual(d, d with { Cards = [.. d.Cards.Skip(1)] });
    }
}
