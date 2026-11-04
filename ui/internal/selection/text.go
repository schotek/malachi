// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package selection

import "fmt"

// Translator is what the texts need of the catalogue (i18n.Tr in GTK,
// L10n in the other clients).
type Translator interface {
	T(msgid string) string
	N(msgid, plural string, n int) string
}

// SelectedText is the reading pane's page while n rows are selected.
func SelectedText(tr Translator, n int) string {
	// TRANSLATORS: The reading pane while several messages are selected in the list; %d is their number.
	return fmt.Sprintf(tr.N("%d message selected", "%d messages selected", n), n)
}

// CancelSendingQuestion asks before Trash cancels the sending of n queued
// messages at once (one is outbox.go's own question).
func CancelSendingQuestion(tr Translator, n int) string {
	// TRANSLATORS: The question before the sending of several queued messages is cancelled; %d is their number.
	return fmt.Sprintf(tr.N("Cancel sending %d message?", "Cancel sending %d messages?", n), n)
}
