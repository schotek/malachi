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

	app.ConnectActivate(func() {
		if win := app.ActiveWindow(); win != nil {
			win.Present()
			return
		}
		window.New(app, rpc, log).Present()
	})
	app.ConnectShutdown(func() { rpc.Close() })

	addActions(app, log)
	os.Exit(app.Run(os.Args))
}

func addActions(app *adw.Application, log *slog.Logger) {
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
		log.Info("preferences: not implemented")
	})
	app.AddAction(prefs)

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
