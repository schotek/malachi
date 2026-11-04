// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// The OOXML container (Open Packaging Conventions over ZIP), read as
// hostile input. Nothing the archive says about itself is trusted: the
// number of entries is capped, every part is decompressed through a
// counter that refuses the document past MaxEntryBytes for the part and
// MaxExpandBytes for all parts read (the sizes in the ZIP headers are never
// used for that), an encrypted entry or a compression method other than
// store and deflate refuses the document, entry names that are not plain
// relative paths or that name the same part twice (OPC compares part names
// case-insensitively, ASCII only) make it damaged, and a relationship is
// followed only to a part inside the package: an external target or one
// that climbs out of the package is never followed.

// The signatures the container check looks for.
var (
	zipSignature = []byte("PK\x03\x04")
	// cfbSignature starts an OLE2 compound file: a password-protected
	// OOXML document (an EncryptedPackage stream) or an older .doc/.xls.
	cfbSignature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
)

// The ZIP general purpose flags that mean an encrypted entry: traditional
// encryption, strong encryption, an encrypted central directory.
const zipEncryptionFlags = 0x0001 | 0x0040 | 0x2000

// The main content types, and the relationship type of the main part.
const (
	ctDocxMain = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
	ctXlsxMain = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"
	ctXlsb     = "application/vnd.ms-excel.sheet.binary.macroenabled.main"

	relOfficeDocument = "officeDocument"
)

// The relationship type URIs are a prefix, transitional or strict, and a
// name (relType).
var relTypePrefixes = []string{
	"http://schemas.openxmlformats.org/officeDocument/2006/relationships/",
	"http://purl.oclc.org/ooxml/officeDocument/relationships/",
}

// The keys of the parts every package has.
const (
	contentTypesKey = "[content_types].xml"
	packageRelsKey  = "_rels/.rels"
)

// budget is what one document may cost, shared by all its parts: the
// decompressed bytes and the XML tokens read so far, and the context that
// ends the reading.
type budget struct {
	ctx      context.Context
	lim      Limits
	expanded int64
	tokens   int
}

// opcPackage is an opened OOXML package.
type opcPackage struct {
	b     *budget
	parts map[string]*zip.File // by partKey; directories left out
	types contentTypes
}

// openPackage checks the container of an OOXML document and opens it: an
// OLE2 compound file is officeCFB, anything not starting like a ZIP is
// wrongFormat, and the archive must pass every structural check before a
// single part is read.
func openPackage(ctx context.Context, data []byte, lim Limits) (*opcPackage, error) {
	switch {
	case bytes.HasPrefix(data, cfbSignature):
		return nil, refuse(OfficeCFB)
	case !bytes.HasPrefix(data, zipSignature):
		return nil, refuse(WrongFormat)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, refuse(Damaged)
	}
	if len(zr.File) > lim.MaxZipEntries {
		return nil, refuse(TooManyParts)
	}
	p := &opcPackage{
		b:     &budget{ctx: ctx, lim: lim},
		parts: make(map[string]*zip.File, len(zr.File)),
	}
	for _, f := range zr.File {
		if f.Flags&zipEncryptionFlags != 0 {
			return nil, &Refusal{Code: Unsupported, What: WhatZipEncryption}
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, &Refusal{Code: Unsupported, What: WhatCompressionMethod}
		}
		name, dir, ok := entryName(f.Name)
		if !ok {
			return nil, refuse(Damaged)
		}
		if dir {
			continue
		}
		key := partKey(name)
		if p.parts[key] != nil {
			return nil, refuse(Damaged)
		}
		p.parts[key] = f
	}
	return p, nil
}

// entryName checks the name of a ZIP entry: a relative path of non-empty
// segments, none of them "." or "..", without backslashes or NUL. A name
// ending in "/" is a directory, which holds no part.
func entryName(name string) (string, bool, bool) {
	dir := strings.HasSuffix(name, "/")
	if dir {
		name = strings.TrimSuffix(name, "/")
	}
	if name == "" || strings.ContainsAny(name, "\\\x00") || name[0] == '/' {
		return "", false, false
	}
	for seg := range strings.SplitSeq(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", false, false
		}
	}
	return name, dir, true
}

// partKey is the name a part is found by: the part name without a leading
// slash, its percent-escapes decoded (when they all are valid and none
// stands for a slash, a backslash or NUL), ASCII letters lower-cased. Part
// names compare case-insensitively in OPC, for ASCII only; nothing else is
// folded.
func partKey(name string) string {
	name = strings.TrimPrefix(name, "/")
	if dec, ok := percentDecode(name); ok {
		name = dec
	}
	return asciiLower(name)
}

// percentDecode decodes the %HH escapes of s. It fails on a malformed
// escape and on one that would make a separator or NUL.
func percentDecode(s string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, true
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", false
		}
		hi, ok1 := unhex(s[i+1])
		lo, ok2 := unhex(s[i+2])
		if !ok1 || !ok2 {
			return "", false
		}
		c := hi<<4 | lo
		if c == '/' || c == '\\' || c == 0 {
			return "", false
		}
		b.WriteByte(c)
		i += 2
	}
	return b.String(), true
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// asciiLower lower-cases the ASCII letters of s and nothing else (never
// strings.ToLower: the Kelvin sign is not a k).
func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if c := b[j]; c >= 'A' && c <= 'Z' {
					b[j] = c + 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// has reports whether the package has a part with this key.
func (p *opcPackage) has(key string) bool { return p.parts[key] != nil }

// open opens a part for reading through the package's caps. A part that
// does not exist is errNoPart.
func (p *opcPackage) open(key string) (io.ReadCloser, error) {
	f := p.parts[key]
	if f == nil {
		return nil, errNoPart
	}
	rc, err := f.Open()
	if err != nil {
		return nil, refuse(Damaged)
	}
	return &capReader{b: p.b, rc: rc, left: p.b.lim.MaxEntryBytes}, nil
}

// errNoPart is a part that is not in the package.
var errNoPart = errors.New("extract: no such part")

// capReader counts the decompressed bytes of one part against its own cap
// and the document's; one byte past either refuses the document. A read
// error of the archive (a bad checksum, broken deflate data, a size that
// disagrees with the header) makes it damaged.
type capReader struct {
	b    *budget
	rc   io.ReadCloser
	left int64
	err  error
}

func (r *capReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	left := max(min(r.left, r.b.lim.MaxExpandBytes-r.b.expanded), 0)
	if int64(len(p)) > left+1 {
		p = p[:left+1]
	}
	n, err := r.rc.Read(p)
	if int64(n) > left {
		r.err = refuse(Expands)
		return 0, r.err
	}
	r.left -= int64(n)
	r.b.expanded += int64(n)
	switch {
	case err == io.EOF:
		r.err = io.EOF
	case err != nil:
		r.err = refuse(Damaged)
	}
	return n, r.err
}

func (r *capReader) Close() error { return r.rc.Close() }

// scan opens a part as XML.
func (p *opcPackage) scan(key string) (*xmlScanner, func(), error) {
	rc, err := p.open(key)
	if err != nil {
		return nil, nil, err
	}
	s, err := newXMLScanner(rc, p.b)
	if err != nil {
		_ = rc.Close()
		return nil, nil, err
	}
	return s, func() { _ = rc.Close() }, nil
}

// --- content types -----------------------------------------------------------

// contentTypes is [Content_Types].xml: a content type per part name
// (Override) and per extension (Default).
type contentTypes struct {
	overrides map[string]string // by partKey
	defaults  map[string]string // by lower-case extension
}

// readContentTypes reads [Content_Types].xml. A package without one is not
// OPC at all: some other ZIP.
func (p *opcPackage) readContentTypes() error {
	if !p.has(contentTypesKey) {
		return &Refusal{Code: WrongFormat, What: WhatOtherZip}
	}
	s, done, err := p.scan(contentTypesKey)
	if err != nil {
		return err
	}
	defer done()
	root, err := s.root()
	if err != nil {
		return err
	}
	if root.Name.Space != nsContentTypes || root.Name.Local != "Types" {
		return refuse(Damaged)
	}
	p.types = contentTypes{overrides: map[string]string{}, defaults: map[string]string{}}
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return err
		}
		if se.Name.Space == nsContentTypes {
			ct, _ := attr(*se, "ContentType", "")
			switch se.Name.Local {
			case "Override":
				name, _ := attr(*se, "PartName", "")
				key := partKey(name)
				if _, dup := p.types.overrides[key]; dup {
					return refuse(Damaged)
				}
				p.types.overrides[key] = ct
			case "Default":
				ext, _ := attr(*se, "Extension", "")
				ext = asciiLower(ext)
				if _, dup := p.types.defaults[ext]; dup {
					return refuse(Damaged)
				}
				p.types.defaults[ext] = ct
			}
		}
		if err := s.skip(); err != nil {
			return err
		}
	}
}

// contentType is the content type of a part: its Override, else the
// Default of its extension.
func (p *opcPackage) contentType(key string) string {
	if ct, ok := p.types.overrides[key]; ok {
		return ct
	}
	base := key[strings.LastIndexByte(key, '/')+1:]
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		return p.types.defaults[base[i+1:]]
	}
	return ""
}

// --- relationships -----------------------------------------------------------

// rel is one relationship of a part.
type rel struct {
	id, typ string
	// key is the target part's key, or "" when the target is external,
	// climbs out of the package or is not a plain path: such a target is
	// never followed.
	key string
}

// rels are the relationships of one part, in document order.
type rels struct {
	list []rel
	byID map[string]int
}

// target is the part a relationship id points to, if it is in the package.
func (r rels) target(id string) (rel, bool) {
	i, ok := r.byID[id]
	if !ok {
		return rel{}, false
	}
	return r.list[i], r.list[i].key != ""
}

// first is the first relationship of a type that points into the package.
func (r rels) first(typ string) (string, bool) {
	for _, l := range r.list {
		if l.typ == typ && l.key != "" {
			return l.key, true
		}
	}
	return "", false
}

// relsKey is the key of the relationships part of a part ("" is the
// package).
func relsKey(source string) string {
	dir, base := "", source
	if i := strings.LastIndexByte(source, '/'); i >= 0 {
		dir, base = source[:i+1], source[i+1:]
	}
	return dir + "_rels/" + base + ".rels"
}

// readRels reads the relationships of a part ("" for the package's own).
// A part without a relationships part has none. Two relationships with one
// id make the package damaged.
func (p *opcPackage) readRels(source string) (rels, error) {
	r := rels{byID: map[string]int{}}
	key := relsKey(source)
	if !p.has(key) {
		return r, nil
	}
	s, done, err := p.scan(key)
	if err != nil {
		return r, err
	}
	defer done()
	root, err := s.root()
	if err != nil {
		return r, err
	}
	if root.Name.Space != nsPackageRels || root.Name.Local != "Relationships" {
		return r, refuse(Damaged)
	}
	for {
		se, err := s.child()
		if err != nil || se == nil {
			return r, err
		}
		if se.Name.Space == nsPackageRels && se.Name.Local == "Relationship" {
			id, _ := attr(*se, "Id", "")
			if _, dup := r.byID[id]; dup {
				return r, refuse(Damaged)
			}
			typ, _ := attr(*se, "Type", "")
			mode, _ := attr(*se, "TargetMode", "")
			target, _ := attr(*se, "Target", "")
			l := rel{id: id, typ: relType(typ)}
			if !strings.EqualFold(mode, "External") {
				l.key = resolveTarget(source, target)
			}
			r.byID[id] = len(r.list)
			r.list = append(r.list, l)
		}
		if err := s.skip(); err != nil {
			return r, err
		}
	}
}

// relType is the name of a relationship type in the transitional or the
// strict namespace ("officeDocument", "worksheet", …), "" for any other.
func relType(uri string) string {
	for _, prefix := range relTypePrefixes {
		if len(uri) > len(prefix) && strings.EqualFold(uri[:len(prefix)], prefix) {
			return uri[len(prefix):]
		}
	}
	return ""
}

// resolveTarget resolves the target of an internal relationship against
// the part it belongs to and returns the key of the part it names, or ""
// for a target that is not followed: one with a scheme or an authority
// (an external target not marked as one), a query or a fragment, a
// backslash or NUL, or "..": segments that climb out of the package.
func resolveTarget(source, target string) string {
	if target == "" || strings.ContainsAny(target, "?#\\\x00") || strings.HasPrefix(target, "//") {
		return ""
	}
	if i := strings.IndexByte(target, ':'); i >= 0 && !strings.Contains(target[:i], "/") {
		return ""
	}
	var segs []string
	if !strings.HasPrefix(target, "/") {
		if i := strings.LastIndexByte(source, '/'); i >= 0 {
			segs = strings.Split(source[:i], "/")
		}
	}
	for seg := range strings.SplitSeq(strings.TrimPrefix(target, "/"), "/") {
		switch seg {
		case "", ".":
		case "..":
			if len(segs) == 0 {
				return ""
			}
			segs = segs[:len(segs)-1]
		default:
			segs = append(segs, seg)
		}
	}
	if len(segs) == 0 {
		return ""
	}
	return partKey(strings.Join(segs, "/"))
}

// --- the main part -----------------------------------------------------------

// mainPart opens the package's content types and finds its main part, the
// target of its one officeDocument relationship, which must be of the
// declared format's main content type. Another Office type is wrongFormat
// with what it is (macro-enabled, a template, a presentation, a binary
// workbook); anything else, or a package with no main part, is some other
// ZIP.
func (p *opcPackage) mainPart(f Format) (string, error) {
	if err := p.readContentTypes(); err != nil {
		return "", err
	}
	if !p.has(packageRelsKey) {
		return "", refuse(Damaged)
	}
	pr, err := p.readRels("")
	if err != nil {
		return "", err
	}
	var main []rel
	for _, l := range pr.list {
		if l.typ == relOfficeDocument {
			main = append(main, l)
		}
	}
	switch {
	case len(main) == 0:
		return "", &Refusal{Code: WrongFormat, What: WhatOtherZip}
	case len(main) > 1, main[0].key == "", !p.has(main[0].key):
		return "", refuse(Damaged)
	}
	key := main[0].key
	if what := mainTypeWhat(f, p.contentType(key)); what != WhatNone {
		return "", &Refusal{Code: WrongFormat, What: what}
	}
	return key, nil
}

// mainTypeWhat says what a main part of this content type is when it is
// not the declared format's: WhatNone when it is.
func mainTypeWhat(f Format, ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = asciiLower(strings.TrimSpace(ct))
	switch {
	case f == DOCX && ct == ctDocxMain, f == XLSX && ct == ctXlsxMain:
		return WhatNone
	case ct == ctXlsb:
		return WhatBinaryWorkbook
	case strings.Contains(ct, "presentationml"), strings.HasPrefix(ct, "application/vnd.ms-powerpoint"):
		return WhatPresentation
	case strings.HasPrefix(ct, "application/vnd.") && strings.Contains(ct, "template"):
		return WhatTemplate
	case strings.HasPrefix(ct, "application/vnd.ms-") && strings.Contains(ct, "macroenabled"):
		return WhatMacroEnabled
	}
	return WhatOtherZip
}

// attr is the value of the attribute local in one of the namespaces
// spaces ("" for an unqualified one).
func attr(se xml.StartElement, local string, spaces ...string) (string, bool) {
	for _, a := range se.Attr {
		if a.Name.Local != local {
			continue
		}
		for _, sp := range spaces {
			if a.Name.Space == sp {
				return a.Value, true
			}
		}
	}
	return "", false
}
