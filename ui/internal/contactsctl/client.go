// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package contactsctl holds the controllers of the Contacts window, the
// address chips' lookup and Preferences → Contacts
// (docs/plans/address-book.md §5): plain structs over a board.Caller (the
// daemon) and a board.Loop (the main loop) that keep the state of the
// window, of the contact shown and edited, of the chips' lookups and of
// the preferences page, build what the widgets show from the pure
// ui/internal/contacts and report through callbacks (OnChange, OnQuestion,
// OnSignIn, …). No widgets and no gotk4: every method runs on the main
// loop; a call runs on its own goroutine (Options.Spawn) and its answer
// comes back through Loop.Post; debounces go through Loop.After, so the
// tests drive a fake loop.
//
// The Go code is the reference: the macOS client ports it into
// MalachiCore/Controllers/Contacts*.swift and Windows into
// Malachi.Core/Controllers/Contacts*.cs, with the tests.
package contactsctl

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/contacts"
)

// DefaultCallTimeout bounds a call (the board's); a write to the server
// gets WriteTimeout.
const (
	DefaultCallTimeout = 30 * time.Second
	WriteTimeout       = 60 * time.Second
)

// Options are what every controller of the package runs with. The window
// builds one and hands it to each controller.
type Options struct {
	// Caller is the daemon (*client.Client); required.
	Caller board.Caller
	// Loop is the main loop; nil runs answers at once on the calling
	// goroutine and never runs a debounce (tests pass a loop).
	Loop board.Loop
	// Tr translates; Format writes birthdays (calendar.Strftime of the
	// locale).
	Tr     contacts.Translator
	Format contacts.Strftime
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// CallTimeout is DefaultCallTimeout when 0.
	CallTimeout time.Duration
	// Spawn runs a call off the main loop; nil is a goroutine. The tests
	// run it in place.
	Spawn func(func())
	// RPCText is the usual sentence of a failed call with the action what
	// (widget.RPCErrorText) for an error contacts has no sentence of; nil
	// is what alone.
	RPCText func(what string, err error) string
	// Log may be nil; only method names and error codes are logged, never
	// a contact's text.
	Log *slog.Logger
}

// Clock is now.
func (o Options) Clock() time.Time {
	if o.Now == nil {
		return time.Now()
	}
	return o.Now()
}

func (o Options) after(d time.Duration, f func()) {
	if o.Loop != nil {
		o.Loop.After(d, f)
	}
}

func (o Options) post(f func()) {
	if o.Loop == nil {
		f()
		return
	}
	o.Loop.Post(f)
}

// call runs method off the main loop and hands done the result (nil on
// failure) and the error on the main loop.
func call[R any](o Options, method string, params any, timeout time.Duration, done func(*R, *api.Error)) {
	if timeout <= 0 {
		timeout = o.CallTimeout
	}
	if timeout <= 0 {
		timeout = DefaultCallTimeout
	}
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		r := new(R)
		err := o.Caller.Call(ctx, method, params, r)
		e := AsError(err)
		if e != nil {
			r = nil
			if o.Log != nil {
				o.Log.Info("contacts call failed", "method", method, "code", int(e.Code))
			}
		}
		o.post(func() { done(r, e) })
	}
	if o.Spawn != nil {
		o.Spawn(run)
		return
	}
	go run()
}

// ErrorMessage is the sentence of a failed contacts call: contacts' own
// by code (account is the AccountLabel for consent), else RPCText with
// what; "" for nil. Never the daemon's message.
func (o Options) ErrorMessage(e *api.Error, account, what string) string {
	if e == nil {
		return ""
	}
	if t, ok := contacts.ErrorText(e.Code, account, o.Tr); ok {
		return t
	}
	if what == "" {
		what = contacts.WindowTitle(o.Tr)
	}
	if o.RPCText == nil {
		return what
	}
	return o.RPCText(what, e)
}

// AsError is err as the daemon's error: an *api.Error as it is, a call
// without a connection CodeUnavailable, one that ran out of time or was
// cancelled CodeCancelled, anything else CodeInternalError; nil for nil.
func AsError(err error) *api.Error {
	if err == nil {
		return nil
	}
	var e *api.Error
	switch {
	case errors.As(err, &e):
		return e
	case errors.Is(err, client.ErrDisconnected):
		return &api.Error{Code: api.CodeUnavailable, Message: "not connected"}
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return &api.Error{Code: api.CodeCancelled, Message: "timed out"}
	}
	return &api.Error{Code: api.CodeInternalError, Message: "call failed"}
}

// decodeData reads an error's data into v (the client decodes it as a
// map; a test may pass the type itself).
func decodeData(e *api.Error, v any) bool {
	if e == nil || e.Data == nil {
		return false
	}
	raw, err := json.Marshal(e.Data)
	return err == nil && json.Unmarshal(raw, v) == nil
}

// ConflictCard is the server's version in a contactConflict's data.
func ConflictCard(e *api.Error) (api.ContactCard, bool) {
	if e == nil || e.Code != api.CodeContactConflict {
		return api.ContactCard{}, false
	}
	var d api.ContactConflictData
	if !decodeData(e, &d) || d.Server.ID == "" {
		return api.ContactCard{}, false
	}
	return d.Server, true
}

// ConsentData is a contactsConsentRequired's data.
func ConsentData(e *api.Error) (api.ContactsConsentRequiredData, bool) {
	if e == nil || e.Code != api.CodeContactsConsentRequired {
		return api.ContactsConsentRequiredData{}, false
	}
	var d api.ContactsConsentRequiredData
	return d, decodeData(e, &d) && d.AccountID != ""
}

// ContactsChanged reads notify.contactsChanged; false for another
// notification or bad params.
func ContactsChanged(method string, params json.RawMessage) (api.ContactsChangedNotification, bool) {
	var n api.ContactsChangedNotification
	if method != api.NotifyContactsChanged || json.Unmarshal(params, &n) != nil {
		return n, false
	}
	return n, true
}

// cloneConfig copies an account's configuration with its contacts
// pointers.
func cloneConfig(c api.AccountConfig) api.AccountConfig {
	if c.Graph != nil {
		g := *c.Graph
		if g.Contacts != nil {
			k := *g.Contacts
			g.Contacts = &k
		}
		c.Graph = &g
	}
	if c.CalDAV != nil {
		d := *c.CalDAV
		c.CalDAV = &d
	}
	return c
}
