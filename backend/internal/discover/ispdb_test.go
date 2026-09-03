// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package discover

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

const testdata = "../../testdata/autoconfig"

func parseFile(t *testing.T, name, email string) (imap, smtp *api.ServerConfig, provider string, err error) {
	t.Helper()
	f, err := os.Open(filepath.Join(testdata, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return ParseClientConfig(f, email)
}

func TestParseClientConfig(t *testing.T) {
	email := "me@example.org"

	t.Run("happy path", func(t *testing.T) {
		in, out, name, err := parseFile(t, "example-ssl-starttls.xml", email)
		if err != nil {
			t.Fatal(err)
		}
		if in == nil || in.Host != "imap.example.org" || in.Port != 993 || in.Security != api.SecurityTLS || in.Username != email || in.AuthMethod != api.AuthPassword {
			t.Fatalf("imap = %+v", in)
		}
		if out == nil || out.Host != "smtp.example.org" || out.Port != 587 || out.Security != api.SecuritySTARTTLS || out.Username != email {
			t.Fatalf("smtp = %+v", out)
		}
		if name != "Example Mail" {
			t.Fatalf("provider = %q", name)
		}
	})

	t.Run("placeholders and encrypted fallback", func(t *testing.T) {
		in, out, _, err := parseFile(t, "localpart.xml", email)
		if err != nil {
			t.Fatal(err)
		}
		if in == nil || in.Host != "mail.example.org" || in.Username != "me" {
			t.Fatalf("imap = %+v", in)
		}
		if out == nil || out.Port != 465 || out.Security != api.SecurityTLS || out.Username != "me" {
			t.Fatalf("smtp = %+v", out)
		}
	})

	t.Run("plain sockets refused", func(t *testing.T) {
		in, out, _, err := parseFile(t, "plain-socket.xml", email)
		if err != nil || in != nil || out != nil {
			t.Fatalf("got %+v %+v %v", in, out, err)
		}
	})

	t.Run("imap only", func(t *testing.T) {
		in, out, _, err := parseFile(t, "imap-only.xml", email)
		if err != nil || in == nil || out != nil {
			t.Fatalf("got %+v %+v %v", in, out, err)
		}
	})

	t.Run("oauth2 only skipped", func(t *testing.T) {
		in, out, _, err := parseFile(t, "oauth2-only.xml", email)
		if err != nil || in != nil || out != nil {
			t.Fatalf("got %+v %+v %v", in, out, err)
		}
	})

	t.Run("second entry with password wins", func(t *testing.T) {
		in, _, _, err := parseFile(t, "oauth2-then-password.xml", email)
		if err != nil || in == nil || in.Host != "legacy.example.org" {
			t.Fatalf("got %+v %v", in, err)
		}
	})

	t.Run("bad hosts and control chars", func(t *testing.T) {
		in, out, name, err := parseFile(t, "bad-host.xml", email)
		if err != nil || in != nil || out != nil || name != "" {
			t.Fatalf("got %+v %+v %q %v", in, out, name, err)
		}
	})

	t.Run("bad ports", func(t *testing.T) {
		in, out, _, err := parseFile(t, "bad-port.xml", email)
		if err != nil || in != nil || out != nil {
			t.Fatalf("got %+v %+v %v", in, out, err)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		if _, _, _, err := parseFile(t, "malformed.xml", email); err == nil {
			t.Fatal("expected a parse error")
		}
	})

	t.Run("entity bomb", func(t *testing.T) {
		start := time.Now()
		_, _, _, err := parseFile(t, "entity-bomb.xml", email)
		if err == nil {
			t.Fatal("custom entities must not be expanded")
		}
		if time.Since(start) > 2*time.Second {
			t.Fatal("entity bomb took too long")
		}
	})

	t.Run("deep nesting", func(t *testing.T) {
		in, out, _, err := parseFile(t, "deep-nesting.xml", email)
		if in != nil || out != nil {
			t.Fatalf("got %+v %+v %v", in, out, err)
		}
	})

	t.Run("no provider", func(t *testing.T) {
		in, out, _, err := parseFile(t, "no-provider.xml", email)
		if err != nil || in != nil || out != nil {
			t.Fatalf("got %+v %+v %v", in, out, err)
		}
	})

	t.Run("too big", func(t *testing.T) {
		doc := "<clientConfig version=\"1.1\"><emailProvider id=\"x\"><displayName>" +
			strings.Repeat("a", MaxAutoconfigBytes) + "</displayName></emailProvider></clientConfig>"
		_, _, _, err := ParseClientConfig(strings.NewReader(doc), email)
		if !errors.Is(err, ErrTooBig) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestDomain(t *testing.T) {
	for in, want := range map[string]string{"me@Example.ORG": "example.org", "nope": "", "me@": "", "a@b@c.org": "c.org"} {
		if got := Domain(in); got != want {
			t.Errorf("Domain(%q) = %q", in, got)
		}
	}
}
