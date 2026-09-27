// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the seam between NotificationPolicy and the platform's
// notifications, which macOS keeps inside NotificationService.swift (post,
// over UNUserNotificationCenter) and GTK inside notify.go
// (Application.SendNotification). The Windows implementation is the app's
// NotificationService over AppNotificationManager.

namespace Malachi.Core.Presentation;

/// <summary>Shows desktop notifications.</summary>
public interface IDesktopNotifier
{
    /// <summary>
    /// Shows <paramref name="notification"/>, silently (the new-mail sound is
    /// the app's own switch, <see cref="INewMailSound"/>). Never throws: a
    /// platform that cannot show it logs why and drops it.
    /// </summary>
    void Show(DesktopNotification notification);
}
