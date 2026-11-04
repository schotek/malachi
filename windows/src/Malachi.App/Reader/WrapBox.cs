// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of Adw.WrapBox as window.blp uses it (message_from, message_to,
// message_cc: child-spacing 4, line-spacing 4; message_attachments: 6, 6);
// macOS: Shared/FlowView.swift. The children in a row, left to right, a
// new line when the next one does not fit; each line as tall as its
// tallest child, the children at its top; a child that is an IWrapBoxFullLine
// takes a line of its own, as wide as the box (the summary line over the
// attachment tiles, macOS FlowFullLine). WinUI has no such panel in the
// box (the toolkit's WrapPanel would be a package of its own), and the
// chips need nothing more than this.

using System;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.Foundation;

namespace Malachi.App.Reader;

/// <summary>A child of a <see cref="WrapBox"/> that takes a whole line of its own, as wide as the box.</summary>
#pragma warning disable CA1040 // a marker, as Swift's FlowFullLine
public interface IWrapBoxFullLine
#pragma warning restore CA1040
{
}

/// <summary>A panel that lays its children out in lines (Adw.WrapBox).</summary>
public sealed partial class WrapBox : Panel
{
    /// <summary>The space between two children of a line (child-spacing).</summary>
    public static readonly DependencyProperty ChildSpacingProperty = DependencyProperty.Register(
        nameof(ChildSpacing), typeof(double), typeof(WrapBox), new PropertyMetadata(0.0, OnLayoutChanged));

    /// <summary>The space between two lines (line-spacing).</summary>
    public static readonly DependencyProperty LineSpacingProperty = DependencyProperty.Register(
        nameof(LineSpacing), typeof(double), typeof(WrapBox), new PropertyMetadata(0.0, OnLayoutChanged));

    /// <summary>The space between two children of a line.</summary>
    public double ChildSpacing
    {
        get => (double)GetValue(ChildSpacingProperty);
        set => SetValue(ChildSpacingProperty, value);
    }

    /// <summary>The space between two lines.</summary>
    public double LineSpacing
    {
        get => (double)GetValue(LineSpacingProperty);
        set => SetValue(LineSpacingProperty, value);
    }

    /// <inheritdoc/>
    protected override Size MeasureOverride(Size availableSize)
    {
        var width = availableSize.Width;
        double lineWidth = 0, lineHeight = 0, totalWidth = 0, totalHeight = 0;
        var first = true;
        var breakNext = false;
        foreach (var child in Children)
        {
            child.Measure(new Size(width, double.PositiveInfinity));
            if (child.Visibility == Visibility.Collapsed)
            {
                continue;
            }
            var full = child is IWrapBoxFullLine;
            var size = child.DesiredSize;
            if (full && !double.IsInfinity(width))
            {
                size = new Size(width, size.Height);
            }
            if (!first && (breakNext || full || (lineWidth > 0 && lineWidth + ChildSpacing + size.Width > width)))
            {
                totalWidth = Math.Max(totalWidth, lineWidth);
                totalHeight += lineHeight + LineSpacing;
                lineWidth = 0;
                lineHeight = 0;
            }
            lineWidth += (lineWidth > 0 ? ChildSpacing : 0) + size.Width;
            lineHeight = Math.Max(lineHeight, size.Height);
            first = false;
            breakNext = full;
        }
        totalWidth = Math.Max(totalWidth, lineWidth);
        totalHeight += lineHeight;
        return new Size(double.IsInfinity(width) ? totalWidth : Math.Min(totalWidth, width), totalHeight);
    }

    /// <inheritdoc/>
    protected override Size ArrangeOverride(Size finalSize)
    {
        var width = finalSize.Width;
        double x = 0, y = 0, lineHeight = 0;
        bool started = false, breakNext = false;
        foreach (var child in Children)
        {
            if (child.Visibility == Visibility.Collapsed)
            {
                child.Arrange(new Rect(0, 0, 0, 0));
                continue;
            }
            var full = child is IWrapBoxFullLine;
            var size = full ? new Size(width, child.DesiredSize.Height) : child.DesiredSize;
            if (started && (breakNext || full || (x > 0 && x + ChildSpacing + size.Width > width)))
            {
                x = 0;
                y += lineHeight + LineSpacing;
                lineHeight = 0;
            }
            if (x > 0)
            {
                x += ChildSpacing;
            }
            child.Arrange(new Rect(x, y, Math.Min(size.Width, Math.Max(width - x, 0)), size.Height));
            x += size.Width;
            lineHeight = Math.Max(lineHeight, size.Height);
            started = true;
            breakNext = full;
        }
        return finalSize;
    }

    private static void OnLayoutChanged(DependencyObject d, DependencyPropertyChangedEventArgs e) =>
        ((WrapBox)d).InvalidateMeasure();
}
