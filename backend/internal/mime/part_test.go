// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"bytes"
	"errors"
	"testing"
)

func TestExtractPart(t *testing.T) {
	raw := readFile(t, "mixed-attachments.eml")

	png, err := ExtractPart(bytes.NewReader(raw), "1.2", DefaultLimits(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if png.ContentType != "image/png" || png.Filename != "logo.png" || png.ContentID != "img1@example.org" || !png.Inline || len(png.Body) != 64 {
		t.Fatalf("png part = %+v (body %d bytes)", png, len(png.Body))
	}
	if !bytes.HasPrefix(png.Body, []byte("\x89PNG")) {
		t.Fatalf("png body not decoded: %q", png.Body[:8])
	}

	pdf, err := ExtractPart(bytes.NewReader(raw), "2", DefaultLimits(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if pdf.ContentType != "application/pdf" || pdf.Inline || pdf.Filename == "" || len(pdf.Body) == 0 {
		t.Fatalf("pdf part = %+v", pdf)
	}
	// The numbering is the one Parse reports.
	p := parseFile(t, "mixed-attachments.eml")
	for _, a := range p.Attachments {
		got, err := ExtractPart(bytes.NewReader(raw), a.PartID, DefaultLimits(), 0)
		if err != nil {
			t.Errorf("part %s: %v", a.PartID, err)
			continue
		}
		if got.ContentType != a.ContentType || got.Filename != a.Filename || int64(len(got.Body)) != a.Size {
			t.Errorf("part %s = %+v, Parse says %+v", a.PartID, got, a)
		}
	}

	html, err := ExtractPart(bytes.NewReader(raw), "1.1", DefaultLimits(), 0)
	if err != nil || html.ContentType != "text/html" {
		t.Fatalf("html part = %+v, %v", html, err)
	}

	for _, id := range []string{"9", "1", "1.9", "2.1", "0"} {
		if _, err := ExtractPart(bytes.NewReader(raw), id, DefaultLimits(), 0); !errors.Is(err, ErrPartNotFound) {
			t.Errorf("part %q: err = %v, want ErrPartNotFound", id, err)
		}
	}
	if _, err := ExtractPart(bytes.NewReader(raw), "2", DefaultLimits(), 16); !errors.Is(err, ErrPartTooBig) {
		t.Errorf("cap: err = %v, want ErrPartTooBig", err)
	}
	if _, err := ExtractPart(bytes.NewReader(nil), "1", DefaultLimits(), 0); err == nil {
		t.Error("empty input: expected an error")
	}

	// A non-multipart message is part 1.
	simple := readFile(t, "simple-text.eml")
	one, err := ExtractPart(bytes.NewReader(simple), "1", DefaultLimits(), 0)
	if err != nil || one.ContentType != "text/plain" || len(one.Body) == 0 {
		t.Fatalf("simple part 1 = %+v, %v", one, err)
	}
}

func TestValidPartID(t *testing.T) {
	for _, ok := range []string{"1", "2.1", "10.3.7"} {
		if !ValidPartID(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", ".", "1.", ".1", "a", "1..2", "01", "1.02", "-1", "1/2", "../x"} {
		if ValidPartID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
