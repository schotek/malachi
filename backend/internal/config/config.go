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
)

const appDir = "malachi"

// Config is the parsed config.toml.
type Config struct {
	// Sync holds daemon-wide synchronisation defaults.
	Sync SyncConfig `toml:"sync"`
	// Accounts are non-secret account definitions. TODO: decide whether
	// accounts live in config.toml (user-editable, git-friendly) or in the
	// SQLite store (managed through account.add). Both are read here for now.
	Accounts []account.Config `toml:"accounts"`
}

type SyncConfig struct {
	IntervalSeconds int `toml:"interval_seconds"`
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{Sync: SyncConfig{IntervalSeconds: 300}}
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
	RuntimeDir string // $XDG_RUNTIME_DIR/malachi
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
	// so the daemon still starts.
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		p.RuntimeDir = filepath.Join(rt, appDir)
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
