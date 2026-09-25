// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package certtrust decides what the UI shows and offers when an IMAP or
// SMTP server's certificate is refused: the details of a tlsError
// (docs/api.md §2, TLSErrorData), whether the account assistant may offer
// to trust the certificate by pinning its SHA-256 fingerprint
// (ServerConfig.certificateSha256), when a pin survives an edit of the
// server settings, and which account states are certificate problems. It
// holds no GTK and no user-facing text, so its rules are tested without a
// display; the assistant, the sidebar, the banner and the Accounts page act
// on its answers, and the macOS client mirrors them one to one.
package certtrust

import (
	"net"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Caps of CertificateInfo (docs/api.md §2). The backend enforces them; the
// UI enforces them again because the text is the server's.
const (
	maxTextBytes = 128
	maxListItems = 8
)

// Problem is a cleaned tlsError: why the connection failed, the server's
// certificate when it presented a usable one (nil otherwise), and for
// api.TLSPinMismatch the pinned fingerprint ("" when absent). Reason is one
// of the api.TLS* reasons; an unknown one is api.TLSOther. Every string of
// Cert is plain text without control or format characters, SHA256 and
// Expected are 64 lowercase hex digits.
type Problem struct {
	Reason   api.TLSErrorReason
	Cert     *api.CertificateInfo
	Expected string
}

// Category groups the reasons by what the user can do about them.
type Category int

const (
	// Connection: no certificate was judged (handshake failure, STARTTLS
	// not offered, the server demands TLS before login). Nothing to trust;
	// the account shows as offline.
	Connection Category = iota
	// Certificate: the server's certificate was refused (untrusted issuer,
	// another name, expired, not yet valid, invalid, anything else the
	// system verifier refused, and any reason this client does not know).
	Certificate
	// Changed: the endpoint pins a certificate and the server presented
	// another one.
	Changed
)

// CategoryOf sorts a reason; unknown reasons count as api.TLSOther.
func CategoryOf(r api.TLSErrorReason) Category {
	switch NormalizeReason(r) {
	case api.TLSHandshake, api.TLSStartTLSUnavail, api.TLSRequired:
		return Connection
	case api.TLSPinMismatch:
		return Changed
	}
	return Certificate
}

// Category is the category of p's reason.
func (p Problem) Category() Category { return CategoryOf(p.Reason) }

// NormalizeReason returns r when it is a reason of docs/api.md §2 and
// api.TLSOther for anything else, the empty reason included.
func NormalizeReason(r api.TLSErrorReason) api.TLSErrorReason {
	switch r {
	case api.TLSUntrusted, api.TLSHostnameMismatch, api.TLSExpired, api.TLSNotYetValid,
		api.TLSInvalid, api.TLSOther, api.TLSHandshake, api.TLSStartTLSUnavail,
		api.TLSRequired, api.TLSPinMismatch:
		return r
	}
	return api.TLSOther
}

// Details reads the TLS details of e. false when e is not a tlsError or
// carries no details (no data, or data without a reason). The reason is
// normalised, every certificate string cleaned and capped, lists capped;
// a certificate whose fingerprint is not 64 hex digits is dropped whole,
// an invalid expected fingerprint becomes "".
func Details(e *api.Error) (Problem, bool) {
	d, ok := api.TLSErrorDataOf(e)
	if !ok || d.Reason == "" {
		return Problem{}, false
	}
	p := Problem{Reason: NormalizeReason(d.Reason), Cert: cleanCertificate(d.Certificate)}
	if exp, ok := api.NormalizeCertificateSHA256(d.ExpectedSHA256); ok {
		p.Expected = exp
	}
	return p, true
}

func cleanCertificate(c *api.CertificateInfo) *api.CertificateInfo {
	if c == nil {
		return nil
	}
	sum, ok := api.NormalizeCertificateSHA256(c.SHA256)
	if !ok {
		return nil
	}
	return &api.CertificateInfo{
		SHA256:      sum,
		Subject:     cleanText(c.Subject),
		Issuer:      cleanText(c.Issuer),
		DNSNames:    cleanList(c.DNSNames),
		IPAddresses: cleanList(c.IPAddresses),
		NotBefore:   c.NotBefore,
		NotAfter:    c.NotAfter,
		SelfSigned:  c.SelfSigned,
	}
}

// cleanText drops invalid UTF-8, control characters (Cc, newlines
// included), format characters (Cf, such as U+202E, which reverses what
// follows) and the line and paragraph separators (Zl U+2028, Zp U+2029),
// trims space and caps the result at maxTextBytes on a rune boundary,
// trimming again after the cut.
func cleanText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if len(out) <= maxTextBytes {
		return out
	}
	cut := maxTextBytes
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return strings.TrimSpace(out[:cut])
}

// cleanList cleans every entry, drops the empty ones and keeps at most
// maxListItems.
func cleanList(list []string) []string {
	var out []string
	for _, s := range list {
		if len(out) == maxListItems {
			break
		}
		if s = cleanText(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// FormatFingerprint shows a SHA-256 fingerprint as 16 groups of 4
// upper-case hex digits separated by single spaces ("AB12 CD34 …"); "" when
// hex is not a fingerprint NormalizeCertificateSHA256 accepts.
func FormatFingerprint(hex string) string {
	sum, ok := api.NormalizeCertificateSHA256(hex)
	if !ok {
		return ""
	}
	sum = strings.ToUpper(sum)
	var b strings.Builder
	for i := 0; i < len(sum); i += 4 {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(sum[i : i+4])
	}
	return b.String()
}

// Pinnable reports an endpoint configuration that may pin a certificate:
// TLS or STARTTLS, signing in with a password (docs/api.md §4.1; tokens of
// an oauth2 endpoint never go to a pinned server).
func Pinnable(sc api.ServerConfig) bool {
	return (sc.Security == api.SecurityTLS || sc.Security == api.SecuritySTARTTLS) &&
		sc.AuthMethod == api.AuthPassword
}

// Trustable decides whether the assistant offers to trust the certificate
// of an endpoint that failed the connection test with configuration sc:
// the test failed with a tlsError whose details name a certificate with a
// valid fingerprint, the reason is about that certificate (Certificate or
// Changed, never Connection), sc is Pinnable, and the certificate is not
// the one sc already pins. The Problem is what the confirmation shows.
func Trustable(res *api.EndpointTestResult, sc api.ServerConfig) (Problem, bool) {
	if res == nil || res.OK || res.Error == nil {
		return Problem{}, false
	}
	p, ok := Details(res.Error)
	if !ok || p.Cert == nil || p.Category() == Connection || !Pinnable(sc) {
		return Problem{}, false
	}
	if pinned, ok := api.NormalizeCertificateSHA256(sc.CertificateSHA256); ok && pinned == p.Cert.SHA256 {
		return Problem{}, false
	}
	return p, true
}

// SameCertificate reports two certificates with the same valid fingerprint
// (IMAP and SMTP of a mail bridge presenting one certificate: one
// confirmation trusts both).
func SameCertificate(a, b *api.CertificateInfo) bool {
	if a == nil || b == nil {
		return false
	}
	x, ok := api.NormalizeCertificateSHA256(a.SHA256)
	if !ok {
		return false
	}
	y, ok := api.NormalizeCertificateSHA256(b.SHA256)
	return ok && x == y
}

// FromSyncState reports an account whose last connection attempt failed on
// its server's certificate: status offline or error with a tlsError whose
// details are not of Category Connection. The certificate itself is not
// required (the sidebar and the banner only need the category).
func FromSyncState(s api.SyncState) (Problem, bool) {
	if s.Status != api.SyncOffline && s.Status != api.SyncError {
		return Problem{}, false
	}
	p, ok := Details(s.Error)
	if !ok || p.Category() == Connection {
		return Problem{}, false
	}
	return p, true
}

// KeepPin is the pin an endpoint keeps after its settings changed from old
// to cur: old's pin (normalised) while the host (trimmed, case-insensitive)
// and the port are the same and cur is still Pinnable; "" otherwise, and
// when old has no valid pin. A pin is trusted for one server, never for
// whatever the endpoint is changed to.
func KeepPin(old, cur api.ServerConfig) string {
	pin, ok := api.NormalizeCertificateSHA256(old.CertificateSHA256)
	if !ok || !Pinnable(cur) || old.Port != cur.Port ||
		!strings.EqualFold(strings.TrimSpace(old.Host), strings.TrimSpace(cur.Host)) {
		return ""
	}
	return pin
}

// ServerName is the endpoint as the confirmation names it: "host:port",
// with an IPv6 literal in brackets.
func ServerName(sc api.ServerConfig) string {
	return net.JoinHostPort(strings.TrimSpace(sc.Host), strconv.Itoa(sc.Port))
}

// IssuedTo is the "Issued to" value of the confirmation: the subject on
// the first line, then the DNS names and IP addresses the certificate is
// valid for, without duplicates and without the subject itself (compared
// ignoring case), joined by ", " on the second line. "" when the
// certificate names nothing.
func IssuedTo(c api.CertificateInfo) string {
	var lines []string
	subject := cleanText(c.Subject)
	if subject != "" {
		lines = append(lines, subject)
	}
	var names []string
	seen := map[string]bool{strings.ToLower(subject): true}
	for _, n := range append(cleanList(c.DNSNames), cleanList(c.IPAddresses)...) {
		key := strings.ToLower(n)
		if seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, n)
	}
	if len(names) > 0 {
		lines = append(lines, strings.Join(names, ", "))
	}
	return strings.Join(lines, "\n")
}
