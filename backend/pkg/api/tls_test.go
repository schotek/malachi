// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeCertificateSHA256(t *testing.T) {
	hex := strings.Repeat("ab", 32)
	for _, in := range []string{
		hex,
		strings.ToUpper(hex),
		"AB:" + strings.Repeat("AB:", 30) + "AB",
		strings.Repeat("abab ", 16),
	} {
		got, ok := NormalizeCertificateSHA256(in)
		if !ok || got != hex {
			t.Errorf("%q → %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "ab", strings.Repeat("ab", 33), strings.Repeat("zz", 32), hex + "\n"} {
		if _, ok := NormalizeCertificateSHA256(in); ok {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestTLSErrorDataOf(t *testing.T) {
	d := TLSErrorData{Reason: TLSUntrusted, Certificate: &CertificateInfo{SHA256: strings.Repeat("0", 64), Subject: "127.0.0.1"}}
	e := &Error{Code: CodeTLSError, Message: "tls", Data: d}
	if got, ok := TLSErrorDataOf(e); !ok || got.Reason != TLSUntrusted || got.Certificate.Subject != "127.0.0.1" {
		t.Fatalf("in-process: %+v %v", got, ok)
	}
	// What a client gets after JSON: a map.
	raw, _ := json.Marshal(e)
	var back Error
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if _, isMap := back.Data.(map[string]any); !isMap {
		t.Fatalf("decoded data is %T", back.Data)
	}
	if got, ok := TLSErrorDataOf(&back); !ok || got.Reason != TLSUntrusted || got.Certificate == nil || got.Certificate.SHA256 != d.Certificate.SHA256 {
		t.Fatalf("decoded: %+v %v", got, ok)
	}
	for _, e := range []*Error{nil, {Code: CodeServerError, Data: d}, {Code: CodeTLSError}, {Code: CodeTLSError, Data: map[string]any{"x": 1}}} {
		if _, ok := TLSErrorDataOf(e); ok {
			t.Errorf("%+v: details found", e)
		}
	}
}
