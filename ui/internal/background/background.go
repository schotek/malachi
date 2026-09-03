// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package background talks to the XDG Background portal
// (org.freedesktop.portal.Background) to request permission to run without
// a window and to be started at login. gotk4 does not wrap the portal, so
// the calls are made directly over the session bus with gio's D-Bus API.
//
// The portal works both inside Flatpak and on a host with
// xdg-desktop-portal; per docs/architecture.md, autostart is never done by
// writing ~/.config/autostart ourselves.
package background

import (
	"context"
	"errors"
	"fmt"
	"github.com/schotek/malachi/ui/internal/i18n"
	"strings"
	"sync/atomic"
	"time"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

const (
	portalName      = "org.freedesktop.portal.Desktop"
	portalPath      = "/org/freedesktop/portal/desktop"
	ifaceBackground = "org.freedesktop.portal.Background"
	ifaceRequest    = "org.freedesktop.portal.Request"

	// responseTimeout bounds how long we wait for the portal's Response
	// signal (the user may be looking at a permission dialog).
	responseTimeout = 120 * time.Second
)

// ErrCancelled is returned when the user dismissed the portal dialog.
var ErrCancelled = errors.New("background portal request cancelled")

// ErrFailed is returned when the portal reported an error response.
var ErrFailed = errors.New("background portal request failed")

// Result is what the portal granted.
type Result struct {
	Background bool // may run without a window
	Autostart  bool // will be started at login
}

// Options for Request.
type Options struct {
	// Autostart asks to be launched at login with Command.
	Autostart bool
	// Command is the autostart command line, e.g. {"malachi", "--gapplication-service"}.
	Command []string
	// Reason is shown to the user by the portal dialog.
	Reason string
}

var tokenCounter atomic.Uint64

// Request asks the portal and calls done on the GTK main loop with the
// outcome. It must itself be called from the main loop: the Response signal
// is delivered to the thread-default main context of the caller.
func Request(opts Options, done func(Result, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := gio.BusGetSync(ctx, gio.BusTypeSession)
	if err != nil {
		done(Result{}, fmt.Errorf("session bus: %w", err))
		return
	}

	token := fmt.Sprintf("malachi_%d_%d", time.Now().UnixNano(), tokenCounter.Add(1))
	path := RequestPath(conn.UniqueName(), token)

	// Subscribe before calling so a fast Response cannot be missed.
	var sub uint
	var timeout glib.SourceHandle
	finish := func(res Result, err error) {
		if sub != 0 {
			conn.SignalUnsubscribe(sub)
			sub = 0
		}
		if timeout != 0 {
			glib.SourceRemove(timeout)
			timeout = 0
		}
		done(res, err)
	}
	sub = conn.SignalSubscribe(portalName, ifaceRequest, "Response", path, "",
		gio.DBusSignalFlagsNone,
		func(_ *gio.DBusConnection, _, _, _, _ string, params *glib.Variant) {
			finish(parseResponse(params))
		})
	timeout = glib.TimeoutSecondsAdd(uint(responseTimeout/time.Second), func() bool {
		timeout = 0
		finish(Result{}, fmt.Errorf("%w: no response from the portal", ErrFailed))
		return false
	})

	dict := glib.NewVariantBuilder(glib.NewVariantType("a{sv}"))
	add := func(key string, v *glib.Variant) {
		dict.AddValue(glib.NewVariantDictEntry(glib.NewVariantString(key), glib.NewVariantVariant(v)))
	}
	add("handle_token", glib.NewVariantString(token))
	add("reason", glib.NewVariantString(opts.Reason))
	add("autostart", glib.NewVariantBoolean(opts.Autostart))
	if len(opts.Command) > 0 {
		add("commandline", glib.NewVariantStrv(opts.Command))
	}
	add("dbus-activatable", glib.NewVariantBoolean(false))

	// parent_window is left empty: allowed by the portal spec, and exporting
	// a Wayland handle would need the gdkwayland package.
	params := glib.NewVariantTuple([]*glib.Variant{glib.NewVariantString(""), dict.End()})
	if _, err := conn.CallSync(ctx, portalName, portalPath, ifaceBackground, "RequestBackground",
		params, glib.NewVariantType("(o)"), gio.DBusCallFlagsNone, 5000); err != nil {
		finish(Result{}, fmt.Errorf("RequestBackground: %w", err))
	}
}

// RequestAutostart is Request configured for Malachi Mail's own autostart:
// enable starts `malachi --gapplication-service` at login, disable removes it.
func RequestAutostart(enable bool, done func(Result, error)) {
	Request(Options{
		Autostart: enable,
		Command:   []string{"malachi", "--gapplication-service"},
		Reason:    i18n.T("Check for new mail and show notifications in the background"),
	}, done)
}

// RequestPath is where the portal emits Response for our request: the
// caller's unique bus name with ':' dropped and '.' replaced by '_', plus
// the handle token.
func RequestPath(uniqueName, token string) string {
	sender := strings.ReplaceAll(strings.TrimPrefix(uniqueName, ":"), ".", "_")
	return "/org/freedesktop/portal/desktop/request/" + sender + "/" + token
}

// parseResponse decodes the (ua{sv}) Response payload.
func parseResponse(params *glib.Variant) (Result, error) {
	if params == nil || params.NChildren() < 2 {
		return Result{}, fmt.Errorf("%w: malformed response", ErrFailed)
	}
	switch params.ChildValue(0).Uint32() {
	case 0:
	case 1:
		return Result{}, ErrCancelled
	default:
		return Result{}, ErrFailed
	}
	var res Result
	results := params.ChildValue(1)
	if v := results.LookupValue("background", glib.NewVariantType("b")); v != nil {
		res.Background = v.Boolean()
	}
	if v := results.LookupValue("autostart", glib.NewVariantType("b")); v != nil {
		res.Autostart = v.Boolean()
	}
	return res, nil
}
