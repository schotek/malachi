// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file, a stub of the shell (phase E wave 1, E1): the entry
// points through which the shell hands the platform services of
// docs/windows-port.md §10 their moments. The platform-services package
// (E7: notifications and sound, the notification-area icon, launch at
// login, the mailto: registration) owns this file and replaces the bodies;
// the shell calls exactly these, in this order:
//
// 1. InitializeEarly, in Main before AppInstance.FindOrRegisterForKey:
//    AppNotificationManager's NotificationInvoked handler first, then
//    Register() (a toast's click is delivered through COM, and without a
//    handler the class is registered single-use). A click while the app
//    runs arrives on a worker thread; hand its argument to invoked, which
//    shows the main window. A cold start by a click is the shell's (the
//    activation kind AppNotification).
// 2. Start, on the UI thread once the application's objects, the main
//    window and the Integration exist, before the connection starts.
// 3. NewMessage, for every notify.newMessage, before the list gets it
//    (notify.go's order).
// 4. MainWindowVisibilityChanged, whenever the main window is shown or
//    hidden (Run in Background: the tray icon comes and goes).
// 5. Stop, on the UI thread at Quit's point of no return (the icon goes).
// 6. Shutdown, in Main once Application.Start has returned (Unregister()).

using System;
using Malachi.App.Shell;
using Malachi.Core.Api;

namespace Malachi.App.Platform;

/// <summary>The platform services' entry points (a stub until E7 replaces it).</summary>
internal static class PlatformServices
{
    /// <summary>Main, before the single instance: the notification registration.</summary>
    /// <param name="invoked">Called, on any thread, with the argument of a notification clicked while the app runs.</param>
    public static void InitializeEarly(Action<string> invoked)
    {
        ArgumentNullException.ThrowIfNull(invoked);
    }

    /// <summary>The application is up (UI thread): what the services need is in <paramref name="state"/>.</summary>
    public static void Start(AppState state)
    {
        ArgumentNullException.ThrowIfNull(state);
    }

    /// <summary>notify.newMessage (UI thread), before the list: the desktop notification and the sound.</summary>
    public static void NewMessage(NewMessageNotification notification)
    {
        ArgumentNullException.ThrowIfNull(notification);
    }

    /// <summary>The main window was shown or hidden (UI thread).</summary>
    public static void MainWindowVisibilityChanged(bool visible)
    {
        _ = visible;
    }

    /// <summary>Quit's point of no return (UI thread).</summary>
    public static void Stop()
    {
    }

    /// <summary>The application has ended (Main, after Application.Start).</summary>
    public static void Shutdown()
    {
    }
}
