// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package vcard

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func photoCard(t *testing.T, name string) (*Card, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "vcard", "photos", name))
	if err != nil {
		t.Fatal(err)
	}
	return mustParse(t, raw), raw
}

// TestPhotoCorpus: what Parse makes of each PHOTO of the corpus
// (docs/plans/address-book.md §9.2). The pictures themselves are judged by
// internal/contactphoto; here only what the card carries.
func TestPhotoCorpus(t *testing.T) {
	for _, tc := range []struct {
		file, mime, magic string
		uri               bool
		none              bool
	}{
		{file: "jpeg-3.0.vcf", mime: "image/jpeg", magic: "\xff\xd8\xff"},
		{file: "png-alpha-4.0.vcf", mime: "image/png", magic: "\x89PNG"},
		{file: "gif-2.1.vcf", mime: "image/gif", magic: "GIF8"},
		{file: "many-frames-gif.vcf", mime: "image/gif", magic: "GIF8"},
		{file: "bomb-png-40000.vcf", mime: "image/png", magic: "\x89PNG"},
		{file: "bomb-jpeg-65000.vcf", mime: "image/jpeg", magic: "\xff\xd8\xff"},
		{file: "truncated-jpeg.vcf", mime: "image/jpeg", magic: "\xff\xd8\xff"},
		{file: "type-mismatch.vcf", mime: "image/gif", magic: "\xff\xd8\xff"},
		{file: "wrong-mime.vcf", mime: "text/html", magic: "\xff\xd8\xff"},
		{file: "not-an-image.vcf", mime: "image/png", magic: "<html>"},
		{file: "base64-garbage.vcf", none: true},
		{file: "data-not-base64.vcf", none: true},
		{file: "uri-3.0.vcf", uri: true},
		{file: "uri-4.0.vcf", uri: true},
	} {
		t.Run(tc.file, func(t *testing.T) {
			c, _ := photoCard(t, tc.file)
			if c.PhotoURI != tc.uri {
				t.Errorf("PhotoURI = %v", c.PhotoURI)
			}
			if tc.uri || tc.none {
				if c.Photo != nil {
					t.Errorf("photo %q of %d bytes", c.Photo.MIME, len(c.Photo.Data))
				}
				return
			}
			if c.Photo == nil {
				t.Fatal("no photo")
			}
			if c.Photo.MIME != tc.mime || !bytes.HasPrefix(c.Photo.Data, []byte(tc.magic)) {
				t.Errorf("photo %q starting %q", c.Photo.MIME, c.Photo.Data[:min(8, len(c.Photo.Data))])
			}
			if !strings.HasSuffix(c.Emails[0].Address, "@photo.example") {
				t.Errorf("emails %v", c.Emails)
			}
			if !reflect.DeepEqual(Clean(c), c) {
				t.Error("Clean changed the card")
			}
		})
	}
}

func TestPhotoValues(t *testing.T) {
	for _, tc := range []struct {
		line string
		mime string
		data string
		uri  bool
	}{
		{line: "PHOTO;ENCODING=b;TYPE=image/png:aGk=", mime: "image/png", data: "hi"},
		{line: "PHOTO;TYPE=JPEG;ENCODING=BASE64:aGk", mime: "image/jpeg", data: "hi"},
		{line: "PHOTO;BASE64;JPEG:a G\tk=", mime: "image/jpeg", data: "hi"},
		{line: "PHOTO;ENCODING=b:aGk=", data: "hi"},
		{line: "PHOTO;ENCODING=b;TYPE=\"bad type\":aGk=", data: "hi"},
		{line: "PHOTO:DATA:IMAGE/GIF;BASE64,aGk=", mime: "image/gif", data: "hi"},
		{line: "PHOTO:data:image/png;charset=x;base64,aGk=", mime: "image/png", data: "hi"},
		{line: "PHOTO:data:;base64,aGk=", data: "hi"},
		{line: "PHOTO:data:image/png;base64,", mime: ""},
		{line: "PHOTO:data:image/png;base64", mime: ""},
		{line: "PHOTO;VALUE=URL:http://example.org/a.png", uri: true},
		{line: "PHOTO:https://example.org/a.png", uri: true},
		{line: "PHOTO:file:///etc/passwd", uri: true},
		{line: "PHOTO:not a link", mime: ""},
		{line: "PHOTO:", mime: ""},
	} {
		c := mustParse(t, []byte("BEGIN:VCARD\nVERSION:3.0\nFN:x\n"+tc.line+"\nEND:VCARD\n"))
		if c.PhotoURI != tc.uri {
			t.Errorf("%s: PhotoURI %v", tc.line, c.PhotoURI)
		}
		if tc.data == "" {
			if c.Photo != nil {
				t.Errorf("%s: photo %q", tc.line, c.Photo.Data)
			}
			continue
		}
		if c.Photo == nil || c.Photo.MIME != tc.mime || string(c.Photo.Data) != tc.data {
			t.Errorf("%s: photo %+v", tc.line, c.Photo)
		}
	}
}

// TestFirstPhotoOnly: a second PHOTO is not read.
func TestFirstPhotoOnly(t *testing.T) {
	c := mustParse(t, []byte("BEGIN:VCARD\nVERSION:3.0\nPHOTO;VALUE=uri:https://a.example/x\nPHOTO;ENCODING=b:aGk=\nEND:VCARD\n"))
	if !c.PhotoURI || c.Photo != nil {
		t.Errorf("photo %+v uri %v", c.Photo, c.PhotoURI)
	}
}

// TestBigPhotoIsDecoded: a PHOTO line over MaxLineBytes (but within the
// card's limit) is read, another property that long is not.
func TestBigPhotoIsDecoded(t *testing.T) {
	c, raw := photoCard(t, "../pathological/huge-base64.vcf")
	if len(raw) <= MaxLineBytes {
		t.Fatalf("sample of %d bytes", len(raw))
	}
	if c.Photo == nil || len(c.Photo.Data) < MaxLineBytes/2 || c.Photo.MIME != "image/png" {
		t.Fatalf("photo %v", c.Photo != nil)
	}
	long := "BEGIN:VCARD\nVERSION:3.0\nNOTE:" + strings.Repeat("a", MaxLineBytes+1) + "\nEND:VCARD\n"
	if c := mustParse(t, []byte(long)); c.Note != "" {
		t.Errorf("a long NOTE was read")
	}
}

// TestPatchKeepsPhoto: an edit of a card with a big PHOTO leaves the PHOTO
// lines byte for byte, and the photo read again is the same.
func TestPatchKeepsPhoto(t *testing.T) {
	for _, name := range []string{"../pathological/huge-base64.vcf", "jpeg-3.0.vcf", "gif-2.1.vcf", "uri-4.0.vcf"} {
		before, raw := photoCard(t, name)
		after := Clean(before)
		after.DisplayName = "Renamed"
		after.Photo, after.PhotoURI = nil, false // a client's card never carries one
		out, err := Patch(raw, before, after)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		start := bytes.Index(raw, []byte("PHOTO"))
		end := start + bytes.Index(raw[start:], []byte("\nEND:VCARD"))
		if start < 0 || !bytes.Contains(out, raw[start:end]) {
			t.Errorf("%s: the PHOTO lines changed", name)
		}
		got := mustParse(t, out)
		if got.DisplayName != "Renamed" || !reflect.DeepEqual(got.Photo, before.Photo) || got.PhotoURI != before.PhotoURI {
			t.Errorf("%s: %q, photo kept %v", name, got.DisplayName, reflect.DeepEqual(got.Photo, before.Photo))
		}
	}
}

// TestWriteHasNoPhoto: a new card is written without a PHOTO even when the
// model carries one.
func TestWriteHasNoPhoto(t *testing.T) {
	c := &Card{DisplayName: "A", Photo: &Photo{MIME: "image/png", Data: []byte("x")}, PhotoURI: true}
	for _, v := range []string{"3.0", "4.0"} {
		out := New(c, v, "uid-1")
		if bytes.Contains(bytes.ToUpper(out), []byte("PHOTO")) {
			t.Errorf("%s: %s", v, out)
		}
	}
}
