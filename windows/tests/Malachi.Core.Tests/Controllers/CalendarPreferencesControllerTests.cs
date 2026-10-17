// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarPreferencesControllerTests.swift.
// Preferences → Calendar → Display (CalendarPreferencesController): the
// rows' choices, a change shown at once and written, the answer kept, a
// refusal undone, the working hours. Swift's calWaitUntil polls are
// Quiescence.IdleAsync here. The last test is Windows's: the writes go one
// after another (Swift sends them at once).

using System;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;
using Write = Malachi.Core.Calendars.InMemoryCalendarSource.Write;

namespace Malachi.Core.Tests.Controllers;

public sealed class CalendarPreferencesControllerTests
{
    /// <summary>Wednesday 14 October 2026, 10:17 in Prague (Swift <c>calNow</c>).</summary>
    private static readonly DateTimeOffset Now = F.At(2026, 10, 14, 10, 17);

    /// <summary>The samples with emulated writes (Swift <c>calSamples()</c>).</summary>
    private static InMemoryCalendarSource Samples()
    {
        var s = InMemoryCalendarSource.Samples(Now, F.Prague);
        s.EmulatesWrites = true;
        return s;
    }

    [Fact]
    public async Task RoundTrip()
    {
        using var h = new Harness();
        var src = Samples();
        var p = await h.Ui.RunAsync(() =>
        {
            var c = new CalendarPreferencesController(src, F.Env(), h.Pending);
            c.Start();
            return c;
        });
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(
                [WeekStart.Locale, WeekStart.Monday, WeekStart.Sunday, WeekStart.Saturday],
                p.WeekStartChoices.Select(c => c.Value.Value));
            Assert.Equal("System", p.WeekStartChoices[0].Title);
            Assert.Equal([15, 30, 45, 60, 90, 120], p.DurationChoices.Select(c => c.Value));
            Assert.Equal(Cal.AlertTitle(-1), p.AlertChoices[0].Title);
            Assert.Equal("Every weekday · 08:00–17:00", p.WorkingHoursText);
            p.SetWeekStart(WeekStart.Sunday);
            Assert.Equal(WeekStart.Sunday, p.Preferences.WeekStart.Value);
            Assert.True(p.Saving);
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(p.Saving);
            Assert.Equal([new Write.SetPreferences(new CalendarPreferencesUpdate { WeekStart = WeekStart.Sunday })], src.Writes);
            Assert.Equal(WeekStart.Sunday, src.Snapshot.Preferences.WeekStart.Value);
            p.SetShowDeclined(true);
            p.SetDefaultDuration(30);
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(p.Saving);
            Assert.True(p.Preferences.ShowDeclined);
            Assert.Equal(30, p.Preferences.DefaultDurationMinutes);
            // A value that is not a choice is listed.
            p.SetDefaultAlert(20);
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(p.Saving);
            Assert.Contains(20, p.AlertChoices.Select(c => c.Value));
        });
    }

    [Fact]
    public async Task RefusalIsUndone()
    {
        using var h = new Harness();
        var src = Samples();
        src.Failures[API.CalendarSetPreferences.Name] = new RpcException(new RpcError { Code = ErrorCode.InvalidArgument, Message = "range" });
        var p = await h.Ui.RunAsync(() =>
        {
            var c = new CalendarPreferencesController(src, F.Env(), h.Pending);
            c.Start();
            c.SetDefaultDuration(90);
            Assert.Equal(90, c.Preferences.DefaultDurationMinutes);
            return c;
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(p.Saving);
            Assert.Equal(60, p.Preferences.DefaultDurationMinutes);
            Assert.NotEmpty(p.Error);
        });
    }

    [Fact]
    public async Task WorkingHours()
    {
        using var h = new Harness();
        var src = Samples();
        var p = await h.Ui.RunAsync(() =>
        {
            var c = new CalendarPreferencesController(src, F.Env(), h.Pending);
            c.Start();
            Assert.Equal(["MO", "TU", "WE", "TH", "FR", "SA", "SU"], c.WorkingDayChoices.Select(d => d.Value));
            Assert.Equal("Mon", c.WorkingDayChoices[0].Title);
            Assert.True(c.IsWorkingDay("MO"));
            Assert.False(c.IsWorkingDay("SA"));
            c.SetWorkingDay("SA", true);
            return c;
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(["MO", "TU", "WE", "TH", "FR", "SA"], p.Preferences.WorkingHours.Days);
            var times = p.WorkingTimeChoices("08:15");
            Assert.Equal(49, times.Count);
            Assert.Contains(times, t => t.Value == "08:15");
            Assert.Equal("00:00", times[0].Title);
            p.SetWorkingStart("18:00");
            Assert.Equal("08:00", p.Preferences.WorkingHours.Start);
            p.SetWorkingStart("09:30");
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            p.SetWorkingEnd("09:00");
            Assert.Equal("17:00", p.Preferences.WorkingHours.End);
            p.SetWorkingEnd("18:30");
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal("09:30", src.Snapshot.Preferences.WorkingHours.Start);
            Assert.Equal("18:30", src.Snapshot.Preferences.WorkingHours.End);
        });
        foreach (var d in new[] { "MO", "TU", "WE", "TH", "FR" })
        {
            await h.Ui.RunAsync(() => p.SetWorkingDay(d, false));
            await h.IdleAsync();
        }
        // The last day stays.
        await h.Ui.RunAsync(() =>
        {
            p.SetWorkingDay("SA", false);
            Assert.Equal(["SA"], p.Preferences.WorkingHours.Days);
            Assert.False(p.Saving);
        });
    }

    /// <summary>Windows: a write waits for the one before it; both show at once and stay.</summary>
    [Fact]
    public async Task WritesGoOneAfterAnother()
    {
        using var h = new Harness();
        var src = Samples();
        src.Holding = true;
        var p = await h.Ui.RunAsync(() =>
        {
            var c = new CalendarPreferencesController(src, F.Env(), h.Pending);
            c.Start();
            c.SetShowDeclined(true);
            c.SetDefaultDuration(30);
            Assert.True(c.Preferences.ShowDeclined);
            Assert.Equal(30, c.Preferences.DefaultDurationMinutes);
            return c;
        });
        await Eventually.Holds(() => src.HeldCount == 1);
        await h.Ui.RunAsync(() =>
        {
            Assert.Single(src.Writes);
            Assert.True(p.Saving);
            src.Release();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(
                [
                    new Write.SetPreferences(new CalendarPreferencesUpdate { ShowDeclined = true }),
                    new Write.SetPreferences(new CalendarPreferencesUpdate { DefaultDurationMinutes = 30 }),
                ],
                src.Writes);
            Assert.False(p.Saving);
            Assert.True(p.Preferences.ShowDeclined);
            Assert.Equal(30, p.Preferences.DefaultDurationMinutes);
        });
    }

    private sealed class Harness : IDisposable
    {
        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending);

        public void Dispose() => Ui.Dispose();
    }
}
