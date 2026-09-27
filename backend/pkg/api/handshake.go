// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

// HandshakeReason classifies a failed handshake (HandshakeError).
type HandshakeReason int

const (
	// HandshakeProtocolMismatch: the daemon speaks another protocol version
	// (HandshakeError.Daemon; 1 for a daemon that does not know
	// system.hello). The key file was not read and nothing was sent after
	// system.hello.
	HandshakeProtocolMismatch HandshakeReason = iota + 1

	// HandshakeKeyUnavailable: the key file is missing, unreadable or not a
	// key file (HandshakeError.Err wraps ErrKeyUnavailable and the cause).
	HandshakeKeyUnavailable

	// HandshakeDaemonUnproven: whatever answers on the socket did not prove
	// that it holds the key of the key file: another process, or a daemon
	// of another run. Nothing was sent after system.hello.
	HandshakeDaemonUnproven

	// HandshakeRejected: the daemon answered a handshake call with an error
	// (HandshakeError.Code; unauthenticated for a proof it refused).
	HandshakeRejected

	// HandshakeMalformed: an answer broke the protocol.
	HandshakeMalformed

	// HandshakeTimedOut: no answer within HandshakeTimeout or before the
	// context's deadline.
	HandshakeTimedOut
)

// String returns the reason's stable symbolic name.
func (r HandshakeReason) String() string {
	switch r {
	case HandshakeProtocolMismatch:
		return "protocolMismatch"
	case HandshakeKeyUnavailable:
		return "keyUnavailable"
	case HandshakeDaemonUnproven:
		return "daemonUnproven"
	case HandshakeRejected:
		return "rejected"
	case HandshakeMalformed:
		return "malformed"
	case HandshakeTimedOut:
		return "timedOut"
	}
	return fmt.Sprintf("unknown(%d)", int(r))
}

// HandshakeError is how ClientHandshake reports a daemon it cannot or must
// not use. Neither its text nor its cause ever contains the key, a nonce or
// a proof.
type HandshakeError struct {
	Reason HandshakeReason
	Daemon int       // HandshakeProtocolMismatch: the daemon's protocol version
	Code   ErrorCode // HandshakeRejected: the daemon's error code; its message is dropped
	Err    error     // the cause, if any
}

func (e *HandshakeError) Error() string {
	switch e.Reason {
	case HandshakeProtocolMismatch:
		return fmt.Sprintf("malachid speaks protocol version %d, this client %d", e.Daemon, ProtocolVersion)
	case HandshakeKeyUnavailable:
		if e.Err == nil {
			return "cannot use malachid's connection key"
		}
		return fmt.Sprintf("cannot use malachid's connection key: %v", e.Err)
	case HandshakeDaemonUnproven:
		return "the process on the socket did not prove it holds malachid's connection key"
	case HandshakeRejected:
		return fmt.Sprintf("malachid rejected the handshake (%s)", e.Code)
	case HandshakeMalformed:
		if e.Err == nil {
			return "malformed handshake answer from malachid"
		}
		return "malformed handshake answer from malachid: " + e.Err.Error()
	case HandshakeTimedOut:
		return "malachid did not complete the handshake in time"
	}
	return fmt.Sprintf("rpc handshake failed (%s)", e.Reason)
}

func (e *HandshakeError) Unwrap() error { return e.Err }

const (
	// maxHandshakeLine caps one line from the daemon during the handshake.
	// The answers are a few hundred bytes; a protocol-1 notification is
	// larger but far below this.
	maxHandshakeLine = 64 << 10

	// maxHandshakeNotifications is how many notifications ClientHandshake
	// skips before the system.hello answer. A daemon of protocol 1
	// broadcasts them to every connection, and its methodNotFound answer
	// must still be reached to report the mismatch. A daemon of protocol 2
	// sends none before the system.authenticate answer.
	maxHandshakeNotifications = 8
)

// ClientHandshake authenticates a new connection as the daemon's client
// (docs/api.md §1.4) and must be the first thing done on it. It sends
// system.hello, checks the protocol version, reads the key file at keyPath
// (afresh, and only once the daemon has answered in the current protocol),
// verifies the daemon's proof, then sends system.authenticate and waits for
// its answer. On a nil error the connection is ready for calls.
//
// r is the caller's reader over conn, which the caller keeps reading from:
// the handshake reads through r only and never past the newline of the
// last answer, so whatever the daemon sent after it stays in r.
//
// The exchange is bounded by HandshakeTimeout and by ctx through conn's
// deadline, which is cleared on success. A failure is a *HandshakeError,
// ctx.Err() when ctx was cancelled, or a plain error when the connection
// broke (to be handled like any dropped connection); after any error the
// caller closes conn. ClientHandshake never logs.
func ClientHandshake(ctx context.Context, conn net.Conn, r *bufio.Reader, keyPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(HandshakeTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("rpc handshake: set deadline: %w", err)
	}
	// A deadline in the past makes the pending read or write return at once.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	h := &clientHandshake{ctx: ctx, conn: conn, r: r}
	if err := h.run(keyPath); err != nil {
		stop()
		return err
	}
	if !stop() {
		// ctx ended after the last answer; its callback moves the deadline.
		return ctx.Err()
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("rpc handshake: clear deadline: %w", err)
	}
	return nil
}

type clientHandshake struct {
	ctx  context.Context
	conn net.Conn
	r    *bufio.Reader
}

func (h *clientHandshake) run(keyPath string) error {
	clientNonce := NewAuthNonce()
	if err := h.send(1, MethodSystemHello, SystemHelloParams{ClientNonce: clientNonce.Hex()}); err != nil {
		return err
	}
	res, rpcErr, err := h.answer(1, MethodSystemHello, maxHandshakeNotifications)
	switch {
	case err != nil:
		return err
	case rpcErr != nil && rpcErr.Code == CodeMethodNotFound:
		// A daemon of protocol 1 does not know system.hello.
		return &HandshakeError{Reason: HandshakeProtocolMismatch, Daemon: 1}
	case rpcErr != nil:
		return &HandshakeError{Reason: HandshakeRejected, Code: rpcErr.Code}
	}

	// protocolVersion first and on its own: it is the one member of the
	// result that every protocol version keeps, whatever the others are.
	var version struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if json.Unmarshal(res, &version) != nil || version.ProtocolVersion <= 0 {
		return malformed("the system.hello result has no valid protocolVersion")
	}
	if version.ProtocolVersion != ProtocolVersion {
		return &HandshakeError{Reason: HandshakeProtocolMismatch, Daemon: version.ProtocolVersion}
	}
	var hello SystemHelloResult
	if json.Unmarshal(res, &hello) != nil {
		return malformed("the system.hello result does not decode")
	}
	daemonNonce, ok := ParseAuthHex(hello.DaemonNonce)
	if !ok {
		return malformed("daemonNonce is not 64 lowercase hex digits")
	}
	daemonProof, ok := ParseAuthHex(hello.DaemonProof)
	if !ok {
		return malformed("daemonProof is not 64 lowercase hex digits")
	}

	key, err := ReadKeyFile(keyPath)
	if err != nil {
		return &HandshakeError{Reason: HandshakeKeyUnavailable, Err: err}
	}
	if !DaemonProof(key, clientNonce, daemonNonce).Equal(daemonProof) {
		return &HandshakeError{Reason: HandshakeDaemonUnproven}
	}

	proof := ClientProof(key, clientNonce, daemonNonce)
	if err := h.send(2, MethodSystemAuthenticate, SystemAuthenticateParams{ClientProof: proof.Hex()}); err != nil {
		return err
	}
	res, rpcErr, err = h.answer(2, MethodSystemAuthenticate, 0)
	switch {
	case err != nil:
		return err
	case rpcErr != nil:
		return &HandshakeError{Reason: HandshakeRejected, Code: rpcErr.Code}
	}
	var done SystemAuthenticateResult
	if json.Unmarshal(res, &done) != nil {
		return malformed("the system.authenticate result is not an object")
	}
	return nil
}

// send writes one request line in a single Write.
func (h *clientHandshake) send(id int, method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("rpc handshake: encode %s: %w", method, err)
	}
	line, err := json.Marshal(Request{JSONRPC: JSONRPCVersion, ID: json.RawMessage(strconv.Itoa(id)), Method: method, Params: raw})
	if err != nil {
		return fmt.Errorf("rpc handshake: encode %s: %w", method, err)
	}
	if _, err := h.conn.Write(append(line, '\n')); err != nil {
		return h.ioError("send "+method, err)
	}
	return nil
}

// handshakeLine is one line from the daemon during the handshake.
type handshakeLine struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Result  json.RawMessage `json:"result"`
	Error   *Error          `json:"error"`
}

// answer reads the daemon's answer to the request with this id and method,
// skipping at most skip notifications before it, and returns its result or
// its error.
func (h *clientHandshake) answer(id int, method string, skip int) (json.RawMessage, *Error, error) {
	want := strconv.Itoa(id)
	for {
		line, err := h.readLine("read the " + method + " answer")
		if err != nil {
			return nil, nil, err
		}
		var m handshakeLine
		if json.Unmarshal(line, &m) != nil || m.JSONRPC != JSONRPCVersion {
			return nil, nil, malformed("a line is not a JSON-RPC 2.0 message")
		}
		if (len(m.ID) == 0 || string(m.ID) == "null") && m.Method != "" {
			if skip == 0 {
				return nil, nil, malformed("an unexpected notification")
			}
			skip--
			continue
		}
		switch {
		case m.Method != "":
			return nil, nil, malformed("a request instead of an answer")
		case string(m.ID) != want:
			return nil, nil, malformed("an answer without the request's id")
		case m.Error != nil && m.Result != nil:
			return nil, nil, malformed("an answer with both a result and an error")
		case m.Error != nil:
			return nil, m.Error, nil
		case len(m.Result) == 0 || string(m.Result) == "null":
			return nil, nil, malformed("an answer without a result")
		}
		return m.Result, nil, nil
	}
}

// readLine returns the next line from the daemon, newline included, at
// most maxHandshakeLine bytes. ReadSlice leaves everything after the
// newline in r.
func (h *clientHandshake) readLine(op string) ([]byte, error) {
	var line []byte
	for {
		frag, err := h.r.ReadSlice('\n')
		if len(line)+len(frag) > maxHandshakeLine {
			return nil, malformed("a line is longer than 64 KiB")
		}
		line = append(line, frag...)
		if err == nil {
			return line, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, h.ioError(op, err)
		}
	}
}

// ioError classifies a failed read or write: ctx's cancellation, the
// deadline (HandshakeTimedOut), or a broken connection (a plain error).
func (h *clientHandshake) ioError(op string, err error) error {
	if cerr := h.ctx.Err(); errors.Is(cerr, context.Canceled) {
		return cerr
	}
	var ne net.Error
	if errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return &HandshakeError{Reason: HandshakeTimedOut, Err: err}
	}
	return fmt.Errorf("rpc handshake: %s: %w", op, err)
}

// malformed reports an answer that broke the protocol. detail is fixed
// text: it never quotes the answer.
func malformed(detail string) *HandshakeError {
	return &HandshakeError{Reason: HandshakeMalformed, Err: errors.New(detail)}
}
