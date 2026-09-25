// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package config loads the daemon configuration from
// $XDG_CONFIG_HOME/malachi/config.toml and resolves XDG paths.
//
// Secrets never live here: passwords and tokens go to the system keyring
// (internal/auth). The config file holds only non-secret account settings and
// daemon options — and, for the backend's own OAuth2 sign-in, the OAuth
// client registrations, whose client_secret is an installed-app secret
// (not a user secret; docs/security.md §6).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/schotek/malachi/backend/internal/account"
	"github.com/schotek/malachi/backend/pkg/api"
)

const appDir = "malachi"

// Config is the parsed config.toml.
type Config struct {
	// Sync holds daemon-wide synchronisation defaults.
	Sync SyncConfig `toml:"sync"`
	// OAuth2 names the OAuth clients of the backend's own sign-in
	// (account.oauthStart). None is built in: without a client id for a
	// provider its sign-in reports oauthClientMissing.
	OAuth2 OAuth2Config `toml:"oauth2"`
	// Accounts are bootstrap entries: at daemon start they are imported into
	// the store when no account with the same e-mail exists and the e-mail has
	// not been imported before (core.Backend.ImportConfigAccounts). The store
	// is authoritative and the daemon never writes this file.
	Accounts []account.Config `toml:"accounts"`
}

type SyncConfig struct {
	IntervalSeconds int `toml:"interval_seconds"`
	// OfflineDays bounds the local mail cache (0 = keep everything).
	OfflineDays int `toml:"offline_days"`
}

// OAuth2Config is the [oauth2.*] tables: one client registration per
// provider the backend signs in to itself.
type OAuth2Config struct {
	// Google is a Google Cloud OAuth client of type "Desktop app".
	Google OAuth2Client `toml:"google"`
	// Microsoft is a Microsoft Entra app registration with the "Mobile and
	// desktop applications" platform: a public client, no secret.
	Microsoft OAuth2Client `toml:"microsoft"`
}

// OAuth2Client is one client registration. ClientSecret applies to Google
// only (its Desktop clients present one at the token endpoint), Tenant to
// Microsoft only ("" = "common").
type OAuth2Client struct {
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
	Tenant       string `toml:"tenant"`
}

// maxOAuth2Field bounds client_id and client_secret; maxTenant the tenant.
const (
	maxOAuth2Field = 256
	maxTenant      = 64
)

// validate checks the [oauth2.*] tables. A value in the wrong table or of
// the wrong shape is a configuration error, like an unknown key.
func (o *OAuth2Config) validate() error {
	for _, c := range []struct {
		name string
		cl   *OAuth2Client
	}{{"oauth2.google", &o.Google}, {"oauth2.microsoft", &o.Microsoft}} {
		c.cl.ClientID = strings.TrimSpace(c.cl.ClientID)
		c.cl.ClientSecret = strings.TrimSpace(c.cl.ClientSecret)
		c.cl.Tenant = strings.TrimSpace(c.cl.Tenant)
		if !PrintableToken(c.cl.ClientID, maxOAuth2Field) {
			return fmt.Errorf("%s: client_id must be printable ASCII without spaces (at most %d bytes)", c.name, maxOAuth2Field)
		}
		if !PrintableToken(c.cl.ClientSecret, maxOAuth2Field) {
			return fmt.Errorf("%s: client_secret must be printable ASCII without spaces (at most %d bytes)", c.name, maxOAuth2Field)
		}
		if c.cl.ClientSecret != "" && c.cl.ClientID == "" {
			return fmt.Errorf("%s: client_secret needs client_id", c.name)
		}
	}
	if o.Google.Tenant != "" {
		return errors.New("oauth2.google: tenant applies to oauth2.microsoft only")
	}
	if o.Microsoft.ClientSecret != "" {
		return errors.New("oauth2.microsoft: client_secret does not apply (a \"Mobile and desktop\" registration is a public client)")
	}
	if o.Microsoft.Tenant != "" && !ValidTenant(o.Microsoft.Tenant) {
		return fmt.Errorf("oauth2.microsoft: tenant must be letters, digits, '.' and '-', not starting with '.' (at most %d bytes)", maxTenant)
	}
	return nil
}

// PrintableToken reports s empty or printable ASCII without spaces, at
// most limit bytes: the shape of an OAuth client id or secret.
func PrintableToken(s string, limit int) bool {
	if len(s) > limit {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] > '~' {
			return false
		}
	}
	return true
}

// ValidTenant reports a Microsoft tenant: a GUID, a domain or one of the
// well-known names — letters, digits, '.' and '-', 1 to 64 bytes, not
// starting with '.'. The tenant becomes a path segment of the endpoint
// URLs, where "." and ".." (or any leading dot) have no business.
func ValidTenant(s string) bool {
	if s == "" || len(s) > maxTenant || s[0] == '.' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

// WarnExposedSecret logs a warning when the file at path holds an OAuth
// client secret and other users may read it (group or other permission
// bits). The secret is an installed-app secret, not a password, but a
// readable one invites copying the registration. It reports whether it
// warned.
func WarnExposedSecret(log *slog.Logger, path string, c Config) bool {
	if c.OAuth2.Google.ClientSecret == "" && c.OAuth2.Microsoft.ClientSecret == "" {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	perm := fi.Mode().Perm()
	if perm&0o077 == 0 {
		return false
	}
	if log != nil {
		log.Warn("config file holds an OAuth client_secret and is readable by other users; chmod 600 it",
			"path", path, "mode", fmt.Sprintf("%04o", perm))
	}
	return true
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{Sync: SyncConfig{IntervalSeconds: 300, OfflineDays: 30}}
}

// Load reads path. A missing file is not an error: defaults are returned and
// the second result is false.
func Load(path string) (Config, bool, error) {
	cfg := Default()
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, false, nil
		}
		return cfg, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return cfg, true, fmt.Errorf("%s: unknown keys: %v", path, undecoded)
	}
	if err := cfg.OAuth2.validate(); err != nil {
		return cfg, true, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, true, nil
}

// Paths are the resolved XDG locations for this application.
type Paths struct {
	ConfigDir  string // $XDG_CONFIG_HOME/malachi
	DataDir    string // $XDG_DATA_HOME/malachi
	RuntimeDir string // $XDG_RUNTIME_DIR/malachi (api.SocketBase: app dir inside Flatpak)
	CacheDir   string // $XDG_CACHE_HOME/malachi
}

// ConfigFile is the default config path.
func (p Paths) ConfigFile() string { return filepath.Join(p.ConfigDir, "config.toml") }

// StoreFile is the default database path.
func (p Paths) StoreFile() string { return filepath.Join(p.DataDir, "store.db") }

// SocketFile is the default RPC socket path.
func (p Paths) SocketFile() string { return filepath.Join(p.RuntimeDir, "rpc.sock") }

// ResolvePaths applies the XDG Base Directory specification.
func ResolvePaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("home directory: %w", err)
	}
	p := Paths{
		ConfigDir: filepath.Join(envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config")), appDir),
		DataDir:   filepath.Join(envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share")), appDir),
		CacheDir:  filepath.Join(envOr("XDG_CACHE_HOME", filepath.Join(home, ".cache")), appDir),
	}
	// XDG_RUNTIME_DIR has no spec-defined fallback. Outside a session
	// (containers, ssh) fall back to a private directory under the cache dir
	// so the daemon still starts. Inside Flatpak the base is the app's own
	// runtime dir, shared between sandbox instances (api.SocketBase).
	if base := api.SocketBase(os.Getenv("XDG_RUNTIME_DIR"), os.Getenv("FLATPAK_ID")); base != "" {
		p.RuntimeDir = filepath.Join(base, appDir)
	} else {
		p.RuntimeDir = filepath.Join(p.CacheDir, "run")
	}
	return p, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
