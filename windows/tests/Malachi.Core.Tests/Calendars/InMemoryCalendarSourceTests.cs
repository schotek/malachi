// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The rules of macos/Sources/MalachiCore/Calendar/InMemoryCalendarSource.swift
// and CalendarSamples.swift, which Swift reaches only through its controller
// tests (CalendarControllerTests, EventDetailControllerTests, …, ported
// with the controllers). Windows-only suite: the samples' reads, the
// preview's refusal of every write but a calendar's checkbox, the emulated
// writes of the tests, the notifications, Holding and the observers.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Transport;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;
using Write = Malachi.Core.Calendars.InMemoryCalendarSource.Write;

namespace Malachi.Core.Tests.Calendars;

public sealed class InMemoryCalendarSourceTests
{
    private static readonly DateTimeOffset Now = F.At(2026, 10, 14, 12);

    private static InMemoryCalendarSource Samples() => InMemoryCalendarSource.Samples(Now, F.Prague);

    private static Cal.Range Week() => Cal.ViewRange(Cal.View.Week, Now, Cal.Weekday.Monday, F.Prague);

    private static (InMemoryCalendarSource Source, List<Cal.SourceChange> Changes) Observed(InMemoryCalendarSource? s = null)
    {
        s ??= Samples();
        var changes = new List<Cal.SourceChange>();
        s.Observe(changes.Add);
        return (s, changes);
    }

    private static EventInput Input(string title) => new() { Title = title, AllDay = true, StartDate = "2026-10-20", EndDate = "2026-10-21" };

    [Fact]
    public void SamplesAreReady()
    {
        var s = Samples();
        Assert.Equal(Cal.Phase.Ready, s.Snapshot.Phase);
        Assert.Equal(3, s.Snapshot.Accounts.Count);
        Assert.Equal(3, s.Snapshot.Unanswered);
        Assert.Equal(3, s.Snapshot.Invitations.Count);
        Assert.Equal(Cal.SampleSnapshot(s.Data), s.Snapshot);
        Assert.Equal("MALACHI_CALENDAR_SAMPLES", InMemoryCalendarSource.SamplesVariable);
        var empty = new InMemoryCalendarSource();
        Assert.Equal(Cal.Phase.Ready, empty.Snapshot.Phase);
        Assert.Empty(empty.Snapshot.Accounts);
    }

    [Fact]
    public async Task OccurrencesOfTheVisibleCalendars()
    {
        var s = Samples();
        var all = await s.OccurrencesAsync(Week());
        Assert.Equal(s.Data.Occurrences.Count, all.Occurrences.Count);
        Assert.True(all.Complete);
        Assert.False(all.Truncated);
        Assert.Null(s.CachedOccurrences(Week()));
        s.SetVisible(false, Cal.SampleWorkCalendar);
        var some = await s.OccurrencesAsync(Week());
        Assert.DoesNotContain(some.Occurrences, o => o.CalendarId == Cal.SampleWorkCalendar);
        // The invitation that is in no calendar stays.
        Assert.Contains(some.Occurrences, o => o.Invitation is not null);
        // Another week has none.
        var next = Cal.ViewRange(Cal.View.Week, Now.AddDays(14), Cal.Weekday.Monday, F.Prague);
        Assert.Empty((await s.OccurrencesAsync(next)).Occurrences);
        Assert.Equal([API.EventList.Name, API.EventList.Name, API.EventList.Name], s.Reads);
    }

    [Fact]
    public async Task EventsAndSearch()
    {
        var s = Samples();
        var e = await s.EventAsync(new CalendarEventId("ev_sample_planning"), null);
        Assert.Equal("Q4 planning", e.Title);
        var monday = s.Data.Events[0];
        var tuesday = await s.EventAsync(new CalendarEventId("ev_sample_standup"), s.Data.Events[1].RecurrenceId, reload: true);
        Assert.Same(s.Data.Events[1], tuesday);
        Assert.NotSame(monday, tuesday);
        var missing = await Assert.ThrowsAsync<RpcException>(() => s.EventAsync(new CalendarEventId("nope"), null));
        Assert.Equal(new ErrorCode(ErrorCode.EventNotFound), missing.Error.Code);
        // Title or location, case-insensitive, each event once.
        var standups = await s.SearchAsync(new EventSearchParams { Query = "STAND" });
        Assert.Single(standups.Occurrences);
        Assert.Equal(1, standups.Total);
        var room = await s.SearchAsync(new EventSearchParams { Query = "room" });
        Assert.Equal(["Standup", "Q4 planning"], room.Occurrences.Select(o => o.Title));
        var demo = await s.SearchAsync(new EventSearchParams { Query = "vendor" });
        Assert.Single(demo.Occurrences);
    }

    [Fact]
    public async Task FreeBusyOfTheUser()
    {
        var s = Samples();
        var tuesday = Cal.AddDays(Week().From, 1, F.Prague);
        var r = await s.FreeBusyAsync(new CalendarFreeBusyParams
        {
            AccountId = Cal.SampleWork,
            From = tuesday,
            To = Cal.AddDays(tuesday, 1, F.Prague),
            Attendees = ["jana@example.org"],
        });
        Assert.Equal("me@example.org", r.Me.Address);
        Assert.True(r.Me.Known);
        // Standup, Q4 planning, the supplier call and the design review; no
        // all-day or free event.
        Assert.Equal(4, r.Me.Busy.Count);
        Assert.Equal(new ShowAs(ShowAs.Tentative), r.Me.Busy[3].ShowAs);
        var jana = Assert.Single(r.Attendees);
        Assert.Equal("jana@example.org", jana.Address);
        Assert.False(jana.Known);
        // The declined all-hands is not busy time.
        var wednesday = Cal.AddDays(tuesday, 1, F.Prague);
        var w = await s.FreeBusyAsync(new CalendarFreeBusyParams { AccountId = Cal.SampleWork, From = wednesday, To = Cal.AddDays(wednesday, 1, F.Prague) });
        Assert.Equal(2, w.Me.Busy.Count); // the standup and the budget review
        // A test's answer wins.
        var given = new CalendarFreeBusyResult { Me = new Availability { Address = "x", Known = false } };
        s.FreeBusyResult = given;
        Assert.Same(given, await s.FreeBusyAsync(new CalendarFreeBusyParams { AccountId = Cal.SampleWork, From = wednesday, To = wednesday }));
        Assert.Same(s.FindFreeTimeResult, await s.FindFreeTimeAsync(new CalendarFindFreeTimeParams { DurationMinutes = 30, From = wednesday, To = wednesday }));
    }

    [Fact]
    public async Task ThePreviewRefusesEveryWrite()
    {
        var (s, changes) = Observed();
        var p = new EventRespondParams { EventId = "ev_sample_budget", Scope = EditScope.All, ETag = "\"1\"", Response = EventResponse.Accepted };
        Task[] writes =
        [
            s.SyncAsync(new CalendarSyncParams()),
            s.SetPreferencesAsync(new CalendarPreferencesUpdate { ShowDeclined = true }),
            s.CreateEventAsync(new EventCreateParams { CalendarId = Cal.SamplePersonal, Event = Input("x") }),
            s.UpdateEventAsync(new EventUpdateParams { EventId = "ev_sample_brunch", Scope = EditScope.All, ETag = "\"1\"", Event = Input("x") }),
            s.DeleteEventAsync(new EventDeleteParams { EventId = "ev_sample_brunch", Scope = EditScope.All, ETag = "\"1\"" }),
            s.RespondAsync(p),
            s.SnoozeAlertAsync(new EventSnoozeAlertParams { AlertId = "a", Minutes = 5 }),
            s.DismissAlertAsync(new EventDismissAlertParams { AlertId = "a" }),
            s.RespondToInvitationAsync(new InvitationRespondParams { AccountId = Cal.SampleGmail, MessageId = "m", Response = EventResponse.Accepted }),
            s.ApplyCancelAsync(new InvitationApplyCancelParams { AccountId = Cal.SampleGmail, MessageId = "m" }),
            s.DiscoverCalDAVAsync(new AccountDiscoverCalDAVParams { Url = "cloud.example.org" }),
            s.ListCalDAVCalendarsAsync(new AccountListCalDAVCalendarsParams { Config = new AccountConfig { Name = "", Email = "" } }),
            s.AddAccountAsync(new AccountAddParams { Config = new AccountConfig { Name = "", Email = "" } }),
            s.UpdateAccountAsync(new AccountUpdateParams { AccountId = Cal.SampleWork, Config = new AccountConfig { Name = "", Email = "" } }),
            s.ImportAttachmentAsync(new AttachmentImportParams { AccountId = Cal.SampleWork, Path = @"C:\x.pdf" }),
        ];
        foreach (var t in writes)
        {
            var e = await Assert.ThrowsAsync<Cal.SourceError>(() => t);
            Assert.Equal(Cal.SourceErrorKind.Preview, e.Kind);
            Assert.Equal(Cal.Text.PreviewOnly, Cal.ErrorMessage(e));
        }
        Assert.Empty(s.Writes);
        Assert.Empty(changes);
        Assert.Equal(EventResponse.NeedsAction, s.Data.Events.Single(e => e.EventId == p.EventId).MyResponse);
        // The checkbox works: it only changes what is shown.
        s.SetVisible(false, Cal.SampleFamily);
        Assert.False(s.Snapshot.FindCalendar(Cal.SampleFamily)?.Visible);
        Assert.Equal([new Cal.SourceChange.SnapshotChanged(), new Cal.SourceChange.OccurrencesChanged(null, null)], changes);
        Assert.Empty(s.Writes);
        // Again, or an unknown calendar: nothing.
        s.SetVisible(false, Cal.SampleFamily);
        s.SetVisible(true, new CalendarId("nope"));
        Assert.Equal(2, changes.Count);
    }

    [Fact]
    public void VisibilityRefusedSaysWhy()
    {
        var (s, changes) = Observed();
        s.EmulatesWrites = true;
        s.Failures[API.CalendarUpdate.Name] = new RpcException(new RpcError { Code = ErrorCode.CalendarNotFound, Message = "x" });
        s.SetVisible(false, Cal.SampleSport);
        Assert.Equal([new Write.Visible(Cal.SampleSport, false)], s.Writes);
        Assert.Equal([new Cal.SourceChange.Failed("The calendar no longer exists.")], changes);
        Assert.True(s.Snapshot.FindCalendar(Cal.SampleSport)?.Visible);
    }

    [Fact]
    public async Task EmulatedWrites()
    {
        var (s, changes) = Observed();
        s.EmulatesWrites = true;
        // Create.
        var created = await s.CreateEventAsync(new EventCreateParams
        {
            CalendarId = Cal.SampleSport,
            Event = Input("Swim") with { Attendees = [new EventInputAttendee { Address = "a@example.org" }], Notes = "pool" },
        });
        Assert.Equal(new CalendarEventId("ev_created_1"), created.Event.EventId);
        Assert.Equal(Cal.SampleNextcloud, created.Event.AccountId);
        Assert.Equal("pool", created.Event.Notes);
        Assert.Equal(new InvitationsSent { Via = InvitationsVia.Server, Count = 1 }, created.Invitations);
        Assert.Contains(s.Data.Occurrences, o => o.EventId == created.Event.EventId && o.GetType() == typeof(Occurrence));
        Assert.Equal(new Cal.SourceChange.OccurrencesChanged(null, null), changes[^1]);
        // Update: a stale etag is a conflict; the right one changes the event.
        var update = new EventUpdateParams { EventId = "ev_sample_brunch", Scope = EditScope.All, ETag = "\"0\"", Event = Input("Brunch!") };
        var conflict = await Assert.ThrowsAsync<RpcException>(() => s.UpdateEventAsync(update));
        Assert.Equal(new ErrorCode(ErrorCode.Conflict), conflict.Error.Code);
        var updated = await s.UpdateEventAsync(update with { ETag = "\"1\"" });
        Assert.Equal("Brunch!", updated.Event.Title);
        Assert.Equal("\"1\"+", updated.Event.ETag);
        Assert.Equal(new Cal.SourceChange.EventChanged("ev_sample_brunch"), changes[^1]);
        var gone = await Assert.ThrowsAsync<RpcException>(() => s.UpdateEventAsync(update with { EventId = "nope" }));
        Assert.Equal(new ErrorCode(ErrorCode.EventNotFound), gone.Error.Code);
        // Respond sets the answer on the event and its occurrences.
        var respond = new EventRespondParams
        {
            EventId = "ev_sample_budget",
            Scope = EditScope.All,
            ETag = "\"1\"",
            Response = EventResponse.Accepted,
            SendResponse = false,
        };
        var answered = await s.RespondAsync(respond);
        Assert.Equal(EventResponse.Accepted, answered.Event.MyResponse);
        Assert.Equal(new InvitationsVia(InvitationsVia.None), answered.Via);
        Assert.Equal(EventResponse.Accepted, s.Data.Occurrences.Single(o => o.EventId == respond.EventId).MyResponse);
        // Delete one instance of the standup, then the series.
        var tuesday = s.Data.Events[1].RecurrenceId;
        await s.DeleteEventAsync(new EventDeleteParams { EventId = "ev_sample_standup", RecurrenceId = tuesday, Scope = EditScope.This, ETag = "\"1\"" });
        Assert.Equal(4, s.Data.Events.Count(e => e.EventId == new CalendarEventId("ev_sample_standup")));
        Assert.Equal(4, s.Data.Occurrences.Count(o => o.EventId == new CalendarEventId("ev_sample_standup")));
        await s.DeleteEventAsync(new EventDeleteParams { EventId = "ev_sample_standup", Scope = EditScope.All, ETag = "\"1\"" });
        Assert.DoesNotContain(s.Data.Occurrences, o => o.EventId == new CalendarEventId("ev_sample_standup"));
        // Preferences: the members given, in the data and the snapshot.
        var prefs = await s.SetPreferencesAsync(new CalendarPreferencesUpdate { ShowDeclined = true, WeekStart = WeekStart.Sunday });
        Assert.True(prefs.ShowDeclined);
        Assert.Equal(new WeekStart(WeekStart.Sunday), s.Snapshot.Preferences.WeekStart);
        Assert.Equal(60, s.Snapshot.Preferences.DefaultDurationMinutes);
        Assert.Equal(prefs, s.Data.Preferences);
        // The account's configuration.
        var config = s.Snapshot.FindAccount(Cal.SampleWork)!.Config with { Name = "Office" };
        await s.UpdateAccountAsync(new AccountUpdateParams { AccountId = Cal.SampleWork, Config = config });
        Assert.Equal("Office", s.Snapshot.AccountName(Cal.SampleWork));
        Assert.Equal("Office", s.Data.SampleAccountName(Cal.SampleWork));
        // The CalDAV calls, account.add and attachment.import answer what the test set.
        s.DiscoverResult = new AccountDiscoverCalDAVResult { ServerUrl = "https://dav.example.net", FoundVia = CalDAVFoundVia.Srv, NeedsCredentials = true };
        Assert.Same(s.DiscoverResult, await s.DiscoverCalDAVAsync(new AccountDiscoverCalDAVParams { Url = "dav.example.net" }));
        Assert.Same(s.ListResult, await s.ListCalDAVCalendarsAsync(new AccountListCalDAVCalendarsParams { Config = config }));
        Assert.Equal(new AccountId("acc_added"), (await s.AddAccountAsync(new AccountAddParams { Config = config })).AccountId);
        var attachment = await s.ImportAttachmentAsync(new AttachmentImportParams { AccountId = Cal.SampleWork, Path = @"C:\Users\u\agenda.pdf" });
        Assert.Equal("agenda.pdf", attachment.Attachment.Filename);
        Assert.Equal("file", (await s.ImportAttachmentAsync(new AttachmentImportParams { AccountId = Cal.SampleWork })).Attachment.Filename);
        Assert.True((await s.ApplyCancelAsync(new InvitationApplyCancelParams { AccountId = Cal.SampleGmail, MessageId = "m" })).Removed);
        Assert.Equal(
            [
                typeof(Write.Create), typeof(Write.Update), typeof(Write.Update), typeof(Write.Update), typeof(Write.Respond),
                typeof(Write.Delete), typeof(Write.Delete), typeof(Write.SetPreferences), typeof(Write.UpdateAccount),
                typeof(Write.Discover), typeof(Write.ListCalDAV), typeof(Write.AddAccount), typeof(Write.ImportAttachment),
                typeof(Write.ImportAttachment), typeof(Write.ApplyCancel),
            ],
            s.Writes.Select(w => w.GetType()));
    }

    [Fact]
    public async Task InvitationAnswers()
    {
        var s = Samples();
        s.EmulatesWrites = true;
        var p = new InvitationRespondParams
        {
            AccountId = Cal.SampleGmail,
            MessageId = "msg_sample_demo",
            Response = EventResponse.Tentative,
            CalendarId = Cal.SampleFamily,
        };
        var r = await s.RespondToInvitationAsync(p);
        Assert.Equal(new InvitationState(InvitationState.Answered), r.Invitation.State);
        Assert.Equal(EventResponse.Tentative, r.Invitation.Response);
        Assert.Equal(Cal.SampleFamily, r.Invitation.CalendarId);
        var card = s.Data.Cards[1];
        s.RespondedCard = card;
        Assert.Same(card, (await s.RespondToInvitationAsync(p)).Invitation);
        Assert.Equal(card.EventId, (await s.RespondToInvitationAsync(p)).EventId);
        // Without sample cards, a bare card.
        var empty = new InMemoryCalendarSource { EmulatesWrites = true };
        Assert.Equal(new InvitationState(InvitationState.Answered), (await empty.RespondToInvitationAsync(p)).Invitation.State);
    }

    [Fact]
    public async Task FailuresByMethod()
    {
        var s = Samples();
        s.EmulatesWrites = true;
        var refused = new RpcException(new RpcError { Code = ErrorCode.ReadOnly, Message = "x" });
        s.Failures[API.EventRespond.Name] = refused;
        s.Failures[API.EventList.Name] = refused;
        var p = new EventRespondParams { EventId = "ev_sample_budget", Scope = EditScope.All, ETag = "\"1\"", Response = EventResponse.Declined };
        Assert.Same(refused, await Assert.ThrowsAsync<RpcException>(() => s.RespondAsync(p)));
        Assert.Same(refused, await Assert.ThrowsAsync<RpcException>(() => s.OccurrencesAsync(Week())));
        // The write was received, but nothing changed.
        Assert.Equal([new Write.Respond(p)], s.Writes);
        Assert.Equal(EventResponse.NeedsAction, s.Data.Events.Single(e => e.EventId == p.EventId).MyResponse);
    }

    [Fact]
    public async Task HoldingKeepsCallsUntilReleased()
    {
        var s = Samples();
        s.EmulatesWrites = true;
        s.Holding = true;
        var read = s.OccurrencesAsync(Week());
        var write = s.SyncAsync(new CalendarSyncParams { AccountId = Cal.SampleWork });
        Assert.Equal(2, s.HeldCount);
        Assert.False(read.IsCompleted);
        Assert.False(write.IsCompleted);
        Assert.Single(s.Writes);
        s.Release();
        Assert.Equal(0, s.HeldCount);
        Assert.False(s.Holding);
        await write;
        Assert.NotEmpty((await read).Occurrences);
        // The preview refuses at once, without waiting.
        var preview = Samples();
        preview.Holding = true;
        await Assert.ThrowsAsync<Cal.SourceError>(() => preview.SyncAsync(new CalendarSyncParams()));
        Assert.Equal(0, preview.HeldCount);
    }

    [Fact]
    public void Notifications()
    {
        var (s, changes) = Observed();
        var from = F.UtcAt(2026, 10, 16, 0, 0);
        s.Handle(new DaemonNotification.CalendarChanged(new CalendarChangedNotification { From = from }));
        Assert.Equal([new Cal.SourceChange.OccurrencesChanged(from, null)], changes);
        changes.Clear();
        var state = new CalendarSyncState { AccountId = Cal.SampleNextcloud, Status = CalendarSyncStatus.Offline, Progress = -1 };
        s.Handle(new DaemonNotification.CalendarState(state));
        Assert.Same(state, s.Snapshot.FindCalendarAccount(Cal.SampleNextcloud)?.State);
        Assert.Equal([new Cal.SourceChange.SnapshotChanged()], changes);
        // The same state again, or an unknown account: nothing.
        s.Handle(new DaemonNotification.CalendarState(state));
        s.Handle(new DaemonNotification.CalendarState(state with { AccountId = new AccountId("nope") }));
        Assert.Single(changes);
        changes.Clear();
        var alert = new EventAlert
        {
            AlertId = "al",
            State = EventAlertState.Due,
            EventId = "ev_sample_brunch",
            AccountId = Cal.SampleGmail,
            CalendarId = Cal.SampleFamily,
            Title = "Family brunch",
            AllDay = false,
            MinutesBefore = 15,
        };
        s.Handle(new DaemonNotification.EventAlert(new EventAlertNotification { Alert = alert }));
        Assert.Equal([new Cal.SourceChange.AlertChanged(alert)], changes);
        changes.Clear();
        s.Handle(new DaemonNotification.InvitationsChanged(new InvitationsChangedNotification()));
        s.Handle(new DaemonNotification.AccountsChanged());
        s.Refresh();
        Assert.Empty(changes);
    }

    [Fact]
    public void ReplaceKeepsTheUsersVisibility()
    {
        var (s, changes) = Observed();
        s.SetVisible(false, Cal.SampleSport);
        changes.Clear();
        var later = Cal.SampleData(Now.AddDays(7), F.Prague);
        s.Replace(later, Cal.Phase.Unavailable);
        Assert.Same(later, s.Data);
        Assert.Equal(Cal.Phase.Unavailable, s.Snapshot.Phase);
        Assert.False(s.Snapshot.FindCalendar(Cal.SampleSport)?.Visible);
        Assert.True(s.Snapshot.FindCalendar(Cal.SampleFamily)?.Visible);
        Assert.Equal([new Cal.SourceChange.SnapshotChanged(), new Cal.SourceChange.OccurrencesChanged(null, null)], changes);
    }

    [Fact]
    public void UpdateTellsOnlyAChange()
    {
        var (s, changes) = Observed();
        s.Update(snap => snap with { Accounts = [.. snap.Accounts] });
        Assert.Empty(changes);
        s.Update(snap => snap with { Unanswered = 0 });
        Assert.Equal(0, s.Snapshot.Unanswered);
        Assert.Equal([new Cal.SourceChange.SnapshotChanged()], changes);
    }

    [Fact]
    public void ObserversInOrderAndTokens()
    {
        var s = Samples();
        var calls = new List<int>();
        var first = s.Observe(_ => calls.Add(1));
        s.Observe(_ => calls.Add(2));
        s.Handle(new DaemonNotification.CalendarChanged(new CalendarChangedNotification()));
        Assert.Equal([1, 2], calls);
        first.Cancel();
        s.Handle(new DaemonNotification.CalendarChanged(new CalendarChangedNotification()));
        Assert.Equal([1, 2, 2], calls);
    }
}
