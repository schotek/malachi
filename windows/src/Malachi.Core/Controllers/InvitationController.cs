// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/InvitationController.swift
// (InvitationController: TargetChoice, defaultTarget, mode, pill,
// pillTitle, title, dateTile, timeLine, location, videoHost, peopleLine,
// previousLine, conflictLine, day, showsResponses, showsStateStrip,
// showsProposeNewTime, targetIsPopup, targets, replyNote, stateStrip,
// stateLine, replyCard, setTarget, respond, proposeNewTime, run,
// applyCancel, showInCalendar, joinVideoCall, replace); GTK: none yet, the
// card's rules are ui/internal/calendar/invitation.go
// (Calendars/CalendarInvitation.cs).
//
// The invitation card of one message in Mail (docs/calendar.md §5.7): the
// card of Message.Invitation (message.get), which card it is
// (Cal.CardModeOf with the account's switch), its lines, the target
// calendar ("Add to:"), and its actions: Accept / Tentative / Decline (and
// Decline Without Reply), Change Response, Propose New Time…, Remove from
// Calendar of a cancellation (invitation.applyCancel), Show in Calendar and
// Join Video Call (hand-offs). The answer's card replaces the shown one
// and stays until a newer message.get brings another (Replace); an error
// shows inline.
//
// Windows: Swift's onChange is Changed, onShowInCalendar
// ShowInCalendarRequested, onOpenLink OpenLinkRequested. The writes go
// through PerformPastClose: sent even when the card goes away right after
// the click, as Swift's Task, their answer reaching only an open card.
// Close (Dispose) is Windows' own: Swift's card has no closed flag. Swift's
// == of two cards compares their contents; here the contract's JSON.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers.Infrastructure;

namespace Malachi.Core.Controllers;

/// <summary>
/// The controller of the invitation card of one message (Swift
/// <c>InvitationController</c>).
/// </summary>
/// <remarks>
/// Create it, and call it, on the UI thread; every event is raised there.
/// <see cref="Close"/> when the card goes away.
/// </remarks>
public sealed class InvitationController : IDisposable
{
    private readonly ControllerScope scope;

    /// <summary>The card of a message, on the calling (UI) thread.</summary>
    /// <param name="source">The calendar's data (the answer's writes).</param>
    /// <param name="accountId">The message's account.</param>
    /// <param name="messageId">The message.</param>
    /// <param name="card">The card of <c>Message.Invitation</c>.</param>
    /// <param name="cardOn">The account's "Show the Invitation Card above the Message in Mail".</param>
    /// <param name="env">The dates' zone and writer; the system's zone and the current culture when null.</param>
    /// <param name="pending">Counts the background work; one of its own when null.</param>
    public InvitationController(
        ICalendarDataSource source,
        AccountId accountId,
        MessageId messageId,
        InvitationCard card,
        bool cardOn = true,
        Cal.Env? env = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(source);
        ArgumentNullException.ThrowIfNull(card);
        Source = source;
        AccountId = accountId;
        MessageId = messageId;
        Card = card;
        CardOn = cardOn;
        Env = env ?? Cal.Env.ForCulture(TimeZoneInfo.Local, CultureInfo.CurrentCulture);
        Target = DefaultTarget(card);
        scope = new ControllerScope(pending);
    }

    // Outputs

    /// <summary>What the card shows changed: it reads the controller again (Swift <c>onChange</c>).</summary>
    public event EventHandler? Changed;

    /// <summary>
    /// Show in Calendar: Calendar mode, the Day view of the day with the
    /// event selected (Swift <c>onShowInCalendar</c>).
    /// </summary>
    public event EventHandler<(CalendarEventId? EventId, DateTimeOffset? Day)>? ShowInCalendarRequested;

    /// <summary>Join Video Call: opened through the link confirmation (Swift <c>onOpenLink</c>).</summary>
    public event EventHandler<string>? OpenLinkRequested;

    // State

    /// <summary>The calendar's data.</summary>
    public ICalendarDataSource Source { get; }

    /// <summary>The dates' zone and writer.</summary>
    public Cal.Env Env { get; }

    /// <summary>The message's account.</summary>
    public AccountId AccountId { get; }

    /// <summary>The message.</summary>
    public MessageId MessageId { get; }

    /// <summary>The account's "Show the Invitation Card above the Message in Mail".</summary>
    public bool CardOn { get; }

    /// <summary>The card shown: the message's, or the answer's.</summary>
    public InvitationCard Card { get; private set; }

    /// <summary>The "Add to:" calendar (null: the answer only).</summary>
    public CalendarId? Target { get; private set; }

    /// <summary>An answer is on its way: the buttons wait.</summary>
    public bool Busy { get; private set; }

    /// <summary>The inline error ("" none).</summary>
    public string Error { get; private set; } = "";

    /// <summary>invitation.applyCancel removed the event.</summary>
    public bool CancelApplied { get; private set; }

    /// <summary>The folder invitation.respond moved the message to (After Answering).</summary>
    public FolderId? MovedTo { get; private set; }

    /// <summary>Whether <see cref="Close"/> ran.</summary>
    public bool IsClosed => scope.IsClosed;

    // Derived

    /// <summary>Which card it is.</summary>
    public Cal.CardMode Mode => Cal.CardModeOf(Card, CardOn);

    /// <summary>The pill beside the title.</summary>
    public Cal.InvitationPill Pill => Cal.PillKind(Card);

    /// <summary>The pill's text.</summary>
    public string PillTitle => Cal.PillTitle(Pill);

    /// <summary>The bold title.</summary>
    public string Title => Cal.CardTitle(Card);

    /// <summary>The date tile; null without a start.</summary>
    public Cal.DateTile? DateTile => Cal.DateTileOf(Card, Env);

    /// <summary>The clock line.</summary>
    public string TimeLine => Cal.CardTimeLine(Card, Env);

    /// <summary>The location line.</summary>
    public string Location => Cal.CleanLine(Card.Location);

    /// <summary>The video call's host ("" none); Join Video Call opens it.</summary>
    public string VideoHost => Cal.CleanLine(Card.VideoCall?.Host);

    /// <summary>The people line.</summary>
    public string PeopleLine => Cal.CardPeopleLine(Card);

    /// <summary>What an update changed, a line each ("" none).</summary>
    public string PreviousLine => Cal.PreviousLine(Card, Env);

    /// <summary>The conflict strip ("No conflicts in your calendars" without one).</summary>
    public string ConflictLine
    {
        get
        {
            var s = Source.Snapshot;
            var line = Cal.ConflictLine(Card.Conflicts, s.CalendarName, Env);
            return line.Length == 0 ? Cal.NoConflict : line;
        }
    }

    /// <summary>The day strip's span and busy blocks.</summary>
    public InvitationDay Day => Card.Day;

    /// <summary>Accept / Tentative / Decline are shown.</summary>
    public bool ShowsResponses => Cal.ShowsResponses(Card);

    /// <summary>The state strip of an answered invitation is shown.</summary>
    public bool ShowsStateStrip => Cal.ShowsStateStrip(Card);

    /// <summary>Propose New Time… is shown.</summary>
    public bool ShowsProposeNewTime => Cal.ShowsProposeNewTime(Card);

    /// <summary>"Add to:" is a popup (false: a label with the fixed calendar, or nothing without targets).</summary>
    public bool TargetIsPopup => Card.FixedTarget != true && Card.Targets.Count > 1;

    /// <summary>The "Add to:" popup's choices.</summary>
    public IReadOnlyList<TargetChoice> Targets
    {
        get
        {
            var s = Source.Snapshot;
            return
            [
                .. Card.Targets.Select(t =>
                {
                    var c = s.FindCalendar(t.CalendarId);
                    return new TargetChoice(
                        t.CalendarId,
                        Cal.Text.Dot(c is not null ? s.AccountName(c.AccountId) : "", Cal.CleanLine(c?.Name)),
                        c?.Color ?? "",
                        t.IsDefault == true);
                }),
            ];
        }
    }

    /// <summary>The reply note under the buttons.</summary>
    public IReadOnlyList<string> ReplyNote => Cal.ReplyNote(Card, Source.Snapshot.AccountName(Card.ReplyAccountId ?? AccountId));

    /// <summary>"✓ Accepted · in calendar Work · Calendar".</summary>
    public string StateStrip
    {
        get
        {
            var s = Source.Snapshot;
            var name = Card.CalendarId is { } id
                ? Cal.Text.Dot(s.AccountName(s.FindCalendar(id)?.AccountId ?? AccountId), s.CalendarName(id))
                : "";
            return Cal.StateStrip(Card, name);
        }
    }

    /// <summary>The line of an invitation that cannot be answered ("" none).</summary>
    public string StateLine => Cal.StateLine(Card);

    /// <summary>A reply's one-line card.</summary>
    public string ReplyCard => Cal.ReplyCard(Card);

    // Actions

    /// <summary>The "Add to:" popup; an unknown calendar and a fixed target change nothing.</summary>
    public void SetTarget(CalendarId id)
    {
        if (Card.FixedTarget == true || !Card.Targets.Any(t => t.CalendarId == id) || id == Target)
        {
            return;
        }
        Target = id;
        Notify();
    }

    /// <summary>
    /// Accept / Tentative / Decline, Change Response, Decline Without Reply
    /// (<paramref name="withoutReply"/>).
    /// </summary>
    public void Respond(EventResponse response, bool withoutReply = false)
    {
        scope.VerifyAccess();
        if (Busy || !Card.CanRespond)
        {
            return;
        }
        Run(response, withoutReply, null);
    }

    /// <summary>
    /// Propose New Time…: the answer with the proposed start and end
    /// (Tentative unless <paramref name="response"/> says otherwise).
    /// </summary>
    public void ProposeNewTime(DateTimeOffset start, DateTimeOffset end, EventResponse? response = null)
    {
        scope.VerifyAccess();
        if (Busy || !Card.CanProposeNewTime || end <= start)
        {
            return;
        }
        Run(response ?? EventResponse.Tentative, false, new TimeSlot { Start = start, End = end });
    }

    private void Run(EventResponse response, bool withoutReply, TimeSlot? proposed)
    {
        var s = Source.Snapshot;
        var replyAccount = Card.ReplyAccountId ?? AccountId;
        var mail = withoutReply ? null : Cal.MailTexts(Cal.MailKindOf(response), Cal.OccurrenceOf(Card), s.MyName(replyAccount), Env);
        var calendarId = response == EventResponse.Declined || Card.FixedTarget == true ? null : Target;
        var parameters = new InvitationRespondParams
        {
            AccountId = AccountId,
            MessageId = MessageId,
            Response = response,
            CalendarId = calendarId,
            SendResponse = withoutReply ? false : null,
            ProposedStart = proposed?.Start,
            ProposedEnd = proposed?.End,
            Mail = mail,
        };
        Busy = true;
        Error = "";
        Notify();
        var source = Source;
        scope.PerformPastClose(
            () => source.RespondToInvitationAsync(parameters),
            outcome =>
            {
                if (outcome.TryGetValue(out var r, out var error))
                {
                    Card = r.Invitation;
                    MovedTo = r.MovedTo;
                    if (r.Invitation.CalendarId is { } id)
                    {
                        Target = id;
                    }
                }
                else
                {
                    Error = Cal.ErrorMessage(error);
                }
                Busy = false;
                Notify();
            });
    }

    /// <summary>Remove from Calendar of a cancellation.</summary>
    public void ApplyCancel()
    {
        scope.VerifyAccess();
        if (Busy || Mode != Cal.CardMode.Cancelled || CancelApplied)
        {
            return;
        }
        Busy = true;
        Error = "";
        Notify();
        var source = Source;
        var parameters = new InvitationApplyCancelParams { AccountId = AccountId, MessageId = MessageId };
        scope.PerformPastClose(
            () => source.ApplyCancelAsync(parameters),
            outcome =>
            {
                if (outcome.IsSuccess)
                {
                    CancelApplied = true;
                }
                else
                {
                    Error = Cal.ErrorMessage(outcome.Error);
                }
                Busy = false;
                Notify();
            });
    }

    /// <summary>Show in Calendar: the event and its first day.</summary>
    public void ShowInCalendar() =>
        scope.Raise(ShowInCalendarRequested, this, (Card.EventId, Card.Start ?? Cal.ParseDate(Card.StartDate, Env.Zone)));

    /// <summary>Join Video Call.</summary>
    public void JoinVideoCall()
    {
        if (Card.VideoCall?.Url is not { Length: > 0 } url)
        {
            return;
        }
        scope.Raise(OpenLinkRequested, this, url);
    }

    /// <summary>The card of a newer message.get (the message changed); the same card changes nothing.</summary>
    public void Replace(InvitationCard c)
    {
        ArgumentNullException.ThrowIfNull(c);
        if (JsonCoding.Encode(c).AsSpan().SequenceEqual(JsonCoding.Encode(Card)))
        {
            return;
        }
        Card = c;
        Target = DefaultTarget(c);
        Notify();
    }

    /// <summary>The card went away: late answers are dropped (Windows' own; Swift's card has no closed flag).</summary>
    public void Close() => scope.Close();

    /// <inheritdoc/>
    public void Dispose() => Close();

    // The default target: the card's linked calendar, else the default of
    // the targets, else the first.
    private static CalendarId? DefaultTarget(InvitationCard c)
    {
        if (c.CalendarId is { } id && (c.Targets.Any(t => t.CalendarId == id) || c.FixedTarget == true))
        {
            return id;
        }
        return (c.Targets.FirstOrDefault(t => t.IsDefault == true) ?? (c.Targets.Count > 0 ? c.Targets[0] : null))?.CalendarId;
    }

    // Raises Changed, every handler guarded (ControllerEvents); Swift's card
    // has no closed flag, so it is raised after Close too.
    private void Notify()
    {
        if (Changed is not { } handlers)
        {
            return;
        }
        foreach (var handler in handlers.GetInvocationList())
        {
            scope.Guard(() => ((EventHandler)handler).Invoke(this, EventArgs.Empty));
        }
    }

    /// <summary>A choice of the "Add to:" popup.</summary>
    /// <param name="Id">The calendar.</param>
    /// <param name="Title">"Account · Calendar".</param>
    /// <param name="Color">Its colour (#rrggbb, "" unknown).</param>
    /// <param name="IsDefault">The default target.</param>
    public sealed record TargetChoice(CalendarId Id, string Title, string Color, bool IsDefault);
}
