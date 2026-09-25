// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"context"
	"errors"
	"fmt"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/signin"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The backend's own sign-in in the browser (account.oauthStart,
// account.oauthWait, account.oauthCancel; docs/api.md §4.1). For a Google
// or Microsoft 365 address that GNOME Online Accounts does not hold, the
// daemon listens on 127.0.0.1 and hands back the provider's sign-in URL;
// this page opens it in the browser and waits until the daemon has the
// tokens. Nothing secret passes through the UI: account.test and
// account.add receive the completed session's id.

// Names of the oauth_stack pages in account_wizard.blp.
const (
	oauthStackPrompt      = "prompt"
	oauthStackWaiting     = "waiting"
	oauthStackUnavailable = "unavailable"
)

// oauthState is the browser sign-in the oauth page is about.
type oauthState struct {
	// name is the provider's name for the texts ("Google", "Microsoft
	// 365"): a constant, never the daemon's providerName.
	name string
	// cfg is the new account to sign in (from account.discover); nil when
	// editing, where the account's id is signed in again.
	cfg *api.AccountConfig
	// passwordAlt is the provider's app-password account, nil when it has
	// none (Microsoft 365).
	passwordAlt *api.AccountConfig
	// session and authURL are account.oauthStart's answer, "" when no
	// sign-in is under way; complete says the session finished and
	// credentials() passes it on.
	session  string
	authURL  string
	complete bool
}

// providerLabel is the name the texts use for provider: its brand name,
// or for a provider the UI does not know the address's domain.
func providerLabel(provider, email string) string {
	if name := signin.ProviderName(provider); name != "" {
		return name
	}
	return SuggestAccountName(email)
}

// showOAuthPrompt opens the browser page for provider: cfg is the new
// account to sign in, nil when editing (the account's id is signed in
// again); passwordAlt is the app-password account the page may offer
// instead. A sign-in of before is cancelled.
func (w *Wizard) showOAuthPrompt(provider string, cfg, passwordAlt *api.AccountConfig) {
	w.cancelSession()
	email := w.email.Text()
	if cfg != nil {
		email = cfg.Email
	}
	w.oauth = oauthState{name: providerLabel(provider, email), cfg: cfg, passwordAlt: passwordAlt}
	// TRANSLATORS: %s is a provider such as "Google" or "Microsoft 365".
	w.oauthPrompt.SetDescription(glib.MarkupEscapeText(fmt.Sprintf(i18n.T("Your browser will open so you can sign in to %s. Malachi Mail never sees your password; it only receives permission to read and send your mail."), w.oauth.name)))
	// TRANSLATORS: %s is a provider such as "Google" or "Microsoft 365".
	w.oauthSignIn.SetLabel(fmt.Sprintf(i18n.T("_Sign In with %s"), w.oauth.name))
	w.showOAuthStack(oauthStackPrompt)
	if w.signInOnly {
		w.nav.ReplaceWithTags([]string{tagOAuth})
	} else {
		w.nav.ReplaceWithTags([]string{tagIdentity, tagOAuth})
	}
}

// showOAuthStack switches the oauth page. Only while waiting does the page
// hold: Back would leave the sign-in running behind the dialog.
func (w *Wizard) showOAuthStack(name string) {
	w.oauthStack.SetVisibleChildName(name)
	w.oauthPage.SetCanPop(name != oauthStackWaiting)
	w.oauthSignIn.SetSensitive(true)
}

// oauthShown reports whether the oauth page is the one on screen.
func (w *Wizard) oauthShown() bool {
	page := w.nav.VisiblePage()
	return page != nil && page.Tag() == tagOAuth
}

// onOAuthSignIn starts the browser sign-in: account.oauthStart for the new
// account or, when editing, for the account's id; then the browser opens
// and the page waits for the daemon.
func (w *Wizard) onOAuthSignIn() {
	w.cancelSession()
	params := api.AccountOAuthStartParams{BrowserPage: BrowserPage()}
	switch {
	case w.editing != nil:
		params.AccountID = w.editing.ID
	case w.oauth.cfg != nil:
		cfg := withIdentity(*w.oauth.cfg, w.readIdentity())
		params.Config = &cfg
	default:
		return
	}
	w.oauthSignIn.SetSensitive(false)
	w.op++
	op := w.op
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), oauthStartTimeout)
		defer cancel()
		var res api.AccountOAuthStartResult
		err := w.client.Call(ctx, api.MethodAccountOAuthStart, params, &res)
		glib.IdleAdd(func() {
			if w.closed || op != w.op || !w.oauthShown() {
				// Nobody waits for this session any more: the dialog
				// closed, moved on, or Back left the page meanwhile.
				if err == nil && res.SessionID != "" {
					w.cancelSessionID(res.SessionID)
				}
				return
			}
			w.oauthSignIn.SetSensitive(true)
			switch {
			case signin.IsClientMissing(err):
				w.log.Info("account.oauthStart: no OAuth client configured")
				w.showOAuthUnavailable()
				return
			case err != nil:
				w.log.Warn("account.oauthStart", "err", err)
				// TRANSLATORS: progressive form for the RPC error text
				w.toast(widget.RPCErrorText(i18n.T("Starting the sign-in"), err))
				return
			}
			if !signin.BrowserURL(res.AuthURL) {
				// Only an https address from the daemon is opened: the
				// session is let go and the prompt stays, as nothing
				// could come back from the browser.
				w.log.Warn("sign-in address refused: not https")
				if res.SessionID != "" {
					w.cancelSessionID(res.SessionID)
				}
				w.toast(widget.LaunchErrorText(errNotHTTPS))
				return
			}
			w.oauth.session, w.oauth.authURL = res.SessionID, res.AuthURL
			w.showOAuthStack(oauthStackWaiting)
			w.launch(res.AuthURL)
			w.waitOAuth()
		})
	}()
}

// waitOAuth asks the daemon whether the browser has come back. Each
// account.oauthWait blocks up to a minute and then answers pending; the
// next call is made from the main loop, and only while the dialog is open
// and nothing else has started since (op), so Cancel and closing end the
// loop.
func (w *Wizard) waitOAuth() {
	session := w.oauth.session
	w.op++
	op := w.op
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), oauthWaitCallTimeout)
		defer cancel()
		var res api.AccountOAuthWaitResult
		err := w.client.Call(ctx, api.MethodAccountOAuthWait, api.AccountOAuthWaitParams{SessionID: session}, &res)
		glib.IdleAdd(func() {
			if w.closed || op != w.op || session != w.oauth.session {
				return
			}
			switch {
			case err != nil:
				w.oauthFailed(err)
			case res.Status == api.OAuthSessionPending:
				w.waitOAuth()
			case res.Status == api.OAuthSessionComplete && res.Config != nil:
				w.oauthComplete(*res.Config)
			default:
				w.oauthFailed(fmt.Errorf("account.oauthWait: unexpected status %q", res.Status))
			}
		})
	}()
}

// oauthComplete continues with the account the daemon signed in: the
// identity page adds the names, the password (none is needed) is cleared
// and the connection test runs with the session. Back from the test leads
// to the browser page, ready to sign in again.
func (w *Wizard) oauthComplete(cfg api.AccountConfig) {
	w.log.Info("browser sign-in complete")
	cfg = withIdentity(cfg, w.readIdentity())
	w.linkedCfg = &cfg
	w.appPassword = nil
	w.oauth.complete = true
	w.password.SetText("")
	w.showOAuthStack(oauthStackPrompt)
	if w.signInOnly {
		w.nav.ReplaceWithTags([]string{tagOAuth, tagTesting})
	} else {
		w.nav.ReplaceWithTags([]string{tagIdentity, tagOAuth, tagTesting})
	}
	w.runTest()
}

// oauthFailed returns to the prompt and says why. A session the daemon
// may still hold (the wait itself failed) is cancelled.
func (w *Wizard) oauthFailed(err error) {
	w.log.Info("browser sign-in failed", "err", err)
	w.cancelSession()
	w.showOAuthStack(oauthStackPrompt)
	w.toast(oauthErrorText(w.oauth.name, err))
}

// onOAuthReopen opens the sign-in page again, e.g. after the browser tab
// was closed.
func (w *Wizard) onOAuthReopen() {
	if w.oauth.authURL != "" {
		w.launch(w.oauth.authURL)
	}
}

// onOAuthCancel gives up waiting: the running account.oauthWait's answer
// is dropped (op) and the daemon closes its listener.
func (w *Wizard) onOAuthCancel() {
	w.op++
	w.cancelSession()
	w.showOAuthStack(oauthStackPrompt)
}

// cancelSession ends the sign-in the daemon holds for this dialog, if
// any, and forgets it; the daemon discards a completed one with its
// tokens.
func (w *Wizard) cancelSession() {
	id := w.oauth.session
	w.oauth.session, w.oauth.authURL, w.oauth.complete = "", "", false
	if id != "" {
		w.cancelSessionID(id)
	}
}

// cancelSessionID is account.oauthCancel, fire and forget: an unknown or
// finished session is ignored by the daemon.
func (w *Wizard) cancelSessionID(id string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), widget.RPCTimeout)
		defer cancel()
		err := w.client.Call(ctx, api.MethodAccountOAuthCancel, api.AccountOAuthCancelParams{SessionID: id}, &api.AccountOAuthCancelResult{})
		if err != nil {
			w.log.Debug("account.oauthCancel", "err", err)
		}
	}()
}

// errNotHTTPS is the technical reason in the toast for a sign-in address
// that is not opened.
var errNotHTTPS = errors.New("not an https address")

// launch opens the provider's sign-in page in the browser. Only an https
// address from the daemon is opened (onOAuthSignIn already refused any
// other; this guards "Open the Browser Again" too).
func (w *Wizard) launch(uri string) {
	if !signin.BrowserURL(uri) {
		w.log.Warn("sign-in address refused: not https")
		w.toast(widget.LaunchErrorText(errNotHTTPS))
		return
	}
	widget.LaunchURI(nil, uri, func(err error) {
		if err != nil && !w.closed {
			w.log.Warn("open sign-in page", "err", err)
			w.toast(widget.LaunchErrorText(err))
		}
	})
}

// showOAuthUnavailable is the page for oauthClientMissing: without an
// OAuth client the backend cannot sign in; the app-password account is
// offered when the provider has one.
func (w *Wizard) showOAuthUnavailable() {
	// TRANSLATORS: %s is a provider such as "Google" or "Microsoft 365".
	w.oauthUnavailable.SetDescription(glib.MarkupEscapeText(fmt.Sprintf(i18n.T("No OAuth client is configured for %s on this computer. Add a client ID to the mail backend's configuration and try again."), w.oauth.name)))
	w.oauthPassword.SetVisible(w.oauth.passwordAlt != nil)
	w.showOAuthStack(oauthStackUnavailable)
}

// onOAuthPassword gives up the browser sign-in for the provider's
// app-password account (Google): IMAP and SMTP with the password of the
// identity page.
func (w *Wizard) onOAuthPassword() {
	alt := w.oauth.passwordAlt
	if alt == nil {
		return
	}
	w.cancelSession()
	cfg := *alt
	w.appPassword = &cfg
	w.oauth = oauthState{}
	w.linkedCfg = nil
	w.useAppPassword()
}

// useAppPassword continues with the chosen app-password account: to the
// connection test when a password is typed, else back to the identity
// page to type one (Next then continues here).
func (w *Wizard) useAppPassword() {
	id := w.readIdentity()
	id.Email, _ = ValidateEmail(id.Email)
	w.applyConfig(MergeIdentity(*w.appPassword, id))
	if id.Password == "" {
		w.nav.ReplaceWithTags([]string{tagIdentity})
		w.requirePassword(i18n.T("Enter the app password for this account"))
		return
	}
	w.nav.ReplaceWithTags([]string{tagIdentity, tagServers, tagTesting})
	w.runTest()
}

// onSignInAgain is the results page's "Sign In Again": the browser page
// for the same account, a sign-in of before dropped.
func (w *Wizard) onSignInAgain() {
	provider := ""
	if w.linkedCfg != nil {
		provider = signin.Provider(*w.linkedCfg)
	}
	w.showOAuthPrompt(provider, w.oauth.cfg, w.oauth.passwordAlt)
}

// BrowserPage holds the texts of the page the browser shows once the
// provider has sent it back to the daemon, in the user's language (plain
// text; the daemon escapes them). Every account.oauthStart passes them.
func BrowserPage() *api.OAuthBrowserPage {
	return &api.OAuthBrowserPage{
		// TRANSLATORS: title of the page the browser shows
		SuccessTitle: i18n.T("Signed in"),
		// TRANSLATORS: page the browser shows
		SuccessText: i18n.T("You can close this tab and return to Malachi Mail."),
		// TRANSLATORS: title of the page the browser shows
		FailureTitle: i18n.T("Sign-in failed"),
		// TRANSLATORS: page the browser shows
		FailureText: i18n.T("Return to Malachi Mail and try again."),
	}
}

// oauthErrorText is the sentence for a browser sign-in that ended without
// an account; provider is the provider's name.
func oauthErrorText(provider string, err error) string {
	f, signedInAs := signin.ClassifyFailure(err)
	switch f {
	case signin.FailureCancelled:
		return i18n.T("The sign-in was cancelled")
	case signin.FailureRefused:
		// TRANSLATORS: %s is a provider such as "Google" or "Microsoft 365".
		return fmt.Sprintf(i18n.T("The sign-in with %s was refused"), provider)
	case signin.FailureTimeout:
		return i18n.T("The sign-in took too long; try again")
	case signin.FailureWrongAccount:
		// TRANSLATORS: %s is an e-mail address.
		return fmt.Sprintf(i18n.T("The browser signed in to %s, not to this address"), signedInAs)
	}
	// TRANSLATORS: progressive form for the RPC error text
	return widget.RPCErrorText(i18n.T("Signing in"), err)
}
