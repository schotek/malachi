// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/CalendarAccountGroups.swift
// (prefsCalendarColor, PrefsColorSwatch); GTK: none yet (Swift-first,
// docs/calendar.md §5.8). The colour square of a calendar in Preferences →
// Calendar and in the Add CalDAV Calendar window: 12 px, rounded, the
// calendar's #rrggbb, else the dim text colour of the window's theme.

using System;
using System.Globalization;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Windows.UI;

namespace Malachi.App.Preferences;

/// <summary>The colour square of a calendar.</summary>
internal static class CalendarSwatch
{
    /// <summary>
    /// <c>#rrggbb</c> as a colour (macOS <c>prefsCalendarColor</c>); null for
    /// anything else, which keeps the style's dim colour.
    /// </summary>
    public static Color? Parse(string? hex)
    {
        var s = (hex ?? "").Trim();
        if (s.StartsWith('#'))
        {
            s = s[1..];
        }
        if (s.Length != 6 || !uint.TryParse(s, NumberStyles.AllowHexSpecifier, CultureInfo.InvariantCulture, out var v))
        {
            return null;
        }
        return Color.FromArgb(0xff, (byte)((v >> 16) & 0xff), (byte)((v >> 8) & 0xff), (byte)(v & 0xff));
    }

    /// <summary>
    /// The square of <paramref name="hex"/>, styled from
    /// <paramref name="resources"/>' <c>CalendarSwatchStyle</c>
    /// (SettingsStyles.xaml), so that its fallback follows the window's theme.
    /// </summary>
    public static Border Make(FrameworkElement resources, string? hex)
    {
        ArgumentNullException.ThrowIfNull(resources);
        var swatch = new Border();
        if (resources.Resources.TryGetValue("CalendarSwatchStyle", out var style) && style is Style s)
        {
            swatch.Style = s;
        }
        else
        {
            swatch.Width = 12;
            swatch.Height = 12;
            swatch.CornerRadius = new CornerRadius(3);
        }
        AutomationProperties.SetAccessibilityView(swatch, Microsoft.UI.Xaml.Automation.Peers.AccessibilityView.Raw);
        Paint(swatch, hex);
        return swatch;
    }

    /// <summary>Sets <paramref name="swatch"/>'s colour to <paramref name="hex"/>, or back to the style's for none.</summary>
    public static void Paint(Border swatch, string? hex)
    {
        ArgumentNullException.ThrowIfNull(swatch);
        if (Parse(hex) is { } color)
        {
            swatch.Background = new SolidColorBrush(color);
        }
        else
        {
            swatch.ClearValue(Border.BackgroundProperty);
        }
    }
}
