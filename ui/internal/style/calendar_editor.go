// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package style

// The New Event / Edit Event window's static rules (docs/calendar.md
// §5.6, event_editor.blp): the large title field, the link-like buttons
// (the time zone, Next Free Slot), the entry-like frame of the attendee
// row and the notes, and the attachment chips.
func init() {
	registerCalendarCSS(`entry.event-editor-title { font-size: 115%; min-height: 34px; }
.event-editor-link, menubutton.event-editor-link > button { color: @accent_color; padding: 2px 6px; min-height: 0; }
.event-editor-field-frame { border-radius: 6px; background-color: @view_bg_color; box-shadow: inset 0 0 0 1px alpha(currentColor, 0.15); }
box.event-editor-field-frame { padding: 2px 6px; min-height: 30px; }
box.event-editor-field-frame entry { background: none; box-shadow: none; min-height: 26px; }
box.event-editor-chip { padding: 2px 2px 2px 10px; border-radius: 99px; background-color: alpha(@window_fg_color, 0.07); font-size: 90%; }
box.event-editor-chip button { min-width: 18px; min-height: 18px; padding: 0; margin: 2px 0 2px 2px; }
`)
}

// EnsureCalendarCSS loads the calendar's stylesheet without colours when
// nothing has loaded it yet (an editor opened before the Calendar's page,
// which loads the colours with ApplyCalendar). Main loop only.
func EnsureCalendarCSS() {
	if calendarProvider == nil {
		ApplyCalendar(nil)
	}
}
