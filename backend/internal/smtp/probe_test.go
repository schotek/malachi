// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/schotek/malachi/backend/internal/transport/transporttest"
	"github.com/schotek/malachi/backend/pkg/api"
)

const password = "hunter2-secret"

// serverOpts configures the test server. The zero value is a plaintext
// server without AUTH that accepts every envelope and message.
type serverOpts struct {
	tls      *tls.Config
	insecure bool  // AllowInsecureAuth
	auth     bool  // advertise AUTH PLAIN for user "me" / password
	utf8     bool  // advertise SMTPUTF8
	maxBytes int64 // advertise SIZE and enforce it (0 = none)

	mailErr   error         // returned from MAIL FROM
	rcptErr   error         // returned from every RCPT TO
	dataErr   error         // returned after the message was read
	dataDelay time.Duration // wait before answering DATA (cancelled at shutdown)
}

// testServer is a running server plus what the last session recorded.
type testServer struct {
	port int
	done chan struct{}

	mu    sync.Mutex
	from  string
	rcpts []string
	data  []byte
}

// envelope returns what the server received so far.
func (ts *testServer) envelope() (from string, rcpts []string, data []byte) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.from, append([]string(nil), ts.rcpts...), append([]byte(nil), ts.data...)
}

// backend accepts one user; with auth=false it advertises no AUTH at all.
type backend struct {
	opts serverOpts
	ts   *testServer
}

func (b *backend) NewSession(*smtp.Conn) (smtp.Session, error) {
	s := session{b: b}
	if !b.opts.auth {
		return &s, nil
	}
	return &authSession{session: s}, nil
}

type session struct{ b *backend }

func (*session) Reset()        {}
func (*session) Logout() error { return nil }

func (s *session) Mail(from string, _ *smtp.MailOptions) error {
	if err := s.b.opts.mailErr; err != nil {
		return err
	}
	s.b.ts.mu.Lock()
	defer s.b.ts.mu.Unlock()
	s.b.ts.from = from
	return nil
}

func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	if err := s.b.opts.rcptErr; err != nil {
		return err
	}
	s.b.ts.mu.Lock()
	defer s.b.ts.mu.Unlock()
	s.b.ts.rcpts = append(s.b.ts.rcpts, to)
	return nil
}

func (s *session) Data(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if d := s.b.opts.dataDelay; d > 0 {
		select {
		case <-time.After(d):
		case <-s.b.ts.done:
		}
	}
	if err := s.b.opts.dataErr; err != nil {
		return err
	}
	s.b.ts.mu.Lock()
	defer s.b.ts.mu.Unlock()
	s.b.ts.data = data
	return nil
}

type authSession struct{ session }

func (*authSession) AuthMechanisms() []string { return []string{sasl.Plain} }
func (*authSession) Auth(string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(_, username, pw string) error {
		if username != "me" || pw != password {
			return smtp.ErrAuthFailed
		}
		return nil
	}), nil
}

func startServer(t *testing.T, tlsCfg *tls.Config, insecure, auth bool) int {
	t.Helper()
	return startServerWith(t, serverOpts{tls: tlsCfg, insecure: insecure, auth: auth}).port
}

func startServerWith(t *testing.T, opts serverOpts) *testServer {
	t.Helper()
	ts := &testServer{done: make(chan struct{})}
	srv := smtp.NewServer(&backend{opts: opts, ts: ts})
	srv.Domain = "localhost"
	srv.TLSConfig = opts.tls
	srv.AllowInsecureAuth = opts.insecure
	srv.EnableSMTPUTF8 = opts.utf8
	srv.MaxMessageBytes = opts.maxBytes
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { close(ts.done); srv.Close() })
	ts.port = ln.Addr().(*net.TCPAddr).Port
	return ts
}

func cfg(port int, sec api.Security) api.ServerConfig {
	return api.ServerConfig{Host: "127.0.0.1", Port: port, Security: sec, Username: "me", AuthMethod: api.AuthPassword}
}

func code(t *testing.T, err error) api.ErrorCode {
	t.Helper()
	var e *api.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *api.Error, got %T: %v", err, err)
	}
	if strings.Contains(e.Message, password) {
		t.Fatalf("password leaked into %q", e.Message)
	}
	return e.Code
}

func TestProbeSuccess(t *testing.T) {
	port := startServer(t, nil, true, true)
	res, err := Probe(context.Background(), cfg(port, api.SecurityNone), password)
	if err != nil {
		t.Fatal(err)
	}
	if res.Latency <= 0 {
		t.Fatalf("latency = %v", res.Latency)
	}
	joined := strings.Join(res.Capabilities, ",")
	if !strings.Contains(joined, "AUTH PLAIN") || !strings.Contains(joined, "8BITMIME") {
		t.Fatalf("capabilities = %v", res.Capabilities)
	}
}

func TestProbeWrongPassword(t *testing.T) {
	port := startServer(t, nil, true, true)
	_, err := Probe(context.Background(), cfg(port, api.SecurityNone), "wrong")
	if code(t, err) != api.CodeAuthFailed {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestProbeNoAuth(t *testing.T) {
	port := startServer(t, nil, true, false)
	_, err := Probe(context.Background(), cfg(port, api.SecurityNone), password)
	if code(t, err) != api.CodeServerError {
		t.Fatalf("no auth: %v", err)
	}
}

func TestProbeStartTLSNotOffered(t *testing.T) {
	port := startServer(t, nil, true, true)
	_, err := Probe(context.Background(), cfg(port, api.SecuritySTARTTLS), password)
	if code(t, err) != api.CodeTLSError {
		t.Fatalf("no STARTTLS: %v", err)
	}
}

func TestProbeStartTLSSelfSigned(t *testing.T) {
	srvTLS, _ := transporttest.SelfSigned(t)
	port := startServer(t, srvTLS, false, true)
	_, err := Probe(context.Background(), cfg(port, api.SecuritySTARTTLS), password)
	if code(t, err) != api.CodeTLSError {
		t.Fatalf("self-signed STARTTLS: %v", err)
	}
}

func TestProbeImplicitTLSSelfSigned(t *testing.T) {
	srvTLS, _ := transporttest.SelfSigned(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", srvTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	_, err = Probe(context.Background(), cfg(ln.Addr().(*net.TCPAddr).Port, api.SecurityTLS), password)
	if code(t, err) != api.CodeTLSError {
		t.Fatalf("self-signed TLS: %v", err)
	}
}

func TestProbeRefusedAndSilent(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if _, err := Probe(context.Background(), cfg(port, api.SecurityNone), password); code(t, err) != api.CodeNetworkError {
		t.Fatalf("refused: %v", err)
	}

	ln, _ = net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := Probe(ctx, cfg(ln.Addr().(*net.TCPAddr).Port, api.SecurityNone), password); code(t, err) != api.CodeServerTimeout {
		t.Fatalf("silent: %v", err)
	}
}

func TestProbeGreetingRejected(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("554 go away\r\n"))
			c.Close()
		}
	}()
	_, err := Probe(context.Background(), cfg(ln.Addr().(*net.TCPAddr).Port, api.SecurityNone), password)
	if code(t, err) != api.CodeServerError {
		t.Fatalf("554 greeting: %v", err)
	}
}

func TestVerify(t *testing.T) {
	port := startServer(t, nil, true, true)
	if err := Verify(context.Background(), cfg(port, api.SecurityNone)); err != nil {
		t.Fatal(err)
	}
}
