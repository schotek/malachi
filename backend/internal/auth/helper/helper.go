// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package helper implements auth.Keyring over an external program, the way
// git talks to a credential helper: one short-lived process per operation,
// the request on stdin, the answer on stdout, the outcome in the exit
// status. It is the platform-neutral extension point for desktops without a
// Secret Service (MALACHI_KEYRING=helper, the program named by
// MALACHI_KEYRING_HELPER); the macOS app ships malachi-keychain over the
// login keychain as its helper. The daemon itself stays free of platform
// code.
//
// Protocol. The daemon runs `<helper> get|set|delete` and writes exactly one
// JSON line to stdin, `{"account":"…","key":"…"}` (`"value"` added for
// set), then closes it. Values never travel in argv, a file or the
// environment. Exit 0 is success; for get, stdout holds one line
// `{"value":"…"}`. Exit 2 means no such item, exit 3 a malformed request;
// any other status, a signal or the timeout is a keyringError carrying the
// helper's stderr with control characters removed, the value redacted and
// at most 200 bytes kept. Nothing here logs a value.
//
// Trust model (docs/security.md §6): the helper runs as the same user as
// the daemon, so whoever can run it could already read the store and the
// RPC socket — the same boundary the Secret Service client has.
package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// PathEnv names the environment variable holding the helper's path.
	PathEnv = "MALACHI_KEYRING_HELPER"
	// CallTimeout bounds one helper run. A Keychain access-control prompt
	// waits for the user; account.add already budgets that much.
	CallTimeout = 30 * time.Second

	// Exit statuses of the protocol.
	exitNotFound   = 2
	exitBadRequest = 3

	maxStdout    = 64 << 10 // the answer is one short line
	maxStderr    = 4 << 10  // enough for a diagnostic, not for a dump
	maxErrReport = 200      // bytes of stderr that reach an error message
	// waitDelay bounds how long Wait blocks on the pipes after the process
	// is gone (a grandchild could hold them open).
	waitDelay = 2 * time.Second
)

// errNotFound is the helper's exit 2 before Get and Delete map it.
var errNotFound = errors.New("keychain helper: not found")

type request struct {
	Account string `json:"account"`
	Key     string `json:"key"`
	Value   string `json:"value,omitempty"`
}

// response is the helper's answer to get. A pointer so that a reply
// without "value" is an error, not an empty secret.
type response struct {
	Value *string `json:"value"`
}

// Keyring is an auth.Keyring running one helper process per operation.
type Keyring struct {
	path    string
	log     *slog.Logger
	timeout time.Duration
}

var _ auth.Keyring = (*Keyring)(nil)

// New validates the helper path — absolute, and after following symlinks
// a regular file with an execute bit — and returns the keyring. The error
// names PathEnv so the operator knows what to fix.
func New(path string, log *slog.Logger) (*Keyring, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if err := checkPath(path); err != nil {
		return nil, err
	}
	return &Keyring{path: path, log: log.With("component", "keyring"), timeout: CallTimeout}, nil
}

func checkPath(path string) error {
	if path == "" {
		return fmt.Errorf("%s is not set", PathEnv)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s must be an absolute path, got %q", PathEnv, path)
	}
	fi, err := os.Stat(path) // follows symlinks
	if err != nil {
		return fmt.Errorf("%s: %w", PathEnv, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: %s is not a regular file", PathEnv, path)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s: %s is not executable", PathEnv, path)
	}
	return nil
}

// Get returns the stored value or auth.ErrNoSecret.
func (k *Keyring) Get(ctx context.Context, account api.AccountID, key string) (string, error) {
	out, err := k.run(ctx, "get", request{Account: string(account), Key: key})
	switch {
	case errors.Is(err, errNotFound):
		return "", auth.ErrNoSecret
	case err != nil:
		return "", err
	}
	var resp response
	if err := json.Unmarshal(out, &resp); err != nil || resp.Value == nil {
		return "", api.NewError(api.CodeKeyringError, "keychain helper: get returned no parseable value")
	}
	return *resp.Value, nil
}

// Set stores value, replacing an existing item.
func (k *Keyring) Set(ctx context.Context, account api.AccountID, key, value string) error {
	_, err := k.run(ctx, "set", request{Account: string(account), Key: key, Value: value})
	if errors.Is(err, errNotFound) {
		return api.NewError(api.CodeKeyringError, "keychain helper: set reported the item missing")
	}
	return err
}

// Delete removes the item. A missing item is not an error.
func (k *Keyring) Delete(ctx context.Context, account api.AccountID, key string) error {
	_, err := k.run(ctx, "delete", request{Account: string(account), Key: key})
	if errors.Is(err, errNotFound) {
		return nil
	}
	return err
}

// run executes one helper process and returns its stdout. Errors leave
// here mapped to the Keyring contract: errNotFound for exit 2,
// context.Canceled when the caller gave up, otherwise *api.Error with
// CodeKeyringError.
func (k *Keyring) run(parent context.Context, op string, req request) ([]byte, error) {
	in, err := json.Marshal(req)
	if err != nil {
		return nil, api.NewError(api.CodeKeyringError, "keychain helper: encode request: %v", err)
	}
	in = append(in, '\n')

	ctx, cancel := context.WithTimeout(parent, k.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, k.path, op)
	cmd.Stdin = bytes.NewReader(in)
	stdout := &capped{max: maxStdout}
	stderr := &capped{max: maxStderr}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = waitDelay

	err = cmd.Run()
	status := "exit status 0"
	if cmd.ProcessState != nil {
		status = cmd.ProcessState.String()
	}
	k.log.Debug("keyring helper", "op", op, "account", req.Account, "key", req.Key, "status", status)
	if err == nil {
		return stdout.buf, nil
	}

	if errors.Is(parent.Err(), context.Canceled) {
		return nil, parent.Err()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, api.NewError(api.CodeKeyringError, "keychain helper: %s timed out after %s", op, k.timeout)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		switch exitErr.ExitCode() {
		case exitNotFound:
			return nil, errNotFound
		case exitBadRequest:
			return nil, api.NewError(api.CodeKeyringError, "keychain helper: rejected the %s request: %s", op, scrub(stderr.buf, req.Value))
		}
		return nil, api.NewError(api.CodeKeyringError, "keychain helper: %s: %s", status, scrub(stderr.buf, req.Value))
	}
	// Could not start, or the pipes outlived the process (ErrWaitDelay).
	return nil, api.NewError(api.CodeKeyringError, "keychain helper: %v", err)
}

// scrub prepares helper stderr for an error message: the secret becomes
// <redacted>, control characters go (line breaks become spaces), and only
// the first maxErrReport bytes survive, in that order. Redaction runs on
// the raw bytes first, so a value that itself holds a tab or a line break
// is still recognised before the mapping would change it; it runs again
// after the mapping for a value the mapping could have assembled; and it
// runs before truncation so a cut cannot leave a prefix of the value
// behind.
func scrub(stderr []byte, secret string) string {
	raw := string(stderr)
	if secret != "" {
		raw = strings.ReplaceAll(raw, secret, "<redacted>")
	}
	s := strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, strings.ToValidUTF8(raw, ""))
	if secret != "" {
		s = strings.ReplaceAll(s, secret, "<redacted>")
	}
	s = strings.TrimSpace(s)
	if len(s) > maxErrReport {
		s = strings.ToValidUTF8(s[:maxErrReport], "") + "…"
	}
	if s == "" {
		return "(no diagnostic)"
	}
	return s
}

// capped keeps the first max bytes written and drops the rest without
// failing the copy, so a chatty helper cannot grow the daemon's memory.
type capped struct {
	buf []byte
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - len(c.buf); room > 0 {
		if len(p) > room {
			c.buf = append(c.buf, p[:room]...)
		} else {
			c.buf = append(c.buf, p...)
		}
	}
	return len(p), nil
}
