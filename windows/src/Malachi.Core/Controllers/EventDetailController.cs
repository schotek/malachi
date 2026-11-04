// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/EventDetailController.swift
// (EventDetailController: start, stop, detail, shownAttendees,
// showAllAttendees, responseChoices, expandAttendees, load, sourceChanged,
// respond, runRespond, remove, runRemove, edit, showInvitation,
// joinVideoCall, openLink, askAssistant, ask, scope, write; findAnotherTime
// as macos/Sources/MalachiMail/Calendar/EventDetailPopover.swift wires it);
// GTK: none yet, the rows and questions are
// ui/internal/calendar/detail.go (Calendars/CalendarDetail.cs).
//
// One event detail popover (docs/calendar.md §5.5): loads the event
// (event.get, cached by the source), its rows (Cal.DetailOf), and runs the
// flows of its buttons: the answer (with the series question and Decline
// Without Reply), Remove from Calendar / Delete (Cal.RemoveQuestion, else
// the Mail preference confirm-delete and Cal.DeleteQuestion, then the
// series question), Edit (the series question, then the editor), Show
// Invitation in Mail and Join Video Call (hand-offs). A write's error shows
// inline under the footer (Error); a conflict reloads the event and says so.
//
// Windows: Swift's onChange is Changed, onEdit EditRequested,
// onShowInvitation ShowInvitationRequested, onOpenLink OpenLinkRequested,
// onClose CloseRequested and onAsk AskRequested; the questions
// (onQuestion, onScope) are the hooks
// AskQuestion and AskScope, and a hook that throws answers Cancel (its
// failure reported, docs/windows-port.md §7.5). The load goes through the
// scope (Perform: dropped once the popover closed); a flow of a button runs
// in Run, its write sent even when the popover closes meanwhile, as Swift's
// Task is. Swift's convenience init? is ForOccurrence. Swift's
// RPCError.conflictEvent is decoded here (ConflictEventOf). Find Another
// Time is what the macOS popover does with it (EventDetailPopover.wire:
// Edit with the series question, the editor told to run Next Free Slot,
// its findingTime flag), so EditRequested carries FindFreeSlot and Swift's
// onFindAnotherTime, which no macOS view calls, has no counterpart.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text.Json;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Transport;

namespace Malachi.Core.Controllers;

/// <summary>
/// The controller of one event detail popover (Swift
/// <c>EventDetailController</c>): the event and its rows, the answer, Remove
/// / Delete, Edit and the hand-offs. Questions are data the popover presents
/// (<see cref="AskQuestion"/>, <see cref="AskScope"/>).
/// </summary>
/// <remarks>
/// Create it, and call it, on the UI thread; every event is raised there.
/// Subscribe, then call <see cref="Start"/>; <see cref="Stop"/> when the
/// popover closes.
/// </remarks>
public sealed class EventDetailController : IDisposable
{
    private readonly ControllerScope scope;
    private readonly Func<bool> confirmDelete;
    private BoardObserverToken? observer;
    private int generation;

    /// <summary>The popover of one event, on the calling (UI) thread.</summary>
    /// <param name="source">The calendar's data.</param>
    /// <param name="eventId">The event.</param>
    /// <param name="recurrenceId">The occurrence of a series; null for a single event (or the series).</param>
    /// <param name="occurrence">The occurrence the popover was opened for, shown until the event arrives.</param>
    /// <param name="env">The dates' zone and writer; the system's zone and the current culture when null.</param>
    /// <param name="confirmDelete">The Mail preference "Ask before moving to the Trash" (confirm-delete); true when null.</param>
    /// <param name="pending">Counts the background work; one of its own when null.</param>
    public EventDetailController(
        ICalendarDataSource source,
        CalendarEventId eventId,
        DateOrTime? recurrenceId,
        Occurrence? occurrence = null,
        Cal.Env? env = null,
        Func<bool>? confirmDelete = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(source);
        Source = source;
        EventId = eventId;
        RecurrenceId = recurrenceId;
        Occurrence = occurrence;
        Env = env ?? Cal.Env.ForCulture(TimeZoneInfo.Local, CultureInfo.CurrentCulture);
        this.confirmDelete = confirmDelete ?? (static () => true);
        scope = new ControllerScope(pending);
    }

    // Outputs

    /// <summary>What the popover shows changed: it reads the controller again (Swift <c>onChange</c>).</summary>
    public event EventHandler? Changed;

    /// <summary>
    /// Edit, and Find Another Time: the editor for this event and scope
    /// (Swift <c>onEdit</c>); with <c>FindFreeSlot</c> (Find Another Time) it
    /// runs Next Free Slot as it opens (<c>EventEditorController.Start(true)</c>).
    /// </summary>
    public event EventHandler<(CalendarEvent Event, EditScope Scope, bool FindFreeSlot)>? EditRequested;

    /// <summary>Show Invitation in Mail: Mail mode, that message revealed (Swift <c>onShowInvitation</c>).</summary>
    public event EventHandler<MessageRef>? ShowInvitationRequested;

    /// <summary>
    /// Join Video Call, a link of the notes: opened through the link
    /// confirmation with no listed links (Swift <c>onOpenLink</c>).
    /// </summary>
    public event EventHandler<string>? OpenLinkRequested;

    /// <summary>The event is gone (removed here): the popover closes (Swift <c>onClose</c>).</summary>
    public event EventHandler? CloseRequested;

    /// <summary>✦ Ask about This Event: the assistant panel (Swift <c>onAsk</c>).</summary>
    public event EventHandler<CalendarEvent>? AskRequested;

    /// <summary>
    /// Asks a question (Cancel always there; Swift <c>onQuestion</c>). Null,
    /// or a hook that throws, answers Cancel.
    /// </summary>
    public Func<Cal.Question, Task<Cal.Answer>>? AskQuestion { get; set; }

    /// <summary>
    /// Asks which occurrences of a series a change is for (Swift
    /// <c>onScope</c>); null is Cancel. Null, or a hook that throws, cancels.
    /// </summary>
    public Func<Cal.ScopeQuestion, Task<EditScope?>>? AskScope { get; set; }

    // State

    /// <summary>The calendar's data.</summary>
    public ICalendarDataSource Source { get; }

    /// <summary>The dates' zone and writer.</summary>
    public Cal.Env Env { get; }

    /// <summary>The event.</summary>
    public CalendarEventId EventId { get; }

    /// <summary>The occurrence of a series; null for a single event.</summary>
    public DateOrTime? RecurrenceId { get; }

    /// <summary>The occurrence the popover was opened for (shown until the event arrives).</summary>
    public Occurrence? Occurrence { get; private set; }

    /// <summary>The event as event.get answered; null until it did.</summary>
    public CalendarEvent? Event { get; private set; }

    /// <summary>event.get is on its way.</summary>
    public bool Loading { get; private set; }

    /// <summary>A write is on its way: the buttons wait.</summary>
    public bool Busy { get; private set; }

    /// <summary>The inline error under the footer ("" none).</summary>
    public string Error { get; private set; } = "";

    /// <summary>The attendee list beyond <see cref="Cal.MaxAttendeesShown"/> is open.</summary>
    public bool AttendeesExpanded { get; private set; }

    /// <summary>Whether <see cref="Stop"/> ran.</summary>
    public bool IsClosed => scope.IsClosed;

    // Derived

    /// <summary>The popover's rows (null until the event arrived).</summary>
    public Cal.Detail? Detail
    {
        get
        {
            if (Event is not { } e)
            {
                return null;
            }
            var s = Source.Snapshot;
            return Cal.DetailOf(e, s.AccountName(e.AccountId), e.CalendarId is { } c ? s.CalendarName(c) : "", Env);
        }
    }

    /// <summary>The attendee rows to show (collapsed beyond <see cref="Cal.MaxAttendeesShown"/>).</summary>
    public IReadOnlyList<Cal.AttendeeRow> ShownAttendees
    {
        get
        {
            if (Detail is not { } d)
            {
                return [];
            }
            return d.Collapsed && !AttendeesExpanded ? [.. d.Attendees.Take(Cal.MaxAttendeesShown)] : d.Attendees;
        }
    }

    /// <summary>"Show All %d Attendees" ("" when nothing is hidden).</summary>
    public string ShowAllAttendees =>
        Detail is { Collapsed: true } d && !AttendeesExpanded ? Cal.ShowAllAttendees(d.Attendees.Count) : "";

    /// <summary>
    /// The "You: …" popup's choices: Accept, Tentative, Decline (the menu of
    /// Decline adds Decline Without Reply).
    /// </summary>
    public static IReadOnlyList<EventResponse> ResponseChoices => Cal.ResponseChoices;

    /// <summary>
    /// The popover of <paramref name="occurrence"/> (Swift's convenience
    /// <c>init?</c>); null for one without an event (an invitation not in a
    /// calendar: its card is in Mail).
    /// </summary>
    public static EventDetailController? ForOccurrence(
        ICalendarDataSource source, Occurrence occurrence, Cal.Env? env = null, Func<bool>? confirmDelete = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(occurrence);
        if (occurrence.EventId is not { } id || string.IsNullOrEmpty(id.Value))
        {
            return null;
        }
        return new EventDetailController(source, id, occurrence.RecurrenceId, occurrence, env, confirmDelete, pending);
    }

    /// <summary>Follows the source and loads the event.</summary>
    public void Start()
    {
        scope.VerifyAccess();
        if (observer is not null)
        {
            return;
        }
        observer = Source.Observe(SourceChanged);
        Load();
    }

    /// <summary>The popover closed: nothing is taken any more.</summary>
    public void Stop()
    {
        scope.Close();
        observer?.Cancel();
        observer = null;
        generation++;
    }

    /// <inheritdoc/>
    public void Dispose() => Stop();

    /// <summary>Opens the attendee list beyond <see cref="Cal.MaxAttendeesShown"/>.</summary>
    public void ExpandAttendees()
    {
        AttendeesExpanded = true;
        Notify();
    }

    // Loading

    /// <summary>event.get (<paramref name="reload"/>: ask the daemon, not the cache).</summary>
    public void Load(bool reload = false)
    {
        scope.VerifyAccess();
        var g = ++generation;
        Loading = true;
        Notify();
        var source = Source;
        var id = EventId;
        var rid = RecurrenceId;
        scope.Perform(
            ct =>
            {
                ct.ThrowIfCancellationRequested();
                return source.EventAsync(id, rid, reload);
            },
            outcome =>
            {
                if (g != generation)
                {
                    return;
                }
                Loading = false;
                if (outcome.TryGetValue(out var e, out var error))
                {
                    Event = e;
                    Occurrence = Cal.OccurrencePart(e);
                }
                else
                {
                    Error = Cal.ErrorMessage(error);
                }
                Notify();
            });
    }

    private void SourceChanged(Cal.SourceChange c)
    {
        if (IsClosed)
        {
            return;
        }
        switch (c)
        {
            case Cal.SourceChange.EventChanged ev when ev.Id == EventId:
                Load();
                break;
            case Cal.SourceChange.OccurrencesChanged o:
                if ((Occurrence ?? (Event is { } e ? Cal.OccurrencePart(e) : null)) is { } occ
                    && Cal.Span(occ, Env.Zone) is { } span
                    && !Cal.RangesOverlap(span.Start, span.End, o.From, o.To))
                {
                    return;
                }
                Load();
                break;
        }
    }

    // Respond

    /// <summary>
    /// The "You: …" popup: answers with <paramref name="response"/>; with a
    /// series asks This Event / All Events first. <paramref name="withoutReply"/>
    /// is Decline Without Reply (<c>sendResponse: false</c>).
    /// </summary>
    public void Respond(EventResponse response, bool withoutReply = false)
    {
        scope.VerifyAccess();
        if (Event is not { } e || !Cal.CanRespond(e) || Busy)
        {
            return;
        }
        scope.Run(_ => RunRespondAsync(e, response, withoutReply));
    }

    private async Task RunRespondAsync(CalendarEvent e, EventResponse response, bool withoutReply)
    {
        if (await ScopeForAsync(Cal.ScopeAction.Respond, e) is not { } editScope)
        {
            return;
        }
        var s = Source.Snapshot;
        var mail = withoutReply ? null : Cal.MailTexts(Cal.MailKindOf(response), e, s.MyName(e.AccountId), Env);
        var parameters = new EventRespondParams
        {
            EventId = EventId,
            RecurrenceId = e.RecurrenceId ?? RecurrenceId,
            Scope = editScope,
            ETag = e.ETag,
            Response = response,
            SendResponse = withoutReply ? false : null,
            Mail = mail,
        };
        await WriteAsync(async () =>
        {
            var r = await Source.RespondAsync(parameters);
            Event = r.Event;
        });
    }

    // Remove

    /// <summary>Remove from Calendar / Delete: the questions, then event.delete.</summary>
    public void Remove()
    {
        scope.VerifyAccess();
        if (Event is not { } e || !e.CanDelete || Busy)
        {
            return;
        }
        scope.Run(_ => RunRemoveAsync(e));
    }

    private async Task RunRemoveAsync(CalendarEvent e)
    {
        bool? notify = null;
        if (Cal.RemoveQuestion(e) is { } q)
        {
            switch (await AskAsync(q))
            {
                case Cal.Answer.Confirm:
                    notify = true;
                    break;
                case Cal.Answer.Alternative:
                    notify = false;
                    break;
                default:
                    return;
            }
        }
        else if (confirmDelete())
        {
            if (await AskAsync(Cal.DeleteQuestion(e)) != Cal.Answer.Confirm)
            {
                return;
            }
        }
        if (await ScopeForAsync(Cal.ScopeAction.Delete, e) is not { } editScope)
        {
            return;
        }
        var s = Source.Snapshot;
        MailText? mail = null;
        if (notify == true)
        {
            var kind = e.RemoveIsDelete ? Cal.MailKind.Cancel : Cal.MailKind.Declined;
            mail = Cal.MailTexts(kind, e, s.MyName(e.AccountId), Env);
        }
        var parameters = new EventDeleteParams
        {
            EventId = EventId,
            RecurrenceId = e.RecurrenceId ?? RecurrenceId,
            Scope = editScope,
            ETag = e.ETag,
            Notify = notify,
            Mail = mail,
        };
        if (await WriteAsync(() => Source.DeleteEventAsync(parameters)))
        {
            RaiseEmpty(CloseRequested, always: true);
        }
    }

    // Hand-offs

    /// <summary>Edit: with a series the scope first, then the editor.</summary>
    public void Edit() => RunEdit(findFreeSlot: false);

    /// <summary>Show Invitation in Mail: the event's first invitation message.</summary>
    public void ShowInvitation()
    {
        if (Event?.InvitationMessages is not { Count: > 0 } messages)
        {
            return;
        }
        scope.Raise(ShowInvitationRequested, this, messages[0]);
    }

    /// <summary>Join Video Call.</summary>
    public void JoinVideoCall()
    {
        if (Event?.VideoCallLink?.Url is not { Length: > 0 } url)
        {
            return;
        }
        scope.Raise(OpenLinkRequested, this, url);
    }

    /// <summary>A link of the notes (<see cref="Cal.Detail.Links"/>); any other URL is ignored.</summary>
    public void OpenLink(string url)
    {
        if (Event?.Links?.Contains(url, StringComparer.Ordinal) != true)
        {
            return;
        }
        scope.Raise(OpenLinkRequested, this, url);
    }

    /// <summary>
    /// ✦ Find Another Time (offered only for an editable event): Edit, with
    /// the series question first, and the editor runs Next Free Slot as it
    /// opens (the macOS popover's flow).
    /// </summary>
    public void FindAnotherTime() => RunEdit(findFreeSlot: true);

    /// <summary>✦ Ask about This Event.</summary>
    public void AskAssistant()
    {
        if (Event is not { } e)
        {
            return;
        }
        scope.Raise(AskRequested, this, e);
    }

    // Helpers

    private void RunEdit(bool findFreeSlot)
    {
        scope.VerifyAccess();
        if (Event is not { } e || !e.CanEdit || Busy)
        {
            return;
        }
        scope.Run(async _ =>
        {
            if (await ScopeForAsync(Cal.ScopeAction.Edit, e) is not { } editScope)
            {
                return;
            }
            scope.Raise(EditRequested, this, (e, editScope, findFreeSlot));
        });
    }

    // The question's answer; Cancel without a hook or when it fails.
    private async Task<Cal.Answer> AskAsync(Cal.Question q)
    {
        if (AskQuestion is not { } ask)
        {
            return Cal.Answer.Cancel;
        }
        try
        {
            return await ask(q);
        }
#pragma warning disable CA1031 // A failed question is a Cancel, and reported.
        catch (Exception error)
#pragma warning restore CA1031
        {
            scope.Pending.Report(error);
            return Cal.Answer.Cancel;
        }
    }

    // The scope of a write: all for a single event, else the series
    // question's answer (null: cancelled).
    private async Task<EditScope?> ScopeForAsync(Cal.ScopeAction action, CalendarEvent e)
    {
        if (e.Recurring != true && e.Recurrence is null)
        {
            return EditScope.All;
        }
        var q = Cal.ScopeQuestionFor(action, e.Recurrence?.Complex ?? false);
        if (q.Choices.Count == 1)
        {
            return q.Choices[0].Scope;
        }
        if (AskScope is not { } ask)
        {
            return null;
        }
        try
        {
            return await ask(q);
        }
#pragma warning disable CA1031 // A failed question is a Cancel, and reported.
        catch (Exception error)
#pragma warning restore CA1031
        {
            scope.Pending.Report(error);
            return null;
        }
    }

    // Runs a write with the buttons waiting; its error goes inline (a
    // conflict reloads the event the source stored). True when it went
    // through.
    private async Task<bool> WriteAsync(Func<Task> body)
    {
        Busy = true;
        Error = "";
        Notify();
        try
        {
            await body();
            return true;
        }
#pragma warning disable CA1031 // Every failure of a write is its inline error, as Swift's catch.
        catch (Exception error)
#pragma warning restore CA1031
        {
            if (IsClosed)
            {
                return false;
            }
            Error = Cal.ErrorMessage(error);
            if (error is RpcException { Code.Value: ErrorCode.Conflict } rpc)
            {
                if (ConflictEventOf(rpc) is { } c)
                {
                    Event = c.Event;
                }
                else
                {
                    Load(reload: true);
                }
            }
            return false;
        }
        finally
        {
            Busy = false;
            Notify();
        }
    }

    /// <summary>
    /// Swift's <c>RPCError.conflictEvent</c>: the current event a
    /// <c>conflict</c> of an event write carries in its data; null without
    /// one or with data of another shape.
    /// </summary>
    private static ConflictEventData? ConflictEventOf(RpcException e)
    {
        if (e.Code != ErrorCode.Conflict || e.ErrorData is not { ValueKind: JsonValueKind.Object } data)
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

    private void Notify() => RaiseEmpty(Changed);

    // Raises a parameterless event, every handler guarded (ControllerEvents);
    // nothing once closed unless always (Swift's onClose has no guard).
    private void RaiseEmpty(EventHandler? handlers, bool always = false)
    {
        if ((IsClosed && !always) || handlers is null)
        {
            return;
        }
        foreach (var handler in handlers.GetInvocationList())
        {
            scope.Guard(() => ((EventHandler)handler).Invoke(this, EventArgs.Empty));
        }
    }
}
