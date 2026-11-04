// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package rulesctl holds the controllers of the mail rules' views
// (docs/plans/rules.md R14): the Rules page of Preferences and the
// context menus' Apply Rules… (Controller), the editor of one rule
// (Editor) and the accounts and folders they name (Directory). Plain
// structs over a board.Caller (the daemon) and a board.Loop (the main
// loop) that keep the state, build what the widgets show from the pure
// ui/internal/rules and report through callbacks (OnChange, OnQuestion,
// OnToast, …). No widgets and no gotk4: every method runs on the main
// loop; a call runs on its own goroutine (Options.Spawn) and its answer
// comes back through Loop.Post, so the tests drive a fake loop.
//
// The Go code is the reference: the macOS client ports it into
// MalachiCore/Controllers/Rules*.swift and Windows into
// Malachi.Core/Controllers/Rules*.cs, with the tests. Every text is
// ui/internal/rules'; every string of a rule may come from mail and is
// shown as plain text only.
package rulesctl

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/rules"
)

// The calls' time limits: a rule write or list is quick, rules.test reads
// a folder, rules.apply runs the rules over a whole folder.
const (
	DefaultCallTimeout = 30 * time.Second
	TestTimeout        = 60 * time.Second
	ApplyTimeout       = 5 * time.Minute
)

// Options are what every controller of the package runs with. The window
// builds one and hands it to each controller.
type Options struct {
	// Caller is the daemon (*client.Client); required.
	Caller board.Caller
	// Loop is the main loop; nil runs answers at once on the calling
	// goroutine.
	Loop board.Loop
	// Tr translates (GTK passes i18n.Tr); Date writes the time of a
	// status line (widget.Strftime of the locale); nil leaves it out.
	Tr   rules.Translator
	Date rules.DateText
	// AccountName and FolderName name an account and a folder as the
	// sidebar does; nil is the account's name (else its address) and the
	// folder's display path (else its name).
	AccountName func(api.Account) string
	FolderName  func(api.Folder) string
	// SortFolders orders an account's folders as the sidebar does; nil
	// keeps the daemon's order.
	SortFolders func(api.Account, []api.Folder) []api.Folder
	// CallTimeout is DefaultCallTimeout when 0.
	CallTimeout time.Duration
	// Spawn runs a call off the main loop; nil is a goroutine. The tests
	// run it in place.
	Spawn func(func())
	// RPCText is the usual sentence of a failed call with the action what
	// (widget.RPCErrorText) for an error rules has no sentence of; nil is
	// what alone.
	RPCText func(what string, err error) string
	// Log may be nil; only method names and error codes are logged, never
	// a rule's text.
	Log *slog.Logger
}

func (o Options) post(f func()) {
	if o.Loop == nil {
		f()
		return
	}
	o.Loop.Post(f)
}

func (o Options) accountName(a api.Account) string {
	if o.AccountName != nil {
		return o.AccountName(a)
	}
	if a.Config.Name != "" {
		return a.Config.Name
	}
	return a.Config.Email
}

func (o Options) folderName(f api.Folder) string {
	if o.FolderName != nil {
		return o.FolderName(f)
	}
	if f.Path != "" {
		return f.Path
	}
	return f.Name
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
				o.Log.Info("rules call failed", "method", method, "code", int(e.Code))
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

// invalidData reads an invalidArgument's RuleInvalidData (the client
// decodes error.data as a map; a test may pass the type itself).
func invalidData(e *api.Error) (api.RuleInvalidData, bool) {
	var d api.RuleInvalidData
	if e == nil || e.Code != api.CodeInvalidArgument || e.Data == nil {
		return d, false
	}
	raw, err := json.Marshal(e.Data)
	if err != nil || json.Unmarshal(raw, &d) != nil || d.Field == "" {
		return d, false
	}
	return d, true
}

// SaveErrorText is the sentence of a failed rules.create, rules.update or
// rules.reorder: ruleNotFound, the daemon's refusal of a field, else the
// plain "could not be saved". Never the daemon's message.
func SaveErrorText(e *api.Error, tr rules.Translator) string {
	switch {
	case e == nil:
		return ""
	case e.Code == api.CodeRuleNotFound:
		return rules.NotFoundText(tr)
	}
	if d, ok := invalidData(e); ok {
		return rules.InvalidText(d, tr)
	}
	return rules.SaveFailedText(tr)
}

// RulesChanged reports notify.rulesChanged.
func RulesChanged(method string) bool { return method == api.NotifyRulesChanged }

// otherErrorText is the sentence of a failed call rules has no text of
// (the list, the delete, the test): RPCText with the page's name.
func (o Options) otherErrorText(e *api.Error) string {
	if e == nil {
		return ""
	}
	if e.Code == api.CodeRuleNotFound {
		return rules.NotFoundText(o.Tr)
	}
	what := rules.PageTitle(o.Tr)
	if o.RPCText == nil {
		return what
	}
	return o.RPCText(what, e)
}
