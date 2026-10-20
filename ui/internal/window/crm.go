// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/crm"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// Saving mail to eWay-CRM (docs/eway.md, ui/internal/crm; the macOS
// client's CRMController.swift is the port this follows). The daemon does
// every CRM call; the window only asks and shows. What the application
// knows of eWay-CRM lives here once, on the main window, and every message
// window shares it: the account used (the first enabled eway account of
// account.list, crm.Available), the messages a save or removal runs for
// (crm.InFlight: their buttons read Saving in every window), and for a few
// seconds the record a finished call reported, so that the button and the
// bar turn before the daemon's notify.messagesChanged has refreshed the
// list and the message (the daemon's record is the truth; this only
// bridges the gap). The per-window controls are crm_actions.go, the dialog
// crm_dialog.go, the bar crm_bar.go.

// crmOverrideFor is how long a call's own answer outranks the list and the
// message cache.
const crmOverrideFor = 10 * time.Second

// Client-side budgets of the CRM calls (docs/eway.md "As built (T6)"):
// crm.save uploads up to 20 messages of up to 2 minutes each and the
// daemon has no budget for the whole call.
const (
	crmLookupTimeout = 30 * time.Second
	crmSearchTimeout = 30 * time.Second
	crmSaveTimeout   = 15 * time.Minute
	crmRemoveTimeout = 30 * time.Second
)

// crmOverride is what a finished call reported for a message: its record,
// nil when it is not saved (any more).
type crmOverride struct {
	rec   *api.CrmRecord
	until time.Time
}

// crmState is the application's eWay-CRM state (one per main window, which
// owns the message windows). Main loop only.
type crmState struct {
	inFlight  crm.InFlight
	overrides map[crm.Key]crmOverride
	now       func() time.Time
}

func newCrmState() *crmState {
	return &crmState{overrides: map[crm.Key]crmOverride{}, now: time.Now}
}

// override is the call's own answer for the message while it is fresh.
func (st *crmState) override(acc api.AccountID, id api.MessageID) (crmOverride, bool) {
	if st == nil {
		return crmOverride{}, false // a test's bare window
	}
	o, ok := st.overrides[crm.Key{Account: acc, Message: id}]
	if !ok || !o.until.After(st.now()) {
		return crmOverride{}, false
	}
	return o, true
}

// setOverride remembers what a call reported for the message (nil: not
// saved) and forgets the stale ones.
func (st *crmState) setOverride(acc api.AccountID, id api.MessageID, rec *api.CrmRecord) {
	now := st.now()
	st.overrides[crm.Key{Account: acc, Message: id}] = crmOverride{rec: rec, until: now.Add(crmOverrideFor)}
	for k, o := range st.overrides {
		if !o.until.After(now) {
			delete(st.overrides, k)
		}
	}
}

// crmSubject is a message the CRM controls act on: the shown message of a
// selected row (conv: the row stands for a conversation, which offers
// nothing, crm.Row.Conversation), or a message window's message.
type crmSubject struct {
	s    api.MessageSummary
	conv bool
}

// crmAccount is the eWay-CRM account the client saves to.
func (w *Window) crmAccount() (*api.Account, bool) { return crm.Available(w.model.accounts) }

// crmAccountID is crmAccount's id, "" without one.
func (w *Window) crmAccountID() api.AccountID {
	if a, ok := w.crmAccount(); ok {
		return a.ID
	}
	return ""
}

// crmMessage is the full message (message.get) of id as the window knows
// it: the cache's, else fallback's (a view's entry the cache let go of).
func (w *Window) crmMessage(id api.MessageID, fallback *loadedMessage) *api.Message {
	if lm := w.loaded[id]; lm != nil && lm.msg != nil {
		return lm.msg
	}
	if fallback != nil && fallback.msg != nil && fallback.msg.ID == id {
		return fallback.msg
	}
	return nil
}

// crmRecord is the record of message s in the account used: a call's own
// answer for a few seconds, else m's (the loaded message; nil: none known).
func (w *Window) crmRecord(s api.MessageSummary, m *api.Message) *api.CrmRecord {
	if o, ok := w.crmState.override(s.AccountID, s.ID); ok {
		return o.rec
	}
	if m == nil {
		m = w.crmMessage(s.ID, nil)
	}
	if m == nil || m.ID != s.ID {
		return nil
	}
	return crm.RecordOf(m, w.crmAccountID())
}

// crmIsSaved is crm.IsSaved with a call's own answer first.
func (w *Window) crmIsSaved(s api.MessageSummary) bool {
	if o, ok := w.crmState.override(s.AccountID, s.ID); ok {
		return o.rec != nil
	}
	return crm.IsSaved(s, w.crmMessage(s.ID, nil), w.crmAccountID())
}

// crmRow is the rule's row of subject x; false when its account is not
// known.
func (w *Window) crmRow(x crmSubject) (crm.Row, bool) {
	a, ok := w.model.account(x.s.AccountID)
	if !ok {
		return crm.Row{}, false
	}
	role := w.model.folderRole(folderKey{Account: x.s.AccountID, Folder: x.s.FolderID})
	return crm.Row{Account: a, Role: role, Saved: w.crmIsSaved(x.s), Conversation: x.conv}, true
}

// crmOffer is crm.OfferFor of the subjects; nothing when one of them has
// an unknown account.
func (w *Window) crmOffer(subjects []crmSubject) crm.Offer {
	rows := make([]crm.Row, 0, len(subjects))
	for _, x := range subjects {
		r, ok := w.crmRow(x)
		if !ok {
			return crm.Offer{}
		}
		rows = append(rows, r)
	}
	return crm.OfferFor(w.model.accounts, rows)
}

// crmBusy reports a save or removal running for any of the subjects.
func (w *Window) crmBusy(subjects []crmSubject) bool {
	if w.crmState == nil {
		return false
	}
	for _, x := range subjects {
		if w.crmState.inFlight.Has(x.s.AccountID, x.s.ID) {
			return true
		}
	}
	return false
}

// crmButton is the button of the subjects: one message by
// crm.ButtonStateFor; for several the Save button with their count ("Save
// 3 Messages to eWay-CRM…", as the context menu has it), Saving while any
// of them is busy (CRMState.button of the macOS client).
func (w *Window) crmButton(subjects []crmSubject) crm.Button {
	busy := w.crmBusy(subjects)
	if len(subjects) == 1 {
		r, ok := w.crmRow(subjects[0])
		if !ok {
			return crm.ButtonFor(crm.ButtonHidden, i18n.Tr)
		}
		return crm.ButtonFor(crm.ButtonStateFor(w.model.accounts, r, busy), i18n.Tr)
	}
	o := w.crmOffer(subjects)
	if !o.Shown {
		return crm.ButtonFor(crm.ButtonHidden, i18n.Tr)
	}
	state := crm.ButtonNotSaved
	if busy {
		state = crm.ButtonSaving
	}
	b := crm.ButtonFor(state, i18n.Tr)
	b.Label = crm.SaveMenuLabel(o.Count, i18n.Tr)
	if !busy {
		b.Tooltip = b.Label
	}
	b.Enabled = o.Enabled && !busy
	return b
}

// crmBegin and crmEnd mark the messages a call runs for; every CRM control
// follows.
func (w *Window) crmBegin(acc api.AccountID, ids []api.MessageID) {
	w.crmState.inFlight.Begin(acc, ids)
	w.refreshCRM()
}

func (w *Window) crmEnd(acc api.AccountID, ids []api.MessageID) {
	w.crmState.inFlight.End(acc, ids)
	w.refreshCRM()
}

// refreshCRM brings every eWay-CRM control up to date: the main window's
// button, actions and menu items, every message window's, and the bars of
// the pane, the message windows and the conversation's cards. Called on
// every change that may move them (the selection, a message loaded, the
// accounts, notify.messagesChanged, a call starting or ending): a hidden
// widget is never re-validated by itself (docs/eway.md "Gate B findings").
func (w *Window) refreshCRM() {
	if w.crmState == nil {
		return // still being built
	}
	if w.crmMain != nil {
		w.crmMain.refresh()
	}
	if w.pane != nil {
		if w.pane.shown.ID != "" {
			w.pane.renderCrm(w.pane.shown, w.crmMessage(w.pane.shown.ID, w.pane.shownLoaded))
		} else {
			w.pane.showCrmBar(nil)
		}
	}
	for _, mw := range w.openMessages {
		if mw.closed {
			continue
		}
		if mw.crm != nil {
			mw.crm.refresh()
		}
		mw.view.renderCrm(mw.s, w.crmMessage(mw.id, mw.view.shownLoaded))
	}
	w.conversationRefreshCrm()
}

// crmMessagesChanged follows notify.messagesChanged of account acc for
// eWay-CRM: the message cache let go of the account's messages, so the
// message of every open message window of it (and the pane's, when the
// pane does not fetch it again itself) is asked for again (message.get
// only, the body stays as shown), and every control is redrawn.
func (w *Window) crmMessagesChanged(acc api.AccountID) {
	if w.crmState == nil {
		return // still being built (or a test's bare window)
	}
	if _, ok := w.crmAccount(); ok {
		for id, mw := range w.openMessages {
			if !mw.closed && mw.s.AccountID == acc {
				w.crmRefetch(acc, id)
			}
		}
		if w.pane != nil && w.pane.shown.ID != "" && w.pane.shown.AccountID == acc {
			w.crmRefetch(acc, w.pane.shown.ID)
		}
	}
	w.refreshCRM()
}
