// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package updatetest is a fake of the update API and of the GitHub
// fallback for tests: an https server on 127.0.0.1 that serves an
// envelope signed with a key of its own, answers If-None-Match with 304
// and can be told to fail. No test touches the network.
package updatetest

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/update"
)

// KeyID is the keyId of the fake's key.
const KeyID = "fake-1"

// Server serves "/api/v1/updates" (the API) and "/updates.json" (the
// fallback).
type Server struct {
	*httptest.Server
	key ed25519.PrivateKey
	pub ed25519.PublicKey

	mu         sync.Mutex
	envelope   []byte
	status     int    // non-zero: answer every request with it
	retryAfter string // with status 429 or 503
	apiDown    bool   // the API path fails with status, the fallback works
	requests   map[string]int
	userAgents []string
}

// New starts a server; it stops with the test.
func New(t interface{ Cleanup(func()) }) *Server {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	s := &Server{key: key, pub: pub, requests: map[string]int{}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Keys are the public keys that verify what the server signs.
func (s *Server) Keys() map[string]ed25519.PublicKey {
	return map[string]ed25519.PublicKey{KeyID: s.pub}
}

// Client is an update.Client that asks this server and trusts its
// certificate.
func (s *Server) Client() *update.Client {
	c := update.NewClient("0.0.0-test")
	c.PrimaryURL = s.URL + "/api/v1/updates"
	c.FallbackURL = s.URL + "/updates.json"
	c.FallbackHosts = []string{"127.0.0.1"}
	tr := s.Server.Client().Transport
	c.Primary.Transport = tr
	c.Fallback.Transport = tr
	return c
}

// Sign makes an envelope of payload with key.
func Sign(key ed25519.PrivateKey, keyID string, payload []byte) []byte {
	b, err := json.Marshal(map[string]string{
		"keyId":     keyID,
		"payload":   base64.StdEncoding.EncodeToString(payload),
		"signature": base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload)),
	})
	if err != nil {
		panic(err)
	}
	return b
}

// Publish serves payload signed with the server's key.
func (s *Server) Publish(payload []byte) {
	s.PublishEnvelope(Sign(s.key, KeyID, payload))
}

// PublishEnvelope serves envelope as it is.
func (s *Server) PublishEnvelope(envelope []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.envelope = envelope
}

// Fail answers every request with status (0: serve again) and, when not
// empty, Retry-After.
func (s *Server) Fail(status int, retryAfter string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.retryAfter, s.apiDown = status, retryAfter, false
}

// FailAPI fails only the API path; the fallback still serves.
func (s *Server) FailAPI(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.apiDown = status, true
}

// Requests counts the requests of a path.
func (s *Server) Requests(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[path]
}

// UserAgents are the User-Agent headers received, in order.
func (s *Server) UserAgents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.userAgents...)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests[r.URL.Path]++
	s.userAgents = append(s.userAgents, r.Header.Get("User-Agent"))
	env, status, retry, apiDown := s.envelope, s.status, s.retryAfter, s.apiDown
	s.mu.Unlock()
	if r.URL.Path != "/api/v1/updates" && r.URL.Path != "/updates.json" {
		http.NotFound(w, r)
		return
	}
	if status != 0 && (!apiDown || r.URL.Path == "/api/v1/updates") {
		if retry != "" {
			w.Header().Set("Retry-After", retry)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"unavailable"}`))
		return
	}
	if env == nil {
		w.Header().Set("Retry-After", strconv.Itoa(int((5 * time.Minute).Seconds())))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"unavailable"}`))
		return
	}
	sum := sha256.Sum256(env)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	if r.URL.Path == "/updates.json" {
		w.Header().Set("Content-Type", "application/octet-stream")
	} else {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "public, max-age=300")
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	_, _ = w.Write(env)
}
