// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/boardreply"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
)

// BoardReply owns the application's one Suggest Reply request, independently
// of the selected case and the lifetime of its detail. Like BoardTriage it
// is attached after the client exists and stopped before that client closes.
type BoardReply struct {
	ctl     *boardreply.Controller
	removes []func()
	stopped bool
}

// AttachBoardReply wires the pure controller to the shared assistant's
// availability, sign-in and consent. The reply uses the ordinary assistant
// consent, not the additional consent needed by background triage.
func (a *Assistant) AttachBoardReply(rpc *client.Client, mainWindow func() *Window) *BoardReply {
	if a.reply != nil {
		return a.reply
	}
	b := &BoardReply{}
	b.ctl = boardreply.NewController(boardreply.Config{
		Caller: rpc, Settings: providerSettings{a}, Locator: providerLocator{a},
		Request: a.NewRequest(), Loop: glibLoop{}, Log: a.log.With("part", "board-reply"),
		Bridge: a.bridge, Socket: rpc.Socket,
		Available: func() bool { return boardreply.ReplyAvailable(a.shown(), a.target()) },
	})
	window := func() *Window {
		if mainWindow == nil {
			return nil
		}
		return mainWindow()
	}
	b.ctl.Consent = func(done func(bool)) {
		var parent gtk.Widgetter
		if w := window(); w != nil {
			parent = w
		}
		a.AskAssistantConsent(parent, done)
	}
	b.ctl.OnRefresh = func() {
		if w := window(); w != nil && w.boardPage != nil && w.boardPage.daemon != nil {
			w.boardPage.src.Refresh()
		}
	}
	b.removes = append(b.removes,
		a.OnChange(b.ctl.AvailabilityChanged),
		a.settings.OnChanged(settings.KeyAssistantClaudePath, b.ctl.AvailabilityChanged),
	)
	a.reply = b
	b.ctl.CheckSignIn()
	return b
}

// StopBoardReply bounds cancellation and pending draft cleanup while the
// daemon is still reachable. Safe to call more than once during shutdown.
func (a *Assistant) StopBoardReply() {
	b := a.reply
	if b == nil || b.stopped {
		return
	}
	b.stopped = true
	b.ctl.CancelAndCleanUp(boardreply.EndWait)
	b.ctl.Close()
	for _, remove := range b.removes {
		remove()
	}
	b.removes = nil
}

// boardSuggestReplyControl is kept across refreshes of the detail: typing
// survives a source refresh but a different case clears the instruction.
// Matches BoardSuggestReplyControl.swift; all model/daemon text is plain.
type boardSuggestReplyControl struct {
	root, input, running *gtk.Box
	field                *gtk.Entry
	button, stop         *gtk.Button
	progress, note       *gtk.Label
	sample               *gtk.Box
	sampleText           *gtk.Label
	shownCase            board.CaseID
	view                 board.SuggestReplyView
	token                *board.ObserverToken
}

func (p *boardPage) initBoardSuggestReply() {
	if p.suggest != nil {
		return
	}
	b := data.Builder("board_suggest_reply.ui")
	s := &boardSuggestReplyControl{
		root:       b.GetObject("suggest_reply").Cast().(*gtk.Box),
		input:      b.GetObject("suggest_input").Cast().(*gtk.Box),
		running:    b.GetObject("suggest_running").Cast().(*gtk.Box),
		field:      b.GetObject("suggest_instruction").Cast().(*gtk.Entry),
		button:     b.GetObject("suggest_button").Cast().(*gtk.Button),
		stop:       b.GetObject("suggest_stop").Cast().(*gtk.Button),
		progress:   b.GetObject("suggest_progress").Cast().(*gtk.Label),
		note:       b.GetObject("suggest_note").Cast().(*gtk.Label),
		sample:     b.GetObject("sample_reply").Cast().(*gtk.Box),
		sampleText: b.GetObject("sample_text").Cast().(*gtk.Label),
	}
	p.suggest = s
	b.GetObject("sample_heading").Cast().(*gtk.Label).SetText(board.DraftHeading(i18n.Tr))
	b.GetObject("sample_note").Cast().(*gtk.Label).SetText(board.DraftNote(i18n.Tr))
	discard := b.GetObject("sample_discard").Cast().(*gtk.Button)
	discard.SetLabel(board.Discard(i18n.Tr))
	discard.ConnectClicked(func() {
		if d := p.ctl.View().Detail; p.daemon == nil && d != nil {
			p.ctl.DiscardDraft(d.ID)
		}
	})
	s.button.ConnectClicked(p.suggestBoardReply)
	s.field.ConnectActivate(p.suggestBoardReply)
	s.stop.ConnectClicked(func() {
		if ctl := p.suggestReplyController(); ctl != nil && s.view.Running {
			ctl.Cancel()
		}
	})
	if ctl := p.suggestReplyController(); ctl != nil {
		s.token = ctl.Observe(func() {
			if d := p.ctl.View().Detail; d != nil {
				p.renderDetailReplySlot(*d)
			}
		})
	}
}

func (p *boardPage) closeBoardSuggestReply() {
	if p.suggest != nil {
		p.suggest.token.Cancel()
	}
}

func (p *boardPage) suggestReplyController() *boardreply.Controller {
	if p.daemon == nil || p.w.assist == nil || p.w.assist.reply == nil || p.w.assist.reply.stopped {
		return nil
	}
	return p.w.assist.reply.ctl
}

func (p *boardPage) suggestReplyView(id board.CaseID) board.SuggestReplyView {
	ctl := p.suggestReplyController()
	if ctl == nil {
		return board.SuggestReplyView{}
	}
	snapshot := p.src.Snapshot()
	k, ok := snapshot.Case(id)
	if !ok {
		return board.SuggestReplyView{}
	}
	words := providerBoardTranslator{p.w.assist, i18n.Tr}
	panel := assistant.PanelTexts(words)
	// A case waiting for them is titled Suggest Follow-up by boardreply's
	// View itself (SuggestReplyInputs.FollowUp, the effective state).
	return ctl.View(k, snapshot, false, board.PanelWords{
		Stop: panel.Stop, NotFound: panel.NotFound, SignInHint: assistant.SignInTexts(words).Hint,
	}, words)
}

func (p *boardPage) suggestBoardReply() {
	d := p.ctl.View().Detail
	ctl := p.suggestReplyController()
	if d == nil || ctl == nil {
		return
	}
	// Recheck current eligibility, even when the field was activated just
	// as a source update removed or changed the case.
	view := p.suggestReplyView(d.ID)
	if !view.Shown || !view.Enabled || view.Running {
		return
	}
	if k, ok := p.src.Snapshot().Case(d.ID); ok {
		ctl.Start(k, p.suggest.field.Text())
	}
}

// renderSuggestReply fills an otherwise empty inline-reply slot with the
// request control or the samples' static draft. The live control is never
// detached for ordinary refreshes, preserving the field and keyboard focus.
func (p *boardPage) renderSuggestReply(d board.Detail) bool {
	p.initBoardSuggestReply()
	s := p.suggest
	if s.shownCase != d.ID {
		s.shownCase = d.ID
		s.field.SetText("")
	}
	var child *gtk.Box
	if p.daemon == nil && d.Draft != "" {
		s.sampleText.SetText(d.Draft)
		child = s.sample
	} else {
		v := p.suggestReplyView(d.ID)
		s.view = v
		if v.Shown {
			focus := p.w.Focus()
			hadInputFocus := focus != nil && gtk.BaseWidget(focus).IsAncestor(s.input)
			s.field.SetPlaceholderText(v.Placeholder)
			s.field.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel}, []coreglib.Value{*coreglib.NewValue(v.Placeholder)})
			s.button.SetLabel(v.Title)
			s.stop.SetLabel(v.Stop)
			s.progress.SetText(v.Progress)
			s.input.SetVisible(!v.Running)
			s.running.SetVisible(v.Running)
			if !v.Enabled && hadInputFocus {
				if v.Running {
					s.stop.GrabFocus()
				} else {
					p.replyButton.GrabFocus()
				}
			}
			s.field.SetSensitive(v.Enabled)
			s.button.SetSensitive(v.Enabled)
			s.note.SetText(v.Note)
			s.note.SetVisible(v.Note != "")
			s.note.RemoveCSSClass("error")
			s.note.RemoveCSSClass("dim-label")
			if v.NoteIsFailure {
				s.note.AddCSSClass("error")
			} else {
				s.note.AddCSSClass("dim-label")
			}
			child = s.root
		}
	}
	if child == nil {
		removeAllChildren(p.replySlot)
		p.replySlot.SetVisible(false)
		return false
	}
	if child.Parent() == nil {
		removeAllChildren(p.replySlot)
		p.replySlot.Append(child)
	}
	p.replySlot.SetVisible(true)
	return true
}
