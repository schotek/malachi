// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/boardreply"
)

// The real draft controller behind an inline Pane, with a form in place
// of WebKit. Asynchronous RPC answers are delivered on the test's thread.
type boardDraftLoop struct{ posted chan func() }

func (l *boardDraftLoop) Post(f func())             { l.posted <- f }
func (*boardDraftLoop) After(time.Duration, func()) {}
func (l *boardDraftLoop) until(t *testing.T, ready func() bool) {
	t.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for !ready() {
		select {
		case f := <-l.posted:
			f()
		case <-timeout.C:
			t.Fatal("draft operation did not finish")
		}
	}
}

type boardDraftForm struct {
	html    string
	flush   func()
	closed  bool
	toasts  []string
	comment bool
}

func (*boardDraftForm) account() api.Account { return api.Account{ID: "account"} }
func (*boardDraftForm) recipients() ([]api.Address, []api.Address, []api.Address, bool) {
	return []api.Address{{Address: "recipient@example.org"}}, nil, nil, true
}
func (*boardDraftForm) subject() string                    { return "Reply" }
func (*boardDraftForm) attachments() []api.DraftAttachment { return nil }
func (f *boardDraftForm) editorHTML() string               { return f.html }
func (f *boardDraftForm) editorText() string               { return f.html }
func (f *boardDraftForm) flushEditor(done func()) {
	if f.flush != nil {
		f.flush()
	}
	done()
}
func (*boardDraftForm) setAttachments([]api.DraftAttachment)     {}
func (*boardDraftForm) setStatus(string)                         {}
func (f *boardDraftForm) toast(s string)                         { f.toasts = append(f.toasts, s) }
func (*boardDraftForm) setSendEnabled(bool)                      {}
func (f *boardDraftForm) closeForm()                             { f.closed = true }
func (f *boardDraftForm) isComment() bool                        { return f.comment }
func (*boardDraftForm) commentVisibility() api.CommentVisibility { return api.CommentPublic }

type boardDraftScript struct {
	mu         sync.Mutex
	saves      []api.Draft
	methods    []string
	saveErrors []error
	sendError  error
	saveGate   chan struct{}
	sendGate   chan struct{}
}

func (s *boardDraftScript) Call(_ context.Context, method string, params, result any) error {
	s.mu.Lock()
	s.methods = append(s.methods, method)
	var err error
	var gate chan struct{}
	switch method {
	case api.MethodDraftSave:
		d := params.(api.DraftSaveParams).Draft
		s.saves = append(s.saves, d)
		gate = s.saveGate
		if len(s.saveErrors) > 0 {
			err, s.saveErrors = s.saveErrors[0], s.saveErrors[1:]
		}
		*result.(*api.DraftSaveResult) = api.DraftSaveResult{DraftID: d.ID, Version: d.Version + 1}
	case api.MethodDraftGet:
		*result.(*api.DraftGetResult) = api.DraftGetResult{Draft: api.Draft{ID: "draft", Version: 8, HTMLBody: "someone else's text"}}
	case api.MethodMessageSend:
		gate, err = s.sendGate, s.sendError
	}
	s.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return err
}
func (s *boardDraftScript) calls() ([]string, []api.Draft) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.methods...), append([]api.Draft{}, s.saves...)
}
func newBoardDraftTest(t *testing.T) (*draftController, *boardDraftForm, *boardDraftScript, *boardDraftLoop) {
	t.Helper()
	f := &boardDraftForm{html: "<p>original</p>"}
	s := &boardDraftScript{}
	l := &boardDraftLoop{posted: make(chan func(), 32)}
	c := newDraftController(OwnerBoard, s, slog.New(slog.DiscardHandler))
	c.form, c.loop = f, l
	c.setOpened("draft", 3, "", true)
	t.Cleanup(c.abandon)
	return c, f, s, l
}

func TestBoardDraftUntouchedAndNormalisedEditorSavesNothing(t *testing.T) {
	for _, ready := range []bool{false, true} {
		c, f, s, _ := newBoardDraftTest(t)
		if ready {
			c.editorReady()
		}
		if !ready {
			f.flush = func() { f.html = "<p>normalised</p>" }
		}
		done := false
		c.settle(func(ok bool) {
			if !ok {
				t.Error("untouched settle failed")
			}
			done = true
		})
		methods, _ := s.calls()
		if !done || len(methods) != 0 || c.draft.closed {
			t.Fatalf("untouched pane changed: done=%v methods=%v closed=%v", done, methods, c.draft.closed)
		}
	}
}

func TestBoardDraftFlushOnlyEditSavedOnceAndRemainsUsable(t *testing.T) {
	c, f, s, l := newBoardDraftTest(t)
	c.editorReady()
	f.flush = func() { f.html = "<p>typed between reports</p>" }
	done := false
	c.settle(func(ok bool) {
		if !ok {
			t.Error("settle failed")
		}
		done = true
	})
	l.until(t, func() bool { return done })
	_, saves := s.calls()
	if len(saves) != 1 || saves[0].ID != "draft" || saves[0].HTMLBody != f.html || c.draft.closed {
		t.Fatalf("saved=%+v closed=%v", saves, c.draft.closed)
	}
	c.settle(func(ok bool) {
		if !ok {
			t.Error("second settle failed")
		}
	})
	_, saves = s.calls()
	if len(saves) != 1 {
		t.Fatalf("unchanged pane saved again: %d", len(saves))
	}
}

func TestBoardDraftConflictRetainsTextAndIdentity(t *testing.T) {
	c, f, s, l := newBoardDraftTest(t)
	s.saveErrors = []error{&api.Error{Code: api.CodeConflict}}
	f.html = "<p>my unsaved reply</p>"
	c.markDirty()
	done := false
	c.settle(func(ok bool) {
		if !ok {
			t.Error("conflict was not settled")
		}
		done = true
	})
	l.until(t, func() bool { return done })
	methods, saves := s.calls()
	if len(saves) != 2 || saves[0].ID != "draft" || saves[1].ID != "draft" || saves[1].Version != 8 || saves[1].HTMLBody != f.html {
		t.Fatalf("conflict lost reply: methods=%v saves=%+v", methods, saves)
	}
}

func TestBoardDraftDeletedElsewhereIsNeverRecreated(t *testing.T) {
	c, _, s, l := newBoardDraftTest(t)
	s.saveErrors = []error{&api.Error{Code: api.CodeDraftNotFound}}
	lost := 0
	c.onLost = func() { lost++ }
	c.markDirty()
	done := false
	c.settle(func(ok bool) {
		if ok {
			t.Error("missing draft reported saved")
		}
		done = true
	})
	l.until(t, func() bool { return done })
	c.markDirty()
	c.settle(func(ok bool) {
		if ok {
			t.Error("lost draft reported saved")
		}
	})
	_, saves := s.calls()
	if lost != 1 || len(saves) != 1 || !c.draft.lost || !c.draft.closed {
		t.Fatalf("lost=%d saves=%d state=%+v", lost, len(saves), c.draft)
	}
}

func TestBoardDraftSettleWaitsForSendFailure(t *testing.T) {
	c, f, s, l := newBoardDraftTest(t)
	s.sendGate = make(chan struct{})
	s.sendError = errors.New("send refused")
	failed := 0
	c.onSendFailed = func() { failed++ }
	c.send()
	done := false
	c.settle(func(ok bool) {
		if !ok {
			t.Error("saved reply lost after send failure")
		}
		done = true
	})
	if done {
		t.Fatal("settle returned before send outcome")
	}
	close(s.sendGate)
	l.until(t, func() bool { return done })
	if failed != 1 || c.draft.sending || c.draft.closed || f.closed {
		t.Fatalf("failed=%d state=%+v form.closed=%v", failed, c.draft, f.closed)
	}
}

func TestBoardDraftDiscardHostFailureKeepsForm(t *testing.T) {
	c, f, s, l := newBoardDraftTest(t)
	var answer func(error)
	c.discardStored = func(account api.AccountID, draft api.DraftID, done func(error)) {
		if account != "account" || draft != "draft" {
			t.Fatal("discard got wrong identity")
		}
		answer = done
	}
	c.discardNow()
	if answer == nil || f.closed {
		t.Fatal("discard did not wait for host")
	}
	answer(errors.New("link is busy"))
	l.until(t, func() bool { return !c.discarding })
	methods, _ := s.calls()
	if f.closed || c.draft.closed || len(f.toasts) != 1 || len(methods) != 0 {
		t.Fatalf("failed discard closed form or sent RPC: %v", methods)
	}
	c.discardNow()
	answer(nil)
	l.until(t, func() bool { return f.closed })
	if !c.draft.discard {
		t.Fatal("successful discard did not end pane")
	}
}

func TestBoardDraftCloseNeverDeletesLateCommentSave(t *testing.T) {
	c, f, s, l := newBoardDraftTest(t)
	f.comment = true
	c.setOriginal("message", "", &api.DraftComment{})
	s.saveGate = make(chan struct{})
	c.markDirty()
	c.save(saveExplicit, nil)
	c.cleanup()
	close(s.saveGate)
	// The callback of that save must be consumed after closing as well.
	select {
	case callback := <-l.posted:
		callback()
	case <-time.After(3 * time.Second):
		t.Fatal("late save did not answer")
	}
	methods, _ := s.calls()
	if len(methods) != 1 || methods[0] != api.MethodDraftSave {
		t.Fatalf("board-owned comment was deleted: %v", methods)
	}
}

func TestFromBoardParamsOpensExistingDraftAsEdit(t *testing.T) {
	draft := api.Draft{ID: "draft", AccountID: "account", Version: 3, HTMLBody: "<p>reply</p>", InReplyTo: "message", Comment: &api.DraftComment{}}
	p := FromBoardParams(boardreply.Params{Account: "account", Draft: draft})
	if p.Kind != KindEdit || p.DraftID != draft.ID || p.Version != draft.Version || p.Comment != draft.Comment || p.BodyHTML != draft.HTMLBody {
		t.Fatalf("board editor lost stored draft identity: %+v", p)
	}
}
