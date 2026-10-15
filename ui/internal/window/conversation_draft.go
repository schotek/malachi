// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/graphene"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// A draft card of the conversation view (conversation.DraftCard): the
// user's unsent reply to the conversation, on an issue the unsent comment,
// right under what opened the conversation (conversation.DraftsFirst). A
// native card, never a web view: the header has the Draft badge (and the
// Internal one of an internal comment), when it was saved, Edit and
// Discard; the body is the draft's plain text. A double click on the
// header is Edit. Edit opens the draft as draft.get has it now in a
// compose window (a comment window for a comment draft), or raises the
// window editing it; Discard asks and deletes it (draft.delete), or raises
// that window instead. The draft's avatar in the timeline is the user's.

// convDraftCard is one draft card.
type convDraftCard struct {
	cv   *conversationView
	card conversation.DraftCard

	root     *gtk.Box
	header   *gtk.Box
	buttons  *gtk.Box
	sender   *gtk.Label
	badge    *gtk.Label
	internal *gtk.Label
	saved    *gtk.Label
	text     *gtk.Label
}

// newConvDraftCard builds the card of a draft.
func newConvDraftCard(cv *conversationView, card conversation.DraftCard) *convDraftCard {
	d := &convDraftCard{cv: cv}
	d.root = gtk.NewBox(gtk.OrientationVertical, 0)
	d.root.AddCSSClass("conversation-card")
	d.root.AddCSSClass("conversation-draft")

	top := gtk.NewBox(gtk.OrientationVertical, 6)
	top.SetMarginStart(convCardPaddingH - 1)
	top.SetMarginEnd(convCardPaddingH - 1)
	top.SetMarginTop(convCardPaddingV - 1)
	top.SetMarginBottom(6)

	d.header = gtk.NewBox(gtk.OrientationHorizontal, 8)
	flow := adw.NewWrapBox()
	flow.SetChildSpacing(8)
	flow.SetLineSpacing(4)
	flow.SetHExpand(true)
	d.sender = plainLabel("", "conversation-sender")
	d.sender.SetEllipsize(pango.EllipsizeEnd)
	d.sender.SetWidthChars(4)
	d.sender.SetVAlign(gtk.AlignCenter)
	d.badge = widget.NewPill()
	d.badge.AddCSSClass("draft-pill")
	d.badge.SetText(conversation.DraftBadge(i18n.Tr))
	d.badge.SetVisible(true)
	d.internal = widget.NewPill()
	d.saved = plainLabel("", "caption", "dim-label")
	d.saved.SetEllipsize(pango.EllipsizeEnd)
	d.saved.SetVAlign(gtk.AlignCenter)
	flow.Append(d.sender)
	flow.Append(d.badge)
	flow.Append(d.internal)
	flow.Append(d.saved)

	buttons := gtk.NewBox(gtk.OrientationHorizontal, 4)
	buttons.SetVAlign(gtk.AlignStart)
	d.buttons = buttons
	edit := gtk.NewButtonWithLabel(conversation.DraftEditLabel(i18n.Tr))
	edit.AddCSSClass("flat")
	edit.ConnectClicked(func() { cv.w.editThreadDraft(d.card.Draft) })
	discard := gtk.NewButtonWithMnemonic(conversation.DraftDiscardLabel(i18n.Tr))
	discard.AddCSSClass("flat")
	discard.ConnectClicked(func() { cv.w.discardThreadDraft(d.card.Draft) })
	buttons.Append(edit)
	buttons.Append(discard)

	d.header.Append(flow)
	d.header.Append(buttons)
	top.Append(d.header)

	d.text = plainLabel("", "message-body")
	d.text.SetYAlign(0)
	d.text.SetWrap(true)
	d.text.SetWrapMode(pango.WrapWordChar)
	d.text.SetSelectable(true)
	d.text.SetMarginStart(convCardPaddingH - 1)
	d.text.SetMarginEnd(convCardPaddingH - 1)
	d.text.SetMarginTop(2)
	d.text.SetMarginBottom(convCardPaddingV - 1)

	d.root.Append(top)
	d.root.Append(d.text)

	click := gtk.NewGestureClick()
	click.SetButton(gdk.BUTTON_PRIMARY)
	click.ConnectReleased(func(n int, x, y float64) {
		if n == 2 && d.onHeader(x, y) {
			cv.w.editThreadDraft(d.card.Draft)
		}
	})
	d.root.AddController(click)

	d.update(card)
	return d
}

// widget is the card's root, for its row.
func (d *convDraftCard) widget() gtk.Widgetter { return d.root }

// update shows card (a newer version of the draft, or the same).
func (d *convDraftCard) update(card conversation.DraftCard) {
	d.card = card
	d.sender.SetText(card.Sender)
	widget.SetInternalPill(d.internal, card.InternalLabel)
	d.internal.SetVisible(card.Internal)
	if card.Draft.UpdatedAt.IsZero() {
		d.saved.SetVisible(false)
	} else {
		d.saved.SetText(conversation.DraftSaved(widget.FormatTime(card.Draft.UpdatedAt), i18n.Tr))
		d.saved.SetVisible(true)
	}
	d.text.SetText(card.Text)
	d.text.SetVisible(card.Text != "")
}

// onHeader reports whether the point, in the card's coordinates, is on the
// header: from the card's top edge to the bottom of the header's line, off
// its buttons (whose clicks are their own), as convCard.onHeader.
func (d *convDraftCard) onHeader(x, y float64) bool {
	line, ok := d.header.ComputeBounds(d.root)
	if !ok || y < 0 || y >= float64(line.Y()+line.Height()) {
		return false
	}
	p := graphene.NewPointAlloc()
	p.Init(float32(x), float32(y))
	if r, ok := d.buttons.ComputeBounds(d.root); ok && r.ContainsPoint(p) {
		return false
	}
	return true
}

// editThreadDraft is a draft card's Edit: the window that edits the draft
// already comes to the front; otherwise the draft as draft.get has it now
// (the card may show an older version) opens in a compose window, a
// comment window for a comment draft. A draft that is gone says so.
func (w *Window) editThreadDraft(d api.Draft) {
	if cw := w.compose.FindDraft(d); cw != nil {
		cw.Present()
		return
	}
	if d.ID == "" || w.findingDrafts[d.ID] {
		return
	}
	w.findingDrafts[d.ID] = true
	params := api.DraftGetParams{AccountID: d.AccountID, DraftID: d.ID}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.DraftGetResult
		err := w.client.Call(ctx, api.MethodDraftGet, params, &res)
		glib.IdleAdd(func() {
			delete(w.findingDrafts, d.ID)
			if err != nil {
				w.log.Warn("draft.get", "err", err)
				var e *api.Error
				if errors.As(err, &e) && e.Code == api.CodeDraftNotFound {
					w.Toast(conversation.DraftGone(i18n.Tr))
					return
				}
				w.Toast(widget.RPCErrorText(i18n.T("Opening the draft"), err))
				return
			}
			if cw := w.compose.FindDraft(res.Draft); cw != nil {
				cw.Present()
				return
			}
			w.compose.Open(compose.FromDraft(compose.KindEdit, res.Draft, api.BlockedContent{}))
		})
	}()
}

// discardThreadDraft is a draft card's Discard: a draft a compose window
// edits is that window's to discard (it comes to the front); otherwise,
// after the question, draft.delete. notify.draftsChanged takes the card
// away.
func (w *Window) discardThreadDraft(d api.Draft) {
	if cw := w.compose.FindDraft(d); cw != nil {
		cw.Present()
		return
	}
	dlg := adw.NewAlertDialog(conversation.DiscardQuestion(i18n.Tr), "")
	dlg.SetHeadingUseMarkup(false)
	dlg.AddResponse("cancel", i18n.T("_Cancel"))
	dlg.AddResponse("discard", conversation.DraftDiscardLabel(i18n.Tr))
	dlg.SetResponseAppearance("discard", adw.ResponseDestructive)
	dlg.SetDefaultResponse("cancel")
	dlg.SetCloseResponse("cancel")
	dlg.ConnectResponse(func(response string) {
		if response != "discard" {
			return
		}
		params := api.DraftDeleteParams{AccountID: d.AccountID, DraftID: d.ID}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
			defer cancel()
			err := w.client.Call(ctx, api.MethodDraftDelete, params, nil)
			glib.IdleAdd(func() {
				if err != nil {
					w.log.Warn("draft.delete", "err", err)
					w.Toast(widget.RPCErrorText(i18n.T("Discarding the draft"), err))
				}
			})
		}()
	})
	dlg.Present(&w.ApplicationWindow.Window)
}

// onDraftsChanged takes notify.draftsChanged: the listed conversations it
// names, and the one on show, are asked thread.get again
// (refetchMembers), which brings their DraftCount and drafts; without
// names, the one on show and every listed one that has drafts. Nothing in
// flat mode (no row shows a conversation).
func (w *Window) onDraftsChanged(n api.DraftsChangedNotification) {
	m := &w.model
	if !m.grouped || m.loading || m.listFolder.Account != n.AccountID {
		return
	}
	shown := api.ThreadID("")
	if row, ok := w.selectedRow(); ok && rowShowsConversation(row) {
		shown = row.Key.Thread
	} else if ok && row.Thread {
		shown = row.Key.Thread
	}
	want := map[api.ThreadID]bool{}
	if len(n.ThreadIDs) == 0 {
		if shown != "" {
			want[shown] = true
		}
		for _, t := range m.threads {
			if t.DraftCount > 0 {
				want[t.ID] = true
			}
		}
	}
	for _, tid := range n.ThreadIDs {
		want[tid] = true
	}
	for tid := range want {
		if _, listed := m.tindex[tid]; listed {
			w.refetchMembers(tid)
		}
	}
}
