// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"encoding/json"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// The daemon's calendar notifications (docs/api.md §5, docs/calendar.md
// §4.7) and the account and connection changes the calendar's source
// follows. notify.go hands every method it has no case of to
// otherNotification, so the calendar's routing lives here: the four
// calendar notifications go to the window's calendar source
// (DataSource.Handle; a source not made yet needs none — its first load
// reads everything) and notify.eventAlert also to the alerts
// (calendarAlert, calendar_alerts.go).

// otherNotification is a notification notify.go has no case of: the
// calendar's, else only logged.
func (w *Window) otherNotification(method string, params json.RawMessage) {
	if calendardata.IsCalendarNotification(method) {
		w.calendarNotification(method, params)
		return
	}
	w.log.Info("notification", "method", method)
}

// calendarNotification routes one of the four calendar notifications.
func (w *Window) calendarNotification(method string, params json.RawMessage) {
	if s := w.cal.source; s != nil {
		s.Handle(method, params)
	}
	if method == api.NotifyEventAlert {
		w.calendarAlert(params)
	}
}

// modeAccountsChanged is notify.accountsChanged's effect on the Board's
// and the Calendar's sources.
func (w *Window) modeAccountsChanged() {
	w.boardAccountsChanged()
	w.calendarAccountsChanged()
}

// modeConnectionChanged mirrors the daemon connection into the Board's
// and the Calendar's sources (showConnectionState).
func (w *Window) modeConnectionChanged(connected bool) {
	w.boardConnectionChanged(connected)
	w.calendarConnectionChanged(connected)
	if connected {
		// The contact photos are asked again of the daemon.
		w.photoHub().connected()
	}
}

// calendarAccountsChanged: the daemon's calendar source lists the accounts
// and calendars again.
func (w *Window) calendarAccountsChanged() {
	if d := w.cal.daemon; d != nil {
		d.AccountsChanged()
	}
}

// calendarConnectionChanged: the daemon's calendar source loads everything
// again on connecting, and keeps its snapshot as unavailable without a
// connection.
func (w *Window) calendarConnectionChanged(connected bool) {
	if d := w.cal.daemon; d != nil {
		d.ConnectionChanged(connected)
	}
}
