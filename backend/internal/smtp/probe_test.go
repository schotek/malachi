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
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/schotek/malachi/backend/internal/transport/transporttest"
	"github.com/schotek/malachi/backend/pkg/api"
)

const password = "hunter2-secret"

// backend accepts one user; with auth=false it advertises no AUTH at all.
type backend struct{ auth bool }

func (b *backend) NewSession(*smtp.Conn) (smtp.Session, error) {
	if !b.auth {
		return &session{}, nil
	}
	return &authSession{}, nil
}

type session struct{}

func (*session) Reset()                               {}
func (*session) Logout() error                        { return nil }
func (*session) Mail(string, *smtp.MailOptions) error { return nil }
func (*session) Rcpt(string, *smtp.RcptOptions) error { return nil }
func (*session) Data(r io.Reader) error               { _, err := io.Copy(io.Discard, r); return err }

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
	srv := smtp.NewServer(&backend{auth: auth})
	srv.Domain = "localhost"
	srv.TLSConfig = tlsCfg
	srv.AllowInsecureAuth = insecure
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
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
