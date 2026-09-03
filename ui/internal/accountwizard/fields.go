// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package accountwizard is the "Add Account" dialog: identity → discovery →
// server settings → connection test → account.add. The backend discovers,
// tests and validates; this package only checks field syntax, fills port
// defaults and shows results.
package accountwizard

import (
	"net/mail"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Endpoint selects the IMAP or SMTP side of the settings.
type Endpoint int

const (
	EndpointIMAP Endpoint = iota
	EndpointSMTP
)

// Identity is what the first page collects.
type Identity struct {
	DisplayName string
	Email       string
	Password    string
}

// ServerFields mirrors the rows of one endpoint on the Servers page.
type ServerFields struct {
	Host     string
	Port     int
	Security api.Security
	Username string
}

// securityChoices is the order of the "Security" StringList in
// account_wizard.blp.
var securityChoices = []api.Security{api.SecurityTLS, api.SecuritySTARTTLS, api.SecurityNone}

// DefaultPort is the conventional port for a security mode.
func DefaultPort(kind Endpoint, sec api.Security) int {
	switch kind {
	case EndpointIMAP:
		if sec == api.SecurityTLS {
			return 993
		}
		return 143
	default:
		if sec == api.SecurityTLS {
			return 465
		}
		return 587
	}
}

// PortForSecurityChange keeps a custom port and only swaps the default of
// the previous mode for the default of the new one.
func PortForSecurityChange(kind Endpoint, port int, from, to api.Security) int {
	if from == to {
		return port
	}
	if port == 0 || port == DefaultPort(kind, from) {
		return DefaultPort(kind, to)
	}
	return port
}

func indexOfSecurity(s api.Security) uint {
	for i, c := range securityChoices {
		if c == s {
			return uint(i)
		}
	}
	return 0
}

func securityAt(i uint) api.Security {
	if i < uint(len(securityChoices)) {
		return securityChoices[i]
	}
	return api.SecurityTLS
}

// ValidateEmail trims and accepts only a bare address: no display name, no
// comments. The backend validates authoritatively; this is immediate
// feedback.
func ValidateEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Name != "" || a.Address != s {
		return "", false
	}
	return s, true
}

// Domain is the lower-cased part after the last '@', or "".
func Domain(email string) string {
	i := strings.LastIndexByte(email, '@')
	if i < 0 || i == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[i+1:])
}

// SuggestAccountName is the default account name: the address's domain.
func SuggestAccountName(email string) string {
	if d := Domain(email); d != "" {
		return d
	}
	return email
}

// GuessConfig is the fallback when discovery finds nothing.
func GuessConfig(email string) api.AccountConfig {
	d := Domain(email)
	return api.AccountConfig{
		Email: email,
		IMAP:  api.ServerConfig{Host: "imap." + d, Port: 993, Security: api.SecurityTLS, Username: email, AuthMethod: api.AuthPassword},
		SMTP:  api.ServerConfig{Host: "smtp." + d, Port: 587, Security: api.SecuritySTARTTLS, Username: email, AuthMethod: api.AuthPassword},
	}
}

// MergeIdentity copies what the identity page knows into a discovered or
// guessed configuration.
func MergeIdentity(cfg api.AccountConfig, id Identity) api.AccountConfig {
	cfg.Email = id.Email
	cfg.DisplayName = strings.TrimSpace(id.DisplayName)
	if strings.TrimSpace(cfg.Name) == "" {
		cfg.Name = SuggestAccountName(id.Email)
	}
	for _, sc := range []*api.ServerConfig{&cfg.IMAP, &cfg.SMTP} {
		if strings.TrimSpace(sc.Username) == "" {
			sc.Username = id.Email
		}
		sc.AuthMethod = api.AuthPassword
	}
	return cfg
}

// IdentityProblems flags the identity fields that cannot be sent.
type IdentityProblems struct{ Email, Password bool }

// Any reports whether anything is wrong.
func (p IdentityProblems) Any() bool { return p.Email || p.Password }

// ValidateIdentity checks address syntax and a non-empty password.
func ValidateIdentity(id Identity) IdentityProblems {
	_, ok := ValidateEmail(id.Email)
	return IdentityProblems{Email: !ok, Password: id.Password == ""}
}

// ServerProblems flags the server rows that cannot be sent.
type ServerProblems struct {
	IMAPHost, IMAPUser, SMTPHost, SMTPUser bool
}

// Any reports whether anything is wrong.
func (p ServerProblems) Any() bool { return p.IMAPHost || p.IMAPUser || p.SMTPHost || p.SMTPUser }

// ValidateServers flags empty hosts and user names. Ports are bounded by
// the spin rows; everything else is the backend's call.
func ValidateServers(imap, smtp ServerFields) ServerProblems {
	return ServerProblems{
		IMAPHost: strings.TrimSpace(imap.Host) == "",
		IMAPUser: strings.TrimSpace(imap.Username) == "",
		SMTPHost: strings.TrimSpace(smtp.Host) == "",
		SMTPUser: strings.TrimSpace(smtp.Username) == "",
	}
}

// BuildConfig assembles the wire configuration from the rows. It is the
// one place that sets the authentication method.
func BuildConfig(id Identity, name string, imap, smtp ServerFields) api.AccountConfig {
	email := strings.TrimSpace(id.Email)
	name = strings.TrimSpace(name)
	if name == "" {
		name = SuggestAccountName(email)
	}
	return api.AccountConfig{
		Name:        name,
		Email:       email,
		DisplayName: strings.TrimSpace(id.DisplayName),
		IMAP:        serverConfig(imap),
		SMTP:        serverConfig(smtp),
	}
}

func serverConfig(f ServerFields) api.ServerConfig {
	return api.ServerConfig{
		Host:       strings.TrimSpace(f.Host),
		Port:       f.Port,
		Security:   f.Security,
		Username:   strings.TrimSpace(f.Username),
		AuthMethod: api.AuthPassword,
	}
}

func credentialsFor(id Identity) api.Credentials {
	return api.Credentials{Password: id.Password}
}
