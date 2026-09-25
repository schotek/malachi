// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package smtp

import (
	"bufio"
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

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/transport/transporttest"
	"github.com/schotek/malachi/backend/pkg/api"
)

const password = "hunter2-secret"

// serverOpts configures the test server. The zero value is a plaintext
// server without AUTH that accepts every envelope and message.
type serverOpts struct {
	tls      *tls.Config
	implicit bool  // speak TLS (tls) from the first byte instead of offering STARTTLS
	insecure bool  // AllowInsecureAuth
	auth     bool  // advertise AUTH PLAIN for user "me" / password
	utf8     bool  // advertise SMTPUTF8
	maxBytes int64 // advertise SIZE and enforce it (0 = none)
	// xoauth2 makes the server advertise AUTH XOAUTH2 only, accepting
	// this token for user "me".
	xoauth2 string

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
	switch {
	case b.opts.xoauth2 != "":
		return &xoauth2Session{session: s, token: b.opts.xoauth2}, nil
	case !b.opts.auth:
		return &s, nil
	}
	return &authSession{session: s}, nil
}

// xoauth2Session takes SASL XOAUTH2 the way Gmail does: the right token
// for "me" passes; a wrong one gets a JSON report as the challenge and
// then the 535.
type xoauth2Session struct {
	session
	token string
}

func (*xoauth2Session) AuthMechanisms() []string { return []string{auth.XOAuth2} }
func (s *xoauth2Session) Auth(mech string) (sasl.Server, error) {
	if mech != auth.XOAuth2 {
		return nil, smtp.ErrAuthFailed
	}
	return &xoauth2Server{want: "user=me\x01auth=Bearer " + s.token + "\x01\x01"}, nil
}

type xoauth2Server struct {
	want   string
	failed bool
}

func (x *xoauth2Server) Next(resp []byte) (challenge []byte, done bool, err error) {
	switch {
	case x.failed:
		return nil, false, smtp.ErrAuthFailed
	case resp == nil:
		return []byte{}, false, nil
	case string(resp) == x.want:
		return nil, true, nil
	default:
		x.failed = true
		return []byte(`{"status":"400","schemes":"Bearer"}`), false, nil
	}
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
	if opts.implicit {
		srv.TLSConfig = nil
		ln = tls.NewListener(ln, opts.tls)
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

func TestProbeXOAuth2(t *testing.T) {
	ts := startServerWith(t, serverOpts{xoauth2: "ya29.good", insecure: true})
	oauth := cfg(ts.port, api.SecurityNone)
	oauth.AuthMethod = api.AuthOAuth2

	if _, err := Probe(context.Background(), oauth, "ya29.good"); err != nil {
		t.Fatalf("right token: %v", err)
	}
	_, err := Probe(context.Background(), oauth, "ya29.bad")
	if code(t, err) != api.CodeAuthFailed || strings.Contains(err.Error(), "ya29") {
		t.Fatalf("wrong token: %v", err)
	}

	// A password endpoint cannot use this server, and an oauth2 endpoint
	// cannot use a server without an OAuth2 mechanism: both are the
	// server's fault, not a sign-in problem.
	if _, err := Probe(context.Background(), cfg(ts.port, api.SecurityNone), password); code(t, err) != api.CodeServerError {
		t.Fatalf("password against XOAUTH2 only: %v", err)
	}
	plain := startServer(t, nil, true, true)
	oauth.Port = plain
	if _, err := Probe(context.Background(), oauth, "ya29.good"); code(t, err) != api.CodeServerError {
		t.Fatalf("oauth2 against PLAIN only: %v", err)
	}
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
	if d := tlsData(t, err); d.Reason != api.TLSStartTLSUnavail || d.Certificate != nil {
		t.Fatalf("no STARTTLS: %+v", d)
	}
}

func TestProbeStartTLSSelfSigned(t *testing.T) {
	srvTLS, cert := transporttest.SelfSigned(t)
	port := startServer(t, srvTLS, false, true)
	_, err := Probe(context.Background(), cfg(port, api.SecuritySTARTTLS), password)
	if d := tlsData(t, err); !certReason(d.Reason) || d.Certificate == nil || d.Certificate.SHA256 != transporttest.Fingerprint(cert) {
		t.Fatalf("self-signed STARTTLS: %+v", d)
	}
}

func TestProbeImplicitTLSSelfSigned(t *testing.T) {
	srvTLS, cert := transporttest.SelfSigned(t)
	ts := startServerWith(t, serverOpts{tls: srvTLS, implicit: true, auth: true})
	_, err := Probe(context.Background(), cfg(ts.port, api.SecurityTLS), password)
	if d := tlsData(t, err); !certReason(d.Reason) || d.Certificate == nil || d.Certificate.SHA256 != transporttest.Fingerprint(cert) {
		t.Fatalf("self-signed TLS: %+v", d)
	}
}

// A pinned certificate is accepted on both kinds of TLS and nothing else
// is; the error describes what the server presented. STARTTLS upgrades
// lazily, so the mismatch surfaces on the first EHLO over TLS.
func TestProbePinned(t *testing.T) {
	srvTLS, cert := transporttest.SelfSigned(t)
	ports := map[api.Security]int{
		api.SecurityTLS:      startServerWith(t, serverOpts{tls: srvTLS, implicit: true, auth: true}).port,
		api.SecuritySTARTTLS: startServer(t, srvTLS, false, true),
	}
	wrong := strings.Repeat("0f", 32)
	for sec, port := range ports {
		t.Run(string(sec), func(t *testing.T) {
			c := cfg(port, sec)
			c.CertificateSHA256 = transporttest.Fingerprint(cert)
			if _, err := Probe(context.Background(), c, password); err != nil {
				t.Fatalf("pinned: %v", err)
			}
			if _, err := Probe(context.Background(), c, "wrong"); code(t, err) != api.CodeAuthFailed {
				t.Fatalf("pinned, wrong password: %v", err)
			}
			c.CertificateSHA256 = wrong
			_, err := Probe(context.Background(), c, password)
			d := tlsData(t, err)
			if d.Reason != api.TLSPinMismatch || d.ExpectedSHA256 != wrong || d.Certificate == nil ||
				d.Certificate.SHA256 != transporttest.Fingerprint(cert) || !d.Certificate.SelfSigned {
				t.Fatalf("wrong pin: %+v", d)
			}
		})
	}
}

// scriptedServer answers every connection with a 220 greeting, EHLO with
// ehlo (the lines after the first), and any other command by its verb from
// replies (500 for the rest).
func scriptedServer(t *testing.T, ehlo string, replies map[string]string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				io.WriteString(c, "220 localhost ESMTP\r\n")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					verb, _, _ := strings.Cut(strings.ToUpper(strings.TrimSpace(line)), " ")
					switch reply, ok := replies[verb]; {
					case verb == "EHLO":
						io.WriteString(c, "250-localhost\r\n"+ehlo)
					case verb == "QUIT":
						io.WriteString(c, "221 bye\r\n")
						return
					case ok:
						io.WriteString(c, reply)
					default:
						io.WriteString(c, "500 what\r\n")
					}
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// A server that answers AUTH with 530 wants STARTTLS first; one that
// answers STARTTLS with 454 cannot do it now.
func TestProbeTLSRequiredAndRefused(t *testing.T) {
	port := scriptedServer(t, "250 AUTH PLAIN\r\n", map[string]string{"AUTH": "530 5.7.0 Must issue a STARTTLS command first\r\n"})
	_, err := Probe(context.Background(), cfg(port, api.SecurityNone), password)
	if d := tlsData(t, err); d.Reason != api.TLSRequired || d.Certificate != nil {
		t.Fatalf("530: %+v", d)
	}

	port = scriptedServer(t, "250-STARTTLS\r\n250 AUTH PLAIN\r\n", map[string]string{"STARTTLS": "454 4.7.0 TLS not available due to temporary reason\r\n"})
	_, err = Probe(context.Background(), cfg(port, api.SecuritySTARTTLS), password)
	if d := tlsData(t, err); d.Reason != api.TLSStartTLSUnavail || d.Certificate != nil {
		t.Fatalf("454: %+v", d)
	}
}

// tlsData asserts err is a tlsError with details and returns them.
func tlsData(t *testing.T, err error) api.TLSErrorData {
	t.Helper()
	var e *api.Error
	if code(t, err) != api.CodeTLSError || !errors.As(err, &e) {
		t.Fatalf("expected tlsError: %v", err)
	}
	d, ok := api.TLSErrorDataOf(e)
	if !ok {
		t.Fatalf("tlsError without details: %v", err)
	}
	return d
}

// certReason: a verdict on the certificate; which one depends on the
// platform's verifier (these tests use the system trust store).
func certReason(r api.TLSErrorReason) bool {
	switch r {
	case api.TLSUntrusted, api.TLSHostnameMismatch, api.TLSExpired, api.TLSNotYetValid, api.TLSInvalid, api.TLSOther:
		return true
	}
	return false
}

func TestVerify(t *testing.T) {
	port := startServer(t, nil, true, true)
	if err := Verify(context.Background(), cfg(port, api.SecurityNone)); err != nil {
		t.Fatal(err)
	}
}
