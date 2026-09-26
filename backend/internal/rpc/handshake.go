// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package rpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The daemon's side of the connection handshake (docs/api.md §1.4): the
// state machine that answers system.hello and system.authenticate, the
// byte budget and the log limiter for connections that are not
// authenticated yet, and the key file.

// secretKey holds this run's key where fmt cannot reach it. Printing a
// struct prints the bytes of an unexported api.AuthKey field, because fmt
// cannot call the key's own redaction there, and with an invalid verb (%s)
// even the bytes behind a pointer; a func prints as its address whatever
// the verb, and nothing shows what a closure holds.
type secretKey func() api.AuthKey

func newSecretKey(k api.AuthKey) secretKey { return func() api.AuthKey { return k } }

// hsState is how far a connection has come in the handshake.
type hsState int

const (
	hsNew           hsState = iota // system.hello is due
	hsHelloAnswered                // system.authenticate is due
	hsAuthenticated                // the connection is usable
	hsFailed                       // the connection is to be closed
)

// Why a handshake failed. The daemon's log shows these, and a 1005 answer
// carries them; they are fixed text and never quote what the peer sent.
const (
	failNotRequest      = "not a JSON-RPC request with an id"
	failVersion         = "not a JSON-RPC 2.0 request"
	failBeforeHello     = "a call before system.hello"
	failSecondHello     = "a second system.hello"
	failNotAuthenticate = "a call other than system.authenticate after system.hello"
	failNonce           = "clientNonce is not 64 lowercase hex digits"
	failProofFormat     = "clientProof is not 64 lowercase hex digits"
	failWrongProof      = "wrong clientProof"
	failInternal        = "internal error"
	failOver            = "the handshake is over"
	failTimeout         = "not authenticated in time"
)

// failBudget is the reason for a connection that used up its budget before
// it authenticated: it sent more than the budget, or all of it with the
// handshake still unfinished.
var failBudget = fmt.Sprintf("the %d-byte budget before authentication is used up", api.MaxHandshakeBytes)

// hsStep is what the connection does with one line of the handshake.
type hsStep struct {
	// reply is the answer to write, nil for none.
	reply *api.Response
	// hello: reply answers system.hello. The key file must hold this run's
	// key before reply is written, because the client reads the file next.
	hello bool
	// authed: reply answers system.authenticate, and the connection is
	// authenticated once it is written.
	authed bool
	// fail is non-empty when the handshake failed, for this reason: the
	// connection is closed after reply, if any, is written.
	fail string
}

// handshake is one connection's handshake: a state machine fed one line at
// a time that does no I/O of its own (FuzzHandshakeStep).
type handshake struct {
	key      secretKey
	newNonce func() api.AuthNonce
	state    hsState
	cn, dn   api.AuthNonce // the client's and the daemon's nonce, once hello is answered
}

// step handles one line from the peer, which the caller has checked is not
// empty. Only system.hello and then system.authenticate, each with a valid
// argument, get a result; any other request with an id gets error 1005 with
// that id, anything else no answer, and both end the handshake.
func (h *handshake) step(line []byte) hsStep {
	if h.state == hsAuthenticated || h.state == hsFailed {
		return hsStep{fail: failOver} // the caller has stopped feeding it
	}
	// A map, not a struct: JSON-RPC member names match exactly, where
	// encoding/json would match struct fields regardless of case.
	var msg map[string]json.RawMessage
	if json.Unmarshal(line, &msg) != nil || msg == nil {
		return h.failed(nil, failNotRequest) // not JSON, or not an object
	}
	id := msg["id"]
	if len(id) == 0 || string(id) == "null" {
		return h.failed(nil, failNotRequest)
	}
	var version, method string
	if json.Unmarshal(msg["jsonrpc"], &version) != nil || version != api.JSONRPCVersion {
		return h.failed(id, failVersion)
	}
	if json.Unmarshal(msg["method"], &method) != nil {
		method = "" // not a string: no method this state accepts
	}
	if h.state == hsNew {
		return h.hello(id, method, msg["params"])
	}
	return h.authenticate(id, method, msg["params"])
}

// hello answers the connection's first line, which must be system.hello.
func (h *handshake) hello(id json.RawMessage, method string, params json.RawMessage) hsStep {
	if method != api.MethodSystemHello {
		return h.failed(id, failBeforeHello)
	}
	var p api.SystemHelloParams
	if json.Unmarshal(params, &p) != nil {
		return h.failed(id, failNonce)
	}
	cn, ok := api.ParseAuthHex(p.ClientNonce)
	if !ok {
		return h.failed(id, failNonce)
	}
	h.cn, h.dn = cn, h.newNonce()
	res, err := json.Marshal(api.SystemHelloResult{
		ProtocolVersion: api.ProtocolVersion,
		DaemonNonce:     h.dn.Hex(),
		DaemonProof:     api.DaemonProof(h.key(), h.cn, h.dn).Hex(),
	})
	if err != nil {
		return h.failed(id, failInternal)
	}
	h.state = hsHelloAnswered
	return hsStep{reply: result(id, res), hello: true}
}

// authenticate answers the line after the hello answer, which must be
// system.authenticate with the client's proof for this connection's nonces.
func (h *handshake) authenticate(id json.RawMessage, method string, params json.RawMessage) hsStep {
	switch method {
	case api.MethodSystemAuthenticate:
	case api.MethodSystemHello:
		return h.failed(id, failSecondHello)
	default:
		return h.failed(id, failNotAuthenticate)
	}
	var p api.SystemAuthenticateParams
	if json.Unmarshal(params, &p) != nil {
		return h.failed(id, failProofFormat)
	}
	proof, ok := api.ParseAuthHex(p.ClientProof)
	if !ok {
		return h.failed(id, failProofFormat)
	}
	if !api.ClientProof(h.key(), h.cn, h.dn).Equal(proof) {
		return h.failed(id, failWrongProof)
	}
	h.state = hsAuthenticated
	return hsStep{reply: result(id, json.RawMessage(`{}`)), authed: true}
}

// failed ends the handshake for reason. A request with an id gets error
// 1005 with that id; without one there is no answer.
func (h *handshake) failed(id json.RawMessage, reason string) hsStep {
	h.state = hsFailed
	st := hsStep{fail: reason}
	if id != nil {
		st.reply = &api.Response{
			JSONRPC: api.JSONRPCVersion,
			ID:      id,
			Error:   api.NewError(api.CodeUnauthenticated, "the connection is not authenticated: %s", reason),
		}
	}
	return st
}

func result(id, res json.RawMessage) *api.Response {
	return &api.Response{JSONRPC: api.JSONRPCVersion, ID: id, Result: res}
}

// errHandshakeBudget stops the reading of a connection that sent more than
// api.MaxHandshakeBytes before it authenticated.
var errHandshakeBudget = errors.New("rpc: handshake byte budget exceeded")

// handshakeLimit reads a connection for its handshake: at most
// api.MaxHandshakeBytes, all lines together and blank ones included, until
// lift is called once the connection has authenticated. Only the
// connection's read loop uses it.
type handshakeLimit struct {
	r      io.Reader
	left   int  // bytes that may still be read before authentication
	lifted bool // authenticated: no limit any more
	read   bool // a byte was read: the peer is more than a probe
}

func newHandshakeLimit(r io.Reader) *handshakeLimit {
	return &handshakeLimit{r: r, left: api.MaxHandshakeBytes}
}

func (l *handshakeLimit) Read(p []byte) (int, error) {
	if l.lifted {
		return l.r.Read(p)
	}
	if l.left <= 0 {
		return 0, errHandshakeBudget
	}
	if len(p) > l.left {
		p = p[:l.left]
	}
	n, err := l.r.Read(p)
	l.left -= n
	if n > 0 {
		l.read = true
	}
	return n, err
}

// lift removes the budget: the connection has authenticated.
func (l *handshakeLimit) lift() { l.lifted = true }

// split is bufio.ScanLines, except that before authentication a line
// counts only with its newline: a fragment left when the connection ended,
// timed out or ran out of budget is dropped, not handled as a line.
func (l *handshakeLimit) split(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && !l.lifted && bytes.IndexByte(data, '\n') < 0 {
		return 0, nil, nil
	}
	return bufio.ScanLines(data, atEOF)
}

// warnEvery is how often one kind of handshake trouble is logged at Warn: a
// peer that fails in a loop must not flood the log.
const warnEvery = 10 * time.Second

// logLimiter lets one event per warnEvery through and counts the ones it
// holds back.
type logLimiter struct {
	mu         sync.Mutex
	now        func() time.Time // nil: time.Now; tests set a clock
	last       time.Time
	suppressed int
}

// allow reports whether an event may be logged at Warn now and, if so, how
// many were held back since the last one that was.
func (l *logLimiter) allow() (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.now != nil {
		now = l.now()
	}
	if !l.last.IsZero() && now.Sub(l.last) < warnEvery {
		l.suppressed++
		return false, 0
	}
	n := l.suppressed
	l.last, l.suppressed = now, 0
	return true, n
}

// warnLimited logs msg at Warn when l allows it, with the number of events
// held back since the last time ("suppressed"); held back ones go to Debug.
// Callers pass fixed text only: never the key, a nonce, a proof or anything
// the peer sent.
func warnLimited(log *slog.Logger, l *logLimiter, msg string, args ...any) {
	ok, held := l.allow()
	if !ok {
		log.Debug(msg, args...)
		return
	}
	if held > 0 {
		args = append(args[:len(args):len(args)], "suppressed", held)
	}
	log.Warn(msg, args...)
}

// fileRetryWaits are the pauses of retryFileOp between its attempts: ten
// attempts over about 1.3 s. A variable so that tests can shorten it.
var fileRetryWaits = []time.Duration{
	20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond, 160 * time.Millisecond,
	200 * time.Millisecond, 200 * time.Millisecond, 200 * time.Millisecond, 200 * time.Millisecond,
	200 * time.Millisecond,
}

// retryFileOp runs op until it succeeds, fails because the file does not
// exist, or has failed len(fileRetryWaits)+1 times. Windows refuses to
// open, replace or remove a file in a way that conflicts with another
// process's open handle of it (os.Open does not share deletion): a client
// reading the key file holds up its replacement for a moment.
func retryFileOp(op func() error) error {
	err := op()
	for _, wait := range fileRetryWaits {
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return err
		}
		time.Sleep(wait)
		err = op()
	}
	return err
}

// keyShaped reports whether b could be a key file or a torn one: nothing
// but lowercase hex digits and newlines.
func keyShaped(b []byte) bool {
	for _, c := range b {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || c == '\n') {
			return false
		}
	}
	return true
}

// keyReplaceable returns nil when the daemon may write its key file at
// path: nothing is there, or a key-shaped file is, a regular file of at
// most api.KeyFileSize bytes holding only lowercase hex digits and
// newlines (the key file of an earlier run, or a torn one). Anything else
// may be someone's data and stays; the error says why, never what the file
// holds.
func keyReplaceable(path string) error {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !fi.Mode().IsRegular():
		return errors.New("not a regular file")
	case fi.Size() > api.KeyFileSize:
		return errors.New("larger than a key file")
	}
	var f *os.File
	err = retryFileOp(func() error {
		var err error
		f, err = openNoWait(path)
		return err
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil // removed meanwhile
	}
	if err != nil {
		return err
	}
	defer f.Close()
	// Judge the file that was opened, which is what gets replaced even if
	// the path changed since Lstat: a rename replaces the directory entry,
	// never the target of a link.
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, api.KeyFileSize+1))
	switch {
	case err != nil:
		return err
	case len(b) > api.KeyFileSize:
		return errors.New("larger than a key file")
	case !keyShaped(b):
		return errors.New("not a key file")
	}
	return nil
}

// openNoWait opens path for keyReplaceable. O_NONBLOCK: when the path has
// turned into a named pipe since Lstat, open(2) must not wait for a writer
// (forever, perhaps, and under keyMu, which every later system.hello and
// Close need); the Stat after the open refuses the pipe. Windows ignores
// the flag. A variable so that tests can change the path between Lstat and
// the open.
var openNoWait = func(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

// writeKeyFile writes key to path atomically: a temporary file beside it
// (mode 0600) is written, synced and closed, then renamed over path, with
// retries because Windows cannot rename over a file that a client is
// reading. On every error the temporary file is removed. The caller has
// checked path with keyReplaceable.
func writeKeyFile(path string, key api.AuthKey) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create key file: %w", err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	b := api.FormatKeyFile(key)
	_, err = f.Write(b)
	clear(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("write key file %s: %w", tmp, err)
	}
	if err = retryFileOp(func() error { return os.Rename(tmp, path) }); err != nil {
		return fmt.Errorf("install key file: %w", err)
	}
	return nil
}

// ensureKeyFile runs before every system.hello answer: clients read the key
// file next, so it must hold this run's key. The file is written again when
// it is missing or holds another key (someone removed it, or another
// daemon starting at the same moment wrote its own), under the rule of
// Listen: a file that is not key-shaped is left alone. Once the key is
// retired (Close) nothing is written.
func (s *Server) ensureKeyFile() {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if s.keyRetired || s.key == nil {
		return
	}
	if k, err := api.ReadKeyFile(s.keyPath); err == nil && k.Equal(s.key()) {
		return
	}
	if err := keyReplaceable(s.keyPath); err != nil {
		warnLimited(s.log, &s.keyLog, "the key file is not this daemon's and not a key file; left alone",
			"path", s.keyPath, "err", err)
		return
	}
	if err := writeKeyFile(s.keyPath, s.key()); err != nil {
		warnLimited(s.log, &s.keyLog, "cannot restore the key file", "path", s.keyPath, "err", err)
		return
	}
	warnLimited(s.log, &s.keyLog, "key file restored: it was missing or held another key", "path", s.keyPath)
}

// retireKeyFile ends the key's life at shutdown: its file is never written
// again, and it is removed only while it still holds this run's key, never
// a key another daemon wrote since.
func (s *Server) retireKeyFile() {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if s.keyRetired {
		return
	}
	s.keyRetired = true
	if s.key == nil {
		return // Listen did not get as far
	}
	k, err := api.ReadKeyFile(s.keyPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return
	case err != nil:
		s.log.Warn("key file left in place: it cannot be read as a key file", "path", s.keyPath, "err", err)
		return
	case !k.Equal(s.key()):
		s.log.Info("key file left in place: it holds another daemon's key", "path", s.keyPath)
		return
	}
	if err := retryFileOp(func() error { return os.Remove(s.keyPath) }); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.log.Warn("cannot remove the key file", "path", s.keyPath, "err", err)
	}
}
