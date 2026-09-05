// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The pure helpers behind the attachment chips. i18n is not bound in
// tests, so the English msgids come back verbatim.

func TestChipAttachments(t *testing.T) {
	logo := api.Attachment{PartID: "1.2", Filename: "logo.png", ContentID: "logo@x", Inline: true}
	stray := api.Attachment{PartID: "1.3", Filename: "stray.png", ContentID: "stray@x", Inline: true}
	pdf := api.Attachment{PartID: "2", Filename: "report.pdf"}
	atts := []api.Attachment{logo, stray, pdf}
	html := &api.MessageBodyResult{BodyState: api.BodyFetched, HTML: "<p>x</p>", InlineParts: map[string]string{"logo@x": "1.2"}}

	cases := []struct {
		name string
		b    *api.MessageBodyResult
		want []string
	}{
		{"html shows the logo", html, []string{"1.3", "2"}},
		{"text only lists every part", &api.MessageBodyResult{BodyState: api.BodyFetched, Text: "hi", InlineParts: map[string]string{"logo@x": "1.2"}}, []string{"1.2", "1.3", "2"}},
		{"withheld html lists every part", &api.MessageBodyResult{BodyState: api.BodyFetched, Text: "hi", HTMLWithheld: true}, []string{"1.2", "1.3", "2"}},
		{"no inline parts", &api.MessageBodyResult{BodyState: api.BodyFetched, HTML: "<p>x</p>"}, []string{"1.2", "1.3", "2"}},
		{"another part under the same cid", &api.MessageBodyResult{BodyState: api.BodyFetched, HTML: "<p>x</p>", InlineParts: map[string]string{"logo@x": "9"}}, []string{"1.2", "1.3", "2"}},
		{"no body yet", nil, []string{"1.2", "1.3", "2"}},
	}
	for _, c := range cases {
		got := chipAttachments(atts, c.b)
		ids := make([]string, len(got))
		for i, a := range got {
			ids[i] = a.PartID
		}
		if len(ids) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, ids, c.want)
			continue
		}
		for i := range ids {
			if ids[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v", c.name, ids, c.want)
				break
			}
		}
	}

	// A part without a part number is never mistaken for a shown picture.
	odd := []api.Attachment{{ContentID: "x@x"}}
	if got := chipAttachments(odd, &api.MessageBodyResult{BodyState: api.BodyFetched, HTML: "<p>x</p>"}); len(got) != 1 {
		t.Errorf("empty PartID hidden: %v", got)
	}
}

func TestPartAvailable(t *testing.T) {
	small := api.Attachment{Size: 1024}
	huge := api.Attachment{Size: api.MaxAttachmentDataBytes + 1}
	edge := api.Attachment{Size: api.MaxAttachmentDataBytes}
	fetched := &api.MessageBodyResult{BodyState: api.BodyFetched}
	cases := []struct {
		name string
		a    api.Attachment
		b    *api.MessageBodyResult
		ok   bool
		why  string
	}{
		{"no body", small, nil, false, ""},
		{"pending", small, &api.MessageBodyResult{BodyState: api.BodyPending}, false, "This message has not been downloaded yet."},
		{"tooBig", small, &api.MessageBodyResult{BodyState: api.BodyTooBig}, false, "This message is too large to download."},
		{"failed", small, &api.MessageBodyResult{BodyState: api.BodyFailed}, false, "This message could not be read."},
		{"fetched", small, fetched, true, ""},
		{"at the cap", edge, fetched, true, ""},
		{"over the cap", huge, fetched, false, "Attachments over 16.0 MiB cannot be opened or saved yet."},
	}
	for _, c := range cases {
		ok, why := partAvailable(c.a, c.b)
		if ok != c.ok || why != c.why {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", c.name, ok, why, c.ok, c.why)
		}
	}
}

func TestExecutableAttachment(t *testing.T) {
	cases := []struct {
		name, ct string
		want     bool
	}{
		{"setup.exe", "application/octet-stream", true},
		{"invoice.pdf.exe", "application/pdf", true},
		{"Report.PDF", "application/pdf", false},
		{"run.SH", "text/plain", true},
		{"notes.txt", "text/plain", false},
		{"payload", "application/x-shellscript", true},
		{"payload", "application/x-executable; name=x", true},
		{"photo.jpg", "image/jpeg", false},
		{"launcher.desktop", "application/x-desktop", true},
		{"tool.jar", "application/java-archive", true},
		{"", "", false},
		{".bashrc", "", false}, // no extension of its own
	}
	for _, c := range cases {
		if got := executableAttachment(c.name, c.ct); got != c.want {
			t.Errorf("executableAttachment(%q, %q) = %v, want %v", c.name, c.ct, got, c.want)
		}
	}
}

func TestUniqueName(t *testing.T) {
	taken := map[string]bool{"a.txt": true, "a (2).txt": true, "b": true, "c.tar.gz": true}
	has := func(n string) bool { return taken[n] }
	cases := map[string]string{
		"a.txt":    "a (3).txt",
		"b":        "b (2)",
		"c.tar.gz": "c.tar (2).gz",
		"new.txt":  "new.txt",
	}
	for in, want := range cases {
		if got := uniqueName(in, has); got != want {
			t.Errorf("uniqueName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := uniqueName("x", func(string) bool { return true }); got != "x" {
		t.Errorf("giving up should return the name itself, got %q", got)
	}
}

func TestFileName(t *testing.T) {
	a := api.Attachment{Filename: "listed.pdf"}
	cases := []struct {
		res  api.MessagePartResult
		want string
	}{
		{api.MessagePartResult{Filename: "served.pdf"}, "served.pdf"},
		{api.MessagePartResult{}, "listed.pdf"},
		{api.MessagePartResult{Filename: "../../etc/passwd"}, "passwd"},
		{api.MessagePartResult{Filename: "/"}, "listed.pdf"},
	}
	for _, c := range cases {
		if got := fileName(&c.res, a); got != c.want {
			t.Errorf("fileName(%q) = %q, want %q", c.res.Filename, got, c.want)
		}
	}
	if got := fileName(&api.MessagePartResult{}, api.Attachment{}); got != "attachment" {
		t.Errorf("nameless part: got %q", got)
	}
}

func TestSaveAllSummary(t *testing.T) {
	if got := saveAllSummary(1, 0); got != "Saved 1 attachment" {
		t.Errorf("one: %q", got)
	}
	if got := saveAllSummary(3, 0); got != "Saved 3 attachments" {
		t.Errorf("three: %q", got)
	}
	if got := saveAllSummary(2, 1); got != "1 of 3 attachments could not be saved" {
		t.Errorf("failed: %q", got)
	}
}

func TestSweepOpenDir(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old")
	fresh := filepath.Join(dir, "fresh")
	for _, d := range []string{old, fresh} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	stale := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}
	sweepOpenDir(dir, time.Hour)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the old entry should be gone")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("the fresh entry should stay")
	}
	sweepOpenDir(filepath.Join(dir, "missing"), time.Hour) // no directory: no-op
}
