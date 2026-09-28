// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: one entry of the notification-area icon's native menu
// (TrayIcon.ShowMenu, TrayMenu.Items).

namespace Malachi.Platform.Windows.Tray;

/// <summary>A menu entry: a command with its text, or a separator.</summary>
/// <param name="Command">What the entry does; <see cref="TrayCommand.None"/> for a separator.</param>
/// <param name="Text">The label in Win32 menu syntax (<c>&amp;</c> before the access key); null for a separator.</param>
/// <param name="IsDefault">Drawn bold: what a click on the icon does.</param>
public readonly record struct TrayMenuItem(TrayCommand Command, string? Text, bool IsDefault = false)
{
    /// <summary>A separator line.</summary>
    public static TrayMenuItem Separator { get; } = new(TrayCommand.None, null);

    /// <summary>Whether the entry is a separator.</summary>
    public bool IsSeparator => Text is null;
}
