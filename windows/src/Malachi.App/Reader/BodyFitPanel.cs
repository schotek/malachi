// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the page box of window.blp around body_stack, quoted_row and
// body_filler (ui/internal/window/message_view_fit.go); macOS:
// MessageView/MessageViewController.swift (textFit, htmlFit, bodySpacer).
// GTK's box and AppKit's constraint priorities give the body its natural
// height while it fits; a WinUI Grid row is either Auto (never less than
// its content, so a long body would not scroll) or star (always all of it),
// so this panel does it: the first child is the body area, the second the
// row under it, and the body area is as tall as BodyFit.Height says, the
// row right under it, the rest of the panel empty.

using System;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.Foundation;

namespace Malachi.App.Reader;

/// <summary>The body area and the "•••" row under it (body_stack, quoted_row, body_filler).</summary>
public sealed partial class BodyFitPanel : Panel
{
    /// <summary>How tall the body area is (<see cref="BodyFit.ModeFor"/>).</summary>
    public static readonly DependencyProperty ModeProperty = DependencyProperty.Register(
        nameof(Mode), typeof(BodyFitMode), typeof(BodyFitPanel), new PropertyMetadata(BodyFitMode.Fill, OnLayoutChanged));

    /// <summary>The known height of the body, for <see cref="BodyFitMode.Height"/>.</summary>
    public static readonly DependencyProperty FitHeightProperty = DependencyProperty.Register(
        nameof(FitHeight), typeof(double), typeof(BodyFitPanel), new PropertyMetadata(0.0, OnLayoutChanged));

    /// <summary>How tall the body area is.</summary>
    public BodyFitMode Mode
    {
        get => (BodyFitMode)GetValue(ModeProperty);
        set => SetValue(ModeProperty, value);
    }

    /// <summary>The known height of the body, for <see cref="BodyFitMode.Height"/>.</summary>
    public double FitHeight
    {
        get => (double)GetValue(FitHeightProperty);
        set => SetValue(FitHeightProperty, value);
    }

    /// <inheritdoc/>
    protected override Size MeasureOverride(Size availableSize)
    {
        var (body, under) = Parts();
        double underHeight = 0, width = 0;
        if (under is not null)
        {
            under.Measure(availableSize);
            underHeight = under.DesiredSize.Height;
            width = under.DesiredSize.Width;
        }
        var room = double.IsPositiveInfinity(availableSize.Height)
            ? double.PositiveInfinity
            : Math.Max(0, availableSize.Height - underHeight);
        double bodyHeight = 0;
        if (body is not null)
        {
            body.Measure(new Size(availableSize.Width, room));
            bodyHeight = BodyFit.Height(Mode, room, body.DesiredSize.Height, FitHeight);
            width = Math.Max(width, body.DesiredSize.Width);
        }
        var height = double.IsPositiveInfinity(availableSize.Height) ? bodyHeight + underHeight : availableSize.Height;
        return new Size(double.IsPositiveInfinity(availableSize.Width) ? width : availableSize.Width, height);
    }

    /// <inheritdoc/>
    protected override Size ArrangeOverride(Size finalSize)
    {
        var (body, under) = Parts();
        var underHeight = under?.DesiredSize.Height ?? 0;
        var room = Math.Max(0, finalSize.Height - underHeight);
        var bodyHeight = body is null ? 0 : BodyFit.Height(Mode, room, body.DesiredSize.Height, FitHeight);
        body?.Arrange(new Rect(0, 0, finalSize.Width, bodyHeight));
        under?.Arrange(new Rect(0, bodyHeight, finalSize.Width, underHeight));
        return finalSize;
    }

    private (UIElement? Body, UIElement? Under) Parts() =>
        (Children.Count > 0 ? Children[0] : null, Children.Count > 1 ? Children[1] : null);

    private static void OnLayoutChanged(DependencyObject d, DependencyPropertyChangedEventArgs e) =>
        ((BodyFitPanel)d).InvalidateMeasure();
}
