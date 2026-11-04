// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: a click on a new-message notification, as the app
// receives it (docs/windows-port.md §10). As GTK's app.open-message
// (window/notify_open.go) and macOS's userInfo, it names the message, which
// the click opens in its own window (ActivationRequest.FromNotification).
// A click on an event's alert toast (docs/calendar.md §5.9) names no
// message; it carries its alert instead (Alert), which the app hands to the
// calendar's alerts. Accept and Remove from Calendar of an invitation's or a
// cancellation's toast name no message either: they carry the invitation's
// (Invitation), which the app answers without opening it.

using Malachi.Core.Api;

namespace Malachi.Platform.Windows.Notifications;

/// <summary>
/// The user clicked a notification: open the message it was about, when the
/// notification said, else show the main window.
/// </summary>
/// <param name="AccountId">The message's account; null when the notification carried none.</param>
/// <param name="MessageId">The message; null when the notification carried none.</param>
public sealed record NotificationActivation(AccountId? AccountId, MessageId? MessageId)
{
    /// <summary>The event's alert of an alert toast's click; null for a new-message toast's.</summary>
    public CalendarAlertActivation? Alert { get; init; }

    /// <summary>The answer of an invitation's or a cancellation's toast button; null for any other click.</summary>
    public InvitationActivation? Invitation { get; init; }
}
