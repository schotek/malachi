// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import (
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The verbatim check of docs/api.md §4.13 "Quotes": a deadline's quote
// must be in the text of a member of the case, a commitment's in the
// user's own text of one of their messages. Both sides are normalised
// alike — CleanText (control, format and invisible characters removed),
// every URL token replaced by one placeholder character (so that removing
// a URL never joins the words around it), Unicode NFC, typographic
// apostrophes and quotation marks folded to ASCII, every run of whitespace
// one space — and the quote must then be an exact substring.

// CleanQuote cleans a quote an assistant sent (CleanLine, then NFC) and
// reports whether it is within the limits: api.MinBoardQuoteBytes to
// api.MaxBoardQuoteBytes bytes and at least api.MinBoardQuoteChars
// characters that are not spaces. The cleaned quote is what a case stores
// and shows.
func CleanQuote(q string) (string, bool) {
	c, _ := CleanLine(q, 0)
	c = norm.NFC.String(c)
	if len(c) < api.MinBoardQuoteBytes || len(c) > api.MaxBoardQuoteBytes {
		return c, false
	}
	chars := 0
	for _, r := range c {
		if !unicode.IsSpace(r) {
			chars++
		}
	}
	return c, chars >= api.MinBoardQuoteChars
}

// QuoteIn reports whether quote, within the limits of CleanQuote, is
// found verbatim in text (a member's stored plain text, whole). quote is
// the one the assistant sent, before CleanQuote: a URL in it then matches
// the same URL in the text (CleanQuote's result has lost it).
func QuoteIn(text, quote string) bool {
	if _, ok := CleanQuote(quote); !ok {
		return false
	}
	return strings.Contains(normalizeQuoted(text), normalizeQuoted(quote))
}

// QuoteInOwnText reports whether quote (as for QuoteIn), within the limits
// of CleanQuote, is found verbatim in OwnText(m), the own text of one of
// the user's messages (Text and, for a message with HTML, OwnText set as
// for the rules). A quote that is found only in the quoted history, or
// that runs into it, is not.
func QuoteInOwnText(m Member, quote string) bool {
	return QuoteIn(OwnText(m), quote)
}

// DueInRange reports whether a deadline lies within the range of the
// message it was found in: from api.BoardDueEarliest (a day before) to
// api.BoardDueLatest (400 days after) messageDate, which callers pass as
// the message's arrival (Arrival), never its forgeable Date header.
func DueInRange(due, messageDate time.Time) bool {
	return !due.Before(messageDate.Add(api.BoardDueEarliest)) && !due.After(messageDate.Add(api.BoardDueLatest))
}

// quoteFolds are the typographic characters folded to ASCII on both sides
// of the check.
var quoteFolds = strings.NewReplacer(
	"\u2018", "'", "\u2019", "'", "\u201A", "'", "\u201B", "'", "\u2032", "'", "\u02BC", "'",
	"\u201C", "\"", "\u201D", "\"", "\u201E", "\"", "\u201F", "\"", "\u2033", "\"",
)

// urlPlaceholder stands for a removed URL on both sides of the check;
// one in the input is dropped first, so it cannot be forged.
const urlPlaceholder = "\uFFFC"

// normalizeQuoted is the normalisation both sides of the check share.
func normalizeQuoted(s string) string {
	c := strings.ReplaceAll(CleanText(s), urlPlaceholder, "")
	c = stripURLs(c, urlPlaceholder)
	c = quoteFolds.Replace(norm.NFC.String(c))
	return strings.Join(strings.Fields(c), " ")
}
