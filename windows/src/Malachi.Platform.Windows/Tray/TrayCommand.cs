// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the commands of the notification-area icon's menu
// (docs/windows-port.md §10, the deviation "a notification-area icon while
// running in the background" of windows/README.md). Each is an application
// action GTK has: app.show, app.compose, win.refresh; Quit is app.quit.

namespace Malachi.Platform.Windows.Tray;

/// <summary>What the notification-area icon's menu offers; the values are the menu item ids.</summary>
public enum TrayCommand
{
    /// <summary>The menu was dismissed.</summary>
    None = 0,

    /// <summary>Shows the main window (GTK <c>app.show</c>); also the icon's click.</summary>
    Open = 1,

    /// <summary>Opens an empty compose window (GTK <c>app.compose</c>).</summary>
    NewMessage = 2,

    /// <summary>Synchronises every account now (GTK <c>win.refresh</c>).</summary>
    CheckForNewMail = 3,

    /// <summary>Quits the application (GTK <c>app.quit</c>).</summary>
    Quit = 4,
}
