// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"bytes"
	"strings"
	"testing"
)

// TestOpaqueUnwrapped: opaque S/MIME signed data reads as its content
// (plan S14), its parts numbered inside the wrapper, and ExtractPart finds
// them by those numbers.
func TestOpaqueUnwrapped(t *testing.T) {
	raw := readFile(t, "smime-valid-opaque.eml")
	p, err := Parse(bytes.NewReader(raw), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !p.Opaque || !p.Crypto {
		t.Fatalf("opaque=%v crypto=%v", p.Opaque, p.Crypto)
	}
	if p.Text != "Opaque hello from Alice." || !p.HasHTML || !strings.Contains(p.RawHTML, "cid:logo@example.org") {
		t.Errorf("text %q html %q", p.Text, p.RawHTML)
	}
	if p.TextPartID != "1.1.1" || p.HTMLPartID != "1.1.2" {
		t.Errorf("text %s html %s", p.TextPartID, p.HTMLPartID)
	}
	var ids []string
	for _, a := range p.Attachments {
		ids = append(ids, a.PartID+"="+a.ContentType)
		if strings.Contains(a.Filename, "p7m") {
			t.Errorf("the wrapper is listed: %+v", a)
		}
	}
	if strings.Join(ids, ",") != "1.2=image/png,1.3=application/pdf" || !p.HasAttachments {
		t.Errorf("attachments %v", ids)
	}
	if !ValidPartID("1.2") {
		t.Fatal("inner number not valid")
	}
	img, err := ExtractPart(bytes.NewReader(raw), "1.2", DefaultLimits(), 1<<20)
	if err != nil || !bytes.HasPrefix(img.Body, []byte("\x89PNG")) || img.ContentID != "logo@example.org" {
		t.Fatalf("part 1.2: %+v %v", img, err)
	}
	if _, err := ExtractPart(bytes.NewReader(raw), "1", DefaultLimits(), 1<<20); err != ErrPartNotFound {
		t.Errorf("the wrapper is a part: %v", err)
	}
}

func TestOpaqueNested(t *testing.T) {
	p, err := Parse(bytes.NewReader(readFile(t, "smime-opaque-in-opaque.eml")), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !p.Opaque || p.TextPartID != "1.1.1" || !strings.HasPrefix(p.Text, "Hello Bob,") || len(p.Attachments) != 0 {
		t.Errorf("%+v", p)
	}
}

// TestOpaqueBroken: data that does not unwrap stays the attachment it was.
func TestOpaqueBroken(t *testing.T) {
	p, err := Parse(bytes.NewReader(readFile(t, "smime-asn1-huge.eml")), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if p.Opaque || len(p.Attachments) != 1 || p.Attachments[0].Filename != "smime.p7m" {
		t.Errorf("%+v", p)
	}
	// Enveloped data is not unwrapped.
	msg := "Content-Type: application/pkcs7-mime; smime-type=enveloped-data; name=smime.p7m\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\nMIAGCSqGSIb3DQEHA6CAMIACAQAx\r\n"
	p = parseString(t, msg, DefaultLimits())
	if p.Opaque || len(p.Attachments) != 1 {
		t.Errorf("enveloped: %+v", p)
	}
}

// TestOpaqueInMixed: a wrapper as the first part of a multipart/mixed
// (a list's footer after it) unwraps; one deeper does not.
func TestOpaqueInMixed(t *testing.T) {
	raw := readFile(t, "smime-valid-opaque.eml")
	_, body, _ := bytes.Cut(raw, []byte("\r\n\r\n"))
	part := "Content-Type: application/pkcs7-mime; smime-type=signed-data\r\nContent-Transfer-Encoding: base64\r\n\r\n" + string(body)
	mixed := "Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\n" + part + "\r\n--b\r\nContent-Type: text/plain\r\n\r\nfooter\r\n--b--\r\n"
	p := parseString(t, mixed, DefaultLimits())
	if !p.Opaque || p.TextPartID != "1.1.1" {
		t.Errorf("mixed: %+v", p)
	}
	deeper := "Content-Type: multipart/mixed; boundary=a\r\n\r\n--a\r\nContent-Type: text/plain\r\n\r\nfirst\r\n--a\r\n" + part + "\r\n--a--\r\n"
	p = parseString(t, deeper, DefaultLimits())
	if p.Opaque || len(p.Attachments) != 1 {
		t.Errorf("second part unwrapped: %+v", p)
	}
}

func FuzzOpaqueUnwrap(f *testing.F) {
	for _, name := range []string{"smime-valid-opaque.eml", "smime-opaque-in-opaque.eml", "smime-asn1-huge.eml"} {
		raw := readFile(f, name)
		_, body, _ := bytes.Cut(raw, []byte("\r\n\r\n"))
		f.Add(body)
	}
	limits := DefaultLimits()
	limits.MaxTextBytes = 64 << 10
	f.Fuzz(func(t *testing.T, b64 []byte) {
		msg := "Content-Type: application/pkcs7-mime; smime-type=signed-data\r\nContent-Transfer-Encoding: base64\r\n\r\n" + string(b64)
		p, err := Parse(strings.NewReader(msg), limits)
		if err != nil {
			return
		}
		checkInvariants(t, p, limits)
		for _, a := range p.Attachments {
			if !ValidPartID(a.PartID) {
				t.Errorf("part id %q", a.PartID)
			}
		}
	})
}
