// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package certtrust

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

var (
	sumA = strings.Repeat("ab", 32)
	sumB = strings.Repeat("0f", 32)

	notBefore = time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	notAfter  = time.Date(2044, 1, 2, 3, 4, 5, 0, time.UTC)

	bridgeCert = api.CertificateInfo{SHA256: sumA, Subject: "127.0.0.1", Issuer: "127.0.0.1",
		IPAddresses: []string{"127.0.0.1"}, NotBefore: notBefore, NotAfter: notAfter, SelfSigned: true}

	imapTLS = api.ServerConfig{Host: "bridge.tail.example", Port: 1143, Security: api.SecuritySTARTTLS,
		Username: "me", AuthMethod: api.AuthPassword}
)

// Invisible characters of hostile certificates, as rune constants so the
// source stays readable.
var (
	rlo  = string(rune(0x202e)) // RIGHT-TO-LEFT OVERRIDE
	zwsp = string(rune(0x200b)) // ZERO WIDTH SPACE
	lri  = string(rune(0x2066)) // LEFT-TO-RIGHT ISOLATE
	lrm  = string(rune(0x200e)) // LEFT-TO-RIGHT MARK
	ls   = string(rune(0x2028)) // LINE SEPARATOR (Zl)
	ps   = string(rune(0x2029)) // PARAGRAPH SEPARATOR (Zp)
)

func tlsErr(data any) *api.Error {
	return &api.Error{Code: api.CodeTLSError, Message: "x509: certificate signed by unknown authority", Data: data}
}

// overJSON is what a client holds after the RPC: Data decoded into a map.
func overJSON(t *testing.T, e *api.Error) *api.Error {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var back api.Error
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	return &back
}

func cert(c api.CertificateInfo) *api.CertificateInfo { return &c }

func TestDetails(t *testing.T) {
	for _, c := range []struct {
		name string
		e    *api.Error
		ok   bool
		want Problem
	}{
		{"nil", nil, false, Problem{}},
		{"other code", &api.Error{Code: api.CodeNetworkError, Data: api.TLSErrorData{Reason: api.TLSUntrusted}}, false, Problem{}},
		{"no data", tlsErr(nil), false, Problem{}},
		{"empty reason", tlsErr(api.TLSErrorData{Certificate: cert(bridgeCert)}), false, Problem{}},
		{"map without reason", tlsErr(map[string]any{"certificate": map[string]any{"sha256": sumA}}), false, Problem{}},
		{"wrong shape", tlsErr("untrusted"), false, Problem{}},
		{"untrusted with certificate", tlsErr(api.TLSErrorData{Reason: api.TLSUntrusted, Certificate: cert(bridgeCert)}), true,
			Problem{Reason: api.TLSUntrusted, Cert: cert(bridgeCert)}},
		{"handshake without certificate", tlsErr(api.TLSErrorData{Reason: api.TLSHandshake}), true,
			Problem{Reason: api.TLSHandshake}},
		{"unknown reason is other", tlsErr(api.TLSErrorData{Reason: "quantumDecoherence", Certificate: cert(bridgeCert)}), true,
			Problem{Reason: api.TLSOther, Cert: cert(bridgeCert)}},
		{"pin mismatch keeps the expected fingerprint", tlsErr(api.TLSErrorData{Reason: api.TLSPinMismatch, Certificate: cert(bridgeCert), ExpectedSHA256: strings.ToUpper(sumB)}), true,
			Problem{Reason: api.TLSPinMismatch, Cert: cert(bridgeCert), Expected: sumB}},
		{"invalid expected fingerprint is dropped", tlsErr(api.TLSErrorData{Reason: api.TLSPinMismatch, ExpectedSHA256: "nope"}), true,
			Problem{Reason: api.TLSPinMismatch}},
		{"certificate with a bad fingerprint is dropped", tlsErr(api.TLSErrorData{Reason: api.TLSUntrusted, Certificate: &api.CertificateInfo{SHA256: "12:34", Subject: "x"}}), true,
			Problem{Reason: api.TLSUntrusted}},
		{"certificate without a fingerprint is dropped", tlsErr(api.TLSErrorData{Reason: api.TLSExpired, Certificate: &api.CertificateInfo{Subject: "x"}}), true,
			Problem{Reason: api.TLSExpired}},
		{"upper-case fingerprint is normalised", tlsErr(api.TLSErrorData{Reason: api.TLSUntrusted, Certificate: &api.CertificateInfo{SHA256: strings.ToUpper(sumA)}}), true,
			Problem{Reason: api.TLSUntrusted, Cert: &api.CertificateInfo{SHA256: sumA}}},
		{"pointer data", tlsErr(&api.TLSErrorData{Reason: api.TLSExpired}), true, Problem{Reason: api.TLSExpired}},
	} {
		got, ok := Details(c.e)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestDetailsOverJSON(t *testing.T) {
	in := tlsErr(api.TLSErrorData{Reason: api.TLSHostnameMismatch, Certificate: cert(bridgeCert)})
	got, ok := Details(overJSON(t, in))
	if !ok || got.Reason != api.TLSHostnameMismatch || got.Cert == nil {
		t.Fatalf("decoded: %+v %v", got, ok)
	}
	if !reflect.DeepEqual(*got.Cert, bridgeCert) {
		t.Errorf("certificate: got %+v, want %+v", *got.Cert, bridgeCert)
	}
	// A hand-written map, as another daemon version might send it.
	raw := tlsErr(map[string]any{
		"reason":      "pinMismatch",
		"certificate": map[string]any{"sha256": sumB, "subject": "mail", "notBefore": "2024-01-02T03:04:05Z", "notAfter": "2044-01-02T03:04:05Z", "selfSigned": true, "extra": 1},
		"unknown":     []any{1, 2},
	})
	got, ok = Details(overJSON(t, raw))
	if !ok || got.Reason != api.TLSPinMismatch || got.Cert == nil || got.Cert.SHA256 != sumB || !got.Cert.SelfSigned || !got.Cert.NotAfter.Equal(notAfter) {
		t.Fatalf("map: %+v %v", got, ok)
	}
	// Wrong types inside the certificate: no details at all rather than a
	// half-read certificate.
	if got, ok := Details(overJSON(t, tlsErr(map[string]any{"reason": "untrusted", "certificate": map[string]any{"sha256": 42}}))); ok {
		t.Errorf("mistyped certificate accepted: %+v", got)
	}
}

func TestDetailsCleansHostileCertificates(t *testing.T) {
	long := strings.Repeat("ž", 100) // 200 bytes
	hostile := api.CertificateInfo{
		SHA256:      sumA,
		Subject:     "  evil" + rlo + zwsp + "moc.knab\x00\x1b[31m\n ",
		Issuer:      "CA\r\nInjected: yes" + lri + ls + ps,
		DNSNames:    []string{"a.example", "", lrm, "b\texample", "c", "d", "e", "f", "g", "h", "i", "j"},
		IPAddresses: []string{"127.0.0.1\u0085", long},
		NotBefore:   notBefore,
		NotAfter:    notAfter,
	}
	for _, e := range []*api.Error{tlsErr(api.TLSErrorData{Reason: api.TLSOther, Certificate: &hostile}),
		overJSON(t, tlsErr(api.TLSErrorData{Reason: api.TLSOther, Certificate: &hostile}))} {
		p, ok := Details(e)
		if !ok || p.Cert == nil {
			t.Fatalf("no details: %+v %v", p, ok)
		}
		c := p.Cert
		if c.Subject != "evilmoc.knab[31m" {
			t.Errorf("subject: %q", c.Subject)
		}
		if c.Issuer != "CAInjected: yes" {
			t.Errorf("issuer: %q", c.Issuer)
		}
		if want := []string{"a.example", "bexample", "c", "d", "e", "f", "g", "h"}; !reflect.DeepEqual(c.DNSNames, want) {
			t.Errorf("dns names: %q", c.DNSNames)
		}
		if len(c.IPAddresses) != 2 || c.IPAddresses[0] != "127.0.0.1" {
			t.Errorf("ip addresses: %q", c.IPAddresses)
		}
		if l := c.IPAddresses[1]; len(l) > maxTextBytes || !strings.HasPrefix(long, l) || len(l) != 128 {
			t.Errorf("cap: %d bytes %q", len(l), l)
		}
		for _, s := range append(append([]string{c.Subject, c.Issuer}, c.DNSNames...), c.IPAddresses...) {
			if strings.ContainsFunc(s, isHidden) {
				t.Errorf("hidden character left in %q", s)
			}
		}
		// The caller's data is not modified.
		if hostile.Subject == c.Subject {
			t.Error("input modified")
		}
	}
}

func isHidden(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x200b || r == 0x200e || r == 0x202e || r == 0x2066 || r == 0x2028 || r == 0x2029
}

func TestCleanTextCapsOnRuneBoundary(t *testing.T) {
	s := "a" + strings.Repeat("€", 60) // 1 + 180 bytes
	got := cleanText(s)
	if len(got) > maxTextBytes || !strings.HasPrefix(s, got) || len(got) != 127 {
		t.Errorf("%d bytes: %q", len(got), got)
	}
	if got := cleanText("bad\xffutf8"); got != "badutf8" {
		t.Errorf("invalid UTF-8: %q", got)
	}
	if got := cleanText("line" + ls + "para" + ps); got != "linepara" {
		t.Errorf("line and paragraph separators: %q", got)
	}
	// Trimmed again after the cut.
	if got := cleanText(strings.Repeat("x", 127) + " y"); got != strings.Repeat("x", 127) {
		t.Errorf("trim after the cut: %q", got)
	}
}

func TestCategory(t *testing.T) {
	for r, want := range map[api.TLSErrorReason]Category{
		api.TLSUntrusted:        Certificate,
		api.TLSHostnameMismatch: Certificate,
		api.TLSExpired:          Certificate,
		api.TLSNotYetValid:      Certificate,
		api.TLSInvalid:          Certificate,
		api.TLSOther:            Certificate,
		"somethingNew":          Certificate,
		"":                      Certificate,
		api.TLSPinMismatch:      Changed,
		api.TLSHandshake:        Connection,
		api.TLSStartTLSUnavail:  Connection,
		api.TLSRequired:         Connection,
	} {
		if got := CategoryOf(r); got != want {
			t.Errorf("%q: got %d, want %d", r, got, want)
		}
	}
	if NormalizeReason("somethingNew") != api.TLSOther || NormalizeReason(api.TLSExpired) != api.TLSExpired {
		t.Error("NormalizeReason")
	}
}

func TestFormatFingerprint(t *testing.T) {
	want := strings.TrimSpace(strings.Repeat("ABAB ", 16))
	for _, in := range []string{sumA, strings.ToUpper(sumA), "AB:" + strings.Repeat("AB:", 30) + "AB"} {
		if got := FormatFingerprint(in); got != want {
			t.Errorf("%q → %q", in, got)
		}
	}
	if got := FormatFingerprint("0123456789abcdef" + strings.Repeat("0", 48)); !strings.HasPrefix(got, "0123 4567 89AB CDEF 0000") || len(got) != 64+15 {
		t.Errorf("groups: %q", got)
	}
	for _, in := range []string{"", "abc", sumA + "00", strings.Repeat("zz", 32), rlo + sumA} {
		if got := FormatFingerprint(in); got != "" {
			t.Errorf("%q accepted: %q", in, got)
		}
	}
}

func TestTrustable(t *testing.T) {
	failed := func(e *api.Error) *api.EndpointTestResult { return &api.EndpointTestResult{Error: e} }
	untrusted := failed(tlsErr(api.TLSErrorData{Reason: api.TLSUntrusted, Certificate: cert(bridgeCert)}))
	with := func(f func(*api.ServerConfig)) api.ServerConfig {
		sc := imapTLS
		f(&sc)
		return sc
	}
	for _, c := range []struct {
		name string
		res  *api.EndpointTestResult
		sc   api.ServerConfig
		want bool
	}{
		{"untrusted, starttls, password", untrusted, imapTLS, true},
		{"untrusted over JSON", failed(overJSON(t, untrusted.Error)), imapTLS, true},
		{"implicit TLS", untrusted, with(func(sc *api.ServerConfig) { sc.Security = api.SecurityTLS }), true},
		{"hostname mismatch", failed(tlsErr(api.TLSErrorData{Reason: api.TLSHostnameMismatch, Certificate: cert(bridgeCert)})), imapTLS, true},
		{"expired", failed(tlsErr(api.TLSErrorData{Reason: api.TLSExpired, Certificate: cert(bridgeCert)})), imapTLS, true},
		{"not yet valid", failed(tlsErr(api.TLSErrorData{Reason: api.TLSNotYetValid, Certificate: cert(bridgeCert)})), imapTLS, true},
		{"invalid", failed(tlsErr(api.TLSErrorData{Reason: api.TLSInvalid, Certificate: cert(bridgeCert)})), imapTLS, true},
		{"other (macOS not standards compliant)", failed(tlsErr(api.TLSErrorData{Reason: api.TLSOther, Certificate: cert(bridgeCert)})), imapTLS, true},
		{"unknown reason", failed(tlsErr(api.TLSErrorData{Reason: "fancy", Certificate: cert(bridgeCert)})), imapTLS, true},
		{"pin mismatch: trust the new certificate", failed(tlsErr(api.TLSErrorData{Reason: api.TLSPinMismatch, Certificate: cert(bridgeCert), ExpectedSHA256: sumB})),
			with(func(sc *api.ServerConfig) { sc.CertificateSHA256 = sumB }), true},
		{"already pinned to this certificate", untrusted, with(func(sc *api.ServerConfig) { sc.CertificateSHA256 = strings.ToUpper(sumA) }), false},
		{"nil result", nil, imapTLS, false},
		{"ok result", &api.EndpointTestResult{OK: true, Error: untrusted.Error}, imapTLS, false},
		{"no error", failed(nil), imapTLS, false},
		{"other error code", failed(&api.Error{Code: api.CodeNetworkError, Data: api.TLSErrorData{Reason: api.TLSUntrusted, Certificate: cert(bridgeCert)}}), imapTLS, false},
		{"tlsError without details", failed(tlsErr(nil)), imapTLS, false},
		{"no certificate", failed(tlsErr(api.TLSErrorData{Reason: api.TLSUntrusted})), imapTLS, false},
		{"certificate with a bad fingerprint", failed(tlsErr(api.TLSErrorData{Reason: api.TLSUntrusted, Certificate: &api.CertificateInfo{SHA256: "ab"}})), imapTLS, false},
		{"handshake", failed(tlsErr(api.TLSErrorData{Reason: api.TLSHandshake, Certificate: cert(bridgeCert)})), imapTLS, false},
		{"starttls unavailable", failed(tlsErr(api.TLSErrorData{Reason: api.TLSStartTLSUnavail, Certificate: cert(bridgeCert)})), imapTLS, false},
		{"tls required", failed(tlsErr(api.TLSErrorData{Reason: api.TLSRequired, Certificate: cert(bridgeCert)})), imapTLS, false},
		{"security none", untrusted, with(func(sc *api.ServerConfig) { sc.Security = api.SecurityNone }), false},
		{"security empty", untrusted, with(func(sc *api.ServerConfig) { sc.Security = "" }), false},
		{"oauth2", untrusted, with(func(sc *api.ServerConfig) { sc.AuthMethod = api.AuthOAuth2 }), false},
		{"auth method empty", untrusted, with(func(sc *api.ServerConfig) { sc.AuthMethod = "" }), false},
	} {
		p, ok := Trustable(c.res, c.sc)
		if ok != c.want {
			t.Errorf("%s: got %v, want %v", c.name, ok, c.want)
			continue
		}
		if ok && (p.Cert == nil || p.Cert.SHA256 != sumA) {
			t.Errorf("%s: problem %+v", c.name, p)
		}
		if !ok && !reflect.DeepEqual(p, Problem{}) {
			t.Errorf("%s: problem returned with false: %+v", c.name, p)
		}
	}
}

func TestSameCertificate(t *testing.T) {
	a, b := cert(bridgeCert), cert(bridgeCert)
	b.SHA256 = strings.ToUpper(sumA)
	other := cert(bridgeCert)
	other.SHA256 = sumB
	for _, c := range []struct {
		name string
		x, y *api.CertificateInfo
		want bool
	}{
		{"same", a, b, true},
		{"itself", a, a, true},
		{"different", a, other, false},
		{"nil", a, nil, false},
		{"both nil", nil, nil, false},
		{"both invalid", &api.CertificateInfo{SHA256: "x"}, &api.CertificateInfo{SHA256: "x"}, false},
		{"both empty", &api.CertificateInfo{}, &api.CertificateInfo{}, false},
	} {
		if got := SameCertificate(c.x, c.y); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestFromSyncState(t *testing.T) {
	untrusted := tlsErr(api.TLSErrorData{Reason: api.TLSUntrusted, Certificate: cert(bridgeCert)})
	for _, c := range []struct {
		name string
		s    api.SyncState
		ok   bool
		cat  Category
	}{
		{"offline untrusted", api.SyncState{Status: api.SyncOffline, Error: untrusted}, true, Certificate},
		{"offline untrusted over JSON", api.SyncState{Status: api.SyncOffline, Error: overJSON(t, untrusted)}, true, Certificate},
		{"error status counts too", api.SyncState{Status: api.SyncError, Error: untrusted}, true, Certificate},
		{"without certificate", api.SyncState{Status: api.SyncOffline, Error: tlsErr(api.TLSErrorData{Reason: api.TLSExpired})}, true, Certificate},
		{"unknown reason", api.SyncState{Status: api.SyncOffline, Error: tlsErr(api.TLSErrorData{Reason: "later"})}, true, Certificate},
		{"pin mismatch", api.SyncState{Status: api.SyncOffline, Error: tlsErr(api.TLSErrorData{Reason: api.TLSPinMismatch, Certificate: cert(bridgeCert), ExpectedSHA256: sumB})}, true, Changed},
		{"handshake is offline", api.SyncState{Status: api.SyncOffline, Error: tlsErr(api.TLSErrorData{Reason: api.TLSHandshake})}, false, 0},
		{"starttls unavailable is offline", api.SyncState{Status: api.SyncOffline, Error: tlsErr(api.TLSErrorData{Reason: api.TLSStartTLSUnavail})}, false, 0},
		{"tls required is offline", api.SyncState{Status: api.SyncOffline, Error: tlsErr(api.TLSErrorData{Reason: api.TLSRequired})}, false, 0},
		{"tlsError without details", api.SyncState{Status: api.SyncOffline, Error: tlsErr(nil)}, false, 0},
		{"network error", api.SyncState{Status: api.SyncOffline, Error: &api.Error{Code: api.CodeNetworkError}}, false, 0},
		{"no error", api.SyncState{Status: api.SyncOffline}, false, 0},
		// Connected again: the pass runs while error still holds the last
		// failure.
		{"syncing", api.SyncState{Status: api.SyncSyncing, Error: untrusted}, false, 0},
		{"idle", api.SyncState{Status: api.SyncIdle, Error: untrusted}, false, 0},
		{"disabled", api.SyncState{Status: api.SyncDisabled, Error: untrusted}, false, 0},
		{"auth required", api.SyncState{Status: api.SyncAuthRequired, Error: untrusted}, false, 0},
	} {
		p, ok := FromSyncState(c.s)
		if ok != c.ok || (ok && p.Category() != c.cat) {
			t.Errorf("%s: got %+v %v", c.name, p, ok)
		}
	}
}

func TestKeepPin(t *testing.T) {
	old := imapTLS
	old.CertificateSHA256 = sumA
	with := func(f func(*api.ServerConfig)) api.ServerConfig {
		sc := imapTLS
		f(&sc)
		return sc
	}
	for _, c := range []struct {
		name string
		old  api.ServerConfig
		cur  api.ServerConfig
		want string
	}{
		{"unchanged", old, imapTLS, sumA},
		{"user name changed", old, with(func(sc *api.ServerConfig) { sc.Username = "other" }), sumA},
		{"security tls", old, with(func(sc *api.ServerConfig) { sc.Security = api.SecurityTLS }), sumA},
		{"host case and spaces", old, with(func(sc *api.ServerConfig) { sc.Host = "  Bridge.Tail.EXAMPLE " }), sumA},
		{"new config already has another pin", old, with(func(sc *api.ServerConfig) { sc.CertificateSHA256 = sumB }), sumA},
		{"upper-case pin is normalised", with(func(sc *api.ServerConfig) { sc.CertificateSHA256 = strings.ToUpper(sumA) }), imapTLS, sumA},
		{"host changed", old, with(func(sc *api.ServerConfig) { sc.Host = "bridge2.tail.example" }), ""},
		{"port changed", old, with(func(sc *api.ServerConfig) { sc.Port = 993 }), ""},
		{"security none", old, with(func(sc *api.ServerConfig) { sc.Security = api.SecurityNone }), ""},
		{"oauth2", old, with(func(sc *api.ServerConfig) { sc.AuthMethod = api.AuthOAuth2 }), ""},
		{"no pin", imapTLS, imapTLS, ""},
		{"invalid pin", with(func(sc *api.ServerConfig) { sc.CertificateSHA256 = "abc" }), imapTLS, ""},
	} {
		if got := KeepPin(c.old, c.cur); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestServerName(t *testing.T) {
	for _, c := range []struct {
		sc   api.ServerConfig
		want string
	}{
		{api.ServerConfig{Host: "imap.example", Port: 993}, "imap.example:993"},
		{api.ServerConfig{Host: " 100.64.0.1 ", Port: 1143}, "100.64.0.1:1143"},
		{api.ServerConfig{Host: "fd7a:115c::1", Port: 1025}, "[fd7a:115c::1]:1025"},
	} {
		if got := ServerName(c.sc); got != c.want {
			t.Errorf("%+v: %q", c.sc, got)
		}
	}
}

func TestIssuedTo(t *testing.T) {
	for _, c := range []struct {
		name string
		c    api.CertificateInfo
		want string
	}{
		{"bridge: subject is the only name", bridgeCert, "127.0.0.1"},
		{"names after the subject", api.CertificateInfo{Subject: "mail.example", DNSNames: []string{"MAIL.example", "imap.example", "imap.example"}, IPAddresses: []string{"10.0.0.1"}},
			"mail.example\nimap.example, 10.0.0.1"},
		{"no subject", api.CertificateInfo{DNSNames: []string{"a.example", "b.example"}}, "a.example, b.example"},
		{"nothing", api.CertificateInfo{}, ""},
		{"hostile", api.CertificateInfo{Subject: "x" + rlo + "\ny", DNSNames: []string{zwsp, "z\x00"}}, "xy\nz"},
	} {
		if got := IssuedTo(c.c); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
