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

func TestPartState(t *testing.T) {
	small := api.Attachment{Size: 1024}
	remote := api.Attachment{Size: 512 << 10, Remote: true}
	huge := api.Attachment{Size: api.MaxAttachmentDataBytes + 1}
	hugeRemote := api.Attachment{Size: api.MaxAttachmentDataBytes + 1, Remote: true}
	edge := api.Attachment{Size: api.MaxAttachmentDataBytes}
	fetched := &api.MessageBodyResult{BodyState: api.BodyFetched}
	pending := &api.MessageBodyResult{BodyState: api.BodyPending}
	const onServer = "On the server only; it is downloaded when you open it"
	cases := []struct {
		name string
		a    api.Attachment
		b    *api.MessageBodyResult
		st   partAvailability
		why  string
	}{
		{"no body", small, nil, partWaiting, ""},
		{"no body, remote", remote, nil, partWaiting, ""},
		{"pending: message.download fetches the body", small, pending, partRemote, onServer},
		{"tooBig", small, &api.MessageBodyResult{BodyState: api.BodyTooBig}, partUnavailable, "This message is too large to download."},
		{"failed", small, &api.MessageBodyResult{BodyState: api.BodyFailed}, partUnavailable, "This message could not be read."},
		{"fetched", small, fetched, partLocal, ""},
		{"fetched, on the server only", remote, fetched, partRemote, onServer},
		{"at the cap", edge, fetched, partLocal, ""},
		{"over the cap", huge, fetched, partUnavailable, "Attachments over 16.0 MiB cannot be opened or saved yet."},
		{"over the cap beats remote", hugeRemote, fetched, partUnavailable, "Attachments over 16.0 MiB cannot be opened or saved yet."},
		{"over the cap while pending", huge, pending, partUnavailable, "Attachments over 16.0 MiB cannot be opened or saved yet."},
		{"unknown body state", small, &api.MessageBodyResult{BodyState: "brandNew"}, partWaiting, ""},
	}
	for _, c := range cases {
		st, why := partState(c.a, c.b)
		if st != c.st || why != c.why {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", c.name, st, why, c.st, c.why)
		}
	}
}

func TestAnyRemote(t *testing.T) {
	local := api.Attachment{PartID: "2", Size: 1024}
	remote := api.Attachment{PartID: "3", Size: 512 << 10, Remote: true}
	fetched := &api.MessageBodyResult{BodyState: api.BodyFetched}
	if anyRemote([]api.Attachment{local, local}, fetched) {
		t.Error("all local")
	}
	if !anyRemote([]api.Attachment{local, remote}, fetched) {
		t.Error("one on the server")
	}
	if !anyRemote([]api.Attachment{local}, &api.MessageBodyResult{BodyState: api.BodyPending}) {
		t.Error("a body not downloaded yet is fetched by the download too")
	}
	if anyRemote([]api.Attachment{remote}, nil) {
		t.Error("no body loaded: nothing to decide yet")
	}
	if anyRemote(nil, fetched) {
		t.Error("no attachments")
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

func TestAttachedMessage(t *testing.T) {
	cases := []struct {
		a    api.Attachment
		want bool
	}{
		{api.Attachment{Filename: "report.eml", ContentType: "message/rfc822"}, true},
		{api.Attachment{Filename: "attachment-1.eml", ContentType: "message/rfc822"}, true},
		{api.Attachment{Filename: "whatever", ContentType: "Message/RFC822; name=x"}, true},
		{api.Attachment{Filename: "Forwarded.EML", ContentType: "application/octet-stream"}, true},
		{api.Attachment{Filename: "report.eml.exe", ContentType: "application/octet-stream"}, false},
		{api.Attachment{Filename: "a.pdf", ContentType: "application/pdf"}, false},
		{api.Attachment{}, false},
	}
	for _, c := range cases {
		if got := attachedMessage(c.a); got != c.want {
			t.Errorf("attachedMessage(%q, %q) = %v, want %v", c.a.Filename, c.a.ContentType, got, c.want)
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

func TestOpenDirFor(t *testing.T) {
	tests := []struct {
		name                      string
		runtime, cache, flatpakID string
		want                      string
	}{
		{"runtime dir", "/run/user/1000", "/home/u/.cache", "", "/run/user/1000/malachi/open"},
		{"no runtime dir", "", "/home/u/.cache", "", "/home/u/.cache/malachi/open"},
		// The one part of the sandbox's runtime dir the host previewer can read.
		{"flatpak", "/run/user/1000", "/home/u/.var/app/io.github.schotek.Malachi/cache", "io.github.schotek.Malachi",
			"/run/user/1000/app/io.github.schotek.Malachi/malachi/open"},
		{"flatpak, no runtime dir", "", "/home/u/.var/app/x/cache", "x", "/home/u/.var/app/x/cache/malachi/open"},
	}
	for _, tc := range tests {
		if got := openDirFor(tc.runtime, tc.cache, tc.flatpakID); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
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

// purgeOpenDir takes the directory openDirFor names with everything in it,
// and refuses every other path without touching it.
func TestPurgeOpenDir(t *testing.T) {
	base := t.TempDir()
	dir := openDirFor(base, "", "")
	file := filepath.Join(dir, "x1", "report.pdf")
	keep := filepath.Join(base, "malachi", "keep")
	for _, d := range []string{filepath.Dir(file), keep} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(file, []byte("%PDF-1.7"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{
		"", ".", "/", "malachi/open", // relative: runtime and cache dir unset
		base, filepath.Join(base, "malachi"), keep,
		filepath.Join(base, "open"), filepath.Join(dir, "x1"),
		filepath.Join(dir, ".."),
	} {
		if err := purgeOpenDir(bad); err == nil {
			t.Errorf("purgeOpenDir(%q) should refuse", bad)
		}
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("a refused purge removed something: %v", err)
	}

	if err := purgeOpenDir(dir + string(filepath.Separator)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the directory for opening should be gone")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("its sibling should stay")
	}
	if err := purgeOpenDir(dir); err != nil {
		t.Errorf("a missing directory: %v", err)
	}

	// A link in its place goes; what it points to stays.
	target := filepath.Join(base, "elsewhere")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, dir); err != nil {
		t.Fatal(err)
	}
	if err := purgeOpenDir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Error("the link should be gone")
	}
	if _, err := os.Stat(filepath.Join(target, "notes.txt")); err != nil {
		t.Error("the link's target should stay")
	}
}
