// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/DaemonCalendarSource.swift
// (DaemonCalendarSource; its CalendarObservers is CalendarObservers.cs);
// Swift-first, no GTK counterpart yet (docs/calendar.md §4.3–4.7, §5.2).
//
// The calendar from the daemon: account.list, calendar.list,
// calendar.preferences and invitation.list for the snapshot; event.list
// per range and event.get per event, both cached; every write passed
// through with its answer stored.
//
// It subscribes to nothing itself: the application's notification fan-out
// calls Handle with each DaemonNotification (the calendar's four are acted
// on), AccountsChanged on notify.accountsChanged and ConnectionChanged
// when the connection comes and goes; Start loads the first time.
//
// - notify.calendarChanged drops every cached range overlapping its
//   from/to (absent: every range) and the cached events of that time,
//   tells the observers (OccurrencesChanged), and lists the calendars again
//   (and, without calendars named, the preferences: §4.4 says so for
//   calendar.setPreferences).
// - notify.calendarState replaces the account's state in the snapshot.
// - notify.eventAlert goes to the observers as it is (AlertChanged).
// - notify.invitationsChanged lists the calendars (the count) and the
//   unanswered invitations again.
//
// One snapshot load is on its way at a time: asked for again meanwhile it
// runs once more after the answer. A range asked for twice while its
// event.list is on its way waits for that one call. A range dropped while
// its call is on its way is not stored from that answer.
//
// calendar.update visible is optimistic: the snapshot shows the change at
// once (an overlay a calendar.list arriving meanwhile does not undo), the
// call follows, a refusal takes it back and says why (Failed). A write that
// answers conflict stores the event of its data and tells the observers
// (EventChanged) before the error is rethrown.
//
// Windows: Swift's TimeZone.current (event.get's zone, the span of a cached
// event) is the zone passed in, the local one by default, sent as its IANA
// name (Cal.IanaZoneName). The snapshot is reported only when it changed:
// the API's records compare their lists by reference, so a snapshot decoded
// anew is compared by its JSON as well (Swift's Equatable struct compares
// by content). Stop also forgets a snapshot load on its way, so that a
// Start after it loads again (Swift's stop leaves loadInFlight set). The
// loads and the visibility writes run through a ControllerScope (tracked,
// yield-first); a write that is fire-and-forget is sent even when the
// source is disposed right after (PerformPastClose), as Swift's Task is,
// and the awaited writes are never cancelled once asked for. Create it,
// and call it, on the UI thread; the observers are called there.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Platform;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Calendars;

/// <summary>
/// The calendar from the daemon (Swift <c>DaemonCalendarSource</c>).
/// Create it, and call it, on the UI thread.
/// </summary>
public sealed partial class DaemonCalendarSource : ICalendarDataSource, IDisposable
{
    private readonly RpcClient client;
    private readonly TimeZoneInfo zone;
    private readonly ControllerScope scope;
    private readonly ILogger logger;
    private readonly CalendarObservers observers = new();

    // calendar.update visible under way, by calendar.
    private readonly Dictionary<CalendarId, (int Token, bool Visible)> pendingVisible = [];

    // event.list answers by range key, with the range.
    private readonly Dictionary<string, (Cal.Range Range, EventListResult Result)> ranges = new(StringComparer.Ordinal);

    // event.list calls on their way, by range key, with their generation.
    private readonly Dictionary<string, RangeCall> rangeTasks = new(StringComparer.Ordinal);

    // event.get answers by event key.
    private readonly Dictionary<string, CalendarEvent> events = new(StringComparer.Ordinal);

    // The daemon's data as last answered, without the overlays (Swift's base).
    private Cal.Snapshot data = Cal.Snapshot.Empty;
    private int nextToken;
    private int rangeGeneration;
    private bool started;
    private bool loadInFlight;
    private bool loadAgain;

    // What the next load asks for besides the calendars.
    private bool wantAccounts = true;
    private bool wantPreferences = true;
    private bool wantInvitations = true;
    private int connectionGeneration;
    private int writesInFlight;

    /// <summary>A source over <paramref name="client"/>, on the calling (UI) thread, in phase Loading; it asks for nothing before <see cref="Start"/>.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="zone">event.get's zone and the days of a cached event (Swift's <c>TimeZone.current</c>); the local zone when null.</param>
    /// <param name="logger">Receives method names and error classes, never a calendar's text.</param>
    /// <param name="pending">Counts the background work; one of its own when null.</param>
    public DaemonCalendarSource(
        RpcClient client, TimeZoneInfo? zone = null, ILogger<DaemonCalendarSource>? logger = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        this.client = client;
        this.zone = zone ?? TimeZoneInfo.Local;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <inheritdoc/>
    public Cal.Snapshot Snapshot { get; private set; } = Cal.Snapshot.Empty;

    /// <summary>
    /// Nothing is on its way (a load, an event.list, a visibility write):
    /// what the tests wait for.
    /// </summary>
    public bool IsIdle => !loadInFlight && rangeTasks.Count == 0 && writesInFlight == 0;

    /// <inheritdoc/>
    public BoardObserverToken Observe(Action<Cal.SourceChange> handler)
    {
        scope.VerifyAccess();
        return observers.Add(handler);
    }

    // What the application calls

    /// <summary>Loads the snapshot.</summary>
    public void Start()
    {
        scope.VerifyAccess();
        started = true;
        Refresh();
    }

    /// <summary>Stops: no call is made and no answer is taken any more.</summary>
    public void Stop()
    {
        scope.VerifyAccess();
        started = false;
        connectionGeneration++;
        loadInFlight = false;
        loadAgain = false;
        foreach (var t in rangeTasks.Values)
        {
            t.Stop.Cancel();
        }
        rangeTasks.Clear();
    }

    /// <summary>Stops for good: the calls under way are dropped.</summary>
    public void Dispose()
    {
        if (scope.IsClosed)
        {
            return;
        }
        Stop();
        scope.Close();
    }

    /// <summary>
    /// notify.accountsChanged (an account.update, e.g. calendars switched
    /// off in Preferences): the accounts and calendars again at once.
    /// </summary>
    public void AccountsChanged()
    {
        scope.VerifyAccess();
        if (started)
        {
            Refresh();
        }
    }

    /// <summary>
    /// The connection came (everything again, the caches dropped) or went
    /// (the snapshot stays, in phase <see cref="Cal.Phase.Unavailable"/>).
    /// </summary>
    public void ConnectionChanged(bool connected)
    {
        scope.VerifyAccess();
        if (!started)
        {
            return;
        }
        connectionGeneration++;
        loadInFlight = false;
        loadAgain = false;
        if (connected)
        {
            DropRanges(null, null);
            Refresh();
        }
        else
        {
            data = data with { Phase = Cal.Phase.Unavailable };
            Publish();
        }
    }

    /// <inheritdoc/>
    public void Refresh()
    {
        scope.VerifyAccess();
        if (!started)
        {
            return;
        }
        wantAccounts = true;
        wantPreferences = true;
        wantInvitations = true;
        Load();
    }

    /// <inheritdoc/>
    public void Handle(DaemonNotification notification)
    {
        ArgumentNullException.ThrowIfNull(notification);
        scope.VerifyAccess();
        if (!started)
        {
            return;
        }
        switch (notification)
        {
            case DaemonNotification.CalendarChanged c:
                var named = c.Payload.CalendarIds ?? [];
                DropRanges(c.Payload.From, c.Payload.To, named.ToHashSet());
                if (named.Count == 0)
                {
                    wantPreferences = true;
                }
                Load();
                break;
            case DaemonNotification.CalendarState s:
                var accounts = data.CalendarAccounts.ToList();
                var i = accounts.FindIndex(a => a.AccountId == s.State.AccountId);
                if (i < 0 || accounts[i].State == s.State)
                {
                    return;
                }
                accounts[i] = accounts[i] with { State = s.State };
                data = data with { CalendarAccounts = accounts };
                Publish();
                break;
            case DaemonNotification.EventAlert a:
                observers.Notify(new Cal.SourceChange.AlertChanged(a.Payload.Alert));
                break;
            case DaemonNotification.InvitationsChanged:
                wantInvitations = true;
                Load();
                break;
        }
    }

    /// <summary>Why a load failed decides the phase (Swift <c>phase(of:)</c>).</summary>
    public static Cal.Phase PhaseOf(Exception? error) => error switch
    {
        RpcClientException { Error.Kind: ClientErrorKind.NotConnected or ClientErrorKind.Disconnected } => Cal.Phase.Unavailable,
        RpcException { Code.Value: ErrorCode.MethodNotFound or ErrorCode.NotImplemented } => Cal.Phase.Unsupported,
        _ => Cal.Phase.Failed,
    };

    // The snapshot

    private void Load()
    {
        if (!started)
        {
            return;
        }
        if (loadInFlight)
        {
            loadAgain = true;
            return;
        }
        loadInFlight = true;
        var accounts = wantAccounts || data.Accounts.Count == 0;
        var prefs = wantPreferences;
        var invitations = wantInvitations;
        wantAccounts = false;
        wantPreferences = false;
        wantInvitations = false;
        var generation = connectionGeneration;
        scope.Perform(
            ct => LoadAsync(accounts, prefs, invitations, ct),
            outcome =>
            {
                if (generation != connectionGeneration)
                {
                    return;
                }
                loadInFlight = false;
                var loaded = outcome.TryGetValue(out var o, out var error) ? o : new Loaded { Error = error };
                Apply(loaded, accounts, prefs, invitations);
                if (loadAgain)
                {
                    loadAgain = false;
                    Load();
                }
            });
    }

    // The calls of one load, in Swift's order; the first that fails ends it
    // with what arrived before.
    private async Task<Loaded> LoadAsync(bool accounts, bool prefs, bool invitations, CancellationToken ct)
    {
        var o = new Loaded();
        try
        {
            if (accounts)
            {
                o.Accounts = (await client.CallAsync(API.AccountList, new EmptyParams(), ct)).Accounts;
            }
            o.Calendars = await client.CallAsync(API.CalendarList, new CalendarListParams(), ct);
            if (prefs)
            {
                o.Preferences = (await client.CallAsync(API.CalendarPreferences, new EmptyParams(), ct)).Preferences;
            }
            if (invitations)
            {
                var p = new InvitationListParams { State = new InvitationState(InvitationState.Unanswered) };
                o.Invitations = (await client.CallAsync(API.InvitationList, p, ct)).Invitations;
            }
        }
        catch (Exception e) when (e is RpcException or RpcClientException or OperationCanceledException)
        {
            o.Error = e;
        }
        return o;
    }

    private void Apply(Loaded o, bool askedAccounts, bool askedPreferences, bool askedInvitations)
    {
        var d = data;
        if (o.Accounts is { } a)
        {
            d = d with { Accounts = a };
        }
        if (o.Calendars is { } c)
        {
            d = d with { CalendarAccounts = c.Accounts, Unanswered = c.Unanswered };
        }
        if (o.Preferences is { } p)
        {
            d = d with { Preferences = p };
        }
        if (o.Invitations is { } i)
        {
            d = d with { Invitations = i };
        }
        if (o.Error is { } err)
        {
            LogCallFailed("calendar load", err);
            // What was not asked successfully is asked again next time.
            wantAccounts = wantAccounts || (askedAccounts && o.Accounts is null);
            wantPreferences = wantPreferences || (askedPreferences && o.Preferences is null);
            wantInvitations = wantInvitations || (askedInvitations && o.Invitations is null);
            d = d with { Phase = PhaseOf(err) };
        }
        else
        {
            d = d with { Phase = Cal.Phase.Ready };
        }
        data = d;
        Publish();
    }

    // The daemon's data with the overlays; tells the observers when it changed.
    private void Publish()
    {
        var s = data;
        if (pendingVisible.Count > 0)
        {
            s = s with
            {
                CalendarAccounts =
                [
                    .. s.CalendarAccounts.Select(a => a with
                    {
                        Calendars = [.. a.Calendars.Select(c => pendingVisible.TryGetValue(c.Id, out var p) ? c with { Visible = p.Visible } : c)],
                    }),
                ],
            };
        }
        if (SameData(s, Snapshot))
        {
            return;
        }
        Snapshot = s;
        observers.Notify(new Cal.SourceChange.SnapshotChanged());
    }

    // Swift's Equatable snapshot: the records compare their lists by
    // reference, so what Equals cannot tell apart is compared by its JSON.
    private static bool SameData(Cal.Snapshot a, Cal.Snapshot b)
    {
        if (a.Equals(b))
        {
            return true;
        }
        if (a.Phase != b.Phase || a.Unanswered != b.Unanswered)
        {
            return false;
        }
        return string.Equals(Encoded(a), Encoded(b), StringComparison.Ordinal);
    }

    private static string Encoded(Cal.Snapshot s) => string.Join(
        "\n",
        JsonCoding.EncodeToString(new AccountListResult { Accounts = s.Accounts }),
        JsonCoding.EncodeToString(new CalendarListResult { Accounts = s.CalendarAccounts, Unanswered = s.Unanswered }),
        JsonCoding.EncodeToString(new InvitationListResult { Invitations = s.Invitations }),
        JsonCoding.EncodeToString(new CalendarPreferencesResult { Preferences = s.Preferences }));

    // Ranges and events

    // Drops the cached ranges overlapping [from, to) and, when calendars
    // are named, every cached range holding an occurrence of one of them
    // (a calendar switched off, emptied or synchronised again); the
    // observers then hear of every range when one outside the bounds went.
    private void DropRanges(DateTimeOffset? from, DateTimeOffset? to, HashSet<CalendarId>? calendars = null)
    {
        rangeGeneration++;
        var outside = false;
        foreach (var (k, v) in ranges.ToList())
        {
            if (Cal.RangesOverlap(v.Range.From, v.Range.To, from, to))
            {
                ranges.Remove(k);
            }
            else if (calendars is { Count: > 0 } && v.Result.Occurrences.Any(o => o.CalendarId is { } c && calendars.Contains(c)))
            {
                ranges.Remove(k);
                outside = true;
            }
        }
        // A call on its way when anything was dropped is not stored from
        // that answer (rangeGeneration).
        foreach (var (k, e) in events.ToList())
        {
            if (e.CalendarId is { } c && calendars is not null && calendars.Contains(c))
            {
                events.Remove(k);
                continue;
            }
            if ((from is null && to is null) || Cal.Span(e, zone) is not { } span)
            {
                events.Remove(k);
                continue;
            }
            if (Cal.RangesOverlap(span.Start, span.End, from, to))
            {
                events.Remove(k);
            }
        }
        observers.Notify(outside ? new Cal.SourceChange.OccurrencesChanged(null, null) : new Cal.SourceChange.OccurrencesChanged(from, to));
    }

    /// <inheritdoc/>
    public EventListResult? CachedOccurrences(Cal.Range range)
    {
        ArgumentNullException.ThrowIfNull(range);
        return ranges.TryGetValue(Cal.RangeKey(range), out var hit) ? hit.Result : null;
    }

    /// <inheritdoc/>
    public async Task<EventListResult> OccurrencesAsync(Cal.Range range)
    {
        ArgumentNullException.ThrowIfNull(range);
        scope.VerifyAccess();
        var key = Cal.RangeKey(range);
        if (ranges.TryGetValue(key, out var hit))
        {
            return hit.Result;
        }
        if (rangeTasks.TryGetValue(key, out var running))
        {
            return await running.Task;
        }
        var p = new EventListParams
        {
            From = range.From,
            To = range.To,
            TimeZone = new TimeZoneName(Cal.IanaZoneName(range.Zone)),
            IncludeInvitations = true,
        };
        using var stop = CancellationTokenSource.CreateLinkedTokenSource(scope.Lifetime);
        var call = new RangeCall(rangeGeneration, client.CallAsync(API.EventList, p, stop.Token), stop);
        rangeTasks[key] = call;
        try
        {
            var result = await call.Task;
            if (rangeTasks.TryGetValue(key, out var now) && now == call && call.Generation == rangeGeneration)
            {
                ranges[key] = (range, result);
            }
            return result;
        }
        finally
        {
            if (rangeTasks.TryGetValue(key, out var now) && now == call)
            {
                rangeTasks.Remove(key);
            }
        }
    }

    /// <inheritdoc/>
    public async Task<CalendarEvent> EventAsync(CalendarEventId id, DateOrTime? recurrenceId, bool reload = false)
    {
        scope.VerifyAccess();
        var key = Cal.EventKey(id, recurrenceId);
        if (!reload && events.TryGetValue(key, out var cached))
        {
            return cached;
        }
        var p = new EventGetParams { EventId = id, RecurrenceId = recurrenceId, TimeZone = new TimeZoneName(Cal.IanaZoneName(zone)) };
        var e = (await client.CallAsync(API.EventGet, p, scope.Lifetime)).Event;
        var changed = !events.TryGetValue(key, out var old) || !string.Equals(old.ETag, e.ETag, StringComparison.Ordinal);
        events[key] = e;
        if (changed && reload)
        {
            observers.Notify(new Cal.SourceChange.EventChanged(id));
        }
        return e;
    }

    // Stores a write's (or a conflict's) event and tells the observers.
    private void Store(CalendarEvent e, DateOrTime? recurrenceId)
    {
        if (e.EventId is not { } id)
        {
            return;
        }
        events[Cal.EventKey(id, e.RecurrenceId ?? recurrenceId)] = e;
        observers.Notify(new Cal.SourceChange.EventChanged(id));
    }

    private void Conflict(Exception error, DateOrTime? recurrenceId)
    {
        if (Cal.ConflictEventOf(error) is { } c)
        {
            Store(c.Event, recurrenceId);
        }
    }

    /// <inheritdoc/>
    public Task<EventSearchResult> SearchAsync(EventSearchParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        return client.CallAsync(API.EventSearch, parameters, scope.Lifetime);
    }

    /// <inheritdoc/>
    public Task<CalendarFreeBusyResult> FreeBusyAsync(CalendarFreeBusyParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        return client.CallAsync(API.CalendarFreeBusy, parameters, scope.Lifetime);
    }

    /// <inheritdoc/>
    public Task<CalendarFindFreeTimeResult> FindFreeTimeAsync(CalendarFindFreeTimeParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        return client.CallAsync(API.CalendarFindFreeTime, parameters, scope.Lifetime);
    }

    // Writes

    /// <inheritdoc/>
    public void SetVisible(bool visible, CalendarId calendar)
    {
        scope.VerifyAccess();
        if (Snapshot.FindCalendar(calendar) is not { } c || c.Visible == visible)
        {
            return;
        }
        nextToken++;
        var token = nextToken;
        pendingVisible[calendar] = (token, visible);
        Publish();
        writesInFlight++;
        scope.PerformPastClose(
            client, API.CalendarUpdate, new CalendarUpdateParams { CalendarId = calendar, Visible = visible }, outcome =>
            {
                writesInFlight--;
                if (!pendingVisible.TryGetValue(calendar, out var p) || p.Token != token)
                {
                    return;
                }
                pendingVisible.Remove(calendar);
                if (outcome.TryGetValue(out var answer, out var failure))
                {
                    data = data with
                    {
                        CalendarAccounts =
                        [
                            .. data.CalendarAccounts.Select(a => a with
                            {
                                Calendars = [.. a.Calendars.Select(x => x.Id == calendar ? answer.Calendar : x)],
                            }),
                        ],
                    };
                }
                Publish();
                if (failure is not null)
                {
                    LogCallFailed(API.CalendarUpdate.Name, failure);
                    observers.Notify(new Cal.SourceChange.Failed(Cal.ErrorMessage(failure)));
                }
            });
    }

    /// <inheritdoc/>
    public async Task SyncAsync(CalendarSyncParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        await client.CallAsync(API.CalendarSync, parameters, CancellationToken.None);
    }

    /// <inheritdoc/>
    public async Task<CalendarPreferences> SetPreferencesAsync(CalendarPreferencesUpdate update)
    {
        ArgumentNullException.ThrowIfNull(update);
        scope.VerifyAccess();
        var p = (await client.CallAsync(API.CalendarSetPreferences, new CalendarSetPreferencesParams { Preferences = update }, CancellationToken.None))
            .Preferences;
        data = data with { Preferences = p };
        Publish();
        return p;
    }

    /// <inheritdoc/>
    public async Task<EventCreateResult> CreateEventAsync(EventCreateParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        var r = await client.CallAsync(API.EventCreate, parameters, CancellationToken.None);
        Store(r.Event, null);
        return r;
    }

    /// <inheritdoc/>
    public async Task<EventUpdateResult> UpdateEventAsync(EventUpdateParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        try
        {
            var r = await client.CallAsync(API.EventUpdate, parameters, CancellationToken.None);
            Store(r.Event, parameters.RecurrenceId);
            return r;
        }
        catch (RpcException e)
        {
            Conflict(e, parameters.RecurrenceId);
            throw;
        }
    }

    /// <inheritdoc/>
    public async Task<EventDeleteResult> DeleteEventAsync(EventDeleteParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        try
        {
            var r = await client.CallAsync(API.EventDelete, parameters, CancellationToken.None);
            events.Remove(Cal.EventKey(parameters.EventId, parameters.RecurrenceId));
            observers.Notify(new Cal.SourceChange.EventChanged(parameters.EventId));
            return r;
        }
        catch (RpcException e)
        {
            Conflict(e, parameters.RecurrenceId);
            throw;
        }
    }

    /// <inheritdoc/>
    public async Task<EventRespondResult> RespondAsync(EventRespondParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        try
        {
            var r = await client.CallAsync(API.EventRespond, parameters, CancellationToken.None);
            Store(r.Event, parameters.RecurrenceId);
            return r;
        }
        catch (RpcException e)
        {
            Conflict(e, parameters.RecurrenceId);
            throw;
        }
    }

    /// <inheritdoc/>
    public async Task SnoozeAlertAsync(EventSnoozeAlertParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        await client.CallAsync(API.EventSnoozeAlert, parameters, CancellationToken.None);
    }

    /// <inheritdoc/>
    public async Task DismissAlertAsync(EventDismissAlertParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        await client.CallAsync(API.EventDismissAlert, parameters, CancellationToken.None);
    }

    /// <inheritdoc/>
    public Task<InvitationRespondResult> RespondToInvitationAsync(InvitationRespondParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        return client.CallAsync(API.InvitationRespond, parameters, CancellationToken.None);
    }

    /// <inheritdoc/>
    public Task<InvitationApplyCancelResult> ApplyCancelAsync(InvitationApplyCancelParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        return client.CallAsync(API.InvitationApplyCancel, parameters, CancellationToken.None);
    }

    /// <inheritdoc/>
    public Task<AccountDiscoverCalDAVResult> DiscoverCalDAVAsync(AccountDiscoverCalDAVParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        return client.CallAsync(API.AccountDiscoverCalDAV, parameters, scope.Lifetime);
    }

    /// <inheritdoc/>
    public Task<AccountListCalDAVCalendarsResult> ListCalDAVCalendarsAsync(AccountListCalDAVCalendarsParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        return client.CallAsync(API.AccountListCalDAVCalendars, parameters, scope.Lifetime);
    }

    /// <inheritdoc/>
    public async Task<AccountAddResult> AddAccountAsync(AccountAddParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        var r = await client.CallAsync(API.AccountAdd, parameters, RpcTimeouts.Save, CancellationToken.None);
        Refresh();
        return r;
    }

    /// <inheritdoc/>
    public async Task UpdateAccountAsync(AccountUpdateParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        await client.CallAsync(API.AccountUpdate, parameters, RpcTimeouts.Save, CancellationToken.None);
        Refresh();
    }

    /// <inheritdoc/>
    public Task<AttachmentImportResult> ImportAttachmentAsync(AttachmentImportParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        scope.VerifyAccess();
        return client.CallAsync(API.AttachmentImport, parameters, RpcTimeouts.Part, CancellationToken.None);
    }

    private void LogCallFailed(string method, Exception? error)
    {
        if (logger.IsEnabled(LogLevel.Information))
        {
            var (kind, daemonError) = RpcErrorText.Classify(error);
            LogFailed(logger, method, kind, daemonError?.Code.Value ?? 0);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "{Method} failed: {Kind} {Code}")]
    private static partial void LogFailed(ILogger logger, string method, RpcErrorText.FailureKind kind, int code);

    // An event.list on its way: the range generation it was asked in, the
    // call, and what cancels it (Stop).
    private sealed record RangeCall(int Generation, Task<EventListResult> Task, CancellationTokenSource Stop);

    // What one load brought (Swift's Loaded).
    private sealed class Loaded
    {
        public IReadOnlyList<Account>? Accounts { get; set; }

        public CalendarListResult? Calendars { get; set; }

        public CalendarPreferences? Preferences { get; set; }

        public IReadOnlyList<InvitationListItem>? Invitations { get; set; }

        public Exception? Error { get; set; }
    }
}
