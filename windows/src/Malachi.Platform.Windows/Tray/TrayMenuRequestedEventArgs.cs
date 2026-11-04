// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: where the notification-area icon's menu was asked for
// (TrayIcon.MenuRequested): the anchor Explorer passes with WM_CONTEXTMENU,
// the click's point or, from the keyboard, the icon's.

using System;

namespace Malachi.Platform.Windows.Tray;

/// <summary>The screen point at which the icon's menu opens.</summary>
/// <param name="x">Horizontal, in physical screen pixels.</param>
/// <param name="y">Vertical, in physical screen pixels.</param>
public sealed class TrayMenuRequestedEventArgs(int x, int y) : EventArgs
{
    /// <summary>Horizontal, in physical screen pixels.</summary>
    public int X { get; } = x;

    /// <summary>Vertical, in physical screen pixels.</summary>
    public int Y { get; } = y;
}
