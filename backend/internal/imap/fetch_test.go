// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"

	"github.com/schotek/malachi/backend/pkg/api"
)

// uidValidity reads a mailbox's UIDVALIDITY straight from the server.
func (h *harness) uidValidity(mailbox string) uint32 {
	h.t.Helper()
	c := h.client()
	defer func() { c.Logout().Wait() }()
	data, err := c.Select(mailbox, nil).Wait()
	if err != nil {
		h.t.Fatal(err)
	}
	return data.UIDValidity
}

// fetchInto runs FetchMessage against the harness and returns what fn got.
func (h *harness) fetchInto(ctx context.Context, loc Location) ([]byte, int64, error) {
	var got bytes.Buffer
	var size int64
	err := FetchMessage(ctx, *h.acc.Config.IMAP, password, loc, func(r io.Reader, n int64) error {
		size = n
		_, err := io.Copy(&got, r)
		return err
	})
	return got.Bytes(), size, err
}

// FetchMessage streams exactly the stored bytes and changes nothing on the
// server: EXAMINE and BODY.PEEK leave the message unread.
func TestFetchMessage(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	raw := rawMessage("f1", "Fetch me", strings.Repeat("attachment data ", 1000))
	uid := h.append("INBOX", raw, daysAgo(1), imap.FlagFlagged)
	loc := Location{Mailbox: "INBOX", UIDValidity: h.uidValidity("INBOX"), UID: uid}

	got, size, err := h.fetchInto(context.Background(), loc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != raw || size != int64(len(raw)) {
		t.Fatalf("got %d bytes (announced %d), want %d", len(got), size, len(raw))
	}
	flags := h.serverFlags("INBOX", uid)
	if hasIMAPFlag(flags, imap.FlagSeen) || !hasIMAPFlag(flags, imap.FlagFlagged) || len(flags) != 1 {
		t.Fatalf("flags after the download: %v", flags)
	}
	// Without a UIDVALIDITY to compare, the UID is taken as it is.
	loc.UIDValidity = 0
	if got, _, err := h.fetchInto(context.Background(), loc); err != nil || string(got) != raw {
		t.Fatalf("unchecked validity: %d bytes, %v", len(got), err)
	}
}

func TestFetchMessageGone(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	if err := h.user.Create("Archive", nil); err != nil {
		t.Fatal(err)
	}
	keep := h.append("INBOX", rawMessage("k", "Keep", "body"), daysAgo(1))
	gone := h.append("INBOX", rawMessage("g", "Gone", "body"), daysAgo(1))
	validity := h.uidValidity("INBOX")

	// Expunged by another client.
	c := h.client()
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(imap.UIDSetNum(imap.UID(gone)), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Expunge().Close(); err != nil {
		t.Fatal(err)
	}
	c.Logout().Wait()

	cases := []struct {
		name string
		loc  Location
	}{
		{"expunged UID", Location{Mailbox: "INBOX", UIDValidity: validity, UID: gone}},
		{"UID never used", Location{Mailbox: "INBOX", UIDValidity: validity, UID: 999}},
		{"missing mailbox", Location{Mailbox: "Deleted Folder", UIDValidity: validity, UID: keep}},
		{"another UIDVALIDITY", Location{Mailbox: "INBOX", UIDValidity: validity + 1, UID: keep}},
		{"no UID", Location{Mailbox: "INBOX", UIDValidity: validity}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			called := false
			err := FetchMessage(context.Background(), *h.acc.Config.IMAP, password, c.loc, func(io.Reader, int64) error {
				called = true
				return nil
			})
			if !errors.Is(err, ErrGone) || called {
				t.Fatalf("err %v, fn called %v", err, called)
			}
		})
	}
	// The message still there is still fetched.
	if got, _, err := h.fetchInto(context.Background(), Location{Mailbox: "INBOX", UIDValidity: validity, UID: keep}); err != nil || !strings.Contains(string(got), "Subject: Keep") {
		t.Fatalf("kept message: %v", err)
	}
}

func TestFetchMessageFailures(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	raw := rawMessage("f", "Big", strings.Repeat("0123456789abcdef", 64<<10)) // 1 MiB
	uid := h.append("INBOX", raw, daysAgo(1))
	loc := Location{Mailbox: "INBOX", UIDValidity: h.uidValidity("INBOX"), UID: uid}
	ctx := context.Background()

	if err := FetchMessage(ctx, *h.acc.Config.IMAP, "wrong", loc, func(io.Reader, int64) error { return nil }); code(t, err) != api.CodeAuthFailed {
		t.Fatalf("bad password: %v", err)
	}

	// The consumer's own failure comes back as it is, without the rest of
	// the message being read.
	boom := errors.New("disk full")
	read := 0
	err := FetchMessage(ctx, *h.acc.Config.IMAP, password, loc, func(r io.Reader, _ int64) error {
		buf := make([]byte, 4096)
		n, _ := r.Read(buf)
		read += n
		return boom
	})
	if !errors.Is(err, boom) || read == 0 || read >= len(raw) {
		t.Fatalf("consumer failure: %v after %d bytes", err, read)
	}

	// The connection breaking off under the consumer is the connection's
	// failure, whatever the consumer makes of it.
	cut := *h.acc.Config.IMAP
	cut.Port = cuttingRelay(t, h.srvURL, 256<<10)
	err = FetchMessage(ctx, cut, password, loc, func(r io.Reader, _ int64) error {
		_, err := io.Copy(io.Discard, r)
		return errors.Join(errors.New("stored nothing"), err)
	})
	if c := code(t, err); c != api.CodeNetworkError {
		t.Fatalf("outage during the download: %v", err)
	}

	// No server at all.
	h.proxy.block(true)
	if err := FetchMessage(ctx, *h.acc.Config.IMAP, password, loc, func(io.Reader, int64) error { return nil }); code(t, err) != api.CodeNetworkError {
		t.Fatalf("outage: %v", err)
	}
	h.proxy.block(false)
	if got, _, err := h.fetchInto(ctx, loc); err != nil || string(got) != raw {
		t.Fatalf("after the outage: %d bytes, %v", len(got), err)
	}

	// The context bounds the whole exchange.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := FetchMessage(cctx, *h.acc.Config.IMAP, password, loc, func(io.Reader, int64) error { return nil }); err == nil {
		t.Fatal("cancelled context: no error")
	}
}

// refusals are the NO answers a refusingSession gives in place of the
// memory server's: to EXAMINE (and SELECT) of a mailbox, and to every UID
// FETCH.
type refusals struct {
	mu      sync.Mutex
	examine map[string]*imap.Error
	fetch   *imap.Error
}

func (r *refusals) set(examine map[string]*imap.Error, fetch *imap.Error) {
	r.mu.Lock()
	r.examine, r.fetch = examine, fetch
	r.mu.Unlock()
}

// refusingSession is a memory server session that refuses as r says, like
// a server in trouble.
type refusingSession struct {
	imapserver.Session
	r *refusals
}

func (s *refusingSession) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	s.r.mu.Lock()
	e := s.r.examine[mailbox]
	s.r.mu.Unlock()
	if e != nil {
		return nil, e
	}
	return s.Session.Select(mailbox, options)
}

func (s *refusingSession) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	s.r.mu.Lock()
	e := s.r.fetch
	s.r.mu.Unlock()
	if e != nil {
		return e
	}
	return s.Session.Fetch(w, numSet, options)
}

// Move keeps the memserver's MOVE reachable (see saslSession).
func (s *refusingSession) Move(w *imapserver.MoveWriter, numSet imap.NumSet, dest string) error {
	return s.Session.(imapserver.SessionMove).Move(w, numSet, dest)
}

// Only a NO that says the message is not there makes it gone: a refusal
// for the moment is unavailable and any other NO the server's error, so
// that a server in trouble never has the user told the message was
// deleted. A NO without a code to EXAMINE is gone only when the mailbox
// list lacks the mailbox.
func TestFetchMessageRefusals(t *testing.T) {
	r := &refusals{}
	h := newHarness(t, harnessOptions{session: func(s imapserver.Session) imapserver.Session {
		return &refusingSession{Session: s, r: r}
	}})
	uid := h.append("INBOX", rawMessage("r", "Refused", "body"), daysAgo(1))
	loc := Location{Mailbox: "INBOX", UIDValidity: h.uidValidity("INBOX"), UID: uid}
	no := func(code imap.ResponseCode) *imap.Error {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Code: code, Text: "refused"}
	}
	examine := func(mailbox string, code imap.ResponseCode) map[string]*imap.Error {
		return map[string]*imap.Error{mailbox: no(code)}
	}
	const gone api.ErrorCode = -1
	cases := []struct {
		name    string
		examine map[string]*imap.Error
		fetch   *imap.Error
		loc     Location
		want    api.ErrorCode
	}{
		{"EXAMINE NONEXISTENT", examine("INBOX", imap.ResponseCodeNonExistent), nil, loc, gone},
		{"EXAMINE without a code, mailbox listed", examine("INBOX", ""), nil, loc, api.CodeServerError},
		{"EXAMINE without a code, mailbox not listed", examine("Gone", ""), nil, Location{Mailbox: "Gone", UID: uid}, gone},
		{"EXAMINE UNAVAILABLE", examine("INBOX", imap.ResponseCodeUnavailable), nil, loc, api.CodeUnavailable},
		{"EXAMINE INUSE", examine("INBOX", imap.ResponseCodeInUse), nil, loc, api.CodeUnavailable},
		{"EXAMINE LIMIT", examine("INBOX", imap.ResponseCodeLimit), nil, loc, api.CodeUnavailable},
		{"EXAMINE NOPERM", examine("INBOX", imap.ResponseCodeNoPerm), nil, loc, api.CodeServerError},
		{"EXAMINE SERVERBUG", examine("INBOX", imap.ResponseCodeServerBug), nil, loc, api.CodeServerError},
		{"UID FETCH NONEXISTENT", nil, no(imap.ResponseCodeNonExistent), loc, gone},
		{"UID FETCH EXPUNGEISSUED", nil, no(codeExpungeIssued), loc, gone},
		{"UID FETCH UNAVAILABLE", nil, no(imap.ResponseCodeUnavailable), loc, api.CodeUnavailable},
		{"UID FETCH without a code", nil, no(""), loc, api.CodeServerError},
		{"UID FETCH SERVERBUG", nil, no(imap.ResponseCodeServerBug), loc, api.CodeServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r.set(c.examine, c.fetch)
			defer r.set(nil, nil)
			called := false
			err := FetchMessage(context.Background(), *h.acc.Config.IMAP, password, c.loc, func(io.Reader, int64) error {
				called = true
				return nil
			})
			switch {
			case called:
				t.Fatalf("fn called: %v", err)
			case c.want == gone:
				if !errors.Is(err, ErrGone) {
					t.Fatalf("want gone: %v", err)
				}
			case errors.Is(err, ErrGone):
				t.Fatalf("a refusal reported as gone: %v", err)
			case code(t, err) != c.want:
				t.Fatalf("want %s: %v", c.want, err)
			}
		})
	}
	// Nothing refused: the message comes.
	if got, _, err := h.fetchInto(context.Background(), loc); err != nil || !strings.Contains(string(got), "Subject: Refused") {
		t.Fatalf("without refusals: %v", err)
	}
}

// cuttingRelay forwards connections to target, but closes each one after
// passing limit bytes from the server: a connection that breaks off in the
// middle of a message. It returns its port.
func cuttingRelay(t *testing.T, target string, limit int64) int {
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
			up, err := net.Dial("tcp", target)
			if err != nil {
				c.Close()
				continue
			}
			go func() { io.Copy(up, c); up.Close() }()
			go func() {
				io.CopyN(c, up, limit)
				c.Close()
				up.Close()
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}
