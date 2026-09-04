// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package goa

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeBus answers method calls from a handler table keyed by "path method".
type fakeBus struct {
	mu       sync.Mutex
	handlers map[string]func(args []any) ([]any, error)
	calls    []string
	closed   bool
}

func newFakeBus() *fakeBus {
	return &fakeBus{handlers: map[string]func([]any) ([]any, error){}}
}

func (b *fakeBus) on(path dbus.ObjectPath, method string, fn func(args []any) ([]any, error)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[string(path)+" "+method] = fn
}

func (b *fakeBus) reply(path dbus.ObjectPath, method string, out ...any) {
	b.on(path, method, func([]any) ([]any, error) { return out, nil })
}

func (b *fakeBus) count(path dbus.ObjectPath, method string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, c := range b.calls {
		if c == string(path)+" "+method {
			n++
		}
	}
	return n
}

func (b *fakeBus) Object(_ string, path dbus.ObjectPath) dbus.BusObject {
	return &fakeObject{b: b, path: path}
}
func (b *fakeBus) AddMatchSignalContext(context.Context, ...dbus.MatchOption) error    { return nil }
func (b *fakeBus) RemoveMatchSignalContext(context.Context, ...dbus.MatchOption) error { return nil }
func (b *fakeBus) Signal(chan<- *dbus.Signal)                                          {}
func (b *fakeBus) RemoveSignal(chan<- *dbus.Signal)                                    {}
func (b *fakeBus) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return nil
}

type fakeObject struct {
	b    *fakeBus
	path dbus.ObjectPath
}

func (o *fakeObject) CallWithContext(_ context.Context, method string, _ dbus.Flags, args ...any) *dbus.Call {
	o.b.mu.Lock()
	o.b.calls = append(o.b.calls, string(o.path)+" "+method)
	fn := o.b.handlers[string(o.path)+" "+method]
	o.b.mu.Unlock()
	call := &dbus.Call{Destination: service, Path: o.path, Method: method, Args: args}
	if fn == nil {
		call.Err = dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownObject", Body: []any{"no handler for " + method}}
		return call
	}
	call.Body, call.Err = fn(args)
	return call
}
func (o *fakeObject) Call(method string, flags dbus.Flags, args ...any) *dbus.Call {
	return o.CallWithContext(context.Background(), method, flags, args...)
}
func (o *fakeObject) GetProperty(string) (dbus.Variant, error) { panic("unused") }
func (o *fakeObject) Go(string, dbus.Flags, chan *dbus.Call, ...any) *dbus.Call {
	panic("unused")
}
func (o *fakeObject) GoWithContext(context.Context, string, dbus.Flags, chan *dbus.Call, ...any) *dbus.Call {
	panic("unused")
}
func (o *fakeObject) AddMatchSignal(string, string, ...dbus.MatchOption) *dbus.Call { panic("unused") }
func (o *fakeObject) RemoveMatchSignal(string, string, ...dbus.MatchOption) *dbus.Call {
	panic("unused")
}
func (o *fakeObject) StoreProperty(string, any) error { panic("unused") }
func (o *fakeObject) SetProperty(string, any) error   { panic("unused") }
func (o *fakeObject) Destination() string             { return service }
func (o *fakeObject) Path() dbus.ObjectPath           { return o.path }

const (
	msID       = "account_1788512854_0"
	msPath     = dbus.ObjectPath(accountsPrefix + msID)
	cloudPath  = dbus.ObjectPath(accountsPrefix + "account_1788289873_0")
	tokenValue = "EwBAAl3BAAUFFpUAo7J3Ve0bjLBWZWCclRC3EoAA"
)

func props(kv ...any) map[string]dbus.Variant {
	out := map[string]dbus.Variant{}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i].(string)] = dbus.MakeVariant(kv[i+1])
	}
	return out
}

func managedObjects() map[dbus.ObjectPath]map[string]map[string]dbus.Variant {
	return map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		managerPath: {},
		cloudPath: {
			ifaceAccount: props("ProviderType", "owncloud", "ProviderName", "Nextcloud", "Identity", "vlada@example.org",
				"PresentationIdentity", "vlada@example.org", "MailDisabled", false, "AttentionNeeded", false),
		},
		msPath: {
			ifaceAccount: props("ProviderType", ProviderMicrosoft365, "ProviderName", "Microsoft 365", "Identity", "me@contoso.com",
				"PresentationIdentity", "me@contoso.com", "MailDisabled", false, "AttentionNeeded", true),
			ifaceMail:   props("EmailAddress", "me@contoso.com", "Name", "Me Myself"),
			ifaceOAuth2: props("ClientId", "b155a604"),
		},
		"/org/gnome/OnlineAccounts/Accounts/../etc": {ifaceAccount: props("ProviderType", ProviderMicrosoft365)},
	}
}

func newClient(t *testing.T) (*Client, *fakeBus) {
	t.Helper()
	b := newFakeBus()
	c := New(nil)
	c.dial = func() (bus, error) { return b, nil }
	return c, b
}

func TestAccounts(t *testing.T) {
	c, b := newClient(t)
	b.reply(managerPath, ifaceObjectManager+".GetManagedObjects", managedObjects())

	got, err := c.Accounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("accounts = %+v", got)
	}
	// Sorted by id: the Nextcloud account first.
	if got[0].ProviderType != "owncloud" || got[0].Email != "" || got[0].OAuth2 {
		t.Fatalf("first = %+v", got[0])
	}
	ms := got[1]
	if ms.ID != msID || ms.ProviderType != ProviderMicrosoft365 || ms.ProviderName != "Microsoft 365" ||
		ms.Email != "me@contoso.com" || ms.Name != "Me Myself" || !ms.AttentionNeeded || ms.MailDisabled || !ms.OAuth2 {
		t.Fatalf("microsoft = %+v", ms)
	}
}

func TestAccessTokenCachesUntilExpiry(t *testing.T) {
	c, b := newClient(t)
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	b.reply(msPath, ifaceOAuth2+".GetAccessToken", tokenValue, int32(3600))

	tok, exp, err := c.AccessToken(context.Background(), msID)
	if err != nil || tok != tokenValue || !exp.Equal(now.Add(time.Hour)) {
		t.Fatalf("got %q %v %v", tok, exp, err)
	}
	if _, _, err := c.AccessToken(context.Background(), msID); err != nil {
		t.Fatal(err)
	}
	if n := b.count(msPath, ifaceOAuth2+".GetAccessToken"); n != 1 {
		t.Fatalf("GetAccessToken calls = %d, want 1 (cached)", n)
	}

	now = now.Add(time.Hour - 30*time.Second) // inside the slack
	if _, _, err := c.AccessToken(context.Background(), msID); err != nil {
		t.Fatal(err)
	}
	if n := b.count(msPath, ifaceOAuth2+".GetAccessToken"); n != 2 {
		t.Fatalf("GetAccessToken calls = %d, want 2 (refreshed)", n)
	}

	c.Invalidate(msID)
	if _, _, err := c.AccessToken(context.Background(), msID); err != nil {
		t.Fatal(err)
	}
	if n := b.count(msPath, ifaceOAuth2+".GetAccessToken"); n != 3 {
		t.Fatalf("GetAccessToken calls = %d, want 3 (invalidated)", n)
	}
}

func TestAccessTokenUnknownExpiryIsNotCached(t *testing.T) {
	c, b := newClient(t)
	b.reply(msPath, ifaceOAuth2+".GetAccessToken", tokenValue, int32(0))
	for i := 0; i < 2; i++ {
		if _, exp, err := c.AccessToken(context.Background(), msID); err != nil || !exp.IsZero() {
			t.Fatalf("got %v %v", exp, err)
		}
	}
	if n := b.count(msPath, ifaceOAuth2+".GetAccessToken"); n != 2 {
		t.Fatalf("GetAccessToken calls = %d, want 2", n)
	}
}

func TestAccessTokenErrors(t *testing.T) {
	c, b := newClient(t)

	// Sign-in revoked in GOA.
	b.on(msPath, ifaceOAuth2+".GetAccessToken", func([]any) ([]any, error) {
		return nil, dbus.Error{Name: "org.gnome.OnlineAccounts.Error.NotAuthorized", Body: []any{"token " + tokenValue}}
	})
	_, _, err := c.AccessToken(context.Background(), msID)
	if code(t, err) != api.CodeAuthRequired || strings.Contains(err.Error(), tokenValue) {
		t.Fatalf("not authorized: %v", err)
	}

	// Account removed from GOA: no object at the path.
	_, _, err = c.AccessToken(context.Background(), "account_999_0")
	if code(t, err) != api.CodeAuthRequired {
		t.Fatalf("removed account: %v", err)
	}

	// Empty token.
	b.reply(msPath, ifaceOAuth2+".GetAccessToken", "", int32(0))
	if _, _, err := c.AccessToken(context.Background(), msID); code(t, err) != api.CodeAuthRequired {
		t.Fatalf("empty token: %v", err)
	}

	// Anything else is serverError naming the D-Bus error.
	b.on(msPath, ifaceOAuth2+".GetAccessToken", func([]any) ([]any, error) {
		return nil, dbus.Error{Name: "org.gnome.OnlineAccounts.Error.Failed"}
	})
	_, _, err = c.AccessToken(context.Background(), msID)
	if code(t, err) != api.CodeServerError || !strings.Contains(err.Error(), "Error.Failed") {
		t.Fatalf("failed: %v", err)
	}

	// Malformed ids never reach the bus.
	if _, _, err := c.AccessToken(context.Background(), "../x"); code(t, err) != api.CodeInvalidArgument {
		t.Fatalf("bad id: %v", err)
	}
}

func TestUnavailable(t *testing.T) {
	c := New(nil)
	c.dial = func() (bus, error) { return nil, errors.New("no session bus") }
	if _, err := c.Accounts(context.Background()); code(t, err) != api.CodeUnavailable {
		t.Fatalf("no bus: %v", err)
	}

	b := newFakeBus()
	b.on(managerPath, ifaceObjectManager+".GetManagedObjects", func([]any) ([]any, error) {
		return nil, dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown"}
	})
	c.dial = func() (bus, error) { return b, nil }
	if _, err := c.Accounts(context.Background()); code(t, err) != api.CodeUnavailable {
		t.Fatalf("no GOA: %v", err)
	}
}

func TestReconnectsOnce(t *testing.T) {
	c, b := newClient(t)
	dials := 0
	c.dial = func() (bus, error) { dials++; return b, nil }
	first := true
	b.on(managerPath, ifaceObjectManager+".GetManagedObjects", func([]any) ([]any, error) {
		if first {
			first = false
			return nil, dbus.ErrClosed
		}
		return []any{managedObjects()}, nil
	})
	if got, err := c.Accounts(context.Background()); err != nil || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
	if dials != 2 {
		t.Fatalf("dials = %d", dials)
	}
	if !b.closed {
		t.Fatal("dead connection not closed")
	}
}

func TestCancelPassesThrough(t *testing.T) {
	c, b := newClient(t)
	b.on(msPath, ifaceOAuth2+".GetAccessToken", func([]any) ([]any, error) {
		return nil, context.Canceled
	})
	if _, _, err := c.AccessToken(context.Background(), msID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func code(t *testing.T, err error) api.ErrorCode {
	t.Helper()
	var e *api.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *api.Error, got %T: %v", err, err)
	}
	return e.Code
}
