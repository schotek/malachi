package compose

import (
	"strings"
	"testing"
)

func TestParseMailto(t *testing.T) {
	p, err := ParseMailto("mailto:alice@example.invalid?subject=Hi%20there&body=Line1%0ALine2&cc=bob@example.invalid&Bcc=carol@example.invalid&to=dave@example.invalid&in-reply-to=x")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.To) != 2 || p.To[0].Address != "alice@example.invalid" || p.To[1].Address != "dave@example.invalid" {
		t.Errorf("To = %+v", p.To)
	}
	if len(p.CC) != 1 || len(p.BCC) != 1 || p.Subject != "Hi there" || p.BodyHTML != "Line1<br>Line2" {
		t.Errorf("fields: %+v", p)
	}

	p, err = ParseMailto("mailto:x@example.invalid?body=%3Cscript%3Ealert(1)%3C/script%3E")
	if err != nil || strings.Contains(p.BodyHTML, "<script") || !strings.Contains(p.BodyHTML, "&lt;script&gt;") {
		t.Errorf("hostile body: %+v %v", p, err)
	}

	if _, err := ParseMailto("https://example.invalid"); err == nil {
		t.Error("non-mailto accepted")
	}
	if p, err := ParseMailto("mailto:"); err != nil || len(p.To) != 0 {
		t.Errorf("empty mailto: %+v %v", p, err)
	}
}
