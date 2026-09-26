// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package preview shows a file in the desktop's quick previewer: GNOME's
// Sushi, over D-Bus (org.gnome.NautilusPreviewer2 on
// org.gnome.NautilusPreviewer), the way Files does on the space bar. The
// previewer renders and never runs anything. Inside Flatpak this needs
// --talk-name=org.gnome.NautilusPreviewer and a file the host can read;
// without Sushi (another desktop, the package not installed) the call
// fails and the caller falls back to something else.
package preview

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

const (
	previewerName  = "org.gnome.NautilusPreviewer"
	previewerPath  = "/org/gnome/NautilusPreviewer"
	previewerIface = "org.gnome.NautilusPreviewer2"
	// The first call may start Sushi through D-Bus activation.
	callTimeout = 25 * time.Second
)

// ErrUnavailable is returned when the previewer cannot be reached or
// refused the file.
var ErrUnavailable = errors.New("the file previewer is not available")

// Show previews the file at uri (a file:// URI). done runs on the GTK main
// loop with nil or an error. It may be called from the main loop; the bus
// call itself runs on a goroutine so a slow activation never blocks the
// UI.
func Show(uri string, done func(error)) {
	go func() {
		err := show(uri)
		glib.IdleAdd(func() { done(err) })
	}()
}

func show(uri string) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	conn, err := gio.BusGetSync(ctx, gio.BusTypeSession)
	if err != nil {
		return fmt.Errorf("%w: session bus: %v", ErrUnavailable, err)
	}
	// Sushi 50 added an activation token to ShowFile; 46 and older take
	// three arguments, and D-Bus matches signatures exactly, so the older
	// form is the retry.
	var errs []error
	for _, params := range showFileParams(uri) {
		_, err := conn.CallSync(ctx, previewerName, previewerPath, previewerIface, "ShowFile",
			params, nil, gio.DBusCallFlagsNone, int(callTimeout/time.Millisecond))
		if err == nil {
			return nil
		}
		errs = append(errs, err)
	}
	return fmt.Errorf("%w: %v", ErrUnavailable, errors.Join(errs...))
}

// showFileParams are the ShowFile arguments to try in order: uri, the
// parent window handle, close-if-already-shown and (Sushi 50+) the
// activation token. The handle is empty: the preview then has no parent
// (exporting a Wayland handle would need gdkwayland), and a second click
// shows the file again rather than closing it.
func showFileParams(uri string) []*glib.Variant {
	return []*glib.Variant{
		glib.NewVariantTuple([]*glib.Variant{
			glib.NewVariantString(uri),
			glib.NewVariantString(""),
			glib.NewVariantBoolean(false),
			glib.NewVariantString(""),
		}),
		glib.NewVariantTuple([]*glib.Variant{
			glib.NewVariantString(uri),
			glib.NewVariantString(""),
			glib.NewVariantBoolean(false),
		}),
	}
}
