// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calsync

import (
	"context"
	"testing"
)

type versionedProvider struct {
	*fakeProvider
	version int
}

func (v versionedProvider) DataVersion() int { return v.version }

var _ Versioned = versionedProvider{}

// lastToken is the token of the latest Sync of a calendar.
func lastToken(p *fakeProvider, remote string) (string, bool) {
	calls := p.calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].remoteID == remote {
			return calls[i].token, true
		}
	}
	return "", false
}

func TestProviderDataVersion(t *testing.T) {
	h := newHarness(t)
	fp := newFakeProvider(RemoteCalendar{RemoteID: "def", IsDefault: true}, RemoteCalendar{RemoteID: "other"})
	fp.deltaMode = "delta"
	a := graphAccount("acc_g", true)
	ctx := context.Background()
	h.run()

	// Version 1: recorded after the first pass.
	h.setVersioned(a.ID, versionedProvider{fp, 1})
	h.sv.Start(a)
	eventually(t, "first pass", idleWithSync(h.sv, a.ID))
	if v, ok, _ := h.st.GetMeta(ctx, MetaProviderVersion(a.ID)); !ok || v != "1" {
		t.Fatalf("meta %q %v", v, ok)
	}
	// Same version after a restart: the delta token is kept.
	h.sv.Stop(a.ID)
	n := len(fp.calls())
	h.sv.Start(a)
	eventually(t, "restart pass", func() bool { return len(fp.calls()) >= n+2 })
	if tok, _ := lastToken(fp, "def"); tok == "" {
		t.Errorf("same version dropped the token")
	}
	// Version 2: every calendar re-listed from scratch.
	h.sv.Stop(a.ID)
	h.setVersioned(a.ID, versionedProvider{fp, 2})
	n = len(fp.calls())
	h.sv.Start(a)
	eventually(t, "bumped pass", func() bool { return len(fp.calls()) >= n+2 })
	for _, c := range fp.calls()[n : n+2] {
		if c.token != "" {
			t.Errorf("%s synced with token %q after a version bump", c.remoteID, c.token)
		}
	}
	eventually(t, "meta 2", func() bool { v, _, _ := h.st.GetMeta(ctx, MetaProviderVersion(a.ID)); return v == "2" })
	eventually(t, "state restored", func() bool {
		for _, c := range calendarRows(t, h.st, a.ID) {
			if c.SyncMode != "delta" || c.SyncToken == "" {
				return false
			}
		}
		return true
	})
	// And the next pass is incremental again.
	n = len(fp.calls())
	h.sv.SyncNow(a.ID, "")
	eventually(t, "next pass", func() bool { return len(fp.calls()) >= n+2 })
	if tok, _ := lastToken(fp, "def"); tok == "" {
		t.Errorf("token not kept after the re-list")
	}
}

func TestUnversionedProviderKeepsToken(t *testing.T) {
	h := newHarness(t)
	fp := newFakeProvider(RemoteCalendar{RemoteID: "/cal/"})
	a := caldavAccount("acc_1")
	h.setProvider(a.ID, fp)
	h.run()
	h.sv.Start(a)
	eventually(t, "first pass", idleWithSync(h.sv, a.ID))
	h.sv.Stop(a.ID)
	n := len(fp.calls())
	h.sv.Start(a)
	eventually(t, "restart pass", func() bool { return len(fp.calls()) > n })
	if tok, _ := lastToken(fp, "/cal/"); tok == "" {
		t.Errorf("unversioned provider was reset")
	}
	if _, ok, _ := h.st.GetMeta(context.Background(), MetaProviderVersion(a.ID)); ok {
		t.Errorf("version recorded for an unversioned provider")
	}
}
