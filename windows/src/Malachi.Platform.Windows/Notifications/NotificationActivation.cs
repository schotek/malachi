// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: a click on a new-message notification, as the app
// receives it (docs/windows-port.md §10). GTK's notification only runs
// app.show (notify.go, with a TODO to target the message); macOS's
// userInfo carries the account and message ids and the click only shows
// the main window too. The ids are carried here for the same later use.

using Malachi.Core.Api;

namespace Malachi.Platform.Windows.Notifications;

/// <summary>
/// The user clicked a notification: show the main window. The message it
/// was about, when the notification said.
/// </summary>
/// <param name="AccountId">The message's account; null when the notification carried none.</param>
/// <param name="MessageId">The message; null when the notification carried none.</param>
public sealed record NotificationActivation(AccountId? AccountId, MessageId? MessageId);
