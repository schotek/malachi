// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package rpc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func calendarMethods() []string {
	var out []string
	for _, m := range api.AllMethods {
		switch {
		case m == api.MethodAccountDiscoverCalDAV, m == api.MethodAccountListCalDAVCalendars,
			strings.HasPrefix(m, "calendar."), strings.HasPrefix(m, "event."), strings.HasPrefix(m, "invitation."):
			out = append(out, m)
		}
	}
	return out
}

// Every calendar method reaches the backend's services: the stub answers
// notImplemented, and params that do not decode answer invalidParams
// before anything reaches it. Before the handshake none answers at all.
func TestCalendarMethodsDispatch(t *testing.T) {
	methods := calendarMethods()
	if len(methods) != 21 {
		t.Fatalf("%d calendar methods in api.AllMethods, want 21", len(methods))
	}
	ts := startServer(t, nil, nil, nil)

	pre, preR := dial(t, ts.sock)
	resp := call(t, pre, preR, api.MethodCalendarList, map[string]any{})
	if resp.Error == nil || resp.Error.Code != api.CodeUnauthenticated {
		t.Fatalf("calendar.list before the handshake: want unauthenticated, got %+v", resp)
	}

	c, r := dialAuthed(t, ts.sock)
	for _, m := range methods {
		resp := call(t, c, r, m, map[string]any{})
		if resp.Error == nil || resp.Error.Code != api.CodeNotImplemented {
			t.Errorf("%s: want notImplemented from the stub, got %+v", m, resp)
		}
	}
	resp = call(t, c, r, api.MethodEventGet, map[string]any{"eventId": 7})
	if resp.Error == nil || resp.Error.Code != api.CodeInvalidParams {
		t.Errorf("a number as eventId: want invalidParams, got %+v", resp)
	}
	resp = call(t, c, r, api.MethodEventList, map[string]any{"from": "yesterday"})
	if resp.Error == nil || resp.Error.Code != api.CodeInvalidParams {
		t.Errorf("a malformed time: want invalidParams, got %+v", resp)
	}
}

// The four calendar notifications reach an authenticated client under
// their documented names.
func TestCalendarNotifications(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	c, r := dialAuthed(t, ts.sock)

	var n api.CalendarNotifier = ts.Server
	steps := []struct {
		send   func()
		method string
		params string
	}{
		{func() {
			n.CalendarChanged(api.CalendarChangedNotification{AccountIDs: []api.AccountID{"acc_1"}, CalendarIDs: []api.CalendarID{"cal_1"}})
		}, api.NotifyCalendarChanged, `{"accountIds":["acc_1"],"calendarIds":["cal_1"]}`},
		{func() {
			n.CalendarState(api.CalendarStateNotification{State: api.CalendarSyncState{AccountID: "acc_1", Status: api.CalendarSyncIdle, Progress: -1}})
		}, api.NotifyCalendarState, `{"state":{"accountId":"acc_1","status":"idle","progress":-1}}`},
		{func() {
			n.EventAlert(api.EventAlertNotification{Alert: api.EventAlert{AlertID: "al_1", State: api.AlertDismissed,
				EventID: "ev_1", AccountID: "acc_1", CalendarID: "cal_1", Title: "Standup", MinutesBefore: 15}})
		}, api.NotifyEventAlert, `{"alert":{"alertId":"al_1","state":"dismissed","eventId":"ev_1","accountId":"acc_1",` +
			`"calendarId":"cal_1","title":"Standup","allDay":false,"minutesBefore":15}}`},
		{func() { n.InvitationsChanged(api.InvitationsChangedNotification{}) }, api.NotifyInvitationsChanged, `{}`},
	}
	for _, s := range steps {
		s.send()
		line, err := readLine(c, r, waitLimit)
		if err != nil {
			t.Fatalf("%s: no notification: %v", s.method, err)
		}
		var got api.Notification
		if err := json.Unmarshal(line, &got); err != nil {
			t.Fatal(err)
		}
		if got.Method != s.method || string(got.Params) != s.params {
			t.Errorf("notification: %s, want %s %s", line, s.method, s.params)
		}
	}
}
