// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"strings"
	"testing"
)

func TestWebsiteOnlyTextNamesTheWebsite(t *testing.T) {
	got := WebsiteOnlyText(identity{})
	if !strings.Contains(got, "malachimail.app") {
		t.Fatalf("WebsiteOnlyText = %q, want the website", got)
	}
}
