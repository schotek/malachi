// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the layout of the RecipientTokenField of macos/Sources/MalachiMail/
// Compose/RecipientTokenField.swift (arrange); GTK:
// ui/internal/compose/recipient_field.go (the wrapping row of badges).
// ChipWrapPanel does not fit: it lays out at most six chips to a line and
// every child at its natural width, where here the last child (the typing
// box) takes what is left of the last line, or a line of its own when less
// than a usable width is left. Badges and the box are 4 px apart and the
// lines too; the panel keeps 3 px above and below so that a row of one line
// is 30 px high, as the other fields of the card.

using System;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.Foundation;

namespace Malachi.App.Compose;

/// <summary>Lays badges out in lines; the last child fills the rest of the last line.</summary>
public sealed partial class RecipientWrapPanel : Panel
{
    /// <summary>The space between badges, and between the last badge and the typing box.</summary>
    public const double Spacing = 4;

    /// <summary>The space between lines.</summary>
    public const double LineSpacing = 4;

    /// <summary>The space above the first and below the last line.</summary>
    public const double VerticalInset = 3;

    /// <summary>The least width the typing box gets before it moves to a line of its own.</summary>
    public const double MinEditorWidth = 80;

    /// <inheritdoc/>
    protected override Size MeasureOverride(Size availableSize)
    {
        var width = availableSize.Width;
        double x = 0, y = VerticalInset, line = 0, widest = 0;
        var count = Children.Count;
        for (var i = 0; i < count; i++)
        {
            var child = Children[i];
            var last = i == count - 1;
            if (!last)
            {
                child.Measure(new Size(width, double.PositiveInfinity));
                var size = child.DesiredSize;
                if (x > 0 && x + size.Width > width)
                {
                    widest = Math.Max(widest, x - Spacing);
                    y += line + LineSpacing;
                    x = 0;
                    line = 0;
                }
                x += size.Width + Spacing;
                line = Math.Max(line, size.Height);
                continue;
            }
            var need = Math.Min(MinEditorWidth, width);
            if (x > 0 && x + need > width)
            {
                widest = Math.Max(widest, x - Spacing);
                y += line + LineSpacing;
                x = 0;
                line = 0;
            }
            child.Measure(new Size(Math.Max(width - x, need), double.PositiveInfinity));
            line = Math.Max(line, child.DesiredSize.Height);
            widest = Math.Max(widest, x + child.DesiredSize.Width);
        }
        var height = y + line + VerticalInset;
        return new Size(double.IsInfinity(width) ? widest : width, height);
    }

    /// <inheritdoc/>
    protected override Size ArrangeOverride(Size finalSize)
    {
        double x = 0, y = VerticalInset, line = 0;
        var count = Children.Count;
        for (var i = 0; i < count; i++)
        {
            var child = Children[i];
            var size = child.DesiredSize;
            var last = i == count - 1;
            if (!last)
            {
                if (x > 0 && x + size.Width > finalSize.Width)
                {
                    y += line + LineSpacing;
                    x = 0;
                    line = 0;
                }
                child.Arrange(new Rect(x, y, Math.Min(size.Width, finalSize.Width), size.Height));
                x += size.Width + Spacing;
                line = Math.Max(line, size.Height);
                continue;
            }
            var need = Math.Min(MinEditorWidth, finalSize.Width);
            if (x > 0 && x + need > finalSize.Width)
            {
                y += line + LineSpacing;
                x = 0;
                line = 0;
            }
            child.Arrange(new Rect(x, y, Math.Max(finalSize.Width - x, need), Math.Max(size.Height, line)));
        }
        return finalSize;
    }
}
