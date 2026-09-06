// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package smtp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-smtp"

	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	sender  = "me@example.org"
	bccRcpt = "hidden@example.net"
)

// sendErr asserts that err is a *SendError whose texts never carry the
// password, and returns it.
func sendErr(t *testing.T, err error) *SendError {
	t.Helper()
	var se *SendError
	if !errors.As(err, &se) {
		t.Fatalf("expected *SendError, got %T: %v", err, err)
	}
	if se.Err == nil {
		t.Fatal("SendError without Err")
	}
	if strings.Contains(se.Error(), password) || strings.Contains(se.Err.Message, password) {
		t.Fatalf("password leaked into %q", se.Error())
	}
	if c := code(t, err); c != se.Err.Code {
		t.Fatalf("Unwrap gives code %v, Err has %v", c, se.Err.Code)
	}
	return se
}

func expectSend(t *testing.T, err error, stage string, c api.ErrorCode, permanent bool) *SendError {
	t.Helper()
	se := sendErr(t, err)
	if se.Stage != stage || se.Err.Code != c || se.Permanent != permanent {
		t.Fatalf("got stage=%s code=%v permanent=%v (%v); want stage=%s code=%v permanent=%v",
			se.Stage, se.Err.Code, se.Permanent, se, stage, c, permanent)
	}
	return se
}

// testMessage is a built message addressed to alice only; the Bcc
// recipient exists in the envelope alone.
func testMessage(t *testing.T) []byte {
	t.Helper()
	return build(t, BuildInput{From: api.Address{Address: sender}, To: []api.Address{alice}, Subject: "test", Text: "hello\n"})
}

func deliver(ctx context.Context, port int, pw string, rcpts []string, msg []byte) error {
	return Deliver(ctx, cfg(port, api.SecurityNone), pw, sender, rcpts, bytes.NewReader(msg), int64(len(msg)))
}

func authServer(t *testing.T, extra serverOpts) *testServer {
	t.Helper()
	extra.auth = true
	extra.insecure = true
	return startServerWith(t, extra)
}

func TestDeliverSuccess(t *testing.T) {
	ts := authServer(t, serverOpts{maxBytes: 1 << 20})
	msg := testMessage(t)
	rcpts := []string{alice.Address, bccRcpt, "ALICE@example.org", alice.Address}
	if err := deliver(context.Background(), ts.port, password, rcpts, msg); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	from, got, data := ts.envelope()
	if from != sender {
		t.Fatalf("MAIL FROM = %q", from)
	}
	if strings.Join(got, ",") != alice.Address+","+bccRcpt {
		t.Fatalf("RCPT TO = %v", got)
	}
	if !bytes.Equal(data, msg) {
		t.Fatalf("data differs:\n%q\n%q", data, msg)
	}
	if bytes.Contains(data, []byte(bccRcpt)) {
		t.Fatalf("Bcc recipient in data: %q", data)
	}
}

// An oauth2 endpoint delivers with the token in the password's place; a
// refused token is an authentication failure that never names the token.
func TestDeliverXOAuth2(t *testing.T) {
	ts := startServerWith(t, serverOpts{xoauth2: "ya29.good", insecure: true})
	oauth := cfg(ts.port, api.SecurityNone)
	oauth.AuthMethod = api.AuthOAuth2
	msg := testMessage(t)
	if err := Deliver(context.Background(), oauth, "ya29.good", sender, []string{alice.Address}, bytes.NewReader(msg), int64(len(msg))); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if from, _, data := ts.envelope(); from != sender || data == nil {
		t.Fatalf("envelope = %q, data %d bytes", from, len(data))
	}
	err := Deliver(context.Background(), oauth, "ya29.bad", sender, []string{alice.Address}, bytes.NewReader(msg), int64(len(msg)))
	se := sendErr(t, err)
	if se.Stage != StageAuth || se.Err.Code != api.CodeAuthFailed || strings.Contains(se.Err.Message, "ya29") {
		t.Fatalf("wrong token: %+v", se)
	}
}

func TestDeliverRcptRejected(t *testing.T) {
	ts := authServer(t, serverOpts{rcptErr: &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "no such user " + password}})
	err := deliver(context.Background(), ts.port, password, []string{alice.Address}, testMessage(t))
	se := expectSend(t, err, StageRcpt, api.CodeServerError, true)
	if !strings.Contains(se.Err.Message, "550") || !strings.Contains(se.Err.Message, "no such user") {
		t.Fatalf("message = %q", se.Err.Message)
	}
	if _, _, data := ts.envelope(); data != nil {
		t.Fatal("data was sent after a rejected recipient")
	}
}

func TestDeliverMailRejectedTransient(t *testing.T) {
	ts := authServer(t, serverOpts{mailErr: &smtp.SMTPError{Code: 421, EnhancedCode: smtp.EnhancedCode{4, 3, 2}, Message: "try later"}})
	err := deliver(context.Background(), ts.port, password, []string{alice.Address}, testMessage(t))
	expectSend(t, err, StageMail, api.CodeServerError, false)
}

func TestDeliverDataTransient(t *testing.T) {
	ts := authServer(t, serverOpts{dataErr: &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "queue full"}})
	err := deliver(context.Background(), ts.port, password, []string{alice.Address}, testMessage(t))
	expectSend(t, err, StageData, api.CodeServerError, false)
}

func TestDeliverDataPermanent(t *testing.T) {
	ts := authServer(t, serverOpts{dataErr: &smtp.SMTPError{Code: 554, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "rejected as spam"}})
	err := deliver(context.Background(), ts.port, password, []string{alice.Address}, testMessage(t))
	expectSend(t, err, StageData, api.CodeServerError, true)
}

func TestDeliverWrongPassword(t *testing.T) {
	ts := authServer(t, serverOpts{})
	err := deliver(context.Background(), ts.port, "wrong", []string{alice.Address}, testMessage(t))
	expectSend(t, err, StageAuth, api.CodeAuthFailed, false)
	if from, _, _ := ts.envelope(); from != "" {
		t.Fatal("MAIL FROM sent without authentication")
	}
}

func TestDeliverNoAuthMechanism(t *testing.T) {
	ts := startServerWith(t, serverOpts{})
	err := deliver(context.Background(), ts.port, password, []string{alice.Address}, testMessage(t))
	expectSend(t, err, StageAuth, api.CodeServerError, true)
}

func TestDeliverRefused(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	err := deliver(context.Background(), port, password, []string{alice.Address}, testMessage(t))
	expectSend(t, err, StageConnect, api.CodeNetworkError, false)
}

func TestDeliverStartTLSNotOffered(t *testing.T) {
	ts := authServer(t, serverOpts{})
	msg := testMessage(t)
	err := Deliver(context.Background(), cfg(ts.port, api.SecuritySTARTTLS), password, sender, []string{alice.Address}, bytes.NewReader(msg), int64(len(msg)))
	expectSend(t, err, StageConnect, api.CodeTLSError, false)
}

func TestDeliverSizeTooSmall(t *testing.T) {
	ts := authServer(t, serverOpts{maxBytes: 64})
	msg := testMessage(t)
	if len(msg) <= 64 {
		t.Fatalf("test message too small: %d", len(msg))
	}
	err := deliver(context.Background(), ts.port, password, []string{alice.Address}, msg)
	se := expectSend(t, err, StageSize, api.CodeServerError, true)
	if !strings.Contains(se.Err.Message, "64") {
		t.Fatalf("message = %q", se.Err.Message)
	}
	if from, _, _ := ts.envelope(); from != "" {
		t.Fatal("MAIL FROM sent despite the SIZE limit")
	}
}

func TestDeliverTimeoutDuringData(t *testing.T) {
	ts := authServer(t, serverOpts{dataDelay: 10 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := deliver(ctx, ts.port, password, []string{alice.Address}, testMessage(t))
	if time.Since(start) > 5*time.Second {
		t.Fatalf("Deliver did not return promptly: %v", time.Since(start))
	}
	se := expectSend(t, err, StageData, api.CodeCancelled, false)
	if strings.Contains(se.Error(), password) {
		t.Fatalf("password in %q", se.Error())
	}
}

func TestDeliverCancelledBeforeConnect(t *testing.T) {
	ts := authServer(t, serverOpts{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := deliver(ctx, ts.port, password, []string{alice.Address}, testMessage(t))
	expectSend(t, err, StageConnect, api.CodeCancelled, false)
}

func TestDeliverSMTPUTF8(t *testing.T) {
	msg := testMessage(t)
	utf8Rcpt := "jiří@example.cz"

	ts := authServer(t, serverOpts{})
	err := deliver(context.Background(), ts.port, password, []string{utf8Rcpt}, msg)
	expectSend(t, err, StageMail, api.CodeServerError, true)

	ts = authServer(t, serverOpts{utf8: true})
	if err := deliver(context.Background(), ts.port, password, []string{alice.Address, utf8Rcpt}, msg); err != nil {
		t.Fatalf("Deliver with SMTPUTF8: %v", err)
	}
	if _, rcpts, _ := ts.envelope(); len(rcpts) != 2 || rcpts[1] != utf8Rcpt {
		t.Fatalf("RCPT TO = %v", rcpts)
	}
}

func TestDeliverEnvelopeInjection(t *testing.T) {
	ts := authServer(t, serverOpts{})
	msg := testMessage(t)
	for _, bad := range []string{"a@example.org\r\nRCPT TO:<x@y>", "a@example.org>", "a b@example.org", "", "nodomain", "a@b@c"} {
		err := deliver(context.Background(), ts.port, password, []string{alice.Address, bad}, msg)
		expectSend(t, err, StageRcpt, api.CodeInvalidArgument, true)
	}
	err := Deliver(context.Background(), cfg(ts.port, api.SecurityNone), password, "me@example.org\nX", []string{alice.Address}, bytes.NewReader(msg), int64(len(msg)))
	expectSend(t, err, StageMail, api.CodeInvalidArgument, true)
	err = deliver(context.Background(), ts.port, password, nil, msg)
	expectSend(t, err, StageRcpt, api.CodeInvalidArgument, true)
	if from, _, _ := ts.envelope(); from != "" {
		t.Fatal("server was contacted with an invalid envelope")
	}
}

func TestDeliverReaderError(t *testing.T) {
	ts := authServer(t, serverOpts{})
	broken := io.MultiReader(strings.NewReader("Subject: x\r\n\r\npartial"), &failReader{err: errors.New("disk gone")})
	err := Deliver(context.Background(), cfg(ts.port, api.SecurityNone), password, sender, []string{alice.Address}, broken, 100)
	se := expectSend(t, err, StageData, api.CodeStorageError, false)
	if !strings.Contains(se.Err.Message, "disk gone") {
		t.Fatalf("message = %q", se.Err.Message)
	}
	// Give the server a moment to notice the dropped connection; a
	// truncated message must never have been accepted.
	time.Sleep(50 * time.Millisecond)
	if _, _, data := ts.envelope(); data != nil {
		t.Fatalf("truncated message delivered: %q", data)
	}
}

// An oauth2 endpoint against a server that offers no OAuth2 mechanism is
// the server's fault, at the authentication stage.
func TestDeliverOAuth2WithoutMechanism(t *testing.T) {
	ts := authServer(t, serverOpts{})
	c := cfg(ts.port, api.SecurityNone)
	c.AuthMethod = api.AuthOAuth2
	msg := testMessage(t)
	err := Deliver(context.Background(), c, "ya29.token", sender, []string{alice.Address}, bytes.NewReader(msg), int64(len(msg)))
	se := sendErr(t, err)
	if se.Stage != StageAuth || se.Err.Code != api.CodeServerError {
		t.Fatalf("got %+v", se)
	}
}
