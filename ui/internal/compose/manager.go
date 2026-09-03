// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"context"
	"log/slog"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Manager opens compose windows and keeps what they share: the client, the
// settings, the account list and the list of open windows.
type Manager struct {
	app      *adw.Application
	client   *client.Client
	settings *settings.Store
	log      *slog.Logger

	// OnSent is called with a short message when a window queued a message
	// (e.g. to show a toast on the main window). May be nil.
	OnSent func(text string)

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
	return w
}

// Accounts returns the known accounts, or the placeholder while the
// backend cannot list any.
func (m *Manager) Accounts() []api.Account {
	if len(m.accounts) == 0 {
		return dummyAccounts
	}
	return m.accounts
}

// Placeholder reports whether Accounts is the placeholder identity.
func (m *Manager) Placeholder() bool { return len(m.accounts) == 0 }

// SelfAddress is the first account's address, for Reply All exclusion.
func (m *Manager) SelfAddress() api.Address {
	a := m.Accounts()[0]
	return api.Address{Name: a.Config.DisplayName, Address: a.Config.Email}
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
// windows are refreshed at once; otherwise the next window fetches again.
func (m *Manager) Invalidate() {
	m.fetched = false
	if len(m.windows) > 0 {
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
		})
	}()
}
