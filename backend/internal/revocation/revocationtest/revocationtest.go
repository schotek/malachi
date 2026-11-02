// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package revocationtest is a fake OCSP responder and CRL server for the
// tests of internal/revocation and core (never in production): it answers
// for one issuer with responses it signs on the fly, in the Mode a test
// sets.
package revocationtest

import (
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"golang.org/x/crypto/ocsp"
)

// Mode is how the server answers.
type Mode int

const (
	Good           Mode = iota // not revoked
	Revoked                    // revoked at RevokedAt
	Unknown                    // the responder does not know the certificate
	Unsigned                   // a response signed by another key
	OtherCert                  // a response for another serial number
	Redirect                   // 302 to another URL
	Huge                       // an answer over the cap
	Slow                       // never answers before the client gives up
	ServerError                // HTTP 500
	OCSPTryLater               // an OCSP error status
	WrongResponder             // signed by a certificate of the issuer not meant for OCSP
)

// Server is the fake. Set the fields before the first request.
type Server struct {
	*httptest.Server
	Issuer    *x509.Certificate
	IssuerKey crypto.Signer
	// Responder signs OCSP answers in WrongResponder mode (a certificate
	// the issuer issued without the OCSP signing usage); other modes sign
	// with the issuer.
	Responder    *x509.Certificate
	ResponderKey crypto.Signer
	// OtherKey signs in Unsigned mode.
	OtherKey crypto.Signer

	mu        sync.Mutex
	mode      Mode
	revokedAt time.Time
	hits      int
	serial    *big.Int
}

// New starts a server for the issuer.
func New(issuer *x509.Certificate, key crypto.Signer) *Server {
	s := &Server{Issuer: issuer, IssuerKey: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/ocsp", s.ocsp)
	mux.HandleFunc("/crl", s.crl)
	s.Server = httptest.NewServer(mux)
	return s
}

// SetMode sets how the next requests are answered.
func (s *Server) SetMode(m Mode, revokedAt time.Time) {
	s.mu.Lock()
	s.mode, s.revokedAt = m, revokedAt
	s.mu.Unlock()
}

// Hits counts the requests answered.
func (s *Server) Hits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits
}

func (s *Server) state() (Mode, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits++
	return s.mode, s.revokedAt
}

func (s *Server) common(w http.ResponseWriter, r *http.Request, m Mode) bool {
	switch m {
	case Redirect:
		http.Redirect(w, r, "http://example.org/elsewhere", http.StatusFound)
		return true
	case Huge:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, 2<<20))
		return true
	case Slow:
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
		return true
	case ServerError:
		w.WriteHeader(http.StatusInternalServerError)
		return true
	}
	return false
}

func (s *Server) ocsp(w http.ResponseWriter, r *http.Request) {
	m, at := s.state()
	if s.common(w, r, m) {
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	req, err := ocsp.ParseRequest(body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if m == OCSPTryLater {
		_, _ = w.Write(ocsp.TryLaterErrorResponse)
		return
	}
	now := time.Now()
	tmpl := ocsp.Response{
		SerialNumber: req.SerialNumber,
		ThisUpdate:   now.Add(-time.Hour),
		NextUpdate:   now.Add(24 * time.Hour),
	}
	switch m {
	case Revoked:
		tmpl.Status = ocsp.Revoked
		tmpl.RevokedAt = at
	case Unknown:
		tmpl.Status = ocsp.Unknown
	default:
		tmpl.Status = ocsp.Good
	}
	if m == OtherCert {
		tmpl.SerialNumber = new(big.Int).Add(req.SerialNumber, big.NewInt(1))
	}
	signer, cert := s.IssuerKey, s.Issuer
	switch m {
	case Unsigned:
		signer = s.OtherKey
	case WrongResponder:
		signer = s.ResponderKey
		tmpl.Certificate = s.Responder
	}
	responder := cert
	if m == WrongResponder {
		responder = s.Responder
	}
	out, err := ocsp.CreateResponse(cert, responder, tmpl, signer)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/ocsp-response")
	_, _ = w.Write(out)
}

// RevokeSerial makes the CRL list serial in Revoked mode.
func (s *Server) RevokeSerial(serial *big.Int) {
	s.mu.Lock()
	s.serial = serial
	s.mu.Unlock()
}

func (s *Server) crl(w http.ResponseWriter, r *http.Request) {
	m, at := s.state()
	if s.common(w, r, m) {
		return
	}
	now := time.Now()
	tmpl := &x509.RevocationList{
		Number:     big.NewInt(1),
		ThisUpdate: now.Add(-time.Hour),
		NextUpdate: now.Add(24 * time.Hour),
	}
	if m == Revoked {
		s.mu.Lock()
		serial := s.serial
		s.mu.Unlock()
		if serial != nil {
			tmpl.RevokedCertificateEntries = []x509.RevocationListEntry{{SerialNumber: serial, RevocationTime: at}}
		}
	}
	signer := s.IssuerKey
	if m == Unsigned {
		signer = s.OtherKey
	}
	der, err := x509.CreateRevocationList(rand.Reader, tmpl, s.Issuer, signer)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(der)
}
