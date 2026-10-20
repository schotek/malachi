// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eway

import (
	"github.com/schotek/malachi/backend/internal/eway/ewaytest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

func accountFor(f *ewaytest.Server) store.Account {
	return store.Account{ID: "crm", Name: "CRM", Enabled: true, Config: api.AccountConfig{Name: "CRM", Email: "user@example.org",
		Kind: api.AccountEway, Eway: &api.EwayConfig{ServiceURL: f.ServiceURL(), Username: "user"}}}
}
