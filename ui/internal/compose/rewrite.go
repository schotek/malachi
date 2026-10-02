// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/editor"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Assistant is what the compose windows need of the assistant
// (window/assistant.go): whether its one-shot requests can run (the
// Assistant shown, In App chosen, Claude Code found), a notice when that
// may have changed, a fresh look for Claude Code, and a request for a
// rewrite (its consent is the window's).
type Assistant interface {
	CanRunInApp() bool
	OnChange(f func()) (remove func())
	RefreshHandlers()
	NewRequest() *assistantpanel.Request
}

// assistantIcon is the Assistant's icon (window/assistant.go).
const assistantIcon = "malachi-assistant-symbolic"

// rewriteUI is the compose window's rewrite (ui/internal/assistant
// rewrite.go, the In App target; macOS ComposeRewriteViewController and
// ComposeWindowController): the header's Assistant button while the
// one-shot requests can run, and its popover, which rewrites the selection
// or the user's own text above the quoted original (what the editor holds
// before the attribution line of Params.Attribution) with the user's
// Claude Code, and puts the answer in its place or below it as plain text
// through the editor bridge, one step the editor's undo takes back. The
// first request ever asks for consent on this window (the panel's
// question, assistant-consent). Closing the popover or the window ends a
// running request. The passage is never shown here; the answer is plain
// text in a read-only text view.
type rewriteUI struct {
	w       *Window
	button  *gtk.Button
	popover *gtk.Popover

	title        *gtk.Label
	presets      []*gtk.Button
	custom       *gtk.Entry
	status       *gtk.Box
	resultScroll *gtk.ScrolledWindow
	result       *gtk.TextView
	errLabel     *gtk.Label
	insert       *gtk.Button
	replace      *gtk.Button

	// rewriter is made on first use.
	rewriter *assistantpanel.Rewriter
	// target is the passage the popover works on.
	target editor.RewriteTarget
	// opening: the button was clicked and the popover is on its way
	// (consent, the editor's passage).
	opening bool
	remove  func()
}

// wireRewrite builds the rewrite from the window's builder b.
func (w *Window) wireRewrite(b *gtk.Builder) {
	u := &rewriteUI{
		w:            w,
		button:       b.GetObject("rewrite_button").Cast().(*gtk.Button),
		popover:      b.GetObject("rewrite_popover").Cast().(*gtk.Popover),
		title:        b.GetObject("rewrite_title").Cast().(*gtk.Label),
		custom:       b.GetObject("rewrite_custom").Cast().(*gtk.Entry),
		status:       b.GetObject("rewrite_status").Cast().(*gtk.Box),
		resultScroll: b.GetObject("rewrite_result_scroll").Cast().(*gtk.ScrolledWindow),
		result:       b.GetObject("rewrite_result").Cast().(*gtk.TextView),
		errLabel:     b.GetObject("rewrite_error").Cast().(*gtk.Label),
		insert:       b.GetObject("rewrite_insert").Cast().(*gtk.Button),
		replace:      b.GetObject("rewrite_replace").Cast().(*gtk.Button),
	}
	w.rewrite = u
	tr := i18n.Catalog{}
	t := assistant.ComposeTexts(tr)
	label := assistant.Texts(tr).Assistant
	u.button.SetIconName(assistantIcon)
	u.button.SetTooltipText(label)
	u.popover.SetParent(u.button)
	presets := b.GetObject("rewrite_presets").Cast().(*adw.WrapBox)
	for _, r := range assistant.Rewrites {
		r := r
		btn := gtk.NewButtonWithLabel(assistant.RewriteLabel(tr, r))
		btn.AddCSSClass("chip-action")
		btn.ConnectClicked(func() { u.start(r, "") })
		presets.Append(btn)
		u.presets = append(u.presets, btn)
	}
	u.custom.SetPlaceholderText(t.Custom)
	u.custom.ConnectActivate(u.customActivated)
	b.GetObject("rewrite_status_label").Cast().(*gtk.Label).SetText(t.Rewriting)
	discard := b.GetObject("rewrite_discard").Cast().(*gtk.Button)
	discard.SetLabel(i18n.T("_Discard"))
	discard.ConnectClicked(u.popover.Popdown)
	u.insert.SetLabel(t.InsertBelow)
	u.insert.ConnectClicked(func() { u.apply(true) })
	u.replace.SetLabel(t.Replace)
	u.replace.ConnectClicked(func() { u.apply(false) })
	u.button.ConnectClicked(u.clicked)
	// The popover went (Discard, a click elsewhere, Escape, an apply): a
	// running request ends.
	u.popover.ConnectClosed(func() {
		if u.rewriter != nil {
			u.rewriter.Cancel()
		}
	})

	if a := w.m.Assistant; a != nil {
		// Whether Claude Code is there is looked up now (a few stat
		// calls), so the button reflects it from the start.
		a.RefreshHandlers()
		u.remove = a.OnChange(u.sync)
	}
	u.sync()
}

// available says whether the rewrite can run.
func (u *rewriteUI) available() bool {
	return u.w.m.Assistant != nil && u.w.m.Assistant.CanRunInApp()
}

// sync shows the button while the rewrite can run, and closes the popover
// when it cannot.
func (u *rewriteUI) sync() {
	if u.w.pane.dc.draft.closed {
		return
	}
	ok := u.available()
	u.button.SetVisible(ok)
	if !ok && u.popover.Visible() {
		u.popover.Popdown()
	}
}

// close ends it all with the window. The popover is the button's only by
// SetParent, so it is taken off again before the button goes.
func (u *rewriteUI) close() {
	if u.remove != nil {
		u.remove()
		u.remove = nil
	}
	if u.rewriter != nil {
		u.rewriter.Cancel()
	}
	u.popover.Unparent()
}

// clicked is the Assistant button: the popover, or it closes. The first
// request ever asks for consent first, so that the question does not close
// the popover under it.
func (u *rewriteUI) clicked() {
	if u.popover.Visible() {
		u.popover.Popdown()
		return
	}
	if !u.available() || u.opening || u.w.pane.dc.draft.closed {
		return
	}
	u.opening = true
	if u.w.m.settings.AssistantConsent() {
		u.fetchTarget()
		return
	}
	widget.AskAssistantConsent(u.w, func(allowed bool) {
		if !allowed || u.w.pane.dc.draft.closed {
			u.opening = false
			return
		}
		u.w.m.settings.SetAssistantConsent(true)
		u.fetchTarget()
	})
}

// fetchTarget asks the editor for the passage, then shows the popover.
func (u *rewriteUI) fetchTarget() {
	u.w.pane.editor.RewriteTarget(u.w.pane.params.Attribution, func(t editor.RewriteTarget) {
		u.opening = false
		if !u.available() || u.w.pane.dc.draft.closed {
			return
		}
		u.present(t)
	})
}

// rewriterFor is the rewriter, made on first use.
func (u *rewriteUI) rewriterFor() *assistantpanel.Rewriter {
	if u.rewriter != nil {
		return u.rewriter
	}
	req := u.w.m.Assistant.NewRequest()
	req.Consent = func(done func(bool)) { widget.AskAssistantConsent(u.w, done) }
	u.rewriter = assistantpanel.NewRewriter(i18n.Catalog{}, req)
	u.rewriter.OnState = func(assistantpanel.RewriteState) { u.render() }
	return u.rewriter
}

// present opens the popover on passage t.
func (u *rewriteUI) present(t editor.RewriteTarget) {
	rw := u.rewriterFor()
	rw.Cancel()
	u.target = t
	texts := assistant.ComposeTexts(i18n.Catalog{})
	if t.Selected {
		u.title.SetText(texts.RewriteSelection)
	} else {
		u.title.SetText(texts.RewriteText)
	}
	u.custom.SetText("")
	u.render()
	u.popover.Popup()
	if u.hasPassage() {
		u.custom.GrabFocus()
	}
}

// hasPassage says whether there is anything to rewrite.
func (u *rewriteUI) hasPassage() bool {
	return strings.TrimSpace(u.target.Text) != ""
}

// start asks for rewrite r (custom: the user's own instruction).
func (u *rewriteUI) start(r assistant.Rewrite, custom string) {
	if u.rewriter == nil || u.rewriter.Running() {
		return
	}
	u.rewriter.Start(r, custom, u.target.Text)
}

// customActivated is Return in the instruction field: its words are sent;
// with none, an answer that is there replaces the passage.
func (u *rewriteUI) customActivated() {
	if strings.TrimSpace(u.custom.Text()) != "" {
		u.start(assistant.Custom, u.custom.Text())
		return
	}
	if u.rewriter != nil && u.rewriter.State().Kind == assistantpanel.RewriteDone {
		u.apply(false)
	}
}

// render follows the rewriter's state.
func (u *rewriteUI) render() {
	if u.rewriter == nil {
		return
	}
	st := u.rewriter.State()
	running := st.Kind == assistantpanel.RewriteRunning
	for _, b := range u.presets {
		b.SetSensitive(u.hasPassage() && !running)
	}
	u.custom.SetSensitive(u.hasPassage() && !running)
	u.status.SetVisible(running)
	text, answer, errText := "", false, ""
	switch st.Kind {
	case assistantpanel.RewriteRunning:
		text = st.Text
	case assistantpanel.RewriteDone:
		text, answer = st.Text, true
	case assistantpanel.RewriteFailed:
		errText = st.Text
	}
	buf := u.result.Buffer()
	if buf.Text(buf.StartIter(), buf.EndIter(), false) != text {
		buf.SetText(text)
	}
	u.resultScroll.SetVisible(text != "")
	u.errLabel.SetText(errText)
	u.errLabel.SetVisible(errText != "")
	u.insert.SetSensitive(answer)
	u.replace.SetSensitive(answer)
}

// apply is Replace or Insert Below: the popover goes, the editor takes the
// keyboard (so that Ctrl+Z reaches its undo) and the answer as plain text.
func (u *rewriteUI) apply(below bool) {
	if u.rewriter == nil {
		return
	}
	st := u.rewriter.State()
	if st.Kind != assistantpanel.RewriteDone {
		return
	}
	u.popover.Popdown()
	u.w.pane.editor.GrabFocus()
	u.w.pane.editor.ApplyRewrite(st.Text, below)
}
