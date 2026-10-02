// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/boardtriage"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The Triage control (board_triage_button in board_page.blp): the board's
// own entry into the application's single board triage (board_triage.go's
// BoardTriage, one per application, shared with the status strip's note,
// renderStatusLabel in board.go). This file only wires the GTK button to
// boardtriage.Controller's View and Observe; every decision — whether it
// is offered, its title, sensitivity and tooltip — is the controller's
// (ui/internal/boardtriage), ported from macOS.

// boardTriageOrNil is the application's board triage, nil before
// Assistant.AttachBoardTriage ran (always non-nil once main.go's startup
// has run; a test building a bare Window has none).
func (w *Window) boardTriageOrNil() *BoardTriage {
	if w.assist == nil {
		return nil
	}
	return w.assist.BoardTriage()
}

// wireTriage connects the Triage button to the application's board
// triage. With none (MALACHI_BOARD_SAMPLES has none), the button stays
// hidden, renderTriageButton's default. Observes for the lifetime of the
// board page: the main window lives as long as the application, so this
// is never unbound (as assistant_panel.go's own buttons are not).
func (p *boardPage) wireTriage() {
	p.triageButton.ConnectClicked(p.onTriageClicked)
	bt := p.w.boardTriageOrNil()
	if bt == nil {
		return
	}
	p.triageRemoveObserve = bt.Controller().Observe(func() {
		p.renderTriageButton()
		p.w.refreshBoardStatusLabel()
	})
	// A manual run's own result (OnManualRunEnded skips a declined consent
	// and every automatic run, whose status strip note says enough).
	p.triageRemoveEnded = bt.OnManualRunEnded(p.w.Toast)
}

// boardTriageClick is onTriageClicked's decision for one boardtriage.View
// Control, pure and tested without GTK.
type boardTriageClick int

const (
	// clickNone: ControlHidden or ControlUnavailable — the click does
	// nothing (the button is hidden or insensitive, so the handler is
	// never reached in practice; named for the test, not for a real path).
	clickNone boardTriageClick = iota
	// clickStart: ControlTriage — Start a manual run.
	clickStart
	// clickStop: ControlStop — Cancel the run under way.
	clickStop
	// clickInstall: ControlGetClaudeCode — open Anthropic's page.
	clickInstall
	// clickSignIn: ControlSignIn — run Claude Code's own sign-in.
	clickSignIn
)

// boardTriageClickFor is the action one click on the Triage button takes
// for Control c.
func boardTriageClickFor(c boardtriage.Control) boardTriageClick {
	switch c {
	case boardtriage.ControlTriage:
		return clickStart
	case boardtriage.ControlStop:
		return clickStop
	case boardtriage.ControlGetClaudeCode:
		return clickInstall
	case boardtriage.ControlSignIn:
		return clickSignIn
	}
	return clickNone
}

// onTriageClicked runs the Triage button's control (boardtriage.View's
// Control, boardTriageClickFor), the same four things Preferences → AI's
// own Claude Code row offers (preferences.go bindClaudeCode): start or
// stop a manual run, open Anthropic's page, or run Claude Code's own
// sign-in.
func (p *boardPage) onTriageClicked() {
	bt := p.w.boardTriageOrNil()
	if bt == nil {
		return
	}
	switch boardTriageClickFor(bt.Controller().View().Control) {
	case clickStart:
		bt.Controller().Start(boardtriage.Manual, 0)
	case clickStop:
		bt.Controller().Cancel()
	case clickInstall:
		p.w.launchURI(&p.w.ApplicationWindow.Window, assistant.InstallURL)
	case clickSignIn:
		p.w.assist.locator.SignIn(func(r assistantpanel.SignInResult) {
			switch r.Outcome {
			case assistantpanel.SignInFailed:
				p.w.Toast(assistant.SignInFailedText(i18n.Tr, r.Reason))
			case assistantpanel.SignInTimedOut:
				p.w.Toast(assistant.SignInTexts(i18n.Tr).TimedOut)
			}
		})
	}
}

// renderTriageButton shows the Triage button's state (boardtriage.View):
// hidden when triage is not offered (Offered), its title, sensitivity and
// tooltip otherwise. Called from applyAll/onChange (board.go, after every
// redraw of the board) and from wireTriage's Observe (whenever the
// triage's own state moves between those redraws).
func (p *boardPage) renderTriageButton() {
	bt := p.w.boardTriageOrNil()
	if bt == nil {
		p.triageButton.SetVisible(false)
		return
	}
	v := bt.Controller().View()
	p.triageButton.SetVisible(v.Offered())
	p.triageButton.SetLabel(v.Title)
	p.triageButton.SetSensitive(v.Enabled)
	p.triageButton.SetTooltipText(v.ToolTip)
}
