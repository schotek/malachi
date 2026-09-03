// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/transport/transporttest"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestTLSConfigPolicy(t *testing.T) {
	c := TLSConfig("imap.example.invalid")
	if c.MinVersion != tls.VersionTLS12 || c.ServerName != "imap.example.invalid" || c.InsecureSkipVerify {
		t.Fatalf("policy = %+v", c)
	}
}

func TestDialTLSVerifies(t *testing.T) {
	srvCfg, cert := transporttest.SelfSigned(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", srvCfg)
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
			go func() { io.Copy(io.Discard, c); c.Close() }()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)

	_, err = DialContext(context.Background(), "127.0.0.1", addr.Port, api.SecurityTLS)
	if code(t, err) != api.CodeTLSError {
		t.Fatalf("self-signed: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(cert)
	rootCAs = pool
	defer func() { rootCAs = nil }()
	conn, err := DialContext(context.Background(), "127.0.0.1", addr.Port, api.SecurityTLS)
	if err != nil {
		t.Fatalf("trusted: %v", err)
	}
	if _, ok := conn.(*tls.Conn); !ok {
		t.Fatalf("conn is %T", conn)
	}
	conn.Close()
}

func TestDialErrors(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	_, err := DialContext(context.Background(), "127.0.0.1", port, api.SecurityNone)
	if code(t, err) != api.CodeNetworkError {
		t.Fatalf("refused: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = DialContext(ctx, "127.0.0.1", port, api.SecurityNone)
	if code(t, err) != api.CodeCancelled {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestClassify(t *testing.T) {
	bg := context.Background()
	expired, cancel := context.WithTimeout(bg, time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)

	cases := []struct {
		name  string
		ctx   context.Context
		stage Stage
		err   error
		want  api.ErrorCode
	}{
		{"api error passes", bg, StageAuth, api.NewError(api.CodeAuthFailed, "x"), api.CodeAuthFailed},
		{"deadline", bg, StageGreeting, context.DeadlineExceeded, api.CodeServerTimeout},
		{"ctx deadline after close", expired, StageCommand, net.ErrClosed, api.CodeServerTimeout},
		{"net timeout", bg, StageDial, &net.OpError{Op: "dial", Err: timeoutErr{}}, api.CodeServerTimeout},
		{"dns", bg, StageDial, &net.DNSError{Err: "no such host", Name: "x.invalid"}, api.CodeNetworkError},
		{"refused", bg, StageDial, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, api.CodeNetworkError},
		{"unknown authority", bg, StageDial, x509.UnknownAuthorityError{}, api.CodeTLSError},
		{"tls alert", bg, StageCommand, tls.AlertError(42), api.CodeTLSError},
		{"tls stage anything", bg, StageTLS, errors.New("server doesn't support STARTTLS"), api.CodeTLSError},
		{"eof after connect", bg, StageAuth, io.EOF, api.CodeNetworkError},
		{"reset", bg, StageCommand, syscall.ECONNRESET, api.CodeNetworkError},
		{"protocol garbage", bg, StageGreeting, errors.New("unexpected token"), api.CodeServerError},
		{"cancelled", bg, StageGreeting, context.Canceled, api.CodeCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.ctx, tc.stage, tc.err)
			if got.Code != tc.want {
				t.Fatalf("code = %v (%s), want %v", got.Code, got.Message, tc.want)
			}
		})
	}
	if Classify(bg, StageDial, nil) != nil {
		t.Fatal("nil error must classify to nil")
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestCleanMessage(t *testing.T) {
	long := strings.Repeat("ž", 400)
	got := CleanMessage("  bad\x00\r\nthing " + long)
	if strings.ContainsAny(got, "\x00\r\n") || len(got) > MaxMessageBytes+len("…") || !strings.HasPrefix(got, "bad") {
		t.Fatalf("got %q (%d bytes)", got, len(got))
	}
	if CleanMessage("\xff\xfeok") != "ok" {
		t.Fatal("invalid UTF-8 not dropped")
	}
}

func TestCleanCapabilities(t *testing.T) {
	in := []string{"IMAP4rev1", "IDLE", "IDLE", "", " padded", "ctrl\x01", "ünïcode", strings.Repeat("A", 1<<20)}
	for i := 0; i < 10000; i++ {
		in = append(in, "X"+strings.Repeat("Y", i%70))
	}
	out := CleanCapabilities(in)
	if len(out) > MaxCapabilities {
		t.Fatalf("len = %d", len(out))
	}
	for i, s := range out {
		if len(s) > MaxCapabilityLen || !printableASCII(s) || s == "" {
			t.Fatalf("entry %q", s)
		}
		if i > 0 && out[i-1] >= s {
			t.Fatalf("not sorted/deduped at %d: %q %q", i, out[i-1], s)
		}
	}
	small := CleanCapabilities([]string{"IDLE", "IMAP4rev1", "IDLE"})
	if len(small) != 2 || small[0] != "IDLE" || small[1] != "IMAP4rev1" {
		t.Fatalf("small = %v", small)
	}
}

func TestValidHost(t *testing.T) {
	for h, ok := range map[string]bool{
		"imap.example.org": true, "192.0.2.1": true, "::1": true, "localhost": true, "a.b.": true,
		"": false, "-bad.example": false, "imap example": false, "a..b": false, strings.Repeat("a", 300): false,
	} {
		if ValidHost(h) != ok {
			t.Errorf("ValidHost(%q) = %v", h, !ok)
		}
	}
	if !IsLoopbackHost("LocalHost") || !IsLoopbackHost("127.0.0.1") || !IsLoopbackHost("::1") || IsLoopbackHost("imap.example.org") {
		t.Fatal("loopback detection")
	}
}

func code(t *testing.T, err error) api.ErrorCode {
	t.Helper()
	var e *api.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *api.Error, got %T: %v", err, err)
	}
	return e.Code
}
