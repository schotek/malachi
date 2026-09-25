// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestResolvePathsSocket(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/home/u/.cache")
	for _, tc := range []struct {
		name, rt, flatpak, want string
	}{
		{"session", "/run/user/1000", "", "/run/user/1000/malachi/rpc.sock"},
		{"flatpak", "/run/user/1000", "io.github.schotek.Malachi", "/run/user/1000/app/io.github.schotek.Malachi/malachi/rpc.sock"},
		{"no runtime dir", "", "", "/home/u/.cache/malachi/run/rpc.sock"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_RUNTIME_DIR", tc.rt)
			t.Setenv("FLATPAK_ID", tc.flatpak)
			p, err := ResolvePaths()
			if err != nil {
				t.Fatal(err)
			}
			if got := p.SocketFile(); got != tc.want {
				t.Errorf("SocketFile = %q, want %q", got, tc.want)
			}
		})
	}
}

func writeConfig(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // umask may have narrowed it
		t.Fatal(err)
	}
	return path
}

func TestLoadOAuth2Clients(t *testing.T) {
	path := writeConfig(t, `
[oauth2.google]
client_id = " 123-abc.apps.googleusercontent.com "
client_secret = "GOCSPX-secret"

[oauth2.microsoft]
client_id = "00000000-1111-2222-3333-444444444444"
tenant = "contoso.onmicrosoft.com"

[[accounts]]
name = "Gmail"
email = "me@gmail.invalid"
  [accounts.imap]
  host = "imap.gmail.com"
  port = 993
  security = "tls"
  username = "me@gmail.invalid"
  auth_method = "oauth2"
  [accounts.smtp]
  host = "smtp.gmail.com"
  port = 465
  security = "tls"
  username = "me@gmail.invalid"
  auth_method = "oauth2"
  [accounts.oauth2]
  source = "daemon"
  provider = "google"
`, 0o600)
	cfg, found, err := Load(path)
	if err != nil || !found {
		t.Fatalf("Load: %v %v", found, err)
	}
	g, m := cfg.OAuth2.Google, cfg.OAuth2.Microsoft
	if g.ClientID != "123-abc.apps.googleusercontent.com" || g.ClientSecret != "GOCSPX-secret" || g.Tenant != "" {
		t.Fatalf("google = %+v", g)
	}
	if m.ClientID != "00000000-1111-2222-3333-444444444444" || m.Tenant != "contoso.onmicrosoft.com" || m.ClientSecret != "" {
		t.Fatalf("microsoft = %+v", m)
	}
	if len(cfg.Accounts) != 1 {
		t.Fatalf("accounts = %+v", cfg.Accounts)
	}
	a := cfg.Accounts[0].ToAPI()
	if a.OAuth2 == nil || a.OAuth2.Source != api.OAuth2SourceDaemon || a.OAuth2.Provider != api.OAuth2ProviderGoogle {
		t.Fatalf("account oauth2 = %+v", a.OAuth2)
	}
	if WarnExposedSecret(nil, path, cfg) {
		t.Fatal("warned about a 0600 file")
	}

	// Without the section nothing is configured.
	cfg, _, err = Load(writeConfig(t, "[sync]\ninterval_seconds = 120\n", 0o644))
	if err != nil || cfg.OAuth2 != (OAuth2Config{}) {
		t.Fatalf("empty: %+v %v", cfg.OAuth2, err)
	}
}

func TestLoadOAuth2Rejects(t *testing.T) {
	for name, body := range map[string]string{
		"unknown key":                "[oauth2.google]\nclient_id = \"a\"\nredirect_uri = \"http://x\"\n",
		"unknown provider":           "[oauth2.yahoo]\nclient_id = \"a\"\n",
		"tenant for google":          "[oauth2.google]\nclient_id = \"a\"\ntenant = \"common\"\n",
		"secret for microsoft":       "[oauth2.microsoft]\nclient_id = \"a\"\nclient_secret = \"s\"\n",
		"secret without id":          "[oauth2.google]\nclient_secret = \"s\"\n",
		"id with space":              "[oauth2.google]\nclient_id = \"a b\"\n",
		"id with control":            "[oauth2.microsoft]\nclient_id = \"a\\u0001b\"\n",
		"id too long":                "[oauth2.google]\nclient_id = \"" + strings.Repeat("a", 257) + "\"\n",
		"tenant with slash":          "[oauth2.microsoft]\nclient_id = \"a\"\ntenant = \"common/../x\"\n",
		"tenant too long":            "[oauth2.microsoft]\ntenant = \"" + strings.Repeat("a", 65) + "\"\n",
		"tenant dot-dot":             "[oauth2.microsoft]\nclient_id = \"a\"\ntenant = \"..\"\n",
		"tenant leading dot":         "[oauth2.microsoft]\nclient_id = \"a\"\ntenant = \".contoso.com\"\n",
		"non-ascii secret":           "[oauth2.google]\nclient_id = \"a\"\nclient_secret = \"sé\"\n",
		"account unknown oauth2 key": "[[accounts]]\nname = \"x\"\n[accounts.oauth2]\nsorce = \"daemon\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Load(writeConfig(t, body, 0o600)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestWarnExposedSecret(t *testing.T) {
	body := "[oauth2.google]\nclient_id = \"a\"\nclient_secret = \"GOCSPX-leak\"\n"
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	for _, tc := range []struct {
		mode os.FileMode
		body string
		want bool
	}{
		{0o600, body, false},
		{0o640, body, true},
		{0o604, body, true},
		{0o644, "[oauth2.google]\nclient_id = \"a\"\n", false}, // no secret
	} {
		buf.Reset()
		path := writeConfig(t, tc.body, tc.mode)
		cfg, _, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := WarnExposedSecret(log, path, cfg); got != tc.want {
			t.Errorf("mode %04o: warned = %v", tc.mode, got)
		}
		if tc.want && (!strings.Contains(buf.String(), "client_secret") || strings.Contains(buf.String(), "GOCSPX-leak")) {
			t.Errorf("mode %04o: log = %q", tc.mode, buf.String())
		}
	}
	if WarnExposedSecret(log, filepath.Join(t.TempDir(), "missing.toml"), Config{OAuth2: OAuth2Config{Google: OAuth2Client{ClientID: "a", ClientSecret: "s"}}}) {
		t.Error("warned about a missing file")
	}
}

func TestValidTenant(t *testing.T) {
	for s, want := range map[string]bool{
		"common": true, "organizations": true, "contoso.onmicrosoft.com": true,
		"72f988bf-86f1-41af-91ab-2d7cd011db47": true, "": false, "a b": false, "a/b": false, "ä": false,
		".": false, "..": false, "...": false, ".contoso.com": false, "..contoso": false, "contoso.": true,
	} {
		if got := ValidTenant(s); got != want {
			t.Errorf("ValidTenant(%q) = %v", s, got)
		}
	}
}
