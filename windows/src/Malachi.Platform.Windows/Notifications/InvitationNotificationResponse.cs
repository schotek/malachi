// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Notifications/NotificationService.swift
// (the actions ACCEPT of INVITATION and REMOVE_FROM_CALENDAR of
// CANCELLATION); Swift-first, no GTK counterpart yet (docs/calendar.md
// §5.9). Show and a click on the toast are no answer: they open the message
// as any new message's toast does.

namespace Malachi.Platform.Windows.Notifications;

/// <summary>A button of an invitation's or a cancellation's toast that answers it (Swift's ACCEPT and REMOVE_FROM_CALENDAR).</summary>
public enum InvitationNotificationResponse
{
    /// <summary>Accept: invitation.respond with the card's default calendar.</summary>
    Accept,

    /// <summary>Remove from Calendar: invitation.applyCancel of a cancellation.</summary>
    RemoveFromCalendar,
}
