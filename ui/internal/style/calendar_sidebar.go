// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package style

// The Calendar sidebar's static rules (window/calendar_sidebar.go,
// docs/calendar.md §6.5 G10): the mini month (a 24 px row per week, the
// band of the shown days on the day buttons with rounded ends, today's
// number in a filled accent circle, other months' days grey), the colour
// square of a calendar (its colour from .cal-c-N.cal-swatch), the
// Unanswered row's count badge; and the Agenda's and the results' rows
// (window/calendar_agenda.go: the day headings, the colour dot, dashed
// for an unanswered invitation, the response capsule).
const calendarSidebarCSS = `
.cal-mini-grid { margin-top: 2px; }
.cal-mini-head { min-height: 18px; font-size: 0.8em; }
.cal-mini-step { min-width: 24px; min-height: 24px; padding: 0; }
button.cal-mini-day { min-width: 0; min-height: 24px; padding: 0; margin: 0; border-radius: 0; font-feature-settings: 'tnum'; }
button.cal-mini-day.cal-mini-band { background-color: alpha(@accent_bg_color, 0.15); }
button.cal-mini-day.cal-mini-band-start { border-top-left-radius: 12px; border-bottom-left-radius: 12px; }
button.cal-mini-day.cal-mini-band-end { border-top-right-radius: 12px; border-bottom-right-radius: 12px; }
.cal-mini-number { min-width: 21px; min-height: 21px; border-radius: 999px; font-size: 0.85em; }
.cal-mini-number.cal-mini-out { opacity: 0.4; }
.cal-mini-number.cal-mini-weekend { opacity: 0.7; }
.cal-mini-number.cal-mini-today { background-color: @accent_bg_color; color: @accent_fg_color; font-weight: bold; opacity: 1; }
.cal-square { min-width: 10px; min-height: 10px; border-radius: 2.5px; }
.cal-count-badge { background-color: @accent_bg_color; color: @accent_fg_color; border-radius: 999px; padding: 0 6px; min-width: 10px; font-size: 0.8em; font-weight: bold; font-feature-settings: 'tnum'; }
.cal-sidebar-list row.cal-sidebar-heading { min-height: 0; padding-top: 8px; padding-bottom: 2px; }
.cal-sidebar-list row.cal-calendar-row { min-height: 0; padding-top: 0; padding-bottom: 0; }
.cal-agenda-list { background: none; }
.cal-agenda-list row.cal-agenda-day { padding: 10px 20px 4px 20px; }
.cal-agenda-list row.cal-agenda-item { min-height: 28px; padding: 2px 20px; }
.cal-dot { min-width: 8px; min-height: 8px; border-radius: 999px; }
.cal-dot.cal-dot-accent.cal-swatch { background-color: @accent_bg_color; }
.cal-dot.cal-dot-accent.cal-unanswered { border: 1px dashed @accent_bg_color; }
.cal-dot.cal-unanswered { min-width: 6px; min-height: 6px; }
.cal-agenda-title { font-weight: 500; }
.cal-agenda-badge { border-radius: 999px; padding: 1px 8px; font-size: 0.8em; background-color: alpha(currentColor, 0.1); }
`

func init() { registerCalendarCSS(calendarSidebarCSS) }
