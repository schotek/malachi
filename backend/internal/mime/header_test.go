// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package mime

import (
	"slices"
	"testing"
)

// TestCuratedHeadersKeepSenderFingerprints: the classification of
// internal/bulk reads the bulk-sending services' fields from the curated
// set, so every one of them must be curated.
func TestCuratedHeadersKeepSenderFingerprints(t *testing.T) {
	for _, name := range []string{"Feedback-ID", "X-CSA-Complaints", "X-MSFBL", "X-SG-EID", "X-Mailgun-Sid",
		"X-SES-Outgoing", "X-MC-User", "X-Mandrill-User", "X-PM-Message-Id", "X-SFMC-Stack"} {
		if !slices.Contains(curatedHeaders, name) {
			t.Errorf("%s is not curated", name)
		}
	}
}
