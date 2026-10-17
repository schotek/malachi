// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/InvitationDayStripView.swift
// (show, draw); GTK: none yet (docs/calendar.md §5.7, Cal.DayStripOf). The
// strip is drawn on its canvas again at every change of the strip, the
// width or the theme: the axis in the faintest fill, a hairline and a label
// under it for every whole hour (a label that would touch the one before is
// left out), the busy spans in grey, the invitation's slot dashed in blue
// with its time above it. Windows: the theme's brushes stand for AppKit's
// system colours, and a label's width is measured by the TextBlock itself.

using Malachi.Core.Calendars;
using Microsoft.UI;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Shapes;
using Windows.Foundation;

namespace Malachi.App.Reader;

/// <summary>The invitation card's mini day strip.</summary>
public sealed partial class InvitationDayStripView : UserControl
{
    // The bar's height, the labels' size and the room for a label line.
    private const double BarHeight = 14;
    private const double LabelSize = 10;
    private const double LabelHeight = 14;
    private const double Inset = 1;

    private Cal.DayStrip? strip;

    /// <summary>A hidden strip; <see cref="Show"/> gives it one.</summary>
    public InvitationDayStripView()
    {
        InitializeComponent();
        Height = 2 * LabelHeight + BarHeight + 4;
        ActualThemeChanged += (_, _) => Draw();
    }

    /// <summary>Shows <paramref name="next"/>, or hides the view for none.</summary>
    public void Show(Cal.DayStrip? next)
    {
        if (Equals(next, strip) && (next is null) == (Visibility == Visibility.Collapsed))
        {
            return;
        }
        strip = next;
        Visibility = next is null ? Visibility.Collapsed : Visibility.Visible;
        Draw();
    }

    private void OnSizeChanged(object sender, SizeChangedEventArgs e) => Draw();

    private void Draw()
    {
        Surface.Children.Clear();
        if (strip is not { } s)
        {
            return;
        }
        var total = Surface.ActualWidth;
        var width = total - 2 * Inset;
        if (width <= 20)
        {
            return;
        }
        double X(double f) => Inset + f * width;
        var barTop = LabelHeight + 2;

        // The axis: the day's span in the faintest fill.
        Add(new Rectangle
        {
            Width = width,
            Height = BarHeight,
            RadiusX = 3,
            RadiusY = 3,
            Fill = Brush("SubtleFillColorSecondaryBrush"),
        }, Inset, barTop);

        // The hours: a hairline across the bar and the label under it.
        var lastEnd = double.MinValue;
        foreach (var t in s.Ticks)
        {
            var px = X(t.At);
            Add(new Rectangle { Width = 1, Height = BarHeight, Fill = Brush("DividerStrokeColorDefaultBrush") }, px - 0.5, barTop);
            var label = Label(t.Label, Brush("TextFillColorTertiaryBrush"));
            var w = label.DesiredSize.Width;
            var lx = System.Math.Min(System.Math.Max(px - w / 2, 0), total - w);
            if (lx <= lastEnd + 4)
            {
                continue;
            }
            Add(label, lx, barTop + BarHeight + 2);
            lastEnd = lx + w;
        }

        // What is busy already.
        var busy = new SolidColorBrush(Colors.Gray) { Opacity = 0.45 };
        foreach (var b in s.Busy)
        {
            Add(new Rectangle
            {
                Width = System.Math.Max(2, X(b.To) - X(b.From)),
                Height = BarHeight - 4,
                RadiusX = 2,
                RadiusY = 2,
                Fill = busy,
            }, X(b.From), barTop + 2);
        }

        // The invitation's slot, dashed, and its time above.
        if (s.Proposed is not { } p)
        {
            return;
        }
        var blue = Brush("SystemFillColorAttentionBrush");
        var slotWidth = System.Math.Max(3, X(p.To) - X(p.From));
        Add(new Rectangle
        {
            Width = slotWidth,
            Height = BarHeight - 1,
            RadiusX = 3,
            RadiusY = 3,
            Fill = new SolidColorBrush((blue as SolidColorBrush)?.Color ?? Colors.DodgerBlue) { Opacity = 0.15 },
            Stroke = blue,
            StrokeThickness = 1.5,
            // In units of the thickness: 4 and 2 pixels.
            StrokeDashArray = new DoubleCollection { 4 / 1.5, 2 / 1.5 },
        }, X(p.From), barTop + 0.5);
        if (s.ProposedLabel.Length == 0)
        {
            return;
        }
        var slotLabel = Label(s.ProposedLabel, blue);
        var sw = slotLabel.DesiredSize.Width;
        var mid = X(p.From) + slotWidth / 2;
        Add(slotLabel, System.Math.Min(System.Math.Max(mid - sw / 2, 0), total - sw), 0);
    }

    private void Add(FrameworkElement e, double left, double top)
    {
        Canvas.SetLeft(e, left);
        Canvas.SetTop(e, top);
        Surface.Children.Add(e);
    }

    // A label of the application's own time text, measured for placing.
    private static TextBlock Label(string text, Brush brush)
    {
        var label = new TextBlock
        {
            Text = text,
            FontSize = LabelSize,
            Foreground = brush,
            TextWrapping = TextWrapping.NoWrap,
            IsTextScaleFactorEnabled = false,
        };
        label.Measure(new Size(double.PositiveInfinity, double.PositiveInfinity));
        return label;
    }

    private Brush Brush(string key) =>
        Resources.TryGetValue(key, out var own) && own is Brush mine ? mine
        : Application.Current.Resources.TryGetValue(key, out var value) && value is Brush brush ? brush
        : new SolidColorBrush(Colors.Gray);
}
