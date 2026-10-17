// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarTodayTests.swift (todayRows;
// its alertNotification and invitationNotifications are the notification
// texts' tests); GTK: ui/internal/calendar/today_test.go (TestTodayRows).
//
// Go's and Swift's case reads the rows off the sample data (SampleData,
// CalendarSampleData.cs); the other cases are Windows' own: the cleaning
// of a row and the strip's accounts without the samples.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;
using static Malachi.Core.Calendars.Cal;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarTodayTests
{
    [Fact]
    public void TodayRowsOfTheSamples()
    {
        var env = F.Env();
        // Tuesday 13 October 2026 at noon in the samples' week.
        var now = F.At(2026, 10, 13, 12);
        var s = SampleData(now, F.Prague);
        var got = TodayRows(s.Occurrences, now, env, s.Preferences.ShowDeclined)
            .Select(r => $"{r.Time} {r.Title} [{r.Place}] past={r.TookPlace} respond={r.Respond}");
        Assert.Equal(
            [
                "09:00–09:15 Standup [Room B] past=True respond=False",
                "10:00–11:30 Q4 planning [Room A] past=True respond=False",
                "10:30–11:00 Call with the supplier [] past=True respond=False",
                "11:00–12:00 Design review [] past=True respond=False",
            ],
            got);
        var wed = F.At(2026, 10, 14, 14, 30);
        var rows = TodayRows(s.Occurrences, wed, env, false);
        Assert.Equal(2, rows.Count);
        Assert.True(rows[1].Title == "Budget review" && rows[1].Respond && !rows[1].TookPlace && rows[0].TookPlace, string.Join("; ", rows));
        var withDeclined = TodayRows(s.Occurrences, wed, env, true);
        Assert.Equal(3, withDeclined.Count);
        Assert.Equal(BlockState.Declined, withDeclined[2].State);
        var thu = TodayRows(s.Occurrences, F.At(2026, 10, 15, 8), env, false);
        Assert.NotEmpty(thu);
        Assert.True(thu[0].Time == "All Day" && thu[0].Title == "Team offsite" && !thu[0].TookPlace, thu[0].ToString());
        Assert.Empty(TodayRows([], now, env, false));
        Assert.True(ShowsTodayStrip(s.Accounts));
    }

    private static Occurrence Timed(string title, int h1, int h2, string? location = null) =>
        F.Timed("x", F.At(2026, 10, 13, h1), F.At(2026, 10, 13, h2)) with { Title = title, Location = location };

    [Fact]
    public void ARowCleansWhatTheServerSent()
    {
        var env = F.Env();
        var now = F.At(2026, 10, 13, 8);
        var o = Timed("", 9, 10, "Room\u202E 7");
        var row = Assert.Single(TodayRows([o], now, env, false));
        Assert.Equal("Untitled Event", row.Title);
        Assert.DoesNotContain("\u202E", row.Place, StringComparison.Ordinal);
        Assert.False(row.TookPlace);
        // An invitation not in a calendar offers Respond.
        var inv = F.Occ(showAs: ShowAs.Tentative, invitation: new MessageRef { AccountId = new AccountId("g"), MessageId = new MessageId("m") }) with
        {
            Title = "Vendor demo",
            Start = F.At(2026, 10, 13, 15).ToUniversalTime(),
            End = F.At(2026, 10, 13, 16).ToUniversalTime(),
        };
        Assert.True(Assert.Single(TodayRows([inv], now, env, false)).Respond);
    }

    [Fact]
    public void ShowsTodayStripWithACalendarAccount()
    {
        static Account A(string id, IReadOnlyList<Capability>? caps) => new()
        {
            Id = id,
            Config = new AccountConfig { Name = "", Email = "" },
            Enabled = true,
            State = new SyncState { AccountId = id, Status = SyncStatus.Idle },
            Capabilities = caps,
        };
        var calendar = A("w", [.. API.MailCapabilities, Capability.Calendar]);
        var mailOnly = A("a", API.MailCapabilities);
        var legacy = A("l", null);
        Assert.True(ShowsTodayStrip([mailOnly, calendar]));
        Assert.False(ShowsTodayStrip([mailOnly]));
        Assert.False(ShowsTodayStrip([legacy]));
        Assert.False(ShowsTodayStrip([]));
        Assert.False(ShowsTodayStrip(null));
    }

    [Fact]
    public void Texts()
    {
        Assert.Equal("Today in Calendar", TodayHeading);
        Assert.Equal("Open Calendar", OpenCalendar);
        Assert.Equal("took place", TookPlace);
        Assert.Equal("Respond", Respond);
        Assert.Equal("Nothing in the calendar today.", NothingToday);
    }
}
