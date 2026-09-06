// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settingspanel"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Sync status line, refresh and the sign-in banner.
//
// The daemon owns the sync state; this file only mirrors the last
// sync.status / notify.syncState per account into the sidebar's status line
// and the auth_banner. Folder and account names shown here come from the
// server and are set as plain text.

// syncFallbackSeconds is how long the spinner started by triggerSync stays
// on when no notify.syncState follows (daemon without a syncer, dropped
// notification, …).
const syncFallbackSeconds = 30

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
				// grows a syncer; not worth more than a debug line.
				w.log.Debug("sync.status", "err", err)
				w.syncSpinner.SetVisible(false)
				w.syncLabel.SetLabel(i18n.T("Not syncing"))
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
// onOutboxChanged when the number of pending outgoing messages moved.
func (w *Window) applySyncState(s api.SyncState) {
	prev := w.syncStates[s.AccountID]
	w.syncStates[s.AccountID] = s
	w.refreshSyncLabel()
	if s.AccountID == w.authBannerAccount && s.Status != api.SyncAuthRequired {
		w.hideAuthBanner()
	}
	w.onSyncFinished(prev, s)
	if prev.PendingOutbox != s.PendingOutbox {
		w.onOutboxChanged(s.AccountID)
	}
}

// refreshSyncLabel recomputes the sidebar status line from the cached
// states. Enabled accounts without a cached state fall back to the state
// account.list reported, so the line is right before sync.status answered.
func (w *Window) refreshSyncLabel() {
	text, spinning := syncStatusText(w.syncStates, w.model.accounts, func(acc api.AccountID, id api.FolderID) string {
		f, ok := w.model.folder(folderKey{Account: acc, Folder: id})
		if !ok {
			return ""
		}
		return folderTitle(f)
	})
	w.syncLabel.SetUseMarkup(false)
	w.syncLabel.SetLabel(text)
	w.syncSpinner.SetVisible(spinning)
}

// syncStatusText is the sidebar status line for the given states. Only
// enabled accounts count; an account missing from states uses the state
// embedded in its account.list entry. The most pressing state wins:
// syncing (with the folder or account name and the progress when known),
// then sign-in required, sending (the pending outbox messages of every
// account added up; sending is not a sync status, so it shows while the
// status is idle), error, offline, and finally "Up to date". With no
// enabled account the line is empty. folderName returns the display name of
// a folder or "" when unknown.
func syncStatusText(states map[api.AccountID]api.SyncState, accounts []api.Account, folderName func(api.AccountID, api.FolderID) string) (text string, spinning bool) {
	var (
		syncing      *api.SyncState
		syncingName  string
		authRequired bool
		syncError    bool
		offline      bool
		enabled      int
		pending      int
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
			authRequired = true
		case api.SyncError:
			syncError = true
		case api.SyncOffline:
			offline = true
		}
	}
	switch {
	case enabled == 0:
		return "", false
	case syncing != nil:
		if syncing.Progress >= 0 {
			// TRANSLATORS: %s is a folder or account name, %d the progress in percent.
			return fmt.Sprintf(i18n.T("Syncing %s… %d %%"), syncingName, syncing.Progress), true
		}
		// TRANSLATORS: %s is a folder or account name.
		return fmt.Sprintf(i18n.T("Syncing %s…"), syncingName), true
	case authRequired:
		return i18n.T("Sign-in required"), false
	case pending > 0:
		// TRANSLATORS: %d is the number of messages waiting in the outbox.
		return fmt.Sprintf(i18n.N("Sending %d message…", "Sending %d messages…", pending), pending), true
	case syncError:
		return i18n.T("Sync error"), false
	case offline:
		return i18n.T("Offline, retrying"), false
	}
	return i18n.T("Up to date"), false
}

// triggerSync runs sync.trigger for the selected folder, or for every
// account when nothing is selected (win.refresh). The spinner starts at
// once; the daemon's notify.syncState takes over, with a timer as fallback
// so the spinner never sticks.
func (w *Window) triggerSync() {
	params := api.SyncTriggerParams{}
	if w.model.selected.Folder != "" {
		params.AccountID = w.model.selected.Account
		params.FolderID = w.model.selected.Folder
	}
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

// showAuthRequired reveals auth_banner for the affected account. The
// banner's button opens the preferences (wired in New), where the account
// can be edited; for an account whose sign-in lives in GNOME Online
// Accounts (Microsoft 365, Google) the button opens that panel instead.
// An OAuth2 authUrl is only logged for now: the OpenURI portal flow is a
// later phase.
func (w *Window) showAuthRequired(n api.AuthRequiredNotification) {
	name := string(n.AccountID)
	goa := false
	if a, ok := w.model.account(n.AccountID); ok {
		name = accountRowTitle(a)
		goa = widget.GOAOwned(a.Config)
	}
	w.log.Debug("auth required", "account", n.AccountID, "reason", n.Reason, "authUrl", n.AuthURL, "goa", goa)
	w.authBannerAccount = n.AccountID
	w.authBannerGOA = goa
	w.authBanner.SetUseMarkup(false)
	if goa {
		w.authBanner.SetTitle(goaAuthBannerText(n.Reason, name))
		w.authBanner.SetButtonLabel(i18n.T("Open Online Accounts"))
	} else {
		w.authBanner.SetTitle(authBannerText(n.Reason, name))
		w.authBanner.SetButtonLabel(i18n.T("Open Preferences"))
	}
	w.authBanner.SetRevealed(true)
}

// hideAuthBanner hides auth_banner and forgets its account.
func (w *Window) hideAuthBanner() {
	w.authBannerAccount = ""
	w.authBannerGOA = false
	w.authBanner.SetRevealed(false)
}

// onAuthBannerButton is the banner button: GNOME Settings for an account
// signed in through Online Accounts, the preferences otherwise.
func (w *Window) onAuthBannerButton() {
	if !w.authBannerGOA {
		w.app.ActivateAction("preferences", nil)
		return
	}
	settingspanel.OpenOnlineAccounts(func(err error) {
		if err != nil {
			w.log.Warn("open online accounts", "err", err)
			w.Toast(i18n.T("Could not open Online Accounts; open GNOME Settings yourself"))
		}
	})
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

// authBannerText is the banner sentence for a notify.authRequired reason;
// account is the account's display name.
func authBannerText(reason api.ErrorCode, account string) string {
	switch reason {
	case api.CodeAuthRequired, api.CodeAuthFailed:
		// TRANSLATORS: %s is an account name.
		return fmt.Sprintf(i18n.T("Sign in to %s again"), account)
	case api.CodeKeyringError:
		// TRANSLATORS: %s is an account name.
		return fmt.Sprintf(i18n.T("The system keyring is unavailable; %s cannot sign in"), account)
	}
	// TRANSLATORS: %s is an account name.
	return fmt.Sprintf(i18n.T("%s needs attention"), account)
}
