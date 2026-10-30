// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/contactsctl"
)

// The contacts' views outside the main window (the Contacts window,
// Preferences → Contacts) hear notify.contactsChanged and
// notify.accountsChanged through listenContacts: the main window's
// handleNotification hands every notification to dispatchContacts. All of
// it runs on the main loop.

var (
	contactsListeners    = map[int]func(method string, params json.RawMessage){}
	contactsListenerNext int
	contactsSamples      *contactsctl.SampleCaller
)

// listenContacts adds a listener of the contacts' notifications; cancel
// removes it.
func listenContacts(f func(method string, params json.RawMessage)) (cancel func()) {
	contactsListenerNext++
	id := contactsListenerNext
	contactsListeners[id] = f
	return func() { delete(contactsListeners, id) }
}

// dispatchContacts hands notify.contactsChanged and notify.accountsChanged
// to the listeners.
func dispatchContacts(method string, params json.RawMessage) {
	if method != api.NotifyContactsChanged && method != api.NotifyAccountsChanged {
		return
	}
	for _, id := range sortedListenerIDs() {
		if f, ok := contactsListeners[id]; ok {
			f(method, params)
		}
	}
}

// sortedListenerIDs are the listeners in the order they were added (a
// listener may cancel another while it runs).
func sortedListenerIDs() []int {
	ids := make([]int, 0, len(contactsListeners))
	for id := range contactsListeners {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// contactsCaller is the daemon the contacts' controllers call: the samples'
// in-memory daemon with MALACHI_CONTACTS_SAMPLES=1 (one for the process,
// whose writes notify the listeners), else c; nil without either.
func contactsCaller(c *client.Client) board.Caller {
	if contactsctl.SamplesOn() {
		if contactsSamples == nil {
			contactsSamples = contactsctl.NewSampleCaller(time.Now())
			contactsSamples.Notify = func(method string, params json.RawMessage) {
				glib.IdleAdd(func() { dispatchContacts(method, params) })
			}
		}
		return contactsSamples
	}
	if c == nil {
		return nil
	}
	return c
}
