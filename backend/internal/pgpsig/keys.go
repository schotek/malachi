// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package pgpsig

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"github.com/schotek/malachi/backend/pkg/api"
)

// ImportError is why ReadKeys refused key data.
type ImportError struct{ Reason api.PGPImportFailure }

func (e *ImportError) Error() string { return "pgpsig: key import refused: " + string(e.Reason) }

func refuse(r api.PGPImportFailure) error { return &ImportError{Reason: r} }

// Key is one public key as read: the entity and its serialised public
// packets (what the store keeps), with what the listings show (raw, not
// cleaned).
type Key struct {
	Entity      *openpgp.Entity
	Data        []byte // the transferable public key, binary
	Fingerprint string // primary, lowercase hex
	KeyIDs      []string
	UserIDs     []string
	Emails      []string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
	Algorithm   string
}

// ReadKeys reads public keys from armoured or binary data. A secret key
// anywhere refuses everything (nothing is stored, plan §11.1); more than
// api.MaxPGPImportKeys or more than api.MaxPGPKeyBytes is tooLarge; data
// without a public key noKey; unreadable data malformed. now judges the
// revocation shown in the result.
func ReadKeys(data []byte, now time.Time) (keys []Key, err error) {
	defer func() {
		if p := recover(); p != nil {
			keys, err = nil, refuse(api.PGPImportMalformed)
		}
	}()
	if len(data) > api.MaxPGPKeyBytes {
		return nil, refuse(api.PGPImportTooLarge)
	}
	bin, err := keyBinary(data)
	if err != nil {
		return nil, err
	}
	if len(bin) == 0 {
		return nil, refuse(api.PGPImportNoKey)
	}
	if hasSecretPacket(bin) {
		return nil, refuse(api.PGPImportSecretKey)
	}
	el, err := openpgp.ReadKeyRing(bytes.NewReader(bin))
	if err != nil && len(el) == 0 {
		if strings.Contains(err.Error(), "no armored data") || errors.Is(err, io.EOF) {
			return nil, refuse(api.PGPImportNoKey)
		}
		return nil, refuse(api.PGPImportMalformed)
	}
	if len(el) == 0 {
		return nil, refuse(api.PGPImportNoKey)
	}
	if len(el) > api.MaxPGPImportKeys {
		return nil, refuse(api.PGPImportTooLarge)
	}
	for _, e := range el {
		if e.PrivateKey != nil {
			return nil, refuse(api.PGPImportSecretKey)
		}
		for _, s := range e.Subkeys {
			if s.PrivateKey != nil {
				return nil, refuse(api.PGPImportSecretKey)
			}
		}
	}
	for _, e := range el {
		k, err := describe(e, now)
		if err != nil {
			return nil, refuse(api.PGPImportMalformed)
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// keyBinary undoes armour (one or more blocks), refusing a private key
// block.
func keyBinary(data []byte) ([]byte, error) {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if !bytes.HasPrefix(trimmed, []byte("-----BEGIN PGP ")) {
		if bytes.Contains(data, []byte("-----BEGIN PGP PRIVATE KEY BLOCK-----")) {
			return nil, refuse(api.PGPImportSecretKey)
		}
		return data, nil
	}
	var out []byte
	rest := trimmed
	for blocks := 0; blocks < api.MaxPGPImportKeys+1; blocks++ {
		i := bytes.Index(rest, []byte("-----BEGIN PGP "))
		if i < 0 {
			break
		}
		rest = rest[i:]
		block, err := armor.Decode(bytes.NewReader(rest))
		if err != nil {
			if blocks == 0 {
				return nil, refuse(api.PGPImportMalformed)
			}
			break
		}
		switch block.Type {
		case openpgp.PrivateKeyType:
			return nil, refuse(api.PGPImportSecretKey)
		case openpgp.PublicKeyType:
		default:
			if blocks == 0 {
				return nil, refuse(api.PGPImportNoKey)
			}
		}
		b, err := io.ReadAll(io.LimitReader(block.Body, api.MaxPGPKeyBytes+1))
		if err != nil {
			return nil, refuse(api.PGPImportMalformed)
		}
		if len(out)+len(b) > api.MaxPGPKeyBytes {
			return nil, refuse(api.PGPImportTooLarge)
		}
		if block.Type == openpgp.PublicKeyType {
			out = append(out, b...)
		}
		end := bytes.Index(rest, []byte("-----END PGP "))
		if end < 0 {
			break
		}
		rest = rest[end+len("-----END PGP "):]
	}
	return out, nil
}

// hasSecretPacket scans the packet tags: a secret key (5) or secret subkey
// (7) packet anywhere refuses the data, even where go-crypto would skip
// the entity.
func hasSecretPacket(bin []byte) bool {
	r := packet.NewReader(bytes.NewReader(bin))
	for n := 0; n < 100000; n++ {
		p, err := r.NextWithUnsupported()
		if err == io.EOF {
			return false
		}
		if err != nil {
			// A broken packet: let ReadKeyRing judge the rest; but a secret
			// packet can still hide in raw tags.
			return rawSecretTag(bin)
		}
		switch p.(type) {
		case *packet.PrivateKey:
			return true
		}
	}
	return rawSecretTag(bin)
}

// rawSecretTag looks at the first packet header only (a secret key block
// starts with a secret key packet).
func rawSecretTag(bin []byte) bool {
	if len(bin) == 0 || bin[0]&0x80 == 0 {
		return false
	}
	var tag byte
	if bin[0]&0x40 != 0 {
		tag = bin[0] & 0x3f
	} else {
		tag = (bin[0] >> 2) & 0x0f
	}
	return tag == 5 || tag == 7
}

// describe serialises the public key and collects what the listings show.
func describe(e *openpgp.Entity, now time.Time) (Key, error) {
	var buf bytes.Buffer
	if err := e.Serialize(&buf); err != nil {
		return Key{}, err
	}
	k := Key{
		Entity:      e,
		Data:        buf.Bytes(),
		Fingerprint: Fingerprint(e),
		CreatedAt:   e.PrimaryKey.CreationTime.UTC(),
		ExpiresAt:   KeyExpiry(e),
		RevokedAt:   RevokedAt(e, now),
		Algorithm:   KeyAlgorithm(e.PrimaryKey),
	}
	k.KeyIDs = append(k.KeyIDs, fmt.Sprintf("%016x", e.PrimaryKey.KeyId))
	for _, s := range e.Subkeys {
		k.KeyIDs = append(k.KeyIDs, fmt.Sprintf("%016x", s.PublicKey.KeyId))
	}
	names := make([]string, 0, len(e.Identities))
	for n := range e.Identities {
		names = append(names, n)
	}
	sort.Strings(names)
	seen := map[string]bool{}
	for _, n := range names {
		id := e.Identities[n]
		k.UserIDs = append(k.UserIDs, n)
		if id.UserId != nil && id.UserId.Email != "" {
			em := NormalizeEmail(id.UserId.Email)
			if !seen[em] {
				seen[em] = true
				k.Emails = append(k.Emails, em)
			}
		}
	}
	return k, nil
}

// Identities returns the entity's user IDs that are not revoked at t,
// as (name, address) pairs, in a stable order.
func Identities(e *openpgp.Entity, at time.Time) (names, emails []string) {
	keys := make([]string, 0, len(e.Identities))
	for n := range e.Identities {
		keys = append(keys, n)
	}
	sort.Strings(keys)
	for _, n := range keys {
		id := e.Identities[n]
		if id.UserId == nil || id.Revoked(at) {
			continue
		}
		names = append(names, id.UserId.Name)
		emails = append(emails, id.UserId.Email)
	}
	return names, emails
}

// ParseKeyring reads stored keys (binary transferable public keys).
func ParseKeyring(data [][]byte) openpgp.EntityList {
	var el openpgp.EntityList
	for _, d := range data {
		func() {
			defer func() { _ = recover() }()
			got, err := openpgp.ReadKeyRing(bytes.NewReader(d))
			if err == nil || len(got) > 0 {
				for _, e := range got {
					if e.PrivateKey == nil {
						el = append(el, e)
					}
				}
			}
		}()
	}
	return el
}

// NormalizeEmail lowers the domain of an address (and the local part:
// plan S5 compares case-insensitively; as-built note).
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Autocrypt is a parsed Autocrypt header (Level 1).
type Autocrypt struct {
	Addr    string
	KeyData []byte
}

// ParseAutocrypt reads the values of a message's Autocrypt headers. The
// Autocrypt rule: more than one header (or none) is none; an unknown
// critical attribute (one without a leading "_") invalidates the header.
func ParseAutocrypt(values []string) (Autocrypt, bool) {
	if len(values) != 1 {
		return Autocrypt{}, false
	}
	var ac Autocrypt
	var keydata string
	for _, attr := range strings.Split(values[0], ";") {
		attr = strings.TrimSpace(attr)
		if attr == "" {
			continue
		}
		name, value, ok := strings.Cut(attr, "=")
		if !ok {
			return Autocrypt{}, false
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		switch {
		case name == "addr":
			if ac.Addr != "" {
				return Autocrypt{}, false
			}
			ac.Addr = NormalizeEmail(value)
		case name == "keydata":
			if keydata != "" {
				return Autocrypt{}, false
			}
			keydata = value
		case name == "prefer-encrypt":
		case strings.HasPrefix(name, "_"):
		default:
			return Autocrypt{}, false
		}
	}
	if ac.Addr == "" || keydata == "" || len(keydata) > api.MaxPGPKeyBytes*2 {
		return Autocrypt{}, false
	}
	clean := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		}
		return r
	}, keydata)
	b, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(clean, "="))
		if err != nil {
			return Autocrypt{}, false
		}
	}
	if len(b) == 0 || len(b) > api.MaxPGPKeyBytes {
		return Autocrypt{}, false
	}
	ac.KeyData = b
	return ac, true
}

// FingerprintHex is the lowercase hex of a raw fingerprint.
func FingerprintHex(b []byte) string { return hex.EncodeToString(b) }
