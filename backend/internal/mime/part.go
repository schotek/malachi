// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/emersion/go-message"
)

// Sentinel errors of ExtractPart.
var (
	ErrPartNotFound = errors.New("mime: no such part")
	ErrPartTooBig   = errors.New("mime: part exceeds the size cap")
)

// Part is one decoded leaf of a message, as ExtractPart returns it.
type Part struct {
	PartID      string
	ContentType string // normalised type/subtype
	Filename    string // sanitised, as Parse reports it in Attachment
	ContentID   string // without angle brackets
	Inline      bool
	Body        []byte // decoded; at most the maxBytes ExtractPart was given
}

// ExtractPart reads the message from r and returns the decoded body of the
// leaf part with the given IMAP-style number — the same numbering Parse uses
// for Attachment.PartID. It reads at most maxBytes of the part and returns
// ErrPartTooBig beyond that, ErrPartNotFound when no leaf has the number
// (a multipart container is not a part one can fetch), and a wrapped error
// when the message's header cannot be read at all.
func ExtractPart(r io.Reader, wanted string, limits Limits, maxBytes int64) (*Part, error) {
	limits = limits.withDefaults()
	if maxBytes <= 0 {
		maxBytes = limits.MaxTextBytes
	}
	cr := &countingReader{r: &io.LimitedReader{R: r, N: MaxInputBytes}}
	root, err := message.ReadWithOptions(cr, &message.ReadOptions{MaxHeaderBytes: limits.MaxHeaderBytes})
	if root == nil {
		if err == nil {
			err = errors.New("no entity")
		}
		return nil, fmt.Errorf("mime: read header: %w", err)
	}
	if cr.n == 0 {
		// go-message accepts empty input as an empty message; Parse rejects
		// it and so does this.
		return nil, errors.New("mime: read header: empty input")
	}

	var found *Part
	var tooBig bool
	parts := 0
	visit := func(path []int, e *message.Entity, _ error) error {
		parts++
		if parts > limits.MaxParts || len(path) > limits.MaxDepth {
			return errStop
		}
		mediaType, params, _ := e.Header.ContentType()
		if strings.HasPrefix(mediaType, "multipart/") {
			return nil
		}
		id := partID(path)
		if id != wanted {
			// Every leaf body must be consumed for the multipart reader to
			// reach the next boundary.
			_, _ = io.Copy(io.Discard, e.Body)
			return nil
		}
		ct := normalizeMediaType(mediaType)
		disp, dparams, _ := e.Header.ContentDisposition()
		disp = strings.ToLower(strings.TrimSpace(disp))
		body, rerr := io.ReadAll(io.LimitReader(e.Body, maxBytes+1))
		if rerr != nil {
			return fmt.Errorf("part %s: %w", id, rerr)
		}
		if int64(len(body)) > maxBytes {
			tooBig = true
			return errStop
		}
		contentID := cleanID(strings.Trim(strings.TrimSpace(e.Header.Get("Content-Id")), "<>"), limits.MaxFieldBytes)
		found = &Part{
			PartID:      id,
			ContentType: ct,
			Filename:    attachmentName(id, ct, params, dparams),
			ContentID:   contentID,
			Inline:      contentID != "" && disp != "attachment",
			Body:        body,
		}
		return errStop
	}
	if werr := root.Walk(visit); werr != nil && !errors.Is(werr, errStop) {
		return nil, fmt.Errorf("mime: %w", werr)
	}
	switch {
	case tooBig:
		return nil, ErrPartTooBig
	case found == nil:
		return nil, ErrPartNotFound
	}
	return found, nil
}

// ValidPartID reports whether s has the shape of a part number ("1",
// "2.1", …), so a caller can reject junk before opening a message.
func ValidPartID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, seg := range strings.Split(s, ".") {
		if seg == "" || seg[0] == '0' && len(seg) > 1 {
			return false
		}
		for _, r := range seg {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}
