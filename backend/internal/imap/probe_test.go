// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/internal/transport/transporttest"
	"github.com/schotek/malachi/backend/pkg/api"
)

const password = "hunter2-secret"

// startServer runs an in-memory IMAP server on a loopback port with one
// user. tlsCfg enables STARTTLS; insecure allows LOGIN without TLS.
func startServer(t *testing.T, tlsCfg *tls.Config, insecure bool) int {
	t.Helper()
	mem := imapmemserver.New()
	mem.AddUser(imapmemserver.NewUser("me", password))
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}},
		TLSConfig:    tlsCfg,
		InsecureAuth: insecure,
		Logger:       discardLogger{},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

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
	port := startServer(t, nil, true)
	res, err := Probe(context.Background(), cfg(port, api.SecurityNone), password)
	if err != nil {
		t.Fatal(err)
	}
	if res.Latency <= 0 {
		t.Fatalf("latency = %v", res.Latency)
	}
	found := false
	for _, c := range res.Capabilities {
		if c == "IMAP4rev1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("capabilities = %v", res.Capabilities)
	}
}

func TestProbeWrongPassword(t *testing.T) {
	port := startServer(t, nil, true)
	_, err := Probe(context.Background(), cfg(port, api.SecurityNone), "wrong")
	if code(t, err) != api.CodeAuthFailed {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestProbeLoginDisabled(t *testing.T) {
	port := startServer(t, nil, false) // no TLS, no insecure auth → LOGINDISABLED
	_, err := Probe(context.Background(), cfg(port, api.SecurityNone), password)
	if c := code(t, err); c != api.CodeServerError && c != api.CodeAuthFailed {
		t.Fatalf("login disabled: %v", err)
	}
}

func TestProbeStartTLSNotOffered(t *testing.T) {
	port := startServer(t, nil, true)
	_, err := Probe(context.Background(), cfg(port, api.SecuritySTARTTLS), password)
	if code(t, err) != api.CodeTLSError {
		t.Fatalf("no STARTTLS: %v", err)
	}
}

func TestProbeStartTLSSelfSigned(t *testing.T) {
	srvTLS, _ := transporttest.SelfSigned(t)
	port := startServer(t, srvTLS, false)
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

func TestProbeRefused(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	_, err := Probe(context.Background(), cfg(port, api.SecurityNone), password)
	if code(t, err) != api.CodeNetworkError {
		t.Fatalf("refused: %v", err)
	}
}

func TestProbeSilentServer(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
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
	start := time.Now()
	_, err := Probe(ctx, cfg(ln.Addr().(*net.TCPAddr).Port, api.SecurityNone), password)
	if code(t, err) != api.CodeServerTimeout {
		t.Fatalf("silent: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("deadline not enforced")
	}
}

func TestProbeServerClosesAfterGreeting(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("* OK ready\r\n"))
			c.Close()
		}
	}()
	_, err := Probe(context.Background(), cfg(ln.Addr().(*net.TCPAddr).Port, api.SecurityNone), password)
	if c := code(t, err); c != api.CodeNetworkError && c != api.CodeServerError {
		t.Fatalf("closed after greeting: %v", err)
	}
}

// An oauth2 endpoint against a server without XOAUTH2 or OAUTHBEARER is
// the server's fault, not a sign-in problem, and the token is never sent.
func TestProbeOAuth2WithoutMechanism(t *testing.T) {
	port := startServer(t, nil, true)
	c := cfg(port, api.SecurityNone)
	c.AuthMethod = api.AuthOAuth2
	_, err := Probe(context.Background(), c, "ya29.token")
	if code(t, err) != api.CodeServerError || strings.Contains(err.Error(), "ya29") {
		t.Fatalf("oauth2: %v", err)
	}
}

func TestVerify(t *testing.T) {
	port := startServer(t, nil, true)
	if err := Verify(context.Background(), cfg(port, api.SecurityNone)); err != nil {
		t.Fatal(err)
	}
	if err := Verify(context.Background(), cfg(port, api.SecuritySTARTTLS)); code(t, err) != api.CodeTLSError {
		t.Fatalf("verify STARTTLS: %v", err)
	}
	_ = transport.EndpointTimeout
}
