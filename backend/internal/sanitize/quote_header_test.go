// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package sanitize

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/mime"
)

// cyrillicO looks like the Latin o.
const cyrillicO rune = 0x043E

// An Outlook header block without a separator line above it: Outlook's
// text alternative of an HTML reply and its plain-text replies.
func TestTrimQuotedTextHeaderBlock(t *testing.T) {
	const old = "\n\nOld text.\n"
	cases := []struct {
		name string
		in   string
		want string // "" = unchanged
	}{
		{"english", "Hi.\n\nFrom: A <a@example.org>\nSent: Wednesday, October 1, 2026 4:05 PM\nTo: B\nSubject: RE: x" + old, "Hi."},
		{"date instead of sent", "Hi.\n\nFrom: A\nDate: 1 Oct 2026\nSubject: x" + old, "Hi."},
		{"czech", "Ahoj.\n\nOd: A\nOdesláno: středa 1. října 2026 9:12\nKomu: B\nPředmět: x" + old, "Ahoj."},
		{"german with cc", "Ja.\n\nVon: A\nGesendet: Mittwoch, 1. Oktober 2026 10:00\nAn: B\nCc: C\nBetreff: x" + old, "Ja."},
		{"french, space before the colon", "Oui.\n\nDe : A\nEnvoyé : mercredi 1 octobre 2026 10:00\nÀ : B\nObjet : x" + old, "Oui."},
		{"russian", "Да.\n\nОт: A\nОтправлено: 1 октября 2026 г. 10:00\nКому: B\nТема: x" + old, "Да."},
		{"crlf", "Hi.\r\n\r\nFrom: A\r\nSent: 1 Oct 2026\r\nTo: B\r\n\r\nOld\r\n", "Hi."},
		{"empty subject", "Hi.\n\nFrom: A\nSent: 1 Oct 2026\nTo: B\nSubject:" + old, "Hi."},
		{"a chain: the first block cuts", "Hi.\n\nFrom: A\nSent: 2 Oct 2026\nTo: B\n\nMiddle.\n\nFrom: B\nSent: 1 Oct 2026\nTo: A" + old, "Hi."},
		{"signature above", "Hi.\n\nJan\nPhone 123\n\nFrom: A\nSent: 1 Oct 2026\nTo: B" + old, "Hi.\n\nJan\nPhone 123"},

		// Nothing to trim.
		{"apple forward", "Begin forwarded message:\n\nFrom: A\nSubject: x\nDate: 1 October 2026 at 10:00:00 CEST\nTo: B" + old, ""},
		{"apple forward with a note", "FYI\n\nBegin forwarded message:\n\nFrom: A\nSubject: x\nDate: 1 Oct 2026\nTo: B" + old, ""},
		{"introduced in the text", "Head it like this:\n\nFrom: Sales\nSent: 6 Oct 2026\nTo: all" + old, ""},
		{"nothing above: a forward", "\nFrom: A\nSent: 1 Oct 2026\nTo: B" + old, ""},
		{"block in the first line", "From: A\nSent: 1 Oct 2026\nTo: B" + old, ""},
		{"no blank line before", "Hi.\nFrom: A\nSent: 1 Oct 2026\nTo: B" + old, ""},
		{"no blank line after", "Hi.\n\nFrom: A\nSent: 1 Oct 2026\nTo: B\nOld text.\n", ""},
		{"nothing below", "Hi.\n\nFrom: A\nSent: 1 Oct 2026\nTo: B\n\n\n", ""},
		{"nothing below, at the end", "Hi.\n\nFrom: A\nSent: 1 Oct 2026\nTo: B", ""},
		{"two lines", "Hi.\n\nFrom: A\nSent: 1 Oct 2026" + old, ""},
		{"no date in sent", "Hi.\n\nFrom: Sales\nSent: every Monday\nTo: everyone" + old, ""},
		{"no sent", "Hi.\n\nFrom: A\nTo: B\nSubject: x" + old, ""},
		{"no to or subject", "Hi.\n\nFrom: A\nSent: 1 Oct 2026\nCc: B" + old, ""},
		{"from not first", "Hi.\n\nSent: 1 Oct 2026\nFrom: A\nTo: B" + old, ""},
		{"empty from", "Hi.\n\nFrom:\nSent: 1 Oct 2026\nTo: B" + old, ""},
		{"indented", "Hi.\n\n  From: A\n  Sent: 1 Oct 2026\n  To: B" + old, ""},
		{"quoted", "Hi.\n\n>From: A\n>Sent: 1 Oct 2026\n>To: B" + old, ""},
		{"blank line inside", "Hi.\n\nFrom: A\n\nSent: 1 Oct 2026\nTo: B" + old, ""},
		{"lookalike label", "Hi.\n\nFr" + string(cyrillicO) + "m: A\nSent: 1 Oct 2026\nTo: B" + old, ""},
		{"zero-width in the label", "Hi.\n\nFr" + string(zeroWidthSpace) + "om: A\nSent: 1 Oct 2026\nTo: B" + old, ""},
		{"unknown label inside", "Hi.\n\nFrom: A\nX-Mailer: 2026\nSent: 1 Oct 2026\nTo: B" + old, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := TrimQuotedText(c.in)
			if c.want == "" {
				if ok || got != c.in {
					t.Fatalf("trimmed %q to %q", c.in, got)
				}
				return
			}
			if !ok || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, ok, c.want)
			}
		})
	}

	// Pathological texts stay cheap; a block counts only when it qualifies.
	block := "From: a\nSent: 1 Oct 2026\nTo: b\n"
	if _, ok := TrimQuotedText(strings.Repeat("\n"+block, 20000)); ok {
		t.Error("blocks with nothing of their own above were trimmed")
	}
	long := "Top.\n\nFrom: a\n" + strings.Repeat("Sent: 1 Oct 2026\n", 50000) + "\nOld\n"
	if got, ok := TrimQuotedText(long); ok {
		t.Errorf("a block longer than a header was trimmed to %q", got)
	}
	unclosed := strings.Repeat("\n"+block+"x\n", 15000)
	huge := "Top.\n" + unclosed + "\n" + block + "\nOld\n"
	if got, ok := TrimQuotedText(huge); !ok || got != strings.TrimRight("Top.\n"+unclosed, "\n") {
		t.Errorf("a block after many unclosed ones: %v", ok)
	}
	if s := strings.Repeat("Top.\n\n"+block+"\n", 40000); len(strings.Split(s, "\n")) > maxTextLines {
		if _, ok := TrimQuotedText(s); ok {
			t.Error("a text over maxTextLines lines was trimmed")
		}
	}
}

// The plain-text fixtures: an Outlook reply chain, and a mail whose own
// text holds header-looking lines that must stay.
func TestTrimQuotedTextHeaderBlockCorpus(t *testing.T) {
	cases := []struct {
		file string
		want string // "" = unchanged
	}{
		{"quote-outlook-plain-en.eml", "Hi Eva,\n\nthe problem is the second table: the totals do not add up.\n\nMartin"},
		{"quote-outlook-plain-hostile.eml", ""},
		{"quote-plain-text-cs.eml", "Dobrý den,\n\nsmlouvu podepíšu zítra.\n\nKarel"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(testdata, c.file))
			if err != nil {
				t.Fatal(err)
			}
			p, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
			if err != nil || p.HasHTML {
				t.Fatalf("parse: %v", err)
			}
			got, ok := TrimQuotedText(p.Text)
			got = strings.ReplaceAll(got, "\r", "")
			if c.want == "" {
				if ok {
					t.Fatalf("trimmed to %q", got)
				}
				return
			}
			if !ok || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, ok, c.want)
			}
		})
	}

	// The text alternative of Outlook's HTML reply: the text rules now cut
	// it where the HTML is cut.
	raw, err := os.ReadFile(filepath.Join(testdata, "quote-outlook-desktop-cs.eml"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	got, ok := TrimQuotedText(p.Text)
	if !ok || strings.Contains(got, "Odesláno") || strings.Contains(got, "posílám") || !strings.HasSuffix(got, "Jan") {
		t.Fatalf("outlook text alternative: %q, %v", got, ok)
	}
}
