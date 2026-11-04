// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Notifications/NotificationService.swift
// (the target of onAcceptInvitation and onApplyCancel with the action it came
// with); Swift-first, no GTK counterpart yet (docs/calendar.md §5.9). What
// the Accept button of an invitation's toast, or the Remove from Calendar
// button of a cancellation's, carries back (NotificationArguments.ForInvitation):
// the message whose card answers it. The card is not in the toast: the app
// reads it with message.get, as macOS does.

using Malachi.Core.Api;

namespace Malachi.Platform.Windows.Notifications;

/// <summary>A click on Accept or Remove from Calendar of a new message's toast (Swift's target and action).</summary>
/// <param name="Response">What the click asks for.</param>
/// <param name="AccountId">The message's account.</param>
/// <param name="MessageId">The message with the calendar part.</param>
public sealed record InvitationActivation(InvitationNotificationResponse Response, AccountId AccountId, MessageId MessageId);
