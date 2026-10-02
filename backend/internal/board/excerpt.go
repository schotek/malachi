// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import (
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Excerpt is the plain text of a message as the board shows it
// (api.BoardMessage.Text, api.BoardQueueMessage.Text).
type Excerpt struct {
	Text string
	// Trimmed: something was cut off — quoted history, signature, or the
	// length limit (api.BoardMessage.Trimmed, api.BoardQueueMessage.Truncated).
	Trimmed bool
}

// MessageExcerpt returns the excerpt of a message's stored plain text: the
// quoted history and the signature cut off (sanitize.TrimQuotedText and the
// RFC 3676 separator), cleaned (CleanText),
// each line's whitespace collapsed, at most one empty line in a row, and
// cut to max bytes at a character boundary (max ≤ 0: no limit). Never
// HTML; URLs stay (unlike annotation strings).
func MessageExcerpt(text string, max int) Excerpt {
	own, trimmed := excerptText(text)
	out, fits := cut(collapseBlock(CleanText(own)), max)
	return Excerpt{Text: out, Trimmed: trimmed || !fits}
}

// BoardMessageExcerpt is MessageExcerpt with the limit of board.get
// (api.MaxBoardMessageTextBytes).
func BoardMessageExcerpt(text string) Excerpt {
	return MessageExcerpt(text, api.MaxBoardMessageTextBytes)
}

// QueueExcerpts returns the excerpts of one board.queue item's messages,
// given oldest first as the item lists them: each at most
// api.MaxBoardQueueMessageBytes, and all together at most
// api.MaxBoardQueueCaseBytes, the newest served first (an older message
// gets what is left, possibly nothing, and is then marked trimmed).
func QueueExcerpts(texts []string) []Excerpt {
	out := make([]Excerpt, len(texts))
	budget := api.MaxBoardQueueCaseBytes
	for i := len(texts) - 1; i >= 0; i-- {
		limit := min(api.MaxBoardQueueMessageBytes, budget)
		if limit <= 0 {
			// Nothing left: trimmed when there was anything to show.
			out[i] = Excerpt{Trimmed: strings.TrimSpace(texts[i]) != ""}
			continue
		}
		out[i] = MessageExcerpt(texts[i], limit)
		budget -= len(out[i].Text)
	}
	return out
}
