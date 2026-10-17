// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Integration+Calendar.swift
// (wireCalendarNotifications' onAcceptInvitation and onApplyCancel,
// answerNotified); GTK: none yet (docs/calendar.md §5.9).
//
// Accept of a new invitation's toast and Remove from Calendar of a
// cancellation's (Program routes the click here, on the UI thread): the
// toast goes, the message's card is read with message.get and run through an
// InvitationController over the main window's calendar source (started as
// the Calendar mode starts it), which answers with the card's default
// calendar or applies the cancellation. A failure, or a card that cannot do
// it any more (answered elsewhere, not a cancellation any more), is a toast
// in the main window with the card's own words. Show and a click on the toast
// open the message as any new message's toast does (Integration.Notified.cs).

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Transport;
using Malachi.Platform.Windows.Notifications;

namespace Malachi.App.Shell;

/// <summary>The answers of the invitations' notifications.</summary>
public sealed partial class AppState
{
    // The controllers answering a notification's click, kept until their
    // answer came (Swift notifiedAnswers).
    private readonly List<InvitationController> notifiedAnswers = [];

    /// <summary>
    /// Accept or Remove from Calendar of an invitation's or a cancellation's
    /// toast (macOS <c>answerNotified</c>). Nothing past Quit's point of no
    /// return.
    /// </summary>
    public void AnswerInvitation(InvitationActivation invitation)
    {
        ArgumentNullException.ThrowIfNull(invitation);
        if (IsStopping)
        {
            return;
        }
        Integration?.Mailbox.WithdrawNotifications([invitation.MessageId]);
        if (MainWindow is not { } main)
        {
            return;
        }
        main.StartCalendar();
        _ = AnswerInvitationAsync(invitation, CalendarSourceFor(main), main);
    }

    private async Task AnswerInvitationAsync(InvitationActivation invitation, ICalendarDataSource source, MainWindow main)
    {
        InvitationCard card;
        try
        {
            var result = await Client.CallAsync(
                API.MessageGet, new MessageGetParams { AccountId = invitation.AccountId, MessageId = invitation.MessageId });
            if (result.Message.Invitation is not { } read)
            {
                main.Toasts.Show(Cal.InvalidLine);
                return;
            }
            card = read;
        }
        catch (Exception e) when (e is RpcException or RpcClientException or TimeoutException or OperationCanceledException)
        {
            if (!IsStopping)
            {
                main.Toasts.Show(Cal.ErrorMessage(e));
            }
            return;
        }
        if (IsStopping)
        {
            return;
        }
        var c = new InvitationController(source, invitation.AccountId, invitation.MessageId, card, env: CalendarEnv());
        c.Changed += (_, _) =>
        {
            if (c.Busy)
            {
                return;
            }
            if (c.Error.Length > 0)
            {
                main.Toasts.Show(c.Error);
            }
            notifiedAnswers.Remove(c);
            c.Close();
        };
        notifiedAnswers.Add(c);
        if (invitation.Response == InvitationNotificationResponse.RemoveFromCalendar)
        {
            c.ApplyCancel();
        }
        else
        {
            c.Respond(new EventResponse(EventResponse.Accepted));
        }
        if (!c.Busy)
        {
            // The card cannot do it (answered elsewhere, not a cancellation
            // any more): its message says why.
            notifiedAnswers.Remove(c);
            c.Close();
            main.Toasts.Show(c.StateLine.Length == 0 ? Cal.InvalidLine : c.StateLine);
        }
    }
}
