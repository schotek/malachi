// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Command malachid is the Malachi Mail backend daemon.
//
// It owns the mail store, talks IMAP/SMTP, and exposes everything to user
// interfaces through JSON-RPC 2.0 on a unix socket. See docs/architecture.md.
//
// Every connection first proves that it holds the key the daemon makes at
// every start and writes beside the socket, as PATH.key for --socket PATH
// (docs/api.md §1.4). There is no way to turn this off.
//
// The daemon itself is pkg/embed; this command parses the flags, opens the
// log file of --log-file and stops it on SIGINT and SIGTERM.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/pkg/embed"
)

// version is injected at build time: -ldflags "-X main.version=…".
var version = "dev"

// updateChannel is the package this build is installed from
// (docs/services.md §2.3), injected by the package recipes:
// -ldflags "-X main.updateChannel=macos-dmg". Without one the daemon never
// checks for updates; MALACHI_UPDATE_CHANNEL overrides it for tests.
var updateChannel = ""

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "malachid:", err)
		os.Exit(1)
	}
}

func run() (retErr error) {
	paths, err := config.ResolvePaths()
	if err != nil {
		return err
	}

	var (
		flagConfig  = flag.String("config", paths.ConfigFile(), "configuration file")
		flagStore   = flag.String("store", paths.StoreFile(), "SQLite database")
		flagSocket  = flag.String("socket", paths.SocketFile(), "JSON-RPC unix socket; its connection key is written beside it as PATH.key")
		flagVersion = flag.Bool("version", false, "print version and exit")
		flagLogFile = flag.String("log-file", "", "write the log to this file (0600, rotated at 5 MiB to FILE.1) instead of stderr")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintln(flag.CommandLine.Output(), "\nEnvironment:\n  "+embed.EnvTimeZone+
			"\n    \tthe user's IANA time zone (e.g. Europe/Prague), which the client passes; else TZ, else /etc/localtime"+
			"\n  "+embed.EnvDNSServers+
			"\n    \tDNS servers for MX, TXT and SRV lookups (e.g. 192.0.2.53,[2001:db8::53]:53), which the client passes where /etc/resolv.conf is missing")
	}
	flag.Parse()

	if *flagVersion {
		fmt.Println("malachid", version)
		return nil
	}

	var logOut io.Writer = os.Stderr
	if *flagLogFile != "" {
		lf, err := openLogFile(*flagLogFile)
		if err != nil {
			return err
		}
		defer lf.Close()
		logOut = lf
		// A panic or fatal error goes to the file too: the stderr of a
		// daemon started by a desktop app is often /dev/null.
		_ = lf.CrashOutput()
	}
	log := embed.NewLoggerTo(logOut)
	defer func() {
		if retErr != nil {
			log.Error("malachid stopped", "err", retErr)
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return embed.Run(ctx, embed.Options{
		Version:       version,
		UpdateChannel: updateChannel,
		Config:        *flagConfig,
		Store:         *flagStore,
		Socket:        *flagSocket,
		Log:           log,
	})
}
