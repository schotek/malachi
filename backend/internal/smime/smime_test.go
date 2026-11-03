// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package smime

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cmsOf returns the CMS of a fixture: the base64 body of its last part
// (the detached signature) or of its root (opaque).
func cmsOf(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("../../testdata/mime", name))
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	var b64 []byte
	if i := bytes.LastIndex(raw, []byte("filename=\"smime.p7s\"\n\n")); i >= 0 {
		b64 = raw[i+len("filename=\"smime.p7s\"\n\n"):]
	} else if i := bytes.Index(raw, []byte("filename=\"smime.p7m\"\n\n")); i >= 0 {
		b64 = raw[i+len("filename=\"smime.p7m\"\n\n"):]
	}
	if j := bytes.Index(b64, []byte("\n--")); j >= 0 {
		b64 = b64[:j]
	}
	out, err := base64.StdEncoding.DecodeString(string(bytes.ReplaceAll(b64, []byte("\n"), nil)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func TestParseFixtures(t *testing.T) {
	sd, err := Parse(cmsOf(t, "smime-valid-clear.eml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sd.Signers) != 1 || len(sd.Certificates) != 2 || sd.EContent != nil || sd.Signers[0].SigningTime == nil {
		t.Errorf("%+v", sd)
	}
	sd, err = Parse(cmsOf(t, "smime-valid-opaque.eml"))
	if err != nil || len(sd.EContent) == 0 {
		t.Fatalf("opaque: %v", err)
	}
	if _, err := Parse(cmsOf(t, "smime-chain-20.eml")); !errors.Is(err, ErrLimit) {
		t.Errorf("chain of 20: %v", err)
	}
	if _, err := Parse(cmsOf(t, "smime-multiple-signers.eml")); err != nil {
		t.Errorf("two signers: %v", err)
	}
}

// TestBER: an indefinite-length encoding with a constructed eContent reads
// as its DER.
func TestBER(t *testing.T) {
	der := cmsOf(t, "smime-valid-opaque.eml")
	sd, err := Parse(der)
	if err != nil {
		t.Fatal(err)
	}
	// Re-encode only the outer SEQUENCE with an indefinite length.
	ber, ok := indefinite(der)
	if !ok {
		t.Fatal("cannot make BER")
	}
	sd2, err := Parse(ber)
	if err != nil {
		t.Fatalf("BER: %v", err)
	}
	if !bytes.Equal(sd.EContent, sd2.EContent) {
		t.Error("content differs")
	}
	// A constructed OCTET STRING in pieces.
	pieces := []byte{0x24, 0x80, 0x04, 0x02, 'a', 'b', 0x04, 0x01, 'c', 0x00, 0x00}
	out, err := toDER(pieces, 4, 100)
	if err != nil || !bytes.Equal(out, []byte{0x04, 0x03, 'a', 'b', 'c'}) {
		t.Errorf("%x %v", out, err)
	}
	// DER passes through unchanged.
	out, err = toDER(der, MaxDepth, MaxObjectBytes)
	if err != nil || !bytes.Equal(out, der) {
		t.Errorf("DER changed: %v", err)
	}
}

// indefinite rewrites the outer element of der with an indefinite length.
func indefinite(der []byte) ([]byte, bool) {
	r := &berReader{in: der, maxDepth: 4, maxOut: len(der)}
	h, err := r.header(0)
	if err != nil || !h.constructed {
		return nil, false
	}
	out := append([]byte{}, h.tag...)
	out = append(out, 0x80)
	out = append(out, der[h.hdrLen:]...)
	return append(out, 0, 0), true
}

func TestHostileASN1(t *testing.T) {
	cases := map[string][]byte{
		"empty":         nil,
		"deep":          bytes.Repeat([]byte{0x30, 0x80}, 10000),
		"huge length":   {0x30, 0x84, 0x7f, 0xff, 0xff, 0xff},
		"long length":   {0x30, 0x88, 1, 2, 3, 4, 5, 6, 7, 8},
		"primitive inf": {0x04, 0x80, 0, 0},
		"bad high tag":  {0x1f, 0xff, 0xff, 0xff, 0xff, 0xff},
		"trailing":      {0x30, 0x00, 0x00},
		"no eoc":        {0x30, 0x80, 0x04, 0x00},
	}
	for name, in := range cases {
		if _, err := Parse(in); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func FuzzSignedData(f *testing.F) {
	for _, n := range []string{"smime-valid-clear.eml", "smime-valid-opaque.eml", "smime-multiple-signers.eml", "smime-sha1.eml", "smime-ecdsa.eml", "smime-dsa.eml"} {
		f.Add(cmsOf(f, n))
	}
	f.Add([]byte{0x30, 0x80, 0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x07, 0x02, 0, 0})
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, data []byte) {
		sd, err := Parse(data)
		if err != nil {
			return
		}
		r := Verify(sd, []byte("content"), Options{At: at, Now: at})
		if r.Verified && r.Signer == nil {
			t.Error("verified without a signer")
		}
		_, _ = ExtractContent(data)
	})
}
