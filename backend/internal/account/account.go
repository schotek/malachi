// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package account defines the config.toml form of an account. It is used
// only for the bootstrap import at daemon start: the registry itself lives
// in the store (accounts table) and is managed by internal/core through the
// account.* methods. It never touches secrets; those belong to internal/auth.
package account

import "github.com/schotek/malachi/backend/pkg/api"

// Config is one [[accounts]] entry of config.toml: api.AccountConfig plus an
// optional stable local identifier and the enabled flag.
type Config struct {
	ID          string  `toml:"id"`
	Name        string  `toml:"name"`
	Email       string  `toml:"email"`
	DisplayName string  `toml:"display_name"`
	IMAP        Server  `toml:"imap"`
	SMTP        Server  `toml:"smtp"`
	OAuth2      *OAuth2 `toml:"oauth2"`
	// Enabled is a pointer so that a missing key means enabled, not paused.
	Enabled             *bool `toml:"enabled"`
	SyncIntervalSeconds int   `toml:"sync_interval_seconds"`
}

// Server mirrors api.ServerConfig with TOML tags.
type Server struct {
	Host       string `toml:"host"`
	Port       int    `toml:"port"`
	Security   string `toml:"security"` // tls | starttls | none
	Username   string `toml:"username"`
	AuthMethod string `toml:"auth_method"` // password | oauth2
}

// OAuth2 mirrors api.OAuth2Config with TOML tags.
type OAuth2 struct {
	Provider string   `toml:"provider"` // office365 | custom
	ClientID string   `toml:"client_id"`
	TenantID string   `toml:"tenant_id"`
	AuthURL  string   `toml:"auth_url"`
	TokenURL string   `toml:"token_url"`
	Scopes   []string `toml:"scopes"`
}

// IsEnabled reports the enabled flag with the missing-key default.
func (c Config) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// ToAPI converts the TOML form to the wire form.
func (c Config) ToAPI() api.AccountConfig {
	out := api.AccountConfig{
		Name:         c.Name,
		Email:        c.Email,
		DisplayName:  c.DisplayName,
		IMAP:         c.IMAP.toAPI(),
		SMTP:         c.SMTP.toAPI(),
		SyncInterval: c.SyncIntervalSeconds,
	}
	if c.OAuth2 != nil {
		out.OAuth2 = &api.OAuth2Config{
			Provider: c.OAuth2.Provider,
			ClientID: c.OAuth2.ClientID,
			TenantID: c.OAuth2.TenantID,
			AuthURL:  c.OAuth2.AuthURL,
			TokenURL: c.OAuth2.TokenURL,
			Scopes:   c.OAuth2.Scopes,
		}
	}
	return out
}

func (s Server) toAPI() api.ServerConfig {
	return api.ServerConfig{
		Host:       s.Host,
		Port:       s.Port,
		Security:   api.Security(s.Security),
		Username:   s.Username,
		AuthMethod: api.AuthMethod(s.AuthMethod),
	}
}
