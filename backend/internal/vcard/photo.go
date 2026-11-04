// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package vcard

import (
	"encoding/base64"
	"strings"
)

// Photo is the picture embedded in a card's first PHOTO: its bytes as the
// card carries them and the media type the card declares for them ("" when
// it declares none, lower case otherwise). Neither is checked here: the
// bytes are a third party's image data, hostile input for whoever decodes
// them (internal/contactphoto, docs/plans/address-book.md F5); the type is
// only a hint.
type Photo struct {
	MIME string
	Data []byte
}

// MaxPhotoLineBytes caps the unfolded PHOTO line that is decoded: the
// limit of the whole card, where every other property has MaxLineBytes.
const MaxPhotoLineBytes = MaxCardBytes

// photoOf reads one PHOTO line (docs/plans/address-book.md F2, F4): an
// embedded picture — vCard 2.1 and 3.0 ENCODING=b / BASE64 with TYPE=JPEG,
// PNG or GIF, vCard 4.0 (and 3.0 written so) a data:image/…;base64, URI —
// or uri true for a picture named by a link (VALUE=uri, http:, https: or
// any other URI), which is never fetched. A value that is neither, or
// whose base64 cannot be read, is no photo at all.
func photoOf(p prop) (ph *Photo, uri bool) {
	if len(p.value) > MaxPhotoLineBytes {
		return nil, false
	}
	switch p.encoding() {
	case "B", "BASE64":
		data, ok := decodeBase64(p.value)
		if !ok || len(data) == 0 {
			return nil, false
		}
		return &Photo{MIME: photoMIME(p), Data: data}, false
	}
	v := strings.TrimSpace(p.value)
	if v == "" {
		return nil, false
	}
	if len(v) >= 5 && strings.EqualFold(v[:5], "data:") {
		return dataURIPhoto(v[5:])
	}
	if vt, ok := p.paramValue("VALUE"); ok && (strings.EqualFold(vt, "uri") || strings.EqualFold(vt, "url")) {
		return nil, true
	}
	if i := strings.IndexByte(v, ':'); i > 0 && isScheme(v[:i]) {
		return nil, true
	}
	return nil, false
}

// isScheme reports whether s is a URI scheme (RFC 3986 §3.1).
func isScheme(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return s != ""
}

// dataURIPhoto reads the rest of a data: URI ("image/png;base64,…"); only
// a base64 one is a photo, of the media type it names.
func dataURIPhoto(rest string) (*Photo, bool) {
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return nil, false
	}
	header, body := rest[:comma], rest[comma+1:]
	parts := strings.Split(header, ";")
	base64ed := false
	for _, part := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			base64ed = true
		}
	}
	if !base64ed {
		return nil, false
	}
	data, ok := decodeBase64(body)
	if !ok || len(data) == 0 {
		return nil, false
	}
	return &Photo{MIME: cleanMIME(parts[0]), Data: data}, false
}

// photoMIME is the media type a PHOTO declares: TYPE=JPEG (or a bare 2.1
// JPEG), TYPE=image/jpeg, MEDIATYPE=image/jpeg; "" when none.
func photoMIME(p prop) string {
	if mt, ok := p.paramValue("MEDIATYPE"); ok {
		if m := cleanMIME(mt); m != "" {
			return m
		}
	}
	for _, t := range p.types() {
		switch t {
		case "jpeg", "jpg", "pjpeg":
			return "image/jpeg"
		case "png":
			return "image/png"
		case "gif":
			return "image/gif"
		}
		if strings.Contains(t, "/") {
			return cleanMIME(t)
		}
		if m := cleanMIME("image/" + t); m != "" {
			return m
		}
	}
	return ""
}

// cleanMIME is a media type in lower case when it looks like one
// (type/subtype of token characters, ≤ 64 bytes), else "".
func cleanMIME(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	slash := strings.IndexByte(s, '/')
	if len(s) > 64 || slash <= 0 || slash == len(s)-1 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '/' && i == slash || strings.IndexByte("+-.!#$&^_", c) >= 0) {
			return ""
		}
	}
	return s
}

// decodeBase64 reads standard base64 leniently: whitespace anywhere is
// dropped, padding may be missing. Any other character is a failure.
func decodeBase64(s string) ([]byte, bool) {
	clean := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n', '\f', '\v':
			return -1
		}
		return r
	}, s)
	b, err := base64.StdEncoding.DecodeString(clean)
	if err == nil {
		return b, true
	}
	b, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(clean, "="))
	if err != nil {
		return nil, false
	}
	return b, true
}
