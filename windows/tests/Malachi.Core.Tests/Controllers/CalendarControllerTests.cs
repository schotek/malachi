// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarControllerTests.swift: the
// calendar page's controller (Controllers/CalendarController.cs) over the
// in-memory source with the invented samples: the views and their ranges
// per week start, the navigation, the persisted view, the search with its
// debounce on a clock the test fires, the selection and its keys, the
// sidebar with the consent row, the Unanswered filter, the empty states;
// then Windows-only cases (Show in Calendar, failed loads and searches, a
// stale answer, the debounce on the TimeProvider, Stop).
//
// Swift's CalManualClock is ManualSleep below: its waits end when the test
// fires them, whatever their token says, as Swift's continuations do.
// Swift's disabledCalendarsAreOnlyInPreferences also builds an
// EventEditorController and a CalendarAccountController over the source;
// those asserts belong to those controllers' ports, so this suite keeps the
// sidebar's and the snapshot's half. Everything runs on the test's UI
// thread; "nothing happened" is checked once everything is idle.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Settings;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;
using Write = Malachi.Core.Calendars.InMemoryCalendarSource.Write;

namespace Malachi.Core.Tests.Controllers;

public sealed class CalendarControllerTests
{
    /// <summary>Wednesday 14 October 2026, 10:17 in Prague (Swift <c>calNow</c>).</summary>
    private static readonly DateTimeOffset Now = F.At(2026, 10, 14, 10, 17);

    private static RpcException Daemon(int code, string message) => new(new RpcError { Code = code, Message = message });

    private static InMemoryCalendarSource Samples(bool writes = true) =>
        new(Cal.SampleData(Now, F.Prague)) { EmulatesWrites = writes };

    private static int Lists(InMemoryCalendarSource s) => s.Reads.Count(r => r == API.EventList.Name);

    [Fact]
    public async Task WeekRangeFollowsTheWeekStart()
    {
        await using var h = new Harness();
        var c = await h.StartedAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(Cal.View.Week, c.View);
            Assert.Equal(F.At(2026, 10, 12), c.Range.From);
            Assert.Equal(F.At(2026, 10, 19), c.Range.To);
            Assert.Equal("October 2026", c.Title);
            Assert.NotEmpty(c.Subtitle);
        });
        // The locale's first day (System) is Sunday: the week from the 11th.
        var us = await h.StartedAsync(localeFirstDay: Cal.Weekday.Sunday);
        Assert.Equal(F.At(2026, 10, 11), await h.Ui.RunAsync(() => us.Range.From));
        // The preference wins over the locale.
        var src = Samples();
        src.Update(s => s with { Preferences = s.Preferences with { WeekStart = WeekStart.Saturday } });
        var sat = await h.StartedAsync(src, localeFirstDay: Cal.Weekday.Monday);
        Assert.Equal(F.At(2026, 10, 10), await h.Ui.RunAsync(() => sat.Range.From));
    }

    [Fact]
    public async Task ViewsAndStepsAndToday()
    {
        await using var h = new Harness();
        var backend = new InMemorySettingsBackend();
        using var settings = new SettingsStore(backend, null);
        var c = await h.StartedAsync(settings: settings);
        await h.Ui.RunAsync(() =>
        {
            c.SetView(Cal.View.Day);
            Assert.True(backend.TryGetString("calendar-view", out var stored) && stored == "day");
            Assert.Equal(F.At(2026, 10, 14), c.Range.From);
            Assert.Equal(F.At(2026, 10, 15), c.Range.To);
            c.Step(1);
            Assert.Equal(F.At(2026, 10, 15), c.Range.From);
            c.SetView(Cal.View.Month);
            Assert.Equal(F.At(2026, 9, 28), c.Range.From);
            Assert.Equal(42, Cal.DaysBetween(c.Range.From, c.Range.To, F.Prague));
            c.Step(-1);
            Assert.Equal(9, Cal.PartsOf(c.Anchor, F.Prague).Month);
            c.Today();
            Assert.Equal(F.At(2026, 10, 14), c.Anchor);
            c.SetView(Cal.View.Agenda);
            Assert.Equal(30, Cal.DaysBetween(c.Range.From, c.Range.To, F.Prague));
            c.LoadMoreAgenda();
            Assert.Equal(60, Cal.DaysBetween(c.Range.From, c.Range.To, F.Prague));
        });
        await h.IdleAsync();
        // A new window reads the stored view.
        var again = await h.Ui.RunAsync(() => h.Controller(settings: settings));
        Assert.Equal(Cal.View.Agenda, await h.Ui.RunAsync(() => again.View));
    }

    [Fact]
    public async Task AgendaStartsAtTodayWhenSwitchedTo()
    {
        await using var h = new Harness();
        var c = await h.StartedAsync();
        await h.Ui.RunAsync(() =>
        {
            var today = F.At(2026, 10, 14);
            c.SetView(Cal.View.Day);
            c.GoTo(F.At(2026, 10, 13, 9));
            Assert.Equal(F.At(2026, 10, 13), c.Anchor);
            c.SetView(Cal.View.Agenda);
            Assert.True(c.Anchor == today && c.Range.From == today);
            // ‹ › move the Agenda; leaving it keeps the stepped anchor.
            c.Step(1);
            var stepped = c.Anchor;
            Assert.Equal(Cal.AddDays(today, 30, F.Prague), stepped);
            c.SetView(Cal.View.Week);
            Assert.Equal(stepped, c.Anchor);
            // Month → Week keeps the anchor too.
            c.SetView(Cal.View.Month);
            c.Step(1);
            var month = c.Anchor;
            c.SetView(Cal.View.Week);
            Assert.Equal(month, c.Anchor);
        });
        await h.IdleAsync();
    }

    [Fact]
    public async Task GoToDayAndMiniMonth()
    {
        await using var h = new Harness();
        var c = await h.StartedAsync();
        await h.Ui.RunAsync(() =>
        {
            c.GoTo(F.At(2026, 11, 3, 15));
            Assert.Equal(F.At(2026, 11, 2), c.Range.From);
            Assert.Equal("November 2026", c.Sidebar.MiniMonth.Title);
            c.StepMiniMonth(1);
            Assert.Equal("December 2026", c.Sidebar.MiniMonth.Title);
            // The range did not move with the mini month.
            Assert.Equal(F.At(2026, 11, 2), c.Range.From);
            // The shown week is banded in the mini month of its month.
            c.StepMiniMonth(-1);
            var banded = c.Sidebar.MiniMonth.Days.Where(d => d.Shown).ToList();
            Assert.Equal(7, banded.Count);
            Assert.True(banded[0].BandStart && banded[^1].BandEnd);
        });
        await h.IdleAsync();
    }

    [Fact]
    public async Task OccurrencesOfTheRangeAndVisibility()
    {
        await using var h = new Harness();
        var src = Samples();
        var c = await h.StartedAsync(src);
        await h.Ui.RunAsync(() =>
        {
            Assert.NotEmpty(c.Occurrences);
            Assert.Contains(c.Occurrences, o => o.Title == "Climbing");
            Assert.Contains(c.Occurrences, o => o.Invitation is not null);
            c.SetVisible(false, Cal.SampleSport);
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.DoesNotContain(c.Occurrences, o => o.Title == "Climbing");
            Assert.Contains(new Write.Visible(Cal.SampleSport, false), src.Writes);
            Assert.False(c.Sidebar.Accounts.SelectMany(a => a.Calendars).First(x => x.Id == Cal.SampleSport).Visible);
            // Next week has none of the samples' events.
            c.Step(1);
        });
        await h.IdleAsync();
        Assert.Empty(await h.Ui.RunAsync(() => c.Occurrences));
    }

    [Fact]
    public async Task RefusedVisibilityIsReported()
    {
        await using var h = new Harness();
        var src = Samples();
        src.Failures[API.CalendarUpdate.Name] = Daemon(ErrorCode.CalendarNotFound, "gone");
        var c = await h.StartedAsync(src);
        var errors = new List<string>();
        await h.Ui.RunAsync(() =>
        {
            c.ToastRequested += (_, e) => errors.Add(e);
            c.SetVisible(false, Cal.SampleSport);
            Assert.Equal([Cal.Text.ErrorText(ErrorCode.CalendarNotFound)!], errors);
            Assert.True(src.Snapshot.FindCalendar(Cal.SampleSport)?.Visible);
        });
        await h.IdleAsync();
    }

    [Fact]
    public async Task SearchWaitsForTheDebounceAndTwoCharacters()
    {
        await using var h = new Harness();
        var clock = new ManualSleep();
        var src = Samples();
        var c = await h.StartedAsync(src, sleep: clock.Sleep);
        await h.Ui.RunAsync(() =>
        {
            c.SetSearch("b");
            Assert.True(!c.SearchPending && c.SearchResults is null);
            c.SetSearch("bu");
            c.SetSearch("bud");
        });
        await h.IdleAsync();
        Assert.Equal([CalendarController.SearchDebounce, CalendarController.SearchDebounce], clock.Pending);
        Assert.DoesNotContain(API.EventSearch.Name, src.Reads);
        clock.Fire();
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(c.SearchPending);
            // Only the last query ran.
            Assert.Single(src.Reads, r => r == API.EventSearch.Name);
            Assert.Equal(["Budget review"], c.SearchResults?.Select(o => o.Title));
            Assert.Equal(Cal.ResultsHeading, c.ListHeading);
            Assert.Equal(["Budget review"], c.ShownOccurrences.Select(o => o.Title));
            // Escape without a flyout clears the search.
            Assert.True(c.HandleKey(new Cal.Press(Special: Cal.Special.Escape)));
            Assert.True(c.SearchResults is null && c.SearchText.Length == 0);
            c.SetSearch("zz");
        });
        await h.IdleAsync();
        Assert.Single(clock.Pending);
        clock.Fire();
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(c.SearchPending);
            Assert.Equal(Cal.NoResults, c.EmptyListText);
            c.SetSearch("z");
            Assert.Null(c.SearchResults);
        });
    }

    [Fact]
    public async Task SelectionAndKeys()
    {
        await using var h = new Harness();
        var c = await h.StartedAsync();
        var opened = new List<string>();
        var removed = new List<string>();
        var closed = 0;
        var defaults = new List<Cal.Defaults>();
        await h.Ui.RunAsync(() =>
        {
            c.OpenDetailRequested += (_, o) => opened.Add(Cal.OccurrenceKey(o));
            c.RemoveRequested += (_, o) => removed.Add(Cal.OccurrenceKey(o));
            c.ClosePopoverRequested += (_, _) => closed++;
            c.NewEventRequested += (_, d) => defaults.Add(d);
            // Return and Delete need a selection.
            Assert.False(c.HandleKey(new Cal.Press(Special: Cal.Special.Return)));
            Assert.True(c.HandleKey(new Cal.Press(Special: Cal.Special.Down)));
            var order = Cal.SelectionOrder(c.Occurrences, F.Prague);
            Assert.Equal(order[0], c.Selection);
            Assert.True(c.HandleKey(new Cal.Press(Special: Cal.Special.Right)));
            Assert.Equal(order[1], c.Selection);
            Assert.True(c.HandleKey(new Cal.Press(Special: Cal.Special.Left)));
            Assert.Equal(order[0], c.Selection);
            // Not in a text field.
            Assert.False(c.HandleKey(new Cal.Press(Special: Cal.Special.Down), inText: true));
            Assert.True(c.HandleKey(new Cal.Press(Special: Cal.Special.Return)));
            Assert.Equal([order[0]], opened);
            Assert.True(c.HandleKey(new Cal.Press(Special: Cal.Special.Delete)));
            Assert.Equal([order[0]], removed);
            Assert.True(c.HandleKey(new Cal.Press(Special: Cal.Special.Escape), popoverOpen: true));
            Assert.Equal(1, closed);
            // Primary+Control+3, Primary+→, Primary+T, Primary+Alt+N.
            Assert.True(c.HandleKey(new Cal.Press(Rune: '3', Primary: true, Control: true)));
            Assert.Equal(Cal.View.Month, c.View);
            Assert.True(c.HandleKey(new Cal.Press(Special: Cal.Special.Right, Primary: true)));
            Assert.Equal(11, Cal.PartsOf(c.Anchor, F.Prague).Month);
            Assert.True(c.HandleKey(new Cal.Press(Rune: 't', Primary: true)));
            Assert.Equal(10, Cal.PartsOf(c.Anchor, F.Prague).Month);
            Assert.True(c.HandleKey(new Cal.Press(Rune: 'n', Primary: true, Alt: true)));
            Assert.Equal(F.At(2026, 10, 14, 10, 30), defaults[0].Start);
            // Primary+3 is the window's.
            Assert.False(c.HandleKey(new Cal.Press(Rune: '3', Primary: true)));
        });
        await h.IdleAsync();
    }

    [Fact]
    public async Task NewEventDefaultsByOrigin()
    {
        await using var h = new Harness();
        var c = await h.StartedAsync();
        await h.Ui.RunAsync(() =>
        {
            var cell = c.NewEventDefaults(F.At(2026, 10, 20), Cal.Origin.MonthCell);
            Assert.Equal(F.At(2026, 10, 20, 9), cell.Start);
            Assert.Equal(F.At(2026, 10, 20, 10), cell.End);
            var allDay = c.NewEventDefaults(F.At(2026, 10, 20, 13), Cal.Origin.AllDay);
            Assert.True(allDay.AllDay);
            var slot = c.NewEventDefaults(F.At(2026, 10, 20, 13, 40), Cal.Origin.Slot);
            Assert.Equal(F.At(2026, 10, 20, 13, 30), slot.Start);
        });
    }

    [Fact]
    public async Task SidebarStatusAndConsent()
    {
        await using var h = new Harness();
        var src = Samples();
        var c = await h.StartedAsync(src);
        var signIn = new List<AccountId>();
        await h.Ui.RunAsync(() =>
        {
            var s = c.Sidebar;
            Assert.Equal(["Work", "Gmail", "Nextcloud"], s.Accounts.Select(a => a.Name));
            Assert.Equal([CalendarSource.Graph, CalendarSource.Google, CalendarSource.CalDAV], s.Accounts.Select(a => a.Source.Value));
            Assert.Equal(3, s.Unanswered);
            Assert.True(s.InvitationsTitle == "Invitations" && s.UnansweredTitle == "Unanswered");
            Assert.All(s.Accounts, a => Assert.True(a.StateLine.Length == 0 && !a.OffersSignIn));
            Assert.Equal("Calendars synced 2 minutes ago", c.StatusLine);
            Assert.Equal("3 unanswered invitations", c.UnansweredLine);
            c.SignInRequested += (_, a) => signIn.Add(a);
            src.Handle(new DaemonNotification.CalendarState(new CalendarSyncState
            {
                AccountId = Cal.SampleWork,
                Status = CalendarSyncStatus.ConsentRequired,
                Progress = -1,
            }));
            var work = c.Sidebar.Accounts[0];
            Assert.True(work.StateLine == Cal.Text.ConsentLine && work.OffersSignIn);
            Assert.Equal(Cal.Text.ConsentLine, c.StatusLine);
            c.SignIn(work.AccountId);
            Assert.Equal([Cal.SampleWork], signIn);
        });
    }

    [Fact]
    public async Task DisabledCalendarsAreOnlyInPreferences()
    {
        await using var h = new Harness();
        var src = Samples();
        src.Update(s =>
        {
            var accounts = s.CalendarAccounts.ToList();
            var calendars = accounts[1].Calendars.ToList();
            calendars[1] = calendars[1] with { Disabled = true };
            accounts[1] = accounts[1] with { Calendars = calendars };
            return s with { CalendarAccounts = accounts };
        });
        var c = await h.StartedAsync(src);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal([Cal.SamplePersonal], c.Sidebar.Accounts[1].Calendars.Select(x => x.Id));
            Assert.DoesNotContain(Cal.SampleFamily, src.Snapshot.VisibleCalendars);
            Assert.DoesNotContain(Cal.SampleFamily, src.Snapshot.WritableCalendars.Select(x => x.Id));
            Assert.True(src.Snapshot.FindCalendar(Cal.SampleFamily) is null && src.Snapshot.CalendarName(Cal.SampleFamily).Length == 0);
            Assert.DoesNotContain(c.Occurrences, o => o.CalendarId == Cal.SampleFamily);
            // Preferences → Calendar still lists it, switched off.
            Assert.Equal(
                [Cal.SamplePersonal, Cal.SampleFamily],
                src.Snapshot.FindCalendarAccount(Cal.SampleGmail)?.Calendars.Select(x => x.Id));
        });
    }

    [Fact]
    public async Task UnansweredFilterListsTheInvitations()
    {
        await using var h = new Harness();
        var c = await h.StartedAsync();
        await h.Ui.RunAsync(() =>
        {
            c.ShowUnanswered();
            Assert.Equal(CalendarController.ContentFilter.Unanswered, c.Filter);
            Assert.Equal(Cal.UnansweredHeading, c.ListHeading);
            var items = (c.Agenda ?? []).SelectMany(d => d.Items).ToList();
            Assert.Equal(new HashSet<string> { "Budget review", "Vendor demo", "Club meeting" }, items.Select(i => i.Occurrence.Title).ToHashSet());
            Assert.All(items, i => Assert.Equal(Cal.BlockState.Unanswered, i.State));
            // Escape ends the filter; so does a view change.
            Assert.True(c.HandleKey(new Cal.Press(Special: Cal.Special.Escape)));
            Assert.Equal(CalendarController.ContentFilter.None, c.Filter);
            c.ShowUnanswered();
            c.SetView(Cal.View.Week);
            Assert.Equal(CalendarController.ContentFilter.None, c.Filter);
        });
        await h.IdleAsync();
    }

    [Fact]
    public async Task EmptyStates()
    {
        await using var h = new Harness();
        var empty = new InMemoryCalendarSource();
        var c = await h.StartedAsync(empty);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(Cal.EmptyKind.NoAccounts, c.EmptyKind);
            empty.Update(s => s with { Phase = Cal.Phase.Unavailable });
            Assert.Equal(Cal.EmptyKind.NoBackend, c.EmptyKind);
        });
        var src = Samples();
        src.Update(s => s with { CalendarAccounts = [.. s.CalendarAccounts.Select(a => a with { Sync = false })] });
        var off = await h.StartedAsync(src);
        Assert.Equal(Cal.EmptyKind.SyncOff, await h.Ui.RunAsync(() => off.EmptyKind));
        var full = await h.StartedAsync();
        Assert.Null(await h.Ui.RunAsync(() => full.EmptyKind));
    }

    [Fact]
    public async Task NotificationsReloadAnOverlappingRange()
    {
        await using var h = new Harness();
        var src = Samples();
        await h.StartedAsync(src);
        var before = Lists(src);
        // Next year: not this week.
        await h.HandleAsync(src, F.At(2027, 1, 1), F.At(2027, 1, 2));
        Assert.Equal(before, Lists(src));
        await h.HandleAsync(src, F.At(2026, 10, 15), F.At(2026, 10, 16));
        Assert.Equal(before + 1, Lists(src));
        await h.HandleAsync(src, null, null);
        Assert.Equal(before + 2, Lists(src));
    }

    [Fact]
    public async Task SamplesAnswerThePreview()
    {
        var src = Samples(writes: false);
        var e = await Assert.ThrowsAsync<Cal.SourceError>(() => src.SyncAsync(new CalendarSyncParams()));
        Assert.Equal(Cal.SourceErrorKind.Preview, e.Kind);
        Assert.Equal(Cal.Text.PreviewOnly, Cal.ErrorMessage(Cal.SourceError.Preview));
        // The checkboxes work.
        src.SetVisible(false, Cal.SampleFamily);
        Assert.False(src.Snapshot.FindCalendar(Cal.SampleFamily)?.Visible);
        Assert.Equal(Cal.Phase.Ready, InMemoryCalendarSource.Samples(Now, F.Prague).Snapshot.Phase);
    }

    // Windows-only cases.

    /// <summary>Show in Calendar: the view switched to and kept, the day shown, the occurrence selected.</summary>
    [Fact]
    public async Task ShowSelectsTheOccurrenceOnItsDay()
    {
        await using var h = new Harness();
        var backend = new InMemorySettingsBackend();
        using var settings = new SettingsStore(backend, null);
        var c = await h.StartedAsync(settings: settings);
        var key = await h.Ui.RunAsync(() => Cal.OccurrenceKey(c.Occurrences.First(o => o.Title == "Climbing")));
        await h.Ui.RunAsync(() => c.Show(F.At(2026, 10, 15, 18), select: key));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(Cal.View.Day, c.View);
            Assert.True(backend.TryGetString("calendar-view", out var stored) && stored == "day");
            Assert.Equal(F.At(2026, 10, 15), c.Range.From);
            Assert.Equal(key, c.Selection);
            Assert.Equal("Climbing", c.SelectedOccurrence?.Title);
            // A selection the range no longer has is dropped.
            c.Step(1);
        });
        await h.IdleAsync();
        Assert.Null(await h.Ui.RunAsync(() => c.Selection));
    }

    /// <summary>A failed load and a failed search are toasts; the search's results are empty.</summary>
    [Fact]
    public async Task FailuresAreToasts()
    {
        await using var h = new Harness();
        var clock = new ManualSleep();
        var src = Samples();
        src.Failures[API.EventList.Name] = Daemon(ErrorCode.StorageError, "disk");
        src.Failures[API.EventSearch.Name] = Daemon(ErrorCode.CalendarNotFound, "gone");
        var errors = new List<string>();
        var c = await h.Ui.RunAsync(() =>
        {
            var made = h.Controller(src, sleep: clock.Sleep);
            made.ToastRequested += (_, e) => errors.Add(e);
            made.Start();
            return made;
        });
        await h.IdleAsync();
        Assert.Single(errors);
        Assert.True(await h.Ui.RunAsync(() => c.IsIdle && c.Occurrences.Count == 0));
        await h.Ui.RunAsync(() => c.SetSearch("budget"));
        await h.IdleAsync();
        clock.Fire();
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(Cal.Text.ErrorText(ErrorCode.CalendarNotFound), errors[^1]);
            Assert.True(c.SearchResults is { Count: 0 } && c.SearchTotal == 0 && !c.SearchPending);
            Assert.Equal(Cal.NoResults, c.EmptyListText);
        });
    }

    /// <summary>An answer for a range left meanwhile is dropped; the newest answer counts.</summary>
    [Fact]
    public async Task AStaleAnswerIsDropped()
    {
        await using var h = new Harness();
        var src = Samples();
        var c = await h.StartedAsync(src);
        await h.Ui.RunAsync(() =>
        {
            src.Holding = true;
            c.Step(1);
            c.Step(-1);
        });
        await h.UntilAsync(() => src.HeldCount == 2);
        Assert.True(await h.Ui.RunAsync(() => c.Loading));
        await h.Ui.RunAsync(src.Release);
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(c.Loading);
            Assert.Contains(c.Occurrences, o => o.Title == "Climbing");
        });
    }

    /// <summary>The debounce on the controller's clock: nothing before 300 ms, the search at 300 ms.</summary>
    [Fact]
    public async Task TheDebounceWaitsOnTheClock()
    {
        await using var h = new Harness();
        var src = Samples();
        var c = await h.StartedAsync(src);
        await h.Ui.RunAsync(() => c.SetSearch("climb"));
        await h.IdleAsync();
        h.Clock.Advance(CalendarController.SearchDebounce - TimeSpan.FromMilliseconds(1));
        await h.IdleAsync();
        Assert.DoesNotContain(API.EventSearch.Name, src.Reads);
        Assert.True(await h.Ui.RunAsync(() => c.SearchPending));
        h.Clock.Advance(TimeSpan.FromMilliseconds(1));
        await h.UntilAsync(() => !c.SearchPending);
        await h.IdleAsync();
        Assert.Equal(["Climbing"], await h.Ui.RunAsync(() => c.SearchResults?.Select(o => o.Title).ToList()));
        // A change within the wait starts it over.
        await h.Ui.RunAsync(() => c.SetSearch("budget"));
        await h.IdleAsync();
        h.Clock.Advance(TimeSpan.FromMilliseconds(200));
        await h.Ui.RunAsync(() => c.SetSearch("budget r"));
        await h.IdleAsync();
        h.Clock.Advance(TimeSpan.FromMilliseconds(200));
        await h.IdleAsync();
        Assert.Single(src.Reads, r => r == API.EventSearch.Name);
        h.Clock.Advance(TimeSpan.FromMilliseconds(100));
        await h.UntilAsync(() => !c.SearchPending);
        await h.IdleAsync();
        Assert.Equal(2, src.Reads.Count(r => r == API.EventSearch.Name));
        Assert.Equal(["Budget review"], await h.Ui.RunAsync(() => c.SearchResults?.Select(o => o.Title).ToList()));
    }

    /// <summary>Stopped, the controller follows the source no more and takes no answer.</summary>
    [Fact]
    public async Task StopFollowsNothing()
    {
        await using var h = new Harness();
        var src = Samples();
        var c = await h.StartedAsync(src);
        var changes = 0;
        await h.Ui.RunAsync(() =>
        {
            c.Changed += (_, _) => changes++;
            c.Stop();
            src.Handle(new DaemonNotification.CalendarChanged(new CalendarChangedNotification()));
            src.SetVisible(false, Cal.SampleSport);
        });
        await h.IdleAsync();
        Assert.Equal(0, changes);
        Assert.Contains(await h.Ui.RunAsync(() => c.Occurrences), o => o.Title == "Climbing");
    }

    /// <summary>Swift's CalManualClock: sleeps that wait until the test fires them.</summary>
    private sealed class ManualSleep
    {
        private readonly Lock gate = new();
        private readonly List<(TimeSpan Delay, TaskCompletionSource Done)> waiting = [];

        public IReadOnlyList<TimeSpan> Pending
        {
            get
            {
                lock (gate)
                {
                    return [.. waiting.Select(w => w.Delay)];
                }
            }
        }

        public Task Sleep(TimeSpan delay, CancellationToken cancellationToken)
        {
            var done = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            lock (gate)
            {
                waiting.Add((delay, done));
            }
            return done.Task;
        }

        public void Fire()
        {
            List<(TimeSpan Delay, TaskCompletionSource Done)> w;
            lock (gate)
            {
                w = [.. waiting];
                waiting.Clear();
            }
            foreach (var (_, done) in w)
            {
                done.TrySetResult();
            }
        }
    }

    private sealed class Harness : IAsyncDisposable
    {
        private readonly List<CalendarController> controllers = [];

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeTimeProvider Clock { get; } = new(Now);

        /// <summary>A controller over <paramref name="source"/> (the samples when null), on the UI thread.</summary>
        public CalendarController Controller(
            ICalendarDataSource? source = null, SettingsStore? settings = null, Cal.Weekday localeFirstDay = Cal.Weekday.Monday,
            Cal.Sleep? sleep = null)
        {
            var c = new CalendarController(
                source ?? Samples(), settings, F.Env(), Clock, localeFirstDay, sleep, Pending);
            controllers.Add(c);
            return c;
        }

        /// <summary>Swift's started(controller(…)): made, started and idle.</summary>
        public async Task<CalendarController> StartedAsync(
            ICalendarDataSource? source = null, SettingsStore? settings = null, Cal.Weekday localeFirstDay = Cal.Weekday.Monday,
            Cal.Sleep? sleep = null)
        {
            var c = await Ui.RunAsync(() =>
            {
                var made = Controller(source, settings, localeFirstDay, sleep);
                made.Start();
                return made;
            });
            await IdleAsync();
            Assert.True(await Ui.RunAsync(() => c.IsIdle));
            return c;
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending);

        /// <summary>Hands the source notify.calendarChanged and waits for what follows.</summary>
        public async Task HandleAsync(InMemoryCalendarSource source, DateTimeOffset? from, DateTimeOffset? to)
        {
            await Ui.RunAsync(() => source.Handle(new DaemonNotification.CalendarChanged(new CalendarChangedNotification { From = from, To = to })));
            await IdleAsync();
        }

        /// <summary>Waits until <paramref name="condition"/> holds on the UI thread (for a call held on purpose).</summary>
        public async Task UntilAsync(Func<bool> condition)
        {
            var deadline = DateTime.UtcNow + TimeSpan.FromSeconds(10);
            while (!await Ui.RunAsync(condition))
            {
                if (DateTime.UtcNow > deadline)
                {
                    throw new TimeoutException("the condition did not hold in time");
                }
                await Task.Delay(2);
            }
        }

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(() =>
            {
                foreach (var c in controllers)
                {
                    c.Dispose();
                }
            });
            try
            {
                await Pending.IdleAsync().WaitAsync(TimeSpan.FromSeconds(10));
            }
            catch (TimeoutException)
            {
                // Torn down all the same; the test's own asserts said what went wrong.
            }
            Ui.Dispose();
        }
    }
}
