// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/accountservices"
)

// pruneAccountServices drops Not Now of the calendar and contacts offers
// of accounts that no longer exist (docs/plans/account-services.md, K4).
// It runs with every account.list of the main window (loadAccounts: at
// start, on notify.accountsChanged and after a reconnect), the one place
// that sees the daemon's whole account list; the store writes only a
// change.
func (w *Window) pruneAccountServices(accounts []api.Account) {
	st := w.settings
	if st == nil {
		return
	}
	ids := make([]api.AccountID, 0, len(accounts))
	for _, a := range accounts {
		ids = append(ids, a.ID)
	}
	st.SetAccountServicesDismissed(accountservices.Pruned(st.AccountServicesDismissed(), ids))
}
