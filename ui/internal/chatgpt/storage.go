// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package chatgpt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

type secretBackend interface {
	Read(context.Context) ([]byte, error)
	Write(context.Context, []byte) error
	Delete(context.Context) error
}

// NativeStore writes renewable tokens only to the system Secret Service.
// Registration and a process-shared advisory lock are the only disk files.
type NativeStore struct {
	directory string
	secrets   secretBackend
}

func NewNativeStore(directory string) (*NativeStore, error) {
	if err := privateDirectory(directory); err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(directory))
	return &NativeStore{directory: directory, secrets: &secretService{store: hex.EncodeToString(hash[:])}}, nil
}
func privateDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return authError("storage")
	}
	part := string(filepath.Separator)
	for _, name := range strings.Split(strings.TrimPrefix(path, part), part) {
		part = filepath.Join(part, name)
		st, err := os.Lstat(part)
		if errors.Is(err, os.ErrNotExist) {
			if err = os.Mkdir(part, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return authError("storage")
			}
			st, err = os.Lstat(part)
		}
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return authError("storage")
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		return authError("storage")
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Getuid() || st.Mode().Perm()&0077 != 0 {
		return authError("storage")
	}
	return nil
}
func openPrivate(path string, flags int) (*os.File, error) {
	fd, err := syscall.Open(path, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, authError("storage")
	}
	f := os.NewFile(uintptr(fd), path)
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, authError("storage")
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Getuid() || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, authError("storage")
	}
	return f, nil
}
func (s *NativeStore) Acquire(ctx context.Context) (func(), error) {
	if err := privateDirectory(s.directory); err != nil {
		return nil, err
	}
	f, err := openPrivate(filepath.Join(s.directory, "refresh.lock"), syscall.O_RDWR|syscall.O_CREAT)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, authError("storage")
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
func (s *NativeStore) ReadRegistration(ctx context.Context) (*Registration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(s.directory, "registration.json")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	f, err := openPrivate(path, syscall.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	var r Registration
	if err != nil || len(b) > 64<<10 || json.Unmarshal(b, &r) != nil || r.HostID == "" {
		return nil, authError("storage")
	}
	return &r, nil
}
func (s *NativeStore) WriteRegistration(ctx context.Context, r Registration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil || len(b) > 64<<10 {
		return authError("storage")
	}
	f, err := os.CreateTemp(s.directory, ".registration-")
	if err != nil {
		return authError("storage")
	}
	path := f.Name()
	defer os.Remove(path)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return authError("storage")
	}
	target := filepath.Join(s.directory, "registration.json")
	if st, err := os.Lstat(target); err == nil && (st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular()) {
		return authError("storage")
	}
	if os.Rename(path, target) != nil {
		return authError("storage")
	}
	return nil
}
func (s *NativeStore) ReadTokens(ctx context.Context) (*Tokens, error) {
	b, err := s.secrets.Read(ctx)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	var t Tokens
	if len(b) > 256<<10 || json.Unmarshal(b, &t) != nil || t.AccessToken == "" || t.RefreshToken == "" || t.ConnectionID == "" {
		return nil, authError("storage")
	}
	return &t, nil
}
func (s *NativeStore) WriteTokens(ctx context.Context, t Tokens) error {
	b, err := json.Marshal(t)
	if err != nil || len(b) > 256<<10 {
		return authError("storage")
	}
	return s.secrets.Write(ctx, b)
}
func (s *NativeStore) DeleteTokens(ctx context.Context) error { return s.secrets.Delete(ctx) }

// A fresh D-Bus connection per operation bounds native ownership and avoids
// sharing signal channels. The plain session is private to the user's bus;
// refusing a locked/unavailable keyring never creates a plaintext fallback.
type secretService struct{ store string }
type serviceSecret struct {
	Session           dbus.ObjectPath
	Parameters, Value []byte
	ContentType       string
}

const secretsName = "org.freedesktop.secrets"
const serviceInterface = "org.freedesktop.Secret.Service"
const itemInterface = "org.freedesktop.Secret.Item"
const collectionInterface = "org.freedesktop.Secret.Collection"
const promptInterface = "org.freedesktop.Secret.Prompt"
const secretsPath = dbus.ObjectPath("/org/freedesktop/secrets")

func (s *secretService) attributes() map[string]string {
	return map[string]string{"app": "io.github.schotek.Malachi", "purpose": "chatgpt-native-grant", "store": s.store, "xdg:schema": "io.github.schotek.Malachi.ChatGPT"}
}
func (s *secretService) operate(ctx context.Context, fn func(context.Context, *dbus.Conn, dbus.ObjectPath) error) error {
	c, err := dbus.ConnectSessionBus()
	if err != nil {
		return authError("storage")
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var session dbus.ObjectPath
	var ignored dbus.Variant
	if c.Object(secretsName, secretsPath).CallWithContext(ctx, serviceInterface+".OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&ignored, &session) != nil || session == "/" {
		return authError("storage")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.Object(secretsName, session).CallWithContext(cleanup, "org.freedesktop.Secret.Session.Close", 0).Err
	}()
	if err = fn(ctx, c, session); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return authError("storage")
	}
	return nil
}
func prompt(ctx context.Context, c *dbus.Conn, path dbus.ObjectPath) (dbus.Variant, error) {
	if path == "/" {
		return dbus.Variant{}, nil
	}
	signals := make(chan *dbus.Signal, 8)
	c.Signal(signals)
	defer c.RemoveSignal(signals)
	opts := []dbus.MatchOption{dbus.WithMatchObjectPath(path), dbus.WithMatchInterface(promptInterface), dbus.WithMatchMember("Completed")}
	if c.AddMatchSignalContext(ctx, opts...) != nil {
		return dbus.Variant{}, authError("storage")
	}
	defer c.RemoveMatchSignal(opts...)
	obj := c.Object(secretsName, path)
	if obj.CallWithContext(ctx, promptInterface+".Prompt", 0, "").Err != nil {
		return dbus.Variant{}, authError("storage")
	}
	for {
		select {
		case sig := <-signals:
			if sig == nil || sig.Path != path || sig.Name != promptInterface+".Completed" || len(sig.Body) != 2 {
				continue
			}
			dismissed, ok := sig.Body[0].(bool)
			v, vok := sig.Body[1].(dbus.Variant)
			if !ok || !vok || dismissed {
				return dbus.Variant{}, authError("storage")
			}
			return v, nil
		case <-ctx.Done():
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = obj.CallWithContext(cleanup, promptInterface+".Dismiss", 0).Err
			cancel()
			return dbus.Variant{}, ctx.Err()
		}
	}
}
func (s *secretService) find(ctx context.Context, c *dbus.Conn) ([]dbus.ObjectPath, error) {
	var unlocked, locked []dbus.ObjectPath
	obj := c.Object(secretsName, secretsPath)
	if obj.CallWithContext(ctx, serviceInterface+".SearchItems", 0, s.attributes()).Store(&unlocked, &locked) != nil {
		return nil, authError("storage")
	}
	if len(locked) > 0 {
		var more []dbus.ObjectPath
		var p dbus.ObjectPath
		if obj.CallWithContext(ctx, serviceInterface+".Unlock", 0, locked).Store(&more, &p) != nil {
			return nil, authError("storage")
		}
		v, err := prompt(ctx, c, p)
		if err != nil {
			return nil, err
		}
		if paths, ok := v.Value().([]dbus.ObjectPath); ok {
			more = append(more, paths...)
		}
		unlocked = append(unlocked, more...)
		if len(more) < len(locked) {
			return nil, authError("storage")
		}
	}
	return unlocked, nil
}
func (s *secretService) Read(ctx context.Context) ([]byte, error) {
	var result []byte
	err := s.operate(ctx, func(ctx context.Context, c *dbus.Conn, session dbus.ObjectPath) error {
		items, err := s.find(ctx, c)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		if len(items) != 1 {
			return authError("storage")
		}
		var sec serviceSecret
		if c.Object(secretsName, items[0]).CallWithContext(ctx, itemInterface+".GetSecret", 0, session).Store(&sec) != nil {
			return authError("storage")
		}
		if sec.Session != session || len(sec.Value) > 256<<10 {
			return authError("storage")
		}
		result = sec.Value
		return nil
	})
	return result, err
}
func (s *secretService) Write(ctx context.Context, value []byte) error {
	return s.operate(ctx, func(ctx context.Context, c *dbus.Conn, session dbus.ObjectPath) error {
		obj := c.Object(secretsName, secretsPath)
		var collection dbus.ObjectPath
		if obj.CallWithContext(ctx, serviceInterface+".ReadAlias", 0, "default").Store(&collection) != nil || collection == "/" {
			return authError("storage")
		}
		var unlocked []dbus.ObjectPath
		var p dbus.ObjectPath
		if obj.CallWithContext(ctx, serviceInterface+".Unlock", 0, []dbus.ObjectPath{collection}).Store(&unlocked, &p) != nil {
			return authError("storage")
		}
		if _, err := prompt(ctx, c, p); err != nil {
			return err
		}
		properties := map[string]dbus.Variant{itemInterface + ".Label": dbus.MakeVariant("Malachi Mail — ChatGPT connection"), itemInterface + ".Attributes": dbus.MakeVariant(s.attributes())}
		var item dbus.ObjectPath
		if c.Object(secretsName, collection).CallWithContext(ctx, collectionInterface+".CreateItem", 0, properties, serviceSecret{session, []byte{}, value, "application/json"}, true).Store(&item, &p) != nil {
			return authError("storage")
		}
		if p != "/" {
			_, err := prompt(ctx, c, p)
			return err
		}
		if item == "/" {
			return authError("storage")
		}
		return nil
	})
}
func (s *secretService) Delete(ctx context.Context) error {
	return s.operate(ctx, func(ctx context.Context, c *dbus.Conn, _ dbus.ObjectPath) error {
		items, err := s.find(ctx, c)
		if err != nil {
			return err
		}
		for _, item := range items {
			var p dbus.ObjectPath
			if c.Object(secretsName, item).CallWithContext(ctx, itemInterface+".Delete", 0).Store(&p) != nil {
				return authError("storage")
			}
			if _, err = prompt(ctx, c, p); err != nil {
				return err
			}
		}
		return nil
	})
}
