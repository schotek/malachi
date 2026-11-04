// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package bulk

import (
	"strings"
)

// The trust path of Microsoft 365 (Graph) accounts. Exchange serves the
// MIME of a message rebuilt, so its DKIM signatures no longer verify
// against the bytes the daemon has, but it has verified them on arrival
// and says so in the Authentication-Results field it prepends (RFC 8601).
// For a Graph account the one-click request counts as verified when that
// topmost field has a dkim=pass for a domain of the sender's organisation
// and the message carries a DKIM-Signature of that domain which signs both
// unsubscribe fields. Both fields are parsed strictly and within bounds:
// whatever is odd is "not verified".

const (
	// maxAuthBytes bounds the header values read here.
	maxAuthBytes = 16 << 10
	// maxAuthResults and maxCommentDepth bound the work on a hostile field.
	maxAuthResults  = 32
	maxCommentDepth = 8
	maxSignatures   = 16
)

// AuthResult is one method result of an Authentication-Results field.
type AuthResult struct {
	Method string // lower case, "dkim"
	Result string // lower case, "pass"
	// HeaderD is the lower-case header.d property (the signing domain), ""
	// when the result has none.
	HeaderD string
}

// ParseAuthenticationResults reads the value of an Authentication-Results
// field. Comments in parentheses are removed (unbalanced or too deeply
// nested ones make the field unreadable), the authserv-id is optional (the
// field Exchange prepends has none), results are separated by ";". ok is
// false for a field that is too long, not valid UTF-8 or malformed.
func ParseAuthenticationResults(value string) ([]AuthResult, bool) {
	if len(value) > maxAuthBytes {
		return nil, false
	}
	v, ok := stripComments(value)
	if !ok {
		return nil, false
	}
	var out []AuthResult
	for i, seg := range strings.Split(v, ";") {
		f := strings.Fields(seg)
		if len(f) == 0 {
			continue
		}
		if !strings.Contains(f[0], "=") {
			// authserv-id (with an optional version), or "none".
			if i != 0 && !strings.EqualFold(f[0], "none") {
				return nil, false
			}
			continue
		}
		method, result, ok := strings.Cut(f[0], "=")
		if !ok || method == "" || result == "" || len(out) >= maxAuthResults {
			return nil, false
		}
		r := AuthResult{Method: strings.ToLower(method), Result: strings.ToLower(result)}
		for _, prop := range f[1:] {
			k, val, ok := strings.Cut(prop, "=")
			if !ok {
				continue
			}
			if strings.EqualFold(k, "header.d") {
				val = strings.ToLower(strings.Trim(val, `"`))
				if r.HeaderD != "" || !plain(val) {
					// A repeated or unusable header.d: unreadable.
					return nil, false
				}
				r.HeaderD = val
			}
		}
		out = append(out, r)
	}
	return out, true
}

// stripComments removes RFC 5322 comments (nested parentheses, with
// backslash escapes) and reports false for an unbalanced one. Quoted
// strings are not treated specially: a parenthesis inside one only makes
// the field unreadable or shorter, never trusted.
func stripComments(s string) (string, bool) {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && depth > 0 && i+1 < len(s):
			i++
		case c == '(':
			depth++
			if depth > maxCommentDepth {
				return "", false
			}
		case c == ')':
			if depth == 0 {
				return "", false
			}
			depth--
		case depth == 0:
			b.WriteByte(c)
		}
	}
	return b.String(), depth == 0
}

// DKIMSignature is the d= and h= of a DKIM-Signature field.
type DKIMSignature struct {
	Domain  string   // lower case
	Headers []string // lower case, the h= list
}

// ParseDKIMSignature reads the d= and h= tags of a DKIM-Signature value
// (RFC 6376 §3.2: ";"-separated tag=value, whitespace inside a value
// ignored). It is false for a field that is too long, repeats a tag, or
// lacks a usable d= or h=. Nothing else is interpreted: the signature is
// not verified here.
func ParseDKIMSignature(value string) (DKIMSignature, bool) {
	if len(value) > maxAuthBytes || !strings.Contains(value, "=") {
		return DKIMSignature{}, false
	}
	seen := map[string]bool{}
	var sig DKIMSignature
	for _, tag := range strings.Split(value, ";") {
		k, v, ok := strings.Cut(tag, "=")
		k = strings.ToLower(strings.TrimSpace(k))
		if !ok || k == "" {
			if strings.TrimSpace(tag) == "" {
				continue
			}
			return DKIMSignature{}, false
		}
		if seen[k] {
			return DKIMSignature{}, false
		}
		seen[k] = true
		v = strings.Join(strings.Fields(v), "")
		switch k {
		case "d":
			sig.Domain = strings.ToLower(strings.TrimSuffix(v, "."))
		case "h":
			for _, h := range strings.Split(strings.ToLower(v), ":") {
				if h != "" {
					sig.Headers = append(sig.Headers, h)
				}
			}
		}
	}
	if sig.Domain == "" || !plain(sig.Domain) || len(sig.Headers) == 0 {
		return DKIMSignature{}, false
	}
	return sig, true
}

// Signs reports whether the h= list names the field.
func (s DKIMSignature) Signs(name string) bool {
	for _, h := range s.Headers {
		if strings.EqualFold(h, name) {
			return true
		}
	}
	return false
}

// ExchangeVerified reports whether the one-click request of a message
// served by Exchange counts as verified: the topmost Authentication-
// Results field says dkim=pass for a header.d of the From domain's
// organisation, and one of the message's DKIM-Signature fields has that
// same d= and signs From, List-Unsubscribe and List-Unsubscribe-Post.
// authResults are the values of every Authentication-Results field in
// document order (only the first, topmost, is read: anything below it was
// written before Exchange, by the sender or anyone on the way); sigs the
// values of every DKIM-Signature field.
func ExchangeVerified(authResults, sigs []string, fromDomain string) bool {
	if len(authResults) == 0 || fromDomain == "" {
		return false
	}
	results, ok := ParseAuthenticationResults(authResults[0])
	if !ok {
		return false
	}
	if len(sigs) > maxSignatures {
		sigs = sigs[:maxSignatures]
	}
	for _, r := range results {
		if r.Method != "dkim" || r.Result != "pass" || r.HeaderD == "" || !Aligned(r.HeaderD, fromDomain) {
			continue
		}
		for _, raw := range sigs {
			sig, ok := ParseDKIMSignature(raw)
			if ok && sig.Domain == r.HeaderD && sig.Signs("from") &&
				sig.Signs(HeaderListUnsubscribe) && sig.Signs(HeaderListUnsubscribePost) {
				return true
			}
		}
	}
	return false
}
