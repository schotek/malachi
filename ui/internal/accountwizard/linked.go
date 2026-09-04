// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"context"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settingspanel"
	"github.com/schotek/malachi/ui/internal/widget"
)

// GraphConfig is the account configuration for a linked Microsoft 365
// account: no servers, the sign-in belongs to GNOME Online Accounts.
func GraphConfig(id Identity, name, goaAccountID string) api.AccountConfig {
	email := strings.TrimSpace(id.Email)
	name = strings.TrimSpace(name)
	if name == "" {
		name = SuggestAccountName(email)
	}
	return api.AccountConfig{
		Name:        name,
		Email:       email,
		DisplayName: strings.TrimSpace(id.DisplayName),
		Kind:        api.AccountGraph,
		Graph:       &api.GraphConfig{Source: api.GraphSourceGOA, GOAAccountID: goaAccountID},
	}
}

// LinkedMatch finds the linked account with the address (case-insensitive).
func LinkedMatch(linked []api.LinkedAccount, email string) (api.LinkedAccount, bool) {
	want := strings.ToLower(strings.TrimSpace(email))
	for _, l := range linked {
		if strings.ToLower(l.Email) == want {
			return l, true
		}
	}
	return api.LinkedAccount{}, false
}

// loadLinked asks the daemon for accounts signed in elsewhere on the
// desktop and shows them above the identity rows; then runs, on the main
// loop, with the list (nil on failure — the list is a convenience).
func (w *Wizard) loadLinked(then func([]api.LinkedAccount)) {
	w.op++
	op := w.op
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), widget.RPCTimeout)
		defer cancel()
		var res api.AccountLinkedResult
		err := w.client.Call(ctx, api.MethodAccountLinked, api.AccountLinkedParams{}, &res)
		glib.IdleAdd(func() {
			if w.closed || op != w.op {
				return
			}
			if err != nil {
				w.log.Debug("account.linked", "err", err)
				res.Accounts = nil
			}
			w.linked = res.Accounts
			w.showLinked(res.Accounts)
			if then != nil {
				then(res.Accounts)
			}
		})
	}()
}

// showLinked rebuilds the linked-accounts rows.
func (w *Wizard) showLinked(accounts []api.LinkedAccount) {
	w.linkedRows.RemoveAll()
	shown := 0
	for _, l := range accounts {
		if w.editing != nil {
			break
		}
		l := l
		row := adw.NewActionRow()
		row.SetUseMarkup(false)
		title := l.Name
		if title == "" {
			title = l.Email
		}
		row.SetTitle(title)
		row.SetSubtitle(l.Email)
		row.AddPrefix(gtk.NewImageFromIconName(providerIcon()))
		use := gtk.NewButtonWithLabel(i18n.T("Use"))
		use.SetVAlign(gtk.AlignCenter)
		if l.Configured {
			use.SetLabel(i18n.T("Added"))
			use.SetSensitive(false)
		} else {
			use.AddCSSClass("suggested-action")
			use.ConnectClicked(func() { w.useLinked(l) })
		}
		row.AddSuffix(use)
		row.SetActivatableWidget(use)
		w.linkedRows.Append(row)
		shown++
	}
	w.linkedGroup.SetVisible(shown > 0)
}

// providerIcon is the Microsoft 365 icon GNOME Online Accounts installs,
// with a generic fallback where it is missing.
func providerIcon() string {
	if display := gdk.DisplayGetDefault(); display != nil {
		if gtk.IconThemeGetForDisplay(display).HasIcon("goa-account-ms365-symbolic") {
			return "goa-account-ms365-symbolic"
		}
	}
	return "mail-unread-symbolic"
}

// useLinked fills the identity from a linked account and goes straight to
// the connection test: there is nothing to type and nothing to discover.
func (w *Wizard) useLinked(l api.LinkedAccount) {
	w.email.SetText(l.Email)
	if strings.TrimSpace(w.displayName.Text()) == "" {
		w.displayName.SetText(l.Name)
	}
	w.startGraph(GraphConfig(w.readIdentity(), "", l.GOAAccountID))
}

// startGraph switches the wizard to the Graph path for cfg and tests it.
func (w *Wizard) startGraph(cfg api.AccountConfig) {
	w.graphCfg = &cfg
	w.password.SetText("")
	w.nav.ReplaceWithTags([]string{tagIdentity, tagTesting})
	w.runTest()
}

// showGOAHint opens the "sign in through GNOME Settings" page for a
// Microsoft 365 address the desktop is not signed in to yet.
func (w *Wizard) showGOAHint() {
	w.nav.PushByTag(tagGOA)
}

// onGOAOpen launches the Online Accounts panel of GNOME Settings.
func (w *Wizard) onGOAOpen() {
	w.goaOpen.SetSensitive(false)
	settingspanel.OpenOnlineAccounts(func(err error) {
		if w.closed {
			return
		}
		w.goaOpen.SetSensitive(true)
		if err != nil {
			w.log.Warn("open online accounts", "err", err)
			w.toast(i18n.T("Could not open Online Accounts; open GNOME Settings yourself"))
		}
	})
}

// onGOARecheck asks the daemon again and continues when the typed address
// has appeared among the linked accounts.
func (w *Wizard) onGOARecheck() {
	w.goaRecheck.SetSensitive(false)
	email, _ := ValidateEmail(w.email.Text())
	w.loadLinked(func(linked []api.LinkedAccount) {
		w.goaRecheck.SetSensitive(true)
		if l, ok := LinkedMatch(linked, email); ok && !l.Configured {
			w.useLinked(l)
			return
		}
		w.toast(i18n.T("This address is not signed in yet"))
	})
}
