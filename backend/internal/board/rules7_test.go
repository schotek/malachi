// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// RulesVersion 7: the verdict says where the inbound messages lie, read
// from every copy of each; the store marks a case done or live again on
// the transitions. A copy in the trash says nothing; the user's own
// messages, bulk mail and a message waiting for its classification do
// not count; an issue has no inbox.
func TestArchiveState(t *testing.T) {
	in := func(m Member, folder string, role api.FolderRole) Member {
		return with(m, func(m *Member) { m.FolderID, m.FolderRole = api.FolderID(folder), role })
	}
	archived := func(m Member) Member { return in(m, "f_archive", api.RoleArchive) }
	filed := func(m Member) Member { return in(m, "f_project", api.RoleNone) }
	trashed := func(m Member) Member { return in(m, "f_trash", api.RoleTrash) }
	copyOf := func(m Member, id string) Member { return with(m, func(m *Member) { m.ID = api.MessageID(id) }) }
	bob := inbound("a", 5, "bob@example.com", "me@example.org")
	bob2 := with(inbound("b", 2, "bob@example.com", "me@example.org"), func(m *Member) { m.InReplyTo = "<a@mail.example>" })
	cases := []struct {
		name            string
		members         []Member
		archived, inbox bool
	}{
		{"in the inbox", []Member{bob}, false, true},
		{"archived", []Member{archived(bob)}, true, false},
		{"archived, my reply in Sent", []Member{archived(bob), sent("r", 3, "Sure.", "bob@example.com")}, true, false},
		{"the older archived, the newest in the inbox", []Member{archived(bob), bob2}, false, true},
		{"the older in the inbox, the newest archived", []Member{bob, archived(bob2)}, false, false},
		{"the newest filed elsewhere", []Member{archived(bob), filed(bob2)}, false, false},
		{"a copy in the inbox and one in the archive", []Member{bob, copyOf(archived(bob), "a2")}, false, true},
		{"a copy in the trash says nothing", []Member{archived(bob), copyOf(trashed(bob), "a2")}, true, false},
		{"my messages only", []Member{sent("q", 5, "Did you send it?", "bob@example.com")}, false, false},
		{"bulk mail only", []Member{with(archived(bob), func(m *Member) { m.Bulk = "newsletter" })}, false, false},
		{"waiting for its classification", []Member{with(archived(bob), func(m *Member) { m.Bulk = BulkUnclassified })}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := Evaluate(Thread{Members: c.members}, me, now)
			if v.Archived != c.archived || v.NewestInboundInInbox != c.inbox {
				t.Fatalf("archived %v, newest in the inbox %v; want %v, %v", v.Archived, v.NewestInboundInInbox, c.archived, c.inbox)
			}
		})
	}
	// An issue's items have folder roles of their own; never archived.
	item := with(archived(bob), func(m *Member) { m.AuthorID = "bob"; m.IssueKind = api.IssueItemComment })
	jv := Evaluate(Thread{Issue: &Issue{Watching: true}, Members: []Member{item}}, NewIdentity("me", nil, nil), now)
	if jv.Archived || jv.NewestInboundInInbox {
		t.Fatalf("an issue: archived %v, in the inbox %v", jv.Archived, jv.NewestInboundInInbox)
	}
}
