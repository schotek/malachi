// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import "github.com/schotek/malachi/backend/pkg/api"

// ActiveCalendars are the calendars a client shows and offers: every one
// of calendar.list that is not Disabled (in its account's
// disabledCalendars), in the order given; never nil. It applies wherever
// calendars appear outside Preferences: the sidebar's rows and the set of
// visible calendars, the editor's Calendar popup, the detail's calendar
// name, the free/busy and search scopes, and the counts. Only
// Preferences → Calendar lists the disabled ones, so that their checkbox
// can switch them back on.
func ActiveCalendars(cals []api.Calendar) []api.Calendar {
	out := make([]api.Calendar, 0, len(cals))
	for _, c := range cals {
		if !c.Disabled {
			out = append(out, c)
		}
	}
	return out
}
