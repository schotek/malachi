// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package secretservice implements auth.Keyring over the freedesktop Secret
// Service API (org.freedesktop.secrets), the D-Bus interface behind
// libsecret, GNOME Keyring and KeePassXC.
//
// Items are identified by the attributes app / account / key and live in
// the default collection. The session is "plain": the session bus is
// per-user and a process able to eavesdrop on it could already read the
// store and the RPC socket, so the encrypted DH session would not change
// the threat model (docs/security.md §6). Unlock prompts are the system's
// own dialogs; a dismissed prompt is a keyringError, never a plaintext
// fallback. The connection is opened lazily and re-opened once when the
// bus drops.
package secretservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	service     = "org.freedesktop.secrets"
	servicePath = dbus.ObjectPath("/org/freedesktop/secrets")
	// loginCollection is the fallback when the "default" alias is unset.
	loginCollection = dbus.ObjectPath("/org/freedesktop/secrets/collection/login")

	ifaceService    = "org.freedesktop.Secret.Service"
	ifaceCollection = "org.freedesktop.Secret.Collection"
	ifaceItem       = "org.freedesktop.Secret.Item"
	ifacePrompt     = "org.freedesktop.Secret.Prompt"

	noObject = dbus.ObjectPath("/") // "no prompt" / "no collection" in the Secret Service API

	// AppID is the value of the "app" attribute on every item we create.
	AppID = "io.github.schotek.Malachi"
	// Schema is the xdg:schema attribute; secret managers group by it.
	Schema = AppID + ".Secret"

	// Item attribute names. Values are identifiers, never secrets.
	AttrApp     = "app"
	AttrAccount = "account"
	AttrKey     = "key"
	AttrSchema  = "xdg:schema"

	// CallTimeout bounds one D-Bus round trip; PromptTimeout bounds the
	// user's interaction with an unlock dialog.
	CallTimeout   = 5 * time.Second
	PromptTimeout = 2 * time.Minute
)

// secret is the (oayays) struct of the Secret Service API.
type secret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// Client is an auth.Keyring over the session bus.
type Client struct {
	log  *slog.Logger
	dial func() (bus, error)

	mu      sync.Mutex
	conn    bus
	session dbus.ObjectPath
}

var _ auth.Keyring = (*Client)(nil)

// New returns a client that connects on first use.
func New(log *slog.Logger) *Client {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Client{log: log.With("component", "keyring"), dial: dialSessionBus}
}

// Close drops the bus connection. The client can still be used afterwards;
// it reconnects.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn, c.session = nil, ""
	return err
}

// Get returns the stored value or auth.ErrNoSecret.
func (c *Client) Get(ctx context.Context, account api.AccountID, key string) (string, error) {
	var value string
	err := c.withRetry(ctx, func(ctx context.Context, s *session) error {
		unlocked, locked, err := s.search(ctx, attributes(account, key))
		if err != nil {
			return err
		}
		if len(unlocked)+len(locked) == 0 {
			return auth.ErrNoSecret
		}
		if len(unlocked) == 0 {
			if unlocked, err = s.unlock(ctx, locked); err != nil {
				return err
			}
			if len(unlocked) == 0 {
				return api.NewError(api.CodeKeyringError, "secret stays locked")
			}
		}
		if n := len(unlocked) + len(locked); n > 1 {
			c.log.Warn("several keyring items match", "account", account, "key", key, "count", n)
		}
		sec, err := s.getSecret(ctx, unlocked[0])
		if err != nil {
			return err
		}
		value = string(sec.Value)
		return nil
	})
	return value, err
}

// Set stores value, replacing an existing item with the same attributes.
func (c *Client) Set(ctx context.Context, account api.AccountID, key, value string) error {
	return c.withRetry(ctx, func(ctx context.Context, s *session) error {
		coll, err := s.defaultCollection(ctx)
		if err != nil {
			return err
		}
		locked, err := s.isLocked(ctx, coll)
		if err != nil {
			return err
		}
		if locked {
			if _, err := s.unlock(ctx, []dbus.ObjectPath{coll}); err != nil {
				return err
			}
		}
		attrs := attributes(account, key)
		attrs[AttrSchema] = Schema
		props := map[string]dbus.Variant{
			ifaceItem + ".Label":      dbus.MakeVariant(label(account, key)),
			ifaceItem + ".Attributes": dbus.MakeVariant(attrs),
		}
		sec := secret{Session: s.path, Parameters: []byte{}, Value: []byte(value), ContentType: "text/plain"}
		var item, prompt dbus.ObjectPath
		if err := s.call(ctx, coll, ifaceCollection+".CreateItem", props, sec, true).Store(&item, &prompt); err != nil {
			return err
		}
		if item == noObject {
			if _, err := s.runPrompt(ctx, prompt); err != nil {
				return err
			}
		}
		return nil
	})
}

// Delete removes every item with the given attributes. A missing item is not
// an error.
func (c *Client) Delete(ctx context.Context, account api.AccountID, key string) error {
	return c.withRetry(ctx, func(ctx context.Context, s *session) error {
		unlocked, locked, err := s.search(ctx, attributes(account, key))
		if err != nil {
			return err
		}
		for _, item := range append(unlocked, locked...) {
			var prompt dbus.ObjectPath
			err := s.call(ctx, item, ifaceItem+".Delete").Store(&prompt)
			switch {
			case isNoSuchObject(err):
				continue
			case err != nil:
				return err
			}
			if prompt != noObject {
				if _, err := s.runPrompt(ctx, prompt); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// --- connection management -------------------------------------------------

// session is an open bus connection with a Secret Service session.
type session struct {
	conn bus
	path dbus.ObjectPath // the session object
}

func (s *session) obj(path dbus.ObjectPath) dbus.BusObject {
	return s.conn.Object(service, path)
}

// call performs one round trip under CallTimeout.
func (s *session) call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	return s.obj(path).CallWithContext(ctx, method, 0, args...)
}

// withRetry runs fn on the current session, reconnecting once when the bus
// went away underneath. Errors leave here mapped to the Keyring contract.
func (c *Client) withRetry(ctx context.Context, fn func(context.Context, *session) error) error {
	for attempt := 0; ; attempt++ {
		s, err := c.ensure(ctx)
		if err != nil {
			return mapErr(err)
		}
		err = fn(ctx, s)
		if err != nil && attempt == 0 && isTransient(err) {
			c.log.Debug("keyring connection lost, reconnecting", "err", err)
			c.reset(s)
			continue
		}
		return mapErr(err)
	}
}

func (c *Client) ensure(ctx context.Context) (*session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return &session{conn: c.conn, path: c.session}, nil
	}
	conn, err := c.dial()
	if err != nil {
		return nil, fmt.Errorf("connect to session bus: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	var output dbus.Variant
	var path dbus.ObjectPath
	err = conn.Object(service, servicePath).
		CallWithContext(cctx, ifaceService+".OpenSession", 0, "plain", dbus.MakeVariant("")).
		Store(&output, &path)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open secret service session: %w", err)
	}
	c.conn, c.session = conn, path
	return &session{conn: conn, path: path}, nil
}

// reset forgets s if it is still the current connection.
func (c *Client) reset(s *session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == s.conn {
		c.conn.Close()
		c.conn, c.session = nil, ""
	}
}

// --- Secret Service operations --------------------------------------------

func (s *session) search(ctx context.Context, attrs map[string]string) (unlocked, locked []dbus.ObjectPath, err error) {
	err = s.call(ctx, servicePath, ifaceService+".SearchItems", attrs).Store(&unlocked, &locked)
	return unlocked, locked, err
}

func (s *session) getSecret(ctx context.Context, item dbus.ObjectPath) (secret, error) {
	var sec secret
	err := s.call(ctx, item, ifaceItem+".GetSecret", s.path).Store(&sec)
	return sec, err
}

func (s *session) defaultCollection(ctx context.Context) (dbus.ObjectPath, error) {
	var coll dbus.ObjectPath
	if err := s.call(ctx, servicePath, ifaceService+".ReadAlias", "default").Store(&coll); err != nil {
		return "", err
	}
	if coll == noObject {
		coll = loginCollection
	}
	return coll, nil
}

func (s *session) isLocked(ctx context.Context, coll dbus.ObjectPath) (bool, error) {
	v, err := s.obj(coll).GetProperty(ifaceCollection + ".Locked")
	if err != nil {
		return false, err
	}
	locked, ok := v.Value().(bool)
	if !ok {
		return false, fmt.Errorf("Locked property is %T, not bool", v.Value())
	}
	return locked, nil
}

// unlock asks the service to unlock objects, driving a prompt if needed, and
// returns the objects that are unlocked afterwards.
func (s *session) unlock(ctx context.Context, objects []dbus.ObjectPath) ([]dbus.ObjectPath, error) {
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := s.call(ctx, servicePath, ifaceService+".Unlock", objects).Store(&unlocked, &prompt); err != nil {
		return nil, err
	}
	if prompt == noObject {
		return unlocked, nil
	}
	result, err := s.runPrompt(ctx, prompt)
	if err != nil {
		return nil, err
	}
	if more, ok := result.Value().([]dbus.ObjectPath); ok {
		unlocked = append(unlocked, more...)
	}
	return unlocked, nil
}

// runPrompt shows a Secret Service prompt and waits for Completed. The
// signal match is registered before Prompt() so it cannot be missed.
func (s *session) runPrompt(ctx context.Context, prompt dbus.ObjectPath) (dbus.Variant, error) {
	ch := make(chan *dbus.Signal, 8)
	s.conn.Signal(ch)
	defer s.conn.RemoveSignal(ch)

	opts := []dbus.MatchOption{
		dbus.WithMatchObjectPath(prompt),
		dbus.WithMatchInterface(ifacePrompt),
		dbus.WithMatchMember("Completed"),
	}
	if err := s.conn.AddMatchSignalContext(ctx, opts...); err != nil {
		return dbus.Variant{}, err
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
		defer cancel()
		_ = s.conn.RemoveMatchSignalContext(rctx, opts...)
	}()

	if err := s.call(ctx, prompt, ifacePrompt+".Prompt", "").Err; err != nil {
		return dbus.Variant{}, err
	}

	pctx, cancel := context.WithTimeout(ctx, PromptTimeout)
	defer cancel()
	for {
		select {
		case sig := <-ch:
			if sig == nil || sig.Path != prompt || sig.Name != ifacePrompt+".Completed" || len(sig.Body) < 2 {
				continue
			}
			dismissed, _ := sig.Body[0].(bool)
			if dismissed {
				return dbus.Variant{}, api.NewError(api.CodeKeyringError, "keyring prompt dismissed")
			}
			result, _ := sig.Body[1].(dbus.Variant)
			return result, nil
		case <-pctx.Done():
			dctx, dcancel := context.WithTimeout(context.Background(), CallTimeout)
			_ = s.obj(prompt).CallWithContext(dctx, ifacePrompt+".Dismiss", 0).Err
			dcancel()
			if ctx.Err() != nil {
				return dbus.Variant{}, ctx.Err()
			}
			return dbus.Variant{}, api.NewError(api.CodeKeyringError, "keyring prompt timed out")
		}
	}
}

// --- helpers ----------------------------------------------------------------

func attributes(account api.AccountID, key string) map[string]string {
	return map[string]string{AttrApp: AppID, AttrAccount: string(account), AttrKey: key}
}

// label is what secret managers show. It never carries the value.
func label(account api.AccountID, key string) string {
	return fmt.Sprintf("Malachi Mail: %s (%s)", account, key)
}

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
	case "org.freedesktop.DBus.Error.Disconnected",
		"org.freedesktop.DBus.Error.NoReply",
		"org.freedesktop.Secret.Error.NoSession":
		return true
	}
	return false
}

func isNoSuchObject(err error) bool {
	switch dbusErrorName(err) {
	case "org.freedesktop.DBus.Error.UnknownObject",
		"org.freedesktop.Secret.Error.NoSuchObject":
		return true
	}
	return false
}

// mapErr turns raw failures into the Keyring contract: ErrNoSecret,
// context.Canceled and *api.Error pass through, everything else is
// keyringError naming the D-Bus error, never a secret.
func mapErr(err error) error {
	if err == nil || errors.Is(err, auth.ErrNoSecret) || errors.Is(err, context.Canceled) {
		return err
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	if name := dbusErrorName(err); name != "" {
		return api.NewError(api.CodeKeyringError, "secret service: %s", name)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return api.NewError(api.CodeKeyringError, "secret service: timed out")
	}
	return api.NewError(api.CodeKeyringError, "secret service: %v", err)
}
