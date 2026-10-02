// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"math"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// LayoutInline's own pieces: the footer row (Attach, the status line,
// Discard, Send — no window chrome to put them in), the sized editor (the
// board's own height clamp, board/editor_height.go) and the shortcut
// scope that keeps Ctrl+Enter/Ctrl+S out of the rest of the host window
// (compose_pane.blp's pane_root, never a window-level control: a button's
// own accelerator would fire from anywhere in the main window).

// buildInlineFooter appends "[Attach] status … [Discard] [Send]" as
// pane_root's last child: draft_status (the standalone compose_pane.blp
// object) moves in here instead of being inset as the window layout's
// bottom bar (NewPane). Nothing attaches in comment mode (applyCommentMode
// hides footerAttach the same way the window hides its header bar's own
// Attach button).
func (p *Pane) buildInlineFooter() {
	attach := gtk.NewButtonFromIconName("mail-attachment-symbolic")
	attach.SetTooltipText(i18n.T("Attach Files"))
	attach.SetActionName("compose.attach")
	p.footerAttach = attach

	discard := gtk.NewButtonWithLabel(board.Discard(i18n.Tr))
	discard.SetUseUnderline(true)
	discard.SetActionName("compose.discard")
	p.footerDiscard = discard

	content := adw.NewButtonContent()
	content.SetIconName("mail-send-symbolic")
	content.SetLabel(i18n.T("_Send"))
	content.SetUseUnderline(true)
	send := gtk.NewButton()
	send.SetChild(content)
	send.AddCSSClass("suggested-action")
	send.SetActionName("compose.send")
	p.footerSend = send

	p.status.SetHExpand(true)
	p.status.SetMarginStart(0)

	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	row.SetMarginTop(8)
	row.Append(attach)
	row.Append(p.status)
	row.Append(discard)
	row.Append(send)
	p.root.Append(row)
}

// wireInlineShortcuts gives the pane its own Ctrl+Enter (Send) and Ctrl+S
// (Save) — Save is a no-op in comment mode, same as the window's menu
// item, since compose.save is disabled then. Capture phase, so these win
// over the WebKit view exactly as the window's own ShortcutController
// does (compose.blp); attached to pane_root, so only a key event whose
// path runs through the pane — the keyboard focus is inside it — ever
// reaches them. The inline Send button itself carries no accelerator of
// its own: this is the only way either shortcut reaches Send.
func (p *Pane) wireInlineShortcuts() {
	sc := gtk.NewShortcutController()
	sc.SetPropagationPhase(gtk.PhaseCapture)
	sc.AddShortcut(gtk.NewShortcut(gtk.NewShortcutTriggerParseString("<Control>Return"), gtk.NewNamedAction("compose.send")))
	sc.AddShortcut(gtk.NewShortcut(gtk.NewShortcutTriggerParseString("<Control>s"), gtk.NewNamedAction("compose.save")))
	p.root.AddController(sc)
}

// wireSizedEditor turns the editor's own vexpand off (it no longer fills
// whatever contains it) and gives it a fixed height that follows its
// document (board.NewEditorHeight), reported through the bridge
// (editor.Editor.OnHeight).
func (p *Pane) wireSizedEditor() {
	p.editorSlot.SetVExpand(false)
	p.editor.SetVExpand(false)
	p.editor.SetSizeRequest(-1, int(board.EditorMinHeight))
	p.editor.OnHeight = func(css float64) {
		p.contentHeight, p.haveContentHeight = css, true
		p.applyEditorHeight()
	}
}

// SetVisibleHeight tells the inline pane how tall its host's visible area
// is, for the editor's height cap (board.EditorHeightCap,
// board.EditorVisibleShare of it). The embedding host (the board's case
// detail) calls this whenever that changes; a no-op outside LayoutInline.
func (p *Pane) SetVisibleHeight(h float64) {
	if p.options.Layout != LayoutInline {
		return
	}
	p.visibleHeight = h
	p.applyEditorHeight()
}

// applyEditorHeight clamps the editor's reported content height
// (board.NewEditorHeight) and applies it, within 0.5px of the last value
// applied (so a report that rounds to the same pixel does not re-lay-out
// or re-notify the host). While the content fits (EditorHeight.Scrolls is
// false) the editor's own document never overflows, so there is nothing
// for it to capture the wheel over: it scrolls the host like any other
// widget in the column. Past the cap the WebView scrolls inside on its
// own; nothing else needs doing here.
func (p *Pane) applyEditorHeight() {
	if p.options.Layout != LayoutInline {
		return
	}
	content := p.contentHeight
	if !p.haveContentHeight {
		content = 0
	}
	eh := board.NewEditorHeight(content, p.visibleHeight)
	if p.editorHeightSet && math.Abs(p.appliedHeight-eh.Height) < 0.5 {
		return
	}
	p.editorHeightSet = true
	p.appliedHeight = eh.Height
	p.editor.SetSizeRequest(-1, int(math.Round(eh.Height)))
	if p.OnHeight != nil {
		p.OnHeight(eh.Height)
	}
}
