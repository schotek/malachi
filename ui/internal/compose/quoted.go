// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/composebody"
	"github.com/schotek/malachi/ui/internal/editor"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The window layout's two parts below the editor (compose_extras.blp;
// macOS ComposePane makeQuoteSection and the assistant's bar): a new reply's
// or forward's prepared original, read-only and apart from the editor
// (ui/internal/composebody), and the assistant's bar, which the window
// wires (write.go). The board's inline layout has neither: its reply is a
// saved draft, which opens whole in the editor.

// quotedPart is the panel with the prepared original under the editor.
type quotedPart struct {
	section  *gtk.Box
	toggle   *gtk.Button
	content  *adw.ButtonContent
	include  *gtk.CheckButton
	slot     *gtk.Box
	view     *editor.Editor
	expanded bool
}

// barWidgets are the assistant's bar (compose_extras.blp assistant_bar);
// the window wires them (write.go).
type barWidgets struct {
	root          *gtk.Box
	toggle        *gtk.Button
	title         *adw.ButtonContent
	contents      *gtk.Box
	write         *gtk.Button
	instruction   *gtk.Entry
	spinner       *gtk.Box
	previewScroll *gtk.ScrolledWindow
	preview       *gtk.TextView
	errLabel      *gtk.Label
	buttons       *gtk.Box
	discard       *gtk.Button
	replace       *gtk.Button
}

// bodyLayout lays the window's body out: only the window layout shows an
// original apart and starts with the user's own text (OwnHTML); a draft
// opened from Drafts keeps its body whole (KindEdit's mode is ComposeNew).
func bodyLayout(p Params, layout Layout) composebody.Layout {
	mode, own := p.Kind.Mode(), p.OwnHTML
	if layout != LayoutWindow {
		mode, own = api.ComposeNew, ""
	}
	return composebody.New(mode, p.Comment != nil, p.BodyHTML).WithOwn(own)
}

// writeMode is the kind as the assistant's bar names it.
func (k Kind) writeMode() assistant.WriteMode {
	switch k {
	case KindReply:
		return assistant.WriteReply
	case KindReplyAll:
		return assistant.WriteReplyAll
	case KindForward:
		return assistant.WriteForward
	case KindEdit:
		return assistant.WriteEdit
	}
	return assistant.WriteNew
}

// buildWindowParts builds compose_extras.ui and places its parts among the
// pane's children: the quoted original right under the editor (only with
// one), the assistant's bar under the attachment chips (hidden until the
// window shows it). The window layout only, before the status line goes
// in.
func (p *Pane) buildWindowParts() {
	b := data.Builder("compose_extras.ui")
	p.bar = &barWidgets{
		root:          b.GetObject("assistant_bar").Cast().(*gtk.Box),
		toggle:        b.GetObject("assistant_bar_disclosure").Cast().(*gtk.Button),
		title:         b.GetObject("assistant_bar_title").Cast().(*adw.ButtonContent),
		contents:      b.GetObject("assistant_bar_contents").Cast().(*gtk.Box),
		write:         b.GetObject("assistant_bar_write").Cast().(*gtk.Button),
		instruction:   b.GetObject("assistant_bar_instruction").Cast().(*gtk.Entry),
		spinner:       b.GetObject("assistant_bar_spinner").Cast().(*gtk.Box),
		previewScroll: b.GetObject("assistant_bar_preview_scroll").Cast().(*gtk.ScrolledWindow),
		preview:       b.GetObject("assistant_bar_preview").Cast().(*gtk.TextView),
		errLabel:      b.GetObject("assistant_bar_error").Cast().(*gtk.Label),
		buttons:       b.GetObject("assistant_bar_buttons").Cast().(*gtk.Box),
		discard:       b.GetObject("assistant_bar_discard").Cast().(*gtk.Button),
		replace:       b.GetObject("assistant_bar_replace").Cast().(*gtk.Button),
	}
	p.bar.root.SetVisible(false)
	// The editor keeps a usable height however much goes under it.
	p.editorSlot.SetSizeRequest(-1, 100)
	if p.body.HasQuote() {
		texts := assistant.WriteTexts(i18n.Catalog{}, p.params.Kind.writeMode())
		q := &quotedPart{
			section: b.GetObject("quote_section").Cast().(*gtk.Box),
			toggle:  b.GetObject("quote_disclosure").Cast().(*gtk.Button),
			content: b.GetObject("quote_disclosure_content").Cast().(*adw.ButtonContent),
			include: b.GetObject("include_quote").Cast().(*gtk.CheckButton),
			slot:    b.GetObject("quoted_slot").Cast().(*gtk.Box),
			view:    editor.New(p.log),
		}
		q.content.SetLabel(texts.Quoted)
		q.include.SetLabel(texts.Include)
		q.view.ReadOnly = true
		q.view.SetVExpand(true)
		q.slot.Append(q.view)
		q.view.OnCrashed = func() {
			if !p.tornDown {
				q.view.Load(p.body.QuotedHTML)
			}
		}
		q.toggle.ConnectClicked(func() { q.setExpanded(!q.expanded) })
		q.include.ConnectToggled(func() {
			if !p.dc.draft.closed {
				p.dc.markDirty()
			}
		})
		// Collapsed at first: the user's own text has the window.
		q.setExpanded(false)
		q.view.Load(p.body.QuotedHTML)
		p.quoted = q
		p.root.InsertChildAfter(q.section, p.editorSlot)
	}
	p.root.InsertChildAfter(p.bar.root, p.attBox)
}

// setExpanded shows or hides the original's view; the draft is unchanged.
func (q *quotedPart) setExpanded(on bool) {
	q.expanded = on
	q.slot.SetVisible(on)
	if on {
		q.content.SetIconName("pan-down-symbolic")
		q.toggle.SetTooltipText(i18n.T("Expanded"))
	} else {
		q.content.SetIconName("pan-end-symbolic")
		q.toggle.SetTooltipText(i18n.T("Collapsed"))
	}
}

// includesQuote says whether the draft and the message include the
// original.
func (p *Pane) includesQuote() bool {
	return p.quoted != nil && p.quoted.include.Active()
}

// quotedText is the plain text of the original ("" without one).
func (p *Pane) quotedText() string {
	if p.quoted == nil {
		return ""
	}
	return p.quoted.view.Text()
}

// flushQuoted flushes the original's view after the editor, then calls
// done.
func (p *Pane) flushQuoted(done func()) {
	if p.quoted == nil {
		done()
		return
	}
	p.quoted.view.Flush(done)
}

// closeQuoted ends the original's web process with the pane.
func (p *Pane) closeQuoted() {
	if p.quoted != nil {
		p.quoted.view.Close()
	}
}
