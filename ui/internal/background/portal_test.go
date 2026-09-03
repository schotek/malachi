// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package background

import (
	"errors"
	"os"
	"testing"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

// TestPortalRoundTrip talks to the real Background portal on the session
// bus. It only runs when MALACHI_PORTAL_TEST is set (it may show a portal
// dialog) and asks to *disable* autostart, which is idempotent.
func TestPortalRoundTrip(t *testing.T) {
	if os.Getenv("MALACHI_PORTAL_TEST") == "" {
		t.Skip("set MALACHI_PORTAL_TEST=1 to talk to the session bus")
	}
	loop := glib.NewMainLoop(nil, false)
	var (
		got  Result
		gerr error
	)
	glib.IdleAdd(func() {
		RequestAutostart(false, func(res Result, err error) {
			got, gerr = res, err
			loop.Quit()
		})
	})
	loop.Run()
	if errors.Is(gerr, ErrFailed) {
		// Inside a Toolbx container (or when not launched from a .desktop
		// file) xdg-desktop-portal logs "Autostart not supported (no AppId
		// detected)" and answers with the failure code.
		t.Skipf("portal refused the request, expected in a container: %v", gerr)
	}
	if gerr != nil {
		t.Fatalf("portal request failed: %v", gerr)
	}
	t.Logf("portal granted: %+v", got)
	if got.Autostart {
		t.Error("asked to disable autostart, portal reports it enabled")
	}
}
