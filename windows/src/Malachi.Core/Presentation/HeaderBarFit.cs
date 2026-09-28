// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows addition (docs/windows-port.md §11.3): GTK's Adw.HeaderBar and
// macOS's title bar shorten the title and never let the end buttons reach
// the window's own. A WinUI TitleBar lays its parts out in columns and
// pushes whatever does not fit past the caption buttons, so a window whose
// title bar carries buttons of its own (the compose window's Draft Menu and
// Send) turns narrow before that happens: the buttons lose their labels and
// the app icon goes. This decides when, from the wide layout's measures,
// which the window takes while it is wide; the narrow layout is the wide
// one less what it drops, so the decision does not flip on its own result.

using System;

namespace Malachi.Core.Presentation;

/// <summary>When a title bar with end buttons needs its narrow layout.</summary>
public static class HeaderBarFit
{
    /// <summary>
    /// The least room the title keeps in the wide layout, in effective
    /// pixels: a few characters of the subject and the ellipsis.
    /// </summary>
    public const double MinTitleWidth = 64;

    /// <summary>
    /// Whether a bar <paramref name="barWidth"/> wide needs its narrow
    /// layout: its wide layout has less than <see cref="MinTitleWidth"/>
    /// left for the title between <paramref name="titleStart"/> (where the
    /// title starts: the start buttons and the icon) and
    /// <paramref name="endWidth"/> (all that follows the title: the end
    /// buttons, the strip kept for dragging, the caption buttons). Before
    /// the wide layout was measured (a start of zero) the bar stays wide.
    /// </summary>
    public static bool Narrow(double barWidth, double titleStart, double endWidth)
    {
        if (titleStart <= 0 || double.IsNaN(barWidth) || double.IsNaN(titleStart) || double.IsNaN(endWidth))
        {
            return false;
        }
        return barWidth < titleStart + MinTitleWidth + Math.Max(endWidth, 0);
    }
}
