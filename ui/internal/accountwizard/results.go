// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"fmt"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Outcome summarises an account.test result for the wizard's flow.
type Outcome int

const (
	OutcomeOK         Outcome = iota // both endpoints answered
	OutcomeAuthFailed                // credentials rejected somewhere: back to the identity page
	OutcomeFailed                    // anything else: retry, edit, or add anyway
)

// Classify decides what the results page offers.
func Classify(res api.AccountTestResult) Outcome {
	if res.IMAP.OK && res.SMTP.OK {
		return OutcomeOK
	}
	for _, r := range []api.EndpointTestResult{res.IMAP, res.SMTP} {
		if r.Error != nil && r.Error.Code == api.CodeAuthFailed {
			return OutcomeAuthFailed
		}
	}
	return OutcomeFailed
}

// EndpointSummary is the row icon and subtitle for one endpoint. The
// server's capabilities are hostile data and deliberately not shown.
func EndpointSummary(r api.EndpointTestResult) (icon, text string) {
	if r.OK {
		// TRANSLATORS: %d is the connection latency in milliseconds.
		return "emblem-ok-symbolic", fmt.Sprintf(i18n.T("Connected in %d ms"), r.LatencyMS)
	}
	return "dialog-error-symbolic", widget.EndpointErrorText(r.Error)
}
