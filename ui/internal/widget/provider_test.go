// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestAccountProvider(t *testing.T) {
	graph := api.AccountConfig{Kind: api.AccountGraph, Graph: &api.GraphConfig{Source: api.GraphSourceGOA, GOAAccountID: "account_1_0"}}
	google := api.AccountConfig{
		IMAP: &api.ServerConfig{AuthMethod: api.AuthOAuth2}, SMTP: &api.ServerConfig{AuthMethod: api.AuthOAuth2},
		OAuth2: &api.OAuth2Config{Source: api.OAuth2SourceGOA, Provider: api.OAuth2ProviderGoogle, GOAAccountID: "account_2_0"},
	}
	own := api.AccountConfig{IMAP: &api.ServerConfig{AuthMethod: api.AuthOAuth2}, OAuth2: &api.OAuth2Config{Provider: api.OAuth2ProviderOffice365}}
	password := api.AccountConfig{IMAP: &api.ServerConfig{AuthMethod: api.AuthPassword}}
	for name, c := range map[string]struct {
		cfg      api.AccountConfig
		provider string
	}{
		"graph": {graph, ProviderMicrosoft365}, "google": {google, ProviderGoogle}, "own flow": {own, ""}, "password": {password, ""},
	} {
		if got := AccountProvider(c.cfg); got != c.provider || GOAOwned(c.cfg) != (c.provider != "") {
			t.Errorf("%s: provider %q, owned %v", name, got, GOAOwned(c.cfg))
		}
	}
	if ProviderName(ProviderMicrosoft365) != "Microsoft 365" || ProviderName(ProviderGoogle) != "Google" || ProviderName("") != "" {
		t.Error("provider names")
	}
	if providerIconName(ProviderGoogle) != "goa-account-google-symbolic" || providerIconName(ProviderMicrosoft365) != "goa-account-ms365-symbolic" || providerIconName("x") != "" {
		t.Error("provider icons")
	}
}
