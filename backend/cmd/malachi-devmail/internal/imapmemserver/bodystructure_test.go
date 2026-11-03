// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imapmemserver

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

const corpus = "../../../../testdata/mime"

// emptyMultiparts counts the multiparts without parts in bs, also inside
// attached messages (Walk does not enter those).
func emptyMultiparts(bs imap.BodyStructure) int {
	n := 0
	switch bs := bs.(type) {
	case *imap.BodyStructureMultiPart:
		if len(bs.Children) == 0 {
			n++
		}
		for _, child := range bs.Children {
			n += emptyMultiparts(child)
		}
	case *imap.BodyStructureSinglePart:
		if bs.MessageRFC822 != nil {
			n += emptyMultiparts(bs.MessageRFC822.BodyStructure)
		}
	}
	return n
}

// The corpus files go-imap makes an empty multipart of get one empty
// text/plain part each; every other file's structure is go-imap's own.
func TestBodyStructureOfTheCorpus(t *testing.T) {
	broken := map[string]bool{
		"missing-boundary.eml":              true,
		"skeleton-empty-parts.eml":          true,
		"skeleton-rfc822-same-boundary.eml": true,
		"skeleton-unterminated-quote.eml":   true,
	}
	files, err := filepath.Glob(filepath.Join(corpus, "*.eml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus: %v", err)
	}
	for _, path := range files {
		name := filepath.Base(path)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		upstream := imapserver.ExtractBodyStructure(bytes.NewReader(raw))
		if got := emptyMultiparts(upstream) > 0; got != broken[name] {
			t.Errorf("%s: go-imap's structure has an empty multipart: %v, want %v", name, got, broken[name])
		}
		ours := bodyStructure(raw)
		if n := emptyMultiparts(ours); n > 0 {
			t.Errorf("%s: %d empty multiparts left", name, n)
		}
		if !broken[name] && !reflect.DeepEqual(ours, upstream) {
			t.Errorf("%s: the structure changed", name)
		}
	}
}

// The empty part is Dovecot's: text/plain, us-ascii, 7bit, no bytes, with
// the extension data the writer reads.
func TestBodyStructureOfAMultipartWithoutBoundary(t *testing.T) {
	raw := []byte("From: a@example.org\r\nContent-Type: multipart/mixed\r\n\r\n--x\r\nlost\r\n--x--\r\n")
	mp, ok := bodyStructure(raw).(*imap.BodyStructureMultiPart)
	if !ok || mp.Subtype != "mixed" || len(mp.Children) != 1 {
		t.Fatalf("structure %#v", mp)
	}
	part, ok := mp.Children[0].(*imap.BodyStructureSinglePart)
	if !ok || part.Type != "text" || part.Subtype != "plain" || part.Encoding != "7bit" || part.Size != 0 ||
		part.Params["charset"] != "us-ascii" || part.Text == nil || part.Extended == nil {
		t.Errorf("part %#v", part)
	}
}
