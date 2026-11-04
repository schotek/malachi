// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the seam between NotificationPolicy and the platform's
// notifications, which macOS keeps inside NotificationService.swift (post
// and withdraw, over UNUserNotificationCenter) and GTK inside notify.go
// (Application.SendNotification and WithdrawNotification). The Windows
// implementation is the app's NotificationService over
// AppNotificationManager.

using System.Collections.Generic;

namespace Malachi.Core.Presentation;

/// <summary>Shows desktop notifications and takes them back.</summary>
public interface IDesktopNotifier
{
    /// <summary>
    /// Shows <paramref name="notification"/>, silently (the new-mail sound is
    /// the app's own switch, <see cref="INewMailSound"/>). Never throws: a
    /// platform that cannot show it logs why and drops it.
    /// </summary>
    void Show(DesktopNotification notification);

    /// <summary>
    /// Removes the notifications shown with these <see cref="DesktopNotification.Tag"/>s
    /// from the notification centre (notify.go <c>WithdrawNotification</c>;
    /// Swift <c>withdraw</c>); a tag with nothing shown under it is ignored.
    /// Never throws.
    /// </summary>
    void Withdraw(IReadOnlyList<string> tags);
}
