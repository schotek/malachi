package compose

import "github.com/schotek/malachi/backend/pkg/api"

// dummyAccounts is the identity used while account.list is not implemented.
// TODO(phase-1): remove once the backend serves accounts.
var dummyAccounts = []api.Account{{
	ID:      "acc_dummy",
	Enabled: true,
	Config: api.AccountConfig{
		Name:        "Placeholder",
		Email:       "me@example.invalid",
		DisplayName: "Malachi User",
	},
}}
