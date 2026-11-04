// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only layout of ui/internal/window/folders.go newHeaderRow's box:
// "the capsule follows the name, so the name gives way to it when the
// sidebar is narrow; the empty rest of the row goes after both". A WinUI
// Grid gives an Auto column no less than its content and a star column the
// rest, so neither keeps the capsule right after a name that may be cut.

using System;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.Foundation;

namespace Malachi.App.Main;

/// <summary>
/// Lays out its first child (a name) and its second (a capsule) side by
/// side from the left: the capsule at its size right after the name, the
/// name cut to what is left. A collapsed capsule leaves the name the whole
/// width.
/// </summary>
public sealed partial class CapsulePanel : Panel
{
    /// <summary>The gap between the name and the capsule (GTK's margin-start 6).</summary>
    public double Spacing { get; set; } = 6;

    /// <inheritdoc/>
    protected override Size MeasureOverride(Size availableSize)
    {
        var (name, capsule) = Parts();
        var capsuleSize = new Size(0, 0);
        if (capsule is { Visibility: Visibility.Visible })
        {
            capsule.Measure(new Size(double.PositiveInfinity, availableSize.Height));
            capsuleSize = capsule.DesiredSize;
        }
        var gap = capsuleSize.Width > 0 ? Spacing : 0;
        var nameSize = new Size(0, 0);
        if (name is not null)
        {
            name.Measure(new Size(Math.Max(0, availableSize.Width - capsuleSize.Width - gap), availableSize.Height));
            nameSize = name.DesiredSize;
        }
        return new Size(nameSize.Width + gap + capsuleSize.Width, Math.Max(nameSize.Height, capsuleSize.Height));
    }

    /// <inheritdoc/>
    protected override Size ArrangeOverride(Size finalSize)
    {
        var (name, capsule) = Parts();
        var capsuleWidth = capsule is { Visibility: Visibility.Visible } ? capsule.DesiredSize.Width : 0;
        var gap = capsuleWidth > 0 ? Spacing : 0;
        var nameWidth = name is null ? 0 : Math.Min(name.DesiredSize.Width, Math.Max(0, finalSize.Width - capsuleWidth - gap));
        name?.Arrange(new Rect(0, 0, nameWidth, finalSize.Height));
        if (capsule is not null)
        {
            var h = capsule.DesiredSize.Height;
            capsule.Arrange(new Rect(nameWidth + gap, Math.Max(0, (finalSize.Height - h) / 2), capsuleWidth, h));
        }
        return finalSize;
    }

    private (UIElement? Name, UIElement? Capsule) Parts() =>
        (Children.Count > 0 ? Children[0] : null, Children.Count > 1 ? Children[1] : null);
}
