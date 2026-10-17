// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/DaemonCalendarSourceTests.swift: the
// calendar from the daemon (Calendars/DaemonCalendarSource.cs) against a
// fake daemon: the snapshot's calls and params, the range cache and its
// invalidation by notify.calendarChanged ranges, the event cache and a
// conflict's reload, a consentRequired state reaching the sidebar, the
// optimistic visibility and its revert, an old daemon; then Windows-only
// cases of the source's other rules (the phases, a reconnect, a range
// dropped while its call is on its way, a reload, a delete, the invitations,
// an unchanged state, Stop and Start again).
//
// The fake daemon holds calendar.update until the test releases it;
// "nothing happened" is checked once everything is idle (Quiescence); while
// a reply is held on purpose, the test waits for a condition on the UI
// thread instead. Swift's eventsAreCachedAndAConflictReplacesThem ends with
// an EventDetailController over this source; that part is the detail
// controller's port, not this suite's.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class DaemonCalendarSourceTests
{
    /// <summary>Wednesday 14 October 2026, 10:17 in Prague (Swift <c>calNow</c>).</summary>
    private static readonly DateTimeOffset Now = F.At(2026, 10, 14, 10, 17);

    private static Cal.Range Week() => Cal.ViewRange(Cal.View.Week, Now, Cal.Weekday.Monday, F.Prague);

    private static RpcException Daemon(int code, string message) => new(new RpcError { Code = code, Message = message });

    private static DaemonNotification.CalendarChanged Changed(
        DateTimeOffset? from = null, DateTimeOffset? to = null, IReadOnlyList<CalendarId>? calendars = null) =>
        new DaemonNotification.CalendarChanged(new CalendarChangedNotification { From = from, To = to, CalendarIds = calendars });

    [Fact]
    public async Task StartLoadsTheSnapshot()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        var s = await h.SnapshotAsync();
        Assert.Equal(Cal.Phase.Ready, s.Phase);
        Assert.True(s.Accounts.Count == 3 && s.CalendarAccounts.Count == 3);
        Assert.True(s.Unanswered == 3 && s.Invitations.Count == 3);
        Assert.Equal(
            [API.AccountList.Name, API.CalendarList.Name, API.CalendarPreferences.Name, API.InvitationList.Name],
            h.Script.Calls);
        Assert.Equal(InvitationState.Unanswered, h.Script.Last<InvitationListParams>(API.InvitationList.Name)?.State?.Value);
        Assert.Contains(new Cal.SourceChange.SnapshotChanged(), await h.ChangesAsync());
    }

    [Fact]
    public async Task RangesAreCachedAndDroppedByOverlap()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        var r = Week();
        var (a, b) = await h.Ui.RunAsync(() => (h.Source.OccurrencesAsync(r), h.Source.OccurrencesAsync(r)));
        var x = await a;
        var y = await b;
        Assert.Same(x, y);
        Assert.NotEmpty(x.Occurrences);
        Assert.Equal(1, h.Script.Count(API.EventList.Name));
        var p = Assert.IsType<EventListParams>(h.Script.Last<EventListParams>(API.EventList.Name));
        Assert.True(p.From == r.From && p.To == r.To && p.TimeZone == "Europe/Prague" && p.IncludeInvitations == true);
        Assert.Same(x, await h.Ui.RunAsync(() => h.Source.CachedOccurrences(r)));
        await h.Ui.InvokeAsync(() => h.Source.OccurrencesAsync(r));
        Assert.Equal(1, h.Script.Count(API.EventList.Name));

        // A change next year leaves this week's cache.
        var lists = h.Script.Count(API.CalendarList.Name);
        var march1 = F.At(2027, 3, 1);
        var march2 = F.At(2027, 3, 2);
        await h.HandleAsync(Changed(march1, march2, [Cal.SampleHolidays]));
        Assert.NotNull(await h.Ui.RunAsync(() => h.Source.CachedOccurrences(r)));
        Assert.Equal(new Cal.SourceChange.OccurrencesChanged(march1, march2), (await h.ChangesAsync())[^1]);
        // The calendars are listed again, the preferences not (calendars named).
        Assert.Equal(lists + 1, h.Script.Count(API.CalendarList.Name));
        Assert.Equal(1, h.Script.Count(API.CalendarPreferences.Name));
        // A change on Thursday drops it.
        await h.HandleAsync(Changed(F.At(2026, 10, 15), F.At(2026, 10, 15, 12)));
        Assert.Null(await h.Ui.RunAsync(() => h.Source.CachedOccurrences(r)));
        Assert.Equal(2, h.Script.Count(API.CalendarPreferences.Name));
        await h.Ui.InvokeAsync(() => h.Source.OccurrencesAsync(r));
        Assert.Equal(2, h.Script.Count(API.EventList.Name));
        // Without a range: everything.
        await h.HandleAsync(Changed());
        Assert.Null(await h.Ui.RunAsync(() => h.Source.CachedOccurrences(r)));
    }

    [Fact]
    public async Task EventsAreCachedAndAConflictReplacesThem()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        var id = new CalendarEventId("ev_sample_offsite");
        var e = await h.Ui.InvokeAsync(() => h.Source.EventAsync(id, null));
        await h.Ui.InvokeAsync(() => h.Source.EventAsync(id, null));
        Assert.Equal(1, h.Script.Count(API.EventGet.Name));
        Assert.Equal("Europe/Prague", h.Script.Last<EventGetParams>(API.EventGet.Name)?.TimeZone?.Value);
        h.Script.ConflictEvent = e with { ETag = "\"7\"", Title = "Offsite (changed elsewhere)" };
        var thrown = await Assert.ThrowsAsync<RpcException>(() => h.Ui.InvokeAsync(() => h.Source.UpdateEventAsync(new EventUpdateParams
        {
            EventId = id,
            Scope = EditScope.All,
            ETag = e.ETag,
            Event = new EventInput { Title = "x", AllDay = true },
        })));
        Assert.Equal(ErrorCode.Conflict, thrown.Code.Value);
        Assert.Equal(new Cal.SourceChange.EventChanged(id), (await h.ChangesAsync())[^1]);
        var cached = await h.Ui.InvokeAsync(() => h.Source.EventAsync(id, null));
        Assert.True(cached.ETag == "\"7\"" && cached.Title == "Offsite (changed elsewhere)");
        Assert.Equal(1, h.Script.Count(API.EventGet.Name));
    }

    [Fact]
    public async Task ConsentRequiredReachesTheSidebar()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        var c = await h.Ui.RunAsync(() => new CalendarController(
            h.Source, env: F.Env(), time: new FixedTime(Now), localeFirstDay: Cal.Weekday.Monday, pending: h.Pending));
        await h.Ui.RunAsync(c.Start);
        await h.IdleAsync();
        Assert.False(await h.Ui.RunAsync(() => c.Sidebar.Accounts[0].OffersSignIn));
        await h.Ui.RunAsync(() => h.Source.Handle(new DaemonNotification.CalendarState(new CalendarSyncState
        {
            AccountId = Cal.SampleWork,
            Status = CalendarSyncStatus.ConsentRequired,
            Progress = -1,
        })));
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(CalendarSyncStatus.ConsentRequired, h.Source.Snapshot.FindCalendarAccount(Cal.SampleWork)?.State.Status.Value);
            var work = c.Sidebar.Accounts[0];
            Assert.True(work.StateLine == Cal.Text.ConsentLine && work.OffersSignIn);
            Assert.Equal(Cal.Text.ConsentLine, c.StatusLine);
        });
        // An alert goes to the observers as it is.
        var alert = new EventAlert
        {
            AlertId = new CalendarAlertId("al_9"),
            State = EventAlertState.Due,
            EventId = new CalendarEventId("ev_sample_planning"),
            AccountId = Cal.SampleWork,
            CalendarId = Cal.SampleWorkCalendar,
            Title = "Q4 planning",
            AllDay = false,
            MinutesBefore = 10,
        };
        await h.Ui.RunAsync(() => h.Source.Handle(new DaemonNotification.EventAlert(new EventAlertNotification { Alert = alert })));
        Assert.Equal(new Cal.SourceChange.AlertChanged(alert), (await h.ChangesAsync())[^1]);
        await h.Ui.RunAsync(c.Dispose);
    }

    [Fact]
    public async Task VisibilityIsOptimisticAndRevertedOnRefusal()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        h.Script.Updates.Hold(true);
        h.Script.UpdateFailure = Daemon(ErrorCode.CalendarNotFound, "gone");
        await h.Ui.RunAsync(() =>
        {
            h.Source.SetVisible(false, Cal.SampleSport);
            Assert.False(h.Source.Snapshot.FindCalendar(Cal.SampleSport)?.Visible);
        });
        await h.UntilAsync(() => h.Script.Updates.Waiting == 1);
        // A calendar.list meanwhile does not undo it.
        await h.Ui.RunAsync(h.Source.Refresh);
        await h.UntilAsync(() => h.Script.Count(API.InvitationList.Name) == 2 && h.Pending.Count == 1);
        Assert.False(await h.Ui.RunAsync(() => h.Source.Snapshot.FindCalendar(Cal.SampleSport)?.Visible));
        Assert.Equal(2, h.Script.Count(API.CalendarList.Name));
        h.Script.Updates.Hold(false);
        await h.IdleAsync();
        Assert.True(await h.Ui.RunAsync(() => h.Source.Snapshot.FindCalendar(Cal.SampleSport)?.Visible));
        Assert.Contains(new Cal.SourceChange.Failed(Cal.Text.ErrorText(ErrorCode.CalendarNotFound)!), await h.ChangesAsync());
        var p = Assert.IsType<CalendarUpdateParams>(h.Script.Last<CalendarUpdateParams>(API.CalendarUpdate.Name));
        Assert.True(p.CalendarId == Cal.SampleSport && p.Visible == false);
        // Accepted: it stays.
        h.Script.UpdateFailure = null;
        await h.Ui.RunAsync(() => h.Source.SetVisible(false, Cal.SampleFamily));
        await h.IdleAsync();
        Assert.False(await h.Ui.RunAsync(() => h.Source.Snapshot.FindCalendar(Cal.SampleFamily)?.Visible));
    }

    [Fact]
    public async Task AccountsChangedListsTheCalendarsAgain()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        var lists = h.Script.Count(API.CalendarList.Name);
        var accounts = h.Script.Count(API.AccountList.Name);
        await h.Ui.RunAsync(h.Source.AccountsChanged);
        await h.IdleAsync();
        Assert.Equal(lists + 1, h.Script.Count(API.CalendarList.Name));
        Assert.Equal(accounts + 1, h.Script.Count(API.AccountList.Name));
    }

    [Fact]
    public async Task NamedCalendarsDropTheirRanges()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        var r = Week();
        await h.Ui.InvokeAsync(() => h.Source.OccurrencesAsync(r));
        // A range far away, but naming a calendar this week shows: dropped.
        await h.HandleAsync(Changed(F.At(2027, 3, 1), F.At(2027, 3, 2), [Cal.SampleSport]));
        Assert.Null(await h.Ui.RunAsync(() => h.Source.CachedOccurrences(r)));
        Assert.Equal(new Cal.SourceChange.OccurrencesChanged(null, null), (await h.ChangesAsync())[^1]);
    }

    [Fact]
    public async Task OldDaemonIsUnsupported()
    {
        await using var h = await Harness.StartAsync();
        h.Script.ListFailure = Daemon(ErrorCode.MethodNotFound, "unknown method");
        await h.StartedAsync();
        Assert.Equal(Cal.Phase.Unsupported, (await h.SnapshotAsync()).Phase);
        await h.Ui.RunAsync(() => h.Source.ConnectionChanged(false));
        Assert.Equal(Cal.Phase.Unavailable, (await h.SnapshotAsync()).Phase);
    }

    // Windows-only cases of the source's rules.

    /// <summary>Why a load failed decides the phase (Swift <c>phase(of:)</c>).</summary>
    [Fact]
    public void PhaseOfAFailure()
    {
        Assert.Equal(Cal.Phase.Unavailable, DaemonCalendarSource.PhaseOf(new RpcClientException(ClientError.NotConnected)));
        Assert.Equal(Cal.Phase.Unavailable, DaemonCalendarSource.PhaseOf(new RpcClientException(ClientError.Disconnected)));
        Assert.Equal(Cal.Phase.Failed, DaemonCalendarSource.PhaseOf(new RpcClientException(ClientError.Timeout(API.CalendarList.Name))));
        Assert.Equal(Cal.Phase.Unsupported, DaemonCalendarSource.PhaseOf(Daemon(ErrorCode.MethodNotFound, "x")));
        Assert.Equal(Cal.Phase.Unsupported, DaemonCalendarSource.PhaseOf(Daemon(ErrorCode.NotImplemented, "x")));
        Assert.Equal(Cal.Phase.Failed, DaemonCalendarSource.PhaseOf(Daemon(ErrorCode.StorageError, "x")));
    }

    /// <summary>A failed calendar.list keeps what arrived and asks again next time for what did not.</summary>
    [Fact]
    public async Task AFailedLoadIsAskedAgain()
    {
        await using var h = await Harness.StartAsync();
        h.Script.ListFailure = Daemon(ErrorCode.StorageError, "disk");
        await h.StartedAsync();
        var s = await h.SnapshotAsync();
        Assert.True(s.Phase == Cal.Phase.Failed && s.Accounts.Count == 3 && s.CalendarAccounts.Count == 0);
        Assert.Equal([API.AccountList.Name, API.CalendarList.Name], h.Script.Calls);
        // The next notification asks for the preferences and invitations as well.
        h.Script.ListFailure = null;
        await h.HandleAsync(Changed(calendars: [Cal.SampleSport]));
        s = await h.SnapshotAsync();
        Assert.True(s.Phase == Cal.Phase.Ready && s.CalendarAccounts.Count == 3 && s.Invitations.Count == 3);
        Assert.Equal(1, h.Script.Count(API.AccountList.Name));
        Assert.Equal(1, h.Script.Count(API.CalendarPreferences.Name));
        Assert.Equal(1, h.Script.Count(API.InvitationList.Name));
    }

    /// <summary>The connection back: every cache dropped, everything asked again; gone: the snapshot stays.</summary>
    [Fact]
    public async Task AReconnectDropsTheCaches()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        var r = Week();
        await h.Ui.InvokeAsync(() => h.Source.OccurrencesAsync(r));
        await h.Ui.InvokeAsync(() => h.Source.EventAsync(new CalendarEventId("ev_sample_offsite"), null));
        await h.Ui.RunAsync(() => h.Source.ConnectionChanged(false));
        var s = await h.SnapshotAsync();
        Assert.True(s.Phase == Cal.Phase.Unavailable && s.CalendarAccounts.Count == 3);
        Assert.NotNull(await h.Ui.RunAsync(() => h.Source.CachedOccurrences(r)));
        await h.Ui.RunAsync(() => h.Source.ConnectionChanged(true));
        Assert.Null(await h.Ui.RunAsync(() => h.Source.CachedOccurrences(r)));
        await h.IdleAsync();
        Assert.Equal(Cal.Phase.Ready, (await h.SnapshotAsync()).Phase);
        Assert.Equal(2, h.Script.Count(API.AccountList.Name));
        Assert.Equal(2, h.Script.Count(API.CalendarPreferences.Name));
        await h.Ui.InvokeAsync(() => h.Source.EventAsync(new CalendarEventId("ev_sample_offsite"), null));
        Assert.Equal(2, h.Script.Count(API.EventGet.Name));
    }

    /// <summary>A range dropped while its event.list is on its way is not stored from that answer.</summary>
    [Fact]
    public async Task ARangeDroppedOnItsWayIsNotStored()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        var r = Week();
        h.Script.Lists.Hold(true);
        var call = await h.Ui.RunAsync(() => h.Source.OccurrencesAsync(r));
        await h.UntilAsync(() => h.Script.Lists.Waiting == 1);
        Assert.False(await h.Ui.RunAsync(() => h.Source.IsIdle));
        await h.Ui.RunAsync(() => h.Source.Handle(Changed(F.At(2026, 10, 13), F.At(2026, 10, 14))));
        h.Script.Lists.Hold(false);
        var answer = await call;
        Assert.NotEmpty(answer.Occurrences);
        await h.IdleAsync();
        Assert.True(await h.Ui.RunAsync(() => h.Source.IsIdle));
        Assert.Null(await h.Ui.RunAsync(() => h.Source.CachedOccurrences(r)));
    }

    /// <summary>reload asks the daemon anyway and tells the observers only when the event changed.</summary>
    [Fact]
    public async Task AReloadTellsOfAChangedEvent()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        var id = new CalendarEventId("ev_sample_offsite");
        await h.Ui.InvokeAsync(() => h.Source.EventAsync(id, null));
        var before = (await h.ChangesAsync()).Count;
        await h.Ui.InvokeAsync(() => h.Source.EventAsync(id, null, reload: true));
        Assert.Equal(2, h.Script.Count(API.EventGet.Name));
        Assert.Equal(before, (await h.ChangesAsync()).Count);
        h.Script.ETagSuffix = "+";
        var e = await h.Ui.InvokeAsync(() => h.Source.EventAsync(id, null, reload: true));
        Assert.EndsWith("+", e.ETag, StringComparison.Ordinal);
        Assert.Equal(new Cal.SourceChange.EventChanged(id), (await h.ChangesAsync())[^1]);
    }

    /// <summary>A write's event lands in the cache; a delete drops it.</summary>
    [Fact]
    public async Task WritesLandInTheCache()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        var created = await h.Ui.InvokeAsync(() => h.Source.CreateEventAsync(new EventCreateParams
        {
            CalendarId = Cal.SamplePersonal,
            Event = new EventInput { Title = "Dentist", AllDay = true },
        }));
        var id = Assert.IsType<CalendarEventId>(created.Event.EventId);
        Assert.Equal(new Cal.SourceChange.EventChanged(id), (await h.ChangesAsync())[^1]);
        var cached = await h.Ui.InvokeAsync(() => h.Source.EventAsync(id, null));
        Assert.Same(created.Event, cached);
        Assert.Equal(0, h.Script.Count(API.EventGet.Name));
        await h.Ui.InvokeAsync(() => h.Source.DeleteEventAsync(new EventDeleteParams { EventId = id, Scope = EditScope.All, ETag = cached.ETag }));
        Assert.Equal(new Cal.SourceChange.EventChanged(id), (await h.ChangesAsync())[^1]);
        await Assert.ThrowsAsync<RpcException>(() => h.Ui.InvokeAsync(() => h.Source.EventAsync(id, null)));
        Assert.Equal(1, h.Script.Count(API.EventGet.Name));
        // The preferences the daemon stored are the snapshot's.
        Assert.False((await h.SnapshotAsync()).Preferences.ShowDeclined);
        var p = await h.Ui.InvokeAsync(() => h.Source.SetPreferencesAsync(new CalendarPreferencesUpdate { ShowDeclined = true }));
        var snap = await h.SnapshotAsync();
        Assert.True(p.ShowDeclined && snap.Preferences.ShowDeclined);
        Assert.Same(p, snap.Preferences);
    }

    /// <summary>notify.invitationsChanged lists the calendars and the invitations, not the accounts or the preferences.</summary>
    [Fact]
    public async Task InvitationsChangedListsTheInvitations()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        await h.HandleAsync(new DaemonNotification.InvitationsChanged(new InvitationsChangedNotification()));
        Assert.Equal(2, h.Script.Count(API.CalendarList.Name));
        Assert.Equal(2, h.Script.Count(API.InvitationList.Name));
        Assert.Equal(1, h.Script.Count(API.AccountList.Name));
        Assert.Equal(1, h.Script.Count(API.CalendarPreferences.Name));
    }

    /// <summary>A state the snapshot has already, and a load answering the same data, tell no one.</summary>
    [Fact]
    public async Task NothingNewTellsNoOne()
    {
        await using var h = await Harness.StartAsync();
        await h.StartedAsync();
        var before = (await h.ChangesAsync()).Count;
        var state = (await h.SnapshotAsync()).CalendarAccounts[0].State;
        await h.Ui.RunAsync(() => h.Source.Handle(new DaemonNotification.CalendarState(state)));
        await h.Ui.RunAsync(h.Source.Refresh);
        await h.IdleAsync();
        Assert.Equal(before, (await h.ChangesAsync()).Count);
    }

    /// <summary>Stop drops what is on its way; Start loads again.</summary>
    [Fact]
    public async Task StopAndStartAgain()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Lists.Hold(true);
        await h.Ui.RunAsync(h.Source.Start);
        await h.UntilAsync(() => h.Script.Count(API.CalendarList.Name) == 1);
        await h.Ui.RunAsync(h.Source.Stop);
        h.Script.Lists.Hold(false);
        await h.IdleAsync();
        Assert.Equal(Cal.Phase.Loading, (await h.SnapshotAsync()).Phase);
        // Stopped: a notification asks for nothing.
        await h.HandleAsync(Changed());
        Assert.Equal(1, h.Script.Count(API.CalendarList.Name));
        await h.StartedAsync();
        Assert.Equal(Cal.Phase.Ready, (await h.SnapshotAsync()).Phase);
        Assert.Equal(2, h.Script.Count(API.CalendarList.Name));
    }

    /// <summary>A clock that stands still (the controller's "now").</summary>
    private sealed class FixedTime(DateTimeOffset now) : TimeProvider
    {
        public override DateTimeOffset GetUtcNow() => now.ToUniversalTime();
    }

    /// <summary>Holds replies until released.</summary>
    private sealed class Gate
    {
        private readonly Lock gate = new();
        private bool holding;
        private int waiting;
        private TaskCompletionSource released = new(TaskCreationOptions.RunContinuationsAsynchronously);

        public int Waiting
        {
            get
            {
                lock (gate)
                {
                    return waiting;
                }
            }
        }

        public void Hold(bool on)
        {
            TaskCompletionSource? release = null;
            lock (gate)
            {
                if (on && !holding)
                {
                    holding = true;
                    released = new(TaskCreationOptions.RunContinuationsAsynchronously);
                }
                else if (!on)
                {
                    holding = false;
                    release = released;
                }
            }
            release?.TrySetResult();
        }

        public async Task PassAsync()
        {
            Task wait;
            lock (gate)
            {
                if (!holding)
                {
                    return;
                }
                waiting++;
                wait = released.Task;
            }
            await wait.WaitAsync(TimeSpan.FromSeconds(20));
            lock (gate)
            {
                waiting--;
            }
        }
    }

    /// <summary>The daemon's side (Swift <c>CalendarScript</c>): the samples, the calls with their params. Thread-safe.</summary>
    private sealed class Script
    {
        private readonly Lock gate = new();
        private readonly List<(string Method, string Params)> calls = [];
        private readonly Cal.Samples data = Cal.SampleData(Now, F.Prague);
        private int created;

        public Gate Updates { get; } = new();

        public Gate Lists { get; } = new();

        public RpcException? UpdateFailure { get; set; }

        public RpcException? ListFailure { get; set; }

        public CalendarEvent? ConflictEvent { get; set; }

        /// <summary>Appended to every etag event.get answers (an event changed elsewhere).</summary>
        public string ETagSuffix { get; set; } = "";

        public IReadOnlyList<string> Calls
        {
            get
            {
                lock (gate)
                {
                    return [.. calls.Select(c => c.Method)];
                }
            }
        }

        public int Count(string method)
        {
            lock (gate)
            {
                return calls.Count(c => c.Method == method);
            }
        }

        public T? Last<T>(string method)
            where T : class
        {
            lock (gate)
            {
                var i = calls.FindLastIndex(c => c.Method == method);
                return i < 0 ? null : JsonCoding.Decode<T>(calls[i].Params);
            }
        }

        public void Install(FakeDaemon fake)
        {
            fake.On(API.AccountList.Name, p => Answer(API.AccountList.Name, p, () => new AccountListResult { Accounts = data.Accounts }));
            fake.On(API.CalendarList.Name, (FakeDaemon.MethodHandler)(async p =>
            {
                Record(API.CalendarList.Name, p);
                await Lists.PassAsync();
                if (ListFailure is { } f)
                {
                    throw f;
                }
                return JsonCoding.EncodeToString(data.Calendars);
            }));
            fake.On(API.CalendarPreferences.Name, p => Answer(
                API.CalendarPreferences.Name, p, () => new CalendarPreferencesResult { Preferences = data.Preferences }));
            fake.On(API.InvitationList.Name, p => Answer(API.InvitationList.Name, p, () => new InvitationListResult { Invitations = data.Invitations }));
            fake.On(API.EventList.Name, (FakeDaemon.MethodHandler)(async p =>
            {
                Record(API.EventList.Name, p);
                await Lists.PassAsync();
                var q = JsonCoding.Decode<EventListParams>(p);
                var output = data.Occurrences.Where(o => Cal.Overlaps(o, q.From, q.To, F.Prague)).ToList();
                return JsonCoding.EncodeToString(new EventListResult { Occurrences = output, Truncated = false, Complete = true });
            }));
            fake.On(API.EventGet.Name, p => Answer(API.EventGet.Name, p, () =>
            {
                var q = JsonCoding.Decode<EventGetParams>(p);
                var found = data.Events.FirstOrDefault(e => e.EventId == q.EventId) ?? throw Daemon(ErrorCode.EventNotFound, "no");
                return new EventGetResult { Event = found with { ETag = found.ETag + ETagSuffix } };
            }));
            fake.On(API.CalendarUpdate.Name, (FakeDaemon.MethodHandler)(async p =>
            {
                Record(API.CalendarUpdate.Name, p);
                await Updates.PassAsync();
                if (UpdateFailure is { } f)
                {
                    throw f;
                }
                var q = JsonCoding.Decode<CalendarUpdateParams>(p);
                var cal = data.Calendars.Accounts.SelectMany(a => a.Calendars).First(c => c.Id == q.CalendarId);
                return JsonCoding.EncodeToString(new CalendarUpdateResult { Calendar = cal with { Visible = q.Visible ?? cal.Visible } });
            }));
            fake.On(API.EventUpdate.Name, p => Answer<EventUpdateResult>(API.EventUpdate.Name, p, () =>
            {
                if (ConflictEvent is { } e)
                {
                    var raw = JsonCoding.EncodeToString(new ConflictEventData { Event = e });
                    using var doc = JsonDocument.Parse(raw);
                    throw new RpcException(new RpcError { Code = ErrorCode.Conflict, Message = "etag", Data = doc.RootElement });
                }
                throw Daemon(ErrorCode.ReadOnly, "no");
            }));
            fake.On(API.EventCreate.Name, p => Answer(API.EventCreate.Name, p, () =>
            {
                var q = JsonCoding.Decode<EventCreateParams>(p);
                var n = Interlocked.Increment(ref created);
                return new EventCreateResult
                {
                    Event = data.Events[0] with
                    {
                        EventId = new CalendarEventId($"ev_new_{n}"),
                        RecurrenceId = null,
                        CalendarId = q.CalendarId,
                        Title = q.Event.Title,
                        ETag = "\"1\"",
                    },
                    Invitations = new InvitationsSent { Via = InvitationsVia.None, Count = 0 },
                };
            }));
            fake.On(API.EventDelete.Name, p => Answer(
                API.EventDelete.Name, p, () => new EventDeleteResult { Invitations = new InvitationsSent { Via = InvitationsVia.None, Count = 0 } }));
            fake.On(API.CalendarSetPreferences.Name, p => Answer(API.CalendarSetPreferences.Name, p, () =>
            {
                var q = JsonCoding.Decode<CalendarSetPreferencesParams>(p);
                return new CalendarSetPreferencesResult { Preferences = data.Preferences with { ShowDeclined = q.Preferences.ShowDeclined ?? data.Preferences.ShowDeclined } };
            }));
        }

        private void Record(string method, string p)
        {
            lock (gate)
            {
                calls.Add((method, p));
            }
        }

        private string Answer<T>(string method, string p, Func<T> result)
        {
            Record(method, p);
            return JsonCoding.EncodeToString(result());
        }
    }

    private sealed class Harness : IAsyncDisposable
    {
        private readonly List<Cal.SourceChange> changes = [];

        private Harness()
        {
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeDaemon Fake { get; } = new();

        public Script Script { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        public DaemonCalendarSource Source { get; private set; } = null!;

        public static async Task<Harness> StartAsync()
        {
            var h = new Harness();
            h.Script.Install(h.Fake);
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            h.Source = await h.Ui.RunAsync(() =>
            {
                var s = new DaemonCalendarSource(h.Client, F.Prague, pending: h.Pending);
                s.Observe(h.changes.Add);
                return s;
            });
            return h;
        }

        /// <summary>Starts and waits for the snapshot.</summary>
        public async Task StartedAsync()
        {
            await Ui.RunAsync(Source.Start);
            await IdleAsync();
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        /// <summary>Hands the source a notification and waits for what it asks.</summary>
        public async Task HandleAsync(DaemonNotification n)
        {
            await Ui.RunAsync(() => Source.Handle(n));
            await IdleAsync();
        }

        public Task<Cal.Snapshot> SnapshotAsync() => Ui.RunAsync(() => Source.Snapshot);

        public Task<List<Cal.SourceChange>> ChangesAsync() => Ui.RunAsync(() => changes.ToList());

        /// <summary>Waits until <paramref name="condition"/> holds on the UI thread (Swift's waitUntil, for a reply held on purpose).</summary>
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
            await Ui.RunAsync(Source.Dispose);
            Script.Updates.Hold(false);
            Script.Lists.Hold(false);
            try
            {
                await Pending.IdleAsync().WaitAsync(TimeSpan.FromSeconds(10));
                await Fake.IdleAsync().WaitAsync(TimeSpan.FromSeconds(10));
            }
            catch (TimeoutException)
            {
                // Torn down all the same; the test's own asserts said what went wrong.
            }
            Client.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }
    }
}
