// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// ConfirmQuitUnsaved is presented only after the bounded attempt to save
// every inline reply. Cancel resumes the editors; confirmation allows the
// application's pending close or quit to continue, as on macOS.
func (w *Window) ConfirmQuitUnsaved(done func(bool)) {
	// A main window closed while another compose window kept the app
	// alive may have released its hold; a cancelled quit restores it.
	if w.app != nil {
		w.SetApplication(&w.app.Application)
	}
	w.Present()
	unsaved, sending := false, false
	if pg := w.boardPage; pg != nil && pg.replyPanes != nil {
		unsaved, sending = pg.replyPanes.HasUnsaved(), pg.replyPanes.HasSending()
	}
	d := adw.NewAlertDialog(board.QuitHeading(unsaved, sending, i18n.Tr), board.QuitUnsavedBody(i18n.Tr))
	d.SetHeadingUseMarkup(false)
	d.SetBodyUseMarkup(false)
	d.AddResponse("cancel", i18n.T("_Cancel"))
	d.AddResponse("quit", board.QuitAnyway(i18n.Tr))
	d.SetResponseAppearance("quit", adw.ResponseDestructive)
	d.SetDefaultResponse("cancel")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) { done(response == "quit") })
	d.Present(w)
}

// mainMenuTooltip is the board's main menu button's tooltip, Mail's own
// (window.blp), so board_page.blp needs no msgid of its own.
func mainMenuTooltip() string { return i18n.T("Main Menu") }
