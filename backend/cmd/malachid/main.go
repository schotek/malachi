// Command malachid is the Malachi Mail backend daemon.
//
// It owns the mail store, talks IMAP/SMTP, and exposes everything to user
// interfaces through JSON-RPC 2.0 on a unix socket. See docs/architecture.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/GITHUB_USER/malachi/backend/internal/config"
	"github.com/GITHUB_USER/malachi/backend/internal/rpc"
	"github.com/GITHUB_USER/malachi/backend/internal/store"
)

// version is injected at build time: -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "malachid:", err)
		os.Exit(1)
	}
}

func run() error {
	paths, err := config.ResolvePaths()
	if err != nil {
		return err
	}

	var (
		flagConfig  = flag.String("config", paths.ConfigFile(), "configuration file")
		flagStore   = flag.String("store", paths.StoreFile(), "SQLite database")
		flagSocket  = flag.String("socket", paths.SocketFile(), "JSON-RPC unix socket")
		flagVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *flagVersion {
		fmt.Println("malachid", version)
		return nil
	}

	log := newLogger()
	log.Info("starting malachid", "version", version, "pid", os.Getpid())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, found, err := config.Load(*flagConfig)
	if err != nil {
		return err
	}
	if found {
		log.Info("configuration loaded", "path", *flagConfig, "accounts", len(cfg.Accounts))
	} else {
		log.Info("no configuration file, using defaults", "path", *flagConfig)
	}

	st, err := store.Open(ctx, *flagStore, log)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("close store", "err", err)
		}
	}()
	log.Info("store ready", "path", st.Path())

	backend := &rpc.StubBackend{Version: version, StorePath: st.Path()}
	srv := rpc.NewServer(backend, log)
	if err := srv.Listen(*flagSocket); err != nil {
		return err
	}

	// TODO(phase-1): start per-account sync engines here, giving them srv as
	// their api.Notifier.

	err = srv.Serve(ctx)
	log.Info("shutting down", "reason", ctxReason(ctx))
	srv.Close() // idempotent; closes connections and unlinks the socket
	return err
}

func newLogger() *slog.Logger {
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
		fmt.Fprintln(os.Stderr, "malachid: unknown MALACHI_LOG_LEVEL, using info")
	}
	var h slog.Handler
	if os.Getenv("MALACHI_LOG_FORMAT") == "json" {
		h = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	} else {
		h = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	return slog.New(h)
}

func ctxReason(ctx context.Context) string {
	if err := context.Cause(ctx); err != nil {
		return err.Error()
	}
	return "server stopped"
}
