// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"fmt"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Outcome is what the test page shows and which buttons it offers.
type Outcome int

const (
	OutcomeOK         Outcome = iota // every endpoint answered
	OutcomeAuthFailed                // credentials rejected somewhere: back to the identity page
	OutcomeFailed                    // anything else: retry, edit, or add anyway
)

// Classify reduces a test result to an outcome over the endpoints the
// backend reported (imap and smtp, or graph). A result without any
// endpoint is a failure.
func Classify(res api.AccountTestResult) Outcome {
	endpoints := reported(res)
	if len(endpoints) == 0 {
		return OutcomeFailed
	}
	ok := true
	for _, r := range endpoints {
		if !r.OK {
			ok = false
		}
		if r.Error != nil && r.Error.Code == api.CodeAuthFailed {
			return OutcomeAuthFailed
		}
	}
	if ok {
		return OutcomeOK
	}
	return OutcomeFailed
}

func reported(res api.AccountTestResult) []*api.EndpointTestResult {
	var out []*api.EndpointTestResult
	for _, r := range []*api.EndpointTestResult{res.IMAP, res.SMTP, res.Graph} {
		if r != nil {
			out = append(out, r)
		}
	}
	return out
}

// EndpointSummary is the row icon and subtitle for one endpoint; nil is an
// endpoint the backend did not test. The server's capabilities are hostile
// data and deliberately not shown.
func EndpointSummary(r *api.EndpointTestResult) (icon, text string) {
	if r == nil {
		return "dialog-question-symbolic", i18n.T("Not tested")
	}
	if r.OK {
		// TRANSLATORS: %d is the connection latency in milliseconds.
		return "emblem-ok-symbolic", fmt.Sprintf(i18n.T("Connected in %d ms"), r.LatencyMS)
	}
	return "dialog-error-symbolic", widget.EndpointErrorText(r.Error)
}
