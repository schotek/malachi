// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"log/slog"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/accountwizard"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/signin"
	"github.com/schotek/malachi/ui/internal/widget"
)

// contactsSignIn is Allow… of a contacts consent banner or row
// (contactsConsentRequired, A15): account.oauthStart for the account with
// Contacts, then its sign-in page in the browser, as the calendar's
// consent does (Window.startBrowserSignIn). The daemon completes the
// sign-in by itself and announces it with notify.accountsChanged, which
// reloads the books. parent is the window or dialog the page is opened
// over; toast shows a failure. Without a client (the samples) only the
// failure is shown.
func contactsSignIn(parent gtk.Widgetter, c *client.Client, log *slog.Logger, id api.AccountID, toast func(string)) {
	what := i18n.T("Starting the sign-in")
	if c == nil {
		toast(widget.RPCErrorText(what, client.ErrDisconnected))
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), signInStartTimeout)
		defer cancel()
		var res api.AccountOAuthStartResult
		err := c.Call(ctx, api.MethodAccountOAuthStart,
			api.AccountOAuthStartParams{AccountID: id, BrowserPage: accountwizard.BrowserPage(), Contacts: true}, &res)
		glib.IdleAdd(func() {
			switch {
			case err != nil || res.AuthURL == "":
				if log != nil {
					log.Warn("account.oauthStart for contacts", "account", id, "err", err)
				}
				toast(widget.RPCErrorText(what, err))
				return
			case !signin.BrowserURL(res.AuthURL):
				if log != nil {
					log.Warn("sign-in address refused: not https", "account", id)
				}
				toast(widget.LaunchErrorText(errors.New("not an https address")))
				return
			}
			widget.LaunchURI(rootWindow(parent), res.AuthURL, func(err error) {
				if err != nil {
					if log != nil {
						log.Warn("open sign-in page", "err", err)
					}
					toast(widget.LaunchErrorText(err))
				}
			})
		})
	}()
}

// rootWindow is the window that holds w (nil when it is not in one).
func rootWindow(w gtk.Widgetter) *gtk.Window {
	if w == nil {
		return nil
	}
	root := gtk.BaseWidget(w).Root()
	if root == nil {
		return nil
	}
	switch win := root.Cast().(type) {
	case *gtk.Window:
		return win
	case *gtk.ApplicationWindow:
		return &win.Window
	case *adw.ApplicationWindow:
		return &win.ApplicationWindow.Window
	case *adw.Window:
		return &win.Window
	}
	// Any other window: the page opens without a parent.
	return nil
}
