// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// A literal the batch no longer wants (the store failed on the first body)
// is still read to its end. When the connection breaks under it the batch
// ends at once with the connection's error and does not ask the command
// for more: the library's reader resumed when the read failed, and its
// discard of the rest of the literal would read the connection alongside
// it (a data race under go test -race).
func TestFetchBodyBatchDrainBreaks(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	ctx := context.Background()
	body := strings.Repeat("0123456789abcdef", 16<<10) // 256 KiB each
	var rows []*store.Message
	folders, _, err := h.st.UpsertFolders(ctx, h.acc.ID, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	inbox := folders[0]
	for i := range 3 {
		uid := h.append("INBOX", rawMessage(fmt.Sprintf("d%d", i), "Drained", body), daysAgo(1))
		rows = append(rows, &store.Message{AccountID: h.acc.ID, FolderID: inbox.ID, UID: uid, Subject: "Drained", Size: int64(len(body))})
	}
	if err := h.st.UpsertMessages(ctx, rows); err != nil {
		t.Fatal(err)
	}
	refs, err := h.st.ListUnfetched(ctx, inbox.ID, 10)
	if err != nil || len(refs) != 3 {
		t.Fatalf("unfetched %+v %v", refs, err)
	}
	// Nothing can be staged: every body fails in the store. A file where
	// the staging directory was fails the staging on any system, for root
	// too; a read-only directory would not on Windows, which ignores the
	// read-only attribute of a directory when files are created in it.
	staging := filepath.Join(filepath.Dir(h.st.Path()), "staging")
	if err := os.Remove(staging); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staging, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Remove(staging)
		os.Mkdir(staging, 0o700)
	})

	// The connection breaks in the middle of the second message.
	cut := *h.acc.Config.IMAP
	cut.Port = cuttingRelay(t, h.srvURL, 400<<10)
	sess, err := openSession(ctx, cut, password, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, err := sess.selectMailbox(ctx, "INBOX"); err != nil {
		t.Fatal(err)
	}
	err = h.syncer.fetchBodyBatch(ctx, sess, inbox, refs, 0)
	if c := code(t, err); c != api.CodeNetworkError {
		t.Fatalf("batch over a broken connection: %v", err)
	}
	for _, r := range refs {
		if m, err := h.st.GetMessage(ctx, h.acc.ID, r.ID); err != nil || m.BodyState != store.BodyNone {
			t.Errorf("message %d: %+v %v", r.UID, m.BodyState, err)
		}
	}
}

// drainLiteral reports a literal that ends before its announced size (the
// library's reader says EOF where the connection closed) and a read that
// fails, which the syncer must not follow with another read.
func TestDrainLiteral(t *testing.T) {
	broken := errors.New("connection reset")
	for name, c := range map[string]struct {
		lit  fakeLiteral
		want error
	}{
		"whole":         {fakeLiteral{r: strings.NewReader("12345"), size: 5}, nil},
		"short":         {fakeLiteral{r: strings.NewReader("123"), size: 5}, io.ErrUnexpectedEOF},
		"a failed read": {fakeLiteral{r: io.MultiReader(strings.NewReader("12"), errorReader{broken}), size: 5}, broken},
	} {
		if err := drainLiteral(c.lit); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

// fakeLiteral is an imap.LiteralReader over r announcing size.
type fakeLiteral struct {
	r    io.Reader
	size int64
}

func (l fakeLiteral) Read(p []byte) (int, error) { return l.r.Read(p) }
func (l fakeLiteral) Size() int64                { return l.size }

type errorReader struct{ err error }

func (e errorReader) Read([]byte) (int, error) { return 0, e.err }
