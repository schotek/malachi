// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// What the board's detail shows in place of the suggested reply: nothing
// (Controller's control), the draft loading, the draft ready to be edited
// inline, or why it could not be opened. The suggested reply is a local
// draft the case links (board.Case.Draft); the editor opens it with
// draft.get and keeps it while the case is selected. Every autosave bumps
// the case's version and the board lists the case again, so the editor is
// keyed by case, account and draft, never by the case's version: a refresh
// of the same case never reloads it.
//
// Swift-first, like the board.

package boardreply

import (
	"errors"
	"log/slog"
	"reflect"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
)

// Key is what the editor edits: the draft of a case in its account.
type Key struct {
	CaseID  board.CaseID
	Account api.AccountID
	Draft   api.DraftID
}

// EditorFailure is why the editor's draft could not be opened.
type EditorFailure int

// The failures.
const (
	// EditorGone: draft.get said draftNotFound; the board drops the link
	// soon.
	EditorGone EditorFailure = iota
	// EditorBackend: anything else; Retry offers Try Again.
	EditorBackend
)

// EditorPhaseKind is what an EditorPhase shows.
type EditorPhaseKind int

// The kinds.
const (
	// EditorNone: no suggested reply (or none to show); the Suggest
	// control shows.
	EditorNone EditorPhaseKind = iota
	EditorLoading
	// EditorReady: Draft is the draft as draft.get returned it, ready for
	// a compose pane to open as an edit (ui/internal/compose.FromDraft,
	// outside this package: see panes.go's Params).
	EditorReady
	// EditorFailed: Failure says why; EditorBackend offers Try Again.
	EditorFailed
)

// EditorPhase is the inline reply editor's state for the selected case.
type EditorPhase struct {
	Kind EditorPhaseKind
	// Key is set for every kind but EditorNone.
	Key     Key
	Draft   api.Draft
	Failure EditorFailure
}

// hasKey reports the key of every phase but EditorNone.
func (p EditorPhase) hasKey() (Key, bool) { return p.Key, p.Kind != EditorNone }

// Editor is the inline reply editor's state for the selected case
// (BoardReplyEditorController).
type Editor struct {
	caller Caller
	loop   assistantpanel.Loop
	log    *slog.Logger

	observers board.Observers
	phase     EditorPhase
	// ended are drafts the inline pane sent or discarded: hidden until the
	// board no longer links them to their case.
	ended map[Key]bool
	// generation is bumped by every load and every change of what is
	// shown, so an answer that arrives later than another request is
	// dropped.
	generation int
}

// NewEditor loads drafts for the selected case through caller.
func NewEditor(caller Caller, loop assistantpanel.Loop, log *slog.Logger) *Editor {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Editor{caller: caller, loop: loop, log: log, ended: map[Key]bool{}}
}

// Observe calls f after Phase changed; the token removes it.
func (e *Editor) Observe(f func()) *board.ObserverToken { return e.observers.Add(f) }

// Phase is what the editor shows now.
func (e *Editor) Phase() EditorPhase { return e.phase }

// Show is the selected case (nil: none). A case with a suggested reply
// loads it with draft.get unless the editor has it already: the same key
// changes nothing, whatever else changed in the case (an autosave bumps its
// version, never the editor). A draft Ended stays hidden until the case no
// longer links it. The host does not call this for the invented samples,
// which have no draft behind them.
func (e *Editor) Show(c *board.Case) {
	if c == nil {
		e.reset()
		return
	}
	// The board dropped (or replaced) a link the pane ended: forget it.
	for key := range e.ended {
		if key.CaseID == c.ID && (c.Draft == nil || key.Draft != c.Draft.ID) {
			delete(e.ended, key)
		}
	}
	if c.Draft == nil {
		e.reset()
		return
	}
	key := Key{CaseID: c.ID, Account: c.Account, Draft: c.Draft.ID}
	if e.ended[key] {
		e.reset()
		return
	}
	if k, ok := e.phase.hasKey(); ok && k == key {
		return
	}
	e.start(key)
}

// Retry loads the draft again after a failure (EditorFailed; a draft that
// is gone is tried again too).
func (e *Editor) Retry() {
	if e.phase.Kind != EditorFailed {
		return
	}
	e.start(e.phase.Key)
}

// Ended is the pane that sent or the user that discarded key: the editor
// hides it until the board drops the link (Show with the case without it).
func (e *Editor) Ended(key Key) {
	e.ended[key] = true
	if k, ok := e.phase.hasKey(); ok && k == key {
		e.reset()
	}
}

func (e *Editor) reset() {
	e.generation++
	e.setPhase(EditorPhase{})
}

func (e *Editor) start(key Key) {
	e.generation++
	mine := e.generation
	e.setPhase(EditorPhase{Kind: EditorLoading, Key: key})
	caller := e.caller
	params := api.DraftGetParams{AccountID: key.Account, DraftID: key.Draft}
	go func() {
		var r api.DraftGetResult
		err := callWith(caller, api.MethodDraftGet, params, &r)
		e.loop.Post(func() {
			if mine != e.generation {
				return
			}
			if err != nil {
				e.log.Info("draft.get", "err", err)
				var apiErr *api.Error
				if errors.As(err, &apiErr) && apiErr.Code == api.CodeDraftNotFound {
					e.setPhase(EditorPhase{Kind: EditorFailed, Key: key, Failure: EditorGone})
				} else {
					e.setPhase(EditorPhase{Kind: EditorFailed, Key: key, Failure: EditorBackend})
				}
				return
			}
			e.setPhase(EditorPhase{Kind: EditorReady, Key: key, Draft: r.Draft})
		})
	}()
}

// setPhase changes phase, notifying only on a real change (api.Draft holds
// slices, so Equal, not ==).
func (e *Editor) setPhase(p EditorPhase) {
	if editorPhaseEqual(p, e.phase) {
		return
	}
	e.phase = p
	e.observers.Notify()
}

func editorPhaseEqual(a, b EditorPhase) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case EditorNone:
		return true
	case EditorLoading:
		return a.Key == b.Key
	case EditorFailed:
		return a.Key == b.Key && a.Failure == b.Failure
	case EditorReady:
		return a.Key == b.Key && draftEqual(a.Draft, b.Draft)
	}
	return false
}

// draftEqual compares two drafts field by field (api.Draft holds slices,
// so == does not compile).
func draftEqual(a, b api.Draft) bool { return reflect.DeepEqual(a, b) }
