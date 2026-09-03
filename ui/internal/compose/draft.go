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
	"github.com/schotek/malachi/ui/internal/widget"
)

// autosaveDelay is how long after the last edit a dirty draft is saved.
const autosaveDelay = 30 // seconds

type saveReason int

const (
	saveExplicit saveReason = iota // Ctrl+S, menu, close dialog
	saveAutosave
)

// draftState is the lifecycle of the draft behind a window.
type draftState struct {
	draftID api.DraftID
	version int

	inReplyTo  api.MessageID
	forwarding api.MessageID

	dirty   bool // edits not yet persisted
	saving  bool // draft.save in flight
	sending bool
	// pendingAfterSave runs when the in-flight save finishes.
	pendingAfterSave []func(err error)

	autosave  glib.SourceHandle // 0 when not armed
	lastSaved time.Time
	lastError string // last autosave error shown as a toast

	closed  bool // window is gone; drop late callbacks
	discard bool // close without asking
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
	go func() {
		v, err := call()
		glib.IdleAdd(func() {
			if w.draft.closed {
				return
			}
			then(v, err)
		})
	}()
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
	d := &w.draft
	switch {
	case d.sending:
		w.setStatus("Sending…")
	case d.saving:
		w.setStatus("Saving draft…")
	case d.dirty:
		w.setStatus("Unsaved changes")
	case !d.lastSaved.IsZero():
		w.setStatus("Draft saved " + d.lastSaved.Format("15:04"))
	case w.m.Placeholder():
		w.setStatus("Using placeholder account")
	default:
		w.setStatus("")
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
		HTMLBody:   w.editor.HTML(),
		InReplyTo:  w.draft.inReplyTo,
		Forwarding: w.draft.forwarding,
	}
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
		if d.closed {
			return
		}
		draft := w.build()
		w.rpc(func() (any, error) {
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
		})
	})
}

func (w *Window) saveFailed(reason saveReason, err error) {
	d := &w.draft
	var e *api.Error
	if errors.As(err, &e) && e.Code == api.CodeConflict {
		// Local wins: the next save creates a fresh draft with our text.
		d.draftID, d.version = "", 0
		w.toast("This draft was changed elsewhere; your text will be saved as a new draft")
		return
	}
	text := widget.RPCErrorText("Saving the draft", err)
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

// send validates, saves if needed and queues the message.
func (w *Window) send() {
	d := &w.draft
	if d.sending {
		return
	}
	to, cc, bcc, ok := w.recipients()
	if !ok {
		w.toast("Fix the highlighted recipients")
		return
	}
	if len(to)+len(cc)+len(bcc) == 0 {
		w.toast("Add at least one recipient")
		return
	}
	d.sending = true
	w.actions["send"].SetEnabled(false)
	w.refreshStatus()
	fail := func() {
		d.sending = false
		w.actions["send"].SetEnabled(true)
		w.refreshStatus()
	}
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
				w.toast(widget.RPCErrorText("Sending", err))
				fail()
				return
			}
			d.discard = true
			if w.m.OnSent != nil {
				w.m.OnSent("Message queued for sending")
			}
			w.Close()
		})
	})
}

// discard drops the draft (after confirmation when the setting is on).
func (w *Window) discard() {
	proceed := func() {
		if id := w.draft.draftID; id != "" {
			accountID := w.account().ID
			w.rpc(func() (any, error) {
				return nil, w.m.client.Call(w.ctx(), api.MethodDraftDelete,
					api.DraftDeleteParams{AccountID: accountID, DraftID: id}, &api.DraftDeleteResult{})
			}, func(_ any, err error) {
				if err != nil {
					w.log.Debug("draft.delete", "err", err)
				}
			})
		}
		w.draft.discard = true
		w.Close()
	}
	if !w.m.settings.ConfirmDelete() || (!w.draft.dirty && w.draft.draftID == "") {
		proceed()
		return
	}
	widget.ConfirmDestructive(w, "Discard this message?", "", "_Discard", proceed)
}

// closeRequest keeps the window open while there are unsaved edits and
// asks what to do with them.
func (w *Window) closeRequest() bool {
	d := &w.draft
	if d.discard || (!d.dirty && !d.saving) {
		w.cleanup()
		return false
	}
	dlg := adw.NewAlertDialog("Save changes to this draft?", "")
	dlg.AddResponse("cancel", "_Cancel")
	dlg.AddResponse("discard", "_Discard")
	dlg.AddResponse("save", "_Save Draft")
	dlg.SetResponseAppearance("discard", adw.ResponseDestructive)
	dlg.SetResponseAppearance("save", adw.ResponseSuggested)
	dlg.SetDefaultResponse("save")
	dlg.SetCloseResponse("cancel")
	dlg.ConnectResponse(func(response string) {
		switch response {
		case "discard":
			d.discard = true
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

// cleanup runs when the window really closes.
func (w *Window) cleanup() {
	d := &w.draft
	d.closed = true
	if d.autosave != 0 {
		glib.SourceRemove(d.autosave)
		d.autosave = 0
	}
	for _, a := range w.attachments {
		if a.Inline {
			editor.UnregisterCID(a.ContentID)
		}
	}
	w.m.remove(w)
}

// blockedSummary describes what the backend's sanitiser removed.
func blockedSummary(b api.BlockedContent) string {
	n := b.RemoteImages + b.RemoteStyles + b.RemoteFonts + b.Scripts + b.Forms +
		b.EventHandlers + b.DangerousURLs + b.EmbeddedFrames + b.TrackingPixels
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d unsafe element(s) were removed from the message", n)
}
