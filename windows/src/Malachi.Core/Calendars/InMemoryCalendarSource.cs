// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/InMemoryCalendarSource.swift
// and the InMemoryCalendarSource extension of CalendarSamples.swift
// (samplesRequested, samples()); Swift-first, no GTK counterpart yet.
//
// A calendar source over data held in memory: the sample calendar of
// MALACHI_CALENDAR_SAMPLES (Samples) and the controllers' tests. Reads
// answer from the data (event.list by overlap with the visible calendars
// and the unanswered invitations, event.get by id, a plain substring
// search, the user's free/busy from the occurrences); writes answer
// Cal.SourceError.Preview, unless a test turns EmulatesWrites on: then each
// write is recorded in Writes, fails with the exception a test put in
// Failures (by method name), or changes the data the simplest way (an
// answer sets the response, a delete removes the occurrence, a create adds
// one). The calendars' visibility always works: it only changes what is
// shown. Used on the UI thread only.
//
// Windows: Swift's Write enum is a closed record hierarchy; Holding keeps
// every call waiting on a TaskCompletionSource (Swift's checked
// continuation) until Release; Samples takes the zone (Swift's default
// TimeZone.current is the caller's TimeZoneInfo.Local here).

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Transport;

namespace Malachi.Core.Calendars;

/// <summary>A calendar source over data held in memory: the samples and the tests.</summary>
public sealed class InMemoryCalendarSource : ICalendarDataSource
{
    /// <summary>MALACHI_CALENDAR_SAMPLES: "1" shows the invented sample calendar instead of the daemon's.</summary>
    public const string SamplesVariable = "MALACHI_CALENDAR_SAMPLES";

    // The technical English of the errors the emulated writes answer
    // (RpcError.Message is never shown).
    private const string NoEvent = "no event";
    private const string StaleEtag = "etag";

    private readonly CalendarObservers observers = new();
    private readonly List<TaskCompletionSource> held = [];
    private readonly List<Write> writes = [];
    private readonly List<string> reads = [];
    private int nextEvent = 1;

    /// <summary>A source over <paramref name="data"/>, ready.</summary>
    public InMemoryCalendarSource(Cal.Samples data)
    {
        ArgumentNullException.ThrowIfNull(data);
        Data = data;
        Snapshot = Cal.SampleSnapshot(data);
    }

    /// <summary>An empty calendar.</summary>
    public InMemoryCalendarSource()
        : this(new Cal.Samples())
    {
    }

    /// <summary>MALACHI_CALENDAR_SAMPLES=1, read once (Swift <c>samplesRequested</c>).</summary>
    public static bool SamplesRequested { get; } = Environment.GetEnvironmentVariable(SamplesVariable) == "1";

    /// <inheritdoc/>
    public Cal.Snapshot Snapshot { get; private set; }

    /// <summary>The data the reads answer from.</summary>
    public Cal.Samples Data { get; private set; }

    /// <summary>Writes change the data instead of answering the preview's error.</summary>
    public bool EmulatesWrites { get; set; }

    /// <summary>The writes received, in order (with <see cref="EmulatesWrites"/>).</summary>
    public IReadOnlyList<Write> Writes => writes;

    /// <summary>The reads received: the method names, in order.</summary>
    public IReadOnlyList<string> Reads => reads;

    /// <summary>The exception a method answers, by its name (<c>API.EventRespond.Name</c>…).</summary>
    public Dictionary<string, Exception> Failures { get; } = new(StringComparer.Ordinal);

    /// <summary>What account.discoverCalDAV answers (with <see cref="EmulatesWrites"/>).</summary>
    public AccountDiscoverCalDAVResult DiscoverResult { get; set; } =
        new() { ServerUrl = "", FoundVia = CalDAVFoundVia.Url, NeedsCredentials = true };

    /// <summary>What account.listCalDAVCalendars answers (with <see cref="EmulatesWrites"/>).</summary>
    public AccountListCalDAVCalendarsResult ListResult { get; set; } =
        new() { PrincipalUrl = "", HomeSetUrl = "", Scheduling = CalDAVScheduling.Server };

    /// <summary>The account account.add answers (with <see cref="EmulatesWrites"/>).</summary>
    public AccountId AddedAccount { get; set; } = new("acc_added");

    /// <summary>What calendar.freeBusy answers; null: the user's busy time from the occurrences.</summary>
    public CalendarFreeBusyResult? FreeBusyResult { get; set; }

    /// <summary>What calendar.findFreeTime answers.</summary>
    public CalendarFindFreeTimeResult FindFreeTimeResult { get; set; } = new();

    /// <summary>The invitation card invitation.respond answers (the first sample card with the answer when null).</summary>
    public InvitationCard? RespondedCard { get; set; }

    /// <summary>Holds every call until <see cref="Release"/> (tests of "busy").</summary>
    public bool Holding { get; set; }

    /// <summary>The calls waiting in <see cref="Holding"/>.</summary>
    public int HeldCount => held.Count;

    /// <summary>
    /// Swift <c>samples(now:zone:)</c>: the sample calendar of the week of
    /// <paramref name="now"/> in <paramref name="zone"/>: reads work, writes
    /// answer "The preview cannot do that." (<see cref="Cal.SourceError"/>);
    /// the calendars' checkboxes work, they only change what is shown.
    /// </summary>
    public static InMemoryCalendarSource Samples(DateTimeOffset now, TimeZoneInfo zone) => new(Cal.SampleData(now, zone));

    /// <inheritdoc/>
    public BoardObserverToken Observe(Action<Cal.SourceChange> handler) => observers.Add(handler);

    /// <summary>
    /// Replaces the data, as a source does when its data arrives: the
    /// visibility the user chose stays; tells the observers of the snapshot
    /// and of every range.
    /// </summary>
    public void Replace(Cal.Samples data, Cal.Phase phase = Cal.Phase.Ready)
    {
        ArgumentNullException.ThrowIfNull(data);
        Data = data;
        var s = Cal.SampleSnapshot(data);
        var previous = Snapshot;
        Snapshot = s with
        {
            Phase = phase,
            CalendarAccounts =
            [
                .. s.CalendarAccounts.Select(a => a with
                {
                    Calendars = [.. a.Calendars.Select(c => previous.FindCalendar(c.Id) is { } old ? c with { Visible = old.Visible } : c)],
                }),
            ],
        };
        observers.Notify(new Cal.SourceChange.SnapshotChanged());
        observers.Notify(new Cal.SourceChange.OccurrencesChanged(null, null));
    }

    /// <summary>Changes the snapshot (a state, the count) and tells the observers; a change to the same snapshot tells no one.</summary>
    public void Update(Func<Cal.Snapshot, Cal.Snapshot> change)
    {
        ArgumentNullException.ThrowIfNull(change);
        var s = change(Snapshot);
        if (s == Snapshot)
        {
            return;
        }
        Snapshot = s;
        observers.Notify(new Cal.SourceChange.SnapshotChanged());
    }

    /// <summary>Lets the held calls go on.</summary>
    public void Release()
    {
        Holding = false;
        var h = held.ToList();
        held.Clear();
        foreach (var t in h)
        {
            t.TrySetResult();
        }
    }

    private Task WaitAsync()
    {
        if (!Holding)
        {
            return Task.CompletedTask;
        }
        var t = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        held.Add(t);
        return t.Task;
    }

    /// <inheritdoc/>
    public void Refresh()
    {
    }

    /// <inheritdoc/>
    public void Handle(DaemonNotification notification)
    {
        switch (notification)
        {
            case DaemonNotification.CalendarChanged c:
                observers.Notify(new Cal.SourceChange.OccurrencesChanged(c.Payload.From, c.Payload.To));
                break;
            case DaemonNotification.CalendarState s:
                Update(snap =>
                {
                    var accounts = snap.CalendarAccounts.ToList();
                    var i = accounts.FindIndex(a => a.AccountId == s.State.AccountId);
                    if (i < 0)
                    {
                        return snap;
                    }
                    accounts[i] = accounts[i] with { State = s.State };
                    return snap with { CalendarAccounts = accounts };
                });
                break;
            case DaemonNotification.EventAlert a:
                observers.Notify(new Cal.SourceChange.AlertChanged(a.Payload.Alert));
                break;
        }
    }

    // Reads

    private async Task ReadAsync(string method)
    {
        reads.Add(method);
        await WaitAsync();
        if (Failures.TryGetValue(method, out var f))
        {
            throw f;
        }
    }

    /// <inheritdoc/>
    public EventListResult? CachedOccurrences(Cal.Range range) => null;

    /// <inheritdoc/>
    public async Task<EventListResult> OccurrencesAsync(Cal.Range range)
    {
        ArgumentNullException.ThrowIfNull(range);
        await ReadAsync(API.EventList.Name);
        var visible = Snapshot.VisibleCalendars;
        var output = Data.Occurrences
            .Where(o => (o.CalendarId is not { } c || visible.Contains(c)) && Cal.Overlaps(o, range.From, range.To, range.Zone))
            .ToList();
        return new EventListResult { Occurrences = output, Truncated = false, Complete = true };
    }

    /// <inheritdoc/>
    public async Task<CalendarEvent> EventAsync(CalendarEventId id, DateOrTime? recurrenceId, bool reload = false)
    {
        await ReadAsync(API.EventGet.Name);
        return Data.Events.FirstOrDefault(e => e.EventId == id && (recurrenceId is null || e.RecurrenceId == recurrenceId))
            ?? throw Daemon(ErrorCode.EventNotFound, NoEvent);
    }

    /// <inheritdoc/>
    public async Task<EventSearchResult> SearchAsync(EventSearchParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        await ReadAsync(API.EventSearch.Name);
        var q = parameters.Query.ToLowerInvariant();
        var seen = new HashSet<string>(StringComparer.Ordinal);
        var output = new List<Occurrence>();
        foreach (var o in Data.Occurrences)
        {
            if (!o.Title.ToLowerInvariant().Contains(q, StringComparison.Ordinal)
                && !(o.Location ?? "").ToLowerInvariant().Contains(q, StringComparison.Ordinal))
            {
                continue;
            }
            if (seen.Add(o.EventId?.Value ?? Cal.OccurrenceKey(o)))
            {
                output.Add(o);
            }
        }
        return new EventSearchResult { Occurrences = output, Total = output.Count };
    }

    /// <inheritdoc/>
    public async Task<CalendarFreeBusyResult> FreeBusyAsync(CalendarFreeBusyParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        await ReadAsync(API.CalendarFreeBusy.Name);
        if (FreeBusyResult is { } r)
        {
            return r;
        }
        var busy = new List<BusyInterval>();
        foreach (var o in Data.Occurrences)
        {
            if (o.AllDay || o.ShowAs == ShowAs.Free || o.MyResponse == EventResponse.Declined
                || o.Start is not { } s || o.End is not { } e || s >= parameters.To || e <= parameters.From)
            {
                continue;
            }
            busy.Add(new BusyInterval { Start = s, End = e, ShowAs = o.ShowAs });
        }
        return new CalendarFreeBusyResult
        {
            Me = new Availability { Address = Snapshot.MyAddress(parameters.AccountId), Known = true, Busy = busy },
            Attendees = [.. parameters.Attendees.Select(a => new Availability { Address = a, Known = false })],
        };
    }

    /// <inheritdoc/>
    public async Task<CalendarFindFreeTimeResult> FindFreeTimeAsync(CalendarFindFreeTimeParams parameters)
    {
        await ReadAsync(API.CalendarFindFreeTime.Name);
        return FindFreeTimeResult;
    }

    // Writes

    // Records the write and answers the preview's error, a test's failure or
    // nothing (go on).
    private async Task WriteAsync(Write w, string method)
    {
        if (!EmulatesWrites)
        {
            throw Cal.SourceError.Preview;
        }
        writes.Add(w);
        await WaitAsync();
        if (Failures.TryGetValue(method, out var f))
        {
            throw f;
        }
    }

    /// <inheritdoc/>
    public void SetVisible(bool visible, CalendarId calendar)
    {
        if (Snapshot.FindCalendar(calendar) is not { } c || c.Visible == visible)
        {
            return;
        }
        if (EmulatesWrites)
        {
            writes.Add(new Write.Visible(calendar, visible));
        }
        if (Failures.TryGetValue(API.CalendarUpdate.Name, out var f))
        {
            observers.Notify(new Cal.SourceChange.Failed(Cal.ErrorMessage(f)));
            return;
        }
        Update(s => s with
        {
            CalendarAccounts =
            [
                .. s.CalendarAccounts.Select(a => a with
                {
                    Calendars = [.. a.Calendars.Select(x => x.Id == calendar ? x with { Visible = visible } : x)],
                }),
            ],
        });
        observers.Notify(new Cal.SourceChange.OccurrencesChanged(null, null));
    }

    /// <inheritdoc/>
    public Task SyncAsync(CalendarSyncParams parameters) => WriteAsync(new Write.Sync(parameters), API.CalendarSync.Name);

    /// <inheritdoc/>
    public async Task<CalendarPreferences> SetPreferencesAsync(CalendarPreferencesUpdate update)
    {
        ArgumentNullException.ThrowIfNull(update);
        await WriteAsync(new Write.SetPreferences(update), API.CalendarSetPreferences.Name);
        var p = Data.Preferences;
        p = p with
        {
            WeekStart = update.WeekStart ?? p.WeekStart,
            DefaultDurationMinutes = update.DefaultDurationMinutes ?? p.DefaultDurationMinutes,
            DefaultAlertMinutes = update.DefaultAlertMinutes ?? p.DefaultAlertMinutes,
            ShowDeclined = update.ShowDeclined ?? p.ShowDeclined,
            WorkingHours = update.WorkingHours ?? p.WorkingHours,
        };
        Data = Data with { Preferences = p };
        Update(s => s with { Preferences = p });
        return p;
    }

    /// <inheritdoc/>
    public async Task<EventCreateResult> CreateEventAsync(EventCreateParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        await WriteAsync(new Write.Create(parameters), API.EventCreate.Name);
        var id = new CalendarEventId("ev_created_" + nextEvent.ToString(System.Globalization.CultureInfo.InvariantCulture));
        nextEvent++;
        var input = parameters.Event;
        var n = input.Attendees?.Count ?? 0;
        var e = new CalendarEvent
        {
            EventId = id,
            CalendarId = parameters.CalendarId,
            AccountId = Snapshot.FindCalendar(parameters.CalendarId)?.AccountId ?? new AccountId(""),
            Title = input.Title,
            StartDate = input.StartDate,
            EndDate = input.EndDate,
            AllDay = input.AllDay,
            Location = input.Location,
            Status = EventStatus.Confirmed,
            ShowAs = ShowAs.Busy,
            AttendeeCount = n,
            ETag = "\"1\"",
            Notes = input.Notes,
            AttendeesTruncated = false,
            ResponseRequested = false,
            CanEdit = true,
            CanDelete = true,
            CanRespond = false,
            CanProposeNewTime = false,
            RemoveIsDelete = true,
        };
        Data = Data with { Events = [.. Data.Events, e], Occurrences = [.. Data.Occurrences, Cal.OccurrencePart(e)] };
        observers.Notify(new Cal.SourceChange.OccurrencesChanged(null, null));
        return new EventCreateResult
        {
            Event = e,
            Invitations = new InvitationsSent { Via = n > 0 ? InvitationsVia.Server : InvitationsVia.None, Count = n },
        };
    }

    /// <inheritdoc/>
    public async Task<EventUpdateResult> UpdateEventAsync(EventUpdateParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        await WriteAsync(new Write.Update(parameters), API.EventUpdate.Name);
        var events = Data.Events.ToList();
        var i = events.FindIndex(e => e.EventId == parameters.EventId);
        if (i < 0)
        {
            throw Daemon(ErrorCode.EventNotFound, NoEvent);
        }
        if (!string.Equals(events[i].ETag, parameters.ETag, StringComparison.Ordinal))
        {
            throw Daemon(ErrorCode.Conflict, StaleEtag);
        }
        events[i] = events[i] with
        {
            Title = parameters.Event.Title,
            Location = parameters.Event.Location,
            Notes = parameters.Event.Notes,
            ETag = events[i].ETag + "+",
        };
        Data = Data with { Events = events };
        observers.Notify(new Cal.SourceChange.EventChanged(parameters.EventId));
        return new EventUpdateResult
        {
            Event = events[i],
            DroppedExceptions = 0,
            Invitations = new InvitationsSent { Via = InvitationsVia.None, Count = 0 },
        };
    }

    /// <inheritdoc/>
    public async Task<EventDeleteResult> DeleteEventAsync(EventDeleteParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        await WriteAsync(new Write.Delete(parameters), API.EventDelete.Name);
        bool Gone(Occurrence o) =>
            o.EventId == parameters.EventId && (parameters.Scope != EditScope.This || o.RecurrenceId == parameters.RecurrenceId);
        Data = Data with
        {
            Events = [.. Data.Events.Where(e => !Gone(e))],
            Occurrences = [.. Data.Occurrences.Where(o => !Gone(o))],
        };
        observers.Notify(new Cal.SourceChange.EventChanged(parameters.EventId));
        observers.Notify(new Cal.SourceChange.OccurrencesChanged(null, null));
        return new EventDeleteResult { Invitations = new InvitationsSent { Via = InvitationsVia.None, Count = 0 } };
    }

    /// <inheritdoc/>
    public async Task<EventRespondResult> RespondAsync(EventRespondParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        await WriteAsync(new Write.Respond(parameters), API.EventRespond.Name);
        var events = Data.Events.ToList();
        var i = events.FindIndex(e =>
            e.EventId == parameters.EventId && (parameters.RecurrenceId is null || e.RecurrenceId == parameters.RecurrenceId));
        if (i < 0)
        {
            throw Daemon(ErrorCode.EventNotFound, NoEvent);
        }
        events[i] = events[i] with { MyResponse = parameters.Response, ETag = events[i].ETag + "+" };
        Data = Data with
        {
            Events = events,
            Occurrences = [.. Data.Occurrences.Select(o => o.EventId == parameters.EventId ? o with { MyResponse = parameters.Response } : o)],
        };
        observers.Notify(new Cal.SourceChange.EventChanged(parameters.EventId));
        return new EventRespondResult
        {
            Event = events[i],
            Via = parameters.SendResponse == false ? InvitationsVia.None : InvitationsVia.Server,
        };
    }

    /// <inheritdoc/>
    public Task SnoozeAlertAsync(EventSnoozeAlertParams parameters) =>
        WriteAsync(new Write.Snooze(parameters), API.EventSnoozeAlert.Name);

    /// <inheritdoc/>
    public Task DismissAlertAsync(EventDismissAlertParams parameters) =>
        WriteAsync(new Write.Dismiss(parameters), API.EventDismissAlert.Name);

    /// <inheritdoc/>
    public async Task<InvitationRespondResult> RespondToInvitationAsync(InvitationRespondParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        await WriteAsync(new Write.InvitationRespond(parameters), API.InvitationRespond.Name);
        var card = RespondedCard ?? (Data.Cards.Count > 0 ? Data.Cards[0] : null) ?? new InvitationCard
        {
            Method = InvitationMethod.Request,
            Kind = InvitationKind.Invitation,
            State = InvitationState.Unanswered,
            Title = "",
            PartId = "",
            Uid = "",
            Sequence = 0,
            OrganizerMismatch = false,
            AttendeeCount = 0,
            Conflicts = [],
            Day = new InvitationDay { From = DateTimeOffset.MinValue, To = DateTimeOffset.MinValue },
            Targets = [],
            FixedTarget = false,
            CanRespond = true,
            CanProposeNewTime = false,
            ReplyVia = InvitationsVia.None,
            ReplyApplied = false,
        };
        if (RespondedCard is null)
        {
            card = card with { State = InvitationState.Answered, Response = parameters.Response, CalendarId = parameters.CalendarId };
        }
        return new InvitationRespondResult { Invitation = card, EventId = card.EventId };
    }

    /// <inheritdoc/>
    public async Task<InvitationApplyCancelResult> ApplyCancelAsync(InvitationApplyCancelParams parameters)
    {
        await WriteAsync(new Write.ApplyCancel(parameters), API.InvitationApplyCancel.Name);
        return new InvitationApplyCancelResult { Removed = true };
    }

    /// <inheritdoc/>
    public async Task<AccountDiscoverCalDAVResult> DiscoverCalDAVAsync(AccountDiscoverCalDAVParams parameters)
    {
        await WriteAsync(new Write.Discover(parameters), API.AccountDiscoverCalDAV.Name);
        return DiscoverResult;
    }

    /// <inheritdoc/>
    public async Task<AccountListCalDAVCalendarsResult> ListCalDAVCalendarsAsync(AccountListCalDAVCalendarsParams parameters)
    {
        await WriteAsync(new Write.ListCalDAV(parameters), API.AccountListCalDAVCalendars.Name);
        return ListResult;
    }

    /// <inheritdoc/>
    public async Task<AccountAddResult> AddAccountAsync(AccountAddParams parameters)
    {
        await WriteAsync(new Write.AddAccount(parameters), API.AccountAdd.Name);
        return new AccountAddResult { AccountId = AddedAccount };
    }

    /// <inheritdoc/>
    public async Task UpdateAccountAsync(AccountUpdateParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        await WriteAsync(new Write.UpdateAccount(parameters), API.AccountUpdate.Name);
        if (!Data.Accounts.Any(a => a.Id == parameters.AccountId))
        {
            return;
        }
        Data = Data with
        {
            Accounts = [.. Data.Accounts.Select(a => a.Id == parameters.AccountId ? a with { Config = parameters.Config } : a)],
        };
        Update(s => s with
        {
            Accounts = [.. s.Accounts.Select(a => a.Id == parameters.AccountId ? a with { Config = parameters.Config } : a)],
        });
    }

    /// <inheritdoc/>
    public async Task<AttachmentImportResult> ImportAttachmentAsync(AttachmentImportParams parameters)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        await WriteAsync(new Write.ImportAttachment(parameters), API.AttachmentImport.Name);
        var name = parameters.Filename ?? (parameters.Path is { } path ? Path.GetFileName(path) : "file");
        return new AttachmentImportResult
        {
            Attachment = new DraftAttachment
            {
                Id = "att_" + writes.Count.ToString(System.Globalization.CultureInfo.InvariantCulture),
                Filename = name,
                ContentType = "application/octet-stream",
                Size = 1,
                Inline = false,
            },
        };
    }

    private static RpcException Daemon(ErrorCode code, string message) => new(new RpcError { Code = code, Message = message });

    /// <summary>A write as the source received it (Swift <c>InMemoryCalendarSource.Write</c>).</summary>
    public abstract record Write
    {
        // Only the cases below derive from it.
        private Write()
        {
        }

        /// <summary>calendar.update <c>visible</c>.</summary>
        /// <param name="Calendar">The calendar.</param>
        /// <param name="Value">Shown or not.</param>
        public sealed record Visible(CalendarId Calendar, bool Value) : Write;

        /// <summary>calendar.sync.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record Sync(CalendarSyncParams Parameters) : Write;

        /// <summary>calendar.setPreferences.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record SetPreferences(CalendarPreferencesUpdate Parameters) : Write;

        /// <summary>event.create.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record Create(EventCreateParams Parameters) : Write;

        /// <summary>event.update.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record Update(EventUpdateParams Parameters) : Write;

        /// <summary>event.delete.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record Delete(EventDeleteParams Parameters) : Write;

        /// <summary>event.respond.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record Respond(EventRespondParams Parameters) : Write;

        /// <summary>event.snoozeAlert.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record Snooze(EventSnoozeAlertParams Parameters) : Write;

        /// <summary>event.dismissAlert.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record Dismiss(EventDismissAlertParams Parameters) : Write;

        /// <summary>invitation.respond.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record InvitationRespond(InvitationRespondParams Parameters) : Write;

        /// <summary>invitation.applyCancel.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record ApplyCancel(InvitationApplyCancelParams Parameters) : Write;

        /// <summary>account.discoverCalDAV.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record Discover(AccountDiscoverCalDAVParams Parameters) : Write;

        /// <summary>account.listCalDAVCalendars.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record ListCalDAV(AccountListCalDAVCalendarsParams Parameters) : Write;

        /// <summary>account.add.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record AddAccount(AccountAddParams Parameters) : Write;

        /// <summary>account.update.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record UpdateAccount(AccountUpdateParams Parameters) : Write;

        /// <summary>attachment.import.</summary>
        /// <param name="Parameters">Its params.</param>
        public sealed record ImportAttachment(AttachmentImportParams Parameters) : Write;
    }
}
