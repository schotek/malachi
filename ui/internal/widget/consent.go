// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// AskAssistantConsent asks "Send Mail to Claude?" over parent before the
// assistant's first request ever (the panel's question, ui/internal/assistant
// PanelTexts; the answer is kept in assistant-consent by the caller) and
// calls done with the answer.
func AskAssistantConsent(parent gtk.Widgetter, done func(allowed bool)) {
	t := assistant.PanelTexts(i18n.Catalog{})
	askConsent(parent, t.ConsentHeading, t.ConsentBody, done)
}

// AskTriageConsent asks "Let the Assistant Triage the Board?" over parent
// (nil: a window of its own) before the board's triage runs without its
// consent, and when the consent is turned on in Preferences → AI
// (ui/internal/board's TriageConsentHeading and TriageConsentBody, with the
// panel's Allow and the usual Cancel), and calls done with the answer; the
// caller gives the consent (boardtriage.Controller.GiveConsent, which keeps
// board-triage-consent and assistant-consent).
func AskTriageConsent(parent gtk.Widgetter, done func(allowed bool)) {
	askConsent(parent, board.TriageConsentHeading(i18n.Tr), board.TriageConsentBody(i18n.Tr), done)
}

// askConsent is a consent question: heading and body as plain text, Allow
// suggested and the default, Cancel on close.
func askConsent(parent gtk.Widgetter, heading, body string, done func(allowed bool)) {
	t := assistant.PanelTexts(i18n.Catalog{})
	d := adw.NewAlertDialog(heading, body)
	d.SetHeadingUseMarkup(false)
	d.SetBodyUseMarkup(false)
	d.AddResponse("cancel", i18n.T("_Cancel"))
	d.AddResponse("allow", t.Allow)
	d.SetResponseAppearance("allow", adw.ResponseSuggested)
	d.SetDefaultResponse("allow")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) { done(response == "allow") })
	d.Present(parent)
}
