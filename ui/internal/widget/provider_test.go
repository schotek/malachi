// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"testing"

	"github.com/schotek/malachi/ui/internal/signin"
)

// The sign-in kind and the provider of an account are tested in package
// signin.
func TestProviderIconName(t *testing.T) {
	if providerIconName(signin.ProviderGoogle) != "goa-account-google-symbolic" || providerIconName(signin.ProviderMicrosoft365) != "goa-account-ms365-symbolic" || providerIconName("x") != "" {
		t.Error("provider icons")
	}
}
