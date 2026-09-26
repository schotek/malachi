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
		api.CodeAuthRequired:  "sign-in required",
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
	if got := EndpointErrorText(api.NewError(api.CodeKeyringError, "locked")); got != "The system keyring is unavailable" {
		t.Errorf("keyring: %q", got)
	}
	if got := EndpointErrorText(api.NewError(api.CodeOffline, "x")); got != "No network connection" {
		t.Errorf("offline: %q", got)
	}
	if got := RPCErrorText("Testing the connection", api.NewError(api.CodeAuthRequired, "no stored password")); got != "Testing the connection failed: sign-in required" {
		t.Errorf("authRequired: %q", got)
	}
}

func TestTLSErrorTexts(t *testing.T) {
	cert := &api.CertificateInfo{SHA256: strings.Repeat("ab", 32), Subject: "127.0.0.1"}
	tlsErr := func(reason api.TLSErrorReason) *api.Error {
		return &api.Error{Code: api.CodeTLSError, Message: "x509", Data: api.TLSErrorData{Reason: reason, Certificate: cert}}
	}
	for reason, want := range map[api.TLSErrorReason]string{
		api.TLSUntrusted:        "The server's certificate is not from a trusted authority",
		api.TLSHostnameMismatch: "The server's certificate is for another name",
		api.TLSExpired:          "The server's certificate has expired",
		api.TLSNotYetValid:      "The server's certificate is not valid yet",
		api.TLSInvalid:          "The server's certificate is not valid",
		api.TLSOther:            "The system does not accept the server's certificate",
		"brandNew":              "The system does not accept the server's certificate",
		api.TLSPinMismatch:      "The server presented a different certificate than the one you trust",
		api.TLSStartTLSUnavail:  "The server does not offer STARTTLS",
		api.TLSRequired:         "The server requires TLS before signing in",
	} {
		if got := EndpointErrorText(tlsErr(reason)); got != want {
			t.Errorf("endpoint %s: %q", reason, got)
		}
		if got := RPCErrorText("Sending", tlsErr(reason)); got != want {
			t.Errorf("rpc %s: %q", reason, got)
		}
	}
	// A handshake failure and a tlsError without details keep the general
	// sentence.
	for _, e := range []*api.Error{tlsErr(api.TLSHandshake), api.NewError(api.CodeTLSError, "x")} {
		if got := EndpointErrorText(e); got != "The secure connection could not be established" {
			t.Errorf("endpoint %+v: %q", e.Data, got)
		}
		if got := RPCErrorText("Sending", e); got != "Sending failed: the secure connection could not be established" {
			t.Errorf("rpc %+v: %q", e.Data, got)
		}
	}
}
