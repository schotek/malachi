// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package embed runs the Malachi Mail daemon inside a host process: the
// malachid command is a thin wrapper around Run, and a host that cannot
// start a separate process links the same code as a library.
//
// Run does everything the daemon does: it locks and opens the store,
// imports the configured accounts, listens on the JSON-RPC unix socket
// with its connection key beside it (docs/api.md §1.4), syncs and
// maintains until its context ends, then shuts down in order. Clients
// still talk to it only through the socket, wherever it runs.
//
// The daemon reads its environment as the command does (MALACHI_KEYRING,
// MALACHI_TIME_ZONE, MALACHI_DNS_SERVERS, MALACHI_DEFAULT_*,
// MALACHI_UPDATE_CHANNEL, MALACHI_LOG_LEVEL, MALACHI_LOG_FORMAT); a host
// sets the variables before Run. Options.Keyring replaces the keyring
// MALACHI_KEYRING would choose.
package embed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
	// Embedded IANA tzdata, the same on every platform (Windows has no
	// system zoneinfo): docs/calendar.md D4.
	_ "time/tzdata"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/schotek/malachi/backend/cmd/malachi-mcp/mcpbridge"
	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/auth/helper"
	"github.com/schotek/malachi/backend/internal/auth/secretservice"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/core"
	"github.com/schotek/malachi/backend/internal/rpc"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Options are what the command line gives the daemon.
type Options struct {
	// Version is reported by system.info and to update checks.
	Version string
	// UpdateChannel is the package this build is installed from
	// (docs/services.md §2.3); empty never checks for updates.
	// MALACHI_UPDATE_CHANNEL overrides it.
	UpdateChannel string

	// Config is config.toml; a missing file means defaults.
	Config string
	// Store is the SQLite database; the store lock and the raw messages
	// live beside it.
	Store string
	// Socket is the JSON-RPC unix socket; its connection key is written
	// beside it as Socket + ".key". The platform limits its length (about
	// 103 bytes on Darwin, 107 on Linux and Windows).
	Socket string

	// Keyring stores passwords and tokens. Nil chooses by MALACHI_KEYRING
	// (secretservice, helper or none).
	Keyring Keyring
	// Log takes the daemon's log. Nil logs text (MALACHI_LOG_FORMAT=json:
	// JSON) to stderr at MALACHI_LOG_LEVEL.
	Log *slog.Logger

	// Bridge serves the MCP bridge of the daemon's own assistant on a
	// goroutine of this process, for a host that cannot start processes:
	// a session (assistant.sessionStart) that names no bridge executable
	// gets the bridge of package mcpbridge over an in-memory transport,
	// with the arguments the executable would get. It still connects to
	// Socket with the handshake like any client, and it reads no
	// document (a PDF, DOCX or XLSX attachment is withheld; the
	// executable reads them in a worker process of its own). A session
	// that names an executable starts it as without Bridge.
	Bridge bool
}

// Keyring is the secret store an embedding host implements in place of a
// helper process. Values must never be logged or kept beyond the call.
//
// Get returns ErrNotFound when nothing is stored under the account and the
// key; Set replaces an existing item; Delete of a missing item returns nil
// or ErrNotFound. Any other error is reported to clients as keyringError;
// its text reaches the daemon's log and must not hold a value.
type Keyring interface {
	Get(ctx context.Context, account api.AccountID, key string) (string, error)
	Set(ctx context.Context, account api.AccountID, key, value string) error
	Delete(ctx context.Context, account api.AccountID, key string) error
}

// ErrNotFound is what a Keyring returns for a missing item.
var ErrNotFound = auth.ErrNoSecret

// SyncStopTimeout caps how long shutdown waits for the sync supervisor and
// the maintenance together.
const SyncStopTimeout = 10 * time.Second

// Run runs the daemon until ctx ends or its server stops, and returns
// after the store is closed and the socket and its key file are removed,
// or after SyncStopTimeout when a syncer hangs. The error is why it could
// not start or why it stopped; a cancelled ctx alone returns nil.
func Run(ctx context.Context, o Options) error {
	switch {
	case o.Config == "":
		return errors.New("no configuration path")
	case o.Store == "":
		return errors.New("no store path")
	case o.Socket == "":
		return errors.New("no socket path")
	}
	log := o.Log
	if log == nil {
		log = NewLogger()
	}
	log.Info("starting malachid", "version", o.Version, "pid", os.Getpid())

	cfg, found, err := config.Load(o.Config)
	if err != nil {
		return err
	}
	if found {
		log.Info("configuration loaded", "path", o.Config, "accounts", len(cfg.Accounts),
			"oauthClients", oauthClients(cfg))
		config.WarnExposedSecret(log, o.Config, cfg)
	} else {
		log.Info("no configuration file, using defaults", "path", o.Config)
	}

	// One daemon per store, and before it touches the store or the socket:
	// a second one would sync, send and write the same data, and its
	// socket check can be fooled into taking a live daemon's socket.
	lock, err := store.Lock(ctx, o.Store)
	if err != nil {
		return err
	}
	defer func() { // runs last: the store is closed by then
		if err := lock.Close(); err != nil {
			log.Error("release store lock", "err", err)
		}
	}()
	log.Debug("store locked", "lock", lock.Path())

	st, err := store.Open(ctx, o.Store, log)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("close store", "err", err)
		}
	}()
	log.Info("store ready", "path", st.Path())

	setPreferredZone(log)
	useDNSServers(log)
	backend := core.New(o.Version, st, cfg, log)
	defer backend.Close() // idempotent; also on the early returns below
	backend.Updates.Channel = o.UpdateChannel
	if v, ok := os.LookupEnv("MALACHI_UPDATE_CHANNEL"); ok {
		backend.Updates.Channel = v
	}
	if c := backend.Updates.Channel; c != "" {
		log.Info("update channel", "channel", c)
	}
	if o.Keyring != nil {
		log.Info("keyring: the embedding host's")
		backend.Keyring = hostKeyring{o.Keyring}
	} else {
		ks, closeKeyring, err := keyringFromEnv(log)
		if err != nil {
			return err
		}
		defer closeKeyring()
		backend.Keyring = ks
	}
	// Defaults of preferences the starting process chose (the macOS app);
	// StartSync stores the ones without a value.
	defaults, envErr := core.RuntimeDefaultsFromEnv(os.LookupEnv)
	if envErr != nil {
		log.Warn("ignoring an invalid preference default", "err", envErr)
	}
	backend.SetRuntimeDefaults(defaults)
	if err := backend.ImportConfigAccounts(ctx); err != nil {
		return fmt.Errorf("import accounts from %s: %w", o.Config, err)
	}
	srv := rpc.NewServer(backend, log)
	backend.SetNotifier(srv)
	if err := srv.Listen(o.Socket); err != nil {
		return err
	}
	// Idempotent; removes the key file and the socket on every way out.
	defer srv.Close()
	// The assistant's bridge connects where the clients do.
	backend.SocketPath = o.Socket
	if o.Bridge {
		version := o.Version
		backend.InProcessBridge = func(ctx context.Context, args []string, t mcp.Transport) error {
			return mcpbridge.ServeArgs(ctx, args, mcpbridge.Options{Version: version}, t)
		}
		log.Info("assistant bridge: in this process")
	}
	syncDone := backend.StartSync(ctx)
	maintainDone := make(chan struct{})
	go func() {
		defer close(maintainDone)
		backend.Maintain(ctx)
	}()

	// Say why the daemon stops before the server logs its own shutdown:
	// Serve's context ends only after this line.
	serveCtx, endServe := context.WithCancel(context.WithoutCancel(ctx))
	stopLog := context.AfterFunc(ctx, func() {
		log.Info("shutting down", "reason", ctxReason(ctx))
		endServe()
	})
	err = srv.Serve(serveCtx)
	if stopLog() { // Serve ended although ctx did not
		log.Info("shutting down", "reason", ctxReason(ctx))
	}
	endServe()
	srv.Close()     // idempotent; removes the key file and the socket, closes connections
	backend.Close() // ends waiting sign-ins and closes their listeners

	// Let the syncers log out and finish their current store writes, and
	// the maintenance its current batch, before the deferred store close;
	// a hung connection must not hold the exit.
	deadline := time.After(SyncStopTimeout)
	select {
	case <-syncDone:
	case <-deadline:
		log.Warn("sync supervisor did not stop in time", "timeout", SyncStopTimeout)
		return err
	}
	select {
	case <-maintainDone:
	case <-deadline:
		log.Warn("maintenance did not stop in time", "timeout", SyncStopTimeout)
	}
	return err
}

// keyringFromEnv is the keyring MALACHI_KEYRING names and what releases it.
func keyringFromEnv(log *slog.Logger) (auth.Keyring, func(), error) {
	switch v := strings.ToLower(os.Getenv("MALACHI_KEYRING")); v {
	case "", "secretservice":
		ks := secretservice.New(log)
		return ks, func() { ks.Close() }, nil
	case "helper":
		ks, err := helper.New(os.Getenv(helper.PathEnv), log)
		if err != nil {
			return nil, nil, fmt.Errorf("MALACHI_KEYRING=helper: %w", err)
		}
		log.Info("keyring: external helper", "path", os.Getenv(helper.PathEnv))
		return ks, func() {}, nil
	case "none":
		log.Warn("MALACHI_KEYRING=none: passwords and sign-ins cannot be stored; adding an account with a password or the backend's own OAuth2 sign-in fails with keyringError")
		return auth.UnavailableKeyring{}, func() {}, nil
	default:
		return nil, nil, fmt.Errorf("unknown MALACHI_KEYRING %q (secretservice|helper|none)", v)
	}
}

// hostKeyring holds a host's Keyring to the auth.Keyring contract: a
// missing item is auth.ErrNoSecret for Get and no error for Delete, a
// cancelled call is the context's error, and every other failure an
// *api.Error with CodeKeyringError.
type hostKeyring struct{ k Keyring }

var _ auth.Keyring = hostKeyring{}

func (h hostKeyring) Get(ctx context.Context, account api.AccountID, key string) (string, error) {
	v, err := h.k.Get(ctx, account, key)
	switch {
	case err == nil:
		return v, nil
	case errors.Is(err, ErrNotFound):
		return "", auth.ErrNoSecret
	}
	return "", keyringError(ctx, "get", err)
}

func (h hostKeyring) Set(ctx context.Context, account api.AccountID, key, value string) error {
	err := h.k.Set(ctx, account, key, value)
	if err == nil {
		return nil
	}
	return keyringError(ctx, "set", err)
}

func (h hostKeyring) Delete(ctx context.Context, account api.AccountID, key string) error {
	err := h.k.Delete(ctx, account, key)
	if err == nil || errors.Is(err, ErrNotFound) {
		return nil
	}
	return keyringError(ctx, "delete", err)
}

func keyringError(ctx context.Context, op string, err error) error {
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return err
	}
	var e *api.Error
	if errors.As(err, &e) && e.Code == api.CodeKeyringError {
		return e
	}
	return api.NewError(api.CodeKeyringError, "host keyring: %s: %v", op, err)
}

// oauthClients names the providers config.toml has an OAuth client for
// (the ids themselves are not logged).
func oauthClients(cfg config.Config) []string {
	var out []string
	if cfg.OAuth2.Google.ClientID != "" {
		out = append(out, "google")
	}
	if cfg.OAuth2.Microsoft.ClientID != "" {
		out = append(out, "microsoft")
	}
	return out
}

// NewLogger is the daemon's default log: text, or JSON with
// MALACHI_LOG_FORMAT=json, to stderr at MALACHI_LOG_LEVEL (debug, info,
// warn, error; default info).
func NewLogger() *slog.Logger {
	return NewLoggerTo(os.Stderr)
}

// NewLoggerTo is NewLogger writing to out: the malachid command passes
// the file of its --log-file.
func NewLoggerTo(out io.Writer) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("MALACHI_LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	case "info", "":
	default:
		fmt.Fprintln(out, "malachid: unknown MALACHI_LOG_LEVEL, using info")
	}
	var h slog.Handler
	if os.Getenv("MALACHI_LOG_FORMAT") == "json" {
		h = slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level})
	} else {
		h = slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})
	}
	return slog.New(h)
}

func ctxReason(ctx context.Context) string {
	if err := context.Cause(ctx); err != nil {
		return err.Error()
	}
	return "server stopped"
}
