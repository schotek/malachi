// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Icons.swift (IconSize, Icon.image,
// the named accessors); the table itself is Core's IconGlyphs (GTK icon
// name to Segoe Fluent Icons glyph). The sizes are GTK's: symbolic icons
// are 16 px, the sidebar's 14 px (ui/internal/style), a status page's
// illustration 96 px. The glyphs come from the theme's symbol font
// (SymbolThemeFontFamily, Segoe Fluent Icons on Windows 11). A name the
// table does not know shows a question mark and is logged once, with the
// name (never mail data).
//
// In XAML: <FontIcon Glyph="{x:Bind res:Icons.Glyph('view-refresh')}" />, or
// Icons.Create / Icons.Source from code.

using System.Collections.Concurrent;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Resources;

/// <summary>Segoe Fluent Icons for the GTK icon names of the Blueprints and the Go UI.</summary>
public static partial class Icons
{
    /// <summary>About 14 px: sidebar rows, chips, the star of a list row.</summary>
    public const double Small = 14;

    /// <summary>16 px: buttons, banners, the status line.</summary>
    public const double Regular = 16;

    /// <summary>96 px: the illustration of a status page.</summary>
    public const double Status = 96;

    private static readonly ConcurrentDictionary<string, byte> Reported = new();

    /// <summary>Where an unknown name is reported (the app's log once it runs).</summary>
    public static ILogger Logger { get; set; } = NullLogger.Instance;

    /// <summary>The glyph of <paramref name="gtkName"/> (with or without <c>-symbolic</c>).</summary>
    public static string Glyph(string? gtkName)
    {
        if (IconGlyphs.TryGlyph(gtkName, out var glyph))
        {
            return glyph;
        }
        if (!string.IsNullOrEmpty(gtkName) && Reported.TryAdd(gtkName, 0))
        {
            LogUnknown(Logger, gtkName);
        }
        return glyph;
    }

    /// <summary>A FontIcon of <paramref name="gtkName"/> at <paramref name="size"/>.</summary>
    public static FontIcon Create(string? gtkName, double size = Regular) => new()
    {
        Glyph = Glyph(gtkName),
        FontFamily = SymbolFont,
        FontSize = size,
    };

    /// <summary>A FontIconSource of <paramref name="gtkName"/>, for IconSource properties (InfoBar, TitleBar, commands).</summary>
    public static FontIconSource Source(string? gtkName, double size = Regular) => new()
    {
        Glyph = Glyph(gtkName),
        FontFamily = SymbolFont,
        FontSize = size,
    };

    /// <summary>The theme's symbol font.</summary>
    public static FontFamily SymbolFont =>
        Application.Current?.Resources.TryGetValue("SymbolThemeFontFamily", out var font) == true && font is FontFamily f
            ? f
            : new FontFamily(IconGlyphs.FontFamily);

    [LoggerMessage(Level = LogLevel.Information, Message = "no glyph for GTK icon {Name}")]
    private static partial void LogUnknown(ILogger logger, string name);
}
