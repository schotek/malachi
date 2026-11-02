// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package revocation

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/revocation/revocationtest"
	"github.com/schotek/malachi/backend/pkg/api"
)

func readCert(t *testing.T, name string) *x509.Certificate {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../testdata/signatures", name))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := pem.Decode(b)
	c, err := x509.ParseCertificate(p.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func readKey(t *testing.T, name string) crypto.Signer {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../testdata/signatures", name))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := pem.Decode(b)
	k, err := x509.ParsePKCS8PrivateKey(p.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return k.(crypto.Signer)
}

// toFake sends every request to the fake server, whatever host the
// certificate names.
type toFake struct{ target *url.URL }

func (f toFake) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = f.target.Scheme, f.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

func setup(t *testing.T) (*revocationtest.Server, *Checker, *x509.Certificate, *x509.Certificate) {
	issuer := readCert(t, "intermediate.pem")
	srv := revocationtest.New(issuer, readKey(t, "intermediate.key"))
	t.Cleanup(srv.Close)
	srv.Responder = readCert(t, "mallory.pem")
	srv.ResponderKey = readKey(t, "mallory.key")
	srv.OtherKey = readKey(t, "root.key")
	u, _ := url.Parse(srv.URL)
	c := &Checker{Client: &http.Client{Transport: toFake{u}}}
	return srv, c, readCert(t, "alice.pem"), issuer
}

func TestOCSP(t *testing.T) {
	srv, c, cert, issuer := setup(t)
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		mode  revocationtest.Mode
		state api.RevocationState
		fail  api.RevocationFailure
	}{
		{revocationtest.Good, api.RevocationGood, ""},
		{revocationtest.Revoked, api.RevocationRevoked, ""},
		{revocationtest.Unknown, api.RevocationUnknown, ""},
		{revocationtest.Unsigned, "", api.RevocationFailResponse},
		{revocationtest.OtherCert, "", api.RevocationFailResponse},
		{revocationtest.WrongResponder, "", api.RevocationFailResponse},
		{revocationtest.Redirect, "", api.RevocationFailAddress},
		{revocationtest.Huge, "", api.RevocationFailResponse},
		{revocationtest.ServerError, "", api.RevocationFailServer},
		{revocationtest.OCSPTryLater, "", api.RevocationFailServer},
	}
	// Only OCSP: no CRL to fall back on.
	ocspOnly := *cert
	ocspOnly.CRLDistributionPoints = nil
	for _, cs := range cases {
		srv.SetMode(cs.mode, at)
		res, err := c.Check(context.Background(), &ocspOnly, issuer)
		if cs.fail != "" {
			if FailureOf(err) != cs.fail {
				t.Errorf("mode %d: %v, want %s", cs.mode, err, cs.fail)
			}
			continue
		}
		if err != nil || res.State != cs.state || res.Source != api.RevocationOCSP {
			t.Errorf("mode %d: %+v %v", cs.mode, res, err)
		}
		if cs.state == api.RevocationRevoked && (res.RevokedAt == nil || !res.RevokedAt.Equal(at)) {
			t.Errorf("revoked at %v", res.RevokedAt)
		}
	}
}

func TestCRL(t *testing.T) {
	srv, c, cert, issuer := setup(t)
	crlOnly := *cert
	crlOnly.OCSPServer = nil
	srv.RevokeSerial(cert.SerialNumber)
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	srv.SetMode(revocationtest.Revoked, at)
	res, err := c.Check(context.Background(), &crlOnly, issuer)
	if err != nil || res.State != api.RevocationRevoked || res.Source != api.RevocationCRL || !res.RevokedAt.Equal(at) {
		t.Fatalf("%+v %v", res, err)
	}
	srv.SetMode(revocationtest.Good, at)
	if res, err := c.Check(context.Background(), &crlOnly, issuer); err != nil || res.State != api.RevocationGood {
		t.Errorf("%+v %v", res, err)
	}
	srv.SetMode(revocationtest.Unsigned, at)
	if _, err := c.Check(context.Background(), &crlOnly, issuer); FailureOf(err) != api.RevocationFailResponse {
		t.Errorf("unsigned CRL: %v", err)
	}
	// OCSP fails, the CRL answers.
	srv.SetMode(revocationtest.Good, at)
	if res, err := c.Check(context.Background(), cert, issuer); err != nil || res.State != api.RevocationGood {
		t.Errorf("%+v %v", res, err)
	}
}

func TestAddressesAndTimeout(t *testing.T) {
	srv, c, cert, issuer := setup(t)
	private := *cert
	private.OCSPServer = []string{"http://127.0.0.1/ocsp", "http://10.0.0.1/ocsp", "ftp://example.org/x"}
	private.CRLDistributionPoints = []string{"http://intranet/crl", "http://host.local/crl"}
	strict := &Checker{Client: c.Client}
	if _, err := strict.Check(context.Background(), &private, issuer); FailureOf(err) != api.RevocationFailAddress {
		t.Errorf("private: %v", err)
	}
	if Checkable(cert, nil) || !Checkable(cert, issuer) {
		t.Error("checkable")
	}
	srv.SetMode(revocationtest.Slow, time.Time{})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := c.Check(ctx, cert, issuer); FailureOf(err) != api.RevocationFailTimeout {
		t.Errorf("slow: %v", err)
	}
}
