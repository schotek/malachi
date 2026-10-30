// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/schotek/malachi/ui/internal/contacts"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// ConfirmContactsQuit asks before Quit throws away a changed contact form
// (W6, as on Windows): the Contacts window comes forward with the
// DiscardQuestion, done gets true for Discard and false for Cancel, which
// keeps the form as it is. Without a changed form done is called at once.
// Quit asked again while the question is up is ignored.
func ConfirmContactsQuit(done func(bool)) {
	cw := contactsWin
	if cw == nil || cw.closed || !cw.contact.Dirty() {
		done(true)
		return
	}
	if cw.quitAsking {
		return
	}
	cw.quitAsking = true
	cw.Present()
	cw.ask(contacts.DiscardQuestion(i18n.Tr), func(ok bool) {
		// One answer: the dialog's closing may repeat the close response.
		if !cw.quitAsking {
			return
		}
		cw.quitAsking = false
		done(ok)
	})
}
