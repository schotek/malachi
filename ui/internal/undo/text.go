// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package undo

import "fmt"

// Translator turns msgids into the user's language (i18n.Tr; the Swift
// and C# ports use L10n), as jira.Translator.
type Translator interface {
	T(msgid string) string
	N(msgid, plural string, n int) string
	C(context, msgid string) string
}

// Done is the text of the toast after the move of e: what happened.
func Done(e Entry, tr Translator) string {
	n := e.Count()
	switch e.Kind {
	case Archive, BoardArchive:
		return fmt.Sprintf(tr.N("Archived %d message.", "Archived %d messages.", n), n)
	case Junk:
		// TRANSLATORS: the toast after messages went to the Junk folder;
		// its button takes it back.
		return fmt.Sprintf(tr.N("Moved %d message to Junk.", "Moved %d messages to Junk.", n), n)
	case Move:
		if e.TargetName != "" {
			// TRANSLATORS: the toast after Move To; %d is the number of
			// messages, %s the name of the folder they went to.
			return fmt.Sprintf(tr.N("Moved %d message to “%s”.", "Moved %d messages to “%s”.", n), n, e.TargetName)
		}
		// TRANSLATORS: the toast after Move To, when the folder has no name.
		return fmt.Sprintf(tr.N("Moved %d message.", "Moved %d messages.", n), n)
	}
	return fmt.Sprintf(tr.N("Moved %d message to Trash.", "Moved %d messages to Trash.", n), n)
}

// Button is the button of the toast that takes the move back, with the
// keyboard shortcut that does the same (shortcut: "Ctrl+Z", "⌘Z"; never
// translated); without a shortcut just Undo.
func Button(shortcut string, tr Translator) string {
	if shortcut == "" {
		// TRANSLATORS: the button of a toast that takes back what was just
		// done (an archive).
		return tr.T("Undo")
	}
	// TRANSLATORS: the button of the toast after a message was moved (to
	// Trash, the archive, another folder); it moves it back. %s is the
	// keyboard shortcut that does the same, like Ctrl+Z.
	return fmt.Sprintf(tr.T("Undo (%s)"), shortcut)
}

// Undone is the toast after Undo took e back.
func Undone(e Entry, tr Translator) string {
	n := e.Count()
	// TRANSLATORS: the toast after Undo moved messages back to where they
	// were.
	return fmt.Sprintf(tr.N("Moved %d message back.", "Moved %d messages back.", n), n)
}

// Failed is the toast when Undo could not move the messages back.
func Failed(tr Translator) string {
	// TRANSLATORS: Undo (Ctrl+Z) of a move to Trash or another folder
	// failed.
	return tr.T("Could not move the messages back.")
}

// Nothing is the toast of Undo with nothing on the stack.
func Nothing(tr Translator) string {
	// TRANSLATORS: Ctrl+Z when no move of mail is left to take back.
	return tr.T("Nothing to undo.")
}
