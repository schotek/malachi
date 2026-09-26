// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/certtrust"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/signin"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The status line at the bottom of the sidebar (status_button in
// window.blp) and its popover. The line is one sentence for all accounts
// (syncStatusText in sync.go), unless the connection to the daemon has
// something more pressing to say (statusLineFor). The popover has one row
// per account, paused ones included, with the account's state as a whole
// sentence and the action that helps (accountStatuses), a link to the
// outbox for messages that were not sent, and the daemon at the foot. The
// rows are updated in place: progress arrives every half second and must
// neither rebuild the list nor take the keyboard focus away. Account names
// and the backend's error details are shown as plain text.

// statusAction is what the button of an account's row in the status
// popover does.
type statusAction int

const (
	statusActionNone   statusAction = iota
	statusActionCheck               // sync.trigger for the account (an icon)
	statusActionRetry               // sync.trigger for the account ("Try Again")
	statusActionSignIn              // signInAgain, labelled by authBannerButton (the edit dialog asking for the password, editsPassword)
	statusActionEdit                // editAccount ("Edit Account…")
)

// accountStatus is one account's row in the status popover.
type accountStatus struct {
	Account api.AccountID
	Title   string // accountRowTitle; plain text
	Detail  string // one whole sentence, never pieced together
	Action  statusAction
	SignIn  signin.Kind // where the account signs in: the label and route of statusActionSignIn
	// Reason is why statusActionSignIn is needed: the code of the error
	// of the account's authRequired state, 0 when it has none. With a
	// password account, authRequired or authFailed lead to the edit dialog
	// asking for the password (editsPassword), as the sign-in banner does.
	Reason api.ErrorCode
	Failed int // FailedOutbox; above zero the row links to the outbox
}

// accountStatuses is the status popover's content: one entry per account,
// in account.list order, paused accounts included. Whether an account is
// paused is decided by a.Enabled, not by states: pausing sends no
// notify.syncState, so states keeps what the account said before. A state
// that came afterwards (an outbox change of the paused account) says
// "disabled" and is used for its failed messages; otherwise they come from
// the account.list entry, as does the state of an enabled account missing
// from states. folderName and now are as for syncStatusText.
func accountStatuses(states map[api.AccountID]api.SyncState, accounts []api.Account, folderName func(api.AccountID, api.FolderID) string, now time.Time) []accountStatus {
	out := make([]accountStatus, 0, len(accounts))
	for _, a := range accounts {
		st := accountStatus{Account: a.ID, Title: accountRowTitle(a), SignIn: signin.KindOf(a.Config)}
		s, ok := states[a.ID]
		if !a.Enabled {
			if !ok || s.Status != api.SyncDisabled {
				s = a.State
			}
			st.Detail = i18n.T("Paused")
			st.Failed = s.FailedOutbox
			out = append(out, st)
			continue
		}
		if !ok {
			s = a.State
		}
		st.Detail, st.Action = accountDetail(a.ID, s, folderName, now)
		if st.Action == statusActionSignIn && s.Error != nil {
			st.Reason = s.Error.Code
		}
		st.Failed = s.FailedOutbox
		out = append(out, st)
	}
	return out
}

// accountDetail is the sentence and the action for an enabled account in
// state s. The most pressing state wins: syncing (the folder and progress
// when known, never the account's name, which is the row's title), sign-in
// required, a refused or changed certificate (the reason, and the account
// settings, where it can be trusted), sending, error and offline (the
// reason when known, and a retry), and finally idle with the time of the
// last check. Syncing and sending keep the idle state's check button (the
// daemon coalesces the trigger): a pass starts after every new mail, and a
// button that disappeared then would take the keyboard focus with it.
func accountDetail(acc api.AccountID, s api.SyncState, folderName func(api.AccountID, api.FolderID) string, now time.Time) (string, statusAction) {
	switch s.Status {
	case api.SyncSyncing:
		name := ""
		if s.FolderID != "" && folderName != nil {
			name = folderName(acc, s.FolderID)
		}
		switch {
		case name == "":
			return i18n.T("Syncing…"), statusActionCheck
		case s.Progress >= 0:
			// TRANSLATORS: %s is a folder or account name, %d the progress in percent.
			return fmt.Sprintf(i18n.T("Syncing %s… %d %%"), name, s.Progress), statusActionCheck
		}
		// TRANSLATORS: %s is a folder or account name.
		return fmt.Sprintf(i18n.T("Syncing %s…"), name), statusActionCheck
	case api.SyncAuthRequired:
		return i18n.T("Sign-in required"), statusActionSignIn
	}
	if _, ok := certtrust.FromSyncState(s); ok {
		return widget.EndpointErrorText(s.Error), statusActionEdit
	}
	if s.PendingOutbox > 0 {
		return sendingText(s.PendingOutbox), statusActionCheck
	}
	switch s.Status {
	case api.SyncError:
		if s.Error != nil {
			return widget.EndpointErrorText(s.Error), statusActionRetry
		}
		return i18n.T("Sync error"), statusActionRetry
	case api.SyncOffline:
		if s.Error != nil {
			return widget.EndpointErrorText(s.Error), statusActionRetry
		}
		return i18n.T("Offline, retrying"), statusActionRetry
	}
	if s.LastSync != nil && !s.LastSync.IsZero() {
		// TRANSLATORS: state of an account; %s is the time of its last
		// check for new mail, e.g. "15:04", or its date when that was
		// before today.
		return fmt.Sprintf(i18n.T("Last synced %s"), widget.FormatDate(*s.LastSync, now)), statusActionCheck
	}
	return i18n.T("Up to date"), statusActionCheck
}

// connView is what the window knows of its daemon connection, and of the
// one call whose failure the status line reports (sync.status).
type connView struct {
	State client.State
	// Info is system.info's answer, nil until it arrived or when it
	// failed (InfoFailed).
	Info       *api.SystemInfoResult
	InfoFailed bool
	// SyncFailed is set when sync.status failed; the next state from the
	// daemon clears it.
	SyncFailed bool
}

// statusLine is what the status button shows: the line, whether the
// spinner turns, the connection icon ("" for none), whether the button can
// be clicked, and the foot of its popover ("" for none).
type statusLine struct {
	Text     string
	Spinning bool
	Icon     string
	Active   bool
	Daemon   string
}

// statusLineFor puts the connection over the sync state (text and spinning
// from syncStatusText). Without a connection the line says so, with an
// icon, and the button cannot be clicked: there is no account state to
// show. A daemon of another protocol version, or a failed sync.status,
// takes the line over as well. The popover's foot names the daemon once
// system.info answered, or that it failed; it is empty while the line
// says the protocols do not match.
func statusLineFor(c connView, text string, spinning bool) statusLine {
	switch c.State {
	case client.Connecting:
		return statusLine{Text: i18n.T("Connecting to backend…"), Icon: "network-idle-symbolic"}
	case client.Disconnected:
		return statusLine{Text: i18n.T("Backend unavailable"), Icon: "network-offline-symbolic"}
	}
	if c.Info != nil && c.Info.ProtocolVersion != api.ProtocolVersion {
		return statusLine{
			Text:   fmt.Sprintf(i18n.T("Protocol mismatch: UI %d, backend %d"), api.ProtocolVersion, c.Info.ProtocolVersion),
			Active: true,
		}
	}
	line := statusLine{Text: text, Spinning: spinning}
	if c.SyncFailed {
		line.Text, line.Spinning = i18n.T("Not syncing"), false
	}
	// An empty line (no account at all) is no button to tab to.
	line.Active = line.Text != ""
	switch {
	case c.InfoFailed:
		line.Daemon = i18n.T("Connected, but system.info failed")
	case c.Info != nil:
		line.Daemon = fmt.Sprintf(i18n.T("Connected to malachid %s (pid %d)"), c.Info.Version, c.Info.PID)
	}
	return line
}

// sameAccounts reports whether the popover's rows, built for the accounts
// in order, still fit list.
func sameAccounts(order []api.AccountID, list []accountStatus) bool {
	if len(order) != len(list) {
		return false
	}
	for i, st := range list {
		if order[i] != st.Account {
			return false
		}
	}
	return true
}

// statusRow is one account's part of the status popover: the account's
// row with its action buttons (at most one of them shown), and the row that
// leads to its unsent messages, hidden while there are none.
type statusRow struct {
	row    *adw.ActionRow
	check  *gtk.Button // statusActionCheck: an icon
	button *gtk.Button // the other actions: a label (statusButtonLabel)
	failed *adw.ActionRow
	arrow  *gtk.Image
	// status is what the rows show; set is false until the first apply.
	status accountStatus
	set    bool
}

// refreshStatusPopover brings the popover's rows up to date: rebuilt when
// the accounts changed, updated in place otherwise, so a row keeps the
// keyboard focus while its account syncs.
func (w *Window) refreshStatusPopover() {
	list := accountStatuses(w.syncStates, w.model.accounts, w.syncFolderName, time.Now())
	if !sameAccounts(w.statusOrder, list) {
		w.statusAccounts.RemoveAll()
		w.statusRows = make(map[api.AccountID]*statusRow, len(list))
		w.statusOrder = make([]api.AccountID, 0, len(list))
		for _, st := range list {
			r := w.newStatusRow(st.Account)
			w.statusRows[st.Account] = r
			w.statusOrder = append(w.statusOrder, st.Account)
			w.statusAccounts.Append(r.row)
			w.statusAccounts.Append(r.failed)
		}
	}
	for _, st := range list {
		_, outbox := w.model.outboxKey(st.Account)
		w.statusRows[st.Account].apply(st, outbox)
	}
	w.statusAccounts.SetVisible(len(list) > 0)
}

// newStatusRow builds the rows of account id; apply fills them.
func (w *Window) newStatusRow(id api.AccountID) *statusRow {
	r := &statusRow{row: adw.NewActionRow(), failed: adw.NewActionRow()}
	// The name is the user's, the detail may carry the backend's message.
	r.row.SetUseMarkup(false)
	r.row.SetTitleLines(1)
	r.row.SetActivatable(false)
	r.check = gtk.NewButtonFromIconName("view-refresh-symbolic")
	r.check.SetTooltipText(i18n.T("Check for New Mail"))
	r.check.AddCSSClass("flat")
	r.button = gtk.NewButton()
	for _, b := range []*gtk.Button{r.check, r.button} {
		b.SetVAlign(gtk.AlignCenter)
		b.SetVisible(false)
		b.ConnectClicked(func() { w.onStatusAction(id) })
		r.row.AddSuffix(b)
	}

	r.failed.SetUseMarkup(false)
	r.arrow = gtk.NewImageFromIconName("go-next-symbolic")
	r.failed.AddSuffix(r.arrow)
	r.failed.ConnectActivated(func() {
		w.statusButton.Popdown()
		w.showOutbox(id)
	})
	r.failed.SetVisible(false)
	return r
}

// apply shows st on the rows. The buttons are only touched when the
// action changed, so one that has the focus keeps it while the detail
// moves. outbox says whether the account's outbox can be shown
// (outboxKey): a paused account's cannot.
func (r *statusRow) apply(st accountStatus, outbox bool) {
	r.row.SetTitle(st.Title)
	r.row.SetSubtitle(st.Detail)
	if !r.set || st.Action != r.status.Action || st.SignIn != r.status.SignIn || st.Reason != r.status.Reason {
		r.check.SetVisible(st.Action == statusActionCheck)
		label := statusButtonLabel(st)
		if label != "" {
			r.button.SetUseUnderline(statusButtonMnemonic(st))
			r.button.SetLabel(label)
		}
		r.button.SetVisible(label != "")
	}
	if st.Failed > 0 {
		r.failed.SetTitle(notSentText(st.Failed))
	}
	r.failed.SetVisible(st.Failed > 0)
	r.failed.SetActivatable(outbox)
	r.arrow.SetVisible(outbox)
	r.status = st
	r.set = true
}

// statusButtonLabel is the label of the button that repairs an account:
// "" when there is nothing to repair (checking for new mail is an icon of
// its own). "_Edit Account…" carries a mnemonic (statusButtonMnemonic).
func statusButtonLabel(st accountStatus) string {
	switch st.Action {
	case statusActionRetry:
		return i18n.T("Try Again")
	case statusActionSignIn:
		return authBannerButton(st.SignIn, st.Reason)
	case statusActionEdit:
		return i18n.T("_Edit Account…")
	}
	return ""
}

// statusButtonMnemonic reports a statusButtonLabel with a mnemonic: the
// label "_Edit Account…", for a certificate or a password to fix.
func statusButtonMnemonic(st accountStatus) bool {
	return st.Action == statusActionEdit || (st.Action == statusActionSignIn && editsPassword(st.SignIn, st.Reason))
}

// onStatusAction runs the action of account id's row, after closing the
// popover: a dialog, the browser or the list it leads to takes over.
func (w *Window) onStatusAction(id api.AccountID) {
	r := w.statusRows[id]
	if r == nil {
		return
	}
	st := r.status
	w.statusButton.Popdown()
	switch st.Action {
	case statusActionCheck, statusActionRetry:
		w.triggerAccountSync(id)
	case statusActionSignIn:
		// Only the banner's account has the notification's sign-in page.
		fallback := ""
		if id == w.authBannerAccount {
			fallback = w.authBannerURL
		}
		w.signInAgain(st.SignIn, st.Reason, id, fallback)
	case statusActionEdit:
		w.editAccount(id)
	}
}

// showOutbox selects the account's outbox, as a click on its row in the
// account's tree would (the popover's link to unsent messages).
func (w *Window) showOutbox(acc api.AccountID) {
	k, ok := w.model.outboxKey(acc)
	if !ok {
		return
	}
	w.model.selectedFav = false
	w.selectFolder(k)
	w.outerSplit.SetShowContent(true)
}
