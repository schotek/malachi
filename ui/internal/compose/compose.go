// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Window is one compose window, built from data/ui/compose.blp: the
// chrome (header bar with Attach, Send, the draft menu and the
// assistant's rewrite button; the close question; Escape) around a
// LayoutWindow/OwnerWindow compose.Pane (pane.go), which holds the header
// fields, the formatting bar, the editor, the attachment chips, the
// status line and the draft logic. The window forwards Send, Attach,
// close, Escape, the rewrite and the Format actions to the pane (through
// its own action group and the pane's ShortcutController in compose.blp);
// it is otherwise a thin shell so the same pane can be embedded inline by
// the board (LayoutInline/OwnerBoard, boardreply.Pane) without it.
type Window struct {
	*adw.Window

	m      *Manager
	params Params
	pane   *Pane

	title        *adw.WindowTitle
	toasts       *adw.ToastOverlay
	paneSlot     *gtk.Box
	attachButton *gtk.Button

	rewrite *rewriteUI // the assistant's rewrite (rewrite.go)
}

// newWindow builds and prefills a window; Manager.Open presents it.
func newWindow(m *Manager, p Params) *Window {
	b := data.Builder("compose.ui")
	w := &Window{
		Window:       b.GetObject("compose_window").Cast().(*adw.Window),
		m:            m,
		params:       p,
		title:        b.GetObject("window_title").Cast().(*adw.WindowTitle),
		toasts:       b.GetObject("toast_overlay").Cast().(*adw.ToastOverlay),
		paneSlot:     b.GetObject("pane_slot").Cast().(*gtk.Box),
		attachButton: b.GetObject("attach_button").Cast().(*gtk.Button),
	}

	w.pane = NewPane(m, p, PaneOptions{Layout: LayoutWindow, Owner: OwnerWindow})
	w.pane.dialogParent = &w.Window.Window
	w.pane.OnTitle = func(title string) { w.title.SetTitle(title) }
	w.pane.OnToast = func(text string) { w.toasts.AddToast(widget.PlainToast(text)) }
	w.pane.OnEnd = func(End) { w.Close() }
	// The pane already computed its title in NewPane, before OnTitle
	// existed to carry it here.
	w.title.SetTitle(w.pane.ReplyTitle())
	if p.Comment != nil {
		// A comment names its issue in the window's own title too (not
		// just the header bar's WindowTitle widget), as applyCommentMode
		// always did; it never changes afterwards.
		w.SetTitle(w.pane.ReplyTitle())
		// Nothing attaches in comment mode: the window's own Attach
		// button (comment.go's applyCommentMode does the same for the
		// pane's internal attach controls).
		w.attachButton.SetVisible(false)
	}
	w.paneSlot.Append(w.pane.Widget())
	// The pane's own action group is also on the window itself: the
	// header bar's Send, Attach and menu button are outside the pane's
	// widget tree (compose.blp), so action-name there resolves through
	// the window, not through the pane (which the format toolbar reaches
	// through the pane's own insertion, pane.go ActionGroup).
	w.InsertActionGroup("compose", w.pane.ActionGroup())

	// The To entry (or the editor, in comment mode) takes the focus when
	// the window opens.
	w.SetFocus(w.pane.InitialFocus())
	w.wireRewrite(b)
	w.ConnectCloseRequest(w.closeRequest)
	return w
}

// closeRequest is the GTK "close-request" signal: delegates to the
// pane's draftController.closeRequest (draft.go), then, once it is safe
// to actually close, finishes the window's own teardown.
func (w *Window) closeRequest() bool {
	block := w.pane.dc.closeRequest()
	if !block {
		w.rewrite.close()
		w.pane.finishTeardown()
		w.m.remove(w)
	}
	return block
}
