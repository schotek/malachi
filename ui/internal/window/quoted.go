// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The quoted history under a body (ui/internal/conversation/quoted.go):
// the daemon cuts it off when asked (message.body with trimQuoted, which
// every view asks by default), and a small button under the body ("•••")
// shows it, then hides it again. The two variants of a body share one
// cache entry (loadedMessage.showQuoted), so switching back asks nothing.
// What the user revealed holds while the view shows the same thing: the
// pane forgets it with another message (paneQuoted), a message window
// keeps it while open, the conversation view forgets it with another
// conversation (conversationController.quoted). The pane and a window
// showing the same message share the entry, so either one's switch shows
// in both. An attached message (message.embedded) has no button: its body
// comes whole. A port of the macOS client's QuotedTextButton.swift and
// the quoted halves of MessageViewController.swift.

// quotedDots is the button's face: three bullets (U+2022), the same in
// every language; the tooltip and the accessible name say what it does.
var quotedDots = strings.Repeat(string(rune(0x2022)), 3)

// newQuotedButton is the "•••" button of a conversation card (the pane
// and the message window have theirs in their builder).
func newQuotedButton() *gtk.Button {
	b := gtk.NewButtonWithLabel(quotedDots)
	b.AddCSSClass("quoted-text")
	b.SetHAlign(gtk.AlignStart)
	return b
}

// setQuotedLabel gives the button what offer o says, as its tooltip and
// accessible name.
func setQuotedLabel(b *gtk.Button, o conversation.QuotedOffer) {
	label := o.Label(i18n.Tr)
	b.SetTooltipText(label)
	b.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
		[]coreglib.Value{*coreglib.NewValue(label)})
}

// bindQuotedButton takes the view's "•••" row from the builder, when it
// has one (window.blp and message_window.blp; embedded_window.blp has
// none): its face, and its click, which the owner's onQuoted does.
func (v *messageView) bindQuotedButton(b *gtk.Builder) {
	row := b.GetObject("quoted_row")
	if row == nil {
		return
	}
	v.quotedRow = row.Cast().(gtk.Widgetter)
	v.quotedButton = b.GetObject("quoted_button").Cast().(*gtk.Button)
	v.quotedButton.SetLabel(quotedDots)
	// As the bars' buttons: the button may go away under the click, and
	// the focus would land on a selectable header label (newMessageView).
	v.quotedButton.SetFocusOnClick(false)
	v.quotedButton.ConnectClicked(func() {
		if v.onQuoted != nil && v.quotedNow != conversation.QuotedNone {
			v.onQuoted(v.quotedNow == conversation.QuotedShow)
		}
	})
}

// showQuotedButton puts the "•••" row in offer o: hidden for none.
func (v *messageView) showQuotedButton(o conversation.QuotedOffer) {
	v.quotedNow = o
	if v.quotedRow == nil {
		return
	}
	row := gtk.BaseWidget(v.quotedRow)
	if o == conversation.QuotedNone {
		if row.FocusChild() != nil {
			v.stack.GrabFocus()
		}
		row.SetVisible(false)
		return
	}
	setQuotedLabel(v.quotedButton, o)
	row.SetVisible(true)
}

// setPaneQuoted is the pane's "•••": the quoted history of the message on
// display shows (on) or goes again, the variant held or fetched; the
// choice holds until the pane shows another message.
func (w *Window) setPaneQuoted(on bool) {
	s := w.pane.shown
	if s.ID == "" || w.conversationShown() {
		return
	}
	w.paneQuoted.Set(s.ID, on)
	gen := w.model.bodyGen
	labels := w.pane
	w.fetchMessageQuoted(s.AccountID, s.ID, on, func(lm *loadedMessage) {
		if gen != w.model.bodyGen {
			return // the pane moved on
		}
		labels.render(s, lm)
	})
	if lm := w.loaded[s.ID]; lm != nil && lm.body == nil {
		labels.render(s, lm) // the wait, with the way back
	}
}

// setWindowQuoted is the "•••" of message window mw, as setPaneQuoted;
// the choice holds while the window is open.
func (w *Window) setWindowQuoted(mw *MessageWindow, on bool) {
	s := mw.view.shown
	if s.ID == "" {
		return
	}
	mw.quoted.Set(s.ID, on)
	w.fetchMessageQuoted(s.AccountID, s.ID, on, func(lm *loadedMessage) {
		if !mw.closed {
			mw.show(s, lm)
		}
	})
	if lm := w.loaded[s.ID]; lm != nil && lm.body == nil {
		mw.show(s, lm)
	}
}
