// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestClassify(t *testing.T) {
	ok := &api.EndpointTestResult{OK: true}
	auth := &api.EndpointTestResult{Error: api.NewError(api.CodeAuthFailed, "x")}
	tls := &api.EndpointTestResult{Error: api.NewError(api.CodeTLSError, "x")}
	cases := []struct {
		res  api.AccountTestResult
		want Outcome
	}{
		{api.AccountTestResult{IMAP: ok, SMTP: ok}, OutcomeOK},
		{api.AccountTestResult{IMAP: ok, SMTP: auth}, OutcomeAuthFailed},
		{api.AccountTestResult{IMAP: tls, SMTP: auth}, OutcomeAuthFailed},
		{api.AccountTestResult{IMAP: tls, SMTP: ok}, OutcomeFailed},
		{api.AccountTestResult{}, OutcomeFailed},
	}
	for i, c := range cases {
		if got := Classify(c.res); got != c.want {
			t.Errorf("case %d: got %v, want %v", i, got, c.want)
		}
	}
}

func TestEndpointSummary(t *testing.T) {
	icon, text := EndpointSummary(&api.EndpointTestResult{OK: true, LatencyMS: 42, Capabilities: []string{"<b>x</b>"}})
	if icon != "emblem-ok-symbolic" || text != "Connected in 42 ms" {
		t.Errorf("ok: %q %q", icon, text)
	}
	icon, text = EndpointSummary(&api.EndpointTestResult{Error: api.NewError(api.CodeServerTimeout, "x")})
	if icon != "dialog-error-symbolic" || !strings.Contains(text, "did not respond") {
		t.Errorf("timeout: %q %q", icon, text)
	}
	if _, text := EndpointSummary(&api.EndpointTestResult{}); text != "Failed" {
		t.Errorf("nil error: %q", text)
	}
}

func TestClassifyGraph(t *testing.T) {
	ok := &api.EndpointTestResult{OK: true}
	auth := &api.EndpointTestResult{Error: api.NewError(api.CodeAuthFailed, "x")}
	if got := Classify(api.AccountTestResult{Graph: ok}); got != OutcomeOK {
		t.Errorf("graph ok: %v", got)
	}
	if got := Classify(api.AccountTestResult{Graph: auth}); got != OutcomeAuthFailed {
		t.Errorf("graph auth: %v", got)
	}
	if icon, text := EndpointSummary(nil); icon != "dialog-question-symbolic" || text != "Not tested" {
		t.Errorf("nil endpoint: %q %q", icon, text)
	}
}
