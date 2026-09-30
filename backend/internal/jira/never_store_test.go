// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"bytes"
	"crypto/rand"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	imime "github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The attachment preferences apply to the messages built from the site as
// to any mail: a file the policy leaves "on the server" stays on the site
// (message.download rebuilds the message with it), and the syncer tells
// Stored which policy a body was stored under.
func TestNeverStoreAttachments(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h := newHarness(t, mode)
		h.mu.Lock()
		h.prefs.NeverStoreAttachments = true
		h.mu.Unlock()
		f := h.f
		big := make([]byte, 200<<10)
		_, _ = rand.Read(big)
		var is *jiratest.Issue
		h.at(h.ago(1*day), func() {
			is = f.AddIssue("WEB", "Velká příloha", func(is *jiratest.Issue) { is.Description = "<p>Příloha.</p>" })
			f.AddAttachment(is.ID, "data.bin", "application/octet-stream", big)
		})
		h.mustPass()
		m := h.rows(spaceBox("10001"))["i:"+is.ID]
		if m.BodyState != store.BodyFetched || m.RawState != store.RawPartial || len(m.RemoteParts) != 1 {
			t.Fatalf("stored = body %s raw %s remote %v", m.BodyState, m.RawState, m.RemoteParts)
		}
		if len(m.Attachments) != 1 || !m.Attachments[0].Remote || m.Attachments[0].Size != int64(len(big)) {
			t.Fatalf("attachments = %+v", m.Attachments)
		}
		if raw := h.raw(m.ID); len(raw) > 64<<10 {
			t.Fatalf("the stored message holds %d bytes", len(raw))
		}
		h.mu.Lock()
		calls := append([]storedCall(nil), h.stored...)
		h.mu.Unlock()
		var told bool
		for _, c := range calls {
			if c.id == m.ID {
				told = c.pol.NeverStore
			}
		}
		if !told {
			t.Fatal("Stored was not told the policy")
		}

		// message.download gets the whole message back from the site.
		whole, err := fetchAll(t, h.supervisorFor(), h.acc, m)
		if err != nil {
			t.Fatal(err)
		}
		p, err := imime.Parse(bytes.NewReader(whole), imime.DefaultLimits())
		if err != nil || len(p.Attachments) != 1 || p.Attachments[0].Size != int64(len(big)) || p.MessageID != m.RFCMessageID {
			t.Fatalf("rebuilt: %v %+v", err, p.Attachments)
		}
		// A new view copy of it is built again, not copied from the
		// skeleton.
		h.clock.advance(10 * time.Minute)
		f.Assign(is.ID, f.Me, f.Petr)
		h.clock.advance(time.Minute)
		h.mustPass()
		v := h.rows(viewBox(api.VirtualAssignedToMe))["i:"+is.ID]
		if v.BodyState != store.BodyFetched || len(v.Attachments) != 1 || v.Attachments[0].Size != int64(len(big)) {
			t.Fatalf("view copy = %s %+v", v.BodyState, v.Attachments)
		}
	})
}
