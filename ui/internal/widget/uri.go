// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"context"
	"fmt"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/i18n"
)

// LaunchURI opens uri with the desktop's handler through the OpenURI
// portal (gtk.URILauncher), never xdg-open directly: a link from a message,
// or the sign-in page of the backend's own OAuth flow in the browser.
// parent may be nil. done runs on the main loop with nil or the error; it
// may be nil.
func LaunchURI(parent *gtk.Window, uri string, done func(error)) {
	l := gtk.NewURILauncher(uri)
	l.Launch(context.Background(), parent, func(res gio.AsyncResulter) {
		err := l.LaunchFinish(res)
		if done != nil {
			done(err)
		}
	})
}

// LaunchErrorText is the sentence for a LaunchURI failure.
func LaunchErrorText(err error) string {
	// TRANSLATORS: %s is a technical error message.
	return fmt.Sprintf(i18n.T("The link could not be opened: %s"), err)
}
