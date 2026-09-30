// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"context"
	"log/slog"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/capabilities"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Manager opens compose windows and keeps what they share: the client, the
// settings, the account list and the list of open windows. The account
// list is every account the backend lists; the From list (Accounts) is
// the ones that write mail, while a comment window is pinned to its
// issue-tracker account (commentAccount).
type Manager struct {
	app      *adw.Application
	client   *client.Client
	settings *settings.Store
	log      *slog.Logger

	// OnSent is called with a short message when a window queued a message
	// (e.g. to show a toast on the main window). May be nil.
	OnSent func(text string)
	// OnAccountsChanged is called on the main loop after an account list
	// arrived from the backend, so that the caller can ask CanComposeNew
	// again (New Message). May be nil; while set, Invalidate fetches the
	// list at once even without an open window.
	OnAccountsChanged func()

	windows  []*Window
	accounts []api.Account
	fetched  bool
}

// NewManager wires the shared state; it opens no window.
func NewManager(app *adw.Application, c *client.Client, log *slog.Logger, s *settings.Store) *Manager {
	return &Manager{app: app, client: c, settings: s, log: log.With("component", "compose")}
}

// Open shows a new compose window prefilled from p.
func (m *Manager) Open(p Params) *Window {
	if !m.fetched {
		m.refreshAccounts()
	}
	w := newWindow(m, p)
	m.windows = append(m.windows, w)
	w.SetApplication(&m.app.Application)
	w.Present()
	if msg := blockedSummary(p.Blocked); msg != "" {
		// The backend quoted the original without its remote images and
		// scripts; said once, as after a save.
		w.toast(msg)
	}
	if msg := skippedSummary(p.Skipped); msg != "" {
		// A forward that went on without some of the original's files.
		w.toast(msg)
	}
	return w
}

// Accounts returns the known accounts that write mail (the From list,
// capabilities.ComposeAccounts: not an issue tracker's), or the
// placeholder while the backend lists none.
func (m *Manager) Accounts() []api.Account {
	list := capabilities.ComposeAccounts(m.accounts)
	if len(list) == 0 {
		return dummyAccounts
	}
	return list
}

// Placeholder reports whether Accounts is the placeholder identity.
func (m *Manager) Placeholder() bool { return len(capabilities.ComposeAccounts(m.accounts)) == 0 }

// CanComposeNew reports whether New Message is offered
// (capabilities.CanComposeNew over the known accounts): while none is
// known yet, or when one of them writes mail; never for issue-tracker
// accounts alone. OnAccountsChanged says when to ask again.
func (m *Manager) CanComposeNew() bool { return capabilities.CanComposeNew(m.accounts) }

// knownAccount is the account with id among all the backend listed, those
// that write no mail included; false until the list arrived or for an id
// it lacks.
func (m *Manager) knownAccount(id api.AccountID) (api.Account, bool) {
	for _, a := range m.accounts {
		if a.ID == id {
			return a, true
		}
	}
	return api.Account{}, false
}

// commentAccount is the issue-tracker account a comment window is pinned
// to: as the backend listed it, or one that carries its id until the list
// is there (only the id reaches the backend).
func (m *Manager) commentAccount(id api.AccountID) api.Account {
	if a, ok := m.knownAccount(id); ok {
		return a
	}
	return api.Account{
		ID:           id,
		Enabled:      true,
		Config:       api.AccountConfig{Kind: api.AccountJira},
		Capabilities: []api.AccountCapability{api.CapabilityComment},
	}
}

// SelfAddress is the first account's address, for Reply All exclusion.
func (m *Manager) SelfAddress() api.Address {
	a := m.Accounts()[0]
	return api.Address{Name: a.Config.DisplayName, Address: a.Config.Email}
}

// FindDraft is the open window that edits d: the same saved draft, or the
// same Drafts message taken over (d from draft.open). nil when none.
func (m *Manager) FindDraft(d api.Draft) *Window {
	for _, w := range m.windows {
		if (d.ID != "" && w.draft.draftID == d.ID) || (d.Replaces != "" && w.params.Replaces == d.Replaces) {
			return w
		}
	}
	return nil
}

func (m *Manager) remove(w *Window) {
	for i, x := range m.windows {
		if x == w {
			m.windows = append(m.windows[:i], m.windows[i+1:]...)
			return
		}
	}
}

// Invalidate drops the cached account list (notify.accountsChanged). Open
// windows, and OnAccountsChanged while set, are refreshed at once;
// otherwise the next window fetches again.
func (m *Manager) Invalidate() {
	m.fetched = false
	if len(m.windows) > 0 || m.OnAccountsChanged != nil {
		m.refreshAccounts()
	}
}

// refreshAccounts asks the backend once per process (until Invalidate) and
// pushes the result to open windows.
func (m *Manager) refreshAccounts() {
	m.fetched = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), widget.RPCTimeout)
		defer cancel()
		var res api.AccountListResult
		err := m.client.Call(ctx, api.MethodAccountList, api.AccountListParams{}, &res)
		glib.IdleAdd(func() {
			if err != nil {
				m.log.Debug("account.list", "err", err)
				m.fetched = false // try again on the next window
				return
			}
			m.accounts = res.Accounts
			for _, w := range m.windows {
				w.setAccounts(m.Accounts(), m.Placeholder())
			}
			if m.OnAccountsChanged != nil {
				m.OnAccountsChanged()
			}
		})
	}()
}
