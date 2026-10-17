// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/EventBlockView.swift
// (CalendarMetrics) and the measures of CalendarPageViewController.swift,
// MiniMonthView.swift and CalendarSidebarViewController.swift; GTK: the
// calendar's views follow (docs/calendar.md §6.4).
//
// What the calendar's views lay out with (docs/calendar.md §5.3, the
// wireframe at 1280 × 820), in DIPs where macOS has points. Nothing here
// decides what an occurrence is; Core does (Cal.LayoutDay, Cal.AllDayLanes,
// Cal.MonthLanes, Cal.CellItems).

namespace Malachi.App.Calendars;

/// <summary>The calendar views' measures (macOS <c>CalendarMetrics</c>).</summary>
internal static class CalendarMetrics
{
    /// <summary>The height of an hour in the time grid.</summary>
    public const double HourHeight = 60;

    /// <summary>The hour axis left of the day columns.</summary>
    public const double AxisWidth = 60;

    /// <summary>The day header row of Week and Day.</summary>
    public const double DayHeaderHeight = 40;

    /// <summary>The all-day row at its smallest (one lane and air).</summary>
    public const double AllDayMinimum = 36;

    /// <summary>One lane of the all-day row.</summary>
    public const double LaneHeight = 18;

    /// <summary>The gap between two lanes of the all-day row.</summary>
    public const double LaneGap = 2;

    /// <summary>The most lanes the all-day row grows to (bars in further lanes are not drawn; the Agenda and the Month list them).</summary>
    public const int AllDayMaxLanes = 5;

    /// <summary>The coloured bar at a block's left edge (a tentative one's: <see cref="TentativeBar"/>).</summary>
    public const double BlockBar = 3;

    /// <summary>The hatched bar at the left edge of a tentative occurrence (Windows, Outlook's style).</summary>
    public const double TentativeBar = 4;

    /// <summary>
    /// A stripe of the tentative bar's hatching along a pixel row, the
    /// calendar's colour and then the surface, before the display's scale
    /// rounds it to whole pixels (Windows): about 3 DIP from stripe to
    /// stripe across the 45° hatching.
    /// </summary>
    public const double HatchStripe = 2;

    /// <summary>
    /// The fill of a declined or cancelled occurrence: 18 % of the
    /// calendar's colour over the surface (macOS's fill of every block;
    /// Windows fills a normal one with the colour itself, the owner's
    /// decision of 2026-10-17).
    /// </summary>
    public const double FadedTint = 0.18;

    /// <summary>
    /// The light fill of a tentative occurrence: 25 % of the calendar's
    /// colour over the surface, with the theme's texts (Windows, the
    /// owner's second decision of 2026-10-17: Outlook's style).
    /// </summary>
    public const double TentativeTint = 0.25;

    /// <summary>How far the colour bar of a filled block goes from its colour towards black (Windows).</summary>
    public const double BarShade = 0.25;

    /// <summary>The dashed outline of an unanswered invitation.</summary>
    public const double UnansweredOutline = 1.5;

    /// <summary>The selection's ring at a block's edge, in the accent colour (Windows).</summary>
    public const double SelectionRing = 2;

    /// <summary>The gap in the surface's colour between the selection's ring and the fill (Windows).</summary>
    public const double SelectionGap = 1;

    /// <summary>How far outside a block the keyboard's focus visual is drawn, clear of the selection's ring (Windows).</summary>
    public const double FocusVisualOffset = 3;

    /// <summary>A month cell's pill line.</summary>
    public const double PillLine = 17;

    /// <summary>A month cell's day number line.</summary>
    public const double CellNumberLine = 22;

    /// <summary>The week-number column of the month.</summary>
    public const double WeekNumberWidth = 28;

    /// <summary>The month's weekday header.</summary>
    public const double WeekdayHeaderHeight = 24;

    /// <summary>The page's sidebar (CalendarPageViewController.sidebarDefault).</summary>
    public const double SidebarWidth = 240;

    /// <summary>The narrowest sidebar before it folds (sidebarMinimum).</summary>
    public const double SidebarMinimum = 200;

    /// <summary>The narrowest content before the sidebar, then the panel, fold (contentMinimum).</summary>
    public const double ContentMinimum = 360;

    /// <summary>The mini month's title row.</summary>
    public const double MiniTitleHeight = 28;

    /// <summary>The mini month's weekday header.</summary>
    public const double MiniHeaderHeight = 18;

    /// <summary>A row of the mini month's days.</summary>
    public const double MiniRowHeight = 24;

    /// <summary>The width under which the bar's view segments become a menu (Windows: the bar has no overflow).</summary>
    public const double BarCompactWidth = 640;
}
