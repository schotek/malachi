// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/crm"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The eWay-CRM controls of one window (docs/eway.md §2.6; the macOS
// CRMController and CRMToolbarItem): the main window acts on the
// selection, a message window on its message. The header bar's group of
// its own between Archive/Trash and the Star (crm_slot in window.blp and
// message_window.blp) holds crm_button, "Save to eWay-CRM" with the CRM
// icon (a spinner instead and off while a save or removal runs, tooltip
// "Saving to eWay-CRM…"), or crm_menu_button, "In eWay-CRM" with a check
// mark, whose menu is crm.SavedMenu: two widgets, one visible. Both are
// icon buttons with the label as tooltip, as every action of the header
// bar. Below the narrow breakpoint the slot hides (a setter of the
// window's Adw.Breakpoint) and the same items stay in More Actions, which
// has them always. The actions are prefix.crm-save, crm-open,
// crm-edit-links and crm-remove (win.* or msg.*), enabled by
// crm.OfferFor; the list's context menu (context_menus.go) uses the win.*
// ones.

// The names of the actions, without the prefix.
const (
	crmActionSave      = "crm-save"
	crmActionOpen      = "crm-open"
	crmActionEditLinks = "crm-edit-links"
	crmActionRemove    = "crm-remove"
)

// crmMenuActions are the actions of crm.SavedMenu's items.
var crmMenuActions = map[crm.MenuID]string{
	crm.MenuOpen:      crmActionOpen,
	crm.MenuEditLinks: crmActionEditLinks,
	crm.MenuRemove:    crmActionRemove,
}

// crmControls are the CRM button, actions and More Actions items of one
// window.
type crmControls struct {
	w      *Window
	prefix string
	// parent hosts the dialog and the question; toast shows a message in
	// the window.
	parent *gtk.Window
	toast  func(string)
	// subjects are the messages the window acts on now.
	subjects func() []crmSubject

	save        *gtk.Button
	saveIcon    *gtk.Image
	saveSpinner *adw.Spinner
	saved       *gtk.MenuButton
	// section is the CRM section of More Actions, rebuilt when what it
	// offers changes (sectionKey).
	section    *gio.Menu
	sectionKey string
	actions    map[string]*gio.SimpleAction
}

// newCrmControls binds the CRM widgets of builder b (window.blp or
// message_window.blp), adds the actions with add and the section to the
// window's More Actions menu (message_menu_model).
func newCrmControls(w *Window, prefix string, parent *gtk.Window, b *gtk.Builder, add func(*gio.SimpleAction),
	subjects func() []crmSubject, toast func(string)) *crmControls {
	c := &crmControls{
		w:           w,
		prefix:      prefix,
		parent:      parent,
		toast:       toast,
		subjects:    subjects,
		save:        b.GetObject("crm_button").Cast().(*gtk.Button),
		saveIcon:    b.GetObject("crm_button_icon").Cast().(*gtk.Image),
		saveSpinner: b.GetObject("crm_button_spinner").Cast().(*adw.Spinner),
		saved:       b.GetObject("crm_menu_button").Cast().(*gtk.MenuButton),
		section:     gio.NewMenu(),
		actions:     map[string]*gio.SimpleAction{},
	}
	c.saveIcon.SetFromIconName(crm.Icon)
	b.GetObject("crm_menu_icon").Cast().(*gtk.Image).SetFromIconName(crm.Icon)
	menu := gio.NewMenu()
	for _, it := range crm.SavedMenu(i18n.Tr) {
		menu.Append(it.Label, prefix+"."+crmMenuActions[it.ID])
	}
	c.saved.SetMenuModel(menu)
	c.save.SetActionName(prefix + "." + crmActionSave)
	for name, fn := range map[string]func(){
		crmActionSave:      c.saveMessages,
		crmActionOpen:      c.open,
		crmActionEditLinks: c.saveMessages, // the dialog edits once crm.lookup returns the record
		crmActionRemove:    c.remove,
	} {
		a := gio.NewSimpleAction(name, nil)
		a.SetEnabled(false)
		fn := fn
		a.ConnectActivate(func(*glib.Variant) { fn() })
		add(a)
		c.actions[name] = a
	}
	if m, ok := b.GetObject("message_menu_model").Cast().(*gio.Menu); ok {
		m.AppendSection("", c.section)
	}
	return c
}

// emit shows text in the window the action came from, or in the main
// window once that one is gone.
func (c *crmControls) emit(text string) {
	if c.parent != nil && !c.parent.IsVisible() {
		c.w.Toast(text)
		return
	}
	c.toast(text)
}

// refresh shows what the subjects offer now: the button's state, the
// actions and More Actions' items.
func (c *crmControls) refresh() {
	w := c.w
	subjects := c.subjects()
	b := w.crmButton(subjects)
	o := w.crmOffer(subjects)
	busy := w.crmBusy(subjects)

	shown := b.State != crm.ButtonHidden
	c.save.SetVisible(shown && !b.Menu)
	c.saved.SetVisible(shown && b.Menu)
	c.save.SetTooltipText(b.Tooltip)
	c.saved.SetTooltipText(b.Tooltip)
	c.saveIcon.SetVisible(b.State != crm.ButtonSaving)
	c.saveSpinner.SetVisible(b.State == crm.ButtonSaving)

	var rec *api.CrmRecord
	if o.Saved && len(subjects) == 1 {
		rec = w.crmRecord(subjects[0].s, nil)
	}
	enabled := map[string]bool{
		crmActionSave:      o.Shown && o.Enabled && !o.Saved && !busy,
		crmActionOpen:      o.Saved && rec != nil && crm.OpenURL(rec.URL) != "",
		crmActionEditLinks: o.Saved && !busy,
		crmActionRemove:    o.Saved && !busy,
	}
	for name, on := range enabled {
		c.actions[name].SetEnabled(on)
	}
	c.fillSection(o)
}

// fillSection puts the items of offer o into More Actions: Save (with the
// count of several rows), or the saved message's menu.
func (c *crmControls) fillSection(o crm.Offer) {
	key := ""
	switch {
	case !o.Shown:
	case o.Saved:
		key = "saved"
	default:
		key = crm.SaveMenuLabel(o.Count, i18n.Tr)
	}
	if key == c.sectionKey {
		return
	}
	c.sectionKey = key
	c.section.RemoveAll()
	switch {
	case !o.Shown:
	case o.Saved:
		for _, it := range crm.SavedMenu(i18n.Tr) {
			c.section.Append(it.Label, c.prefix+"."+crmMenuActions[it.ID])
		}
	default:
		c.section.Append(key, c.prefix+"."+crmActionSave)
	}
}

// saveMessages is Save to eWay-CRM (and Edit Links… of a saved message):
// the dialog over crm.lookup of the subjects, when they can be saved and
// none is busy.
func (c *crmControls) saveMessages() {
	w := c.w
	subjects := c.subjects()
	o := w.crmOffer(subjects)
	acc, ok := w.crmAccount()
	if !ok || !o.Shown || len(subjects) == 0 || w.crmBusy(subjects) || (!o.Saved && !o.Enabled) {
		return
	}
	msgAccount := subjects[0].s.AccountID
	ids := make([]api.MessageID, len(subjects))
	for i, x := range subjects {
		ids[i] = x.s.ID
	}
	d := newCrmDialog(w, c, msgAccount, ids, crmSummary(subjects))
	d.present(c.parent)
	params := api.CrmLookupParams{AccountID: acc.ID, MessageAccountID: msgAccount, MessageIDs: ids}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), crmLookupTimeout)
		defer cancel()
		var res api.CrmLookupResult
		err := w.client.Call(ctx, api.MethodCrmLookup, params, &res)
		glib.IdleAdd(func() { d.loaded(res, err) })
	}()
}

// open is Open in eWay-CRM: the saved item's URL, which the daemon built
// from the account's service address and the item's GUID (docs/eway.md
// D15), never anything from mail or from the CRM; so it opens in the
// browser at once, without the "Open this link?" question of the mail's
// links (crm.OpenURL opens https only).
func (c *crmControls) open() {
	subjects := c.subjects()
	if len(subjects) != 1 {
		return
	}
	rec := c.w.crmRecord(subjects[0].s, nil)
	if rec == nil {
		return
	}
	if u := crm.OpenURL(rec.URL); u != "" {
		c.w.launchURI(c.parent, u)
	}
}

// remove is Remove from eWay-CRM: the question (crm.RemoveQuestion: the
// item goes for every user of the CRM), then crm.remove of the one saved
// subject.
func (c *crmControls) remove() {
	w := c.w
	subjects := c.subjects()
	if len(subjects) != 1 {
		return
	}
	s := subjects[0].s
	acc, ok := w.crmAccount()
	if !ok || !w.crmIsSaved(s) || w.crmState.inFlight.Has(s.AccountID, s.ID) {
		return
	}
	q := crm.RemoveQuestion(s.Subject, i18n.Tr)
	crmID := acc.ID
	widget.ConfirmDestructive(c.parent, q.Heading, q.Body, q.Confirm, func() {
		c.runRemove(crmID, s)
	})
}

// runRemove calls crm.remove for s; the message is busy meanwhile.
func (c *crmControls) runRemove(crmID api.AccountID, s api.MessageSummary) {
	w := c.w
	if w.crmState.inFlight.Has(s.AccountID, s.ID) {
		return
	}
	ids := []api.MessageID{s.ID}
	w.crmBegin(s.AccountID, ids)
	params := api.CrmRemoveParams{AccountID: crmID, MessageAccountID: s.AccountID, MessageID: s.ID}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), crmRemoveTimeout)
		defer cancel()
		err := w.client.Call(ctx, api.MethodCrmRemove, params, &api.CrmRemoveResult{})
		glib.IdleAdd(func() {
			if err != nil {
				w.log.Info("crm.remove failed", "code", crmErrorCode(err))
				var e *api.Error
				if errors.As(err, &e) && e.Code == api.CodeCrmItemNotFound {
					// The daemon dropped its record too.
					w.crmState.setOverride(s.AccountID, s.ID, nil)
				}
				c.emit(crmErrorText(crm.ActionRemove, err))
			} else {
				w.crmState.setOverride(s.AccountID, s.ID, nil)
				c.emit(crm.RemovedToast(i18n.Tr))
			}
			w.crmEnd(s.AccountID, ids)
		})
	}()
}

// runSave is crm.save of a dialog's choice, in the background: the
// messages are busy meanwhile in every window; a partNotDownloaded
// downloads the messages it names (the status as a toast) and asks again,
// at most twice (docs/eway.md D6).
func (c *crmControls) runSave(params api.CrmSaveParams) {
	w := c.w
	acc, ids := params.MessageAccountID, params.MessageIDs
	w.crmBegin(acc, ids)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), crmSaveTimeout)
		defer cancel()
		var res api.CrmSaveResult
		var err error
		action := crm.ActionSave
		for attempt := 0; ; attempt++ {
			res = api.CrmSaveResult{}
			err = w.client.Call(ctx, api.MethodCrmSave, params, &res)
			missing, ok := crm.NotDownloaded(err)
			if err == nil || !ok || attempt >= 2 {
				break
			}
			n := len(missing)
			glib.IdleAdd(func() { c.emit(crm.DownloadingText(n, i18n.Tr)) })
			for _, id := range missing {
				if _, derr := w.download(acc, id); derr != nil {
					err, action = derr, crm.ActionDownload
					break
				}
			}
			if action == crm.ActionDownload {
				break
			}
		}
		glib.IdleAdd(func() {
			if err != nil {
				w.log.Info("crm.save failed", "code", crmErrorCode(err))
				c.emit(crmErrorText(action, err))
			} else {
				for _, o := range res.Results {
					if o.Error == nil {
						w.crmState.setOverride(acc, o.MessageID, o.Record)
					}
				}
				text, e := crm.SaveToast(crm.Summarize(res), i18n.Tr)
				if text == "" && e != nil {
					text = widget.RPCErrorText(crm.ActionText(crm.ActionSave, i18n.Tr), e)
				}
				c.emit(text)
			}
			w.crmEnd(acc, ids)
		})
	}()
}

// crmErrorText is the sentence for a failed CRM call: the CRM's own
// (crm.ErrorText), else the general one with the action.
func crmErrorText(a crm.Action, err error) string {
	if t := crm.ErrorText(err, i18n.Tr); t != "" {
		return t
	}
	return widget.RPCErrorText(crm.ActionText(a, i18n.Tr), err)
}

// crmErrorCode is the code of a daemon error for the log (nothing about
// the message or the CRM's items is logged); 0 for another error.
func crmErrorCode(err error) int {
	var e *api.Error
	if errors.As(err, &e) {
		return int(e.Code)
	}
	return 0
}

// crmSummary is the dialog's one line: the first message's subject and
// sender, and the number of messages when there are several. Plain text.
func crmSummary(subjects []crmSubject) string {
	if len(subjects) == 0 {
		return ""
	}
	first := subjects[0].s
	parts := []string{jira.Clean(subjectText(first.Subject))}
	if len(first.From) > 0 {
		if sender := jira.Clean(widget.DisplayName(first.From[0])); sender != "" {
			parts = append(parts, sender)
		}
	}
	if n := len(subjects); n > 1 {
		parts = append(parts, fmt.Sprintf(i18n.N("%d message", "%d messages", n), n))
	}
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += " · "
		}
		out += p
	}
	return out
}

// crmRefetch asks message.get again for message id of account acc when the
// cache has no full message for it and none is on its way: the CRM record
// of a message window or the pane after the cache let go of it
// (notify.messagesChanged). The body is left alone.
func (w *Window) crmRefetch(acc api.AccountID, id api.MessageID) {
	lm := w.loadedFor(id)
	if lm.msg != nil || lm.getting {
		return
	}
	if lm.account == "" {
		lm.account = acc
	}
	lm.getting = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.MessageGetResult
		err := w.client.Call(ctx, api.MethodMessageGet, api.MessageGetParams{AccountID: acc, MessageID: id}, &res)
		glib.IdleAdd(func() {
			lm.getting = false
			if err != nil {
				w.log.Warn("message.get", "err", err)
			} else {
				lm.msg = &res.Message
			}
			w.settleLoaded(id, lm)
		})
	}()
}

// crmMainSubjects are the main window's subjects: the shown message of
// each selected row, a conversation's row marked as such.
func (w *Window) crmMainSubjects() []crmSubject {
	rows := w.selectedRows()
	out := make([]crmSubject, len(rows))
	for i, r := range rows {
		out[i] = crmSubject{s: r.Message, conv: r.Thread}
	}
	return out
}
