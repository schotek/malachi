// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package smime

import (
	"errors"
	"fmt"
)

// S/MIME producers often encode CMS in BER (indefinite lengths, an
// eContent split into a constructed OCTET STRING); cryptobyte reads DER
// only. toDER rewrites the encoding without interpreting it: every length
// becomes definite and minimal, every constructed OCTET STRING primitive
// (its pieces concatenated), every primitive's content is copied as it is.
// A DER input comes back byte for byte, so the signed attributes, which
// the signature covers as transmitted, keep their bytes.

// errLimit marks an input over a limit of the parser (depth, size,
// count); the verdict is error/limit, not invalid.
var errLimit = errors.New("smime: over a limit")

// errMalformed marks broken ASN.1 or CMS structure.
var errMalformed = errors.New("smime: malformed")

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{errMalformed}, args...)...)
}

// maxBERNodes caps the elements one conversion builds (a certificate has
// about a hundred).
const maxBERNodes = 100000

// berNode is one converted element: a primitive's content (pieces of the
// input, several for a constructed OCTET STRING made primitive) or a
// constructed element's children.
type berNode struct {
	tag      []byte
	pieces   [][]byte
	children []*berNode
	length   int // content length in DER, set by size
}

// berReader walks one BER buffer.
type berReader struct {
	in       []byte
	maxDepth int
	maxOut   int
	nodes    int
}

// toDER converts a single BER element (the whole of in, trailing bytes are
// malformed) to DER.
func toDER(in []byte, maxDepth, maxOut int) ([]byte, error) {
	r := &berReader{in: in, maxDepth: maxDepth, maxOut: maxOut}
	n, used, err := r.element(0, 0)
	if err != nil {
		return nil, err
	}
	if used != len(in) {
		return nil, malformed("%d trailing bytes", len(in)-used)
	}
	total := n.size()
	if total > maxOut {
		return nil, errLimit
	}
	out := make([]byte, 0, total)
	return n.write(out), nil
}

// header is a parsed identifier and length.
type header struct {
	tag         []byte // identifier octets as read
	constructed bool
	universal   uint64 // tag number when class universal, else ^0
	indefinite  bool
	length      int
	hdrLen      int
}

func (r *berReader) header(pos int) (header, error) {
	var h header
	in := r.in
	if pos >= len(in) {
		return h, malformed("truncated identifier")
	}
	start := pos
	b := in[pos]
	pos++
	h.constructed = b&0x20 != 0
	class := b >> 6
	num := uint64(b & 0x1f)
	if num == 0x1f {
		num = 0
		for i := 0; ; i++ {
			if pos >= len(in) || i >= 4 {
				return h, malformed("bad high tag")
			}
			c := in[pos]
			pos++
			num = num<<7 | uint64(c&0x7f)
			if c&0x80 == 0 {
				break
			}
		}
	}
	h.tag = in[start:pos]
	h.universal = ^uint64(0)
	if class == 0 {
		h.universal = num
	}
	if pos >= len(in) {
		return h, malformed("truncated length")
	}
	l := in[pos]
	pos++
	switch {
	case l < 0x80:
		h.length = int(l)
	case l == 0x80:
		if !h.constructed {
			return h, malformed("indefinite length on a primitive")
		}
		h.indefinite = true
	default:
		n := int(l & 0x7f)
		if n > 4 {
			return h, errLimit
		}
		if pos+n > len(in) {
			return h, malformed("truncated length")
		}
		v := 0
		for i := 0; i < n; i++ {
			v = v<<8 | int(in[pos+i])
		}
		pos += n
		if v < 0 {
			return h, errLimit
		}
		h.length = v
	}
	h.hdrLen = pos - start
	if !h.indefinite && h.length > len(in)-pos {
		return h, malformed("length past the end")
	}
	return h, nil
}

// element converts the element at pos and returns its node and the number
// of input bytes it spanned.
func (r *berReader) element(pos, depth int) (*berNode, int, error) {
	if depth > r.maxDepth {
		return nil, 0, errLimit
	}
	r.nodes++
	if r.nodes > maxBERNodes {
		return nil, 0, errLimit
	}
	h, err := r.header(pos)
	if err != nil {
		return nil, 0, err
	}
	body := pos + h.hdrLen
	if !h.constructed {
		return &berNode{tag: h.tag, pieces: [][]byte{r.in[body : body+h.length]}}, h.hdrLen + h.length, nil
	}
	end := len(r.in)
	if !h.indefinite {
		end = body + h.length
	}
	node := &berNode{tag: h.tag}
	p := body
	for {
		if h.indefinite {
			if p+2 > len(r.in) {
				return nil, 0, malformed("missing end of contents")
			}
			if r.in[p] == 0 && r.in[p+1] == 0 {
				p += 2
				break
			}
		} else if p >= end {
			break
		}
		child, n, err := r.element(p, depth+1)
		if err != nil {
			return nil, 0, err
		}
		if !h.indefinite && p+n > end {
			return nil, 0, malformed("child past its parent")
		}
		node.children = append(node.children, child)
		p += n
	}
	if h.universal == 4 { // constructed OCTET STRING → primitive
		var pieces [][]byte
		for _, c := range node.children {
			if len(c.tag) != 1 || c.tag[0] != 0x04 {
				return nil, 0, malformed("constructed OCTET STRING with a non-OCTET STRING piece")
			}
			pieces = append(pieces, c.pieces...)
		}
		return &berNode{tag: []byte{0x04}, pieces: pieces}, p - pos, nil
	}
	return node, p - pos, nil
}

// size computes the DER length of the node (header included) and records
// its content length.
func (n *berNode) size() int {
	c := 0
	if n.children == nil {
		for _, p := range n.pieces {
			c += len(p)
		}
	} else {
		for _, ch := range n.children {
			c += ch.size()
		}
	}
	n.length = c
	return len(n.tag) + lengthLen(c) + c
}

func (n *berNode) write(out []byte) []byte {
	out = append(out, n.tag...)
	out = appendLength(out, n.length)
	if n.children == nil {
		for _, p := range n.pieces {
			out = append(out, p...)
		}
		return out
	}
	for _, ch := range n.children {
		out = ch.write(out)
	}
	return out
}

func lengthLen(n int) int {
	switch {
	case n < 0x80:
		return 1
	case n <= 0xff:
		return 2
	case n <= 0xffff:
		return 3
	case n <= 0xffffff:
		return 4
	default:
		return 5
	}
}

func appendLength(out []byte, n int) []byte {
	switch {
	case n < 0x80:
		return append(out, byte(n))
	case n <= 0xff:
		return append(out, 0x81, byte(n))
	case n <= 0xffff:
		return append(out, 0x82, byte(n>>8), byte(n))
	case n <= 0xffffff:
		return append(out, 0x83, byte(n>>16), byte(n>>8), byte(n))
	default:
		return append(out, 0x84, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
}
