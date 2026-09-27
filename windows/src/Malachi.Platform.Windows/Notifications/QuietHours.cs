// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: whether the new-mail sound may play now
// (docs/windows-port.md §10: "skipped in quiet hours"). GTK's sound theme and
// macOS's NSSound have no such check; on Windows a program that plays its
// own sound asks SHQueryUserNotificationState, the state the shell itself
// uses for balloons: only QUNS_ACCEPTS_NOTIFICATIONS lets the sound play.
// The others are the blocked modes (the screen saver, a locked session or
// another user's fast-user-switching session, a full-screen application or
// game, presentation mode) and quiet time. When Windows cannot say, the
// sound plays, as it does everywhere else.

using Windows.Win32;
using Windows.Win32.UI.Shell;

namespace Malachi.Platform.Windows.Notifications;

/// <summary>Whether Windows asks programs not to disturb the user now.</summary>
public static class QuietHours
{
    /// <summary>
    /// True unless the shell says the user accepts notifications
    /// (<c>SHQueryUserNotificationState</c>); false when it cannot say.
    /// </summary>
    public static bool IsQuiet() =>
        PInvoke.SHQueryUserNotificationState(out var state).Succeeded && IsQuiet((int)state);

    /// <summary>
    /// Whether a <c>QUERY_USER_NOTIFICATION_STATE</c> value asks for quiet:
    /// every state but QUNS_ACCEPTS_NOTIFICATIONS (5), unknown ones included.
    /// </summary>
    public static bool IsQuiet(int state) => state != (int)QUERY_USER_NOTIFICATION_STATE.QUNS_ACCEPTS_NOTIFICATIONS;
}
