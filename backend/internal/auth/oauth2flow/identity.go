// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// maxIDToken bounds the ID token we are willing to decode.
const maxIDToken = 16 << 10

// maxEmail bounds an identity address (RFC 5321 path limit).
const maxEmail = 254

// idTokenLeeway is the clock skew tolerated on the ID token's exp.
const idTokenLeeway = 2 * time.Minute

// googleIdentity returns the built-in identity of Google grants: the
// email claim of the ID token. The token came straight from the token
// endpoint over TLS, so its signature need not be checked (OpenID
// Connect Core 1.0 §3.1.3.7, item 6); issuer, audience (and azp),
// expiry and email_verified still are, against the injected clock.
func googleIdentity(clientID string, now func() time.Time) IdentityFunc {
	return func(_ context.Context, g Grant) (string, error) {
		return idTokenEmail(g.IDToken, clientID, now())
	}
}

var errIDToken = errors.New("ID token unusable")

type idClaims struct {
	Iss           string          `json:"iss"`
	Aud           json.RawMessage `json:"aud"`
	Azp           string          `json:"azp"`
	Exp           json.RawMessage `json:"exp"`
	Email         string          `json:"email"`
	EmailVerified json.RawMessage `json:"email_verified"`
}

// idTokenEmail decodes the payload of a JWT without verifying its
// signature and returns its email when issuer, audience, authorised
// party, expiry and email_verified allow; "" with an error otherwise.
func idTokenEmail(token, clientID string, now time.Time) (string, error) {
	if token == "" {
		return "", errors.New("no ID token in the token response")
	}
	if len(token) > maxIDToken {
		return "", errIDToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errIDToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", errIDToken
	}
	var c idClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return "", errIDToken
	}
	if !slices.Contains(googleIssuers, c.Iss) {
		return "", errors.New("ID token from an unexpected issuer")
	}
	if !audienceOK(c.Aud, c.Azp, clientID) {
		return "", errors.New("ID token for another client")
	}
	exp, ok := numericDate(c.Exp)
	if !ok {
		return "", errors.New("ID token without a usable exp")
	}
	if now.After(exp.Add(idTokenLeeway)) {
		return "", errors.New("ID token expired")
	}
	if !emailVerified(c.EmailVerified) {
		return "", errors.New("ID token address not verified")
	}
	email := cleanEmail(c.Email)
	if email == "" {
		return "", errors.New("ID token names no address")
	}
	return email, nil
}

// audienceOK: aud is the client id, or an array holding it — then azp
// must be the client id too (OpenID Connect Core 1.0 §3.1.3.7, items 3
// to 5). An azp present with a single audience must match as well.
func audienceOK(raw json.RawMessage, azp, clientID string) bool {
	if clientID == "" {
		return false
	}
	if azp != "" && azp != clientID {
		return false
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == clientID
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return slices.Contains(many, clientID) && azp == clientID
	}
	return false
}

// numericDate reads a JWT NumericDate (seconds since the epoch, a JSON
// number); false when absent or not a positive, finite number.
func numericDate(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 {
		return time.Time{}, false
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 || f > math.MaxInt32*4 {
		return time.Time{}, false
	}
	return time.Unix(int64(f), 0), true
}

// emailVerified: the claim must be present and true (a JSON true, or the
// string "true" some providers send); absent, false or anything else is
// not verified.
func emailVerified(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.EqualFold(s, "true")
	}
	return false
}

// cleanEmail accepts what can be an address: at most maxEmail bytes, no
// spaces, control characters (Cc) or format characters (Cf: bidi
// overrides such as U+202E, zero-width characters), one '@' with text on
// both sides. Anything else yields "": such an identity is refused, not
// repaired, so it can neither match an expected address nor reach a UI
// as signedInAs.
func cleanEmail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxEmail {
		return ""
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) || r == '<' || r == '>' || r == '"' || r == utf8.RuneError {
			return ""
		}
	}
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || at == len(s)-1 || strings.Count(s, "@") != 1 {
		return ""
	}
	return s
}

// normalizeAddress is store.NormalizeAddress, copied because this package
// must not depend on the store: surrounding space trimmed, lower-cased.
// TestNormalizeAddressMatchesStore keeps the two equal.
func normalizeAddress(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
