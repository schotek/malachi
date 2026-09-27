// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test vectors of docs/api.md §1.4, checked independently with
// OpenSSL: key = bytes 0x00..0x1f, clientNonce = 0x20..0x3f, daemonNonce =
// 0x40..0x5f.
const (
	vectorKey         = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	vectorClientNonce = "202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f"
	vectorDaemonNonce = "404142434445464748494a4b4c4d4e4f505152535455565758595a5b5c5d5e5f"
	vectorDaemonProof = "04abc851d52b40dc687920756f15f42f44da2635331732bf02be0a0b01de2a1f"
	vectorClientProof = "024f86a00c241237f4556a27e83f8e41b13053bbcfd2980e029c300a5a2e84b2"
)

func TestAuthVectors(t *testing.T) {
	var k AuthKey
	var cn, dn AuthNonce
	for i := range k {
		k[i] = byte(i)
		cn[i] = byte(0x20 + i)
		dn[i] = byte(0x40 + i)
	}
	if got := string(FormatKeyFile(k)); got != vectorKey+"\n" {
		t.Errorf("key file %q", got)
	}
	if cn.Hex() != vectorClientNonce || dn.Hex() != vectorDaemonNonce {
		t.Errorf("nonces %s %s", cn.Hex(), dn.Hex())
	}
	if got := DaemonProof(k, cn, dn).Hex(); got != vectorDaemonProof {
		t.Errorf("daemonProof = %s, want %s", got, vectorDaemonProof)
	}
	if got := ClientProof(k, cn, dn).Hex(); got != vectorClientProof {
		t.Errorf("clientProof = %s, want %s", got, vectorClientProof)
	}
	// 91 bytes: the label, NUL, a six-letter role, NUL, two 32-byte nonces.
	if n := len(authLabel) + 1 + len(roleDaemon) + 1 + 2*32; n != 91 || len(roleClient) != len(roleDaemon) {
		t.Errorf("proof message is %d bytes", n)
	}
}

func TestAuthVectorsDocumented(t *testing.T) {
	doc := readAPIDoc(t)
	for name, v := range map[string]string{
		"key":         vectorKey,
		"clientNonce": vectorClientNonce,
		"daemonNonce": vectorDaemonNonce,
		"daemonProof": vectorDaemonProof,
		"clientProof": vectorClientProof,
		"label":       hex.EncodeToString([]byte(authLabel)),
		"daemon role": hex.EncodeToString([]byte(roleDaemon)),
		"client role": hex.EncodeToString([]byte(roleClient)),
	} {
		if !strings.Contains(doc, v) {
			t.Errorf("docs/api.md lacks the test vector %s (%s)", name, v)
		}
	}
	if !strings.Contains(doc, `"`+authLabel+`"`) {
		t.Errorf("docs/api.md does not name the label %q", authLabel)
	}
}

func TestProofsSeparateRoles(t *testing.T) {
	k, cn, dn := NewAuthKey(), NewAuthNonce(), NewAuthNonce()
	d, c := DaemonProof(k, cn, dn), ClientProof(k, cn, dn)
	if d.Equal(c) {
		t.Fatal("the daemon's and the client's proofs are equal")
	}
	if !DaemonProof(k, cn, dn).Equal(d) || !ClientProof(k, cn, dn).Equal(c) {
		t.Fatal("proofs are not deterministic")
	}
	if DaemonProof(k, dn, cn).Equal(d) || ClientProof(k, dn, cn).Equal(c) {
		t.Error("swapping the nonces keeps a proof")
	}
	flip := func(b [32]byte, bit int) [32]byte {
		b[bit/8] ^= 1 << (bit % 8)
		return b
	}
	for _, bit := range []int{0, 7, 8, 131, 255} {
		k2, cn2, dn2 := AuthKey(flip(k, bit)), AuthNonce(flip(cn, bit)), AuthNonce(flip(dn, bit))
		for name, p := range map[string][2]AuthProof{
			"key":         {DaemonProof(k2, cn, dn), ClientProof(k2, cn, dn)},
			"clientNonce": {DaemonProof(k, cn2, dn), ClientProof(k, cn2, dn)},
			"daemonNonce": {DaemonProof(k, cn, dn2), ClientProof(k, cn, dn2)},
		} {
			if p[0].Equal(d) || p[1].Equal(c) {
				t.Errorf("flipping bit %d of the %s keeps a proof", bit, name)
			}
		}
		if k2.Equal(k) {
			t.Errorf("AuthKey.Equal ignores bit %d", bit)
		}
	}
	if !k.Equal(k) || AuthProof(d).Equal(AuthProof(flip(d, 3))) {
		t.Error("Equal is wrong")
	}
}

func TestParseAuthHex(t *testing.T) {
	good := strings.Repeat("0123456789abcdef", 4)
	b, ok := ParseAuthHex(good)
	if !ok || hex.EncodeToString(b[:]) != good {
		t.Fatalf("%q → %x, %v", good, b, ok)
	}
	for name, s := range map[string]string{
		"empty":                     "",
		"63 digits":                 good[:63],
		"65 digits":                 good + "0",
		"128 digits":                good + good,
		"upper case":                strings.ToUpper(good),
		"mixed case":                good[:60] + "ABcd",
		"0x prefix":                 "0x" + good[:62],
		"0x prefix, 66 bytes":       "0x" + good,
		"leading space":             " " + good[1:],
		"trailing space":            good[:63] + " ",
		"surrounding space":         " " + good + " ",
		"tab":                       "\t" + good[1:],
		"newline":                   good[:63] + "\n",
		"trailing newline":          good + "\n",
		"NUL":                       good[:63] + "\x00",
		"g":                         good[:63] + "g",
		"full-width digits":         strings.Repeat("\uff10", 64),
		"full-width, 64 bytes":      strings.Repeat("\uff10", 21) + "0",
		"multi-byte rune, 64 bytes": good[:62] + "é",
		"invalid UTF-8":             good[:63] + "\xff",
	} {
		if strings.HasSuffix(name, "64 bytes") && len(s) != 64 {
			t.Fatalf("%s: the case is %d bytes", name, len(s))
		}
		if _, ok := ParseAuthHex(s); ok {
			t.Errorf("%s: %q accepted", name, s)
		}
	}
}

func TestKeyFileRoundTrip(t *testing.T) {
	for range 16 {
		k := NewAuthKey()
		b := FormatKeyFile(k)
		if len(b) != KeyFileSize || b[KeyFileSize-1] != '\n' || string(b[:64]) != hex.EncodeToString(k[:]) {
			t.Fatalf("key file %q", b)
		}
		got, err := ParseKeyFile(b)
		if err != nil || !got.Equal(k) {
			t.Fatalf("round trip: %v", err)
		}
	}
}

func TestParseKeyFileRejects(t *testing.T) {
	k := NewAuthKey()
	good := hex.EncodeToString(k[:])
	cases := map[string][]byte{
		"empty":                   nil,
		"newline only":            []byte("\n"),
		"missing newline":         []byte(good),
		"CRLF":                    []byte(good + "\r\n"),
		"CR instead of newline":   []byte(good + "\r"),
		"two newlines":            []byte(good + "\n\n"),
		"newline first":           []byte("\n" + good),
		"leading space":           []byte(" " + good + "\n"),
		"leading space, 65 bytes": []byte(" " + good[1:] + "\n"),
		"trailing space":          []byte(good + " \n"),
		"space before newline":    []byte(good[:63] + " \n"),
		"BOM":                     []byte("\ufeff" + good + "\n"),
		"BOM, 65 bytes":           []byte("\ufeff" + good[3:] + "\n"),
		"upper case":              []byte(strings.ToUpper(good) + "\n"),
		"NUL":                     []byte(good[:63] + "\x00\n"),
		"NUL instead of newline":  []byte(good + "\x00"),
		"1 MiB":                   bytes.Repeat([]byte("a"), 1<<20),
		"1 MiB of keys":           bytes.Repeat([]byte(good+"\n"), (1<<20)/KeyFileSize),
		"key and garbage":         []byte(good + "\ngarbage"),
		"two keys":                []byte(good + "\n" + good + "\n"),
	}
	hexRun := regexp.MustCompile(`[0-9a-fA-F]{8,}`)
	for name, in := range cases {
		_, err := ParseKeyFile(in)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		msg := err.Error()
		if hexRun.MatchString(msg) || strings.Contains(msg, "garbage") || (len(in) > 0 && strings.Contains(msg, string(in))) {
			t.Errorf("%s: the error quotes the input: %s", name, msg)
		}
	}
}

func TestReadKeyFile(t *testing.T) {
	dir := t.TempDir()
	k := NewAuthKey()
	secret := hex.EncodeToString(k[:])
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	valid := write("rpc.sock.key", FormatKeyFile(k))
	refused := func(t *testing.T, path string) error {
		t.Helper()
		_, err := ReadKeyFile(path)
		if err == nil {
			t.Fatalf("%s accepted", path)
		}
		if !errors.Is(err, ErrKeyUnavailable) {
			t.Errorf("%v does not wrap ErrKeyUnavailable", err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error quotes the key: %v", err)
		}
		return err
	}

	t.Run("valid", func(t *testing.T) {
		got, err := ReadKeyFile(valid)
		if err != nil || !got.Equal(k) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		err := refused(t, filepath.Join(dir, "missing.key"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%v: not fs.ErrNotExist", err)
		}
	})
	t.Run("directory", func(t *testing.T) {
		p := filepath.Join(dir, "dir.key")
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		refused(t, p)
	})
	t.Run("symlink", func(t *testing.T) {
		p := filepath.Join(dir, "link.key")
		if err := os.Symlink(valid, p); err != nil {
			t.Skipf("cannot create a symbolic link here: %v", err)
		}
		refused(t, p)
	})
	t.Run("10 MiB", func(t *testing.T) {
		refused(t, write("big.key", bytes.Repeat(FormatKeyFile(k), (10<<20)/KeyFileSize)))
	})
	t.Run("empty", func(t *testing.T) {
		refused(t, write("empty.key", nil))
	})
	t.Run("upper case", func(t *testing.T) {
		refused(t, write("upper.key", bytes.ToUpper(FormatKeyFile(k))))
	})
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

// promptly returns read's error and fails the test if read takes more than
// 2 s: opening a named pipe for reading without O_NONBLOCK waits for a
// writer. The test then opens pipe for writing, which lets read go on.
func promptly(t *testing.T, pipe string, read func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- read() }()
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

func TestReadKeyFileRefusesNamedPipes(t *testing.T) {
	read := func(path string) func() error {
		return func() error { _, err := ReadKeyFile(path); return err }
	}
	t.Run("at the path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "rpc.sock.key")
		mkfifo(t, path)
		if err := promptly(t, path, read(path)); !errors.Is(err, ErrKeyUnavailable) {
			t.Fatalf("got %v, want ErrKeyUnavailable", err)
		}
	})
	t.Run("swapped in after Lstat", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "rpc.sock.key")
		if err := os.WriteFile(path, FormatKeyFile(NewAuthKey()), 0o600); err != nil {
			t.Fatal(err)
		}
		pipe := filepath.Join(dir, "pipe")
		mkfifo(t, pipe)
		open := openKeyFile
		t.Cleanup(func() { openKeyFile = open })
		openKeyFile = func(p string) (*os.File, error) {
			// Lstat saw the key file; the open meets the pipe.
			if err := os.Rename(pipe, p); err != nil {
				return nil, err
			}
			return open(p)
		}
		if err := promptly(t, path, read(path)); !errors.Is(err, ErrKeyUnavailable) {
			t.Fatalf("got %v, want ErrKeyUnavailable", err)
		}
		if fi, err := os.Lstat(path); err != nil || fi.Mode().Type() != fs.ModeNamedPipe {
			t.Fatalf("the pipe was not swapped in (%v)", err)
		}
	})
}

func TestAuthKeyRedacted(t *testing.T) {
	k := NewAuthKey()
	arr := [32]byte(k)
	jsonArr, _ := json.Marshal(arr)
	forms := []string{
		hex.EncodeToString(k[:]),
		strings.ToUpper(hex.EncodeToString(k[:])),
		fmt.Sprint(arr)[1:40], // the bytes as fmt prints an array, "[12 34 …"
		string(jsonArr[1:40]),
		base64.StdEncoding.EncodeToString(k[:])[:40],
		base64.RawURLEncoding.EncodeToString(k[:])[:40],
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%x", "%X", "%q", "%d", "%08b", "%-12.3v", "% x"} {
		if got := fmt.Sprintf(verb, k); got != redacted {
			t.Errorf("Sprintf(%q, key) = %q", verb, got)
		}
		if got := fmt.Sprintf(verb, &k); got != redacted {
			t.Errorf("Sprintf(%q, &key) = %q", verb, got)
		}
	}
	out := map[string]string{
		"Sprint":         fmt.Sprint(k),
		"Sprintln":       fmt.Sprintln(k),
		"String":         k.String(),
		"GoString":       k.GoString(),
		"exported field": fmt.Sprintf("%+v", struct{ Key AuthKey }{k}),
		"Go syntax":      fmt.Sprintf("%#v", struct{ Key AuthKey }{k}),
		"slice":          fmt.Sprint([]AuthKey{k}),
		"map":            fmt.Sprint(map[string]AuthKey{"k": k}),
		"Errorf":         fmt.Errorf("key %v", k).Error(),
	}
	var text, js bytes.Buffer
	for _, h := range []slog.Handler{slog.NewTextHandler(&text, nil), slog.NewJSONHandler(&js, nil)} {
		slog.New(h).Info("msg", "key", k, slog.Any("any", k), slog.Group("g", "key", &k))
	}
	out["slog text"] = text.String()
	out["slog JSON"] = js.String()
	for name, s := range out {
		if !strings.Contains(s, redacted) {
			t.Errorf("%s: %q does not say [redacted]", name, s)
		}
		for _, f := range forms {
			if strings.Contains(s, f) {
				t.Errorf("%s leaks the key: %q", name, s)
			}
		}
	}
	for name, v := range map[string]any{
		"value":   k,
		"pointer": &k,
		"field":   struct{ Key AuthKey }{k},
		"map key": map[AuthKey]int{k: 1},
		"slice":   []AuthKey{k},
	} {
		b, err := json.Marshal(v)
		if err == nil {
			t.Errorf("json.Marshal(%s) = %s, want an error", name, b)
			continue
		}
		for _, f := range forms {
			if strings.Contains(err.Error(), f) {
				t.Errorf("json.Marshal(%s): the error leaks the key: %v", name, err)
			}
		}
	}
	if b, err := k.MarshalText(); err == nil || b != nil {
		t.Errorf("MarshalText = %q, %v", b, err)
	}
}

func TestNewAuthNonceDistinct(t *testing.T) {
	seen := make(map[AuthNonce]bool, 1000)
	for i := range 1000 {
		n := NewAuthNonce()
		if seen[n] || n == (AuthNonce{}) {
			t.Fatalf("nonce %d repeats or is zero: %s", i, n.Hex())
		}
		seen[n] = true
	}
	if a, b := NewAuthKey(), NewAuthKey(); a.Equal(b) || a.Equal(AuthKey{}) {
		t.Fatal("NewAuthKey repeats or is zero")
	}
}

func TestKeyPath(t *testing.T) {
	for in, want := range map[string]string{
		"/run/user/1000/malachi/rpc.sock":                               "/run/user/1000/malachi/rpc.sock.key",
		"/run/user/1000/app/io.github.schotek.Malachi/malachi/rpc.sock": "/run/user/1000/app/io.github.schotek.Malachi/malachi/rpc.sock.key",
		"/home/u/.cache/malachi/run/rpc.sock":                           "/home/u/.cache/malachi/run/rpc.sock.key",
		`C:\Users\u\AppData\Local\malachi\run\rpc.sock`:                 `C:\Users\u\AppData\Local\malachi\run\rpc.sock.key`,
		`\\?\C:\Users\u\AppData\Local\malachi\run\rpc.sock`:             `\\?\C:\Users\u\AppData\Local\malachi\run\rpc.sock.key`,
		"rpc.sock": "rpc.sock.key",
	} {
		if got := KeyPath(in); got != want {
			t.Errorf("KeyPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzParseKeyFile(f *testing.F) {
	var k AuthKey
	for i := range k {
		k[i] = byte(i)
	}
	f.Add(FormatKeyFile(k))
	f.Add([]byte{})
	f.Add([]byte(strings.Repeat("A", 64) + "\n"))
	f.Add([]byte(vectorKey + "\r\n"))
	f.Add([]byte("\ufeff" + vectorKey[3:] + "\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		k, err := ParseKeyFile(b)
		if err != nil {
			return
		}
		if !bytes.Equal(FormatKeyFile(k), b) {
			t.Fatalf("%q accepted but encodes back to %q", b, FormatKeyFile(k))
		}
	})
}

func FuzzParseAuthHex(f *testing.F) {
	f.Add(vectorDaemonProof)
	f.Add("")
	f.Add(strings.ToUpper(vectorClientProof))
	f.Add("0x" + vectorKey[2:])
	f.Add(strings.Repeat("\uff10", 21) + "0")
	f.Fuzz(func(t *testing.T, s string) {
		b, ok := ParseAuthHex(s)
		if !ok {
			return
		}
		if hex.EncodeToString(b[:]) != s {
			t.Fatalf("%q accepted but encodes back to %x", s, b)
		}
	})
}
