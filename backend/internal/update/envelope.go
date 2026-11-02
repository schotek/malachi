// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package update reads the signed manifest of Malachi Mail versions
// (docs/services.md §2) and picks the update a build may be offered. It is
// pure: no store, no clock of its own; the HTTP client is client.go.
//
// An envelope is verified before its payload is parsed (Verify), and a
// payload is checked whole before anything of it is used (Parse). Any
// violation is an *Error and the caller keeps what it had.
package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
)

// Size limits of docs/services.md §2.2.
const (
	MaxEnvelope = 256 << 10
	MaxPayload  = 192 << 10
)

// Kind classifies a rejected envelope or manifest.
type Kind int

const (
	// TooLarge: the envelope or its payload is over its limit.
	TooLarge Kind = iota + 1
	// Format: the envelope is not the JSON object of §2.2 or its base64 is
	// not canonical.
	Format
	// UnknownKey: no compiled-in public key has the envelope's keyId.
	UnknownKey
	// Signature: the signature does not verify.
	Signature
	// BadManifest: the payload breaks §2.3. Path names the offending value
	// ("" for the payload as a whole).
	BadManifest
)

func (k Kind) String() string {
	switch k {
	case TooLarge:
		return "tooLarge"
	case Format:
		return "format"
	case UnknownKey:
		return "unknownKey"
	case Signature:
		return "signature"
	case BadManifest:
		return "manifest"
	}
	return "unknown"
}

// Error is why an envelope or a manifest was refused. Its text names the
// rule and the path, never a value from the manifest.
type Error struct {
	Kind Kind
	Path string
	Msg  string
	// Newer: the manifest has a schema above 1. Not a fault of the
	// server: a later version reads it, this one keeps what it had.
	Newer bool
}

func (e *Error) Error() string {
	if e.Kind == BadManifest {
		return fmt.Sprintf("update manifest: %s: %s", e.Path, e.Msg)
	}
	return fmt.Sprintf("update envelope: %s: %s", e.Kind, e.Msg)
}

func envErr(k Kind, format string, a ...any) *Error {
	return &Error{Kind: k, Msg: fmt.Sprintf(format, a...)}
}

// Verify checks an envelope against keys and returns the exact payload
// bytes its signature covers.
func Verify(envelope []byte, keys map[string]ed25519.PublicKey) ([]byte, error) {
	if len(envelope) > MaxEnvelope {
		return nil, envErr(TooLarge, "envelope of %d bytes", len(envelope))
	}
	if !validUTF8(envelope) {
		return nil, envErr(Format, "envelope is not UTF-8")
	}
	v, err := parseJSON(envelope)
	if err != nil {
		return nil, envErr(Format, "envelope is not JSON")
	}
	if v.kind != kObject {
		return nil, envErr(Format, "envelope is a JSON %s", v.kind)
	}
	known := []string{"keyId", "payload", "signature"}
	fields := map[string]string{}
	for _, m := range v.obj {
		if _, dup := fields[m.key]; dup {
			return nil, envErr(Format, "field %q twice", m.key)
		}
		if twin(m.key, known) {
			return nil, envErr(Format, "field %q differs from a known one only in case", m.key)
		}
		if !slices.Contains(known, m.key) {
			fields[m.key] = ""
			continue // an unknown field is ignored (§1)
		}
		if m.val.kind != kString || m.val.bad {
			return nil, envErr(Format, "%s is not a string", m.key)
		}
		fields[m.key] = m.val.str
	}
	for _, k := range known {
		if _, ok := fields[k]; !ok {
			return nil, envErr(Format, "%s is missing", k)
		}
	}
	payload, ok := canonicalBase64(fields["payload"])
	if !ok {
		return nil, envErr(Format, "payload is not canonical base64")
	}
	if len(payload) > MaxPayload {
		return nil, envErr(TooLarge, "payload of %d bytes", len(payload))
	}
	sig, ok := canonicalBase64(fields["signature"])
	if !ok || len(sig) != ed25519.SignatureSize {
		return nil, envErr(Format, "signature is not %d bytes of canonical base64", ed25519.SignatureSize)
	}
	key, ok := keys[fields["keyId"]]
	if !ok {
		return nil, envErr(UnknownKey, "unknown keyId")
	}
	if !ed25519.Verify(key, payload, sig) {
		return nil, envErr(Signature, "signature does not verify")
	}
	return payload, nil
}

// canonicalBase64 decodes standard base64 with padding (RFC 4648 §4) and
// accepts it only when encoding the result gives the same text back: no
// line breaks (which the decoder skips), no URL alphabet, no non-zero
// unused bits.
func canonicalBase64(s string) ([]byte, bool) {
	b, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil || base64.StdEncoding.EncodeToString(b) != s {
		return nil, false
	}
	return b, true
}

// twin: key equals one of known ignoring case but not exactly.
func twin(key string, known []string) bool {
	for _, k := range known {
		if k != key && strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}
