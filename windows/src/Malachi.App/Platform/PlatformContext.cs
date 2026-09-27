// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: what the shell hands PlatformServices.Start once the
// application runs (docs/windows-port.md §10): the objects the platform
// services work with and the application actions the notification-area
// icon offers, the counterparts of the GActions of ui/main.go (app.show,
// app.compose, win.refresh, app.quit) and of AppState.Hooks and
// showMainWindow in macos/Sources/MalachiMail/App/AppState.swift.

using System;
using Malachi.Core.Presentation;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;

namespace Malachi.App.Platform;

/// <summary>The application's side of the platform services.</summary>
/// <remarks>Every action is called on the UI thread.</remarks>
public sealed class PlatformContext
{
    /// <summary>The settings: desktop-notifications, notification-sound, launch-at-login.</summary>
    public required SettingsStore Settings { get; init; }

    /// <summary>
    /// The application's hub, attached to its connection: the new-mail
    /// notifications come from its <c>notify.newMessage</c> handlers.
    /// </summary>
    public required NotificationHub Notifications { get; init; }

    /// <summary>
    /// Whether the main window is the active (foreground) window right now:
    /// no notification and no sound then (notify.go <c>w.IsActive()</c>).
    /// </summary>
    public required Func<bool> IsMainWindowActive { get; init; }

    /// <summary>
    /// Shows the main window, creating it if needed, and brings it to the
    /// front: <c>AppWindow.Show()</c>, <c>Activate()</c>, then
    /// <c>SetForegroundWindow</c> (GTK <c>app.show</c>). The icon's click
    /// and its Open item.
    /// </summary>
    public required Action ShowMainWindow { get; init; }

    /// <summary>Opens an empty compose window (GTK <c>app.compose</c>).</summary>
    public required Action NewMessage { get; init; }

    /// <summary>Asks the daemon to synchronise every account now (GTK <c>win.refresh</c>).</summary>
    public required Action CheckForNewMail { get; init; }

    /// <summary>Quits the application, as Ctrl+Q does (GTK <c>app.quit</c>).</summary>
    public required Action Quit { get; init; }

    /// <summary>Where the services log; nothing when null.</summary>
    public ILoggerFactory? LoggerFactory { get; init; }

    /// <summary>
    /// The executable that mailto:, launch at login and the icon name; the
    /// running one (<see cref="Environment.ProcessPath"/>) when null.
    /// </summary>
    public string? ExecutablePath { get; init; }
}
