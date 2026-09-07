// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package config loads the daemon configuration from
// $XDG_CONFIG_HOME/malachi/config.toml and resolves XDG paths.
//
// Secrets never live here: passwords and tokens go to the system keyring
// (internal/auth). The config file holds only non-secret account settings and
// daemon options.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/schotek/malachi/backend/internal/account"
	"github.com/schotek/malachi/backend/pkg/api"
)

const appDir = "malachi"

// Config is the parsed config.toml.
type Config struct {
	// Sync holds daemon-wide synchronisation defaults.
	Sync SyncConfig `toml:"sync"`
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
