// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package boardtriage

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
)

// Caller is the daemon as this package needs it: one JSON-RPC call, a
// server-side error as *api.Error. ui/internal/client's Client has it. It
// blocks, so it is only ever called off the main loop.
type Caller interface {
	Call(ctx context.Context, method string, params any, result any) error
}

// CallTimeout bounds one call to the daemon.
const CallTimeout = 30 * time.Second

// callWith runs one call bounded by CallTimeout; off the main loop.
func callWith(c Caller, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	return c.Call(ctx, method, params, result)
}

// WhyOf is why a write or load of the board failed, as far as err says,
// for board.Failed's toast (macOS Board.Text's failureReason; the
// promise's own caseNotFound is not a preference's): the daemon's error
// codes, a timeout, and the client's own "not connected" when disconnected
// (may be nil) recognises it.
func WhyOf(err error, disconnected func(error) bool) board.Why {
	var e *api.Error
	switch {
	case err == nil:
		return board.WhyNone
	case errors.As(err, &e):
		switch e.Code {
		case api.CodeCaseNotFound:
			return board.WhyCaseGone
		case api.CodeInvalidArgument:
			return board.WhyNotAccepted
		case api.CodeMethodNotFound, api.CodeNotImplemented:
			return board.WhyNoBoard
		case api.CodeStorageError:
			return board.WhyNotSaved
		case api.CodeDraftNotFound:
			return board.WhyDraftGone
		}
		return board.WhyNone
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return board.WhyTimeout
	case disconnected != nil && disconnected(err):
		return board.WhyBackendDown
	}
	return board.WhyNone
}

// PreferencesConfig is what Preferences needs from the application.
type PreferencesConfig struct {
	Caller     Caller
	Loop       assistantpanel.Loop
	Log        *slog.Logger
	Translator board.Translator
	// Disconnected says whether an error of Caller means "not connected"
	// (errors.Is(err, client.ErrDisconnected)); nil knows none.
	Disconnected func(error) bool
}

// Preferences are the board's preferences in the daemon (docs/api.md
// §4.13 board.preferences, board.setPreferences; macOS
// BoardPreferencesController): whether the board is on, whether the
// assistant's notes count, the windows, the triage accounts and the
// automatic triage's schedule, once for the whole application (the triage
// controller, its schedule and Preferences → AI read the same object).
//
// Current is not known until the daemon answered. Load asks again (at the
// start, on a reconnect, when a window shows the board); a reply that a
// newer load or a lost connection overtook is dropped. Update is
// optimistic like the board's other writes: the change is laid over the
// daemon's preferences at once and reported, then board.setPreferences
// sends the whole object with it (writes go one after another, each with
// what the earlier ones left), and its answer, the preferences as stored,
// replaces it; a refused write is taken back, reported, and OnError says
// why. The contract has no partial write (docs/api.md §4.13: "every
// field"), so each write reads the preferences afresh first and lays only
// the user's changes (the writes' functions) over them: a field another
// client changed since the last load is not written back. A write whose
// fresh read fails goes on with the preferences last read; with none, it
// fails. Observe reports every change of Current, ObserveLoaded every
// answer of board.preferences. Main loop only.
type Preferences struct {
	// OnError gets a short sentence for a toast when a write failed (it is
	// taken back by then), unless the write was quiet.
	OnError func(string)

	caller       Caller
	loop         assistantpanel.Loop
	log          *slog.Logger
	tr           board.Translator
	disconnected func(error) bool

	// current is base with the writes under way; base the daemon's as last
	// answered. nil until the daemon answered.
	current, base *api.BoardPreferences
	overlays      []overlay
	nextToken     int
	// writes are the writes under way or waiting, in order; the first is
	// in flight while writing.
	writes  []pendingWrite
	writing bool
	// loadGen is bumped by every load and a lost connection: older replies
	// are dropped. loading: the latest load is on its way.
	loadGen        int
	loading        bool
	loadErr        error
	lastLoadFailed bool

	changed, loaded observers
}

type overlay struct {
	token int
	apply func(*api.BoardPreferences)
}

type pendingWrite struct {
	token int
	quiet bool
	done  func(bool)
}

// NewPreferences is the board's preferences, not loaded yet.
func NewPreferences(cfg PreferencesConfig) *Preferences {
	return &Preferences{
		caller: cfg.Caller, loop: cfg.Loop, log: orDiscard(cfg.Log), tr: cfg.Translator, disconnected: cfg.Disconnected,
	}
}

// orDiscard is l, or a logger that drops everything when l is nil.
func orDiscard(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.New(slog.DiscardHandler)
	}
	return l
}

// clonePrefs is p with its own TriageAccounts.
func clonePrefs(p api.BoardPreferences) api.BoardPreferences {
	p.TriageAccounts = slices.Clone(p.TriageAccounts)
	return p
}

// samePrefs says whether a and b hold the same preferences (nil: none).
func samePrefs(a, b *api.BoardPreferences) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, y := *a, *b
	same := slices.Equal(x.TriageAccounts, y.TriageAccounts)
	// Every other field, also one a newer contract adds; nil and empty
	// accounts are the same.
	x.TriageAccounts, y.TriageAccounts = nil, nil
	return same && reflect.DeepEqual(x, y)
}

// Current is the preferences with the writes under way; false until the
// daemon answered.
func (p *Preferences) Current() (api.BoardPreferences, bool) {
	if p.current == nil {
		return api.BoardPreferences{}, false
	}
	return clonePrefs(*p.current), true
}

// Stored is the daemon's preferences as last read, without the writes
// under way; false until the daemon answered.
func (p *Preferences) Stored() (api.BoardPreferences, bool) {
	if p.base == nil {
		return api.BoardPreferences{}, false
	}
	return clonePrefs(*p.base), true
}

// LastLoadFailed says whether the last board.preferences failed (the
// daemon is unreachable, or does not know the board); false once one
// answered.
func (p *Preferences) LastLoadFailed() bool { return p.lastLoadFailed }

// Writing says whether a write is under way or waiting.
func (p *Preferences) Writing() bool { return len(p.writes) > 0 }

// Idle says whether nothing is loading or being written.
func (p *Preferences) Idle() bool { return !p.loading && len(p.writes) == 0 }

// Observe calls f after every change of Current (and of LastLoadFailed),
// until the returned function is called.
func (p *Preferences) Observe(f func()) (remove func()) { return p.changed.add(f) }

// ObserveLoaded calls f after every answer of board.preferences (Stored
// holds it), also the reads before a write, until the returned function
// is called.
func (p *Preferences) ObserveLoaded(f func()) (remove func()) { return p.loaded.add(f) }

// Load asks the daemon for the preferences (the answer is reported); done
// (may be nil) is called with whether this load's answer came.
func (p *Preferences) Load(done func(ok bool)) {
	p.loadGen++
	g := p.loadGen
	p.loading = true
	go func() {
		var r api.BoardPreferencesResult
		err := callWith(p.caller, api.MethodBoardPreferences, api.BoardPreferencesParams{}, &r)
		p.loop.Post(func() { p.answered(g, r.Preferences, err, done) })
	}()
}

func (p *Preferences) answered(g int, prefs api.BoardPreferences, err error, done func(bool)) {
	ok := g == p.loadGen
	if ok {
		p.loading = false
		if err == nil {
			p.loadErr = nil
			p.base = &prefs
			p.publish()
			if p.lastLoadFailed {
				p.lastLoadFailed = false
				p.changed.notify()
			}
			p.loaded.notify()
		} else {
			p.log.Info("board.preferences", "err", err)
			p.loadErr = err
			ok = false
			if !p.lastLoadFailed {
				p.lastLoadFailed = true
				p.changed.notify()
			}
		}
	}
	if done != nil {
		done(ok)
	}
}

// ConnectionChanged says the connection came (the preferences are asked
// for again) or went (replies on the way are dropped; the last preferences
// stay).
func (p *Preferences) ConnectionChanged(connected bool) {
	if connected {
		p.Load(nil)
		return
	}
	p.loadGen++
	p.loading = false
}

// Update changes the preferences: at once here, then in the daemon (see
// the type's comment). done (may be nil) is called once with true when the
// daemon stored it, false when it was refused (taken back, OnError called
// unless quiet) or the preferences could not be loaded first.
func (p *Preferences) Update(quiet bool, change func(*api.BoardPreferences), done func(stored bool)) {
	p.nextToken++
	token := p.nextToken
	p.overlays = append(p.overlays, overlay{token: token, apply: change})
	p.publish()
	p.writes = append(p.writes, pendingWrite{token: token, quiet: quiet, done: done})
	if !p.writing {
		p.write()
	}
}

// write sends the first waiting write: the preferences read afresh with it
// and the writes before it, and takes the answer.
func (p *Preferences) write() {
	p.writing = true
	w := p.writes[0]
	p.Load(func(fresh bool) {
		if !fresh && p.base == nil {
			p.lift(w.token)
			why := board.WhyBackendDown
			if p.loadErr != nil {
				why = WhyOf(p.loadErr, p.disconnected)
			}
			p.failed(w, why)
			return
		}
		wanted := clonePrefs(*p.base)
		for _, o := range p.overlays {
			o.apply(&wanted)
			if o.token == w.token {
				break
			}
		}
		if wanted.TriageAccounts == nil {
			// Never null on the wire.
			wanted.TriageAccounts = []api.AccountID{}
		}
		go func() {
			var r api.BoardSetPreferencesResult
			err := callWith(p.caller, api.MethodBoardSetPreferences, api.BoardSetPreferencesParams{Preferences: wanted}, &r)
			p.loop.Post(func() {
				if err != nil {
					p.log.Info("board.setPreferences", "err", err)
					p.lift(w.token)
					p.failed(w, WhyOf(err, p.disconnected))
					return
				}
				stored := r.Preferences
				p.base = &stored
				p.lift(w.token)
				p.complete(true)
			})
		}()
	})
}

// failed reports write w's failure for why and completes it.
func (p *Preferences) failed(w pendingWrite, why board.Why) {
	if !w.quiet && p.OnError != nil {
		p.OnError(board.Failed(board.ActionPreferences, why, p.tr))
	}
	p.complete(false)
}

// complete ends the first write with stored and starts the next.
func (p *Preferences) complete(stored bool) {
	w := p.writes[0]
	p.writes = p.writes[1:]
	p.writing = false
	if w.done != nil {
		w.done(stored)
	}
	if !p.writing && len(p.writes) > 0 {
		p.write()
	}
}

// lift drops the overlay of write token.
func (p *Preferences) lift(token int) {
	p.overlays = slices.DeleteFunc(p.overlays, func(o overlay) bool { return o.token == token })
	p.publish()
}

// publish builds Current from the daemon's preferences and the writes
// under way and reports it when it changed.
func (p *Preferences) publish() {
	var next *api.BoardPreferences
	if p.base != nil {
		c := clonePrefs(*p.base)
		for _, o := range p.overlays {
			o.apply(&c)
		}
		next = &c
	}
	if samePrefs(next, p.current) {
		return
	}
	p.current = next
	p.changed.notify()
}

// observers are functions to call on a change, in the order added.
type observers struct {
	next int
	fs   map[int]func()
}

func (o *observers) add(f func()) (remove func()) {
	if o.fs == nil {
		o.fs = map[int]func(){}
	}
	id := o.next
	o.next++
	o.fs[id] = f
	return func() { delete(o.fs, id) }
}

func (o *observers) notify() {
	ids := make([]int, 0, len(o.fs))
	for id := range o.fs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if f, ok := o.fs[id]; ok {
			f()
		}
	}
}
