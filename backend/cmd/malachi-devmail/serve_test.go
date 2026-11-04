// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/schotek/malachi/backend/cmd/malachi-devmail/internal/imapmemserver"
)

// panics fails the test on the IMAP server's panic, which the server
// answers by closing the connection.
type panics struct {
	mu   sync.Mutex
	seen []string
}

func (p *panics) Printf(format string, args ...any) {
	if strings.HasPrefix(format, "panic") {
		p.mu.Lock()
		p.seen = append(p.seen, fmt.Sprintf(format, args...))
		p.mu.Unlock()
	}
}

// lf is b with CRLF line ends as LF.
func lf(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

// Every file of the MIME corpus, the Windows UI tests' seed, goes over
// IMAP to a client asking what the daemon's header sync asks for
// (internal/imap envelopeOptions), with a BODYSTRUCTURE for each, and
// then as the file is, but for the header's line ends: go-imap writes the
// header of BODY[] again, with CRLF (a real server ends every line so).
// go-imap's own server dropped the connection on four of them.
func TestCorpusOverIMAP(t *testing.T) {
	const corpusDir = "../../testdata/mime"
	mem := imapmemserver.New()
	box := &mailbox{users: map[string]*imapmemserver.User{}}
	u, err := box.add(mem, corpusUser)
	if err != nil {
		t.Fatal(err)
	}
	n, err := seedDirectory(u, corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	names, err := filepath.Glob(filepath.Join(corpusDir, "*.eml"))
	if err != nil || n != len(names) || n < 100 {
		t.Fatalf("%d messages seeded of %d files (%v)", n, len(names), err)
	}
	sort.Strings(names)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	log := &panics{}
	srv := newIMAPServer(mem, log)
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	defer func() {
		log.mu.Lock()
		defer log.mu.Unlock()
		for _, p := range log.seen {
			t.Errorf("the server panicked: %.300s", p)
		}
	}()

	c, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Login(corpusUser, password).Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	all := imap.UIDSet{imap.UIDRange{Start: 1, Stop: 0}}

	headers, err := c.Fetch(all, &imap.FetchOptions{
		UID: true, Flags: true, InternalDate: true, RFC822Size: true, Envelope: true,
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
		BodySection:   []*imap.FetchItemBodySection{{Specifier: imap.PartSpecifierHeader, HeaderFields: []string{"References", "List-Id"}, Peek: true}},
	}).Collect()
	if err != nil {
		t.Fatalf("header fetch: %v", err)
	}
	if len(headers) != n {
		t.Fatalf("%d header fetches of %d", len(headers), n)
	}
	for i, m := range headers {
		if m.UID == 0 || m.Envelope == nil || m.BodyStructure == nil {
			t.Errorf("%s: UID %d, envelope %v, structure %v", filepath.Base(names[i]), m.UID, m.Envelope != nil, m.BodyStructure != nil)
		}
	}

	bodies, err := c.Fetch(all, &imap.FetchOptions{
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{{Peek: true}},
	}).Collect()
	if err != nil {
		t.Fatalf("body fetch: %v", err)
	}
	if len(bodies) != n {
		t.Fatalf("%d bodies of %d", len(bodies), n)
	}
	// Appended in name order, so UID order is name order.
	sort.Slice(bodies, func(i, j int) bool { return bodies[i].UID < bodies[j].UID })
	for i, m := range bodies {
		want, err := os.ReadFile(names[i])
		if err != nil {
			t.Fatal(err)
		}
		if len(m.BodySection) != 1 || !bytes.Equal(lf(m.BodySection[0].Bytes), lf(want)) {
			t.Errorf("%s: BODY[] is not the file", filepath.Base(names[i]))
		}
	}
}
