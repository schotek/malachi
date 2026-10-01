// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
)

func bulkMessage(id, extra, body string) string {
	return fmt.Sprintf("From: News <news@news.example>\r\nTo: me@example.test\r\nSubject: %s\r\nDate: Mon, 01 Sep 2026 11:00:00 +0000\r\n"+
		"Message-ID: <%s@example.test>\r\n%sContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", id, id, extra, body)
}

// The bulk headers come with the envelope: the row is classified, and
// keeps the headers the offer is made from, before any body is downloaded.
func TestBulkClassifiedAtHeaderSync(t *testing.T) {
	h := newHarness(t, harnessOptions{rawLimit: 256})
	big := strings.Repeat("x", 300)
	h.append("INBOX", bulkMessage("b1", "List-Unsubscribe: <https://news.example/u?x=1>, <mailto:unsub@news.example>\r\n"+
		"List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\nPrecedence: bulk\r\n", big), daysAgo(3))
	h.append("INBOX", bulkMessage("b2", "List-Id: Go Nuts <Go.Example.Org>\r\nList-Post: <mailto:go@example.org>\r\n", big), daysAgo(2))
	h.append("INBOX", bulkMessage("b3", "Auto-Submitted: auto-generated\r\n", big), daysAgo(1))
	h.append("INBOX", bulkMessage("b4", "", big), time.Now().Add(-time.Hour))
	start := time.Now()
	h.start()
	h.waitIdle(start)

	byID := map[string]store.Message{}
	for _, m := range h.messages(h.folder("inbox").ID) {
		if m.BodyState != store.BodyTooBig {
			t.Fatalf("body of %s was downloaded: %s", m.RFCMessageID, m.BodyState)
		}
		byID[m.RFCMessageID] = m
	}
	for id, want := range map[string][2]string{
		"b1@example.test": {"newsletter", ""},
		"b2@example.test": {"list", "go.example.org"},
		"b3@example.test": {"automated", ""},
		"b4@example.test": {"none", ""},
	} {
		m := byID[id]
		if m.Bulk != want[0] || m.ListID != want[1] {
			t.Errorf("%s: bulk %q list %q, want %q %q", id, m.Bulk, m.ListID, want[0], want[1])
		}
	}
	b1 := byID["b1@example.test"]
	if b1.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" || !strings.Contains(b1.Headers["List-Unsubscribe"], "https://news.example/u?x=1") {
		t.Errorf("headers: %v", b1.Headers)
	}
	if len(byID["b4@example.test"].Headers) != 0 {
		t.Errorf("headers of a personal message: %v", byID["b4@example.test"].Headers)
	}
	// The threading headers still arrive from the same fetch.
	if byID["b1@example.test"].RFCMessageID == "" {
		t.Error("no message id")
	}
}

// The notification of a new message carries the classification made at
// envelope time.
func TestNewMessageNotificationCarriesBulk(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	start := time.Now()
	h.start()
	h.waitIdle(start)
	h.append("INBOX", bulkMessage("b9", "List-Unsubscribe: <https://news.example/u>\r\n", "hello"), time.Now())
	n := h.waitNewMessage()
	if n.Message.Bulk == nil || n.Message.Bulk.Kind != "newsletter" || n.Message.Bulk.Domain != "news.example" {
		t.Fatalf("bulk = %+v", n.Message.Bulk)
	}
	h.append("INBOX", bulkMessage("b10", "", "hello"), time.Now())
	if n := h.waitNewMessage(); n.Message.Bulk != nil {
		t.Errorf("personal mail: %+v", n.Message.Bulk)
	}
}
