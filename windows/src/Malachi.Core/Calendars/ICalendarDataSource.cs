// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarSource.swift
// (CalendarDataSource and its extension); Swift-first, no GTK counterpart yet
// (docs/calendar.md §5.2 "Source seam", "As built (task M3)").
//
// The calendar's data and its writes. Named like Swift's protocol, not
// CalendarSource: the API mirror has CalendarSource already (where an
// account's calendars come from, graph or caldav). Reads and writes return
// tasks that fault with the daemon's RpcException (or Cal.SourceError, the
// preview's); a write's answer also lands in the caches. Every member runs
// on the UI thread, and every observer is called there, in the order it was
// added.
//
// Windows: the calendar's notifications are DaemonNotification cases here
// (CalendarChanged, CalendarState, EventAlert, InvitationsChanged), not a
// separate CalendarNotification as Swift's; the application's fan-out hands
// every notification to Handle, which ignores the others. Swift's async
// throwing methods are Task-returning ones with the Async suffix; event(_:)
// without reload is the optional parameter.

using System;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;

namespace Malachi.Core.Calendars;

/// <summary>The calendar's data and its writes (Swift <c>CalendarDataSource</c>).</summary>
public interface ICalendarDataSource
{
    /// <summary>What the source knows now. The caller does not change it.</summary>
    Cal.Snapshot Snapshot { get; }

    /// <summary>Calls <paramref name="handler"/> after every change; the token removes it.</summary>
    BoardObserverToken Observe(Action<Cal.SourceChange> handler);

    /// <summary>Asks for the snapshot anew.</summary>
    void Refresh();

    /// <summary>
    /// A notification of the daemon (docs/api.md §5): the calendar's four
    /// (<see cref="DaemonNotification.CalendarChanged"/>,
    /// <see cref="DaemonNotification.CalendarState"/>,
    /// <see cref="DaemonNotification.EventAlert"/>,
    /// <see cref="DaemonNotification.InvitationsChanged"/>) are acted on, the
    /// others ignored.
    /// </summary>
    void Handle(DaemonNotification notification);

    // Reads

    /// <summary>
    /// event.list of <paramref name="range"/> (in its zone, with the
    /// unanswered invitations), cached per range until a notification drops it.
    /// </summary>
    Task<EventListResult> OccurrencesAsync(Cal.Range range);

    /// <summary>The cached answer for <paramref name="range"/>, if any.</summary>
    EventListResult? CachedOccurrences(Cal.Range range);

    /// <summary>
    /// event.get, cached by id and recurrence id until a write or a
    /// notification replaces it; <paramref name="reload"/> asks the daemon anyway.
    /// </summary>
    Task<CalendarEvent> EventAsync(CalendarEventId id, DateOrTime? recurrenceId, bool reload = false);

    /// <summary>event.search.</summary>
    Task<EventSearchResult> SearchAsync(EventSearchParams parameters);

    /// <summary>calendar.freeBusy.</summary>
    Task<CalendarFreeBusyResult> FreeBusyAsync(CalendarFreeBusyParams parameters);

    /// <summary>calendar.findFreeTime.</summary>
    Task<CalendarFindFreeTimeResult> FindFreeTimeAsync(CalendarFindFreeTimeParams parameters);

    // Writes

    /// <summary>
    /// calendar.update <c>visible</c>: shown in the snapshot at once, undone
    /// with <see cref="Cal.SourceChange.Failed"/> when the daemon refuses.
    /// </summary>
    void SetVisible(bool visible, CalendarId calendar);

    /// <summary>calendar.sync.</summary>
    Task SyncAsync(CalendarSyncParams parameters);

    /// <summary>calendar.setPreferences; the preferences the daemon stored.</summary>
    Task<CalendarPreferences> SetPreferencesAsync(CalendarPreferencesUpdate update);

    /// <summary>event.create.</summary>
    Task<EventCreateResult> CreateEventAsync(EventCreateParams parameters);

    /// <summary>
    /// event.update. A <c>conflict</c> stores the current version from the
    /// error's data (<see cref="Cal.SourceChange.EventChanged"/>) before the
    /// task faults.
    /// </summary>
    Task<EventUpdateResult> UpdateEventAsync(EventUpdateParams parameters);

    /// <summary>event.delete.</summary>
    Task<EventDeleteResult> DeleteEventAsync(EventDeleteParams parameters);

    /// <summary>event.respond.</summary>
    Task<EventRespondResult> RespondAsync(EventRespondParams parameters);

    /// <summary>event.snoozeAlert.</summary>
    Task SnoozeAlertAsync(EventSnoozeAlertParams parameters);

    /// <summary>event.dismissAlert.</summary>
    Task DismissAlertAsync(EventDismissAlertParams parameters);

    /// <summary>invitation.respond.</summary>
    Task<InvitationRespondResult> RespondToInvitationAsync(InvitationRespondParams parameters);

    /// <summary>invitation.applyCancel.</summary>
    Task<InvitationApplyCancelResult> ApplyCancelAsync(InvitationApplyCancelParams parameters);

    /// <summary>account.discoverCalDAV.</summary>
    Task<AccountDiscoverCalDAVResult> DiscoverCalDAVAsync(AccountDiscoverCalDAVParams parameters);

    /// <summary>account.listCalDAVCalendars.</summary>
    Task<AccountListCalDAVCalendarsResult> ListCalDAVCalendarsAsync(AccountListCalDAVCalendarsParams parameters);

    /// <summary>account.add.</summary>
    Task<AccountAddResult> AddAccountAsync(AccountAddParams parameters);

    /// <summary>account.update.</summary>
    Task UpdateAccountAsync(AccountUpdateParams parameters);

    /// <summary>attachment.import for the event editor.</summary>
    Task<AttachmentImportResult> ImportAttachmentAsync(AttachmentImportParams parameters);
}
