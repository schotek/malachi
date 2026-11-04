// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package style

// The event detail popover's static rules (docs/calendar.md §5.5, G13;
// event_detail.blp, window/calendar_detail.go): the popover's own padding
// off (the content box carries the 16 px inset of macOS's
// EventDetailViewController, so that it is the 380 px of the artboard),
// the title bold at 17 px, the rows' icons in an 18 px column, the notes
// on a faint grey ground, the link buttons in the accent colour and as
// tight as text, and the attendees' response icons.
const calendarDetailCSS = `
popover.cal-detail > contents { padding: 0; }
.cal-detail-title { font-weight: 700; font-size: 17px; }
.cal-detail-icon { min-width: 18px; -gtk-icon-size: 14px; opacity: 0.7; margin-top: 1px; }
.cal-detail-swatch { min-width: 10px; min-height: 10px; border-radius: 3px; }
.cal-detail-notes { background-color: alpha(currentColor, 0.06); border-radius: 6px; }
.cal-detail-notes > textview, .cal-detail-notes textview > text { background-color: transparent; }
button.cal-detail-link { color: @accent_color; padding: 0 2px; min-height: 0; min-width: 0; }
.cal-detail-response { -gtk-icon-size: 12px; min-width: 14px; }
.cal-detail-response.accepted { color: @success_color; }
.cal-detail-response.declined { color: @error_color; }
.cal-detail-response.tentative { color: @warning_color; }
.cal-detail-response.other { opacity: 0.55; }
`

func init() { registerCalendarCSS(calendarDetailCSS) }
