// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/EventBlockView.swift
// (CalendarColor.color, CalendarColor.of, BlockPainter.background,
// BlockPainter.tint); GTK: the calendar's views follow (docs/calendar.md
// §6.4).
//
// The calendars' colours: a calendar's "#rrggbb" (the daemon always sends
// one; anything else is the accent colour), an occurrence's (an invitation
// not in a calendar: the accent), the views' opaque surface in the
// element's theme (CalendarStyles.xaml's CalendarSurfaceColor, light and
// dark) and the opaque tints blended over it, so that no grid line shows
// through a block (the owner's finding of the live test, docs/calendar.md
// §5.3). Windows: a normal block, bar or all-day pill is filled with the
// saturated colour itself, not macOS's 18 % tint (the owner's decision of
// 2026-10-17 after the Windows live test), so the text on it is chosen by
// contrast (TextOn: white while white has 4.5:1 against the fill, WCAG's
// threshold for normal text, otherwise near-black; the owner's second
// finding of 2026-10-17 over his Outlook calendars, where 3:1 left white
// too weak on the lighter colours); the text in the colour on the surface
// (an unanswered invitation) is shaded until it has 4.5:1 (InkOn), and the
// colour bar of a filled block is the colour 25 % towards black (Shade).
// A tentative occurrence's light fill (Tint, 25 %) keeps the theme's texts.
// The contrast is WCAG 2's (relative luminance, sRGB). The theme is always
// resolved to Light or Dark (ThemeOf, Resolve): ElementTheme.Default is
// the window's or the application's theme, never the dark dictionary that
// CalendarStyles.xaml keys "Default" (Windows).

using System;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Microsoft.UI.Xaml;
using Windows.UI;

namespace Malachi.App.Calendars;

/// <summary>The calendars' colours, the opaque fills of the views and the inks of their texts (macOS <c>CalendarColor</c>, <c>BlockPainter</c>).</summary>
internal static class CalendarPalette
{
    // The surfaces of CalendarStyles.xaml, should the dictionary not be found.
    private static readonly Color LightSurface = Color.FromArgb(255, 0xF9, 0xF9, 0xF9);
    private static readonly Color DarkSurface = Color.FromArgb(255, 0x28, 0x28, 0x28);
    private static readonly Color FallbackAccent = Color.FromArgb(255, 0x00, 0x67, 0xC0);

    // The two inks of the texts on a fill: white, and the near-black of the
    // light theme's primary text (TextFillColorPrimary, #E4000000 over white).
    private static readonly Color White = Color.FromArgb(255, 0xFF, 0xFF, 0xFF);
    private static readonly Color DarkInk = Color.FromArgb(255, 0x1B, 0x1B, 0x1B);
    private static readonly Color Black = Color.FromArgb(255, 0, 0, 0);

    // The contrast a text must have against what it is drawn on, WCAG's
    // 4.5:1 for normal text (the texts are 11 and 12 DIP): white on a fill
    // (below it, a colour such as #0082c9, #e5534b or a pastel, the text is
    // near-black) and a text in the calendar's colour on the surface.
    private const double MinTextContrast = 4.5;

    private static ResourceDictionary? styles;

    /// <summary>The system's accent colour.</summary>
    public static Color Accent =>
        Application.Current?.Resources.TryGetValue("SystemAccentColor", out var v) == true && v is Color c ? c : FallbackAccent;

    /// <summary>A calendar's colour from its "#rrggbb"; the accent for anything else.</summary>
    public static Color Parse(string? hex)
    {
        if (hex is not { Length: 7 } || hex[0] != '#')
        {
            return Accent;
        }
        uint v = 0;
        for (var i = 1; i < 7; i++)
        {
            var d = hex[i] switch
            {
                >= '0' and <= '9' => hex[i] - '0',
                >= 'a' and <= 'f' => hex[i] - 'a' + 10,
                >= 'A' and <= 'F' => hex[i] - 'A' + 10,
                _ => -1,
            };
            if (d < 0)
            {
                return Accent;
            }
            v = (v << 4) | (uint)d;
        }
        return Color.FromArgb(255, (byte)(v >> 16), (byte)(v >> 8), (byte)v);
    }

    /// <summary>The colour of an occurrence's calendar in <paramref name="snapshot"/> (an invitation not in a calendar: the accent).</summary>
    public static Color Of(Occurrence o, Cal.Snapshot snapshot)
    {
        ArgumentNullException.ThrowIfNull(o);
        ArgumentNullException.ThrowIfNull(snapshot);
        return o.CalendarId is { } id && snapshot.FindCalendar(id) is { } c ? Parse(c.Color) : Accent;
    }

    /// <summary>
    /// The theme <paramref name="element"/> is drawn in, Light or Dark: its
    /// ActualTheme, else (ElementTheme.Default) its window's root's, else
    /// the application's; never Default.
    /// </summary>
    public static ElementTheme ThemeOf(FrameworkElement element)
    {
        ArgumentNullException.ThrowIfNull(element);
        if (element.ActualTheme != ElementTheme.Default)
        {
            return element.ActualTheme;
        }
        if (element.XamlRoot?.Content is FrameworkElement root && root.ActualTheme != ElementTheme.Default)
        {
            return root.ActualTheme;
        }
        return ApplicationElementTheme();
    }

    /// <summary><paramref name="theme"/> resolved to Light or Dark: Default is the application's theme.</summary>
    public static ElementTheme Resolve(ElementTheme theme) => theme == ElementTheme.Default ? ApplicationElementTheme() : theme;

    /// <summary>The views' opaque surface in <paramref name="theme"/> (CalendarStyles.xaml's CalendarSurfaceColor; Default resolved).</summary>
    public static Color Surface(ElementTheme theme)
    {
        var light = Resolve(theme) == ElementTheme.Light;
        if (Styles() is { } d && d.ThemeDictionaries.TryGetValue(light ? "Light" : "Default", out var t)
            && t is ResourceDictionary themed && themed.TryGetValue("CalendarSurfaceColor", out var v) && v is Color c)
        {
            return c;
        }
        return light ? LightSurface : DarkSurface;
    }

    /// <summary>
    /// <paramref name="color"/> at <paramref name="fraction"/> over the
    /// surface of <paramref name="theme"/>, opaque (BlockPainter.tint).
    /// </summary>
    public static Color Tint(Color color, double fraction, ElementTheme theme) => Blend(Surface(theme), color, fraction);

    /// <summary>
    /// The text on a block, bar or pill filled with <paramref name="fill"/>:
    /// white while white has 4.5:1 against it (a relative luminance up to
    /// 0.183), near-black #1B1B1B on a lighter colour (Windows).
    /// </summary>
    public static Color TextOn(Color fill) => Contrast(White, fill) >= MinTextContrast ? White : DarkInk;

    /// <summary>
    /// <paramref name="color"/> as a text on the surface of
    /// <paramref name="theme"/>: the colour itself while it has 4.5:1
    /// against the surface, else shaded towards black (light) or white
    /// (dark) in steps of 5 % until it has (Windows).
    /// </summary>
    public static Color InkOn(Color color, ElementTheme theme)
    {
        var surface = Surface(theme);
        var towards = Resolve(theme) == ElementTheme.Light ? Black : White;
        for (var step = 0; step < 20; step++)
        {
            var c = Blend(color, towards, step * 0.05);
            if (Contrast(c, surface) >= MinTextContrast)
            {
                return c;
            }
        }
        return towards;
    }

    /// <summary>The colour bar of a block filled with <paramref name="color"/>: the colour 25 % towards black (Windows).</summary>
    public static Color Shade(Color color) => Blend(color, Black, CalendarMetrics.BarShade);

    /// <summary>WCAG 2's contrast ratio of two opaque colours, 1 to 21.</summary>
    public static double Contrast(Color a, Color b)
    {
        var la = Luminance(a);
        var lb = Luminance(b);
        return (Math.Max(la, lb) + 0.05) / (Math.Min(la, lb) + 0.05);
    }

    /// <summary>WCAG 2's relative luminance of an opaque sRGB colour, 0 (black) to 1 (white).</summary>
    public static double Luminance(Color c)
    {
        static double Linear(byte v)
        {
            var s = v / 255.0;
            return s <= 0.04045 ? s / 12.92 : Math.Pow((s + 0.055) / 1.055, 2.4);
        }
        return (0.2126 * Linear(c.R)) + (0.7152 * Linear(c.G)) + (0.0722 * Linear(c.B));
    }

    /// <summary><paramref name="over"/> blended towards <paramref name="color"/> by <paramref name="fraction"/>, opaque.</summary>
    public static Color Blend(Color over, Color color, double fraction)
    {
        var f = Math.Clamp(fraction, 0, 1);
        byte Mix(byte a, byte b) => (byte)Math.Round(a + ((b - a) * f));
        return Color.FromArgb(255, Mix(over.R, color.R), Mix(over.G, color.G), Mix(over.B, color.B));
    }

    // The application's theme as an element's (Light without an application).
    private static ElementTheme ApplicationElementTheme() =>
        Application.Current?.RequestedTheme == ApplicationTheme.Dark ? ElementTheme.Dark : ElementTheme.Light;

    // The merged CalendarStyles.xaml of App.xaml.
    private static ResourceDictionary? Styles()
    {
        if (styles is not null || Application.Current is null)
        {
            return styles;
        }
        foreach (var d in Application.Current.Resources.MergedDictionaries)
        {
            if (d.Source?.OriginalString.EndsWith("CalendarStyles.xaml", StringComparison.OrdinalIgnoreCase) == true)
            {
                styles = d;
                break;
            }
        }
        return styles;
    }
}
