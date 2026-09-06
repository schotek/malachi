// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package goa reads accounts and OAuth2 access tokens from GNOME Online
// Accounts (org.gnome.OnlineAccounts on the session bus). It is the token
// source for Microsoft 365 accounts: GOA owns the sign-in, the refresh
// token and the refresh itself; Malachi only asks for a valid access token
// and keeps it in memory for as long as GOA says it is valid.
//
// Rules (docs/security.md §6):
//   - tokens are never logged, never written to the store or the keyring;
//   - a sign-in problem is reported as authRequired and the user fixes it
//     in Settings → Online Accounts; the daemon never opens a browser.
//
// The client speaks raw D-Bus through godbus like internal/auth/secretservice:
// it connects lazily and reconnects once when the bus drops. Without a
// session bus (or without GOA) every call fails with unavailable, which the
// callers turn into "no linked accounts" or an account error.
package goa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	service     = "org.gnome.OnlineAccounts"
	managerPath = dbus.ObjectPath("/org/gnome/OnlineAccounts")
	// accountsPrefix is the object path prefix of every account.
	accountsPrefix = "/org/gnome/OnlineAccounts/Accounts/"

	ifaceObjectManager = "org.freedesktop.DBus.ObjectManager"
	ifaceAccount       = "org.gnome.OnlineAccounts.Account"
	ifaceMail          = "org.gnome.OnlineAccounts.Mail"
	ifaceOAuth2        = "org.gnome.OnlineAccounts.OAuth2Based"

	// ProviderMicrosoft365 is the GOA provider type of "Microsoft 365"
	// accounts; their tokens are scoped to Microsoft Graph.
	ProviderMicrosoft365 = "ms_graph"
	// ProviderGoogle is GOA's provider type of a Google account. Its token
	// carries the https://mail.google.com/ scope, so the account is used
	// over IMAP and SMTP with the servers its Mail interface names.
	ProviderGoogle = "google"

	// CallTimeout bounds a local round trip; TokenTimeout bounds
	// GetAccessToken, which may refresh over the network.
	CallTimeout  = 5 * time.Second
	TokenTimeout = 60 * time.Second

	// tokenSlack is how long before GOA's expiry a cached token is dropped.
	tokenSlack = 60 * time.Second
)

// idPattern is the shape of a GOA account id (the last path segment).
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,128}$`)

// ValidID reports whether id can be a GOA account id.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Account is what GOA exposes about one account, secrets excluded.
type Account struct {
	ID                   string // last segment of the object path, e.g. "account_1788512854_0"
	ProviderType         string // ProviderMicrosoft365, ProviderGoogle, …
	ProviderName         string // display name, untrusted text
	Identity             string
	PresentationIdentity string
	Email                string // Mail.EmailAddress; empty without the Mail interface
	Name                 string // Mail.Name
	MailDisabled         bool
	AttentionNeeded      bool
	OAuth2               bool         // implements OAuth2Based
	Mail                 MailSettings // the Mail interface's servers; zero without it
}

// MailSettings is what the Mail interface says about the account's IMAP
// and SMTP servers. The Google provider fills it in (imap.gmail.com,
// smtp.gmail.com, XOAUTH2); Microsoft 365 reports no servers, since its
// token has no IMAP scope.
type MailSettings struct {
	IMAPSupported bool
	IMAPHost      string
	IMAPUserName  string
	IMAPUseSSL    bool // implicit TLS
	IMAPUseTLS    bool // STARTTLS

	SMTPSupported   bool
	SMTPHost        string
	SMTPUserName    string
	SMTPUseAuth     bool
	SMTPUseSSL      bool
	SMTPUseTLS      bool
	SMTPAuthXOAuth2 bool
}

// Client talks to GOA over the session bus.
type Client struct {
	log  *slog.Logger
	dial func() (bus, error)
	now  func() time.Time

	mu     sync.Mutex
	conn   bus
	tokens map[string]cachedToken
}

type cachedToken struct {
	value   string
	expires time.Time
}

// New returns a client that connects on first use.
func New(log *slog.Logger) *Client {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Client{log: log.With("component", "goa"), dial: dialSessionBus, now: time.Now, tokens: map[string]cachedToken{}}
}

// Close drops the bus connection and the cached tokens. The client can
// still be used afterwards; it reconnects.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens = map[string]cachedToken{}
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// Accounts lists every account GOA manages, sorted by id. Without a session
// bus or GOA the error is unavailable.
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var out []Account
	err := c.withRetry(ctx, func(ctx context.Context, conn bus) error {
		var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
		if err := call(ctx, conn, managerPath, ifaceObjectManager+".GetManagedObjects", CallTimeout).Store(&objects); err != nil {
			return err
		}
		out = out[:0]
		for path, ifaces := range objects {
			a, ok := parseAccount(path, ifaces)
			if ok {
				out = append(out, a)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return nil
	})
	return out, err
}

// AccessToken returns a valid access token of the account and the moment
// GOA expects it to expire (zero when unknown). Tokens are cached until
// shortly before expiry; GOA refreshes on its side. A revoked sign-in or an
// account removed from GOA is authRequired; no bus / no GOA is unavailable.
func (c *Client) AccessToken(ctx context.Context, id string) (string, time.Time, error) {
	if !ValidID(id) {
		return "", time.Time{}, api.NewError(api.CodeInvalidArgument, "invalid GNOME Online Accounts id")
	}
	c.mu.Lock()
	if t, ok := c.tokens[id]; ok && !t.expires.IsZero() && c.now().Add(tokenSlack).Before(t.expires) {
		c.mu.Unlock()
		return t.value, t.expires, nil
	}
	c.mu.Unlock()

	var token string
	var expiresIn int32
	err := c.withRetry(ctx, func(ctx context.Context, conn bus) error {
		path := dbus.ObjectPath(accountsPrefix + id)
		err := call(ctx, conn, path, ifaceOAuth2+".GetAccessToken", TokenTimeout).Store(&token, &expiresIn)
		if err != nil && isNoSuchAccount(err) {
			return api.NewError(api.CodeAuthRequired, "account %q is no longer in GNOME Online Accounts", id)
		}
		return err
	})
	if err != nil {
		return "", time.Time{}, err
	}
	if token == "" {
		return "", time.Time{}, api.NewError(api.CodeAuthRequired, "GNOME Online Accounts returned no token for %q", id)
	}
	var expires time.Time
	if expiresIn > 0 {
		expires = c.now().Add(time.Duration(expiresIn) * time.Second)
	}
	c.mu.Lock()
	c.tokens[id] = cachedToken{value: token, expires: expires}
	c.mu.Unlock()
	return token, expires, nil
}

// Invalidate drops the cached token of an account, e.g. after the remote
// service rejected it; the next AccessToken asks GOA again.
func (c *Client) Invalidate(id string) {
	c.mu.Lock()
	delete(c.tokens, id)
	c.mu.Unlock()
}

// --- connection management -------------------------------------------------

func call(ctx context.Context, conn bus, path dbus.ObjectPath, method string, timeout time.Duration, args ...any) *dbus.Call {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return conn.Object(service, path).CallWithContext(ctx, method, 0, args...)
}

// withRetry runs fn on the current connection, reconnecting once when the
// bus went away underneath. Errors leave here mapped to *api.Error.
func (c *Client) withRetry(ctx context.Context, fn func(context.Context, bus) error) error {
	for attempt := 0; ; attempt++ {
		conn, err := c.ensure()
		if err != nil {
			return mapErr(err)
		}
		err = fn(ctx, conn)
		if err != nil && attempt == 0 && isTransient(err) {
			c.log.Debug("session bus connection lost, reconnecting", "err", err)
			c.reset(conn)
			continue
		}
		return mapErr(err)
	}
}

func (c *Client) ensure() (bus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn, nil
	}
	conn, err := c.dial()
	if err != nil {
		return nil, fmt.Errorf("connect to session bus: %w", err)
	}
	c.conn = conn
	return conn, nil
}

// reset forgets conn if it is still the current connection.
func (c *Client) reset(conn bus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == conn {
		c.conn.Close()
		c.conn = nil
	}
}

// --- decoding ----------------------------------------------------------------

func parseAccount(path dbus.ObjectPath, ifaces map[string]map[string]dbus.Variant) (Account, bool) {
	p := string(path)
	if len(p) <= len(accountsPrefix) || p[:len(accountsPrefix)] != accountsPrefix {
		return Account{}, false
	}
	acc, ok := ifaces[ifaceAccount]
	if !ok {
		return Account{}, false
	}
	a := Account{ID: p[len(accountsPrefix):]}
	if !ValidID(a.ID) {
		return Account{}, false
	}
	a.ProviderType = str(acc, "ProviderType")
	a.ProviderName = str(acc, "ProviderName")
	a.Identity = str(acc, "Identity")
	a.PresentationIdentity = str(acc, "PresentationIdentity")
	a.MailDisabled = boolean(acc, "MailDisabled")
	a.AttentionNeeded = boolean(acc, "AttentionNeeded")
	if mail, ok := ifaces[ifaceMail]; ok {
		a.Email = str(mail, "EmailAddress")
		a.Name = str(mail, "Name")
		a.Mail = MailSettings{
			IMAPSupported:   boolean(mail, "ImapSupported"),
			IMAPHost:        str(mail, "ImapHost"),
			IMAPUserName:    str(mail, "ImapUserName"),
			IMAPUseSSL:      boolean(mail, "ImapUseSsl"),
			IMAPUseTLS:      boolean(mail, "ImapUseTls"),
			SMTPSupported:   boolean(mail, "SmtpSupported"),
			SMTPHost:        str(mail, "SmtpHost"),
			SMTPUserName:    str(mail, "SmtpUserName"),
			SMTPUseAuth:     boolean(mail, "SmtpUseAuth"),
			SMTPUseSSL:      boolean(mail, "SmtpUseSsl"),
			SMTPUseTLS:      boolean(mail, "SmtpUseTls"),
			SMTPAuthXOAuth2: boolean(mail, "SmtpAuthXoauth2"),
		}
	}
	_, a.OAuth2 = ifaces[ifaceOAuth2]
	return a, true
}

func str(props map[string]dbus.Variant, key string) string {
	if v, ok := props[key]; ok {
		if s, ok := v.Value().(string); ok {
			return s
		}
	}
	return ""
}

func boolean(props map[string]dbus.Variant, key string) bool {
	if v, ok := props[key]; ok {
		if b, ok := v.Value().(bool); ok {
			return b
		}
	}
	return false
}

// --- errors ------------------------------------------------------------------

func dbusErrorName(err error) string {
	var perr *dbus.Error
	if errors.As(err, &perr) {
		return perr.Name
	}
	var verr dbus.Error
	if errors.As(err, &verr) {
		return verr.Name
	}
	return ""
}

// isTransient reports whether the bus connection is gone and a reconnect
// is worth one retry.
func isTransient(err error) bool {
	if errors.Is(err, dbus.ErrClosed) {
		return true
	}
	switch dbusErrorName(err) {
	case "org.freedesktop.DBus.Error.Disconnected", "org.freedesktop.DBus.Error.NoReply":
		return true
	}
	return false
}

// isNoSuchAccount recognises the errors for an account object that GOA no
// longer exports (removed in Settings) or that has no OAuth2 interface.
func isNoSuchAccount(err error) bool {
	switch dbusErrorName(err) {
	case "org.freedesktop.DBus.Error.UnknownObject",
		"org.freedesktop.DBus.Error.UnknownMethod",
		"org.freedesktop.DBus.Error.UnknownInterface":
		return true
	}
	return false
}

// mapErr turns raw failures into *api.Error: context errors and *api.Error
// pass through; no bus or no GOA is unavailable; a refused sign-in is
// authRequired; anything else is serverError naming the D-Bus error. The
// message never carries a token.
func mapErr(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	name := dbusErrorName(err)
	switch name {
	case "":
		if errors.Is(err, context.DeadlineExceeded) {
			return api.NewError(api.CodeServerError, "gnome online accounts: timed out")
		}
		return api.NewError(api.CodeUnavailable, "gnome online accounts: %v", err)
	case "org.freedesktop.DBus.Error.ServiceUnknown",
		"org.freedesktop.DBus.Error.NameHasNoOwner",
		"org.freedesktop.DBus.Error.Spawn.ExecFailed",
		"org.freedesktop.DBus.Error.Spawn.FileNotFound":
		return api.NewError(api.CodeUnavailable, "gnome online accounts not available: %s", name)
	case "org.gnome.OnlineAccounts.Error.NotAuthorized":
		return api.NewError(api.CodeAuthRequired, "gnome online accounts: sign-in required")
	}
	return api.NewError(api.CodeServerError, "gnome online accounts: %s", name)
}
