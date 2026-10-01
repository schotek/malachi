// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: a click on a new-message notification, as the app
// receives it (docs/windows-port.md §10). As GTK's app.open-message
// (window/notify_open.go) and macOS's userInfo, it names the message, which
// the click opens in its own window (ActivationRequest.FromNotification).

using Malachi.Core.Api;

namespace Malachi.Platform.Windows.Notifications;

/// <summary>
/// The user clicked a notification: open the message it was about, when the
/// notification said, else show the main window.
/// </summary>
/// <param name="AccountId">The message's account; null when the notification carried none.</param>
/// <param name="MessageId">The message; null when the notification carried none.</param>
public sealed record NotificationActivation(AccountId? AccountId, MessageId? MessageId);
