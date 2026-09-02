// Command malachi is the GTK4/libadwaita user interface of Malachi Mail.
//
// It is a thin client of the malachid daemon; see docs/architecture.md.
package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/style"
	"github.com/schotek/malachi/ui/internal/window"
)

// AppID must match the desktop file, metainfo, gschema and Flatpak manifest.
const AppID = "io.github.schotek.Malachi"

// version is injected at build time: -ldflags "-X main.version=…".
var version = "dev"

func main() {
	log := newLogger()

	app := adw.NewApplication(AppID, gio.ApplicationFlagsNone)
	rpc := client.New(client.DefaultSocketPath())

	// Preferences are opened on startup (GTK and libadwaita are initialised
	// by then), before any window or action can use them.
	var (
		prefs   *settings.Store
		mainWin *window.Window
		// serviceHold is set when started with --gapplication-service (the
		// autostart entry): there is no window yet, so hold the application
		// until the first activation shows one.
		serviceHold bool
	)
	app.ConnectStartup(func() {
		prefs = settings.Open(log)
		style.Apply(prefs)
		if app.Flags()&gio.ApplicationIsService != 0 {
			app.Hold()
			serviceHold = true
			log.Info("started as a service; running in the background until activated")
		}
	})
	// show presents the main window, creating it on first use. The window
	// hides instead of closing when "Run in Background" is on, so it is
	// reused; when it really closes the application exits with it.
	show := func() {
		if mainWin == nil {
			mainWin = window.New(app, rpc, log, prefs)
		}
		mainWin.Present()
		if serviceHold {
			app.Release()
			serviceHold = false
		}
	}
	app.ConnectActivate(show)
	app.ConnectShutdown(func() { rpc.Close() })

	addActions(app, func() *settings.Store { return prefs }, show)
	os.Exit(app.Run(os.Args))
}

// addActions registers application actions. store yields the settings store,
// which exists only after startup has run; show presents the main window.
func addActions(app *adw.Application, store func() *settings.Store, show func()) {
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
		d.SetComments("A native mail client for the GNOME desktop.")
		d.Present(app.ActiveWindow())
	})
	app.AddAction(about)

	prefs := gio.NewSimpleAction("preferences", nil)
	prefs.ConnectActivate(func(*glib.Variant) {
		window.NewPreferences(store()).Present(app.ActiveWindow())
	})
	app.AddAction(prefs)
	app.SetAccelsForAction("app.preferences", []string{"<Control>comma"})

	quit := gio.NewSimpleAction("quit", nil)
	quit.ConnectActivate(func(*glib.Variant) { app.Quit() })
	app.AddAction(quit)
	app.SetAccelsForAction("app.quit", []string{"<Control>q"})
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
