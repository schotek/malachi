// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the room a TitleBar keeps for the window's caption
// buttons. WinUI's TitleBar gives its last column (RightPaddingColumn of
// DefaultTitleBarStyle) the caption buttons' inset as it is in physical
// pixels, not in view pixels, so at a display scale above 100 % the end of
// the bar keeps the difference empty: at 200 % the compose window's Send
// stood 196 view pixels before Minimize instead of 52, as if centred
// (measured 2026-11-02 over UI Automation, Windows App SDK 2.3.9). The
// column is set again here, after the TitleBar's own handlers, whenever the
// bar's size or the scale changes, and the bar is asked to recompute the
// parts that take the pointer when it moved anything. Where the TitleBar
// gets it right the column already has the width and nothing changes.

using System;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Controls;

/// <summary>Keeps a TitleBar's room for the caption buttons in view pixels.</summary>
internal static class TitleBarInsets
{
    /// <summary>Follows <paramref name="bar"/> of <paramref name="window"/> for its life.</summary>
    public static void Keep(TitleBar bar, AppWindow window)
    {
        void Later() => bar.DispatcherQueue.TryEnqueue(() => Fit(bar, window));
        bar.SizeChanged += (_, _) => Later();
        bar.Loaded += (_, _) =>
        {
            if (bar.XamlRoot is { } root)
            {
                root.Changed += (_, _) => Later();
            }
            Later();
        };
    }

    private static void Fit(TitleBar bar, AppWindow window)
    {
        if (bar.XamlRoot is not { } root || root.RasterizationScale <= 0
            || VisualTreeHelper.GetChildrenCount(bar) == 0
            || VisualTreeHelper.GetChild(bar, 0) is not Grid layout
            || layout.ColumnDefinitions.Count == 0)
        {
            return;
        }
        double width;
        try
        {
            width = window.TitleBar.RightInset / root.RasterizationScale;
        }
        catch (ObjectDisposedException)
        {
            // The window closed while the call was queued.
            return;
        }
        var padding = layout.ColumnDefinitions[^1];
        if (Math.Abs(padding.ActualWidth - width) < 0.5)
        {
            return;
        }
        padding.Width = new GridLength(width);
        bar.UpdateLayout();
        bar.RecomputeDragRegions();
    }
}
