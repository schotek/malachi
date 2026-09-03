// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package discover

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// autoconfig is what one document yielded.
type autoconfig struct {
	imap, smtp   *api.ServerConfig
	providerName string
}

// clientConfig mirrors the Thunderbird autoconfig format (version 1.1).
type clientConfig struct {
	XMLName   xml.Name        `xml:"clientConfig"`
	Providers []emailProvider `xml:"emailProvider"`
}

type emailProvider struct {
	DisplayName string        `xml:"displayName"`
	Incoming    []serverEntry `xml:"incomingServer"`
	Outgoing    []serverEntry `xml:"outgoingServer"`
}

type serverEntry struct {
	Type           string   `xml:"type,attr"`
	Hostname       string   `xml:"hostname"`
	Port           string   `xml:"port"`
	SocketType     string   `xml:"socketType"`
	Username       string   `xml:"username"`
	Authentication []string `xml:"authentication"`
}

// ErrTooBig is returned for documents over MaxAutoconfigBytes.
var ErrTooBig = errors.New("autoconfig document too big")

const maxProviderNameBytes = 256

// ParseClientConfig parses an autoconfig document. It reads at most
// MaxAutoconfigBytes, skips servers with a plaintext socket type or
// without password authentication, substitutes the %EMAIL…% placeholders,
// validates hosts and ports, and returns nil for endpoints it could not
// fill. Malformed XML is an error; an empty document is not.
func ParseClientConfig(r io.Reader, email string) (imap, smtp *api.ServerConfig, providerName string, err error) {
	lr := &io.LimitedReader{R: r, N: MaxAutoconfigBytes + 1}
	dec := xml.NewDecoder(lr)
	dec.Strict = true
	var doc clientConfig
	if err := dec.Decode(&doc); err != nil {
		if lr.N == 0 {
			return nil, nil, "", ErrTooBig
		}
		return nil, nil, "", fmt.Errorf("parse autoconfig: %w", err)
	}
	if lr.N == 0 {
		return nil, nil, "", ErrTooBig
	}
	if len(doc.Providers) == 0 {
		return nil, nil, "", nil
	}
	p := doc.Providers[0]
	imap = pickServer(p.Incoming, "imap", email)
	smtp = pickServer(p.Outgoing, "smtp", email)
	return imap, smtp, cleanProviderName(p.DisplayName), nil
}

// pickServer prefers an entry with cleartext password authentication and
// falls back to CRAM-style "password-encrypted", which we cannot use yet
// but which at least names the right host.
func pickServer(entries []serverEntry, typ, email string) *api.ServerConfig {
	var fallback *api.ServerConfig
	for _, e := range entries {
		if !strings.EqualFold(e.Type, typ) {
			continue
		}
		sc, cleartext, ok := convertServer(e, email)
		if !ok {
			continue
		}
		if cleartext {
			return sc
		}
		if fallback == nil {
			fallback = sc
		}
	}
	return fallback
}

func convertServer(e serverEntry, email string) (sc *api.ServerConfig, cleartext, ok bool) {
	var sec api.Security
	switch strings.ToUpper(strings.TrimSpace(e.SocketType)) {
	case "SSL":
		sec = api.SecurityTLS
	case "STARTTLS":
		sec = api.SecuritySTARTTLS
	default:
		return nil, false, false
	}
	hasPassword := false
	for _, a := range e.Authentication {
		switch strings.ToLower(strings.TrimSpace(a)) {
		case "password-cleartext", "plain":
			hasPassword, cleartext = true, true
		case "password-encrypted", "secure":
			hasPassword = true
		}
	}
	if !hasPassword {
		return nil, false, false
	}
	host := strings.ToLower(strings.TrimSpace(substitute(e.Hostname, email)))
	if !transport.ValidHost(host) {
		return nil, false, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(e.Port))
	if err != nil || port < 1 || port > 65535 {
		return nil, false, false
	}
	user := strings.TrimSpace(substitute(e.Username, email))
	if strings.Contains(user, "%") || len(user) > 256 || !utf8.ValidString(user) || strings.IndexFunc(user, unicode.IsControl) >= 0 {
		user = "" // the caller fills in the address
	}
	return &api.ServerConfig{Host: host, Port: port, Security: sec, Username: user, AuthMethod: api.AuthPassword}, cleartext, true
}

// substitute expands the placeholders Thunderbird defines.
func substitute(s, email string) string {
	local := email
	if i := strings.LastIndexByte(email, '@'); i >= 0 {
		local = email[:i]
	}
	r := strings.NewReplacer("%EMAILADDRESS%", email, "%EMAILLOCALPART%", local, "%EMAILDOMAIN%", Domain(email))
	return r.Replace(s)
}

func cleanProviderName(s string) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	if strings.IndexFunc(s, unicode.IsControl) >= 0 || len(s) > maxProviderNameBytes {
		return ""
	}
	return s
}

// fetchAutoconfig GETs one document. Anything but a parseable 200 counts
// as "not found" and is only logged.
func (d *Discoverer) fetchAutoconfig(ctx context.Context, u, email string) autoconfig {
	ctx, cancel := context.WithTimeout(ctx, HTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		d.Log.Debug("autoconfig request", "url", u, "err", err)
		return autoconfig{}
	}
	req.Header.Set("Accept", "text/xml, application/xml")
	resp, err := d.HTTP.Do(req)
	if err != nil {
		d.Log.Debug("autoconfig fetch", "url", u, "err", err)
		return autoconfig{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		d.Log.Debug("autoconfig fetch", "url", u, "status", resp.StatusCode)
		return autoconfig{}
	}
	imapCfg, smtpCfg, name, err := ParseClientConfig(resp.Body, email)
	if err != nil {
		d.Log.Debug("autoconfig parse", "url", u, "err", err)
		return autoconfig{}
	}
	return autoconfig{imap: imapCfg, smtp: smtpCfg, providerName: name}
}
