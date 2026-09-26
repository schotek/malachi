// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/accountwizard"
	"github.com/schotek/malachi/ui/internal/certtrust"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settingspanel"
	"github.com/schotek/malachi/ui/internal/signin"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Sync status line, refresh, the sign-in banner and the certificate banner.
//
// The daemon owns the sync state; this file only mirrors the last
// sync.status / notify.syncState per account into the sidebar's status line
// (one line for all accounts; its popover, one row per account, is in
// status.go), the auth_banner and the cert_banner. Folder and account names
// shown here come from the server and are set as plain text.

// syncFallbackSeconds is how long the spinner started by triggerSync stays
// on when no notify.syncState follows (daemon without a syncer, dropped
// notification, …).
const syncFallbackSeconds = 30

// signInStartTimeout bounds account.oauthStart: the daemon only opens a
// listener and builds the provider's URL.
const signInStartTimeout = 10 * time.Second

// statusRefreshSeconds is how often the status line is redrawn without a
// state change, so the time of the last check it names becomes a date
// once the day is over.
const statusRefreshSeconds = 60

// loadSyncStatus runs sync.status after connecting and applies every
// account's state.
func (w *Window) loadSyncStatus() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.SyncStatusResult
		err := w.client.Call(ctx, api.MethodSyncStatus, api.SyncStatusParams{}, &res)
		glib.IdleAdd(func() {
			if err != nil {
				// notImplemented is the expected answer until the daemon
				// grows a syncer; not worth more than a debug line. The
				// line says "Not syncing" until a state arrives after all.
				w.log.Debug("sync.status", "err", err)
				w.conn.SyncFailed = true
				w.refreshSyncLabel()
				return
			}
			for _, s := range res.Accounts {
				w.applySyncState(s)
			}
		})
	}()
}

// applySyncState records one account's state, updates the status line and
// calls onSyncFinished when the account left the syncing state and
// onOutboxChanged when the number of pending or failed outgoing messages
// moved. The first state of an account is no move: loadAccounts fetches
// the folders anyway, and a failed message would otherwise reload them on
// every connect, racing that first load.
func (w *Window) applySyncState(s api.SyncState) {
	prev, known := w.syncStates[s.AccountID]
	w.syncStates[s.AccountID] = s
	w.conn.SyncFailed = false
	w.refreshSyncLabel()
	w.refreshCertBanner()
	if s.AccountID == w.authBannerAccount && s.Status != api.SyncAuthRequired {
		w.hideAuthBanner()
	}
	w.onSyncFinished(prev, s)
	if known && (prev.PendingOutbox != s.PendingOutbox || prev.FailedOutbox != s.FailedOutbox) {
		w.onOutboxChanged(s.AccountID)
	}
}

// refreshSyncLabel recomputes the sidebar's status line from the cached
// states and the connection (statusLineFor) and, while its popover is
// open, the popover's rows. Enabled accounts without a cached state fall
// back to the state account.list reported, so the line is right before
// sync.status answered. Without a connection the button cannot be
// clicked, and an open popover closes.
func (w *Window) refreshSyncLabel() {
	text, spinning := syncStatusText(w.syncStates, w.model.accounts, w.syncFolderName, time.Now())
	line := statusLineFor(w.conn, text, spinning)
	w.syncLabel.SetUseMarkup(false)
	w.syncLabel.SetLabel(line.Text)
	w.syncSpinner.SetVisible(line.Spinning)
	if line.Icon != "" {
		w.connIcon.SetFromIconName(line.Icon)
	}
	w.connIcon.SetVisible(line.Icon != "")
	if !line.Active {
		w.statusButton.Popdown()
	}
	w.statusButton.SetSensitive(line.Active)
	w.statusDaemon.SetUseMarkup(false)
	w.statusDaemon.SetLabel(line.Daemon)
	w.statusDaemon.SetVisible(line.Daemon != "")
	if w.statusPopover.Visible() {
		w.refreshStatusPopover()
	}
}

// syncFolderName is the display name of a folder an account's state names,
// "" while the folder is unknown (syncStatusText, accountStatuses).
func (w *Window) syncFolderName(acc api.AccountID, id api.FolderID) string {
	f, ok := w.model.folder(folderKey{Account: acc, Folder: id})
	if !ok {
		return ""
	}
	return folderTitle(f)
}

// syncStatusText is the sidebar status line for the given states. Only
// enabled accounts count; an account missing from states uses the state
// embedded in its account.list entry. The most pressing state wins:
// syncing (with the folder or account name and the progress when known),
// then sign-in required, a changed server certificate, a refused one
// (certtrust.FromSyncState), sending (the pending outbox messages of every
// account added up; sending is not a sync status, so it shows while the
// status is idle), messages that were not sent (failed, added up the same
// way), error, offline, and finally "Up to date" with the time of the
// newest last check. Sign-in required, error and offline name the account
// when exactly one is in that state and more than one is enabled; with a
// single account the name would say nothing, with several it could not be
// one (the popover lists them). The certificate states leave the name to
// cert_banner. When every account is paused the line says so; with no
// account at all it is empty. folderName
// returns the display name of a folder or "" when unknown; now is the
// moment the time of the last check is shown against.
func syncStatusText(states map[api.AccountID]api.SyncState, accounts []api.Account, folderName func(api.AccountID, api.FolderID) string, now time.Time) (text string, spinning bool) {
	var (
		syncing      *api.SyncState
		syncingName  string
		authRequired []api.Account
		certProblem  bool
		certChanged  bool
		syncError    []api.Account
		offline      []api.Account
		enabled      int
		pending      int
		failed       int
		lastSync     time.Time
	)
	for _, a := range accounts {
		if !a.Enabled {
			continue
		}
		enabled++
		s, ok := states[a.ID]
		if !ok {
			s = a.State
		}
		pending += s.PendingOutbox
		failed += s.FailedOutbox
		if s.LastSync != nil && s.LastSync.After(lastSync) {
			lastSync = *s.LastSync
		}
		switch s.Status {
		case api.SyncSyncing:
			if syncing == nil {
				cur := s
				syncing = &cur
				if s.FolderID != "" && folderName != nil {
					syncingName = folderName(a.ID, s.FolderID)
				}
				if syncingName == "" {
					syncingName = accountRowTitle(a)
				}
			}
		case api.SyncAuthRequired:
			authRequired = append(authRequired, a)
		case api.SyncError:
			syncError = append(syncError, a)
		case api.SyncOffline:
			offline = append(offline, a)
		}
		if p, ok := certtrust.FromSyncState(s); ok {
			if p.Category() == certtrust.Changed {
				certChanged = true
			} else {
				certProblem = true
			}
		}
	}
	// named is the one account in a state, when naming it helps.
	named := func(in []api.Account) (string, bool) {
		if len(in) != 1 || enabled < 2 {
			return "", false
		}
		return accountRowTitle(in[0]), true
	}
	switch {
	case enabled == 0 && len(accounts) > 0:
		return i18n.T("Paused"), false
	case enabled == 0:
		return "", false
	case syncing != nil:
		if syncing.Progress >= 0 {
			// TRANSLATORS: %s is a folder or account name, %d the progress in percent.
			return fmt.Sprintf(i18n.T("Syncing %s… %d %%"), syncingName, syncing.Progress), true
		}
		// TRANSLATORS: %s is a folder or account name.
		return fmt.Sprintf(i18n.T("Syncing %s…"), syncingName), true
	case len(authRequired) > 0:
		if name, ok := named(authRequired); ok {
			// TRANSLATORS: status line; %s is an account name.
			return fmt.Sprintf(i18n.T("Sign-in required: %s"), name), false
		}
		return i18n.T("Sign-in required"), false
	case certChanged:
		return certStatusText(certtrust.Changed), false
	case certProblem:
		return certStatusText(certtrust.Certificate), false
	case pending > 0:
		return sendingText(pending), true
	case failed > 0:
		return notSentText(failed), false
	case len(syncError) > 0:
		if name, ok := named(syncError); ok {
			// TRANSLATORS: status line; %s is an account name.
			return fmt.Sprintf(i18n.T("Sync error: %s"), name), false
		}
		return i18n.T("Sync error"), false
	case len(offline) > 0:
		if name, ok := named(offline); ok {
			// TRANSLATORS: status line of an account that cannot reach its
			// server and keeps trying; %s is an account name.
			return fmt.Sprintf(i18n.T("Offline: %s"), name), false
		}
		return i18n.T("Offline, retrying"), false
	}
	if !lastSync.IsZero() {
		// TRANSLATORS: status line; %s is the time of the last check for
		// new mail, e.g. "15:04", or its date when that was before today.
		return fmt.Sprintf(i18n.T("Up to date · %s"), widget.FormatDate(lastSync, now)), false
	}
	return i18n.T("Up to date"), false
}

// sendingText is the status of n messages waiting in the outbox (the
// status line, an account's row in its popover).
func sendingText(n int) string {
	// TRANSLATORS: %d is the number of messages waiting in the outbox.
	return fmt.Sprintf(i18n.N("Sending %d message…", "Sending %d messages…", n), n)
}

// notSentText is the status of n messages in the outbox whose delivery
// failed (the status line, the popover's link to the outbox).
func notSentText(n int) string {
	// TRANSLATORS: status line; %d is the number of messages in the outbox
	// whose sending failed.
	return fmt.Sprintf(i18n.N("%d message not sent", "%d messages not sent", n), n)
}

// certStatusText is the short status of an account whose server
// certificate was refused (Settings → Accounts, the sidebar line).
func certStatusText(c certtrust.Category) string {
	if c == certtrust.Changed {
		// TRANSLATORS: account status (sidebar, Settings → Accounts)
		return i18n.T("Certificate changed")
	}
	// TRANSLATORS: account status (sidebar, Settings → Accounts)
	return i18n.T("Certificate problem")
}

// refreshCertBanner reveals cert_banner for the first enabled account whose
// server certificate was refused or has changed and hides it when there is
// none; the banner's button edits that account (the connection test there
// offers to trust the certificate).
func (w *Window) refreshCertBanner() {
	a, p, ok := certProblemAccount(w.syncStates, w.model.accounts)
	if !ok {
		w.certBannerAccount = ""
		w.certBanner.SetRevealed(false)
		return
	}
	w.certBannerAccount = a.ID
	w.certBanner.SetUseMarkup(false)
	w.certBanner.SetTitle(certBannerText(p.Category(), accountRowTitle(a)))
	w.certBanner.SetRevealed(true)
}

// certProblemAccount is the first enabled account, in account order, whose
// state is a certificate problem (certtrust.FromSyncState); an account
// missing from states uses the state of its account.list entry, as the
// status line does.
func certProblemAccount(states map[api.AccountID]api.SyncState, accounts []api.Account) (api.Account, certtrust.Problem, bool) {
	for _, a := range accounts {
		if !a.Enabled {
			continue
		}
		s, ok := states[a.ID]
		if !ok {
			s = a.State
		}
		if p, ok := certtrust.FromSyncState(s); ok {
			return a, p, true
		}
	}
	return api.Account{}, certtrust.Problem{}, false
}

// certBannerText is the cert_banner sentence; account is the account's
// display name.
func certBannerText(c certtrust.Category, account string) string {
	if c == certtrust.Changed {
		// TRANSLATORS: banner; %s is an account name.
		return fmt.Sprintf(i18n.T("The certificate of %s has changed"), account)
	}
	// TRANSLATORS: banner; %s is an account name.
	return fmt.Sprintf(i18n.T("The certificate of %s is not trusted"), account)
}

// onCertBannerButton opens the settings of the banner's account.
func (w *Window) onCertBannerButton() {
	w.editAccount(w.certBannerAccount)
}

// editAccount opens the account assistant on account id (the cert banner,
// an account's row in the status popover); its connection test offers to
// trust a refused certificate.
func (w *Window) editAccount(id api.AccountID) {
	a, ok := w.model.account(id)
	if !ok {
		return
	}
	accountwizard.NewEdit(w.client, w.log, a).Present(w)
}

// triggerSync runs sync.trigger for the selected folder, or for every
// account when nothing is selected (win.refresh).
func (w *Window) triggerSync() {
	params := api.SyncTriggerParams{}
	if w.model.selected.Folder != "" {
		params.AccountID = w.model.selected.Account
		params.FolderID = w.model.selected.Folder
	}
	w.startSync(params)
}

// triggerAccountSync runs sync.trigger for one whole account (Check and
// Try Again in the status popover).
func (w *Window) triggerAccountSync(id api.AccountID) {
	w.startSync(api.SyncTriggerParams{AccountID: id})
}

// startSync runs sync.trigger with params. The spinner starts at once; the
// daemon's notify.syncState takes over, with a timer as fallback so the
// spinner never sticks.
func (w *Window) startSync(params api.SyncTriggerParams) {
	w.syncSpinner.SetVisible(true)
	w.syncLabel.SetLabel(i18n.T("Checking for new mail…"))
	glib.TimeoutSecondsAdd(syncFallbackSeconds, func() bool {
		w.refreshSyncLabel()
		return false
	})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.SyncTriggerResult
		err := w.client.Call(ctx, api.MethodSyncTrigger, params, &res)
		if err == nil {
			return
		}
		glib.IdleAdd(func() {
			w.log.Debug("sync.trigger", "err", err)
			w.refreshSyncLabel()
			w.Toast(widget.RPCErrorText(i18n.T("Checking for new mail"), err))
		})
	}()
}

// showAuthRequired reveals auth_banner for the affected account. For a
// password account whose password is missing or refused the banner's
// button asks for it in the account's edit dialog, otherwise it opens the
// preferences (wired in New), where the account can be edited; for an
// account whose sign-in lives in GNOME Online Accounts (Microsoft 365,
// Google) it opens that panel, and for an account of the backend's own
// sign-in the sign-in page in the browser (the notification's authUrl is
// kept as the fallback).
func (w *Window) showAuthRequired(n api.AuthRequiredNotification) {
	name := string(n.AccountID)
	kind := signin.Password
	if a, ok := w.model.account(n.AccountID); ok {
		name = accountRowTitle(a)
		kind = signin.KindOf(a.Config)
	} else if n.AuthURL != "" {
		// Not listed yet, but only the backend's own sign-in has a URL.
		kind = signin.OAuth
	}
	// The authUrl carries the session's state: only its presence is logged.
	w.log.Debug("auth required", "account", n.AccountID, "reason", n.Reason, "authUrl", n.AuthURL != "", "kind", kind)
	w.authBannerAccount = n.AccountID
	w.authBannerKind = kind
	w.authBannerURL = n.AuthURL
	w.authBannerReason = n.Reason
	w.authBanner.SetUseMarkup(false)
	w.authBanner.SetTitle(authBannerTitle(kind, n.Reason, name))
	w.authBanner.SetButtonLabel(authBannerButton(kind, n.Reason))
	w.authBanner.SetRevealed(true)
}

// hideAuthBanner hides auth_banner and forgets its account.
func (w *Window) hideAuthBanner() {
	w.authBannerAccount = ""
	w.authBannerKind = signin.Password
	w.authBannerURL = ""
	w.authBannerReason = 0
	w.authBanner.SetRevealed(false)
}

// onAuthBannerButton is the banner button (signInAgain for the banner's
// account and its reason).
func (w *Window) onAuthBannerButton() {
	w.signInAgain(w.authBannerKind, w.authBannerReason, w.authBannerAccount, w.authBannerURL)
}

// signInAgain repairs the sign-in of account id, which signs in the given
// way after a failure with reason (notify.authRequired's reason, or the
// code of the error of the account's authRequired state; 0 when unknown):
// the account's edit dialog asking for the password when it is missing or
// refused (editsPassword), GNOME Settings for an account signed in through
// Online Accounts, the browser for the backend's own sign-in (fallback is
// the sign-in page to open when the daemon cannot start a fresh one, ""
// for none), the preferences otherwise.
func (w *Window) signInAgain(kind signin.Kind, reason api.ErrorCode, id api.AccountID, fallback string) {
	if editsPassword(kind, reason) {
		if a, ok := w.model.account(id); ok {
			wz := accountwizard.NewEdit(w.client, w.log, a)
			wz.RequestPassword(reason)
			wz.Present(w)
			return
		}
	}
	switch kind {
	case signin.OAuth:
		w.signInInBrowser(id, fallback)
	case signin.GOA:
		settingspanel.OpenOnlineAccounts(func(err error) {
			if err != nil {
				w.log.Warn("open online accounts", "err", err)
				w.Toast(i18n.T("Could not open Online Accounts; open GNOME Settings yourself"))
			}
		})
	default:
		w.app.ActivateAction("preferences", nil)
	}
}

// signInInBrowser opens the sign-in page of an account of the backend's
// own sign-in: a fresh one from account.oauthStart (the daemon hands back
// the session it is already waiting on), or fallback, the notification's
// authUrl, when the daemon cannot answer. The daemon completes the sign-in
// by itself; the banner goes away with the next notify.syncState.
func (w *Window) signInInBrowser(id api.AccountID, fallback string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), signInStartTimeout)
		defer cancel()
		var res api.AccountOAuthStartResult
		err := w.client.Call(ctx, api.MethodAccountOAuthStart,
			api.AccountOAuthStartParams{AccountID: id, BrowserPage: accountwizard.BrowserPage()}, &res)
		glib.IdleAdd(func() {
			uri := res.AuthURL
			if err != nil {
				w.log.Warn("account.oauthStart", "account", id, "err", err)
				uri = fallback
			}
			switch {
			case uri == "":
				w.Toast(widget.RPCErrorText(i18n.T("Starting the sign-in"), err))
				return
			case !signin.BrowserURL(uri):
				w.log.Warn("sign-in address refused: not https", "account", id)
				w.Toast(widget.LaunchErrorText(errors.New("not an https address")))
				return
			}
			widget.LaunchURI(&w.ApplicationWindow.Window, uri, func(err error) {
				if err != nil {
					w.log.Warn("open sign-in page", "err", err)
					w.Toast(widget.LaunchErrorText(err))
				}
			})
		})
	}()
}

// authBannerTitle is the banner sentence for an account that signs in the
// given way; account is the account's display name.
func authBannerTitle(kind signin.Kind, reason api.ErrorCode, account string) string {
	switch kind {
	case signin.GOA:
		return goaAuthBannerText(reason, account)
	case signin.OAuth:
		return oauthAuthBannerText(reason, account)
	}
	return authBannerText(reason, account)
}

// authBannerButton is the banner button's label for an account that signs
// in the given way, after a notify.authRequired with reason.
func authBannerButton(kind signin.Kind, reason api.ErrorCode) string {
	switch {
	case kind == signin.GOA:
		return i18n.T("Open Online Accounts")
	case kind == signin.OAuth:
		// TRANSLATORS: a button that signs in; the plain "Sign In" is a page title
		return i18n.C("button", "Sign In")
	case editsPassword(kind, reason):
		// TRANSLATORS: banner button
		return i18n.T("_Edit Account…")
	}
	return i18n.T("Open Preferences")
}

// editsPassword reports a sign-in banner whose button asks for the
// password in the account's edit dialog (Wizard.RequestPassword): a
// password account whose password is missing (authRequired) or refused
// (authFailed). A keyring failure is not the password's fault.
func editsPassword(kind signin.Kind, reason api.ErrorCode) bool {
	return kind == signin.Password && (reason == api.CodeAuthRequired || reason == api.CodeAuthFailed)
}

// oauthAuthBannerText is authBannerText for an account of the backend's
// own sign-in: whatever the provider refused, signing in again in the
// browser is the repair, unless the keyring that keeps the sign-in is
// what failed.
func oauthAuthBannerText(reason api.ErrorCode, account string) string {
	if reason == api.CodeKeyringError {
		return authBannerText(reason, account)
	}
	// TRANSLATORS: %s is an account name.
	return fmt.Sprintf(i18n.T("Sign in to %s again in your browser"), account)
}

// goaAuthBannerText is authBannerText for an account whose sign-in
// belongs to GNOME Online Accounts.
func goaAuthBannerText(reason api.ErrorCode, account string) string {
	if reason == api.CodeUnavailable {
		// TRANSLATORS: %s is an account name.
		return fmt.Sprintf(i18n.T("GNOME Online Accounts is not available; %s cannot sign in"), account)
	}
	// TRANSLATORS: %s is an account name.
	return fmt.Sprintf(i18n.T("Sign in to %s again in Settings → Online Accounts"), account)
}

// authBannerText is the banner sentence of a password account for a
// notify.authRequired reason; account is the account's display name.
func authBannerText(reason api.ErrorCode, account string) string {
	switch reason {
	case api.CodeAuthRequired:
		// TRANSLATORS: banner; %s is an account name.
		return fmt.Sprintf(i18n.T("No password is stored for %s"), account)
	case api.CodeAuthFailed:
		// TRANSLATORS: banner; %s is an account name.
		return fmt.Sprintf(i18n.T("The server rejected the password of %s"), account)
	case api.CodeKeyringError:
		// TRANSLATORS: %s is an account name.
		return fmt.Sprintf(i18n.T("The system keyring is unavailable; %s cannot sign in"), account)
	}
	// TRANSLATORS: %s is an account name.
	return fmt.Sprintf(i18n.T("%s needs attention"), account)
}
