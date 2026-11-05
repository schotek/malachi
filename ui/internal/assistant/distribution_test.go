// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"strings"
	"testing"
)

func TestWebsiteFeaturesNameTheWebsite(t *testing.T) {
	w := WebsiteFeaturesTexts(identity{})
	for _, s := range []string{w.GroupBody, w.DialogTitle, w.Open} {
		if !strings.Contains(s, "malachimail.app") {
			t.Errorf("%q does not name the website", s)
		}
	}
	if len(w.Features) != 4 {
		t.Errorf("Features = %d lines, want 4", len(w.Features))
	}
	if w.Close != "Close" || w.More != "More…" {
		t.Errorf("buttons = %q, %q", w.Close, w.More)
	}
}
