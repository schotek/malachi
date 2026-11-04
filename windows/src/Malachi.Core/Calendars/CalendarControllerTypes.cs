// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/CalendarController.swift
// (the Cal extension at its top: Answer, Sleep, taskSleep, occurrenceOf);
// Swift-first, no GTK counterpart yet (docs/calendar.md §5.3–5.4).
//
// What the calendar's controllers share: the answer to a question they ask
// their view (Cal.Question), the debounce they wait on, and the occurrence
// an unanswered invitation shows as in the Unanswered agenda; and the
// current event a conflict's error data carries (Swift's
// RPCError.conflictEvent of CalendarAPI.swift, which the daemon's source and
// the event detail read).
//
// Windows: Swift's Sleep takes a Duration and swallows its cancellation
// (try? await Task.sleep); here it takes a TimeSpan and the caller's token,
// and it never faults either: it returns after the delay or as soon as the
// token is cancelled, and the caller checks its token (and its generation)
// after the await, as Swift checks Task.isCancelled. The production sleep
// waits on the injected TimeProvider (SleepOn), as BoardAutoTriageScheduler
// waits with Task.Delay(…, time, …); a wait on a FakeTimeProvider must not
// be tracked (ControllerScope.RunDetached), or IdleAsync would wait for it.

using System;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Transport;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>
    /// Swift <c>Cal.Sleep</c>: waits <paramref name="delay"/> (a debounce);
    /// the tests put a clock of their own here. Never faults: it returns
    /// after the delay or once <paramref name="cancellationToken"/> is
    /// cancelled, and the caller checks the token after the await.
    /// </summary>
    /// <param name="delay">How long to wait.</param>
    /// <param name="cancellationToken">Ends the wait early.</param>
    public delegate Task Sleep(TimeSpan delay, CancellationToken cancellationToken);

    /// <summary>The answer to a <see cref="Question"/>: its confirm button, its alternative (when it has one) or Cancel.</summary>
    public enum Answer
    {
        /// <summary>The question's confirm button.</summary>
        Confirm,

        /// <summary>The question's alternative button (when it has one).</summary>
        Alternative,

        /// <summary>Cancel (also an Escape or a closed dialog).</summary>
        Cancel,
    }

    /// <summary>Swift <c>Cal.taskSleep</c>: the sleep of the system's clock.</summary>
    public static Sleep TaskSleep { get; } = SleepOn(TimeProvider.System);

    /// <summary>The sleep of <paramref name="time"/>: <c>Task.Delay</c> on that clock, its cancellation swallowed.</summary>
    public static Sleep SleepOn(TimeProvider time)
    {
        ArgumentNullException.ThrowIfNull(time);
        return async (delay, cancellationToken) =>
        {
            try
            {
                await Task.Delay(delay, time, cancellationToken);
            }
            catch (OperationCanceledException)
            {
                // Swift's try?: a cancelled sleep simply returns.
            }
        };
    }

    /// <summary>
    /// Swift <c>Cal.occurrenceOf</c>: the occurrence of an unanswered
    /// invitation not in a calendar, for the Unanswered agenda: tentative,
    /// not answered, pointing at its message.
    /// </summary>
    public static Occurrence OccurrenceOf(InvitationListItem i)
    {
        ArgumentNullException.ThrowIfNull(i);
        return new Occurrence
        {
            AccountId = i.AccountId,
            Title = i.Summary.Title,
            Start = i.Summary.Start,
            End = i.Summary.End,
            StartDate = i.Summary.StartDate,
            AllDay = i.Summary.AllDay ?? false,
            Status = EventStatus.Confirmed,
            ShowAs = ShowAs.Tentative,
            MyResponse = EventResponse.NeedsAction,
            Invitation = new MessageRef { AccountId = i.AccountId, MessageId = i.MessageId },
        };
    }

    /// <summary>
    /// Swift <c>RPCError.conflictEvent</c>: the current event of a
    /// <c>conflict</c> on an event write (<see cref="ConflictEventData"/>);
    /// null for any other error or code, a draft conflict (no data) or data
    /// of another shape.
    /// </summary>
    public static ConflictEventData? ConflictEventOf(Exception? error)
    {
        if (error is not RpcException { Error: { Code.Value: ErrorCode.Conflict, Data: { ValueKind: JsonValueKind.Object } data } })
        {
            return null;
        }
        try
        {
            return data.Deserialize(ApiJsonContext.Wire.ConflictEventData);
        }
        catch (JsonException)
        {
            return null;
        }
    }
}
