// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/editor"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// writeUI is the compose window's assistant bar (compose_extras.blp
// assistant_bar, ui/internal/assistant compose_write.go; macOS
// ComposeWindowController wireAssistant and writeWithAssistant,
// ComposeAssistantBar): shown while the rewrite's one-shot requests can
// run (the In App target), never for a comment. Write sends the optional
// instruction, the subject, the user's own text and the prepared original
// (quoted.go) to the user's assistant (assistantpanel.Writer); the answer
// streams into a read-only preview as plain text, and only Replace puts it
// into the editor, in place of the user's own text (everything above the
// attribution line when the original is in the editor, the whole editor
// when it is apart), one step the editor's undo takes back. Cancel or
// Discard drop it. The first request ever asks for consent on this window;
// a change of provider and closing the window end a running request.
type writeUI struct {
	w   *Window
	bar *barWidgets

	// writer is made on first use.
	writer   *assistantpanel.Writer
	expanded bool
	remove   func()
}

// wireWrite wires the pane's bar (window layout only).
func (w *Window) wireWrite() {
	bar := w.pane.bar
	if bar == nil {
		return
	}
	u := &writeUI{w: w, bar: bar, expanded: true}
	w.write = u
	tr := i18n.Catalog{}
	texts := assistant.WriteTexts(tr, w.params.Kind.writeMode())
	bar.write.SetLabel(texts.Write)
	bar.instruction.SetPlaceholderText(texts.Instruction)
	bar.replace.SetLabel(assistant.ComposeTexts(tr).Replace)
	bar.toggle.ConnectClicked(func() { u.setExpanded(!u.expanded) })
	bar.write.ConnectClicked(u.start)
	bar.instruction.ConnectActivate(u.start)
	bar.discard.ConnectClicked(u.cancel)
	bar.replace.ConnectClicked(u.apply)
	u.setExpanded(true)

	if a := w.m.Assistant; a != nil {
		generation := a.RuntimeGeneration()
		u.remove = a.OnChange(func() {
			if now := a.RuntimeGeneration(); now != generation {
				generation = now
				u.cancel()
			}
			u.sync()
		})
	}
	u.sync()
	u.render()
}

// available says whether the bar can write.
func (u *writeUI) available() bool {
	return u.w.m.Assistant != nil && u.w.m.Assistant.CanRunInApp() && !u.w.pane.isComment()
}

// sync shows the bar, named after the provider in effect, while it can
// write; a request under way ends when it cannot.
func (u *writeUI) sync() {
	if u.w.pane.dc.draft.closed {
		return
	}
	ok := u.available()
	u.bar.root.SetVisible(ok)
	if !ok {
		u.cancel()
		return
	}
	u.bar.title.SetLabel(assistant.WriteBarTitle(i18n.Catalog{}, u.w.m.Assistant.UsesChatGPT()))
}

// setExpanded shows or hides the bar's contents under its heading.
func (u *writeUI) setExpanded(on bool) {
	u.expanded = on
	u.bar.contents.SetVisible(on)
	if on {
		u.bar.title.SetIconName("pan-down-symbolic")
	} else {
		u.bar.title.SetIconName("pan-end-symbolic")
	}
}

// writerFor is the writer, made on first use.
func (u *writeUI) writerFor() *assistantpanel.Writer {
	if u.writer != nil {
		return u.writer
	}
	req := u.w.m.Assistant.NewRequest()
	req.Consent = func(done func(bool)) { u.w.m.Assistant.AskAssistantConsent(u.w, done) }
	u.writer = assistantpanel.NewWriter(i18n.Catalog{}, req)
	u.writer.Profile = func() assistant.Profile {
		return u.w.m.Assistant.Profile().For(string(u.w.pane.account().ID))
	}
	u.writer.OnState = func(assistantpanel.RewriteState) { u.render() }
	return u.writer
}

// start is Write (or Return in the instruction): the editors are flushed
// first, so that the request has what they show.
func (u *writeUI) start() {
	p := u.w.pane
	if !u.available() || p.dc.draft.closed || !p.editor.Ready() || (u.writer != nil && u.writer.Running()) {
		return
	}
	instruction := u.bar.instruction.Text()
	p.flushEditor(func() {
		if p.dc.draft.closed || !u.available() {
			return
		}
		u.writerFor().Start(u.w.params.Kind.writeMode(), instruction, p.subject(), p.editor.Text(), p.quotedText())
	})
}

// cancel is Cancel or Discard: the request under way ends, the answer
// goes.
func (u *writeUI) cancel() {
	if u.writer != nil {
		u.writer.Cancel()
	}
	u.render()
}

// render follows the writer's state.
func (u *writeUI) render() {
	var st assistantpanel.RewriteState
	if u.writer != nil {
		st = u.writer.State()
	}
	running := st.Kind == assistantpanel.RewriteRunning
	text, errText := "", ""
	switch st.Kind {
	case assistantpanel.RewriteRunning, assistantpanel.RewriteDone:
		text = st.Text
	case assistantpanel.RewriteFailed:
		errText = st.Text
	}
	u.bar.spinner.SetVisible(running)
	u.bar.write.SetSensitive(!running)
	u.bar.instruction.SetSensitive(!running)
	buf := u.bar.preview.Buffer()
	if buf.Text(buf.StartIter(), buf.EndIter(), false) != text {
		buf.SetText(text)
	}
	u.bar.previewScroll.SetVisible(text != "")
	u.bar.errLabel.SetText(errText)
	u.bar.errLabel.SetVisible(errText != "")
	u.bar.buttons.SetVisible(running || text != "")
	if running {
		u.bar.discard.SetLabel(i18n.T("_Cancel"))
	} else {
		u.bar.discard.SetLabel(i18n.T("_Discard"))
	}
	u.bar.replace.SetSensitive(st.Kind == assistantpanel.RewriteDone)
}

// apply is Replace: the editor takes the keyboard (so that Ctrl+Z reaches
// its undo) and the answer as plain text in place of the user's own text;
// the bar empties.
func (u *writeUI) apply() {
	if u.writer == nil {
		return
	}
	st := u.writer.State()
	if st.Kind != assistantpanel.RewriteDone {
		return
	}
	p := u.w.pane
	attribution := u.w.params.Attribution
	if p.quoted != nil {
		attribution = ""
	}
	text := st.Text
	p.editor.FocusStart()
	p.editor.RewriteTarget(attribution, func(editor.RewriteTarget) {
		if !p.dc.draft.closed {
			p.editor.ApplyRewrite(text, false)
		}
	})
	u.cancel()
}

// close ends it all with the window.
func (u *writeUI) close() {
	if u.remove != nil {
		u.remove()
		u.remove = nil
	}
	if u.writer != nil {
		u.writer.Cancel()
	}
}
