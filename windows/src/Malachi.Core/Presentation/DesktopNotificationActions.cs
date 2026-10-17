// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the categories of macos/Sources/MalachiMail/Notifications/NotificationService.swift
// (post: INVITATION, CANCELLATION); GTK: ui/internal/calendar/notifications.go
// (the buttons of InvitationNotification and CancellationNotification).
// Which buttons a new message's toast has (docs/calendar.md §5.9): none
// for mail, Accept and Show for an invitation or update not answered yet,
// Remove from Calendar for a cancellation not processed yet.

namespace Malachi.Core.Presentation;

/// <summary>The buttons of a new message's desktop notification (Swift's notification categories).</summary>
public enum DesktopNotificationActions
{
    /// <summary>A plain message: the toast opens it, nothing else.</summary>
    None,

    /// <summary>INVITATION: Accept (with the card's default calendar) and Show.</summary>
    Invitation,

    /// <summary>CANCELLATION: Remove from Calendar.</summary>
    Cancellation,
}
