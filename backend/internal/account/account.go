// Package account manages account configuration: validation, persistence,
// and lookup by ID. It never touches secrets; those belong to internal/auth.
package account

import "github.com/GITHUB_USER/malachi/backend/pkg/api"

// Config is one account as stored on disk. It is the api.AccountConfig plus
// a stable local identifier.
type Config struct {
	ID          string `toml:"id"`
	Name        string `toml:"name"`
	Email       string `toml:"email"`
	DisplayName string `toml:"display_name"`
	IMAP        Server `toml:"imap"`
	SMTP        Server `toml:"smtp"`
	Enabled     bool   `toml:"enabled"`
	// TODO: oauth2 section (provider, client_id, tenant_id) once internal/auth
	// defines what it needs.
}

// Server mirrors api.ServerConfig with TOML tags.
type Server struct {
	Host       string `toml:"host"`
	Port       int    `toml:"port"`
	Security   string `toml:"security"` // tls | starttls | none
	Username   string `toml:"username"`
	AuthMethod string `toml:"auth_method"` // password | oauth2
}

// ToAPI converts the stored form to the wire form.
func (c Config) ToAPI() api.AccountConfig {
	return api.AccountConfig{
		Name:        c.Name,
		Email:       c.Email,
		DisplayName: c.DisplayName,
		IMAP:        c.IMAP.toAPI(),
		SMTP:        c.SMTP.toAPI(),
	}
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

// Validate checks structural sanity (not connectivity).
// TODO: hostnames, port ranges, security/port consistency, e-mail syntax.
func (c Config) Validate() error {
	return api.ErrNotImplemented
}

// Manager is the account registry used by the RPC layer and the sync engine.
// TODO: implement over internal/store.
type Manager interface {
	List() ([]Config, error)
	Get(id api.AccountID) (Config, error)
	Add(cfg Config) (api.AccountID, error)
	Remove(id api.AccountID, deleteLocalData bool) error
}
