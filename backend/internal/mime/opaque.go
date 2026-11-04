// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"bytes"
	"io"
	"strings"

	"github.com/emersion/go-message"

	"github.com/schotek/malachi/backend/internal/smime"
)

// Opaque S/MIME signed data (application/pkcs7-mime;
// smime-type=signed-data) is not encrypted, only wrapped: the parser
// unwraps it and shows what is inside (docs/plans/signatures.md S14).
// Nothing is verified here; the CMS is read without trusting it, within
// internal/smime's limits.
//
// Where: an opaque leaf that is the root of the message, or the first part
// of a root multipart/mixed (a list's footer after it, plan S4), and the
// same again inside what was unwrapped, at most maxOpaqueDepth wrappers
// deep. Numbering: the parts inside a wrapper with part number N are
// numbered as the parts of a message of their own, prefixed with "N." —
// so the inner text of a root wrapper is "1.1", the second inner part of
// a multipart content "1.2", and the parts of a wrapper in a mixed "1.1",
// "1.2"… These numbers never collide with the wrapper's siblings (the
// wrapper is a leaf, nothing else is numbered under it), stay valid for
// ValidPartID, and ExtractPart reads them the same way. The wrapper
// itself is no attachment once unwrapped.

// maxOpaqueDepth caps nested wrappers.
const maxOpaqueDepth = 3

// walkFunc is called for every entity of the walk with its part number
// and nesting depth (wrappers count one level each).
type walkFunc func(id string, depth int, e *message.Entity, err error) error

// walk is message.Entity.Walk with IMAP-style numbering that descends into
// opaque signed data; opened is called for each wrapper it unwrapped.
func walk(root *message.Entity, limits Limits, opened func(), fn walkFunc) error {
	return walkLevel(root, "", 0, 0, limits, opened, fn)
}

func walkLevel(root *message.Entity, prefix string, depthBase, wrappers int, limits Limits, opened func(), fn walkFunc) error {
	rootType := ""
	return root.Walk(func(path []int, e *message.Entity, err error) error {
		id := partID(path)
		if prefix != "" {
			id = prefix + "." + id
		}
		depth := depthBase + len(path)
		mediaType, params, _ := e.Header.ContentType()
		if len(path) == 0 {
			rootType = strings.ToLower(mediaType)
		}
		if strings.HasPrefix(mediaType, "multipart/") || wrappers >= maxOpaqueDepth ||
			!opaquePosition(path, rootType) || !isOpaqueSigned(mediaType, params) || depth+1 > limits.MaxDepth {
			return fn(id, depth, e, err)
		}
		data, rerr := io.ReadAll(io.LimitReader(e.Body, MaxInputBytes+1))
		if rerr == nil && len(data) <= MaxInputBytes {
			if content, xerr := smime.ExtractContent(data); xerr == nil {
				// An unknown charset or encoding of the inner root comes
				// back with the entity; its body is read as it is.
				inner, _ := message.ReadWithOptions(bytes.NewReader(content), &message.ReadOptions{MaxHeaderBytes: limits.MaxHeaderBytes})
				if inner != nil && len(content) > 0 {
					if opened != nil {
						opened()
					}
					return walkLevel(inner, id, depth+1, wrappers+1, limits, opened, fn)
				}
			}
		}
		// Not unwrapped: the wrapper is an attachment like any other leaf.
		e.Body = bytes.NewReader(data)
		return fn(id, depth, e, err)
	})
}

// opaquePosition: the root of a walk level, or the first part of a root
// multipart/mixed.
func opaquePosition(path []int, rootType string) bool {
	switch len(path) {
	case 0:
		return true
	case 1:
		return path[0] == 0 && rootType == "multipart/mixed"
	}
	return false
}

// isOpaqueSigned: application/pkcs7-mime (or the x- form) with
// smime-type signed-data, or without smime-type (old producers; enveloped
// data then fails to unwrap and stays an attachment).
func isOpaqueSigned(mediaType string, params map[string]string) bool {
	t, _, _ := strings.Cut(mediaType, ";")
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "application/pkcs7-mime", "application/x-pkcs7-mime":
	default:
		return false
	}
	st := strings.ToLower(strings.TrimSpace(params["smime-type"]))
	return st == "" || st == "signed-data"
}
