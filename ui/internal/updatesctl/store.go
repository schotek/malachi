// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package updatesctl keeps the application's copy of the update check's
// status (docs/api.md §4.20) for the GTK client: one Store for the
// process, which the main window's banner and question, the What's New
// dialog and Preferences → General → Updates all read. The texts and view
// models are ui/internal/updates'; this package only calls the daemon,
// orders the answers and asks a security release's question at most once
// per version in this process (TakePrompt).
//
// It is pure (no GTK): the window passes its RPC client and a Loop that
// posts to the main loop. macOS AppState.updates and Windows
// UpdateStatusStore are the same thing.
package updatesctl

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/updates"
)

// Caller calls the daemon (the window's RPC client); it blocks and is
// called off the main loop.
type Caller interface {
	Call(ctx context.Context, method string, params any, result any) error
}

// Loop runs callbacks on the main loop (the method set of board.Loop, so
// the window passes the same one).
type Loop interface {
	Post(f func())
	After(d time.Duration, f func())
}

// callTimeout bounds one call; checkTimeout bounds update.check, whose
// reply waits for the check itself (at most 70 s in the daemon).
const (
	callTimeout  = 15 * time.Second
	checkTimeout = 75 * time.Second
)

// Store is the last update status the daemon gave. Main loop only: every
// method and every observer run on it.
type Store struct {
	caller Caller
	loop   Loop
	log    *slog.Logger
	locale string

	status    *api.UpdateStatus
	connected bool
	// gen orders the answers: one that ends after a newer call was made
	// is dropped (the newer call brings the newer status).
	gen int

	observers map[int]func()
	nextID    int
	// asked are the versions whose security question this process asked
	// already, whatever the daemon still says.
	asked map[string]bool
}

// NewStore is an empty store whose calls carry locale (Locale).
func NewStore(caller Caller, loop Loop, locale string, log *slog.Logger) *Store {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Store{caller: caller, loop: loop, log: log, locale: locale,
		observers: map[int]func(){}, asked: map[string]bool{}}
}

// Status is the last status, nil before the first answer (or from a
// daemon without the update check).
func (s *Store) Status() *api.UpdateStatus { return s.status }

// Locale is the language the store asks the texts in.
func (s *Store) Locale() string { return s.locale }

// Observe calls f after every change of the status, and after a write the
// daemon refused (a switch then shows the status again), until remove.
func (s *Store) Observe(f func()) (remove func()) {
	s.nextID++
	id := s.nextID
	s.observers[id] = f
	return func() { delete(s.observers, id) }
}

func (s *Store) changed() {
	ids := make([]int, 0, len(s.observers))
	for id := range s.observers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if f, ok := s.observers[id]; ok {
			f()
		}
	}
}

// ConnectionChanged: the connection to the daemon came or went. Coming,
// the status is read again; going, the last one stays shown.
func (s *Store) ConnectionChanged(connected bool) {
	if connected == s.connected {
		return
	}
	s.connected = connected
	if connected {
		s.Refresh()
	}
}

// Handle reads the status again on notify.updateChanged and says whether
// method was that notification.
func (s *Store) Handle(method string) bool {
	if method != api.NotifyUpdateChanged {
		return false
	}
	s.Refresh()
	return true
}

// Refresh is update.status.
func (s *Store) Refresh() {
	s.call(api.MethodUpdateStatus, api.UpdateStatusParams{Locale: s.locale})
}

// Check is update.check (Check Now).
func (s *Store) Check() {
	s.callFor(checkTimeout, api.MethodUpdateCheck, api.UpdateCheckParams{Locale: s.locale})
}

// SetEnabled is update.setPrefs {enabled}.
func (s *Store) SetEnabled(on bool) {
	s.call(api.MethodUpdateSetPrefs, api.UpdateSetPrefsParams{Enabled: &on, Locale: s.locale})
}

// Dismiss is update.dismiss {version, action}.
func (s *Store) Dismiss(version string, action api.UpdateDismissAction) {
	s.call(api.MethodUpdateDismiss, api.UpdateDismissParams{Version: version, Action: action, Locale: s.locale})
}

// DismissNotice is update.dismissNotice {id}.
func (s *Store) DismissNotice(id string) {
	s.call(api.MethodUpdateDismissNotice, api.UpdateDismissNoticeParams{ID: id, Locale: s.locale})
}

// call runs method off the main loop; its status replaces the stored one
// unless a newer call was made meanwhile. A failure keeps the status and
// is logged only (the status carries the check's own failures).
func (s *Store) call(method string, params any) { s.callFor(callTimeout, method, params) }

func (s *Store) callFor(timeout time.Duration, method string, params any) {
	s.gen++
	my := s.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var res api.UpdateStatus
		err := s.caller.Call(ctx, method, params, &res)
		s.loop.Post(func() {
			if err != nil {
				s.log.Info("update check", "method", method, "err", err)
				s.changed()
				return
			}
			if my != s.gen {
				return
			}
			s.status = &res
			s.changed()
		})
	}()
}

// TakePrompt is the security release's question to ask now
// (updates.PromptFor), nil when there is none or this process asked it
// for that version already; a returned question counts as asked. The
// caller calls Dismiss(version, api.UpdatePrompted) once it was answered.
func (s *Store) TakePrompt(tr updates.Translator) *updates.Prompt {
	p := updates.PromptFor(s.status, tr)
	if p == nil || s.asked[p.Version] {
		return nil
	}
	s.asked[p.Version] = true
	return p
}

// Locale is the locale of the update texts for the UI's languages (the
// user's languages in order, as glib.GetLanguageNames gives them): the
// first that has a catalogue among catalogues, "en" when English or
// nothing known comes first.
func Locale(languages []string, catalogues []string) string {
	for _, l := range languages {
		if l == "C" || l == "POSIX" || l == "en" || strings.HasPrefix(l, "en_") {
			return "en"
		}
		if slices.Contains(catalogues, l) {
			return l
		}
	}
	return "en"
}
