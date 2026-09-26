// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package rpc

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// checkNoTempFiles fails if a temporary key file is left in dir.
func checkNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temporary file %s left behind", e.Name())
		}
	}
}

func TestKeyFileWrittenOnListen(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	dir, kp := filepath.Dir(ts.sock), api.KeyPath(ts.sock)
	b, err := os.ReadFile(kp)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != api.KeyFileSize {
		t.Errorf("the key file is %d bytes, want %d", len(b), api.KeyFileSize)
	}
	k, err := api.ParseKeyFile(b)
	if err != nil || !k.Equal(serverKey(ts.Server)) {
		t.Errorf("the key file does not hold the server's key (%v)", err)
	}
	checkNoTempFiles(t, dir)

	// The permissions of a file made with mode 0600 in the same directory:
	// 0600 where the platform has file modes.
	probe := filepath.Join(dir, "probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	pfi, err := os.Stat(probe)
	if err != nil {
		t.Fatal(err)
	}
	kfi, err := os.Stat(kp)
	if err != nil {
		t.Fatal(err)
	}
	if kfi.Mode().Perm() != pfi.Mode().Perm() {
		t.Errorf("key file mode %v, a file made with 0600 has %v", kfi.Mode().Perm(), pfi.Mode().Perm())
	}
	_ = os.Remove(probe)
}

func TestKeyChangesEveryRun(t *testing.T) {
	sock := tempSock(t)
	kp := api.KeyPath(sock)
	var keys []api.AuthKey
	for range 2 {
		s := NewServer(&StubBackend{}, quietLog())
		if err := s.Listen(sock); err != nil {
			t.Fatal(err)
		}
		k, err := api.ReadKeyFile(kp)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
		s.Close()
		for _, p := range []string{sock, kp} {
			if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Close left %s: %v", filepath.Base(p), err)
			}
		}
	}
	if keys[0].Equal(keys[1]) {
		t.Error("two runs wrote the same key")
	}
}

func TestKeyFileRemovedOnClose(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	ts.stop() // Serve's context ends: that closes the server
	for _, p := range []string{ts.sock, api.KeyPath(ts.sock)} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is still there: %v", filepath.Base(p), err)
		}
	}
	checkNoTempFiles(t, filepath.Dir(ts.sock))
}

func TestForeignKeyFileSurvivesClose(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	kp := api.KeyPath(ts.sock)
	foreign := api.FormatKeyFile(api.NewAuthKey())
	if err := os.WriteFile(kp, foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	ts.stop()
	if b, err := os.ReadFile(kp); err != nil || !bytes.Equal(b, foreign) {
		t.Errorf("Close removed or changed another daemon's key file (%v)", err)
	}
	if _, err := os.Lstat(ts.sock); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the socket is still there: %v", err)
	}
}

func TestConcurrentListen(t *testing.T) {
	for round := range 5 {
		sock := tempSock(t)
		servers := make([]*Server, 4)
		errs := make([]error, len(servers))
		var wg sync.WaitGroup
		for i := range servers {
			servers[i] = NewServer(&StubBackend{}, quietLog())
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = servers[i].Listen(sock)
			}()
		}
		wg.Wait()
		serving := 0
		for i, s := range servers {
			if errs[i] == nil {
				serving++
				serve(t, s, sock)
			} else {
				s.Close()
			}
		}
		if serving == 0 {
			t.Fatalf("round %d: no server listens: %v", round, errs)
		}
		// Whoever serves the path proves the key that is in the key file
		// by then: the daemon that answers restores its own.
		c, r := dial(t, sock)
		if err := clientHandshake(c, r, sock); err != nil {
			t.Fatalf("round %d: handshake: %v (Listen: %v)", round, err, errs)
		}
		_ = c.Close()
	}
}

func TestKeyFileRestoredOnHello(t *testing.T) {
	ts := startServer(t, nil, nil, nil)
	kp := api.KeyPath(ts.sock)
	key := serverKey(ts.Server)
	for _, tc := range []struct {
		name   string
		damage func() error
	}{
		{"deleted", func() error { return os.Remove(kp) }},
		{"another key", func() error { return os.WriteFile(kp, api.FormatKeyFile(api.NewAuthKey()), 0o600) }},
		{"truncated", func() error { return os.WriteFile(kp, api.FormatKeyFile(key)[:10], 0o600) }},
		{"empty", func() error { return os.WriteFile(kp, nil, 0o600) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.damage(); err != nil {
				t.Fatal(err)
			}
			dialAuthed(t, ts.sock)
			if k, err := api.ReadKeyFile(kp); err != nil || !k.Equal(key) {
				t.Errorf("the key file was not restored (%v)", err)
			}
			checkNoTempFiles(t, filepath.Dir(kp))
		})
	}
}

// pemLike is a file that must never be taken for a key file.
var pemLike = []byte("-----BEGIN PRIVATE KEY-----\n" + strings.Repeat("TUFMQUNISQ", 40) + "\n-----END PRIVATE KEY-----\n")

func TestForeignFileNotReplacedOnHello(t *testing.T) {
	log, logs := captureLog()
	ts := startServer(t, nil, log, nil)
	kp := api.KeyPath(ts.sock)
	if err := os.Remove(kp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kp, pemLike, 0o600); err != nil {
		t.Fatal(err)
	}
	c, r := dial(t, ts.sock)
	var he *api.HandshakeError
	if err := clientHandshake(c, r, ts.sock); !errors.As(err, &he) || he.Reason != api.HandshakeKeyUnavailable {
		t.Fatalf("handshake with a foreign file at the key path: %v", err)
	}
	if b, err := os.ReadFile(kp); err != nil || !bytes.Equal(b, pemLike) {
		t.Errorf("the foreign file changed (%v)", err)
	}
	out := logs.String()
	if !strings.Contains(out, "left alone") {
		t.Errorf("the refusal is not logged:\n%s", out)
	}
	if strings.Contains(out, "BEGIN PRIVATE KEY") || strings.Contains(out, "TUFMQUNISQ") {
		t.Errorf("the log quotes the foreign file:\n%s", out)
	}
}

func TestListenRefusesForeignKeyPath(t *testing.T) {
	keyShapedContent := api.FormatKeyFile(api.NewAuthKey())
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, kp string) // makes the foreign thing at kp
		check func(t *testing.T, kp string) // it is unchanged
	}{
		{"pem", writeFile(pemLike), sameContent(pemLike)},
		{"a key and a byte more", writeFile(append(bytes.Clone(keyShapedContent), 'a')), sameContent(append(bytes.Clone(keyShapedContent), 'a'))},
		{"text", writeFile([]byte("hello\n")), sameContent([]byte("hello\n"))},
		{"upper case key", writeFile(bytes.ToUpper(keyShapedContent)), sameContent(bytes.ToUpper(keyShapedContent))},
		{"directory", func(t *testing.T, kp string) {
			if err := os.Mkdir(kp, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(kp, "inside"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, func(t *testing.T, kp string) {
			if b, err := os.ReadFile(filepath.Join(kp, "inside")); err != nil || string(b) != "x" {
				t.Errorf("the directory changed (%v)", err)
			}
		}},
		{"symlink", func(t *testing.T, kp string) {
			target := kp + ".target"
			if err := os.WriteFile(target, keyShapedContent, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, kp); err != nil {
				t.Skipf("cannot make a symbolic link here: %v", err)
			}
		}, func(t *testing.T, kp string) {
			if fi, err := os.Lstat(kp); err != nil || fi.Mode().Type() != fs.ModeSymlink {
				t.Errorf("the link changed (%v)", err)
			}
			if b, err := os.ReadFile(kp + ".target"); err != nil || !bytes.Equal(b, keyShapedContent) {
				t.Errorf("the link's target changed (%v)", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sock := tempSock(t)
			kp := api.KeyPath(sock)
			tc.setup(t, kp)
			s := NewServer(&StubBackend{}, quietLog())
			err := s.Listen(sock)
			if err == nil {
				s.Close()
				t.Fatal("Listen replaced a foreign file at the key path")
			}
			if !strings.Contains(err.Error(), kp) {
				t.Errorf("the error does not name the key path: %v", err)
			}
			if strings.Contains(err.Error(), "BEGIN") || strings.Contains(err.Error(), "hello") {
				t.Errorf("the error quotes the file: %v", err)
			}
			tc.check(t, kp)
			if _, err := os.Lstat(sock); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the socket was left behind: %v", err)
			}
			checkNoTempFiles(t, filepath.Dir(sock))
			s.Close()
			tc.check(t, kp)
		})
	}
}

func writeFile(content []byte) func(t *testing.T, path string) {
	return func(t *testing.T, path string) {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func sameContent(want []byte) func(t *testing.T, path string) {
	return func(t *testing.T, path string) {
		if b, err := os.ReadFile(path); err != nil || !bytes.Equal(b, want) {
			t.Errorf("the file changed (%v)", err)
		}
	}
}

func TestListenReplacesKeyShapedLeftovers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content []byte
	}{
		{"empty", nil},
		{"torn", []byte("0123ab")},
		{"torn with a newline", []byte("0123\n")},
		{"a newline", []byte("\n")},
		{"an old key", api.FormatKeyFile(api.NewAuthKey())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sock := tempSock(t)
			kp := api.KeyPath(sock)
			if err := os.WriteFile(kp, tc.content, 0o600); err != nil {
				t.Fatal(err)
			}
			s := NewServer(&StubBackend{}, quietLog())
			if err := s.Listen(sock); err != nil {
				t.Fatalf("Listen refused a key-shaped leftover: %v", err)
			}
			defer s.Close()
			if k, err := api.ReadKeyFile(kp); err != nil || !k.Equal(serverKey(s)) {
				t.Errorf("the key file does not hold the new key (%v)", err)
			}
			checkNoTempFiles(t, filepath.Dir(sock))
		})
	}
}

func TestRetiredKeyIsNeverRewritten(t *testing.T) {
	sock := tempSock(t)
	kp := api.KeyPath(sock)
	s := NewServer(&StubBackend{}, quietLog())
	if err := s.Listen(sock); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := os.Lstat(kp); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Close left the key file: %v", err)
	}
	s.ensureKeyFile() // a system.hello answered while the server closes
	if _, err := os.Lstat(kp); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a retired key was written again: %v", err)
	}
}

// mkfifo makes a named pipe at path, or skips the test where it cannot. On
// Windows, Git's mkfifo writes a Cygwin shortcut beside path, which Go does
// not see as anything.
func mkfifo(t *testing.T, path string) {
	t.Helper()
	if out, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
		t.Skipf("cannot make a named pipe here: %v %s", err, out)
	}
	if fi, err := os.Lstat(path); err != nil || fi.Mode().Type() != fs.ModeNamedPipe {
		t.Skipf("mkfifo made no named pipe here (%v)", err)
	}
}

// promptly returns f's error and fails the test if f takes more than 2 s:
// opening a named pipe for reading without O_NONBLOCK waits for a writer.
// The test then opens pipe for writing, which lets f go on.
func promptly(t *testing.T, pipe string, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
	}
	if w, err := os.OpenFile(pipe, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
		_ = w.Close()
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	t.Fatal("blocked on a named pipe, waiting for a writer")
	return nil
}

// isPipe fails unless path is still a named pipe.
func isPipe(t *testing.T, path string) {
	t.Helper()
	if fi, err := os.Lstat(path); err != nil || fi.Mode().Type() != fs.ModeNamedPipe {
		t.Errorf("the named pipe changed (%v)", err)
	}
}

func TestKeyReplaceableRefusesNamedPipes(t *testing.T) {
	t.Run("at the path", func(t *testing.T) {
		path := filepath.Join(shortDir(t), "rpc.sock.key")
		mkfifo(t, path)
		if promptly(t, path, func() error { return keyReplaceable(path) }) == nil {
			t.Error("a named pipe is replaceable")
		}
		isPipe(t, path)
	})
	t.Run("swapped in after Lstat", func(t *testing.T) {
		dir := shortDir(t)
		path := filepath.Join(dir, "rpc.sock.key")
		if err := os.WriteFile(path, api.FormatKeyFile(api.NewAuthKey()), 0o600); err != nil {
			t.Fatal(err)
		}
		pipe := filepath.Join(dir, "pipe")
		mkfifo(t, pipe)
		open := openNoWait
		t.Cleanup(func() { openNoWait = open })
		openNoWait = func(p string) (*os.File, error) {
			// Lstat saw the key file; the open meets the pipe.
			if err := os.Rename(pipe, p); err != nil {
				return nil, err
			}
			return open(p)
		}
		if promptly(t, path, func() error { return keyReplaceable(path) }) == nil {
			t.Error("a named pipe is replaceable")
		}
		isPipe(t, path)
	})
}

func TestNamedPipeAtTheKeyPath(t *testing.T) {
	mkfifo(t, filepath.Join(shortDir(t), "probe"))

	t.Run("Listen refuses it", func(t *testing.T) {
		sock := tempSock(t)
		kp := api.KeyPath(sock)
		mkfifo(t, kp)
		s := NewServer(&StubBackend{}, quietLog())
		if promptly(t, kp, func() error { return s.Listen(sock) }) == nil {
			s.Close()
			t.Fatal("Listen replaced a named pipe")
		}
		isPipe(t, kp)
		if _, err := os.Lstat(sock); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the socket was left behind: %v", err)
		}
	})

	// A pipe that takes the key file's place while a system.hello is
	// answered holds up neither that answer, nor later ones, nor Close.
	t.Run("swapped in while the daemon answers", func(t *testing.T) {
		var armed atomic.Bool
		open := openNoWait
		t.Cleanup(func() { openNoWait = open }) // after the server has stopped
		openNoWait = func(p string) (*os.File, error) {
			if armed.CompareAndSwap(true, false) {
				_ = os.Rename(filepath.Join(filepath.Dir(p), "pipe"), p)
			}
			return open(p)
		}
		ts := startServer(t, nil, nil, nil)
		kp := api.KeyPath(ts.sock)
		mkfifo(t, filepath.Join(filepath.Dir(kp), "pipe"))
		// Another key in the file: the next system.hello inspects it
		// (keyReplaceable), and the pipe takes its place right then.
		if err := os.WriteFile(kp, api.FormatKeyFile(api.NewAuthKey()), 0o600); err != nil {
			t.Fatal(err)
		}
		armed.Store(true)

		c, r := dial(t, ts.sock)
		var he *api.HandshakeError
		err := promptly(t, kp, func() error { return clientHandshake(c, r, ts.sock) })
		if !errors.As(err, &he) || he.Reason != api.HandshakeKeyUnavailable {
			t.Fatalf("handshake: %v", err)
		}
		if armed.Load() {
			t.Fatal("the pipe was not swapped in")
		}
		isPipe(t, kp)
		promptly(t, kp, func() error { ts.stop(); return nil })
		isPipe(t, kp)
	})
}
