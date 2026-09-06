// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"context"
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settingspanel"
	"github.com/schotek/malachi/ui/internal/widget"
)

// linkedAccountID is the GNOME Online Accounts id an account signs in
// with, "" when it has none yet (the sign-in hint of account.discover).
func linkedAccountID(cfg api.AccountConfig) string {
	switch {
	case cfg.Graph != nil:
		return cfg.Graph.GOAAccountID
	case cfg.OAuth2 != nil && cfg.OAuth2.Source == api.OAuth2SourceGOA:
		return cfg.OAuth2.GOAAccountID
	}
	return ""
}

// withIdentity is the daemon-built account of a linked sign-in with what
// the identity page adds: the display name, and an account name derived
// from the address when the daemon left it at the address itself.
func withIdentity(cfg api.AccountConfig, id Identity) api.AccountConfig {
	cfg.DisplayName = strings.TrimSpace(id.DisplayName)
	if strings.TrimSpace(cfg.Name) == "" || strings.EqualFold(strings.TrimSpace(cfg.Name), cfg.Email) {
		cfg.Name = SuggestAccountName(cfg.Email)
	}
	return cfg
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
		row.AddPrefix(gtk.NewImageFromIconName(widget.ProviderIcon(l.Provider)))
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

// useLinked fills the identity from a linked account and goes straight to
// the connection test with the account the daemon built for it: there is
// nothing to type and nothing to discover.
func (w *Wizard) useLinked(l api.LinkedAccount) {
	if l.Config == nil {
		// A daemon older than the config field; the sign-in is there,
		// but not the account to add it as.
		w.toast(i18n.T("The mail service does not describe this account; update it and try again"))
		return
	}
	w.email.SetText(l.Email)
	if strings.TrimSpace(w.displayName.Text()) == "" {
		w.displayName.SetText(l.Name)
	}
	w.startLinked(*l.Config)
}

// startLinked switches the wizard to an account whose sign-in belongs to
// GNOME Online Accounts (no password, no servers to edit) and tests it.
func (w *Wizard) startLinked(cfg api.AccountConfig) {
	cfg = withIdentity(cfg, w.readIdentity())
	w.linkedCfg = &cfg
	w.password.SetText("")
	w.nav.ReplaceWithTags([]string{tagIdentity, tagTesting})
	w.runTest()
}

// showGOAHint opens the "sign in through GNOME Settings" page for an
// address of the named provider that the desktop is not signed in to yet.
func (w *Wizard) showGOAHint(providerName string) {
	if providerName == "" {
		providerName = widget.ProviderName(widget.ProviderMicrosoft365)
	}
	// TRANSLATORS: %s is a provider such as "Microsoft 365" or "Google".
	w.goaHint.SetDescription(fmt.Sprintf(i18n.T("This address belongs to a %s account. Add it under Settings → Online Accounts, then come back here."), providerName))
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
