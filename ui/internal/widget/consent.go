// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// AskAssistantConsent asks "Send Mail to Claude?" over parent before the
// assistant's first request ever (the panel's question, ui/internal/assistant
// PanelTexts; the answer is kept in assistant-consent by the caller) and
// calls done with the answer.
func AskAssistantConsent(parent gtk.Widgetter, done func(allowed bool)) {
	t := assistant.PanelTexts(i18n.Catalog{})
	d := adw.NewAlertDialog(t.ConsentHeading, t.ConsentBody)
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
