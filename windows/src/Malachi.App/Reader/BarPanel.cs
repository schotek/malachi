// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the horizontal Box of window.blp's remote_bar and pictures_bar
// (spacing 12: a spinner, the label with wrap and hexpand, the buttons);
// macOS: MessageView/RemoteBarView.swift. GTK's label never breaks a word
// and the box asks the window for the room its label and buttons need;
// WinUI gives a Grid's star column whatever is left, down to a letter a
// line. Here the buttons move under the sentence, at its trailing end and
// on as many lines as they need, when the label would get less than it
// needs.
//
// Windows-only: the fallback layout for a pane too narrow for the bar.

using System;
using System.Collections.Generic;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.Foundation;

namespace Malachi.App.Reader;

/// <summary>
/// A bar's row: the children before the first <see cref="TextBlock"/> lead,
/// that TextBlock is the label and takes the rest of the line, the children
/// after it trail at the end; when the label would get less than
/// <see cref="LabelMinimum"/> (or its own width, if narrower), the trailing
/// children take lines of their own under it, each line at the end.
/// </summary>
public sealed partial class BarPanel : Panel
{
    /// <summary>The space between two children of a line (GtkBox spacing).</summary>
    public static readonly DependencyProperty SpacingProperty = DependencyProperty.Register(
        nameof(Spacing), typeof(double), typeof(BarPanel), new PropertyMetadata(0.0, OnLayoutChanged));

    /// <summary>The space between the label's line and the buttons' line.</summary>
    public static readonly DependencyProperty LineSpacingProperty = DependencyProperty.Register(
        nameof(LineSpacing), typeof(double), typeof(BarPanel), new PropertyMetadata(0.0, OnLayoutChanged));

    /// <summary>The width below which the label no longer shares its line.</summary>
    public static readonly DependencyProperty LabelMinimumProperty = DependencyProperty.Register(
        nameof(LabelMinimum), typeof(double), typeof(BarPanel), new PropertyMetadata(160.0, OnLayoutChanged));

    // The last measure's choice, for the arrange.
    private bool stacked;

    /// <summary>The space between two children of a line.</summary>
    public double Spacing
    {
        get => (double)GetValue(SpacingProperty);
        set => SetValue(SpacingProperty, value);
    }

    /// <summary>The space between the label's line and the buttons' line.</summary>
    public double LineSpacing
    {
        get => (double)GetValue(LineSpacingProperty);
        set => SetValue(LineSpacingProperty, value);
    }

    /// <summary>The width below which the label no longer shares its line.</summary>
    public double LabelMinimum
    {
        get => (double)GetValue(LabelMinimumProperty);
        set => SetValue(LabelMinimumProperty, value);
    }

    /// <inheritdoc/>
    protected override Size MeasureOverride(Size availableSize)
    {
        var width = availableSize.Width;
        var infinite = new Size(double.PositiveInfinity, double.PositiveInfinity);
        var label = Label();
        double lead = 0, trail = 0, leadHeight = 0, trailHeight = 0;
        var beforeLabel = true;
        foreach (var child in Children)
        {
            if (ReferenceEquals(child, label))
            {
                beforeLabel = false;
                continue;
            }
            child.Measure(infinite);
            if (child.Visibility == Visibility.Collapsed)
            {
                continue;
            }
            var size = child.DesiredSize;
            if (beforeLabel)
            {
                lead += size.Width + Spacing;
                leadHeight = Math.Max(leadHeight, size.Height);
            }
            else
            {
                trail += Spacing + size.Width;
                trailHeight = Math.Max(trailHeight, size.Height);
            }
        }
        if (label is null)
        {
            stacked = false;
            return new Size(double.IsInfinity(width) ? lead + trail : width, Math.Max(leadHeight, trailHeight));
        }
        label.Measure(infinite);
        var natural = label.DesiredSize.Width;
        var room = width - lead - trail;
        stacked = trail > 0 && !double.IsInfinity(width) && room < Math.Min(natural, LabelMinimum);
        if (!stacked)
        {
            label.Measure(new Size(Math.Max(room, 0), double.PositiveInfinity));
            var height = Math.Max(label.DesiredSize.Height, Math.Max(leadHeight, trailHeight));
            return new Size(double.IsInfinity(width) ? lead + natural + trail : width, height);
        }
        label.Measure(new Size(Math.Max(width - lead, 0), double.PositiveInfinity));
        var first = Math.Max(label.DesiredSize.Height, leadHeight);
        double below = 0;
        foreach (var line in TrailingLines(label, width))
        {
            below += LineSpacing + line.Height;
        }
        return new Size(width, first + below);
    }

    /// <inheritdoc/>
    protected override Size ArrangeOverride(Size finalSize)
    {
        var width = finalSize.Width;
        var label = Label();
        // The height of the first line: the whole bar, or the label's line.
        double first = 0;
        var beforeLabel = true;
        foreach (var child in Children)
        {
            if (child.Visibility == Visibility.Collapsed)
            {
                continue;
            }
            if (ReferenceEquals(child, label))
            {
                beforeLabel = false;
                first = Math.Max(first, child.DesiredSize.Height);
            }
            else if (beforeLabel)
            {
                first = Math.Max(first, child.DesiredSize.Height);
            }
        }
        if (!stacked)
        {
            first = finalSize.Height;
        }

        // The trailing children: at the end of the first line, or on lines
        // of their own under it, each line at the end.
        double trailWidth = 0;
        if (!stacked)
        {
            var end = width;
            for (var i = Children.Count - 1; i >= 0; i--)
            {
                var child = Children[i];
                if (ReferenceEquals(child, label))
                {
                    break;
                }
                if (child.Visibility == Visibility.Collapsed)
                {
                    child.Arrange(new Rect(0, 0, 0, 0));
                    continue;
                }
                var size = child.DesiredSize;
                end -= size.Width;
                child.Arrange(new Rect(Math.Max(end, 0), (first - size.Height) / 2, size.Width, size.Height));
                trailWidth += size.Width + Spacing;
                end -= Spacing;
            }
        }
        else
        {
            var afterLabel = false;
            foreach (var child in Children)
            {
                afterLabel |= ReferenceEquals(child, label);
                if (afterLabel && child.Visibility == Visibility.Collapsed)
                {
                    child.Arrange(new Rect(0, 0, 0, 0));
                }
            }
            var top = first;
            foreach (var line in TrailingLines(label!, width))
            {
                top += LineSpacing;
                var x0 = Math.Max(width - line.Width, 0);
                foreach (var child in line.Children)
                {
                    var size = child.DesiredSize;
                    var w = Math.Min(size.Width, Math.Max(width - x0, 0));
                    child.Arrange(new Rect(x0, top + ((line.Height - size.Height) / 2), w, size.Height));
                    x0 += size.Width + Spacing;
                }
                top += line.Height;
            }
        }

        // The leading children and the label on the first line.
        double x = 0;
        foreach (var child in Children)
        {
            if (ReferenceEquals(child, label))
            {
                var right = stacked ? width : width - trailWidth;
                var labelWidth = Math.Max(right - x, 0);
                var height = child.DesiredSize.Height;
                child.Arrange(new Rect(x, (first - height) / 2, labelWidth, height));
                break;
            }
            if (child.Visibility == Visibility.Collapsed)
            {
                child.Arrange(new Rect(0, 0, 0, 0));
                continue;
            }
            var size = child.DesiredSize;
            child.Arrange(new Rect(x, (first - size.Height) / 2, size.Width, size.Height));
            x += size.Width + Spacing;
        }
        return finalSize;
    }

    // The visible trailing children in lines no wider than width (one child
    // a line when it alone is wider), from their measured sizes.
    private List<TrailingLine> TrailingLines(TextBlock label, double width)
    {
        var lines = new List<TrailingLine>();
        TrailingLine? current = null;
        var afterLabel = false;
        foreach (var child in Children)
        {
            if (ReferenceEquals(child, label))
            {
                afterLabel = true;
                continue;
            }
            if (!afterLabel)
            {
                continue;
            }
            if (child.Visibility == Visibility.Collapsed)
            {
                continue;
            }
            var size = child.DesiredSize;
            if (current is null || current.Width + Spacing + size.Width > width)
            {
                current = new TrailingLine();
                lines.Add(current);
            }
            else
            {
                current.Width += Spacing;
            }
            current.Children.Add(child);
            current.Width += size.Width;
            current.Height = Math.Max(current.Height, size.Height);
        }
        return lines;
    }

    // One line of trailing children.
    private sealed class TrailingLine
    {
        public List<UIElement> Children { get; } = [];

        public double Width { get; set; }

        public double Height { get; set; }
    }

    // The label: the first TextBlock among the children.
    private TextBlock? Label()
    {
        foreach (var child in Children)
        {
            if (child is TextBlock text)
            {
                return text;
            }
        }
        return null;
    }

    private static void OnLayoutChanged(DependencyObject d, DependencyPropertyChangedEventArgs e) =>
        ((BarPanel)d).InvalidateMeasure();
}
