// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The test binary doubles as the helper (the pattern of
// ui/internal/daemon): when fakeEnv is set, TestMain runs fakeHelper
// instead of the tests. The keyring inherits the environment, so a
// t.Setenv in the test selects the behaviour of the process it spawns.
const (
	fakeEnv   = "MALACHI_TEST_FAKE_HELPER"
	fakeStore = "MALACHI_TEST_FAKE_STORE" // directory: store.json and pid
	fakeMode  = "MALACHI_TEST_FAKE_MODE"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeEnv) == "1" {
		os.Exit(fakeHelper())
	}
	os.Exit(m.Run())
}

// fakeHelper speaks the protocol strictly: argv[1] is the operation, stdin
// exactly one JSON line. Modes: "" (a working store in a JSON file),
// "exit1-with-value-in-stderr" (fails and echoes the value),
// "sleep" (records its pid, never answers), "garbage-stdout" (exit 0 with
// a non-JSON answer), "exit3" (rejects the request).
func fakeHelper() int {
	dir := os.Getenv(fakeStore)
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "fake: expected exactly one argument")
		return exitBadRequest
	}
	op := os.Args[1]
	in, err := io.ReadAll(os.Stdin)
	if err != nil || !strings.HasSuffix(string(in), "\n") || strings.Count(string(in), "\n") != 1 {
		fmt.Fprintln(os.Stderr, "fake: stdin must be exactly one line")
		return exitBadRequest
	}
	var req request
	if err := json.Unmarshal(in, &req); err != nil || req.Account == "" || req.Key == "" {
		fmt.Fprintln(os.Stderr, "fake: bad request")
		return exitBadRequest
	}
	switch os.Getenv(fakeMode) {
	case "exit1-with-value-in-stderr":
		fmt.Fprintf(os.Stderr, "fake: cannot store %q for %s\n\tsecond line\x00\x1b[0m\n", req.Value, req.Account)
		return 1
	case "sleep":
		_ = os.WriteFile(filepath.Join(dir, "pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
		time.Sleep(10 * time.Second)
		return 0
	case "garbage-stdout":
		fmt.Println("not json at all")
		return 0
	case "exit3":
		fmt.Fprintln(os.Stderr, "fake: refused")
		return exitBadRequest
	}

	storePath := filepath.Join(dir, "store.json")
	items := map[string]string{}
	if data, err := os.ReadFile(storePath); err == nil {
		_ = json.Unmarshal(data, &items)
	}
	id := req.Account + "/" + req.Key
	switch op {
	case "get":
		v, ok := items[id]
		if !ok {
			return exitNotFound
		}
		out, _ := json.Marshal(response{Value: &v})
		fmt.Println(string(out))
		return 0
	case "set":
		if req.Value == "" {
			fmt.Fprintln(os.Stderr, "fake: set without value")
			return exitBadRequest
		}
		items[id] = req.Value
	case "delete":
		if _, ok := items[id]; !ok {
			return exitNotFound
		}
		delete(items, id)
	default:
		fmt.Fprintln(os.Stderr, "fake: unknown operation")
		return exitBadRequest
	}
	data, _ := json.Marshal(items)
	if err := os.WriteFile(storePath, data, 0o600); err != nil {
		return 1
	}
	fmt.Println("{}")
	return 0
}

// newKeyring points a keyring at the test binary in the given mode.
func newKeyring(t *testing.T, mode string) (*Keyring, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(fakeEnv, "1")
	t.Setenv(fakeStore, dir)
	t.Setenv(fakeMode, mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	k, err := New(exe, nil)
	if err != nil {
		t.Fatal(err)
	}
	return k, dir
}

func TestRoundTrip(t *testing.T) {
	k, _ := newKeyring(t, "")
	ctx := context.Background()
	if err := k.Set(ctx, "acc_1", auth.KeyPassword, "hunter2"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := k.Set(ctx, "acc_1", auth.KeyRefreshToken, "tok"); err != nil {
		t.Fatalf("Set token: %v", err)
	}
	if v, err := k.Get(ctx, "acc_1", auth.KeyPassword); err != nil || v != "hunter2" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if err := k.Set(ctx, "acc_1", auth.KeyPassword, "changed"); err != nil {
		t.Fatalf("Set again: %v", err)
	}
	if v, err := k.Get(ctx, "acc_1", auth.KeyPassword); err != nil || v != "changed" {
		t.Fatalf("Get after replace = %q, %v", v, err)
	}
	if err := k.Delete(ctx, "acc_1", auth.KeyPassword); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := k.Get(ctx, "acc_1", auth.KeyPassword); !errors.Is(err, auth.ErrNoSecret) {
		t.Fatalf("Get after delete = %v, want ErrNoSecret", err)
	}
	if v, err := k.Get(ctx, "acc_1", auth.KeyRefreshToken); err != nil || v != "tok" {
		t.Fatalf("other key touched: %q, %v", v, err)
	}
}

func TestGetMissingIsErrNoSecret(t *testing.T) {
	k, _ := newKeyring(t, "")
	if _, err := k.Get(context.Background(), "acc_none", auth.KeyPassword); !errors.Is(err, auth.ErrNoSecret) {
		t.Fatalf("Get = %v, want ErrNoSecret", err)
	}
}

func TestDeleteMissingIsNotAnError(t *testing.T) {
	k, _ := newKeyring(t, "")
	if err := k.Delete(context.Background(), "acc_none", auth.KeyPassword); err != nil {
		t.Fatalf("Delete = %v, want nil", err)
	}
}

func TestStderrIsScrubbedAndRedacted(t *testing.T) {
	k, _ := newKeyring(t, "exit1-with-value-in-stderr")
	err := k.Set(context.Background(), "acc_1", auth.KeyPassword, "s3cret")
	if code(t, err) != api.CodeKeyringError {
		t.Fatalf("Set = %v", err)
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "s3cret"):
		t.Fatalf("value leaked into the error: %q", msg)
	case !strings.Contains(msg, "<redacted>"):
		t.Fatalf("value not marked as redacted: %q", msg)
	case !strings.Contains(msg, "exit status 1"):
		t.Fatalf("exit status missing: %q", msg)
	case strings.ContainsAny(msg, "\n\t\x00\x1b"):
		t.Fatalf("control characters survived: %q", msg)
	case !strings.Contains(msg, "second line"):
		t.Fatalf("stderr lines not joined: %q", msg)
	}
}

func TestTimeoutKillsTheHelper(t *testing.T) {
	k, dir := newKeyring(t, "sleep")
	k.timeout = time.Second
	start := time.Now()
	_, err := k.Get(context.Background(), "acc_1", auth.KeyPassword)
	if code(t, err) != api.CodeKeyringError || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Get = %v, want a timeout keyringError", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("Get blocked %s past the timeout", time.Since(start))
	}
	raw, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		t.Fatalf("the fake did not record its pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("helper %d still exists after the timeout (kill 0: %v)", pid, err)
	}
}

func TestCancelPassesThrough(t *testing.T) {
	k, _ := newKeyring(t, "sleep")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, err := k.Get(ctx, "acc_1", auth.KeyPassword); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get = %v, want context.Canceled", err)
	}
}

func TestGarbageStdoutIsKeyringError(t *testing.T) {
	k, _ := newKeyring(t, "garbage-stdout")
	_, err := k.Get(context.Background(), "acc_1", auth.KeyPassword)
	if code(t, err) != api.CodeKeyringError || !strings.Contains(err.Error(), "no parseable value") {
		t.Fatalf("Get = %v", err)
	}
	// set and delete ignore stdout: exit 0 is success.
	if err := k.Set(context.Background(), "acc_1", auth.KeyPassword, "x"); err != nil {
		t.Fatalf("Set = %v", err)
	}
}

func TestBadRequestIsKeyringError(t *testing.T) {
	k, _ := newKeyring(t, "exit3")
	err := k.Set(context.Background(), "acc_1", auth.KeyPassword, "x")
	if code(t, err) != api.CodeKeyringError || !strings.Contains(err.Error(), "rejected") || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("Set = %v", err)
	}
	// Exit 3 on get is a failure too, never "no secret".
	if _, err := k.Get(context.Background(), "acc_1", auth.KeyPassword); errors.Is(err, auth.ErrNoSecret) || code(t, err) != api.CodeKeyringError {
		t.Fatalf("Get = %v", err)
	}
}

func TestNewRejectsBadPaths(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"empty":          "",
		"relative":       "malachi-keychain",
		"missing":        filepath.Join(dir, "nonexistent"),
		"not executable": plain,
		"directory":      dir,
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			k, err := New(path, nil)
			if err == nil || k != nil {
				t.Fatalf("New(%q) = %v, %v; want an error", path, k, err)
			}
			if !strings.Contains(err.Error(), PathEnv) {
				t.Fatalf("error does not name %s: %v", PathEnv, err)
			}
		})
	}
	t.Run("symlink to an executable", func(t *testing.T) {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link")
		if err := os.Symlink(exe, link); err != nil {
			t.Fatal(err)
		}
		if _, err := New(link, nil); err != nil {
			t.Fatalf("New(symlink) = %v", err)
		}
	})
}

func TestScrub(t *testing.T) {
	got := scrub([]byte("  bad\nthing\x07 s3cret here \xff\n"), "s3cret")
	if got != "bad thing <redacted> here" {
		t.Fatalf("scrub = %q", got)
	}
	if got := scrub(nil, ""); got != "(no diagnostic)" {
		t.Fatalf("empty scrub = %q", got)
	}
	long := scrub([]byte(strings.Repeat("a", 500)), "")
	if len(long) > maxErrReport+len("…") || !strings.HasSuffix(long, "…") {
		t.Fatalf("long scrub not truncated: %d bytes", len(long))
	}
}

// TestScrubRedactsBeforeMapping covers a value that holds a control
// character: mapping first would turn "s3c\tret" into "s3c ret" and the
// redaction would miss it, or drop the bell and glue "s3cret" together.
func TestScrubRedactsBeforeMapping(t *testing.T) {
	cases := []struct{ secret, stderr, want string }{
		{"s3c\tret", "helper said: s3c\tret (bad)", "helper said: <redacted> (bad)"},
		{"s3c\nret", "line s3c\nret end", "line <redacted> end"},
		{"s3c\x07ret", "s3c\x07ret and s3cret", "<redacted> and s3cret"},
		{"a  b", "value a  b rejected", "value <redacted> rejected"},
	}
	for _, c := range cases {
		if got := scrub([]byte(c.stderr), c.secret); got != c.want {
			t.Errorf("scrub(%q, %q) = %q, want %q", c.stderr, c.secret, got, c.want)
		}
	}
	// A value longer than the report is redacted whole, not cut to a prefix.
	secret := strings.Repeat("p", 300)
	got := scrub([]byte("refused "+secret+" now"), secret)
	if got != "refused <redacted> now" {
		t.Errorf("long value: scrub = %q", got)
	}
}

func TestCappedWriter(t *testing.T) {
	c := &capped{max: 4}
	for _, chunk := range []string{"ab", "cdef", "gh"} {
		if n, err := c.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if string(c.buf) != "abcd" {
		t.Fatalf("buf = %q", c.buf)
	}
}

// TestRealHelper drives an actual helper binary (MALACHI_TEST_REAL_HELPER
// names it, e.g. the built malachi-keychain on macOS) through a round trip
// under an account id no real account has. It may bring up the platform's
// access prompt, so it only runs on request.
func TestRealHelper(t *testing.T) {
	path := os.Getenv("MALACHI_TEST_REAL_HELPER")
	if path == "" {
		t.Skip("MALACHI_TEST_REAL_HELPER not set")
	}
	k, err := New(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	account := api.AccountID(fmt.Sprintf("test_%d", time.Now().UnixNano()))
	t.Cleanup(func() { _ = k.Delete(context.Background(), account, auth.KeyPassword) })

	if _, err := k.Get(ctx, account, auth.KeyPassword); !errors.Is(err, auth.ErrNoSecret) {
		t.Fatalf("Get before Set = %v, want ErrNoSecret", err)
	}
	const value = "pa\"ss wörd\n" // quoting, spaces, non-ASCII and a line break must survive
	if err := k.Set(ctx, account, auth.KeyPassword, value); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, err := k.Get(ctx, account, auth.KeyPassword); err != nil || v != value {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if err := k.Set(ctx, account, auth.KeyPassword, "second"); err != nil {
		t.Fatalf("Set again: %v", err)
	}
	if v, err := k.Get(ctx, account, auth.KeyPassword); err != nil || v != "second" {
		t.Fatalf("Get after replace = %q, %v", v, err)
	}
	if err := k.Delete(ctx, account, auth.KeyPassword); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := k.Get(ctx, account, auth.KeyPassword); !errors.Is(err, auth.ErrNoSecret) {
		t.Fatalf("Get after Delete = %v, want ErrNoSecret", err)
	}
	if err := k.Delete(ctx, account, auth.KeyPassword); err != nil {
		t.Fatalf("Delete of a missing item = %v, want nil", err)
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
