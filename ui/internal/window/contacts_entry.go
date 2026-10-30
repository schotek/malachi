// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
)

// The ways into the Contacts window from the main window (W2): Show
// Contact of an address chip opens the window on the contact, More
// Details… of Add to Contacts opens it with the prefilled form.

// showContact opens the Contacts window on a contact (Show Contact).
func (w *Window) showContact(id api.ContactID) {
	w.OpenContacts().ShowContact(id)
}

// newContactFrom opens the Contacts window with the form of a new
// contact prefilled (Add to Contacts' More Details…).
func (w *Window) newContactFrom(f *contacts.Form) {
	w.OpenContacts().NewContactFrom(f)
}
