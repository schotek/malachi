// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package style

// Invitations in Mail (docs/calendar.md §5.7, §6.5 G15): the list's
// invitation pill (widget/pill.go SetInvitationPill: an invitation blue,
// an update orange, a cancellation red, a reply the pill's grey) and the
// invitation card above a message's headers (window/invitation_card.go,
// invitation_card.blp): its padding, the date tile with its band in the
// pill's colour, Join Video Call as a link, the conflict line in orange.
// Part of the application stylesheet (CSS), not of the calendar's
// provider: Mail shows these whether or not the Calendar ever opened.
const calendarCardCSS = `
label.issue-pill.invitation-pill { color: @blue_3; background-color: alpha(@blue_3, 0.18); }
label.issue-pill.invitation-update { color: @warning_color; background-color: alpha(@orange_3, 0.2); }
label.issue-pill.invitation-cancelled { color: @error_color; background-color: alpha(@error_bg_color, 0.12); }
label.issue-pill.invitation-reply { color: alpha(@window_fg_color, 0.75); }
box.invitation-card { padding: 10px 12px; }
box.invitation-tile { min-width: 52px; border-radius: 8px; border: 1px solid alpha(@window_fg_color, 0.15); background-color: @view_bg_color; }
box.invitation-tile.invitation-pill { border-color: alpha(@blue_3, 0.5); }
label.invitation-tile-band { padding: 2px 2px; border-top-left-radius: 7px; border-top-right-radius: 7px; font-size: 80%; font-weight: bold; color: white; background-color: @blue_3; }
box.invitation-tile.invitation-update label.invitation-tile-band { background-color: @orange_3; }
box.invitation-tile.invitation-cancelled label.invitation-tile-band { background-color: @red_3; }
box.invitation-tile.invitation-reply label.invitation-tile-band { background-color: alpha(@window_fg_color, 0.5); }
label.invitation-tile-day { font-size: 170%; font-weight: 600; margin-top: 2px; }
label.invitation-tile-month { margin-bottom: 6px; }
button.invitation-link { min-height: 0; padding: 0 4px; color: @accent_color; }
label.invitation-conflict { color: @warning_color; }
image.invitation-conflict { color: @warning_color; }
label.invitation-warning { color: @warning_color; }
`
