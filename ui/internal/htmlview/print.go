// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlview

import (
	"log/slog"
	"net/url"
	"os"
	"path/filepath"

	"github.com/diamondburned/gotk4-webkitgtk/pkg/webkit/v6"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// PrintPDFEnv names the development aid of printing: with a path in it the
// print is saved there as PDF, without the print dialog, never to a
// printer (as MALACHI_PRINT_PDF of the macOS client).
const PrintPDFEnv = "MALACHI_PRINT_PDF"

// printJobs keeps the views of running prints alive until WebKit's print
// operation ended (gotk4 would collect a view nobody holds).
var printJobs = map[*View]*webkit.PrintOperation{}

// Print prints fragment, a printing.Fragment (the application's escaped
// header HTML around the sanitiser's bodies; never anything else), from a
// viewer of its own: the same document, CSP, proxy, policy decisions and
// malachi-cid: scheme as a message on screen, so the print gets nothing
// reading does not. title names the job; parent is the dialog's window.
// done (may be nil) runs once on the main loop when the job ended, with
// whether it was printed.
func Print(log *slog.Logger, fetch PartFetcher, fragment, title string, parent *gtk.Window, done func(ok bool)) {
	v := New(log, fetch)
	v.log = v.log.With("job", "print")
	// WebKit prints only a mapped view: unmapped (even realized) it wrote
	// pages without any content, as off a window on macOS. The host is a
	// window nobody sees: fully transparent, undecorated, transient for
	// parent, never focused; a page's size at 72 dpi (at one pixel it
	// printed empty pages again). WebKit paginates for the paper.
	host := gtk.NewWindow()
	host.SetDecorated(false)
	host.SetOpacity(0)
	host.SetDefaultSize(612, 792)
	host.SetCanFocus(false)
	host.SetFocusable(false)
	host.SetCanTarget(false)
	if parent != nil {
		host.SetTransientFor(parent)
	}
	host.SetChild(v)
	host.Present()
	started := false
	finish := func(ok bool) {
		if _, running := printJobs[v]; !running {
			return
		}
		delete(printJobs, v)
		v.Close()
		host.Destroy()
		if done != nil {
			done(ok)
		}
	}
	printJobs[v] = nil
	v.web.ConnectLoadChanged(func(e webkit.LoadEvent) {
		if e != webkit.LoadFinished || started {
			return
		}
		started = true
		op := webkit.NewPrintOperation(v.web)
		printJobs[v] = op
		ok := true
		op.ConnectFailed(func(err error) {
			ok = false
			v.log.Warn("print failed", "err", err)
		})
		op.ConnectFinished(func() { finish(ok) })
		settings := gtk.NewPrintSettings()
		if title != "" {
			// The job's name in the print queue (GtkPrintOperation reads it
			// from the settings it is given).
			settings.Set("job-name", title)
		}
		if pdf := os.Getenv(PrintPDFEnv); pdf != "" {
			abs, err := filepath.Abs(pdf)
			if err != nil {
				v.log.Warn("print: pdf path", "err", err)
				finish(false)
				return
			}
			settings.SetPrinter("Print to File")
			settings.Set(gtk.PRINT_SETTINGS_OUTPUT_FILE_FORMAT, "pdf")
			settings.Set(gtk.PRINT_SETTINGS_OUTPUT_URI, (&url.URL{Scheme: "file", Path: abs}).String())
			op.SetPrintSettings(settings)
			op.Print()
			return
		}
		op.SetPrintSettings(settings)
		if op.RunDialog(parent) == webkit.PrintOperationResponseCancel {
			finish(false)
		}
	})
	v.web.LoadHtml(PrintDocument(fragment), "")
}
