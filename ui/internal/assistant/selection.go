// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

// The Assistant's message actions on a selection of several rows of the
// message list (ui/internal/selection): the ones that make sense for
// several messages, and Ask's label for them.

// MessageActionsFor are MessageActions for count selected rows: several
// leave out Draft a Reply…, which answers one message.
func MessageActionsFor(count int) []Action {
	if count <= 1 {
		return MessageActions
	}
	out := make([]Action, 0, len(MessageActions))
	for _, a := range MessageActions {
		if a != DraftReply {
			out = append(out, a)
		}
	}
	return out
}

// LabelFor is Label for count selected rows: Ask names them as several.
func LabelFor(tr Translator, a Action, count int) string {
	if a == Ask && count > 1 {
		// TRANSLATORS: An item of the Assistant menu while several messages are selected.
		return tr.T("Ask About These Messages…")
	}
	return Label(tr, a)
}
