// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestBlockedSummary(t *testing.T) {
	if got := blockedSummary(api.BlockedContent{}); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := blockedSummary(api.BlockedContent{RemoteImages: 2, Scripts: 1}); got != "3 unsafe elements were removed from the message" {
		t.Errorf("plural: got %q", got)
	}
	if got := blockedSummary(api.BlockedContent{Forms: 1}); got != "1 unsafe element was removed from the message" {
		t.Errorf("singular: got %q", got)
	}
}
