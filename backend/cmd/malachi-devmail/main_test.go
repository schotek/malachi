// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"io"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
)

// Every sample set builds, every message parses, replies reference what
// they reply to, and the Jira set seeds a standalone site.
func TestSampleSets(t *testing.T) {
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.Local)
	for _, lang := range []string{"en", "cs"} {
		t.Run(lang, func(t *testing.T) {
			c, err := loadCorpus(lang)
			if err != nil {
				t.Fatal(err)
			}
			built, err := c.build(now)
			if err != nil {
				t.Fatal(err)
			}
			if len(built) != len(c.Messages) {
				t.Fatalf("%d messages built of %d", len(built), len(c.Messages))
			}
			folders := map[string]int{}
			for i, b := range built {
				m := c.Messages[i]
				msg, err := mail.ReadMessage(bytes.NewReader(b.Raw))
				if err != nil {
					t.Fatalf("%s: %v", m.ID, err)
				}
				if got := msg.Header.Get("Message-ID"); got != messageID(m.ID) {
					t.Errorf("%s: Message-ID %q", m.ID, got)
				}
				if m.InReplyTo != "" && msg.Header.Get("In-Reply-To") != messageID(m.InReplyTo) {
					t.Errorf("%s: In-Reply-To %q", m.ID, msg.Header.Get("In-Reply-To"))
				}
				if cte := msg.Header.Get("Content-Transfer-Encoding"); cte == "" {
					t.Errorf("%s: no Content-Transfer-Encoding", m.ID)
				}
				if _, err := msg.Header.AddressList("From"); err != nil {
					t.Errorf("%s: From: %v", m.ID, err)
				}
				if d, err := msg.Header.Date(); err != nil || !d.Equal(b.Date) {
					t.Errorf("%s: Date %v (%v), built %v", m.ID, d, err, b.Date)
				}
				body, _ := io.ReadAll(msg.Body)
				if len(m.Attachments) > 0 && !strings.Contains(string(body), "Content-Disposition: attachment") {
					t.Errorf("%s: no attachment part", m.ID)
				}
				if m.Invite != nil && !strings.Contains(string(body), "text/calendar") {
					t.Errorf("%s: no calendar part", m.ID)
				}
				if m.List != nil && msg.Header.Get("List-Unsubscribe") == "" {
					t.Errorf("%s: no List-Unsubscribe", m.ID)
				}
				folders[b.Folder]++
			}
			for _, f := range []string{"INBOX", "Sent", "Drafts", "Junk", "Trash", "Archive"} {
				if folders[f] == 0 {
					t.Errorf("no message in %s", f)
				}
			}
			if c.Jira == nil || len(c.Jira.Issues) == 0 {
				t.Fatal("no Jira set")
			}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			site := jiratest.NewStandalone(jiratest.Cloud, ln)
			defer site.Close()
			if err := seedJira(site, c.Jira, now); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(site.Site.String(), "http://127.0.0.1:") {
				t.Errorf("site %s", site.Site)
			}
		})
	}
}

// Relative times resolve against the clock, in its zone.
func TestAt(t *testing.T) {
	now := time.Date(2026, 10, 28, 9, 30, 0, 0, time.Local)
	cases := map[string]time.Time{
		"-1d 09:12": time.Date(2026, 10, 27, 9, 12, 0, 0, time.Local),
		"-3h":       time.Date(2026, 10, 28, 6, 30, 0, 0, time.Local),
		"-45m":      time.Date(2026, 10, 28, 8, 45, 0, 0, time.Local),
		"+2d 10:00": time.Date(2026, 10, 30, 10, 0, 0, 0, time.Local),
	}
	for spec, want := range cases {
		got, err := at(spec, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("%s: %v %v, want %v", spec, got, err, want)
		}
	}
	if _, err := at("yesterday", now); err == nil {
		t.Error("a bad spec passed")
	}
}

// The generated PDF has a valid cross-reference table.
func TestPDF(t *testing.T) {
	b := pdfBytes("Hello\n(world)", 40)
	if !bytes.HasPrefix(b, []byte("%PDF-1.4")) || !bytes.HasSuffix(b, []byte("%%EOF\n")) {
		t.Fatal("not a PDF")
	}
	if len(b) < 39*1024 {
		t.Errorf("padded to %d bytes, want about 40 KiB", len(b))
	}
	i := bytes.LastIndex(b, []byte("startxref\n"))
	var off int
	if _, err := fmtSscan(string(b[i+len("startxref\n"):]), &off); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b[off:], []byte("xref\n")) {
		t.Errorf("startxref %d does not point at the xref", off)
	}
}
