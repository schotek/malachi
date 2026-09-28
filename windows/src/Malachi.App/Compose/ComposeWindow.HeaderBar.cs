// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of what Adw.HeaderBar does for compose.blp's header bar (GTK:
// ui/data/ui/compose.blp, window_title; macOS: the window's own title bar):
// the title is shortened and the end buttons stay clear of the window's own.
//
// Windows specifics (docs/windows-port.md §11.3): the subject is a
// TextBlock in the TitleBar's middle column (ComposeWindow.xaml), which
// shrinks, so no subject pushes Send under the caption buttons (measured at
// 96 DPI with a subject of 117 characters). When the title would get less
// than a few characters (below about 510 px in Czech, of a window whose
// smallest is 360), the bar turns narrow (Core's HeaderBarFit, from the
// wide layout's measures): Send shows only its icon, keeping its name and
// tooltip, and the app icon goes; at 360 px the end buttons still keep the
// drag strip before the caption buttons. The TitleBar recomputes the parts
// that take the pointer only when its own size changes, so a change of the
// end buttons asks it to again.

using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Compose;

/// <summary>The header bar of a compose window.</summary>
public sealed partial class ComposeWindow
{
    // The TitleBar's strip kept for dragging after the end buttons
    // (TitleBarMinDragRegionWidth) when the resource cannot be read.
    private const double DefaultDragStrip = 48;

    // Where the title starts and how wide the end buttons are in the wide
    // layout, measured while the bar is wide.
    private double wideTitleStart;
    private double wideEndButtons;
    private bool narrowHeader;
    private IconSource? appIcon;
    private Thickness sendPadding;
    private double sendSpacing;

    private void WireHeaderBar()
    {
        appIcon = ComposeTitleBar.IconSource;
        sendPadding = SendButton.Padding;
        sendSpacing = SendContent.Spacing;
        ComposeTitleBar.SizeChanged += (_, _) => FitHeaderBar();
    }

    // Measures the wide layout while it shows, and turns the bar narrow or
    // wide when the window's width asks for it.
    private void FitHeaderBar()
    {
        if (closing || TitleText.XamlRoot is not { } root)
        {
            return;
        }
        if (!narrowHeader)
        {
            wideTitleStart = TitleText.TransformToVisual(ComposeTitleBar).TransformPoint(default).X;
            wideEndButtons = EndButtons.ActualWidth + EndButtons.Margin.Left + EndButtons.Margin.Right;
        }
        var captions = AppWindow.TitleBar.RightInset / root.RasterizationScale;
        var narrow = HeaderBarFit.Narrow(ComposeTitleBar.ActualWidth, wideTitleStart, wideEndButtons + DragStrip() + captions);
        if (narrow == narrowHeader)
        {
            return;
        }
        narrowHeader = narrow;
        SendLabel.Visibility = narrow ? Visibility.Collapsed : Visibility.Visible;
        SendContent.Spacing = narrow ? 0 : sendSpacing;
        SendButton.Padding = narrow ? new Thickness(0) : sendPadding;
        SendButton.Width = narrow ? DraftMenuButton.Width : double.NaN;
        ComposeTitleBar.IconSource = narrow ? null : appIcon;
        // After this layout pass: the end buttons moved, the bar did not.
        _ = DispatcherQueue.TryEnqueue(() =>
        {
            if (!closing)
            {
                ComposeTitleBar.RecomputeDragRegions();
            }
        });
    }

    private static double DragStrip() =>
        Application.Current.Resources.TryGetValue("TitleBarMinDragRegionWidth", out var value) && value is double width ? width : DefaultDragStrip;
}
