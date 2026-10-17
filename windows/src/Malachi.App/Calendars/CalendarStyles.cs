// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the styles of CalendarStyles.xaml (merged into
// App.xaml) for the elements the calendar's views make in code. A style's
// {ThemeResource} setters resolve against the element they are set on, so
// a block, a line or a number made in code follows its window's theme
// (light, dark, high contrast) and repaints when it changes, where a brush
// looked up from code would be the application's theme's.

using Microsoft.UI.Xaml;

namespace Malachi.App.Calendars;

/// <summary>The calendar's styles by key.</summary>
internal static class CalendarStyles
{
    /// <summary>The style <paramref name="key"/> of CalendarStyles.xaml (or of the app's resources).</summary>
    public static Style Get(string key) => (Style)Application.Current.Resources[key];
}
