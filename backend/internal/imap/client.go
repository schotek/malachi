// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
	"github.com/emersion/go-sasl"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Budgets and limits of the sync engine. The library has no context
// support, so every command runs under a watchdog that closes the socket
// when its budget is spent (session.do).
const (
	connectTimeout   = 15 * time.Second // dial + greeting + STARTTLS + login
	commandTimeout   = 60 * time.Second // one command exchange (LIST, SELECT, envelope batch, STORE…)
	bodyBatchTimeout = 5 * time.Minute  // one UID FETCH BODY.PEEK[] batch
	idleRecheck      = 25 * time.Minute // leave IDLE, NOOP, re-enter (dead-socket guard)

	backoffMin    = 5 * time.Second // first reconnect delay after a network failure
	backoffMax    = 5 * time.Minute // cap of the exponential backoff
	backoffJitter = 0.2             // ±20 % randomisation of every delay

	maxRawMessageBytes = 25 << 20 // larger messages are never downloaded (bodyState tooBig)
	envelopeBatch      = 200      // messages per UID FETCH of envelopes
	bodyBatchMessages  = 20       // messages per UID FETCH of bodies
	bodyBatchBytes     = 8 << 20  // announced bytes per body batch
	flagBatch          = 2000     // UIDs per UID FETCH (FLAGS)
	maxFolders         = 5000     // LIST entries kept (hostile server guard)
	maxOpAttempts      = 8        // pushes of one local operation before it is dropped
	opBackoffMin       = 30 * time.Second
	opBackoffMax       = time.Hour

	// maxSearchResults bounds a UID SEARCH result before it is expanded into
	// memory; a server answering with a wider range is refused.
	maxSearchResults = 2_000_000
	// maxAddresses bounds one address list taken from an envelope.
	maxAddresses = 200
	// maxFieldBytes caps header-derived strings (subject, names, ids).
	maxFieldBytes = 2048
	// maxAttachmentsPerMessage bounds what BODYSTRUCTURE may contribute.
	maxAttachmentsPerMessage = 200
)

// Conn is an open, not yet authenticated client whose connection is closed
// when the context that opened it ends.
type Conn struct {
	*imapclient.Client
	raw  net.Conn
	stop func() bool
}

// Close releases the connection and the watchdog.
func (c *Conn) Close() error {
	c.stop()
	return c.Client.Close()
}

// connect dials, secures the connection according to cfg.Security and reads
// the greeting; opts (nil allowed) are the client options, TLSConfig is
// always the transport policy. It never authenticates and never logs
// traffic. Errors are *api.Error. The caller must Close the result while
// ctx is still alive.
func connect(ctx context.Context, cfg api.ServerConfig, opts *imapclient.Options) (*Conn, time.Duration, error) {
	start := time.Now()
	raw, err := transport.DialContext(ctx, cfg.Host, cfg.Port, cfg.Security)
	if err != nil {
		return nil, 0, err
	}
	// The library has no context support and a 30 s internal read timeout;
	// closing the socket when ctx ends is what enforces our budget.
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	if opts == nil {
		opts = &imapclient.Options{}
	}
	opts.TLSConfig = transport.TLSConfig(cfg.Host)

	var c *imapclient.Client
	if cfg.Security == api.SecuritySTARTTLS {
		c, err = imapclient.NewStartTLS(raw, opts)
		if err != nil {
			stop()
			return nil, 0, classify(ctx, transport.StageTLS, err)
		}
	} else {
		c = imapclient.New(raw, opts)
		if err := c.WaitGreeting(); err != nil {
			c.Close()
			stop()
			if strings.Contains(err.Error(), "before greeting") {
				// The library reports a socket closed before any greeting as
				// a plain string; that is a network failure, not a protocol one.
				err = fmt.Errorf("%w: %v", io.ErrUnexpectedEOF, err)
			}
			return nil, 0, classify(ctx, transport.StageGreeting, err)
		}
	}
	return &Conn{Client: c, raw: raw, stop: stop}, time.Since(start), nil
}

// login authenticates with the password (SASL PLAIN when offered, LOGIN
// otherwise). Errors are *api.Error; the password never appears in them.
func login(ctx context.Context, c *Conn, cfg api.ServerConfig, password string) error {
	if cfg.AuthMethod != api.AuthPassword {
		return api.ErrNotImplemented
	}
	caps := c.Caps()
	var err error
	switch {
	case caps.Has(imap.AuthCap(sasl.Plain)):
		err = c.Authenticate(sasl.NewPlainClient("", cfg.Username, password))
	case !caps.Has(imap.CapLoginDisabled):
		err = c.Login(cfg.Username, password).Wait()
	default:
		return api.NewError(api.CodeServerError, "server offers no usable authentication mechanism")
	}
	if err != nil {
		return classify(ctx, transport.StageAuth, err)
	}
	return nil
}

// session is one authenticated connection of a syncer. Unilateral server
// data (EXISTS, EXPUNGE, FETCH) is reduced to a non-blocking "something
// changed" signal on events; the handlers never block the decoder.
type session struct {
	*Conn
	caps     imap.CapSet
	events   chan struct{}
	selected string // mailbox currently selected, "" when none
	// noSince is set once the server rejected SEARCH SINCE (some refuse the
	// quoted date go-imap sends); the window is then applied client-side.
	noSince bool
}

// openSession connects and logs in under connectTimeout. capFilter (nil
// allowed) may hide capabilities, e.g. IDLE for tests.
func openSession(ctx context.Context, cfg api.ServerConfig, password string, capFilter func(imap.CapSet) imap.CapSet) (*session, error) {
	s := &session{events: make(chan struct{}, 1)}
	opts := &imapclient.Options{
		WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader},
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Expunge: func(uint32) { s.signal() },
			Mailbox: func(*imapclient.UnilateralDataMailbox) { s.signal() },
			Fetch: func(msg *imapclient.FetchMessageData) {
				// Runs in its own goroutine; the items must be consumed so
				// the decoder can continue.
				for item := msg.Next(); item != nil; item = msg.Next() {
					if bs, ok := item.(imapclient.FetchItemDataBodySection); ok && bs.Literal != nil {
						io.Copy(io.Discard, bs.Literal)
					}
				}
				s.signal()
			},
		},
	}

	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	c, _, err := connect(cctx, cfg, opts)
	if err != nil {
		return nil, err
	}
	if err := login(cctx, c, cfg, password); err != nil {
		c.Close()
		return nil, err
	}
	// The connect watchdog belongs to the short cctx; re-arm it on ctx so
	// the socket closes when the syncer stops.
	c.stop()
	c.stop = context.AfterFunc(ctx, func() { c.raw.Close() })
	s.Conn = c
	caps := c.Caps()
	if capFilter != nil {
		caps = capFilter(caps)
	}
	if caps == nil {
		caps = imap.CapSet{}
	}
	s.caps = caps
	return s, nil
}

func (s *session) signal() {
	select {
	case s.events <- struct{}{}:
	default:
	}
}

// drainEvents forgets signals that arrived so far (they are about to be
// covered by a pass).
func (s *session) drainEvents() {
	select {
	case <-s.events:
	default:
	}
}

// do runs one command exchange under a budget: when d elapses (or ctx
// ends) the socket is closed, which makes the library return an error.
// The result is classified: an *imap.Error (NO/BAD) keeps the session
// usable, anything else means the connection is gone.
func (s *session) do(ctx context.Context, d time.Duration, fn func() error) error {
	bctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	stop := context.AfterFunc(bctx, func() { s.raw.Close() })
	err := fn()
	if !stop() {
		// The watchdog fired: the socket is closed whatever fn reported.
		if err == nil {
			err = bctx.Err()
		}
		s.selected = ""
	}
	if err == nil {
		return nil
	}
	classified := classify(bctx, transport.StageCommand, err)
	var ie *imap.Error
	var ae *api.Error
	if errors.As(err, &ie) && errors.As(classified, &ae) {
		return &commandError{api: ae, imap: ie}
	}
	return classified
}

// commandError is a classified NO/BAD/BYE: it unwraps to both the
// *api.Error (for the state machine) and the *imap.Error (so callers can
// tell a per-command refusal from a dead connection).
type commandError struct {
	api  *api.Error
	imap *imap.Error
}

func (e *commandError) Error() string   { return e.api.Error() }
func (e *commandError) Unwrap() []error { return []error{e.api, e.imap} }

// selectMailbox issues SELECT unless the mailbox is already selected.
func (s *session) selectMailbox(ctx context.Context, mailbox string) (*imap.SelectData, error) {
	var data *imap.SelectData
	err := s.do(ctx, commandTimeout, func() error {
		var err error
		data, err = s.Select(mailbox, nil).Wait()
		return err
	})
	if err != nil {
		s.selected = ""
		return nil, err
	}
	s.selected = mailbox
	return data, nil
}

// logout says goodbye within transport.QuitTimeout and closes the socket.
func (s *session) logout() {
	done := make(chan struct{})
	go func() { _ = s.Logout().Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(transport.QuitTimeout):
	}
	s.Close()
}

// isStatusError reports whether err is a NO/BAD/BYE the server sent for
// one command, i.e. the connection itself is still fine.
func isStatusError(err error) bool {
	var ie *imap.Error
	return errors.As(err, &ie)
}

// errorCode extracts the contract code of a classified error.
func errorCode(err error) api.ErrorCode {
	var e *api.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// uidsFromSet expands a server-supplied UID set with a size guard: a
// dynamic range ("1:*") or more than maxSearchResults members is refused
// instead of allocated.
func uidsFromSet(set imap.UIDSet) ([]uint32, error) {
	var total uint64
	for _, r := range set {
		if r.Start == 0 || r.Stop == 0 || r.Stop < r.Start {
			return nil, api.NewError(api.CodeServerError, "search result is not a static UID set")
		}
		total += uint64(r.Stop-r.Start) + 1
		if total > maxSearchResults {
			return nil, api.NewError(api.CodeServerError, "search result exceeds %d messages", maxSearchResults)
		}
	}
	uids, ok := set.Nums()
	if !ok {
		return nil, api.NewError(api.CodeServerError, "search result is not a static UID set")
	}
	out := make([]uint32, len(uids))
	for i, u := range uids {
		out[i] = uint32(u)
	}
	return out, nil
}

// uidSet builds a UID set from local UIDs.
func uidSet(uids []uint32) imap.UIDSet {
	var set imap.UIDSet
	for _, u := range uids {
		if u != 0 {
			set.AddNum(imap.UID(u))
		}
	}
	return set
}
