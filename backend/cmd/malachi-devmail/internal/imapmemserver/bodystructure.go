// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// This package is go-imap's in-memory server (LICENSE, MIT) with one
// change, in message.go's fetch: BODYSTRUCTURE comes from bodyStructure
// below. go-imap's writer panics on a multipart without a single part
// ("imap.BodyStructureMultiPart must have at least one child"), and its
// server answers a panic by closing the connection, which a client sees
// only as an unexpected EOF; ExtractBodyStructure makes such a multipart
// out of the deliberately broken files of backend/testdata/mime that the
// Windows UI tests seed (missing-boundary, skeleton-empty-parts,
// skeleton-rfc822-same-boundary, skeleton-unterminated-quote). There is
// no hook for it outside the package: the message type and the mailbox
// view that fetches it are unexported. Everything else is upstream's
// (but for a keyed mail.Header literal in message.go's search, for go
// vet), so that a newer go-imap can be copied over again with these two
// lines.

package imapmemserver

import (
	"bytes"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

// bodyStructure is imapserver.ExtractBodyStructure of a message, except
// that a multipart without parts (no boundary, a broken boundary
// parameter, a closing delimiter right away, an inner message cut off by
// the outer boundary) gets one empty text/plain part, as Dovecot
// describes it. The message's bytes are served unchanged.
func bodyStructure(buf []byte) imap.BodyStructure {
	bs := imapserver.ExtractBodyStructure(bytes.NewReader(buf))
	fillEmptyMultiparts(bs)
	return bs
}

// fillEmptyMultiparts gives every empty multipart of bs, also inside an
// attached message, its empty part.
func fillEmptyMultiparts(bs imap.BodyStructure) {
	switch bs := bs.(type) {
	case *imap.BodyStructureMultiPart:
		if len(bs.Children) == 0 {
			bs.Children = []imap.BodyStructure{emptyTextPart()}
		}
		for _, child := range bs.Children {
			fillEmptyMultiparts(child)
		}
	case *imap.BodyStructureSinglePart:
		if msg := bs.MessageRFC822; msg != nil && msg.BodyStructure != nil {
			fillEmptyMultiparts(msg.BodyStructure)
		}
	}
}

// emptyTextPart is Dovecot's EMPTY_BODYSTRUCTURE: ("text" "plain"
// ("charset" "us-ascii") NIL NIL "7bit" 0 0 NIL NIL NIL NIL). The writer
// reads Extended of every single part of an extended structure.
func emptyTextPart() *imap.BodyStructureSinglePart {
	return &imap.BodyStructureSinglePart{
		Type:     "text",
		Subtype:  "plain",
		Params:   map[string]string{"charset": "us-ascii"},
		Encoding: "7bit",
		Text:     &imap.BodyStructureText{},
		Extended: &imap.BodyStructureSinglePartExt{},
	}
}
