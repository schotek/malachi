// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package style

// The Week and Day views' static rules (docs/calendar.md §5.3, §6.5 G11;
// window/calendar_week.go, calendar_allday.go), appended to CalendarCSS
// after the colour classes. The blocks and the all-day bars are buttons
// without the flat class on purpose: Adwaita's button.flat:hover outranks
// .cal-c-N.cal-fill and would show the grid through a hovered block, while
// the plain button's background rules do not; these rules take away the
// rest of a button's look (padding, minimum size, shadow).
//
//	.cal-week-header           the day header row (40 px)
//	.cal-day-weekday,          a column's weekday and day number;
//	.cal-day-number            .cal-weekend greys a Saturday or Sunday,
//	.cal-today-number          today's number in a filled accent circle
//	.cal-allday-row,           the all-day row, its "All Day" caption and
//	.cal-allday-caption,       its day cells (a separator at each day's
//	.cal-allday-cell           start, weekends shaded)
//	.cal-allday-bar            an all-day bar; .cal-continues-before/after
//	                           square the end that goes on
//	.cal-block                 a timed block; .cal-block-title, -time,
//	                           -meta its lines, .cal-badge the "?" of an
//	                           unanswered invitation
//	.cal-selected              the selected block or bar
//	.cal-declined,             struck through; cancelled also faded
//	.cal-cancelled
//	.cal-hour-label            the hour axis's labels
const calendarWeekCSS = `.cal-week-header { min-height: 40px; }
.cal-day-weekday { font-size: smaller; opacity: 0.75; }
.cal-day-number { min-width: 24px; min-height: 24px; border-radius: 9999px; font-weight: normal; }
.cal-weekend { opacity: 0.5; }
.cal-day-number.cal-today-number { background-color: @accent_bg_color; color: @accent_fg_color; font-weight: bold; opacity: 1; }
.cal-allday-caption { font-size: smaller; opacity: 0.75; }
.cal-allday-cell { min-height: 36px; border-left: 1px solid alpha(currentColor, 0.15); }
.cal-allday-cell.cal-weekend { background-color: alpha(currentColor, 0.03); opacity: 1; }
.cal-allday-row { border-bottom: 1px solid alpha(currentColor, 0.15); }
button.cal-allday-bar, button.cal-block { padding: 0; margin: 0; min-height: 0; min-width: 0; box-shadow: none; border-radius: 4px; font-weight: normal; }
button.cal-allday-bar { min-height: 18px; margin: 1px 2px; padding: 0 6px; }
button.cal-allday-bar.cal-continues-before { border-top-left-radius: 0; border-bottom-left-radius: 0; margin-left: 0; }
button.cal-allday-bar.cal-continues-after { border-top-right-radius: 0; border-bottom-right-radius: 0; margin-right: 0; }
.cal-block .cal-bar { min-width: 3px; border-radius: 1.5px; }
.cal-block-title { font-size: smaller; font-weight: bold; }
.cal-block-time, .cal-block-meta { font-size: x-small; opacity: 0.75; }
.cal-badge { font-size: x-small; font-weight: bold; min-width: 13px; min-height: 13px; border-radius: 9999px; background-color: @accent_bg_color; color: @accent_fg_color; }
button.cal-selected { outline: 2px solid @accent_color; outline-offset: -2px; }
.cal-declined label, .cal-cancelled label { text-decoration-line: line-through; }
.cal-cancelled label { opacity: 0.6; }
.cal-hour-label { font-size: x-small; opacity: 0.7; }
`

func init() { registerCalendarCSS(calendarWeekCSS) }
