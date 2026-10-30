// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package rpc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Every address book method reaches the backend's service: the stub
// answers notImplemented, params that do not decode invalidParams, and
// nothing answers before the handshake.
func TestAddressBookMethodsDispatch(t *testing.T) {
	var methods []string
	for _, m := range api.AllMethods {
		if strings.HasPrefix(m, "addressBook.") || (strings.HasPrefix(m, "contact.") && m != api.MethodContactSearch) {
			methods = append(methods, m)
		}
	}
	if len(methods) != 8 {
		t.Fatalf("%d address book methods, want 8", len(methods))
	}
	ts := startServer(t, nil, nil, nil)
	pre, preR := dial(t, ts.sock)
	if resp := call(t, pre, preR, api.MethodContactList, map[string]any{}); resp.Error == nil || resp.Error.Code != api.CodeUnauthenticated {
		t.Fatalf("before the handshake: %+v", resp)
	}
	c, r := dialAuthed(t, ts.sock)
	for _, m := range methods {
		if resp := call(t, c, r, m, map[string]any{}); resp.Error == nil || resp.Error.Code != api.CodeNotImplemented {
			t.Errorf("%s: want notImplemented, got %+v", m, resp)
		}
	}
	if resp := call(t, c, r, api.MethodContactGet, map[string]any{"contactId": 7}); resp.Error == nil || resp.Error.Code != api.CodeInvalidParams {
		t.Errorf("a number as contactId: %+v", resp)
	}
}

func TestContactsChangedNotification(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	c, r := dialAuthed(t, ts.sock)
	var n api.ContactsNotifier = ts.Server
	for _, s := range []struct {
		ev     api.ContactsChangedNotification
		params string
	}{
		{api.ContactsChangedNotification{AccountID: "acc_1", BookIDs: []api.AddressBookID{"b_1"}}, `{"accountId":"acc_1","bookIds":["b_1"]}`},
		{api.ContactsChangedNotification{BookIDs: []api.AddressBookID{api.AddressBookRecent}}, `{"bookIds":["recent"]}`},
	} {
		n.ContactsChanged(s.ev)
		line, err := readLine(c, r, waitLimit)
		if err != nil {
			t.Fatal(err)
		}
		var got api.Notification
		if err := json.Unmarshal(line, &got); err != nil {
			t.Fatal(err)
		}
		if got.Method != api.NotifyContactsChanged || string(got.Params) != s.params {
			t.Errorf("notification %s, want %s", line, s.params)
		}
	}
}
