// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/plans/toolbar-customization.md W.2): the
// calendar's toolbar as the user arranged it. The page's bar holds the
// CalendarBar only once the window's calendar exists (AttachCalendar), so
// the layout and the display mode the window gives before are kept and
// handed to it then.

using System.Collections.Generic;
using Malachi.Core.Toolbars;
using Microsoft.UI.Xaml;

namespace Malachi.App.Calendars;

public sealed partial class CalendarPage
{
    private IReadOnlyList<Toolbar.Shown>? toolbar;
    private Toolbar.DisplayMode displayMode = Toolbar.DisplayMode.Icon;

    /// <summary>The page's bar (a right click on an empty spot offers Customize Toolbar…).</summary>
    internal FrameworkElement ToolbarBar => Bar;

    /// <summary>The calendar's part of Toolbar.PresentFor for toolbar-calendar (MainWindow.Toolbars.cs).</summary>
    internal void ApplyToolbar(IReadOnlyList<Toolbar.Shown> shown)
    {
        toolbar = shown;
        bar?.ApplyToolbar(shown);
    }

    /// <summary>The toolbars' display mode on the bar's icon buttons.</summary>
    internal void ApplyDisplayMode(Toolbar.DisplayMode mode)
    {
        displayMode = mode;
        bar?.Labels.Apply(mode);
    }
}
