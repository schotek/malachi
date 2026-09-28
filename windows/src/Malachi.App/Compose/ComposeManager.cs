// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/ComposeManager.swift (install,
// newWindow, place); GTK: ui/internal/compose/manager.go (Open's newWindow
// and Present) and ui/main.go (the manager made at startup). The WinUI side
// of the compose manager: it makes the windows ComposeController asks for
// (Integration.InstallComposeWindows, which also sets the hooks that open
// them: New Message, a reply or forward the actions prepared through
// draft.create, a draft opened through draft.open, a mailto: link inside a
// message or from an activation; the shell's RaiseDraft brings a window
// already editing a draft to the front). New windows cascade from the first
// one, centred on the display of the window that was active, as document
// windows do.
//
// One Windows addition: when the connection comes up while a compose window
// is open, the account list is asked for again. A cold mailto: launch opens
// only its composer (docs/windows-port.md §10, U4), before the connection
// exists, so its From row would keep the placeholder identity; GTK's
// composer-only launch never connects at all.

using System;
using Malachi.App.Shell;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Windowing;
using Windows.Graphics;
using Windows.Win32;
using Windows.Win32.Foundation;

namespace Malachi.App.Compose;

/// <summary>Makes and places the compose windows.</summary>
internal sealed partial class ComposeManager
{
    /// <summary>How far each new window is moved from the previous one, in effective pixels.</summary>
    public const int CascadeOffset = 32;

    private readonly AppState state;
    private readonly ComposeController controller;
    private readonly ILogger logger;
    private PointInt32? last;

    private ComposeManager(AppState state, Integration integration)
    {
        this.state = state;
        controller = integration.Compose;
        logger = state.Logs.CreateLogger<ComposeManager>();
        integration.InstallComposeWindows(NewWindow);
        // For the app's life: the hub goes with the app.
        _ = state.Notifications.AddConnectionState(s =>
        {
            if (s is ConnectionState.Connected && controller.OpenWindows.Count > 0)
            {
                controller.Invalidate();
            }
        });
    }

    /// <summary>
    /// ComposeManager.install: the window factory and the hooks that open
    /// compose windows, before the first activation is handled (a cold
    /// mailto: opens its composer at once).
    /// </summary>
    public static ComposeManager Install(AppState state, Integration integration)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(integration);
        return new ComposeManager(state, integration);
    }

    // compose.newWindow + Present.
    private ComposeWindow NewWindow(ComposeParams p)
    {
        ComposeWindow window;
        try
        {
            window = new ComposeWindow(state, controller, p);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            // What opened it (a key, a menu item, an activation) may swallow it.
            LogWindowFailed(logger, e);
            throw;
        }
        Place(window);
        WindowPresenter.Present(window);
        return window;
    }

    // Cascades new windows from the first one, centred on the display of the
    // window that was active last (the main window's otherwise); a window
    // that would leave the display starts over at the top left of its work
    // area.
    private void Place(ComposeWindow window)
    {
        var app = window.AppWindow;
        var anchor = state.Windows.Active?.Window ?? state.MainWindow;
        var display = anchor is not null
            ? DisplayArea.GetFromWindowId(anchor.AppWindow.Id, DisplayAreaFallback.Primary)
            : DisplayArea.GetFromWindowId(app.Id, DisplayAreaFallback.Primary);
        var area = display.WorkArea;
        var size = app.Size;
        var dpi = PInvoke.GetDpiForWindow((HWND)WindowPresenter.Handle(window));
        var step = (int)Math.Round(CascadeOffset * (dpi == 0 ? 1.0 : dpi / 96.0));
        PointInt32 position;
        if (last is { } previous && Contains(area, previous))
        {
            position = new PointInt32(previous.X + step, previous.Y + step);
            if (position.X + size.Width > area.X + area.Width || position.Y + size.Height > area.Y + area.Height)
            {
                position = new PointInt32(area.X + step, area.Y + step);
            }
        }
        else
        {
            position = new PointInt32(
                area.X + Math.Max((area.Width - size.Width) / 2, 0),
                area.Y + Math.Max((area.Height - size.Height) / 2, 0));
        }
        app.Move(position);
        last = position;
    }

    [LoggerMessage(Level = LogLevel.Error, Message = "a compose window could not be made")]
    private static partial void LogWindowFailed(ILogger logger, Exception error);

    private static bool Contains(RectInt32 area, PointInt32 p) =>
        p.X >= area.X && p.Y >= area.Y && p.X < area.X + area.Width && p.Y < area.Y + area.Height;
}
