// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"context"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
)

func TestRPCErrorText(t *testing.T) {
	cases := map[api.ErrorCode]string{
		api.CodeAuthFailed:    "rejected the user name or password",
		api.CodeNetworkError:  "could not be reached",
		api.CodeServerError:   "returned an error",
		api.CodeTLSError:      "secure connection",
		api.CodeServerTimeout: "did not respond",
		api.CodeKeyringError:  "keyring",
		api.CodeConflict:      "conflicted",
	}
	for code, want := range cases {
		got := RPCErrorText("Testing", api.NewError(code, "detail"))
		if !strings.HasPrefix(got, "Testing ") || !strings.Contains(got, want) {
			t.Errorf("%v: %q", code, got)
		}
	}
	if got := RPCErrorText("Testing", client.ErrDisconnected); !strings.Contains(got, "running mail backend") {
		t.Errorf("disconnected: %q", got)
	}
	if got := RPCErrorText("Testing", context.DeadlineExceeded); !strings.Contains(got, "timed out") {
		t.Errorf("deadline: %q", got)
	}
	if got := RPCErrorText("Testing", api.NewError(9999, "x")); got != "Testing failed" {
		t.Errorf("unknown: %q", got)
	}
}

func TestEndpointErrorText(t *testing.T) {
	if got := EndpointErrorText(nil); got != "Failed" {
		t.Errorf("nil: %q", got)
	}
	if got := EndpointErrorText(api.NewError(api.CodeInvalidArgument, "port 0")); got != "Rejected: port 0" {
		t.Errorf("invalid: %q", got)
	}
	if got := EndpointErrorText(api.NewError(9999, "odd")); got != "Failed: odd" {
		t.Errorf("unknown: %q", got)
	}
	if got := EndpointErrorText(api.NewError(api.CodeTLSError, "x")); !strings.Contains(got, "secure connection") {
		t.Errorf("tls: %q", got)
	}
}
