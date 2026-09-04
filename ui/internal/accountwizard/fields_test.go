// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestDefaultPort(t *testing.T) {
	cases := []struct {
		kind Endpoint
		sec  api.Security
		want int
	}{
		{EndpointIMAP, api.SecurityTLS, 993}, {EndpointIMAP, api.SecuritySTARTTLS, 143}, {EndpointIMAP, api.SecurityNone, 143},
		{EndpointSMTP, api.SecurityTLS, 465}, {EndpointSMTP, api.SecuritySTARTTLS, 587}, {EndpointSMTP, api.SecurityNone, 587},
	}
	for _, c := range cases {
		if got := DefaultPort(c.kind, c.sec); got != c.want {
			t.Errorf("DefaultPort(%v, %s) = %d, want %d", c.kind, c.sec, got, c.want)
		}
	}
}

func TestPortForSecurityChange(t *testing.T) {
	if got := PortForSecurityChange(EndpointIMAP, 993, api.SecurityTLS, api.SecuritySTARTTLS); got != 143 {
		t.Errorf("default swap: %d", got)
	}
	if got := PortForSecurityChange(EndpointIMAP, 9993, api.SecurityTLS, api.SecuritySTARTTLS); got != 9993 {
		t.Errorf("custom kept: %d", got)
	}
	if got := PortForSecurityChange(EndpointSMTP, 587, api.SecuritySTARTTLS, api.SecuritySTARTTLS); got != 587 {
		t.Errorf("same mode: %d", got)
	}
	if got := PortForSecurityChange(EndpointSMTP, 0, api.SecurityTLS, api.SecurityNone); got != 587 {
		t.Errorf("zero port: %d", got)
	}
}

func TestSecurityChoices(t *testing.T) {
	for i, s := range securityChoices {
		if indexOfSecurity(s) != uint(i) || securityAt(uint(i)) != s {
			t.Errorf("round trip %d/%s", i, s)
		}
	}
	if securityAt(99) != api.SecurityTLS || indexOfSecurity("bogus") != 0 {
		t.Error("fallbacks")
	}
}

func TestValidateEmail(t *testing.T) {
	for in, ok := range map[string]bool{
		"me@example.org": true, "  me@example.org ": true,
		"Name <me@example.org>": false, "me@": false, "@example.org": false, "": false, "a b@example.org": false,
	} {
		if _, got := ValidateEmail(in); got != ok {
			t.Errorf("ValidateEmail(%q) = %v", in, got)
		}
	}
	if addr, _ := ValidateEmail("  me@example.org "); addr != "me@example.org" {
		t.Errorf("trim: %q", addr)
	}
}

func TestGuessAndMerge(t *testing.T) {
	g := GuessConfig("me@Example.org")
	if g.IMAP.Host != "imap.example.org" || g.IMAP.Port != 993 || g.IMAP.Security != api.SecurityTLS ||
		g.SMTP.Host != "smtp.example.org" || g.SMTP.Port != 587 || g.SMTP.Security != api.SecuritySTARTTLS ||
		g.IMAP.Username != "me@Example.org" || g.SMTP.AuthMethod != api.AuthPassword {
		t.Fatalf("guess = %+v", g)
	}
	if SuggestAccountName("me@Example.org") != "example.org" || SuggestAccountName("nope") != "nope" {
		t.Error("account name")
	}

	discovered := api.AccountConfig{Name: "  ", IMAP: api.ServerConfig{Host: "imap.x.org", Username: ""}, SMTP: api.ServerConfig{Host: "smtp.x.org", Username: "custom"}}
	m := MergeIdentity(discovered, Identity{DisplayName: " Me ", Email: "me@x.org", Password: "p"})
	if m.Name != "x.org" || m.Email != "me@x.org" || m.DisplayName != "Me" || m.IMAP.Username != "me@x.org" ||
		m.SMTP.Username != "custom" || m.IMAP.AuthMethod != api.AuthPassword || m.SMTP.AuthMethod != api.AuthPassword {
		t.Fatalf("merge = %+v", m)
	}
}

func TestValidateAndBuild(t *testing.T) {
	p := ValidateIdentity(Identity{Email: "bad", Password: ""}, true)
	if !p.Email || !p.Password || !p.Any() {
		t.Errorf("identity problems = %+v", p)
	}
	if ValidateIdentity(Identity{Email: "me@x.org", Password: "p"}, true).Any() {
		t.Error("valid identity flagged")
	}
	if ValidateIdentity(Identity{Email: "me@x.org"}, false).Any() {
		t.Error("optional password flagged")
	}

	sp := ValidateServers(ServerFields{Host: " ", Username: "u"}, ServerFields{Host: "smtp.x.org", Username: ""})
	if !sp.IMAPHost || sp.IMAPUser || sp.SMTPHost || !sp.SMTPUser {
		t.Errorf("server problems = %+v", sp)
	}

	cfg := BuildConfig(Identity{DisplayName: "Me", Email: " me@x.org ", Password: "p"}, "",
		ServerFields{Host: " imap.x.org ", Port: 993, Security: api.SecurityTLS, Username: "me@x.org"},
		ServerFields{Host: "smtp.x.org", Port: 587, Security: api.SecuritySTARTTLS, Username: "me@x.org"})
	if cfg.Name != "x.org" || cfg.Email != "me@x.org" || cfg.IMAP.Host != "imap.x.org" || cfg.IMAP.AuthMethod != api.AuthPassword ||
		cfg.SMTP.Port != 587 || cfg.SMTP.AuthMethod != api.AuthPassword || cfg.OAuth2 != nil {
		t.Fatalf("config = %+v", cfg)
	}
	if credentialsFor(Identity{Password: "p"}).Password != "p" {
		t.Error("credentials")
	}
}
