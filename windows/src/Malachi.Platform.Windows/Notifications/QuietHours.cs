// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: whether the new-mail sound may play now
// (docs/windows-port.md §10). GTK's sound theme and macOS's NSSound have no
// such check; on Windows toasts are held back by Windows itself, but a sound
// the app plays is not, so the app asks two things before it plays:
//   ToastNotificationManager.GetDefault().NotificationMode: anything but
//     Unrestricted (PriorityOnly, AlarmsOnly) is Do Not Disturb, switched
//     on by hand, on a schedule or by its automatic rules (a game, a
//     full-screen app, a duplicated display), which
//     SHQueryUserNotificationState is not documented to reflect. The call
//     works from an unpackaged process.
//   SHQueryUserNotificationState, the state the shell uses for balloons: the
//     screen saver, a locked session or another user's session
//     (QUNS_NOT_PRESENT), a full-screen program (QUNS_BUSY), a full-screen
//     Direct3D game (QUNS_RUNNING_D3D_FULL_SCREEN), presentation mode
//     (QUNS_PRESENTATION_MODE), and quiet time, the first hour after a new
//     user's first sign-in (QUNS_QUIET_TIME). QUNS_APP (a Store app in
//     front) is not quiet: since Windows 10 such apps run in ordinary
//     windows (Settings, Calculator), and a full-screen one is caught by Do
//     Not Disturb's automatic rule for full-screen apps.
// What Windows cannot say (a call that fails, a state it adds later) does
// not keep the sound quiet, as nothing does anywhere else.

using System;
using Windows.Win32;
using Windows.Win32.UI.Shell;
using ToastMode = Windows.UI.Notifications.ToastNotificationMode;

namespace Malachi.Platform.Windows.Notifications;

/// <summary>Whether Windows asks programs not to disturb the user now.</summary>
public static class QuietHours
{
    /// <summary>
    /// True while Do Not Disturb is on, or the shell asks for quiet
    /// (<c>SHQueryUserNotificationState</c>: a presentation, a full-screen
    /// program, the screen saver, a locked session, quiet time); false when
    /// Windows cannot say.
    /// </summary>
    public static bool IsQuiet() => IsQuiet(ShellState(), ToastModeNow());

    /// <summary>
    /// Whether a <c>QUERY_USER_NOTIFICATION_STATE</c> value and a
    /// <c>ToastNotificationMode</c> (null for either when Windows could not
    /// say) ask for quiet.
    /// </summary>
    internal static bool IsQuiet(int? shellState, int? toastMode) =>
        (toastMode is { } mode && mode != (int)ToastMode.Unrestricted) || (shellState is { } state && IsQuietState(state));

    /// <summary>
    /// Whether a <c>QUERY_USER_NOTIFICATION_STATE</c> value asks for quiet:
    /// the blocked states and quiet time, not QUNS_ACCEPTS_NOTIFICATIONS,
    /// QUNS_APP or a value Windows adds later.
    /// </summary>
    internal static bool IsQuietState(int state) => (QUERY_USER_NOTIFICATION_STATE)state is
        QUERY_USER_NOTIFICATION_STATE.QUNS_NOT_PRESENT
        or QUERY_USER_NOTIFICATION_STATE.QUNS_BUSY
        or QUERY_USER_NOTIFICATION_STATE.QUNS_RUNNING_D3D_FULL_SCREEN
        or QUERY_USER_NOTIFICATION_STATE.QUNS_PRESENTATION_MODE
        or QUERY_USER_NOTIFICATION_STATE.QUNS_QUIET_TIME;

    /// <summary>The shell's answer; null when it has none.</summary>
    internal static int? ShellState() =>
        PInvoke.SHQueryUserNotificationState(out var state).Succeeded ? (int)state : null;

    /// <summary>The user's notification mode (Do Not Disturb); null when it cannot be read.</summary>
    internal static int? ToastModeNow()
    {
        try
        {
            return (int)global::Windows.UI.Notifications.ToastNotificationManager.GetDefault().NotificationMode;
        }
#pragma warning disable CA1031 // Whatever the notification platform throws, the answer is "cannot say".
        catch (Exception)
#pragma warning restore CA1031
        {
            return null;
        }
    }
}
