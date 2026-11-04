// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package style

// The Month view's static rules (window/calendar_month.go,
// docs/calendar.md §5.4, §6.5 G12): the weekday header and the week
// numbers small and dim, the day number (today's in a filled accent
// circle, other months' grey), the pills, bars and "+N more" as flat
// 17 px buttons whose height the view counts on (CellCapacity), the
// pill's colour dot, the bar's ends squared where the event continues
// (cal-continues-before/after), the selected pill or bar outlined in the
// accent, a cancelled one faded. The colours come from .cal-c-N.
const calendarMonthCSS = `
.cal-month-weekday { font-size: 0.8em; opacity: 0.6; min-height: 24px; }
.cal-month-week { font-size: 0.75em; opacity: 0.45; font-feature-settings: 'tnum'; margin-top: 4px; }
.cal-month-day { min-width: 20px; min-height: 20px; border-radius: 999px; font-size: 0.85em; font-feature-settings: 'tnum'; }
.cal-month-day.cal-month-out { opacity: 0.4; }
.cal-month-day.cal-month-today { background-color: @accent_bg_color; color: @accent_fg_color; font-weight: bold; opacity: 1; }
button.cal-month-pill, button.cal-month-bar, button.cal-month-more { min-height: 0; min-width: 0; padding: 0 4px; margin: 0; border-radius: 3px; font-size: 0.8em; font-weight: normal; }
button.cal-month-pill { padding-left: 3px; }
button.cal-month-bar { padding-left: 0; }
button.cal-month-bar.cal-continues-before { padding-left: 4px; border-top-left-radius: 0; border-bottom-left-radius: 0; }
button.cal-month-bar.cal-continues-after { border-top-right-radius: 0; border-bottom-right-radius: 0; }
button.cal-month-bar .cal-bar { min-width: 3px; border-radius: 1.5px; }
button.cal-month-pill .cal-month-dot { min-width: 6px; min-height: 6px; border-radius: 3px; }
button.cal-month-more { opacity: 0.7; }
.cal-month-time { font-feature-settings: 'tnum'; opacity: 0.7; }
button.cal-month-pill.cal-month-selected, button.cal-month-bar.cal-month-selected { box-shadow: inset 0 0 0 1px @accent_color; }
button.cal-month-pill.cal-month-cancelled, button.cal-month-bar.cal-month-cancelled { opacity: 0.55; }
.cal-month-popover-heading { font-weight: bold; margin-bottom: 4px; }
`

func init() { registerCalendarCSS(calendarMonthCSS) }
