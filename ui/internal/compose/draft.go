// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/editor"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/widget"
)

// autosaveDelay is how long after the last edit a dirty draft is saved.
const autosaveDelay = 30 // seconds

// richText is whether the editor's HTML is transmitted with the draft. The
// backend sanitises it in compose mode and derives the text alternative,
// so the formatting toolbar and inline images are on. Off, only the plain
// text goes over the wire (kept as a switch for a text-only build).
const richText = true

type saveReason int

const (
	saveExplicit saveReason = iota // Ctrl+S, menu, close dialog
	saveAutosave
)

// draftState is the lifecycle of the draft behind a window. A comment
// (comment.go) goes through it too, with the differences of the comment*
// and closesUnasked, deletesOnClose functions: no Save Draft, a silent
// autosave, and a copy that goes with the window unless it was sent.
type draftState struct {
	draftID api.DraftID
	version int

	inReplyTo  api.MessageID
	forwarding api.MessageID
	// replaces is the Drafts message the first save takes over
	// (draft.open); cleared once a save went through.
	replaces api.MessageID
	// explicitSave is set once the draft is the user's to keep: saved
	// with Ctrl+S, the menu or the close dialog, or opened from the Drafts
	// folder. Until then Discard in the close dialog deletes what the
	// autosave stored, which would otherwise live on in the Drafts folder.
	explicitSave bool

	dirty   bool // edits not yet persisted
	saving  bool // draft.save in flight
	sending bool
	// pendingAfterSave runs when the in-flight save finishes.
	pendingAfterSave []func(err error)

	autosave  glib.SourceHandle // 0 when not armed
	lastSaved time.Time
	lastError string // last autosave error shown as a toast
	flushed   flushEcho

	closed bool // window is gone; drop late callbacks
	// discard closes without asking; for a comment it also says that its
	// copy is settled (sent, or deleted already).
	discard bool
}

// flushEcho remembers the content a save's flush reported: the editor's
// "changed" carrying the same HTML is that report, not an edit. Without it
// every save marked the draft dirty again and armed the next autosave.
type flushEcho struct {
	html string
	set  bool
}

// record notes the HTML the flush reported.
func (f *flushEcho) record(html string) { f.html, f.set = html, true }

// echo reports whether html is what the last flush reported; any other
// content is an edit and forgets the record.
func (f *flushEcho) echo(html string) bool {
	if f.set && f.html == html {
		return true
	}
	f.html, f.set = "", false
	return false
}

func (w *Window) ctx() context.Context {
	// Callers run the call in a goroutine bounded by RPCTimeout; the parent
	// is background so a closed window does not cancel a save in flight.
	ctx, cancel := context.WithTimeout(context.Background(), widget.RPCTimeout)
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ctx
}

// rpc runs call off the main loop and then on it, unless the window closed.
func (w *Window) rpc(call func() (any, error), then func(v any, err error)) {
	w.rpcOr(call, then, nil)
}

// rpcOr is rpc with gone (optional) run on the main loop in place of then
// when the window closed meanwhile.
func (w *Window) rpcOr(call func() (any, error), then, gone func(v any, err error)) {
	go func() {
		v, err := call()
		glib.IdleAdd(func() {
			if w.draft.closed {
				if gone != nil {
					gone(v, err)
				}
				return
			}
			then(v, err)
		})
	}()
}

// editorChanged is the editor's "changed": the one a save's flush produces
// reports what is being saved; only other content is an edit.
func (w *Window) editorChanged() {
	if w.draft.flushed.echo(w.editor.HTML()) {
		return
	}
	w.markDirty()
}

// markDirty records an edit and arms the autosave timer.
func (w *Window) markDirty() {
	d := &w.draft
	d.dirty = true
	w.refreshStatus()
	if d.autosave == 0 {
		d.autosave = glib.TimeoutSecondsAdd(autosaveDelay, func() bool {
			d.autosave = 0
			if d.dirty && !d.saving {
				w.save(saveAutosave, nil)
			}
			return false
		})
	}
}

func (w *Window) refreshStatus() {
	w.setStatus(draftStatus(&w.draft, w.isComment(), w.m.Placeholder()))
}

// draftStatus is the status line under the window. A comment is saved
// only against a crash, not as a draft the user keeps: nothing to say
// about it but that it is being sent.
func draftStatus(d *draftState, comment, placeholder bool) string {
	switch {
	case d.sending:
		return i18n.T("Sending…")
	case comment:
		return ""
	case d.saving:
		return i18n.T("Saving draft…")
	case d.dirty:
		return i18n.T("Unsaved changes")
	case !d.lastSaved.IsZero():
		return fmt.Sprintf(i18n.T("Draft saved %s"), widget.FormatTime(d.lastSaved))
	case placeholder:
		return i18n.T("Using placeholder account")
	default:
		return ""
	}
}

// build assembles the wire draft from the rows and the editor's last
// content. Call after editor.Flush.
func (w *Window) build() api.Draft {
	to, cc, bcc, _ := w.recipients()
	d := api.Draft{
		ID:         w.draft.draftID,
		AccountID:  w.account().ID,
		Version:    w.draft.version,
		To:         to,
		CC:         cc,
		BCC:        bcc,
		Subject:    w.subject.Text(),
		TextBody:   w.editor.Text(),
		InReplyTo:  w.draft.inReplyTo,
		Forwarding: w.draft.forwarding,
		Replaces:   w.draft.replaces,
	}
	if richText {
		d.HTMLBody = w.editor.HTML()
	}
	// A comment has no recipients; of it draft.save reads only the
	// visibility, and the issue goes back as it came.
	d.Comment = wireComment(w.params.Comment, w.visibility())
	if to == nil {
		d.To = []api.Address{}
	}
	for _, a := range w.attachments {
		d.Attachments = append(d.Attachments, api.DraftAttachment{ID: a.ID})
	}
	return d
}

// save persists the draft; done (optional) runs with the outcome. A save
// already in flight queues done behind it.
func (w *Window) save(reason saveReason, done func(err error)) {
	d := &w.draft
	if done != nil {
		d.pendingAfterSave = append(d.pendingAfterSave, done)
	}
	if d.saving {
		return
	}
	if d.autosave != 0 {
		glib.SourceRemove(d.autosave)
		d.autosave = 0
	}
	d.saving = true
	d.dirty = false // edits during the call set it again
	w.refreshStatus()

	w.editor.Flush(func() {
		// Runs before OnChanged of the same message (editor.onMessage).
		d.flushed.record(w.editor.HTML())
		if d.closed {
			return
		}
		draft := w.build()
		w.rpcOr(func() (any, error) {
			var res api.DraftSaveResult
			err := w.m.client.Call(w.ctx(), api.MethodDraftSave, api.DraftSaveParams{Draft: draft}, &res)
			return res, err
		}, func(v any, err error) {
			d.saving = false
			if err != nil {
				d.dirty = true
				w.saveFailed(reason, err)
			} else {
				res := v.(api.DraftSaveResult)
				d.draftID, d.version = res.DraftID, res.Version
				d.replaces = ""
				d.explicitSave = d.explicitSave || reason == saveExplicit
				d.lastSaved = time.Now()
				d.lastError = ""
				if len(res.Attachments) != len(w.attachments) {
					w.setAttachments(res.Attachments)
				}
				if msg := blockedSummary(res.Blocked); msg != "" {
					w.toast(msg)
				}
			}
			w.refreshStatus()
			if d.dirty && d.autosave == 0 && err == nil {
				w.markDirty() // edits arrived during the save
			}
			pending := d.pendingAfterSave
			d.pendingAfterSave = nil
			for _, f := range pending {
				f(err)
			}
		}, func(v any, err error) {
			// The window went while a comment was being saved: no Drafts
			// folder keeps it, so the copy goes too.
			if res, ok := v.(api.DraftSaveResult); ok && err == nil && draft.Comment != nil {
				w.forget(draft.AccountID, res.DraftID)
			}
		})
	})
}

func (w *Window) saveFailed(reason saveReason, err error) {
	d := &w.draft
	var e *api.Error
	if errors.As(err, &e) {
		switch e.Code {
		case api.CodeConflict:
			// Local wins: the next save creates a fresh draft with our text.
			d.draftID, d.version, d.replaces = "", 0, ""
			w.toast(i18n.T("This draft was changed elsewhere; your text will be saved as a new draft"))
			return
		case api.CodeDraftNotFound:
			// Deleted meanwhile (its copy went to the Trash): the text
			// survives as a new draft, its attachments with it.
			d.draftID, d.version, d.replaces = "", 0, ""
			w.toast(i18n.T("This draft was removed elsewhere; your text will be saved as a new draft"))
			return
		case api.CodeMessageNotFound:
			if d.replaces != "" {
				// The Drafts message it was to take over is gone: save
				// without it.
				d.replaces = ""
				w.markDirty()
				return
			}
		}
	}
	if w.isComment() {
		// A comment is saved to be sent (send), and otherwise only
		// against a crash: a failed autosave says nothing and tries again.
		if text := commentSaveFailure(reason, err); text != "" {
			w.toast(text)
			return
		}
		w.log.Debug("comment autosave failed", "err", err)
		if d.autosave == 0 {
			w.markDirty()
		}
		return
	}
	text := widget.RPCErrorText(i18n.T("Saving the draft"), err)
	if reason == saveAutosave {
		// Do not nag every 30 s with the same failure (e.g. no backend).
		if text == d.lastError {
			w.log.Debug("autosave failed again", "err", err)
			return
		}
		d.lastError = text
	}
	w.toast(text)
	// Retry later.
	if d.autosave == 0 {
		w.markDirty()
	}
}

// send validates, saves if needed and queues the message. A comment has
// no recipients; it needs text (jira.SendProblem, checked on the editor's
// current content).
func (w *Window) send() {
	d := &w.draft
	if d.sending {
		return
	}
	comment := w.isComment()
	if !comment {
		to, cc, bcc, ok := w.recipients()
		if !ok {
			w.toast(i18n.T("Fix the highlighted recipients"))
			return
		}
		if len(to)+len(cc)+len(bcc) == 0 {
			w.toast(i18n.T("Add at least one recipient"))
			return
		}
	}
	d.sending = true
	w.actions["send"].SetEnabled(false)
	w.refreshStatus()
	fail := func() {
		d.sending = false
		w.actions["send"].SetEnabled(true)
		w.refreshStatus()
	}
	if !comment {
		w.queue(fail)
		return
	}
	w.editor.Flush(func() {
		// Runs before OnChanged of the same message: what it reports is
		// the content being sent, not an edit (flushEcho).
		d.flushed.record(w.editor.HTML())
		if d.closed {
			return
		}
		if problem := jira.SendProblem(w.editor.Text(), i18n.Tr); problem != "" {
			w.toast(problem)
			fail()
			return
		}
		w.queue(fail)
	})
}

// queue is send's second half: the explicit save, then message.send; fail
// gives Send back.
func (w *Window) queue(fail func()) {
	d := &w.draft
	w.save(saveExplicit, func(err error) {
		if err != nil {
			fail()
			return
		}
		accountID, draftID, version := w.account().ID, d.draftID, d.version
		w.rpc(func() (any, error) {
			var res api.MessageSendResult
			err := w.m.client.Call(w.ctx(), api.MethodMessageSend,
				api.MessageSendParams{AccountID: accountID, DraftID: draftID, Version: version}, &res)
			return res, err
		}, func(_ any, err error) {
			if err != nil {
				var e *api.Error
				if errors.As(err, &e) && e.Code == api.CodeConflict {
					d.draftID, d.version = "", 0
					d.dirty = true
				}
				w.toast(widget.RPCErrorText(i18n.T("Sending"), err))
				fail()
				return
			}
			d.discard = true
			if w.m.OnSent != nil {
				w.m.OnSent(queuedText(w.isComment()))
			}
			w.Close()
		})
	})
}

// queuedText is the toast after Send went through: a comment's
// (jira.CommentQueued) or a message's.
func queuedText(comment bool) string {
	if comment {
		return jira.CommentQueued(i18n.Tr)
	}
	return i18n.T("Message queued for sending")
}

// deleteDraft deletes the stored draft (and with it its copy in the
// Drafts folder); the window is closing, so a failure is only logged.
func (w *Window) deleteDraft() {
	w.forget(w.account().ID, w.draft.draftID)
}

// forget is draft.delete of draft id in the background, also once the
// window is gone; a failure is only logged.
func (w *Window) forget(accountID api.AccountID, id api.DraftID) {
	c, log, ctx := w.m.client, w.log, w.ctx()
	go func() {
		err := c.Call(ctx, api.MethodDraftDelete,
			api.DraftDeleteParams{AccountID: accountID, DraftID: id}, &api.DraftDeleteResult{})
		if err != nil {
			log.Debug("draft.delete", "err", err)
		}
	}()
}

// discard drops the draft (after confirmation when the setting is on). A
// draft never saved has no id, but may hold attachments the backend
// imported for it (the template's pictures and files); those are released
// rather than left for the sweep.
func (w *Window) discard() {
	proceed := func() {
		accountID := w.account().ID
		if w.draft.draftID != "" {
			w.deleteDraft()
		} else {
			for _, a := range w.attachments {
				attID := a.ID
				w.rpc(func() (any, error) {
					return nil, w.m.client.Call(w.ctx(), api.MethodAttachmentRemove,
						api.AttachmentRemoveParams{AccountID: accountID, AttachmentID: attID}, &api.AttachmentRemoveResult{})
				}, func(_ any, err error) {
					if err != nil {
						w.log.Debug("attachment.remove", "err", err)
					}
				})
			}
		}
		w.draft.discard = true
		w.Close()
	}
	if !w.m.settings.ConfirmDelete() || (!w.draft.dirty && w.draft.draftID == "") {
		proceed()
		return
	}
	widget.ConfirmDestructive(w, i18n.T("Discard this message?"), "", i18n.T("_Discard"), proceed)
}

// closeRequest keeps the window open while there are unsaved edits and
// asks what to do with them. A comment has no Save Draft: the question is
// whether to discard it, and its saved copy goes too.
func (w *Window) closeRequest() bool {
	d := &w.draft
	if closesUnasked(d, w.isComment(), w.editor.Text()) {
		w.cleanup()
		return false
	}
	if w.isComment() {
		widget.ConfirmDestructive(w, i18n.T("Discard this message?"), "", i18n.T("_Discard"), func() {
			if d.closed {
				return
			}
			if d.draftID != "" {
				w.deleteDraft()
			}
			d.discard = true
			w.Close()
		})
		return true
	}
	dlg := adw.NewAlertDialog(i18n.T("Save changes to this draft?"), "")
	dlg.AddResponse("cancel", i18n.T("_Cancel"))
	dlg.AddResponse("discard", i18n.T("_Discard"))
	dlg.AddResponse("save", i18n.T("_Save Draft"))
	dlg.SetResponseAppearance("discard", adw.ResponseDestructive)
	dlg.SetResponseAppearance("save", adw.ResponseSuggested)
	dlg.SetDefaultResponse("save")
	dlg.SetCloseResponse("cancel")
	dlg.ConnectResponse(func(response string) {
		switch response {
		case "discard":
			d.discard = true
			if !d.explicitSave && d.draftID != "" {
				w.deleteDraft()
			}
			w.Close()
		case "save":
			w.save(saveExplicit, func(err error) {
				if err == nil {
					d.discard = true
					w.Close()
				}
				// On failure the toast is shown and the window stays.
			})
		}
	})
	dlg.Present(w)
	return true
}

// cleanup runs when the window really closes. A comment's saved copy goes
// with the window unless it was sent (deletesOnClose).
func (w *Window) cleanup() {
	d := &w.draft
	if deletesOnClose(d, w.isComment()) {
		w.deleteDraft()
	}
	d.closed = true
	if d.autosave != 0 {
		glib.SourceRemove(d.autosave)
		d.autosave = 0
	}
	for _, s := range w.suggest {
		s.hide() // a pending search must not touch the rows after this
	}
	for _, a := range w.attachments {
		if a.Inline {
			editor.UnregisterCID(a.ContentID)
		}
	}
	w.rewrite.close()
	// The editor's web process goes with the window, not when the view is
	// collected some time later.
	w.editor.Close()
	w.m.remove(w)
}

// closesUnasked is closeRequest's first branch: the window may go without
// a question, having no edits to lose. A comment, which no Drafts folder
// keeps, goes unasked only while there is nothing in it (jira.SendProblem
// on text, the editor's last reported text), a save under way or not (its
// copy goes too, deletesOnClose).
func closesUnasked(d *draftState, comment bool, text string) bool {
	if d.discard {
		return true
	}
	if comment {
		return jira.SendProblem(text, i18n.Tr) != ""
	}
	return !d.dirty && !d.saving
}

// deletesOnClose reports whether cleanup deletes the saved draft: a
// comment's copy goes with the window unless it was sent or deleted
// already (draftState.discard); its autosave is only against a crash. An
// e-mail's draft stays.
func deletesOnClose(d *draftState, comment bool) bool {
	return comment && !d.discard && d.draftID != ""
}

// commentSaveFailure is the toast after a failed save of a comment that
// was no conflict (saveFailed): a comment is saved on its way out, so the
// explicit save says Sending; its autosave, only against a crash, says
// nothing ("").
func commentSaveFailure(reason saveReason, err error) string {
	if reason == saveAutosave {
		return ""
	}
	return widget.RPCErrorText(i18n.T("Sending"), err)
}

// blockedSummary describes what the backend's sanitiser removed.
func blockedSummary(b api.BlockedContent) string {
	n := b.RemoteImages + b.RemoteStyles + b.RemoteFonts + b.Scripts + b.Forms +
		b.EventHandlers + b.DangerousURLs + b.EmbeddedFrames + b.TrackingPixels
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(i18n.N("%d unsafe element was removed from the message",
		"%d unsafe elements were removed from the message", n), n)
}

// skippedSummary says how many parts of the original a reply or forward
// went without (draft.create's skipped); nothing when it took them all.
func skippedSummary(n int) string {
	if n <= 0 {
		return ""
	}
	// TRANSLATORS: %d is the number of files of the forwarded (or quoted) message that the new one lacks.
	return fmt.Sprintf(i18n.N("%d attachment of the original could not be attached",
		"%d attachments of the original could not be attached", n), n)
}
