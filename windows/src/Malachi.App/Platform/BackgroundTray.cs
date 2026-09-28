// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the notification-area icon while the app runs in the
// background (docs/windows-port.md §10, the row of windows/README.md). GTK
// keeps a hidden window reachable through the desktop (a relaunch, a
// notification's app.show) and macOS through the Dock; on Windows a hidden
// app is invisible, so while the main window is hidden (Run in Background,
// or started by launch at login) an icon offers Open (also its click), New
// Message, Check for New Mail and Quit. The actions are posted to the UI
// thread's queue, so they run after the icon's window procedure and its
// menu have returned; the icon's click carries the foreground right that
// ShowMainWindow's SetForegroundWindow needs (APP-SPIKES §5.2).

using System;
using System.ComponentModel;
using Malachi.Core;
using Malachi.Platform.Windows.Tray;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Dispatching;

namespace Malachi.App.Platform;

/// <summary>Shows and hides the notification-area icon and runs its commands.</summary>
/// <remarks>UI-thread only.</remarks>
internal sealed partial class BackgroundTray(PlatformContext context, DispatcherQueue dispatcher, string executablePath, ILogger logger) : IDisposable
{
    private TrayIcon? icon;

    /// <summary>Whether the icon is in the notification area.</summary>
    public bool IsVisible => icon is not null;

    /// <summary>Adds or removes the icon.</summary>
    public void SetVisible(bool visible)
    {
        if (visible == icon is not null)
        {
            return;
        }
        if (!visible)
        {
            Remove();
            return;
        }
        try
        {
            var tray = new TrayIcon(AppIdentity.DisplayName, executablePath, logger);
            tray.Activated += OnActivated;
            tray.MenuRequested += OnMenuRequested;
            icon = tray;
        }
        catch (Exception e) when (e is InvalidOperationException or PlatformNotSupportedException or Win32Exception)
        {
            LogNoIcon(logger, e.Message);
        }
    }

    /// <summary>Removes the icon.</summary>
    public void Dispose() => Remove();

    private void Remove()
    {
        if (icon is { } tray)
        {
            icon = null;
            tray.Activated -= OnActivated;
            tray.MenuRequested -= OnMenuRequested;
            tray.Dispose();
        }
    }

    private void OnActivated(object? sender, EventArgs e) => Run(TrayCommand.Open);

    private void OnMenuRequested(object? sender, TrayMenuRequestedEventArgs e)
    {
        if (sender is TrayIcon tray)
        {
            Run(tray.ShowMenu(e.X, e.Y, TrayMenu.Items()));
        }
    }

    private void Run(TrayCommand command)
    {
        Action? action = command switch
        {
            TrayCommand.Open => context.ShowMainWindow,
            TrayCommand.NewMessage => context.NewMessage,
            TrayCommand.CheckForNewMail => context.CheckForNewMail,
            TrayCommand.Quit => context.Quit,
            _ => null,
        };
        if (action is not null && !dispatcher.TryEnqueue(() => Invoke(command, action)))
        {
            LogNotQueued(logger, command);
        }
    }

    // A failure of the shell's action is logged: an exception out of a
    // queued callback would end the application.
    private void Invoke(TrayCommand command, Action action)
    {
        try
        {
            action();
        }
#pragma warning disable CA1031 // The icon's command must not take the application down.
        catch (Exception e)
#pragma warning restore CA1031
        {
            LogCommandFailed(logger, command, e);
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "tray: no icon ({Reason})")]
    private static partial void LogNoIcon(ILogger logger, string reason);

    [LoggerMessage(Level = LogLevel.Error, Message = "tray: {Command} failed")]
    private static partial void LogCommandFailed(ILogger logger, TrayCommand command, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "tray: {Command} was not queued; the UI thread is shutting down")]
    private static partial void LogNotQueued(ILogger logger, TrayCommand command);
}
