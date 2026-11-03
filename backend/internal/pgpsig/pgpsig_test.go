// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package pgpsig

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"github.com/schotek/malachi/backend/pkg/api"
)

var now = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)

func readKey(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../testdata/signatures", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReadKeys(t *testing.T) {
	keys, err := ReadKeys(readKey(t, "alice-pgp.asc"), now)
	if err != nil || len(keys) != 1 {
		t.Fatalf("%v %d", err, len(keys))
	}
	k := keys[0]
	if len(k.Fingerprint) != 40 || len(k.Emails) != 1 || k.Emails[0] != "alice@example.org" || k.Algorithm != "Ed25519" || len(k.KeyIDs) != 2 {
		t.Errorf("%+v", k)
	}
	rev, err := ReadKeys(readKey(t, "robert-revoked.asc"), now)
	if err != nil || rev[0].RevokedAt == nil {
		t.Errorf("revoked: %+v %v", rev, err)
	}
	// Two keys in one armour stream.
	both := append(readKey(t, "carol-1.asc"), readKey(t, "carol-2.asc")...)
	if keys, err := ReadKeys(both, now); err != nil || len(keys) != 2 {
		t.Errorf("two keys: %d %v", len(keys), err)
	}
}

func TestReadKeysRefuses(t *testing.T) {
	e, err := openpgp.NewEntity("Secret", "", "secret@example.org", &packet.Config{Algorithm: packet.PubKeyAlgoEd25519})
	if err != nil {
		t.Fatal(err)
	}
	var sec bytes.Buffer
	w, _ := armor.Encode(&sec, openpgp.PrivateKeyType, nil)
	if err := e.SerializePrivate(w, nil); err != nil {
		t.Fatal(err)
	}
	w.Close()
	var secBin bytes.Buffer
	_ = e.SerializePrivate(&secBin, nil)
	cases := map[string]struct {
		data []byte
		want api.PGPImportFailure
	}{
		"secret armoured":  {sec.Bytes(), api.PGPImportSecretKey},
		"secret binary":    {secBin.Bytes(), api.PGPImportSecretKey},
		"secret in a pair": {append(readKey(t, "alice-pgp.asc"), sec.Bytes()...), api.PGPImportSecretKey},
		"not a key":        {[]byte("hello"), api.PGPImportMalformed},
		"signature":        {[]byte("-----BEGIN PGP SIGNATURE-----\n\niQ==\n=AAAA\n-----END PGP SIGNATURE-----\n"), api.PGPImportNoKey},
		"too large":        {make([]byte, api.MaxPGPKeyBytes+1), api.PGPImportTooLarge},
		"broken armour":    {[]byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\n\n!!!!\n-----END PGP PUBLIC KEY BLOCK-----\n"), api.PGPImportMalformed},
	}
	for name, c := range cases {
		_, err := ReadKeys(c.data, now)
		var ie *ImportError
		if !errors.As(err, &ie) || ie.Reason != c.want {
			t.Errorf("%s: %v, want %s", name, err, c.want)
		}
	}
}

func TestAutocrypt(t *testing.T) {
	keyB64 := "bWVyZWx5IGJ5dGVz"
	cases := []struct {
		in   []string
		ok   bool
		addr string
	}{
		{[]string{"addr=Alice@Example.org; prefer-encrypt=mutual; keydata=" + keyB64}, true, "alice@example.org"},
		{[]string{"addr=a@x; keydata=\r\n " + keyB64[:4] + "\r\n " + keyB64[4:]}, true, "a@x"},
		{[]string{"addr=a@x; _private=1; keydata=" + keyB64}, true, "a@x"},
		{[]string{"addr=a@x; critical=1; keydata=" + keyB64}, false, ""},
		{[]string{"addr=a@x; keydata=" + keyB64, "addr=b@x; keydata=" + keyB64}, false, ""},
		{[]string{"addr=a@x"}, false, ""},
		{[]string{"keydata=" + keyB64}, false, ""},
		{[]string{"addr=a@x; addr=b@x; keydata=" + keyB64}, false, ""},
		{[]string{"addr=a@x; keydata=!!!"}, false, ""},
		{nil, false, ""},
	}
	for i, c := range cases {
		ac, ok := ParseAutocrypt(c.in)
		if ok != c.ok || ac.Addr != c.addr {
			t.Errorf("%d: %v %+v", i, ok, ac)
		}
	}
}

func TestInline(t *testing.T) {
	if !IsInlineSigned("\r\n-----BEGIN PGP SIGNED MESSAGE-----\r\nHash: SHA256\r\n") || IsInlineSigned("Hi\n-----BEGIN PGP SIGNED MESSAGE-----") {
		t.Error("inline detection")
	}
}

func TestMD5Peek(t *testing.T) {
	// A v4 signature packet header with hash algorithm 1 (MD5).
	if !md5Signature([]byte{0xc2, 10, 4, 0, 1, 1, 0, 0, 0, 0, 0, 0}) {
		t.Error("MD5 not seen")
	}
	if md5Signature([]byte{0xc2, 10, 4, 0, 1, 8, 0, 0}) {
		t.Error("SHA-256 seen as MD5")
	}
}

func FuzzPGPSignature(f *testing.F) {
	keys, err := ReadKeys(readKey(f, "alice-pgp.asc"), now)
	if err != nil {
		f.Fatal(err)
	}
	ring := openpgp.EntityList{keys[0].Entity}
	raw, err := os.ReadFile("../../testdata/mime/pgp-valid.eml")
	if err != nil {
		f.Fatal(err)
	}
	if i := bytes.Index(raw, []byte("-----BEGIN PGP SIGNATURE-----")); i >= 0 {
		sig := raw[i:]
		if j := bytes.Index(sig, []byte("-----END PGP SIGNATURE-----")); j >= 0 {
			f.Add(sig[:j+len("-----END PGP SIGNATURE-----")])
		}
	}
	f.Add([]byte{0xc2, 10, 4, 0, 1, 8})
	f.Fuzz(func(t *testing.T, sig []byte) {
		r := Verify([]byte("content\r\n"), sig, ring, Options{At: now, Now: now})
		if r.Verified && r.Entity == nil {
			t.Error("verified without a key")
		}
		_, _ = ReadKeys(sig, now)
	})
}

func FuzzAutocrypt(f *testing.F) {
	raw, err := os.ReadFile("../../testdata/mime/pgp-valid.eml")
	if err != nil {
		f.Fatal(err)
	}
	if i := bytes.Index(raw, []byte("Autocrypt: ")); i >= 0 {
		v := raw[i+len("Autocrypt: "):]
		if j := bytes.Index(v, []byte("\r\nContent-Type")); j >= 0 {
			v = v[:j]
		}
		f.Add(string(v))
	}
	f.Add("addr=a@x; keydata=AAAA")
	f.Fuzz(func(t *testing.T, v string) {
		ac, ok := ParseAutocrypt([]string{v})
		if !ok {
			return
		}
		if ac.Addr == "" || len(ac.KeyData) == 0 || strings.ContainsAny(ac.Addr, ";") {
			t.Error("accepted without address or key")
		}
		_, _ = ReadKeys(ac.KeyData, now)
	})
}
