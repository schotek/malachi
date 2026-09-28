// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the FlowView of macos/Sources/MalachiMail/Compose/
// AttachmentChipsView.swift; GTK: the FlowBox of compose.blp
// (attachments_box: max-children-per-line 6, not homogeneous). WinUI has no
// wrapping panel of its own (the toolkit's is not among the app's
// packages): chips are laid out left to right at their natural width, a new
// line when the next one does not fit or the line holds six, 6 px apart as
// flowbox children's padding puts them.

using System;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.Foundation;

namespace Malachi.App.Compose;

/// <summary>Lays chips out in lines, at most <see cref="MaxPerLine"/> to a line.</summary>
public sealed partial class ChipWrapPanel : Panel
{
    /// <summary>compose.blp max-children-per-line.</summary>
    public const int MaxPerLine = 6;

    /// <summary>The space between chips and between lines.</summary>
    public const double Spacing = 6;

    /// <inheritdoc/>
    protected override Size MeasureOverride(Size availableSize)
    {
        var width = availableSize.Width;
        double x = 0, y = 0, line = 0, widest = 0;
        var inLine = 0;
        foreach (var child in Children)
        {
            child.Measure(new Size(width, double.PositiveInfinity));
            var size = child.DesiredSize;
            if (inLine > 0 && (inLine == MaxPerLine || x + Spacing + size.Width > width))
            {
                widest = Math.Max(widest, x);
                y += line + Spacing;
                x = 0;
                line = 0;
                inLine = 0;
            }
            x += (inLine > 0 ? Spacing : 0) + size.Width;
            line = Math.Max(line, size.Height);
            inLine++;
        }
        widest = Math.Max(widest, x);
        return new Size(double.IsInfinity(width) ? widest : Math.Min(widest, width), inLine == 0 ? 0 : y + line);
    }

    /// <inheritdoc/>
    protected override Size ArrangeOverride(Size finalSize)
    {
        double x = 0, y = 0, line = 0;
        var inLine = 0;
        foreach (var child in Children)
        {
            var size = child.DesiredSize;
            if (inLine > 0 && (inLine == MaxPerLine || x + Spacing + size.Width > finalSize.Width))
            {
                y += line + Spacing;
                x = 0;
                line = 0;
                inLine = 0;
            }
            if (inLine > 0)
            {
                x += Spacing;
            }
            child.Arrange(new Rect(x, y, Math.Min(size.Width, finalSize.Width), size.Height));
            x += size.Width;
            line = Math.Max(line, size.Height);
            inLine++;
        }
        return finalSize;
    }
}
