// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package secretservice

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeBus answers method calls from a handler table keyed by "path method"
// and delivers signals to the registered channel.
type fakeBus struct {
	mu       sync.Mutex
	handlers map[string]func(args []any) ([]any, error)
	props    map[string]any // "path property" → value
	calls    []string       // "path method" in order
	sigCh    chan<- *dbus.Signal
	closed   bool
}

func newFakeBus() *fakeBus {
	return &fakeBus{handlers: map[string]func([]any) ([]any, error){}, props: map[string]any{}}
}

func (b *fakeBus) on(path dbus.ObjectPath, method string, fn func(args []any) ([]any, error)) {
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

func (b *fakeBus) emit(sig *dbus.Signal) {
	b.mu.Lock()
	ch := b.sigCh
	b.mu.Unlock()
	if ch != nil {
		ch <- sig
	}
}

func (b *fakeBus) Object(_ string, path dbus.ObjectPath) dbus.BusObject {
	return &fakeObject{b: b, path: path}
}
func (b *fakeBus) AddMatchSignalContext(context.Context, ...dbus.MatchOption) error {
	return nil
}
func (b *fakeBus) RemoveMatchSignalContext(context.Context, ...dbus.MatchOption) error {
	return nil
}
func (b *fakeBus) Signal(ch chan<- *dbus.Signal) { b.mu.Lock(); b.sigCh = ch; b.mu.Unlock() }
func (b *fakeBus) RemoveSignal(chan<- *dbus.Signal) {
	b.mu.Lock()
	b.sigCh = nil
	b.mu.Unlock()
}
func (b *fakeBus) Close() error { b.mu.Lock(); b.closed = true; b.mu.Unlock(); return nil }

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
		call.Err = dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownMethod", Body: []any{"no handler for " + method}}
		return call
	}
	call.Body, call.Err = fn(args)
	return call
}
func (o *fakeObject) Call(method string, flags dbus.Flags, args ...any) *dbus.Call {
	return o.CallWithContext(context.Background(), method, flags, args...)
}
func (o *fakeObject) GetProperty(p string) (dbus.Variant, error) {
	o.b.mu.Lock()
	defer o.b.mu.Unlock()
	v, ok := o.b.props[string(o.path)+" "+p]
	if !ok {
		return dbus.Variant{}, dbus.Error{Name: "org.freedesktop.DBus.Error.InvalidArgs"}
	}
	return dbus.MakeVariant(v), nil
}
func (o *fakeObject) Go(string, dbus.Flags, chan *dbus.Call, ...any) *dbus.Call { panic("unused") }
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
	sessionPath = dbus.ObjectPath("/org/freedesktop/secrets/session/s1")
	collPath    = dbus.ObjectPath("/org/freedesktop/secrets/collection/keys")
	itemPath    = dbus.ObjectPath("/org/freedesktop/secrets/collection/keys/1")
	promptPath  = dbus.ObjectPath("/org/freedesktop/secrets/prompt/p1")
)

// newClient wires a client to a fresh fake with a working OpenSession.
func newClient(t *testing.T) (*Client, *fakeBus) {
	t.Helper()
	b := newFakeBus()
	b.reply(servicePath, ifaceService+".OpenSession", dbus.MakeVariant(""), sessionPath)
	b.reply(servicePath, ifaceService+".ReadAlias", collPath)
	b.props[string(collPath)+" "+ifaceCollection+".Locked"] = false
	c := New(nil)
	c.dial = func() (bus, error) { return b, nil }
	return c, b
}

func TestSetCreatesItem(t *testing.T) {
	c, b := newClient(t)
	var gotProps map[string]dbus.Variant
	var gotSecret secret
	var gotReplace bool
	b.on(collPath, ifaceCollection+".CreateItem", func(args []any) ([]any, error) {
		gotProps = args[0].(map[string]dbus.Variant)
		gotSecret = args[1].(secret)
		gotReplace = args[2].(bool)
		return []any{itemPath, noObject}, nil
	})

	if err := c.Set(context.Background(), "acc_1", auth.KeyPassword, "hunter2"); err != nil {
		t.Fatal(err)
	}
	attrs := gotProps[ifaceItem+".Attributes"].Value().(map[string]string)
	want := map[string]string{AttrApp: AppID, AttrAccount: "acc_1", AttrKey: auth.KeyPassword, AttrSchema: Schema}
	if len(attrs) != len(want) {
		t.Fatalf("attributes = %v", attrs)
	}
	for k, v := range want {
		if attrs[k] != v {
			t.Fatalf("attribute %s = %q, want %q", k, attrs[k], v)
		}
	}
	lbl := gotProps[ifaceItem+".Label"].Value().(string)
	if lbl != "Malachi Mail: acc_1 (password)" || strings.Contains(lbl, "hunter2") {
		t.Fatalf("label = %q", lbl)
	}
	if string(gotSecret.Value) != "hunter2" || gotSecret.ContentType != "text/plain" || gotSecret.Session != sessionPath || !gotReplace {
		t.Fatalf("secret = %+v replace=%v", gotSecret, gotReplace)
	}
}

func TestGet(t *testing.T) {
	c, b := newClient(t)
	b.reply(servicePath, ifaceService+".SearchItems", []dbus.ObjectPath{itemPath}, []dbus.ObjectPath{})
	b.reply(itemPath, ifaceItem+".GetSecret", secret{Session: sessionPath, Value: []byte("hunter2"), ContentType: "text/plain"})

	v, err := c.Get(context.Background(), "acc_1", auth.KeyPassword)
	if err != nil || v != "hunter2" {
		t.Fatalf("got %q, %v", v, err)
	}

	b.reply(servicePath, ifaceService+".SearchItems", []dbus.ObjectPath{}, []dbus.ObjectPath{})
	if _, err := c.Get(context.Background(), "acc_1", auth.KeyPassword); !errors.Is(err, auth.ErrNoSecret) {
		t.Fatalf("missing: %v", err)
	}
}

func TestGetUnlocksThroughPrompt(t *testing.T) {
	c, b := newClient(t)
	b.reply(servicePath, ifaceService+".SearchItems", []dbus.ObjectPath{}, []dbus.ObjectPath{itemPath})
	b.reply(servicePath, ifaceService+".Unlock", []dbus.ObjectPath{}, promptPath)
	b.on(promptPath, ifacePrompt+".Prompt", func([]any) ([]any, error) {
		go b.emit(&dbus.Signal{Path: promptPath, Name: ifacePrompt + ".Completed",
			Body: []any{false, dbus.MakeVariant([]dbus.ObjectPath{itemPath})}})
		return nil, nil
	})
	b.reply(itemPath, ifaceItem+".GetSecret", secret{Value: []byte("s")})

	v, err := c.Get(context.Background(), "acc_1", auth.KeyPassword)
	if err != nil || v != "s" {
		t.Fatalf("got %q, %v", v, err)
	}
	if b.count(promptPath, ifacePrompt+".Prompt") != 1 {
		t.Fatal("prompt not shown")
	}
}

func TestPromptDismissedAndTimeout(t *testing.T) {
	c, b := newClient(t)
	b.reply(servicePath, ifaceService+".SearchItems", []dbus.ObjectPath{}, []dbus.ObjectPath{itemPath})
	b.reply(servicePath, ifaceService+".Unlock", []dbus.ObjectPath{}, promptPath)
	b.reply(promptPath, ifacePrompt+".Dismiss")

	b.on(promptPath, ifacePrompt+".Prompt", func([]any) ([]any, error) {
		go b.emit(&dbus.Signal{Path: promptPath, Name: ifacePrompt + ".Completed", Body: []any{true, dbus.MakeVariant("")}})
		return nil, nil
	})
	_, err := c.Get(context.Background(), "acc_1", auth.KeyPassword)
	if code(t, err) != api.CodeKeyringError || !strings.Contains(err.Error(), "dismissed") {
		t.Fatalf("dismissed: %v", err)
	}

	b.reply(promptPath, ifacePrompt+".Prompt") // never completes
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = c.Get(ctx, "acc_1", auth.KeyPassword)
	if code(t, err) != api.CodeKeyringError {
		t.Fatalf("timeout: %v", err)
	}
	if b.count(promptPath, ifacePrompt+".Dismiss") != 1 {
		t.Fatal("prompt not dismissed after timeout")
	}
}

func TestSetFallsBackToLoginCollection(t *testing.T) {
	c, b := newClient(t)
	b.reply(servicePath, ifaceService+".ReadAlias", noObject)
	b.props[string(loginCollection)+" "+ifaceCollection+".Locked"] = false
	b.reply(loginCollection, ifaceCollection+".CreateItem", itemPath, noObject)
	if err := c.Set(context.Background(), "acc_1", auth.KeyPassword, "x"); err != nil {
		t.Fatal(err)
	}
	if b.count(loginCollection, ifaceCollection+".CreateItem") != 1 {
		t.Fatal("login collection not used")
	}
}

func TestSetUnlocksLockedCollection(t *testing.T) {
	c, b := newClient(t)
	b.props[string(collPath)+" "+ifaceCollection+".Locked"] = true
	b.reply(servicePath, ifaceService+".Unlock", []dbus.ObjectPath{collPath}, noObject)
	b.reply(collPath, ifaceCollection+".CreateItem", itemPath, noObject)
	if err := c.Set(context.Background(), "acc_1", auth.KeyPassword, "x"); err != nil {
		t.Fatal(err)
	}
	if b.count(servicePath, ifaceService+".Unlock") != 1 {
		t.Fatal("collection not unlocked")
	}
}

func TestServiceErrors(t *testing.T) {
	c := New(nil)
	c.dial = func() (bus, error) { return nil, errors.New("no session bus") }
	if err := c.Set(context.Background(), "acc_1", auth.KeyPassword, "hunter2"); code(t, err) != api.CodeKeyringError || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("dial: %v", err)
	}

	b := newFakeBus()
	b.on(servicePath, ifaceService+".OpenSession", func([]any) ([]any, error) {
		return nil, dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown"}
	})
	c.dial = func() (bus, error) { return b, nil }
	err := c.Set(context.Background(), "acc_1", auth.KeyPassword, "hunter2")
	if code(t, err) != api.CodeKeyringError || !strings.Contains(err.Error(), "ServiceUnknown") || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("service unknown: %v", err)
	}
	if !b.closed {
		t.Fatal("failed connection not closed")
	}
}

func TestReconnectsOnce(t *testing.T) {
	c, b := newClient(t)
	dials := 0
	c.dial = func() (bus, error) { dials++; return b, nil }
	first := true
	b.on(servicePath, ifaceService+".SearchItems", func([]any) ([]any, error) {
		if first {
			first = false
			return nil, dbus.ErrClosed
		}
		return []any{[]dbus.ObjectPath{itemPath}, []dbus.ObjectPath{}}, nil
	})
	b.reply(itemPath, ifaceItem+".GetSecret", secret{Value: []byte("s")})

	if v, err := c.Get(context.Background(), "acc_1", auth.KeyPassword); err != nil || v != "s" {
		t.Fatalf("got %q, %v", v, err)
	}
	if dials != 2 {
		t.Fatalf("dials = %d", dials)
	}
}

func TestDelete(t *testing.T) {
	c, b := newClient(t)
	b.reply(servicePath, ifaceService+".SearchItems", []dbus.ObjectPath{}, []dbus.ObjectPath{})
	if err := c.Delete(context.Background(), "acc_1", auth.KeyPassword); err != nil {
		t.Fatalf("missing: %v", err)
	}

	other := dbus.ObjectPath("/org/freedesktop/secrets/collection/keys/2")
	b.reply(servicePath, ifaceService+".SearchItems", []dbus.ObjectPath{itemPath}, []dbus.ObjectPath{other})
	b.reply(itemPath, ifaceItem+".Delete", noObject)
	b.on(other, ifaceItem+".Delete", func([]any) ([]any, error) {
		return nil, dbus.Error{Name: "org.freedesktop.Secret.Error.NoSuchObject"}
	})
	if err := c.Delete(context.Background(), "acc_1", auth.KeyPassword); err != nil {
		t.Fatal(err)
	}
	if b.count(itemPath, ifaceItem+".Delete") != 1 || b.count(other, ifaceItem+".Delete") != 1 {
		t.Fatalf("delete calls: %v", b.calls)
	}
}

func TestCancelPassesThrough(t *testing.T) {
	c, b := newClient(t)
	b.reply(servicePath, ifaceService+".SearchItems", []dbus.ObjectPath{}, []dbus.ObjectPath{itemPath})
	b.reply(servicePath, ifaceService+".Unlock", []dbus.ObjectPath{}, promptPath)
	b.reply(promptPath, ifacePrompt+".Prompt")
	b.reply(promptPath, ifacePrompt+".Dismiss")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if _, err := c.Get(ctx, "acc_1", auth.KeyPassword); !errors.Is(err, context.Canceled) {
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
