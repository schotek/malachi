// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendarctl"
	"github.com/schotek/malachi/ui/internal/calendardata"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The calendar's desktop notifications (docs/calendar.md §5.9, §6.5 G18),
// the port of the calendar part of NotificationService.swift and
// Integration+Calendar.swift. An event's alert (notify.eventAlert due,
// calendar_notify.go → calendarAlert) is a gio.Notification under
// alertNotificationID: its click opens the event in the Calendar with
// its detail (app.calendar-open-event), Join only with a video call
// (app.calendar-join: the link from event.get, never the notification,
// through openLink), Snooze (app.calendar-snooze, 5 minutes); a click or
// Join dismisses it for every client, and dismissed from any client
// withdraws it. GNotification has no per-notification categories, so
// each notification carries its own buttons, and it reports no close:
// closing one dismisses nothing (the daemon's alert stays due for the
// other clients). A new message with an invitation or a cancellation is
// the mail notification retitled (withInvitation, called by
// notifyNewMessage): Accept (app.calendar-accept-invitation) and Show
// (app.open-message) while unanswered, Remove from Calendar
// (app.calendar-remove-cancelled) until processed; both go through an
// InvitationController over the window's calendar source and the
// message's card (message.get). The alerts follow Preferences →
// Notifications (desktop-notifications, the sound under its own key) and
// are shown in every mode: they are about the time, not a folder.

// The application actions of the calendar's notifications (registered in
// main.go by CalendarNotificationActions).
const (
	CalendarOpenEventAction         = "calendar-open-event"
	CalendarJoinAction              = "calendar-join"
	CalendarSnoozeAction            = "calendar-snooze"
	CalendarAcceptInvitationAction  = "calendar-accept-invitation"
	CalendarRemoveCancelledAction   = "calendar-remove-cancelled"
	calendarAlertTargetVariantType  = "(ssssx)"
	calendarNotificationIconName    = "x-office-calendar-symbolic"
	calendarAlertNotificationPrefix = "alert-"
)

// alertNotificationID is the notification id of an event's alert.
func alertNotificationID(id api.AlertID) string { return calendarAlertNotificationPrefix + string(id) }

// CalendarAlertTarget is the parameter of the alert actions: the alert,
// the account, the event and its instance, and the day the Calendar opens
// (zero: today).
type CalendarAlertTarget struct {
	AlertID      api.AlertID
	AccountID    api.AccountID
	EventID      api.EventID
	RecurrenceID api.DateOrTime
	Day          time.Time
}

// alertTargetOf is the target of an alert's notification.
func alertTargetOf(r calendarctl.AlertRequest) CalendarAlertTarget {
	return CalendarAlertTarget{AlertID: r.AlertID, AccountID: r.AccountID, EventID: r.EventID,
		RecurrenceID: r.RecurrenceID, Day: r.Day}
}

// CalendarAlertTargetType is the parameter type of the alert actions:
// alert, account, event, recurrence id ("" none) and the day in Unix
// seconds (0 none).
func CalendarAlertTargetType() *glib.VariantType {
	return glib.NewVariantType(calendarAlertTargetVariantType)
}

// Variant is t as the alert actions' parameter.
func (t CalendarAlertTarget) Variant() *glib.Variant {
	var day int64
	if !t.Day.IsZero() {
		day = t.Day.Unix()
	}
	return glib.NewVariantTuple([]*glib.Variant{
		glib.NewVariantString(string(t.AlertID)),
		glib.NewVariantString(string(t.AccountID)),
		glib.NewVariantString(string(t.EventID)),
		glib.NewVariantString(string(t.RecurrenceID)),
		glib.NewVariantInt64(day),
	})
}

// ParseCalendarAlertTarget reads the alert actions' parameter. ok is
// false for another type or without the alert and the event: the actions
// are public on the session bus, so the parameter is no more trusted than
// a click.
func ParseCalendarAlertTarget(v *glib.Variant) (CalendarAlertTarget, bool) {
	if v == nil || !v.IsOfType(CalendarAlertTargetType()) {
		return CalendarAlertTarget{}, false
	}
	t := CalendarAlertTarget{
		AlertID:      api.AlertID(v.ChildValue(0).String()),
		AccountID:    api.AccountID(v.ChildValue(1).String()),
		EventID:      api.EventID(v.ChildValue(2).String()),
		RecurrenceID: api.DateOrTime(v.ChildValue(3).String()),
	}
	if s := v.ChildValue(4).Int64(); s > 0 {
		t.Day = time.Unix(s, 0).In(time.Local)
	}
	if t.AlertID == "" || t.EventID == "" {
		return CalendarAlertTarget{}, false
	}
	return t, true
}

// notificationButton is one button of a calendar notification (or its
// default action, Label ""): the app action and its parameter.
type notificationButton struct {
	Label, Action string
	Target        *glib.Variant
}

// calendarNote is what a calendar notification shows: its title, body,
// default action and buttons, built without GTK (the test reads it).
type calendarNote struct {
	Title, Body string
	Default     notificationButton
	Buttons     []notificationButton
}

// alertNote is the notification of an alert: Join only with a video
// call, then Snooze.
func alertNote(r calendarctl.AlertRequest, tr calendar.Translator) calendarNote {
	t := alertTargetOf(r).Variant()
	n := calendarNote{Title: capNotificationText(r.Title), Body: capNotificationText(r.Body),
		Default: notificationButton{Action: "app." + CalendarOpenEventAction, Target: t}}
	if r.Category() == calendarctl.CategoryEventAlertJoin {
		n.Buttons = append(n.Buttons, notificationButton{calendar.Join(tr), "app." + CalendarJoinAction, t})
	}
	n.Buttons = append(n.Buttons, notificationButton{calendar.Snooze(tr), "app." + CalendarSnoozeAction, t})
	return n
}

// invitationButtons are the buttons of a new message's notification with
// an invitation (Accept, Show) or a cancellation (Remove from Calendar);
// none for a plain one.
func invitationButtons(c calendarctl.NotificationCategory, acc api.AccountID, id api.MessageID,
	tr calendar.Translator) []notificationButton {
	t := NotificationTarget(acc, id)
	switch c {
	case calendarctl.CategoryInvitation:
		return []notificationButton{
			{calendar.ResponseAction(api.ResponseAccepted, tr), "app." + CalendarAcceptInvitationAction, t},
			{calendar.Show(tr), "app." + OpenMessageAction, t},
		}
	case calendarctl.CategoryCancellation:
		return []notificationButton{
			{calendar.RemoveFromCalendar(tr), "app." + CalendarRemoveCancelledAction, t},
		}
	}
	return nil
}

// capNotificationText trims a line of a notification and caps it like
// notificationText (the calendar already cleaned it).
func capNotificationText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > notificationBodyMax {
		s = strings.ToValidUTF8(s[:notificationBodyMax], "") + "…"
	}
	return s
}

// calendarAlert is notify.eventAlert's raw params (calendar_notify.go).
func (w *Window) calendarAlert(raw json.RawMessage) {
	w.calendarAlerts().Handle(api.NotifyEventAlert, raw)
}

// calendarAlerts is the window's alerts controller over its calendar
// source, made on the first alert or action (one main window: one
// controller for the application).
func (w *Window) calendarAlerts() *calendarctl.CalendarAlertsController {
	if a := w.cal.alerts; a != nil {
		return a
	}
	a := calendarctl.NewCalendarAlertsController(w.calendarSource(), w.calendarOptions())
	a.Enabled = func() bool { return w.settings == nil || w.settings.DesktopNotifications() }
	a.OnPresent = w.presentAlert
	a.OnWithdraw = func(id api.AlertID) { w.app.WithdrawNotification(alertNotificationID(id)) }
	a.OnError = func(text string) { w.log.Warn("calendar alert", "err", text) }
	w.cal.alerts = a
	return a
}

// presentAlert sends (or replaces) an alert's notification, with the
// new-mail sound under its own setting.
func (w *Window) presentAlert(r calendarctl.AlertRequest) {
	if w.settings != nil && w.settings.NotificationSound() {
		w.playNewMailSound()
	}
	w.app.SendNotification(alertNotificationID(r.AlertID), alertNote(r, i18n.Tr).notification())
}

// notification is n as a gio.Notification.
func (n calendarNote) notification() *gio.Notification {
	note := gio.NewNotification(n.Title)
	note.SetBody(n.Body)
	note.SetIcon(gio.NewThemedIcon(calendarNotificationIconName))
	if n.Default.Action != "" {
		note.SetDefaultActionAndTarget(n.Default.Action, n.Default.Target)
	}
	for _, b := range n.Buttons {
		note.AddButtonWithTarget(b.Label, b.Action, b.Target)
	}
	return note
}

// withInvitation retitles a new message's notification note when the
// message carries an invitation or a cancellation
// (calendarctl.InvitationNotificationOf) and adds its buttons; note as it
// is otherwise.
func (w *Window) withInvitation(note *gio.Notification, n api.NewMessageNotification) *gio.Notification {
	inv, ok := calendarctl.InvitationNotificationOf(n.Message.Invitation, w.calendarEnv())
	if !ok {
		return note
	}
	if t := capNotificationText(inv.Title); t != "" {
		note.SetTitle(t)
	}
	if b := capNotificationText(inv.Body); b != "" {
		note.SetBody(b)
	}
	note.SetIcon(gio.NewThemedIcon(calendarNotificationIconName))
	for _, b := range invitationButtons(inv.Category, n.AccountID, n.Message.ID, i18n.Tr) {
		note.AddButtonWithTarget(b.Label, b.Action, b.Target)
	}
	return note
}

// CalendarAlertAction answers an alert's notification: action is
// CalendarOpenEventAction (the click: the Calendar with the event's
// detail), CalendarJoinAction or CalendarSnoozeAction.
func (w *Window) CalendarAlertAction(action string, t CalendarAlertTarget) {
	alerts := w.calendarAlerts()
	switch action {
	case CalendarOpenEventAction:
		alerts.Dismiss(t.AlertID)
		w.showInCalendar(t.EventID, t.RecurrenceID, t.Day)
	case CalendarJoinAction:
		alerts.Dismiss(t.AlertID)
		w.joinAlertCall(t)
	case CalendarSnoozeAction:
		alerts.Snooze(t.AlertID)
	}
}

// joinAlertCall opens the alerted event's video call: its link from
// event.get, through openLink.
func (w *Window) joinAlertCall(t CalendarAlertTarget) {
	opts := w.calendarOptions()
	w.calendarSource().Event(t.EventID, t.RecurrenceID, false, func(e *api.Event, err *api.Error) {
		if err != nil {
			w.Present()
			w.Toast(opts.ErrorMessage(err, ""))
			return
		}
		if e == nil || e.VideoCallLink == nil || e.VideoCallLink.URL == "" {
			return
		}
		w.Present()
		w.openLink(&w.ApplicationWindow.Window, e.VideoCallLink.URL, nil)
	})
}

// CalendarInvitationAction answers a new invitation's notification
// (CalendarAcceptInvitationAction: Accept with the card's default
// calendar) or a cancellation's (CalendarRemoveCancelledAction: Remove
// from Calendar), over the message's card; a failure, or a card that
// cannot do it any more (answered elsewhere), is a toast.
func (w *Window) CalendarInvitationAction(action string, acc api.AccountID, id api.MessageID) {
	cancel := action == CalendarRemoveCancelledAction
	if !cancel && action != CalendarAcceptInvitationAction {
		return
	}
	w.withdrawNotifications([]api.MessageID{id})
	opts := w.calendarOptions()
	src := w.calendarSource()
	go func() {
		var res api.MessageGetResult
		ctx, cancelCtx := context.WithTimeout(context.Background(), rpcTimeout)
		err := w.client.Call(ctx, api.MethodMessageGet, api.MessageGetParams{AccountID: acc, MessageID: id}, &res)
		cancelCtx()
		glib.IdleAdd(func() {
			if err != nil {
				w.Toast(opts.ErrorMessage(calendardata.AsError(err), ""))
				return
			}
			if res.Message.Invitation == nil {
				w.Toast(calendar.InvalidLine(opts.Env.Tr))
				return
			}
			w.answerNotified(src, opts, acc, id, *res.Message.Invitation, cancel)
		})
	}()
}

// answerNotified runs Accept or Remove from Calendar of a notification on
// the message's card.
func (w *Window) answerNotified(src calendardata.DataSource, opts calendarctl.Options, acc api.AccountID,
	id api.MessageID, card api.InvitationCard, cancel bool) {
	c := calendarctl.NewInvitationController(src, opts, acc, id, card, true)
	// ran: the card took the action (it went busy); the answer may come
	// before Respond returns (an in-memory source).
	ran := false
	c.OnChange = func() {
		if c.Busy() {
			ran = true
			return
		}
		if !ran {
			return
		}
		c.Stop()
		if e := c.Error(); e != "" {
			w.Toast(e)
		}
	}
	if cancel {
		c.ApplyCancel()
	} else {
		c.Respond(api.ResponseAccepted, false)
	}
	if !ran {
		// The card cannot do it (answered elsewhere, not a cancellation
		// any more): its state line says why.
		c.Stop()
		line := c.StateLine()
		if line == "" {
			line = calendar.InvalidLine(opts.Env.Tr)
		}
		w.Toast(line)
	}
}

// CalendarNotificationAction is one application action of the calendar's
// notifications for main.go: its name, parameter type and what it does
// with the main window.
type CalendarNotificationAction struct {
	Name     string
	Type     *glib.VariantType
	Activate func(w *Window, p *glib.Variant)
}

// CalendarNotificationActions are the application actions of the
// calendar's notifications. A parameter they cannot read presents the
// main window, as app.open-message does.
func CalendarNotificationActions() []CalendarNotificationAction {
	alert := func(name string) CalendarNotificationAction {
		return CalendarNotificationAction{Name: name, Type: CalendarAlertTargetType(), Activate: func(w *Window, p *glib.Variant) {
			t, ok := ParseCalendarAlertTarget(p)
			if !ok {
				w.Present()
				return
			}
			w.CalendarAlertAction(name, t)
		}}
	}
	invitation := func(name string) CalendarNotificationAction {
		return CalendarNotificationAction{Name: name, Type: NotificationTargetType(), Activate: func(w *Window, p *glib.Variant) {
			acc, id, ok := ParseNotificationTarget(p)
			if !ok {
				w.Present()
				return
			}
			w.CalendarInvitationAction(name, acc, id)
		}}
	}
	return []CalendarNotificationAction{
		alert(CalendarOpenEventAction), alert(CalendarJoinAction), alert(CalendarSnoozeAction),
		invitation(CalendarAcceptInvitationAction), invitation(CalendarRemoveCancelledAction),
	}
}
