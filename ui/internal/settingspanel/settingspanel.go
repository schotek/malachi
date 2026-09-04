// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package settingspanel opens panels of GNOME Settings over D-Bus
// (org.freedesktop.Application on org.gnome.Settings), the way the desktop
// itself does it. Inside Flatpak this needs --talk-name=org.gnome.Settings;
// without GNOME Settings the call fails and the caller says so.
package settingspanel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

const (
	settingsName = "org.gnome.Settings"
	settingsPath = "/org/gnome/Settings"
	ifaceApp     = "org.freedesktop.Application"
	callTimeout  = 10 * time.Second
)

// ErrUnavailable is returned when GNOME Settings cannot be reached.
var ErrUnavailable = errors.New("GNOME Settings is not available")

// OpenOnlineAccounts shows the Online Accounts panel. done runs on the GTK
// main loop with nil or an error. It may be called from the main loop; the
// bus call itself runs on a goroutine so a slow activation never blocks
// the UI.
func OpenOnlineAccounts(done func(error)) {
	Open("online-accounts", done)
}

// Open launches the named panel (`launch-panel` action of GNOME Settings).
func Open(panel string, done func(error)) {
	go func() {
		err := open(panel)
		glib.IdleAdd(func() { done(err) })
	}()
}

func open(panel string) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	conn, err := gio.BusGetSync(ctx, gio.BusTypeSession)
	if err != nil {
		return fmt.Errorf("%w: session bus: %v", ErrUnavailable, err)
	}
	// ActivateAction("launch-panel", [<(panel, [])>], {})
	variantType := glib.NewVariantType("v")
	panelArg := glib.NewVariantTuple([]*glib.Variant{
		glib.NewVariantString(panel),
		glib.NewVariantArray(variantType, nil),
	})
	params := glib.NewVariantTuple([]*glib.Variant{
		glib.NewVariantString("launch-panel"),
		glib.NewVariantArray(variantType, []*glib.Variant{glib.NewVariantVariant(panelArg)}),
		glib.NewVariantBuilder(glib.NewVariantType("a{sv}")).End(),
	})
	if _, err := conn.CallSync(ctx, settingsName, settingsPath, ifaceApp, "ActivateAction",
		params, nil, gio.DBusCallFlagsNone, int(callTimeout/time.Millisecond)); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}
