// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package signature

import (
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"
	"golang.org/x/text/unicode/norm"

	"github.com/schotek/malachi/backend/internal/board"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Findings is what a verifier found, in the classes of plan §3, plus what
// the identity check needs. Compose orders them into the verdict.
type Findings struct {
	Kind api.SignatureKind
	// Verified: the signature matches the content and the key.
	Verified    bool
	Invalid     api.SignatureReason
	Untrusted   api.SignatureReason
	Weak        api.SignatureReason
	Unsupported api.SignatureReason
	Error       api.SignatureReason
	Revoked     bool
	UnknownKey  bool
	// Seen: OpenPGP, the key is only seen (TOFU), not confirmed.
	Seen bool
	// Identities are the signer's addresses (the certificate's or the
	// key's user IDs'); From the message's From addresses.
	Identities []string
	From       []string
	Notes      []api.SignatureNote
}

// Verdict is Compose's result.
type Verdict struct {
	Status api.SignatureStatus
	Reason api.SignatureReason
	Notes  []api.SignatureNote
	// Matched is the identity that equals the From address ("" for none).
	Matched string
}

// Compose applies plan §3: the first status that holds, in the order
// invalid, revoked, mismatch, untrusted, unknownKey, weak, unsupported,
// error, valid / validSeen. Mismatch needs a signature that matched; the
// identity is the From address only when the message has exactly one From
// address and it equals one of the signer's (plan S5).
func Compose(f Findings) Verdict {
	v := Verdict{Notes: SortNotes(f.Notes)}
	if f.Verified {
		v.Matched = matchIdentity(f.Identities, f.From)
	}
	switch {
	case f.Invalid != "":
		v.Status, v.Reason = api.SignatureInvalid, f.Invalid
	case f.Revoked && f.Verified:
		v.Status = api.SignatureRevoked
	case f.Verified && v.Matched == "":
		v.Status = api.SignatureMismatch
	case f.Untrusted != "":
		v.Status, v.Reason = api.SignatureUntrusted, f.Untrusted
	case f.UnknownKey:
		v.Status = api.SignatureUnknownKey
	case f.Weak != "" && f.Verified:
		v.Status, v.Reason = api.SignatureWeak, f.Weak
	case f.Unsupported != "":
		v.Status, v.Reason = api.SignatureUnsupported, f.Unsupported
	case f.Error != "":
		v.Status, v.Reason = api.SignatureError, f.Error
	case f.Verified && f.Seen:
		v.Status = api.SignatureValidSeen
	case f.Verified:
		v.Status = api.SignatureValid
	default:
		v.Status, v.Reason = api.SignatureError, api.SignatureReasonInternal
	}
	return v
}

// noteOrder is the order of SignatureSummary.Notes (the enum's).
var noteOrder = map[api.SignatureNote]int{
	api.SignatureNoteExpiredSince:           0,
	api.SignatureNoteRevocationUnchecked:    1,
	api.SignatureNoteRevocationGood:         2,
	api.SignatureNoteRevokedAfter:           3,
	api.SignatureNotePartialFooter:          4,
	api.SignatureNoteProtectedHeadersDiffer: 5,
	api.SignatureNoteInlinePGP:              6,
	api.SignatureNoteKeyChanged:             7,
	api.SignatureNoteOpaque:                 8,
}

// SortNotes returns the notes deduplicated in the enum's order (nil for
// none).
func SortNotes(notes []api.SignatureNote) []api.SignatureNote {
	seen := map[api.SignatureNote]bool{}
	var out []api.SignatureNote
	for _, n := range notes {
		if _, known := noteOrder[n]; known && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return noteOrder[out[i]] < noteOrder[out[j]] })
	return out
}

// matchIdentity returns the signer's identity that is the message's only
// From address.
func matchIdentity(ids, from []string) string {
	if len(from) != 1 {
		return ""
	}
	want := NormalizeAddress(from[0])
	if want == "" {
		return ""
	}
	for _, id := range ids {
		if NormalizeAddress(id) == want {
			return id
		}
	}
	return ""
}

// NormalizeAddress is the form addresses are compared in (plan S5): NFC,
// trimmed, lower case — the domain always, and the local part too (as
// built: no mail system in use distinguishes it, and a case difference
// must not turn a valid signature yellow). "" for a string without "@".
func NormalizeAddress(s string) string {
	s = strings.TrimSpace(norm.NFC.String(s))
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || at == len(s)-1 || strings.ContainsAny(s, " \t\r\n<>") {
		return ""
	}
	return strings.ToLower(s)
}

// Clean makes a string from a certificate or key safe to show: no control,
// format, bidi or invisible characters, no line breaks, whitespace runs one
// space, at most api.MaxSignatureTextBytes on a rune boundary.
func Clean(s string) string {
	s = strings.Join(strings.Fields(board.CleanText(s)), " ")
	return capBytes(s, api.MaxSignatureTextBytes)
}

// CleanList cleans each string, drops empty and repeated ones and keeps at
// most max.
func CleanList(in []string, max int) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		c := Clean(s)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
		if len(out) == max {
			break
		}
	}
	return out
}

func capBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// ProtectedHeaders compares the protected headers of the signed entity
// (RFC 9788: Content-Type parameter protected-headers="v1") with the
// outer header of the message; it returns the ones that differ, cleaned,
// at most api.MaxProtectedHeaders. signed is the signed entity's bytes
// (header and body), outer the message's raw bytes.
func ProtectedHeaders(signed, outer []byte) []api.ProtectedHeader {
	in, ok := parseEntity(signed)
	if !ok || !strings.EqualFold(in.params["protected-headers"], "v1") {
		return nil
	}
	out, ok := parseEntity(outer)
	if !ok {
		return nil
	}
	ih := mail.Header{Header: messageHeader(in)}
	oh := mail.Header{Header: messageHeader(out)}
	var diff []api.ProtectedHeader
	if in.header.Has("Subject") {
		is, _ := ih.Subject()
		os, _ := oh.Subject()
		if Clean(is) != Clean(os) {
			diff = append(diff, api.ProtectedHeader{Name: api.ProtectedSubject, Value: Clean(is)})
		}
	}
	for _, a := range []struct {
		key  string
		name api.ProtectedHeaderName
	}{{"From", api.ProtectedFrom}, {"To", api.ProtectedTo}, {"Cc", api.ProtectedCc}, {"Reply-To", api.ProtectedReplyTo}} {
		if !in.header.Has(a.key) {
			continue
		}
		il, _ := ih.AddressList(a.key)
		ol, _ := oh.AddressList(a.key)
		if addressSet(il) != addressSet(ol) {
			diff = append(diff, api.ProtectedHeader{Name: a.name, Value: Clean(in.header.Get(a.key))})
		}
	}
	if in.header.Has("Date") {
		id, ierr := ih.Date()
		od, oerr := oh.Date()
		if ierr != nil || oerr != nil || !id.Equal(od) {
			v := Clean(in.header.Get("Date"))
			if ierr == nil {
				v = id.UTC().Format(time.RFC1123Z)
			}
			diff = append(diff, api.ProtectedHeader{Name: api.ProtectedDate, Value: v})
		}
	}
	if len(diff) > api.MaxProtectedHeaders {
		diff = diff[:api.MaxProtectedHeaders]
	}
	return diff
}

func addressSet(l []*mail.Address) string {
	var s []string
	for _, a := range l {
		s = append(s, NormalizeAddress(a.Address))
	}
	sort.Strings(s)
	return strings.Join(s, ",")
}

// FromAddresses reads the From addresses of a raw message. A header with
// more than one From field gives none (which field a server or client
// shows differs, so no identity can match it).
func FromAddresses(raw []byte) []string {
	e, ok := parseEntity(raw)
	if !ok || len(e.header.Values("From")) != 1 {
		return nil
	}
	h := mail.Header{Header: messageHeader(e)}
	l, err := h.AddressList("From")
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range l {
		out = append(out, a.Address)
	}
	return out
}

// HeaderValues returns the values of a header field of the raw message's
// top-level header (unfolded, not decoded).
func HeaderValues(raw []byte, key string) []string {
	e, ok := parseEntity(raw)
	if !ok {
		return nil
	}
	return e.header.Values(key)
}
