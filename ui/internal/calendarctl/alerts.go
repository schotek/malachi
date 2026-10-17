// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"encoding/json"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// The calendar's desktop notifications for the whole application
// (docs/calendar.md §5.9), the port of CalendarAlertsController.swift
// with the invitation part of NotificationService.swift: notify.eventAlert
// due becomes a request the notification layer presents (Join only with
// a video call, Snooze), dismissed (from any client) withdraws it; Snooze
// is event.snoozeAlert for 5 minutes, a click or the close button
// dismisses (event.dismissAlert). The application routes the daemon's
// notifications here itself (Handle), not through a window's source, so
// that an alert is shown once whatever the number of windows. A new
// message with an invitation or a cancellation is the mail notification,
// retitled by InvitationNotificationOf; notify.invitationsChanged carries
// nothing to present.

// NotificationCategory is which actions a calendar notification offers.
type NotificationCategory int

// The categories.
const (
	// CategoryNone: a plain notification (an invitation already
	// answered, a processed cancellation).
	CategoryNone NotificationCategory = iota
	// CategoryEventAlert: Snooze.
	CategoryEventAlert
	// CategoryEventAlertJoin: Join (calendar.Join), Snooze.
	CategoryEventAlertJoin
	// CategoryInvitation: Accept (the card's default target), Show.
	CategoryInvitation
	// CategoryCancellation: Remove from Calendar (invitation.applyCancel).
	CategoryCancellation
)

// AlertRequest is what the notification layer presents for one alert.
type AlertRequest struct {
	AlertID     api.AlertID
	Title, Body string
	// Join: the event has a video call (its link comes from event.get
	// when the user clicks Join, through the link confirmation).
	Join         bool
	EventID      api.EventID
	RecurrenceID api.DateOrTime
	AccountID    api.AccountID
	// Day is the day a click opens in the Calendar (zero when unknown).
	Day time.Time
}

// Category is the request's category.
func (r AlertRequest) Category() NotificationCategory {
	if r.Join {
		return CategoryEventAlertJoin
	}
	return CategoryEventAlert
}

// CalendarAlertsController presents and withdraws the alerts.
type CalendarAlertsController struct {
	Source calendardata.DataSource
	Opts   Options
	// Enabled reads Preferences → Notifications (desktop notifications):
	// false presents nothing (a dismissal still withdraws); nil is on.
	Enabled func() bool

	// OnPresent presents (or replaces) a notification.
	OnPresent func(AlertRequest)
	// OnWithdraw withdraws the notification of an alert.
	OnWithdraw func(api.AlertID)
	// OnError: a snooze or dismissal failed (a log line, not a toast).
	OnError func(string)

	shown map[api.AlertID]AlertRequest
}

// NewCalendarAlertsController is the application's alerts over src (the
// source the writes go to).
func NewCalendarAlertsController(src calendardata.DataSource, opts Options) *CalendarAlertsController {
	return &CalendarAlertsController{Source: src, Opts: opts, shown: map[api.AlertID]AlertRequest{}}
}

// Shown are the alerts presented and not withdrawn.
func (a *CalendarAlertsController) Shown() map[api.AlertID]AlertRequest { return a.shown }

// Handle takes a daemon notification (method and raw params): an alert
// is presented or withdrawn; true for the calendar's notifications (the
// others than notify.eventAlert need nothing here), false otherwise. A
// malformed payload is ignored.
func (a *CalendarAlertsController) Handle(method string, raw json.RawMessage) bool {
	if !calendardata.IsCalendarNotification(method) {
		return false
	}
	if method == api.NotifyEventAlert {
		var n api.EventAlertNotification
		if json.Unmarshal(raw, &n) == nil {
			a.HandleAlert(n.Alert)
		}
	}
	return true
}

// HandleAlert takes one alert.
func (a *CalendarAlertsController) HandleAlert(al api.EventAlert) {
	switch al.State {
	case api.AlertDue:
		if a.Enabled != nil && !a.Enabled() {
			return
		}
		text := calendar.AlertNotification(al, a.Opts.Clock(), a.Opts.Env)
		r := AlertRequest{AlertID: al.AlertID, Title: text.Title, Body: text.Body, Join: al.VideoCall,
			EventID: al.EventID, RecurrenceID: al.RecurrenceID, AccountID: al.AccountID}
		if al.Start != nil {
			r.Day = al.Start.In(a.Opts.Loc())
		} else if d, ok := calendar.ParseDate(al.StartDate, a.Opts.Loc()); ok {
			r.Day = d
		}
		a.shown[al.AlertID] = r
		if a.OnPresent != nil {
			a.OnPresent(r)
		}
	case api.AlertDismissed:
		a.withdraw(al.AlertID)
	}
}

// Snooze shows the alert again in calendar.SnoozeMinutes (the daemon
// sends due then).
func (a *CalendarAlertsController) Snooze(id api.AlertID) {
	a.withdraw(id)
	a.Source.SnoozeAlert(api.EventSnoozeAlertParams{AlertID: id, Minutes: calendar.SnoozeMinutes}, a.failed)
}

// Dismiss: the notification was clicked or closed; dismissed for every
// client.
func (a *CalendarAlertsController) Dismiss(id api.AlertID) {
	a.withdraw(id)
	a.Source.DismissAlert(api.EventDismissAlertParams{AlertID: id}, a.failed)
}

func (a *CalendarAlertsController) failed(e *api.Error) {
	if e != nil && a.OnError != nil {
		a.OnError(a.Opts.ErrorMessage(e, ""))
	}
}

func (a *CalendarAlertsController) withdraw(id api.AlertID) {
	if _, ok := a.shown[id]; !ok {
		return
	}
	delete(a.shown, id)
	if a.OnWithdraw != nil {
		a.OnWithdraw(id)
	}
}

// InvitationNotification is how a new message's notification reads when
// the message carries an invitation or a cancellation.
type InvitationNotification struct {
	Title, Body string
	Category    NotificationCategory
}

// InvitationNotificationOf is the notification of a new message with inv
// (MessageSummary.Invitation): an invitation or update with Accept and
// Show while unanswered, a cancellation with Remove from Calendar until
// processed; false for none or another kind (the mail notification as
// it is).
func InvitationNotificationOf(inv *api.InvitationSummary, env calendar.Env) (InvitationNotification, bool) {
	if inv == nil {
		return InvitationNotification{}, false
	}
	switch inv.Kind {
	case api.InvitationKindInvitation, api.InvitationKindUpdate:
		t := calendar.InvitationNotification(*inv, env)
		n := InvitationNotification{Title: t.Title, Body: t.Body}
		if inv.State == api.InvitationUnanswered {
			n.Category = CategoryInvitation
		}
		return n, true
	case api.InvitationKindCancelled:
		t := calendar.CancellationNotification(*inv, env)
		n := InvitationNotification{Title: t.Title, Body: t.Body}
		if inv.State != api.InvitationProcessed {
			n.Category = CategoryCancellation
		}
		return n, true
	}
	return InvitationNotification{}, false
}
