// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Command malachi is the GTK4/libadwaita user interface of Malachi Mail.
//
// It is a thin client of the malachid daemon; see docs/architecture.md.
package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/accountwizard"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/daemon"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/style"
	"github.com/schotek/malachi/ui/internal/window"
)

// AppID must match the desktop file, metainfo, gschema and Flatpak manifest.
const AppID = "io.github.schotek.Malachi"

// version and localeDir are injected at build time:
// -ldflags "-X main.version=… -X main.localeDir=…".
var (
	version   = "dev"
	localeDir = ""
)

func main() {
	log := newLogger()
	// Translations first: every widget built from here on is localised.
	i18n.Init(i18n.LocaleDir(localeDir))

	// HandlesOpen: mailto: URIs arrive through the "open" signal (desktop
	// file MimeType, D-Bus Open, or `malachi mailto:…`).
	app := adw.NewApplication(AppID, gio.ApplicationHandlesOpen)
	rpc := client.New(client.DefaultSocketPath())
	// The daemon is ours to run: nothing on the desktop starts malachid
	// (the Flatpak has one command, the autostart entry is this binary).
	// One that already answers on the socket is used instead and left
	// alone on exit.
	daemonPath, err := daemon.Locate()
	if err != nil {
		log.Warn("not starting malachid; expecting one to be started by other means", "err", err)
	}
	sup := daemon.New(rpc.Socket, daemonPath, log)

	// Preferences are opened on startup (GTK and libadwaita are initialised
	// by then), before any window or action can use them.
	var (
		prefs   *settings.Store
		mainWin *window.Window
		mgr     *compose.Manager
		// serviceHold is set when started with --gapplication-service (the
		// autostart entry): there is no window yet, so hold the application
		// until the first activation shows one.
		serviceHold bool
	)
	app.ConnectStartup(func() {
		// Attachments a previous run wrote for opening (docs/security.md §8).
		window.SweepOpenedAttachments()
		addUninstalledIconPath()
		prefs = settings.Open(log)
		style.Apply(prefs)
		mgr = compose.NewManager(app, rpc, log, prefs)
		mgr.OnSent = func(text string) {
			if mainWin != nil {
				// A short confirmation: the outbox folder and the "sent"
				// toast that follows carry the rest.
				mainWin.ToastFor(text, 2)
			}
		}
		if app.Flags()&gio.ApplicationIsService != 0 {
			app.Hold()
			serviceHold = true
			log.Info("started as a service; running in the background until activated")
		}
		// Startup runs in the primary instance only, so this is the one
		// place a daemon is started even when no window ever opens (the
		// login autostart, a mailto: activation). Off the main loop: the
		// window's first dial waits for the same spawn.
		go func() {
			if err := sup.Ensure(); err != nil {
				log.Warn("malachid is not running", "err", err)
			}
		}()
	})
	// show presents the main window, creating it on first use. The window
	// hides instead of closing when "Run in Background" is on, so it is
	// reused; when it really closes the application exits with it.
	show := func() {
		if mainWin == nil {
			mainWin = window.New(app, rpc, log, prefs, mgr, sup)
		}
		mainWin.Present()
		if serviceHold {
			app.Release()
			serviceHold = false
		}
	}
	app.ConnectActivate(show)
	// mailto: handling. The UI only splits the URI (compose.ParseMailto);
	// everything else about the message is the backend's business.
	app.ConnectOpen(func(files []gio.Filer, hint string) {
		for _, f := range files {
			uri := f.URI()
			p, err := compose.ParseMailto(uri)
			if err != nil {
				log.Warn("ignoring non-mailto URI", "err", err)
				continue
			}
			mgr.Open(p)
		}
	})
	app.ConnectShutdown(func() {
		window.SweepOpenedAttachments()
		rpc.Close()
		// Quitting the application quits the daemon it started; "Run in
		// Background" keeps the application (and so the daemon) alive by
		// hiding the window instead. A UI crash skips this and leaves the
		// daemon running for the next start to find.
		sup.Stop()
	})

	addActions(app, rpc, log, func() *settings.Store { return prefs }, show, func() *compose.Manager { return mgr })
	os.Exit(app.Run(os.Args))
}

// iconDirEnv points at the application icon for uninstalled (development)
// runs. Installed, the icon sits in the hicolor theme and GTK finds it on
// its own; from a source tree nothing does, so the window and the about
// dialog would fall back to the generic placeholder.
const iconDirEnv = "MALACHI_ICON_DIR"

// addUninstalledIconPath makes the icon of a source tree visible to GTK.
// Call from startup, after GTK is initialised. A no-op when installed.
func addUninstalledIconPath() {
	dir := os.Getenv(iconDirEnv)
	if dir == "" {
		return
	}
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}
	gtk.IconThemeGetForDisplay(display).AddSearchPath(dir)
}

// addActions registers application actions. store yields the settings store,
// which exists only after startup has run; show presents the main window.
func addActions(app *adw.Application, rpc *client.Client, log *slog.Logger, store func() *settings.Store, show func(), composer func() *compose.Manager) {
	newMessage := gio.NewSimpleAction("compose", nil)
	newMessage.ConnectActivate(func(*glib.Variant) { composer().Open(compose.Params{}) })
	app.AddAction(newMessage)
	app.SetAccelsForAction("app.compose", []string{"<Control>n"})

	// app.show is the default action of desktop notifications and the way
	// a hidden (background) window comes back.
	showAction := gio.NewSimpleAction("show", nil)
	showAction.ConnectActivate(func(*glib.Variant) { show() })
	app.AddAction(showAction)

	about := gio.NewSimpleAction("about", nil)
	about.ConnectActivate(func(*glib.Variant) {
		d := adw.NewAboutDialog()
		d.SetApplicationName("Malachi Mail")
		d.SetApplicationIcon(AppID)
		d.SetDeveloperName("Malachi Mail contributors")
		d.SetVersion(version)
		d.SetLicenseType(gtk.LicenseGPL30)
		d.SetWebsite("https://github.com/schotek/malachi")
		d.SetIssueURL("https://github.com/schotek/malachi/issues")
		d.SetComments(i18n.T("A native mail client for the GNOME desktop."))
		d.Present(app.ActiveWindow())
	})
	app.AddAction(about)

	prefs := gio.NewSimpleAction("preferences", nil)
	prefs.ConnectActivate(func(*glib.Variant) {
		window.NewPreferences(store(), rpc, log).Present(app.ActiveWindow())
	})
	app.AddAction(prefs)
	app.SetAccelsForAction("app.preferences", []string{"<Control>comma"})

	// app.add-account opens the wizard from the main window's empty state
	// (and anywhere else without a reload callback: the window learns of
	// the new account through notify.accountsChanged).
	addAccount := gio.NewSimpleAction("add-account", nil)
	addAccount.ConnectActivate(func(*glib.Variant) {
		accountwizard.New(rpc, log).Present(app.ActiveWindow())
	})
	app.AddAction(addAccount)

	quit := gio.NewSimpleAction("quit", nil)
	quit.ConnectActivate(func(*glib.Variant) { app.Quit() })
	app.AddAction(quit)
	app.SetAccelsForAction("app.quit", []string{"<Control>q"})

	// Per-message actions of the main window (window.registerActions).
	// Single-letter accelerators are safe: the main window has no text
	// entry that could want the key. A message window mirrors them for its
	// msg.* group (window.messageShortcuts).
	for action, accel := range map[string]string{
		"win.trash":       "Delete",
		"win.archive":     "a",
		"win.junk":        "j",
		"win.mark-unread": "u",
		"win.toggle-flag": "s",
		"win.refresh":     "<Control>r",
	} {
		app.SetAccelsForAction(action, []string{accel})
	}
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
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}
