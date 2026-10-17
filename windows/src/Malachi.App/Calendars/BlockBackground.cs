// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/EventBlockView.swift
// (BlockPainter.paint, the colour bar of EventBlockView.draw and
// MonthSpanBarView.draw); GTK: the calendar's views follow
// (docs/calendar.md §6.4).
//
// The fill and the outline of a block, a bar or a pill by its
// Cal.BlockState, and the ink of the texts drawn over them. Windows (the
// owner's decisions of 2026-10-17 after the Windows live test; macOS fills
// every block with its colour at 18 % over the surface): a normal block is
// filled with its calendar's colour itself, its texts white or, where white
// has less than 4.5:1, near-black (CalendarPalette.TextOn), its 3 DIP
// colour bar at the left the colour 25 % towards black; tentative is
// Outlook's: a light fill of the colour at 25 % over the surface with the
// theme's texts and a 4 DIP bar hatched at 45° in the colour and the
// surface, its stripes on whole pixels of the display's scale (Hatch); an
// unanswered invitation is the surface with a 1.5 DIP dashed outline in
// the colour and its texts in the colour, shaded for contrast
// (CalendarPalette.InkOn), without the bar; declined and cancelled are the
// colour at 18 % over the surface with the theme's texts and the bar in
// the colour. The selection is a 2 DIP ring in the accent colour with a
// 1 DIP gap in the surface's colour inside it, over the edge of the fill
// (and the bar), the same in every state; the keyboard's focus visual is
// drawn outside the block (OccurrenceView), so the two never look alike.
// The ends that continue (a month bar running on into the next or previous
// week) run square to the edge: drawn wider and clipped. Drawn under the
// block's text, never hit-testable (the block takes the clicks). The one
// painter of every view (the Week's blocks and all-day bars, the Month's
// bars and pills): each calls Paint from OccurrenceView.Repaint, in its
// window's resolved theme and with fresh brushes, when it is made, loaded
// and first laid out and on every change of its theme or selection.

using System;
using Malachi.Core.Calendars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Shapes;
using Windows.Foundation;
using Windows.UI;

namespace Malachi.App.Calendars;

/// <summary>The painted background of a block, a bar or a pill (macOS <c>BlockPainter</c>).</summary>
internal sealed partial class BlockBackground : Grid
{
    // How far a continuing end runs past the edge before the clip.
    private const double Overrun = 6;

    private readonly Rectangle fill = new() { IsHitTestVisible = false };
    private readonly Rectangle outline = new()
    {
        IsHitTestVisible = false,
        StrokeThickness = CalendarMetrics.UnansweredOutline,
        // In units of the thickness: 4 and 3 DIP.
        StrokeDashArray = [4 / CalendarMetrics.UnansweredOutline, 3 / CalendarMetrics.UnansweredOutline],
    };
    private readonly Rectangle bar = new()
    {
        IsHitTestVisible = false,
        Width = CalendarMetrics.BlockBar,
        HorizontalAlignment = HorizontalAlignment.Left,
        RadiusX = 1.5,
        RadiusY = 1.5,
    };
    private readonly Rectangle ring = new()
    {
        IsHitTestVisible = false,
        StrokeThickness = CalendarMetrics.SelectionRing,
        Style = CalendarStyles.Get("CalendarSelectionRingStyle"),
        Visibility = Visibility.Collapsed,
    };
    private readonly Rectangle gap = new()
    {
        IsHitTestVisible = false,
        StrokeThickness = CalendarMetrics.SelectionGap,
        Style = CalendarStyles.Get("CalendarSelectionGapStyle"),
        Visibility = Visibility.Collapsed,
    };

    private readonly RectangleGeometry clip = new();

    // The colours of a tentative bar's hatching (null: the bar is plain),
    // the display scale its brush was made for, and the window followed for
    // a change of that scale while a hatched bar shows.
    private (Color Ink, Color Ground)? hatch;
    private double hatchScale;
    private XamlRoot? watched;

    /// <summary>An empty background; <see cref="Paint"/> draws it.</summary>
    public BlockBackground()
    {
        IsHitTestVisible = false;
        Children.Add(fill);
        Children.Add(outline);
        Children.Add(bar);
        Children.Add(ring);
        Children.Add(gap);
        Clip = clip;
        SizeChanged += (_, e) => clip.Rect = new Rect(0, 0, e.NewSize.Width, e.NewSize.Height);
        Loaded += (_, _) =>
        {
            Watch(hatch is null ? null : XamlRoot);
            RefreshHatch();
        };
        Unloaded += (_, _) => Watch(null);
    }

    /// <summary>
    /// The width of the bar at the left of an occurrence drawn as
    /// <paramref name="state"/>, for its texts to start after it: a
    /// tentative one's hatched bar is wider (Windows).
    /// </summary>
    public static double BarWidth(Cal.BlockState state) =>
        state == Cal.BlockState.Tentative ? CalendarMetrics.TentativeBar : CalendarMetrics.BlockBar;

    /// <summary>
    /// Draws the state of an occurrence of <paramref name="color"/> in
    /// <paramref name="theme"/> and returns the ink of the texts over it
    /// (null: the theme's, as their styles have it, where nothing or only
    /// a light tint is under them): <paramref name="filled"/> false
    /// leaves a pill other than an unanswered one without its fill (a
    /// timed month pill: only its dot); <paramref name="showBar"/> the
    /// colour bar at the start, not drawn for an unanswered invitation nor
    /// where the occurrence continues before.
    /// </summary>
    public Color? Paint(
        Color color,
        Cal.BlockState state,
        bool selected,
        ElementTheme theme,
        double radius,
        bool showBar = true,
        bool filled = true,
        bool continuesBefore = false,
        bool continuesAfter = false)
    {
        var margin = new Thickness(continuesBefore ? -Overrun : 0, 0, continuesAfter ? -Overrun : 0, 0);
        foreach (var r in (Rectangle[])[fill, outline, ring])
        {
            r.RadiusX = radius;
            r.RadiusY = radius;
            r.Margin = margin;
        }
        var inset = CalendarMetrics.SelectionRing;
        gap.RadiusX = Math.Max(0, radius - inset);
        gap.RadiusY = gap.RadiusX;
        gap.Margin = new Thickness(margin.Left + inset, inset, margin.Right + inset, inset);

        Color? under = null;
        Color? ink = null;
        var barColor = color;
        var hatched = false;
        if (state == Cal.BlockState.Unanswered)
        {
            under = CalendarPalette.Surface(theme);
            ink = CalendarPalette.InkOn(color, theme);
        }
        else if (filled && state == Cal.BlockState.Tentative)
        {
            under = CalendarPalette.Tint(color, CalendarMetrics.TentativeTint, theme);
            hatched = true;
        }
        else if (filled && state is Cal.BlockState.Declined or Cal.BlockState.Cancelled)
        {
            under = CalendarPalette.Tint(color, CalendarMetrics.FadedTint, theme);
        }
        else if (filled)
        {
            under = color;
            ink = CalendarPalette.TextOn(color);
            barColor = CalendarPalette.Shade(color);
        }
        fill.Fill = under is { } u ? new SolidColorBrush(u) : null;
        outline.Stroke = state == Cal.BlockState.Unanswered ? new SolidColorBrush(color) : null;
        bar.Width = hatched ? CalendarMetrics.TentativeBar : CalendarMetrics.BlockBar;
        if (hatched)
        {
            hatch = (color, CalendarPalette.Surface(theme));
            hatchScale = 0;
            RefreshHatch();
        }
        else
        {
            hatch = null;
            bar.Fill = new SolidColorBrush(barColor);
        }
        if (IsLoaded)
        {
            Watch(hatch is null ? null : XamlRoot);
        }
        bar.Visibility = showBar && !continuesBefore && state != Cal.BlockState.Unanswered ? Visibility.Visible : Visibility.Collapsed;
        ring.Visibility = selected ? Visibility.Visible : Visibility.Collapsed;
        gap.Visibility = ring.Visibility;
        return ink;
    }

    /// <summary>
    /// Gives <paramref name="ink"/> to the texts and symbols of
    /// <paramref name="elements"/> (a panel's children too); null gives
    /// them back their styles' theme colours.
    /// </summary>
    public static void Ink(Color? ink, params UIElement?[] elements)
    {
        ArgumentNullException.ThrowIfNull(elements);
        var brush = ink is { } c ? new SolidColorBrush(c) : null;
        foreach (var e in elements)
        {
            Apply(e, brush);
        }
    }

    private static void Apply(UIElement? e, SolidColorBrush? brush)
    {
        switch (e)
        {
            case TextBlock t when brush is null:
                t.ClearValue(TextBlock.ForegroundProperty);
                break;
            case TextBlock t:
                t.Foreground = brush;
                break;
            case IconElement i when brush is null:
                i.ClearValue(IconElement.ForegroundProperty);
                break;
            case IconElement i:
                i.Foreground = brush;
                break;
            case Panel p:
                foreach (var child in p.Children)
                {
                    Apply(child, brush);
                }
                break;
        }
    }

    // Follows the window's change of display scale (moved to another
    // monitor) while a hatched bar shows; null lets go.
    private void Watch(XamlRoot? root)
    {
        if (ReferenceEquals(root, watched))
        {
            return;
        }
        if (watched is not null)
        {
            watched.Changed -= OnRootChanged;
        }
        watched = root;
        if (watched is not null)
        {
            watched.Changed += OnRootChanged;
        }
    }

    private void OnRootChanged(XamlRoot sender, XamlRootChangedEventArgs args) => RefreshHatch();

    // The tentative bar's hatching for the scale the view is shown at (1
    // until it is in a window), made again only when the scale changes.
    private void RefreshHatch()
    {
        if (hatch is not { } h)
        {
            return;
        }
        var scale = XamlRoot?.RasterizationScale is > 0 and var s ? s : 1;
        if (scale == hatchScale)
        {
            return;
        }
        hatchScale = scale;
        bar.Fill = Hatch(h.Ink, h.Ground, scale);
    }

    // Stripes at 45° from the top left to the bottom right, each the same
    // whole number of physical pixels along a pixel row (HatchStripe at the
    // display's scale, rounded half up: 2 px at 100 %, 3 px at 125 % and
    // 150 %, 4 px at 175 % and 200 %), first the colour, then the ground.
    // The brush is sampled at the pixels' centres, which fall on whole steps
    // of the pattern (the bar is at a whole pixel, layout rounding), so its
    // stops sit half a pixel off them: no pixel blends the two colours.
    // Spread by reflection, never by repetition: while a LinearGradientBrush
    // with GradientSpreadMethod.Repeat is drawn, WinUI draws the solid
    // fills, outlines, texts and icons it renders in the same pass with half
    // their red, green and blue (their alpha kept), which turned every block
    // of a Week or Day that showed a tentative event dark (measured on the
    // owner's machine, 2026-10-17: #1F6FEB came out #0F3775, the surface
    // #F9F9F9 #7C7C7C; Pad and Reflect draw nothing wrong). So one period of
    // the gradient is laid out symmetric about its middle, the colour in its
    // middle half, and the reflection repeats it unchanged.
    private static LinearGradientBrush Hatch(Color ink, Color ground, double scale)
    {
        var stripe = Math.Max(1, Math.Round(CalendarMetrics.HatchStripe * scale, MidpointRounding.AwayFromZero));
        var period = 2 * stripe;
        // The pattern's length along a row in DIPs; the gradient runs across
        // the stripes, so at a pixel's centre its parameter times the period
        // is the pixel's column minus its row, plus one and a half stripes,
        // less half a pixel: the points move along the gradient by that half
        // stripe less half a pixel (shift, in DIPs on each axis), so that the
        // colour's half of the period covers the same pixels a repeated
        // pattern starting with the colour half a pixel in would.
        var along = period / scale;
        var shift = ((stripe / 2) - 0.5) / (2 * scale);
        var b = new LinearGradientBrush
        {
            MappingMode = BrushMappingMode.Absolute,
            StartPoint = new Point(-shift, (along / 2) + shift),
            EndPoint = new Point((along / 2) - shift, shift),
            SpreadMethod = GradientSpreadMethod.Reflect,
        };
        b.GradientStops.Add(new GradientStop { Color = ground, Offset = 0 });
        b.GradientStops.Add(new GradientStop { Color = ground, Offset = 0.25 });
        b.GradientStops.Add(new GradientStop { Color = ink, Offset = 0.25 });
        b.GradientStops.Add(new GradientStop { Color = ink, Offset = 0.75 });
        b.GradientStops.Add(new GradientStop { Color = ground, Offset = 0.75 });
        b.GradientStops.Add(new GradientStop { Color = ground, Offset = 1 });
        return b;
    }
}
