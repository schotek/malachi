// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/internal/transport/transporttest"
	"github.com/schotek/malachi/backend/pkg/api"
)

// tlsServer serves s over TLS on loopback with a self-signed certificate.
func tlsServer(t *testing.T, s *caldavtest.Server) (url, pin string) {
	t.Helper()
	cfg, cert := transporttest.SelfSigned(t)
	srv := httptest.NewUnstartedServer(s.Handler())
	srv.TLS = cfg
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.URL, transporttest.Fingerprint(cert)
}

func tlsData(t *testing.T, err error) api.TLSErrorData {
	t.Helper()
	ae := ToAPIError(err)
	if ae == nil || ae.Code != api.CodeTLSError {
		t.Fatalf("want tlsError, got %v", err)
	}
	d, ok := ae.Data.(api.TLSErrorData)
	if !ok {
		t.Fatalf("data %T", ae.Data)
	}
	return d
}

func TestTLSPin(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	base, pin := tlsServer(t, s)
	open := func(pinned string) *Client {
		c, err := NewClient(Options{
			ServerURL: base + "/remote.php/dav", Username: caldavtest.Username, CertificateSHA256: pinned,
			Password: func(context.Context) (string, error) { return caldavtest.Password, nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	_, err := open("").ListCalendars(t.Context())
	d := tlsData(t, err)
	if d.Reason != api.TLSUntrusted || d.Certificate == nil || d.Certificate.SHA256 != pin {
		t.Fatalf("unpinned: %+v", d)
	}
	for _, r := range s.Requests() {
		if r.HasAuthorization {
			t.Fatal("a password went over an unverified connection")
		}
	}

	info, err := open(pin).ListCalendars(t.Context())
	if err != nil || len(info.Calendars) != 2 {
		t.Fatalf("pinned: %+v %v", info, err)
	}

	wrong := strings.Repeat("0", 64)
	_, err = open(wrong).ListCalendars(t.Context())
	d = tlsData(t, err)
	if d.Reason != api.TLSPinMismatch || d.ExpectedSHA256 != wrong || d.Certificate == nil || d.Certificate.SHA256 != pin {
		t.Fatalf("wrong pin: %+v", d)
	}
}

func TestDiscoverOverTLS(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	base, pin := tlsServer(t, s)
	_, err := (&Discoverer{}).Discover(t.Context(), base+"/remote.php/dav")
	if d := tlsData(t, err); d.Reason != api.TLSUntrusted {
		t.Fatalf("unpinned: %+v", d)
	}
	res, err := (&Discoverer{Pin: pin}).Discover(t.Context(), base+"/remote.php/dav")
	if err != nil {
		t.Fatal(err)
	}
	if res.FoundVia != api.CalDAVFoundURL || res.ServerURL != base+"/remote.php/dav" || res.Certificate == nil ||
		res.Certificate.SHA256 != pin || !res.Certificate.SelfSigned || res.Certificate.Subject != "localhost" {
		t.Fatalf("result %+v cert %+v", res, res.Certificate)
	}
}
