// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// setMIME replaces the MIME content the service returns for a message.
func (f *fakeGraph) setMIME(id, raw string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages[id].mime = raw
}

// withAttachment is a message with one attachment of n bytes, the
// Message-ID the fake's listing reports for subject.
func withAttachment(subject string, n int) string {
	data := bytes.Repeat([]byte{0x5a, 0x01, 0xc3, 0x7f}, n/4)
	enc := base64.StdEncoding.EncodeToString(data)
	var body strings.Builder
	for len(enc) > 76 {
		body.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	body.WriteString(enc)
	return strings.Join([]string{
		"From: alice@example.test",
		"To: me@contoso.invalid",
		"Subject: " + subject,
		"Message-ID: <" + strings.ReplaceAll(subject, " ", "") + "@example.test>",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="b"`,
		"",
		"--b",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"the numbers",
		"--b",
		"Content-Type: application/pdf",
		`Content-Disposition: attachment; filename="report.pdf"`,
		"Content-Transfer-Encoding: base64",
		"",
		body.String(),
		"--b--",
		"",
	}, "\r\n")
}

// fetchClient is a client of the fake service with a counter of token
// invalidations.
func fetchClient(fake *fakeGraph, invalidated *atomic.Int32) *Client {
	return NewClient(Options{
		BaseURL: fake.srv.URL,
		Token: func(context.Context) (string, error) {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			return fake.token, nil
		},
		Invalidate: func() { invalidated.Add(1) },
		Sleep:      func(context.Context, time.Duration) error { return nil },
	})
}

func TestFetchMessage(t *testing.T) {
	fake := newFakeGraph(t)
	id := fake.add("F-INBOX", "Report", "alice@example.test", daysAgo(1), "body")
	raw := withAttachment("Report", 300<<10)
	fake.setMIME(id, raw)
	var invalidated atomic.Int32
	c := fetchClient(fake, &invalidated)
	ctx := context.Background()

	fetch := func() (string, error) {
		var got bytes.Buffer
		err := FetchMessage(ctx, c, id, func(r io.Reader, size int64) error {
			if size != -1 {
				t.Errorf("size %d announced", size)
			}
			_, err := io.Copy(&got, r)
			return err
		})
		return got.String(), err
	}
	if got, err := fetch(); err != nil || got != raw {
		t.Fatalf("fetch: %d bytes, %v", len(got), err)
	}
	if n := fake.count("GET", "/me/messages/"+id+"/$value"); n != 1 {
		t.Fatalf("$value requests: %d", n)
	}

	// A token the service rejects is asked for again once.
	fake.mu.Lock()
	fake.unauthorized = 1
	fake.mu.Unlock()
	if got, err := fetch(); err != nil || got != raw || invalidated.Load() != 1 {
		t.Fatalf("after a 401: %d bytes, %v, %d invalidations", len(got), err, invalidated.Load())
	}
	// A 503 is retried; more than the client's retries is serverTimeout.
	fake.mu.Lock()
	fake.failValue[id] = throttleRetries
	fake.mu.Unlock()
	if got, err := fetch(); err != nil || got != raw {
		t.Fatalf("after 503s: %v", err)
	}
	fake.mu.Lock()
	fake.failValue[id] = throttleRetries + 1
	fake.mu.Unlock()
	if _, err := fetch(); code(t, err) != api.CodeServerTimeout {
		t.Fatalf("persistent 503: %v", err)
	}

	// The consumer's failure comes back as it is.
	boom := errors.New("disk full")
	if err := FetchMessage(ctx, c, id, func(io.Reader, int64) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("consumer failure: %v", err)
	}

	fake.remove(id)
	if _, err := fetch(); !errors.Is(err, ErrGone) {
		t.Fatalf("deleted message: %v", err)
	}
	if err := FetchMessage(ctx, c, "", func(io.Reader, int64) error { return nil }); !errors.Is(err, ErrGone) {
		t.Fatalf("no id: %v", err)
	}
}

// A body stored under a policy that keeps no large attachment loses its
// attachment at ingest; the size stored is what was downloaded, not what
// the listing said.
func TestSyncStripsAttachmentsAtIngest(t *testing.T) {
	h := newHarness(t, SyncPrefs{OfflineDays: 30, AttachmentOfflineDays: api.AttachmentOfflineNone})
	id := h.fake.add("F-INBOX", "Report", "alice@example.test", daysAgo(3), "body")
	raw := withAttachment("Report", 300<<10)
	h.fake.setMIME(id, raw)
	draft := h.fake.add("F-DRAFTS", "Draft report", "me@contoso.invalid", daysAgo(3), "body")
	h.fake.setMIME(draft, withAttachment("Draft report", 300<<10))
	start := time.Now()
	h.start()
	h.waitIdle(start)

	m, ok := h.byRemote(id)
	if !ok || m.BodyState != store.BodyFetched || m.RawState != store.RawPartial || m.Size != int64(len(raw)) ||
		len(m.Attachments) != 1 || !m.Attachments[0].Remote || m.Attachments[0].Size != 300<<10 {
		t.Fatalf("message = %+v (ok=%v)", m, ok)
	}
	r, err := h.st.OpenMessageRaw(context.Background(), h.acc.ID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(r)
	r.Close()
	if len(stored) > 4<<10 || !strings.Contains(string(stored), "the numbers") {
		t.Fatalf("stored %d bytes", len(stored))
	}
	if d, ok := h.byRemote(draft); !ok || d.RawState != store.RawFull || d.Attachments[0].Remote {
		t.Fatalf("draft = %+v", d)
	}
}
