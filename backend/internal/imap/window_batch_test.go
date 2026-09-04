// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"fmt"
	"testing"
	"time"
)

// TestClientSideWindowAcrossBatches covers a folder larger than one
// INTERNALDATE batch when the window is applied client-side: every recent
// message is kept whichever batch it lands in.
func TestClientSideWindowAcrossBatches(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.syncer.deps.NoSinceSearch = true
	const n = flagBatch + 500
	want := 0
	for i := 0; i < n; i++ {
		at := daysAgo(60)
		if i%3 == 0 {
			at = daysAgo(2)
			want++
		}
		h.append("INBOX", rawMessage(fmt.Sprintf("b%d", i), fmt.Sprintf("msg %d", i), "body"), at)
	}
	start := time.Now()
	h.start()
	h.waitIdle(start)
	inbox := h.folder("inbox")
	if inbox.Total != want {
		t.Fatalf("kept %d messages, want %d", inbox.Total, want)
	}
}

// TestUIDBatchesBounded checks that every batch honours both the count and
// the encoded-size limits and that the batches cover the input in order.
func TestUIDBatchesBounded(t *testing.T) {
	var uids []uint32
	u := uint32(1)
	for len(uids) < 5000 {
		uids = append(uids, u)
		if u%7 == 0 {
			u += 10 // a gap: forces a new range
		} else {
			u++
		}
	}
	batches := uidBatches(uids, 2000, 300)
	var joined []uint32
	for _, b := range batches {
		if len(b) == 0 || len(b) > 2000 {
			t.Fatalf("batch size %d", len(b))
		}
		if enc := uidSet(b).String(); len(enc) > 300 {
			t.Fatalf("encoded batch %d bytes: %s", len(enc), enc)
		}
		joined = append(joined, b...)
	}
	if len(joined) != len(uids) {
		t.Fatalf("batches cover %d of %d", len(joined), len(uids))
	}
	for i := range uids {
		if joined[i] != uids[i] {
			t.Fatalf("order broken at %d", i)
		}
	}
	if got := len(uidBatches(nil, 10, 10)); got != 0 {
		t.Fatalf("empty input gave %d batches", got)
	}
}
