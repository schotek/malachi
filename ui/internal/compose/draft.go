// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The lifecycle of the draft behind a compose pane: autosave, build, save,
// send, discard, closeRequest, cleanup, settle and finish. Ported from the
// macOS reference (macos/Sources/MalachiCore/Controllers/ComposeDraftController.swift)
// so a pane (pane.go) and the board's inline suggested reply share one
// implementation, picked apart from the widgets so it is testable without
// GTK or WebKit: draftController reads and writes a pane through the
// composeForm interface (pane.go implements it over the real widgets;
// draft_test.go uses a fake) and talks to the daemon through the caller
// interface (*client.Client in production, a fake in tests).

// autosaveDelay is how long after the last edit a dirty draft is saved.
const autosaveDelay = 30 * time.Second

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

// Owner says who keeps the draft behind a pane.
type Owner int

const (
	// OwnerWindow: the compose window (compose.Window). Closing may ask,
	// delete an unsaved draft or a comment's copy, and a conflict or a
	// draft deleted elsewhere starts a new draft.
	OwnerWindow Owner = iota
	// OwnerBoard: a board case's suggested reply edited inline (a local
	// draft the case links). The board keeps it, so nothing here ever
	// deletes it except Discard (through DiscardStored), closing never
	// asks, a conflict keeps our text in the same draft (draft.get for
	// the version) and a draft deleted elsewhere is reported (OnLost),
	// never recreated.
	OwnerBoard
)

// draftCloseAnswer is what the user answered to "Save changes to this
// draft?".
type draftCloseAnswer int

const (
	answerCancel draftCloseAnswer = iota
	answerDiscard
	answerSave
)

// draftState is the lifecycle of the draft behind a pane.
type draftState struct {
	draftID api.DraftID
	version int

	inReplyTo  api.MessageID
	forwarding api.MessageID
	// comment is the issue a comment draft goes to, as draft.create
	// returned it; nil for an e-mail.
	comment *api.DraftComment
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

	lastSaved time.Time
	lastError string // last autosave error shown as a toast

	closed bool // the pane is gone; drop late callbacks
	// discard closes without asking; for a comment it also says that its
	// copy is settled (sent, or deleted already).
	discard bool

	// OwnerBoard only:
	// lost: the draft was deleted elsewhere (draftNotFound).
	lost bool
}

// composeForm is what the draft controller reads from and writes to the
// pane (the rows, the editor, the chips, the status line, the toasts).
// pane.go implements it over the real widgets; draft_test.go uses a fake.
// Mirrors macOS ComposeForm.
type composeForm interface {
	// account is the selected From identity: the placeholder account
	// while the backend lists none, or (board owner, inline layout) the
	// draft's own account, never nil.
	account() api.Account
	// recipients parses the three rows; ok is false when any token is
	// invalid.
	recipients() (to, cc, bcc []api.Address, ok bool)
	subject() string
	// attachments are what the pane lists, in order.
	attachments() []api.DraftAttachment
	// editorHTML / editorText are the editor's last reported content.
	editorHTML() string
	editorText() string
	// flushEditor asks the editor for its current content and calls done
	// once it reported it.
	flushEditor(done func())
	// setAttachments replaces the list and the chips with what the
	// backend kept.
	setAttachments(atts []api.DraftAttachment)
	setStatus(text string)
	toast(text string)
	// setSendEnabled toggles the Send button (and menu item).
	setSendEnabled(enabled bool)
	// closeForm is called once the controller decided the pane goes:
	// sent, discarded, or closed after the close question.
	closeForm()
	// isComment: the pane writes a comment on an issue, no recipients, no
	// Save Draft (no Drafts folder keeps a comment).
	isComment() bool
	// commentVisibility is the comment's visibility as chosen in the
	// pane; public for an e-mail.
	commentVisibility() api.CommentVisibility
}

// caller is the one daemon method the draft controller needs: a client's
// Call. *client.Client satisfies it; tests use a fake.
type caller interface {
	Call(ctx context.Context, method string, params, result any) error
}

// draftController is compose/draft.go's old Window methods, generalised
// over composeForm and made owner-aware (ComposeDraftController.swift).
type draftController struct {
	call  caller
	ctxFn func() context.Context
	// loop runs save/send continuations back on the owning loop (GTK's
	// main loop in production) and the autosave timer
	// (assistantpanel.Loop; glibLoop in pane.go, a fake in tests).
	loop assistantpanel.Loop
	// confirmDelete reports the setting that makes Discard ask.
	confirmDelete func() bool
	// placeholder reports whether the From list is the placeholder
	// identity (the status line says so).
	placeholder func() bool
	owner       Owner
	log         *slog.Logger
	// now is the clock behind lastSaved; tests pin it.
	now func() time.Time
	// delay is autosaveDelay; tests shorten it.
	delay time.Duration

	// form is the pane; nil briefly in tests that construct the
	// controller before wiring it.
	form composeForm

	draft draftState

	// onSent is called with a short confirmation once a send went
	// through ("Message queued for sending", or a comment's).
	onSent func(text string)
	// onLost: OwnerBoard only, the draft was deleted elsewhere. The
	// controller has already abandoned itself; no new draft is made.
	onLost func()
	// discardStored: OwnerBoard only, Discard deletes the stored draft
	// through this (the board's board.discardDraft, which also unlinks
	// it from the case) instead of a plain draft.delete. A failure keeps
	// the form open with a toast.
	discardStored func(accountID api.AccountID, draftID api.DraftID, done func(err error))
	// onSendFailed is called once a send failed and Send is back (after
	// the form's toast saying why); not when the draft was lost (onLost).
	onSendFailed func()
	// unregisterCID forgets an inline picture's cid: registration
	// (editor.UnregisterCID); nil is a no-op (tests).
	unregisterCID func(contentID string)

	// confirmDiscard is widget.ConfirmDestructive's shape: heading, body,
	// label, and what runs only if the user confirms. nil always confirms.
	confirmDiscard func(heading, body, label string, proceed func())
	// saveDraftQuestion presents "Save changes to this draft?"; done runs
	// with the answer. nil always cancels.
	saveDraftQuestion func(done func(answer draftCloseAnswer))

	pendingAfterSave []func(err error)
	// autosavePending: the autosave timer is armed (Go's old autosave !=
	// 0); autosaveGen invalidates a stale fire after cancelAutosave.
	autosavePending bool
	autosaveGen     int

	// OwnerBoard only:
	// sendPending: a send is under way and has not answered yet (unlike
	// draft.sending, never reset on success: the pane closes anyway).
	sendPending bool
	// sendWaiters run once sendPending clears (settle waiting on a send).
	sendWaiters []func()
	// refetching: the draft.get after a conflict is in flight.
	refetching bool
	// refetchWaiters run once refetching clears.
	refetchWaiters []func()
	// lastSavedBody is the body of the last draft.save that went through,
	// so settle/finish can see an edit the editor reported only with its
	// flush (hasLastSavedBody: "" is a valid saved body).
	lastSavedBody    string
	hasLastSavedBody bool
	// editorRendered: the editor's own rendering of the loaded draft is
	// known (editorReady), so what it reports from now on is comparable.
	editorRendered bool
	// discarding: Discard is deleting the stored draft.
	discarding bool
}

// errFormClosed answers a pendingAfterSave callback the cleanup cancels
// (Swift's CancellationError).
var errFormClosed = errors.New("compose: form closed")

// newDraftController makes a controller with sane defaults; callers set
// form, the hooks and the dialog functions before use.
func newDraftController(owner Owner, call caller, log *slog.Logger) *draftController {
	return &draftController{
		call:          call,
		owner:         owner,
		log:           log,
		now:           time.Now,
		delay:         autosaveDelay,
		confirmDelete: func() bool { return true },
		placeholder:   func() bool { return false },
		ctxFn:         func() context.Context { return context.Background() },
	}
}

// autosaveArmed reports whether the autosave timer is armed (tests).
func (c *draftController) autosaveArmed() bool { return c.autosavePending }

// isComment reports whether the pane writes a comment (composeForm, or the
// controller's own setOriginal when there is no form yet).
func (c *draftController) isComment() bool {
	if c.form != nil {
		return c.form.isComment()
	}
	return c.draft.comment != nil
}

// setOriginal records what draft.create answered: the message a reply or
// forward refers to, and for a comment the issue it goes to.
func (c *draftController) setOriginal(inReplyTo, forwarding api.MessageID, comment *api.DraftComment) {
	c.draft.inReplyTo = inReplyTo
	c.draft.forwarding = forwarding
	c.draft.comment = comment
}

// setOpened records the saved draft the pane edits: its id and version
// make the saves updates, and a draft opened from the Drafts folder
// (fromDrafts) is never deleted by closing unasked.
func (c *draftController) setOpened(draftID api.DraftID, version int, replaces api.MessageID, fromDrafts bool) {
	c.draft.draftID = draftID
	c.draft.version = version
	c.draft.replaces = replaces
	c.draft.explicitSave = fromDrafts
}

// canCloseWithoutAsking is closeRequest's first branch: the pane may go
// without a question.
func (c *draftController) canCloseWithoutAsking() bool {
	text := ""
	if c.form != nil {
		text = c.form.editorText()
	}
	return closesUnasked(&c.draft, c.owner, c.isComment(), text)
}

// closesUnasked is canCloseWithoutAsking's pure core: the board keeps its
// draft (never asks), a comment goes unasked only while there is nothing
// in it (jira.SendProblem on text, the editor's last reported text), an
// e-mail only without unsaved edits or a save under way.
func closesUnasked(d *draftState, owner Owner, comment bool, text string) bool {
	if d.discard || owner == OwnerBoard {
		return true
	}
	if comment {
		return jira.SendProblem(text, i18n.Tr) != ""
	}
	return !d.dirty && !d.saving
}

// deletesOnClose reports whether cleanup deletes the saved draft: a
// window-owned comment's copy goes with the window unless it was sent or
// deleted already; the board keeps its draft always, and an e-mail's draft
// stays in Drafts.
func deletesOnClose(d *draftState, owner Owner, comment bool) bool {
	return owner == OwnerWindow && comment && !d.discard && d.draftID != ""
}

// MARK: Dirty state and status

// markDirty records an edit and arms the autosave timer (once; later edits
// before it fires do not restart the clock).
func (c *draftController) markDirty() {
	if c.draft.closed {
		return
	}
	c.draft.dirty = true
	c.refreshStatus()
	if c.autosavePending || c.loop == nil {
		return
	}
	c.autosavePending = true
	gen := c.autosaveGen
	c.loop.After(c.delay, func() {
		if gen != c.autosaveGen || !c.autosavePending {
			return
		}
		c.autosavePending = false
		if c.draft.dirty && !c.draft.saving {
			c.save(saveAutosave, nil)
		}
	})
}

func (c *draftController) cancelAutosave() {
	c.autosaveGen++
	c.autosavePending = false
}

func (c *draftController) refreshStatus() {
	if c.form == nil {
		return
	}
	c.form.setStatus(draftStatus(&c.draft, c.isComment(), c.placeholder()))
}

// draftStatus is the status line under the pane. A comment is saved only
// against a crash, not as a draft the user keeps: nothing to say about it
// but that it is being sent.
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

// MARK: Building and saving

// build assembles the wire draft from the rows and the editor's last
// content. Call after flushEditor; nil without a form.
func (c *draftController) build() *api.Draft {
	if c.form == nil {
		return nil
	}
	to, cc, bcc, _ := c.form.recipients()
	d := api.Draft{
		ID:         c.draft.draftID,
		AccountID:  c.form.account().ID,
		Version:    c.draft.version,
		To:         to,
		CC:         cc,
		BCC:        bcc,
		Subject:    c.form.subject(),
		TextBody:   c.form.editorText(),
		InReplyTo:  c.draft.inReplyTo,
		Forwarding: c.draft.forwarding,
		Replaces:   c.draft.replaces,
	}
	if richText {
		d.HTMLBody = c.form.editorHTML()
	}
	if to == nil {
		d.To = []api.Address{}
	}
	// Of a comment draft.save reads only the visibility; the issue goes
	// back as it came.
	if c.isComment() {
		d.Comment = wireComment(c.draft.comment, c.form.commentVisibility())
	}
	for _, a := range c.form.attachments() {
		d.Attachments = append(d.Attachments, api.DraftAttachment{ID: a.ID})
	}
	return &d
}

// bodyOf is the body a save sent (settle/finish compare the editor with
// it): the HTML when there is one, the text otherwise.
func bodyOf(d api.Draft) string {
	if d.HTMLBody != "" {
		return d.HTMLBody
	}
	return d.TextBody
}

// save persists the draft; done (optional) runs with the outcome. A save
// already in flight queues done behind it.
func (c *draftController) save(reason saveReason, done func(err error)) {
	if done != nil {
		c.pendingAfterSave = append(c.pendingAfterSave, done)
	}
	if c.draft.saving {
		return
	}
	c.cancelAutosave()
	c.draft.saving = true
	c.draft.dirty = false // edits during the call set it again
	c.refreshStatus()
	if c.form == nil {
		return
	}
	c.form.flushEditor(func() {
		if c.draft.closed {
			return
		}
		wire := c.build()
		if wire == nil {
			return
		}
		owner := c.owner
		call, ctx := c.call, c.ctxFn()
		go func() {
			var res api.DraftSaveResult
			err := call.Call(ctx, api.MethodDraftSave, api.DraftSaveParams{Draft: *wire}, &res)
			c.loop.Post(func() {
				if c.draft.closed {
					// The pane went while a comment was being saved: no
					// Drafts folder keeps it, so the copy goes too. The
					// board's copy is the case's suggested reply: it stays.
					if owner == OwnerWindow && wire.Comment != nil && err == nil {
						c.forget(wire.AccountID, res.DraftID)
					}
					return
				}
				if err == nil {
					c.lastSavedBody, c.hasLastSavedBody = bodyOf(*wire), true
				}
				c.saved(reason, res, err)
			})
		}()
	})
}

func (c *draftController) saved(reason saveReason, res api.DraftSaveResult, err error) {
	c.draft.saving = false
	var failure error
	if err != nil {
		failure = err
		c.draft.dirty = true
		c.saveFailed(reason, err)
	} else {
		c.draft.draftID = res.DraftID
		c.draft.version = res.Version
		c.draft.replaces = ""
		c.draft.explicitSave = c.draft.explicitSave || reason == saveExplicit
		c.draft.lastSaved = c.now()
		c.draft.lastError = ""
		if c.form != nil {
			if len(res.Attachments) != len(c.form.attachments()) {
				c.form.setAttachments(res.Attachments)
			}
			if msg := blockedSummary(res.Blocked); msg != "" {
				c.form.toast(msg)
			}
		}
	}
	c.refreshStatus()
	if c.draft.dirty && !c.autosavePending && failure == nil {
		c.markDirty() // edits arrived during the save
	}
	pending := c.pendingAfterSave
	c.pendingAfterSave = nil
	for _, f := range pending {
		f(failure)
	}
}

// saveFailed: a conflict or a draft deleted meanwhile starts over with a
// fresh draft (local wins) for a window owner; the board instead keeps our
// text in the very same draft (refetchVersion) or reports it lost
// (markLost). A Drafts message to take over that is gone is dropped from
// the next save; an autosave does not nag with the same failure every 30s.
func (c *draftController) saveFailed(reason saveReason, err error) {
	var e *api.Error
	if c.owner == OwnerBoard && errors.As(err, &e) {
		switch e.Code {
		case api.CodeConflict:
			c.refetchVersion()
			return
		case api.CodeDraftNotFound:
			c.markLost()
			return
		}
	}
	if errors.As(err, &e) {
		switch {
		case e.Code == api.CodeConflict:
			// Local wins: the next save creates a fresh draft with our text.
			c.draft.draftID, c.draft.version, c.draft.replaces = "", 0, ""
			if c.form != nil {
				c.form.toast(i18n.T("This draft was changed elsewhere; your text will be saved as a new draft"))
			}
			return
		case e.Code == api.CodeDraftNotFound:
			// Deleted meanwhile (its copy went to the Trash): the text
			// survives as a new draft, its attachments with it.
			c.draft.draftID, c.draft.version, c.draft.replaces = "", 0, ""
			if c.form != nil {
				c.form.toast(i18n.T("This draft was removed elsewhere; your text will be saved as a new draft"))
			}
			return
		case e.Code == api.CodeMessageNotFound && c.draft.replaces != "":
			// The Drafts message it was to take over is gone: save
			// without it.
			c.draft.replaces = ""
			c.markDirty()
			return
		}
	}
	if c.isComment() {
		// A comment is saved to be sent (send), and otherwise only
		// against a crash: a failed autosave says nothing and tries again.
		if text := commentSaveFailure(reason, err); text != "" {
			if c.form != nil {
				c.form.toast(text)
			}
			return
		}
		c.log.Debug("comment autosave failed", "err", err)
		if !c.autosavePending {
			c.markDirty()
		}
		return
	}
	text := widget.RPCErrorText(i18n.T("Saving the draft"), err)
	if reason == saveAutosave {
		// Do not nag every 30s with the same failure (e.g. no backend).
		if text == c.draft.lastError {
			c.log.Debug("autosave failed again", "err", err)
			return
		}
		c.draft.lastError = text
	}
	if c.form != nil {
		c.form.toast(text)
	}
	// Retry later.
	if !c.autosavePending {
		c.markDirty()
	}
}

// MARK: Sending

// send validates, saves if needed and queues the message. A comment has
// no recipients; it needs text (jira.SendProblem, checked on the editor's
// current content).
func (c *draftController) send() {
	if c.draft.sending || c.form == nil {
		return
	}
	comment := c.isComment()
	if !comment {
		to, cc, bcc, ok := c.form.recipients()
		if !ok {
			c.form.toast(i18n.T("Fix the highlighted recipients"))
			return
		}
		if len(to)+len(cc)+len(bcc) == 0 {
			c.form.toast(i18n.T("Add at least one recipient"))
			return
		}
	}
	c.draft.sending = true
	c.sendPending = true
	c.form.setSendEnabled(false)
	c.refreshStatus()
	fail := func() {
		c.draft.sending = false
		if c.form != nil {
			c.form.setSendEnabled(true)
		}
		c.refreshStatus()
		if c.onSendFailed != nil {
			c.onSendFailed()
		}
		c.sendAnswered()
	}
	if !comment {
		c.queue(fail)
		return
	}
	c.form.flushEditor(func() {
		if c.draft.closed || c.form == nil {
			return
		}
		if problem := jira.SendProblem(c.form.editorText(), i18n.Tr); problem != "" {
			c.form.toast(problem)
			fail()
			return
		}
		c.queue(fail)
	})
}

// queue is send's second half: the explicit save, then message.send; fail
// gives Send back.
func (c *draftController) queue(fail func()) {
	c.save(saveExplicit, func(err error) {
		if err != nil {
			fail()
			return
		}
		if c.form == nil || c.draft.draftID == "" {
			fail()
			return
		}
		accountID, draftID, version := c.form.account().ID, c.draft.draftID, c.draft.version
		call, ctx := c.call, c.ctxFn()
		go func() {
			var res api.MessageSendResult
			err := call.Call(ctx, api.MethodMessageSend,
				api.MessageSendParams{AccountID: accountID, DraftID: draftID, Version: version}, &res)
			c.loop.Post(func() {
				if c.draft.closed {
					return
				}
				if err != nil {
					var e *api.Error
					if c.owner == OwnerBoard && errors.As(err, &e) && e.Code == api.CodeDraftNotFound {
						c.markLost()
						return
					}
					if errors.As(err, &e) && e.Code == api.CodeConflict {
						if c.owner == OwnerBoard {
							c.draft.dirty = true
							c.refetchVersion()
						} else {
							c.draft.draftID, c.draft.version, c.draft.dirty = "", 0, true
						}
					}
					if c.form != nil {
						c.form.toast(widget.RPCErrorText(i18n.T("Sending"), err))
					}
					fail()
					return
				}
				comment := c.isComment()
				c.draft.discard = true
				if c.onSent != nil {
					c.onSent(queuedText(comment))
				}
				if c.form != nil {
					c.form.closeForm()
				}
				c.sendAnswered()
			})
		}()
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

// MARK: Discarding and closing

// deleteDraft deletes the stored draft (and with it its copy in the
// Drafts folder); the pane is closing, so a failure is only logged.
func (c *draftController) deleteDraft() {
	if c.form == nil {
		return
	}
	c.forget(c.form.account().ID, c.draft.draftID)
}

// forget is draft.delete of id in the background, also once the pane is
// gone; a failure is only logged.
func (c *draftController) forget(accountID api.AccountID, id api.DraftID) {
	call, log, ctx := c.call, c.log, c.ctxFn()
	go func() {
		err := call.Call(ctx, api.MethodDraftDelete,
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
func (c *draftController) discard() {
	if c.form == nil {
		return
	}
	proceed := func() { c.discardNow() }
	if !c.confirmDelete() || (!c.draft.dirty && c.draft.draftID == "") {
		proceed()
		return
	}
	c.doConfirmDiscard(i18n.T("Discard this message?"), "", i18n.T("_Discard"), func() {
		if c.draft.closed {
			return
		}
		proceed()
	})
}

func (c *draftController) discardNow() {
	if c.form == nil {
		return
	}
	accountID := c.form.account().ID
	if c.owner == OwnerBoard && c.draft.draftID != "" {
		c.discardBoard(accountID, c.draft.draftID)
		return
	}
	if c.draft.draftID != "" {
		c.deleteDraft()
	} else {
		call, log, ctx := c.call, c.log, c.ctxFn()
		for _, a := range c.form.attachments() {
			attID := a.ID
			go func() {
				err := call.Call(ctx, api.MethodAttachmentRemove,
					api.AttachmentRemoveParams{AccountID: accountID, AttachmentID: attID}, &api.AttachmentRemoveResult{})
				if err != nil {
					log.Debug("attachment.remove", "err", err)
				}
			}()
		}
	}
	c.draft.discard = true
	c.form.closeForm()
}

// discardBoard: OwnerBoard, a saved draft. The stored draft goes through
// discardStored (or draft.delete); only then does the form close. A
// failure says so and keeps the form, its text and the autosave.
func (c *draftController) discardBoard(accountID api.AccountID, draftID api.DraftID) {
	if c.discarding {
		return
	}
	c.discarding = true
	c.cancelAutosave()
	deliver := func(err error) {
		if c.draft.closed {
			return
		}
		c.discarding = false
		if err != nil {
			var e *api.Error
			if !(errors.As(err, &e) && e.Code == api.CodeDraftNotFound) {
				// Not already gone (what Discard wanted): report and keep
				// the form.
				if c.form != nil {
					c.form.toast(widget.RPCErrorText(i18n.T("Discarding the draft"), err))
				}
				if c.draft.dirty {
					c.markDirty()
				}
				return
			}
		}
		c.draft.discard = true
		if c.form != nil {
			c.form.closeForm()
		}
	}
	if c.discardStored != nil {
		c.discardStored(accountID, draftID, func(err error) {
			c.loop.Post(func() { deliver(err) })
		})
		return
	}
	call, ctx := c.call, c.ctxFn()
	go func() {
		err := call.Call(ctx, api.MethodDraftDelete, api.DraftDeleteParams{AccountID: accountID, DraftID: draftID}, &api.DraftDeleteResult{})
		c.loop.Post(func() { deliver(err) })
	}()
}

func (c *draftController) doConfirmDiscard(heading, body, label string, proceed func()) {
	if c.confirmDiscard != nil {
		c.confirmDiscard(heading, body, label, proceed)
		return
	}
	proceed()
}

func (c *draftController) doSaveDraftQuestion(done func(draftCloseAnswer)) {
	if c.saveDraftQuestion != nil {
		c.saveDraftQuestion(done)
		return
	}
	done(answerCancel)
}

// closeRequest keeps the pane open while there are unsaved edits and asks
// what to do with them (window owner only: the board owner's
// canCloseWithoutAsking is always true, so this never reaches a dialog for
// it). true blocks the close for now; the dialog's answer later calls
// form.closeForm(), which the host turns into an actual close (which
// re-enters closeRequest, now unblocked). false lets the close proceed at
// once (cleanup already ran). A comment has no Save Draft: the question is
// whether to discard it.
func (c *draftController) closeRequest() bool {
	if c.canCloseWithoutAsking() {
		c.cleanup()
		return false
	}
	if c.isComment() {
		c.doConfirmDiscard(i18n.T("Discard this message?"), "", i18n.T("_Discard"), func() {
			if c.draft.closed {
				return
			}
			if c.draft.draftID != "" {
				c.deleteDraft()
			}
			c.draft.discard = true
			if c.form != nil {
				c.form.closeForm()
			}
		})
		return true
	}
	c.doSaveDraftQuestion(func(answer draftCloseAnswer) {
		switch answer {
		case answerDiscard:
			c.draft.discard = true
			if !c.draft.explicitSave && c.draft.draftID != "" {
				c.deleteDraft()
			}
			if c.form != nil {
				c.form.closeForm()
			}
		case answerSave:
			c.save(saveExplicit, func(err error) {
				if err != nil || c.draft.closed {
					return // the toast is shown and the pane stays
				}
				c.draft.discard = true
				if c.form != nil {
					c.form.closeForm()
				}
			})
		}
	})
	return true
}

// cleanup runs when the pane really closes: late replies are dropped, the
// autosave is disarmed, the inline pictures forgotten. Idempotent. A
// window-owned comment's saved copy goes with the window unless it was
// sent: no Drafts folder keeps it (the autosave is only for a crash). The
// board never deletes here, even a late save after this answers.
func (c *draftController) cleanup() {
	if c.draft.closed {
		return
	}
	if deletesOnClose(&c.draft, c.owner, c.isComment()) {
		c.deleteDraft()
	}
	c.draft.closed = true
	c.cancelAutosave()
	if c.unregisterCID != nil && c.form != nil {
		for _, a := range c.form.attachments() {
			if a.Inline {
				c.unregisterCID(a.ContentID)
			}
		}
	}
	pending := c.pendingAfterSave
	c.pendingAfterSave = nil
	for _, f := range pending {
		f(errFormClosed)
	}
	c.sendAnswered()
}

// MARK: The board's draft

// editorReady: OwnerBoard, the editor reported ready for the loaded draft.
// One flush learns how the editor itself writes the draft (it normalises
// the HTML it was given), so settle can tell an edit the editor reported
// only with its flush from that normalisation; the flush's own "changed"
// is not an edit (the form drops it), so nothing is saved for it. Again
// after every reload of the editor.
func (c *draftController) editorReady() {
	if c.form == nil || c.draft.closed {
		return
	}
	c.editorRendered = false
	c.form.flushEditor(func() {
		c.editorRendered = true
	})
}

// refetchVersion: OwnerBoard after a conflict. The stored version from
// draft.get, then our text is saved over it (local wins in place). A
// draft gone meanwhile is lost; another failure retries with the autosave.
func (c *draftController) refetchVersion() {
	c.draft.dirty = true
	if c.refetching || c.form == nil || c.draft.draftID == "" {
		return
	}
	c.cancelAutosave()
	c.refetching = true
	accountID, draftID := c.form.account().ID, c.draft.draftID
	call, ctx := c.call, c.ctxFn()
	go func() {
		var res api.DraftGetResult
		err := call.Call(ctx, api.MethodDraftGet, api.DraftGetParams{AccountID: accountID, DraftID: draftID}, &res)
		c.loop.Post(func() {
			c.refetching = false
			waiters := c.refetchWaiters
			c.refetchWaiters = nil
			defer func() {
				for _, f := range waiters {
					f()
				}
			}()
			if c.draft.closed {
				return
			}
			if err != nil {
				var e *api.Error
				if errors.As(err, &e) && e.Code == api.CodeDraftNotFound {
					c.markLost()
					return
				}
				c.log.Debug("draft.get", "err", err)
			} else {
				c.draft.version = res.Draft.Version
			}
			c.markDirty()
		})
	}()
}

// markLost: OwnerBoard, the draft was deleted elsewhere. Nothing is saved
// again (that would make a draft no case links); the host hears it.
func (c *draftController) markLost() {
	if c.draft.lost || c.draft.closed {
		return
	}
	c.draft.lost = true
	c.abandon()
	if c.onLost != nil {
		c.onLost()
	}
}

// finish: OwnerBoard, the pane goes (another case selected, the board
// left, the app quits): what was typed is saved first (settle), then the
// controller cleans up. done(true) when nothing typed was lost; done(false)
// when a save failed (the controller stays usable, its autosave armed, so
// the host may keep the pane and call again) or the draft was lost. Never
// asks, never deletes. Waits for the editor and the daemon without a limit
// of its own: the caller bounds it.
func (c *draftController) finish(done func(ok bool)) {
	if c.draft.closed {
		done(!c.draft.dirty && !c.draft.lost)
		return
	}
	if c.form == nil {
		c.cleanup()
		done(!c.draft.dirty)
		return
	}
	c.settle(func(ok bool) {
		if ok && !c.draft.closed {
			c.cleanup()
		}
		done(ok)
	})
}

// settle: saves everything typed and leaves the controller open (the
// board's panes decide when it closes, boardreply.Panes). Waits for a
// send under way to answer; flushes the editor and saves while there are
// unsaved edits or a save is under way (a conflict refetch is awaited and
// the save retried). done(true) when nothing typed is unsaved (or the
// draft was sent); done(false) when a save failed (the autosave stays
// armed) or the draft was lost. No limit of its own.
func (c *draftController) settle(done func(ok bool)) {
	if c.draft.sending {
		c.sendWaiters = append(c.sendWaiters, func() { c.settleBegin(done) })
		return
	}
	c.settleBegin(done)
}

func (c *draftController) settleBegin(done func(ok bool)) {
	if c.draft.closed {
		done(!c.draft.dirty && !c.draft.lost)
		return
	}
	if c.form == nil {
		done(!c.draft.dirty)
		return
	}
	// What the editor reported last; the flush may report more. Its
	// "changed" is the flush's own and does not mark the draft dirty (the
	// form drops it), so the difference is looked at here: two reads of
	// the editor's own rendering, once editorReady learned it (before
	// that editorHTML is the HTML the editor was given, which it writes
	// back differently: never an edit).
	before := c.form.editorHTML()
	comparable := c.editorRendered
	c.form.flushEditor(func() {
		if c.draft.closed {
			done(!c.draft.dirty && !c.draft.lost)
			return
		}
		if comparable && c.form.editorHTML() != before {
			// An edit the editor reported only with the flush.
			c.draft.dirty = true
		} else if c.hasLastSavedBody {
			if wire := c.build(); wire != nil && bodyOf(*wire) != c.lastSavedBody {
				// Reported by an earlier flush whose save did not carry it.
				c.draft.dirty = true
			}
		}
		c.settleLoop(done, 0)
	})
}

func (c *draftController) settleLoop(done func(ok bool), i int) {
	if i >= 4 {
		done(false)
		return
	}
	if c.draft.closed {
		done(!c.draft.dirty && !c.draft.lost)
		return
	}
	if c.refetching {
		c.refetchWaiters = append(c.refetchWaiters, func() { c.settleLoop(done, i+1) })
		return
	}
	if !c.draft.dirty && !c.draft.saving {
		done(true)
		return
	}
	c.save(saveExplicit, func(err error) {
		if err != nil && !c.refetching {
			done(false)
			return
		}
		c.settleLoop(done, i+1)
	})
}

// sendAnswered: the send answered (or the pane went): settle goes on.
func (c *draftController) sendAnswered() {
	c.sendPending = false
	waiters := c.sendWaiters
	c.sendWaiters = nil
	for _, f := range waiters {
		f()
	}
}

// abandon forgets the form without saving or deleting anything: Discard
// was handled by the host, or the draft is gone. Idempotent.
func (c *draftController) abandon() {
	if c.draft.closed {
		return
	}
	c.draft.discard = true
	c.cleanup()
}

// MARK: Toasts over the save/send RPCs

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
