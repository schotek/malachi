// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"encoding/json"
	"log/slog"
	"slices"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/rulesctl"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The mail rules' views outside the main window's own (Preferences →
// Rules and its editors) hear notify.rulesChanged and
// notify.accountsChanged through listenRules: the main window's
// handleNotification hands every notification to dispatchRules. All of it
// runs on the main loop. The main window keeps its own controller for
// the context menus (rules_menus.go).

var (
	rulesListeners    = map[int]func(method string, params json.RawMessage){}
	rulesListenerNext int
	rulesSamples      *rulesctl.SampleCaller
)

// listenRules adds a listener of the rules' notifications; cancel removes
// it.
func listenRules(f func(method string, params json.RawMessage)) (cancel func()) {
	rulesListenerNext++
	id := rulesListenerNext
	rulesListeners[id] = f
	return func() { delete(rulesListeners, id) }
}

// dispatchRules hands notify.rulesChanged and notify.accountsChanged to
// the listeners, in the order they were added (a listener may cancel
// another while it runs).
func dispatchRules(method string, params json.RawMessage) {
	if method != api.NotifyRulesChanged && method != api.NotifyAccountsChanged {
		return
	}
	ids := make([]int, 0, len(rulesListeners))
	for id := range rulesListeners {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if f, ok := rulesListeners[id]; ok {
			f(method, params)
		}
	}
}

// rulesCaller is the daemon the rules' controllers call: the samples'
// in-memory daemon with MALACHI_RULES_SAMPLES=1 (one for the process,
// whose writes notify the listeners), else c; nil without either.
func rulesCaller(c *client.Client) board.Caller {
	if rulesctl.SamplesOn() {
		if rulesSamples == nil {
			rulesSamples = rulesctl.NewSampleCaller(time.Now())
			rulesSamples.Notify = func(method string, params json.RawMessage) {
				glib.IdleAdd(func() { dispatchRules(method, params) })
			}
		}
		return rulesSamples
	}
	if c == nil {
		return nil
	}
	return c
}

// rulesOptions are what every controller of the rules runs with, over
// caller: the names as the sidebar shows them, the texts of i18n.
func rulesOptions(caller board.Caller, log *slog.Logger) rulesctl.Options {
	return rulesctl.Options{
		Caller:      caller,
		Loop:        glibLoop{},
		Tr:          i18n.Tr,
		Date:        widget.FormatDateTime,
		AccountName: accountLabel,
		FolderName:  rulesFolderName,
		SortFolders: rulesFolderOrder,
		RPCText:     widget.RPCErrorText,
		Log:         log,
	}
}

// rulesFolderName names a folder in the rules' choices: a role folder by
// its localised name, any other by its display path (so two subfolders of
// the same name can be told apart).
func rulesFolderName(f api.Folder) string {
	if f.Role != api.RoleNone && f.Role != "" || f.Virtual != "" {
		return folderTitle(f)
	}
	if f.Path != "" {
		return f.Path
	}
	return f.Name
}

// rulesFolderOrder orders an account's folders as the sidebar does.
func rulesFolderOrder(a api.Account, folders []api.Folder) []api.Folder {
	out := make([]api.Folder, 0, len(folders))
	for _, e := range folderTree(a, folders, newCollapseState(), newFavouriteState()) {
		if !e.Header {
			out = append(out, e.Folder)
		}
	}
	return out
}
