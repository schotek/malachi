// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// advance moves the syncer's clock.
func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	h.offset += d
	h.mu.Unlock()
}

// TestRemovedThenPresentSurvives: Graph may report an item as removed and
// later, in the same stream, as present again (moved out and back). The last
// report wins, so the row must stay.
func TestRemovedThenPresentSurvives(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	x := h.fake.add("F-PROJ", "Out and back", "bob@example.test", daysAgo(1), "x")
	start := time.Now()
	h.start()
	h.waitIdle(start)
	if _, ok := h.byRemote(x); !ok {
		t.Fatal("setup: message missing")
	}

	// The filler pushes the two reports onto different pages.
	h.fake.add("F-PROJ", "Filler", "bob@example.test", daysAgo(1), "f")
	h.fake.reportRemoved("F-PROJ", x)
	h.fake.setRead(x, true)
	h.pass(api.FolderID(h.folder("F-PROJ").ID), false)

	m, ok := h.byRemote(x)
	if !ok {
		t.Fatal("message reported removed and then present was deleted")
	}
	if !hasFlag(m.Flags, api.FlagSeen) {
		t.Errorf("the later report was not applied: %+v", m.Flags)
	}
}

// TestPresentThenRemovedGoesQuietly is the inverse: the last report is the
// removal, so the row goes and nobody is told of new mail.
func TestPresentThenRemovedGoesQuietly(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	h.fake.add("F-PROJ", "Anchor", "bob@example.test", daysAgo(2), "a")
	start := time.Now()
	h.start()
	h.waitIdle(start)

	z := h.fake.add("F-PROJ", "Here and gone", "bob@example.test", daysAgo(1), "z")
	h.fake.reportRemoved("F-PROJ", z)
	h.pass(api.FolderID(h.folder("F-PROJ").ID), false)

	if _, ok := h.byRemote(z); ok {
		t.Fatal("message reported present and then removed was kept")
	}
	for _, n := range h.notes.newMessages() {
		if n.Message.Subject == "Here and gone" {
			t.Fatalf("removed message announced as new: %+v", n)
		}
	}
}

// TestInboxCountCheckRepairsMissedMessage: a message the delta stream never
// reports is found by the count check and the next pass enumerates the
// inbox; the check is asked at most every ten minutes.
func TestInboxCountCheckRepairsMissedMessage(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	h.fake.add("F-INBOX", "One", "alice@example.test", daysAgo(1), "1")
	h.fake.add("F-INBOX", "Two", "alice@example.test", daysAgo(2), "2")
	start := time.Now()
	h.start()
	h.waitIdle(start)
	enumerated := h.fake.enumerated("F-INBOX")

	missed := h.fake.add("F-INBOX", "Missed", "alice@example.test", daysAgo(1), "m")
	h.fake.blind(missed)
	h.pass("", false)
	if n := h.fake.countQueried(); n != 1 {
		t.Fatalf("count requests = %d, want 1", n)
	}
	waitFor(t, "the repairing enumeration", func() bool {
		_, ok := h.byRemote(missed)
		return ok && h.fake.enumerated("F-INBOX") > enumerated
	})

	// Within ten minutes the check is not asked again, however many passes.
	h.pass("", false)
	h.pass("", false)
	if n := h.fake.countQueried(); n != 1 {
		t.Fatalf("count requests within ten minutes = %d, want 1", n)
	}
	enumerated = h.fake.enumerated("F-INBOX")

	// Later it is, and the counts agree now: nothing is enumerated.
	h.advance(11 * time.Minute)
	h.pass("", false)
	if n := h.fake.countQueried(); n != 2 {
		t.Fatalf("count requests after ten minutes = %d, want 2", n)
	}
	if n := h.fake.enumerated("F-INBOX"); n != enumerated {
		t.Fatalf("matching counts caused an enumeration: %d, want %d", n, enumerated)
	}
}

// TestInboxCountCheckDoesNotLoop: a count that no enumeration can
// reconcile forces at most one enumeration an hour.
func TestInboxCountCheckDoesNotLoop(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	h.fake.add("F-INBOX", "One", "alice@example.test", daysAgo(1), "1")
	start := time.Now()
	h.start()
	h.waitIdle(start)
	enumerated := h.fake.enumerated("F-INBOX")

	h.fake.skewCounts(1)
	h.pass("", false)
	waitFor(t, "the forced enumeration", func() bool { return h.fake.enumerated("F-INBOX") == enumerated+1 })
	h.pass("", false) // settles the background pass

	// The mismatch is still there ten minutes later: no second enumeration.
	h.advance(11 * time.Minute)
	checks := h.fake.countQueried()
	h.pass("", false)
	if h.fake.countQueried() <= checks {
		t.Fatal("the check was not made")
	}
	h.pass("", false)
	if n := h.fake.enumerated("F-INBOX"); n != enumerated+1 {
		t.Fatalf("enumerations = %d, want %d: forced again within the hour", n, enumerated+1)
	}

	// An hour after the first one it may.
	h.advance(time.Hour)
	h.pass("", false)
	waitFor(t, "the second forced enumeration", func() bool { return h.fake.enumerated("F-INBOX") == enumerated+2 })
}
