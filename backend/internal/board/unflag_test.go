// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package board

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func flaggedIDs(members []Member) []api.MessageID {
	return FlaggedCopies(Thread{Members: members}, me, now)
}

// The copies board.unflag clears: every flagged copy of a message that
// counts (or waits for its classification), wherever it is filed, and
// nothing in the trash, junk or drafts, hidden, virtual, or of a message
// that does not count — also when a copy pretends otherwise.
func TestFlaggedCopies(t *testing.T) {
	flag := func(m Member) Member { m.Flagged = true; return m }
	a := inbound("a", 5, "bob@example.com", "me@example.org")
	aArchive := with(flag(a), func(m *Member) {
		m.ID, m.FolderID, m.FolderRole = "a2", "f_archive", api.RoleArchive
		m.MessageID = "  <A@MAIL.example>  " // the same Message-ID, as normMessageID reads it
		m.StoredAt = m.StoredAt.Add(1)
	})
	aTrash := with(flag(a), func(m *Member) { m.ID, m.FolderID, m.FolderRole = "a3", "f_trash", api.RoleTrash })
	aJunk := with(flag(a), func(m *Member) { m.ID, m.FolderID, m.FolderRole = "a4", "f_junk", api.RoleJunk })
	aDrafts := with(flag(a), func(m *Member) { m.ID, m.FolderID, m.FolderRole = "a5", "f_drafts", api.RoleDrafts })
	aHidden := with(flag(a), func(m *Member) { m.ID, m.Hidden = "a6", true })
	aVirtual := with(flag(a), func(m *Member) { m.ID, m.Virtual = "a7", true })
	news := with(flag(inbound("n", 4, "news@example.com", "me@example.org")), func(m *Member) { m.Bulk = "newsletter" })
	// A forged twin of a newsletter: stored later, claims to be personal
	// and is flagged. The copy stored first represents the message, which
	// therefore does not count.
	twin := with(flag(news), func(m *Member) { m.ID, m.Bulk, m.StoredAt = "n2", BulkNone, m.StoredAt.Add(1) })
	pending := with(flag(inbound("p", 3, "carol@example.com", "me@example.org")), func(m *Member) { m.Bulk = BulkUnclassified })
	mine := flag(sent("s", 2, "ok", "bob@example.com"))
	noID1 := with(flag(inbound("x", 1, "bob@example.com", "me@example.org")), func(m *Member) { m.MessageID = "" })
	noID2 := with(flag(inbound("y", 1, "bob@example.com", "me@example.org")), func(m *Member) { m.MessageID = "" })

	members := []Member{a, aArchive, aTrash, aJunk, aDrafts, aHidden, aVirtual, news, twin, pending, mine, noID1, noID2}
	got := flaggedIDs(members)
	want := []api.MessageID{"a2", "p", "s", "x", "y"}
	if !slices.Equal(got, want) {
		t.Fatalf("flagged copies = %v, want %v", got, want)
	}
	if got := flaggedIDs([]Member{a, news, twin, aTrash}); len(got) != 0 {
		t.Fatalf("nothing that counts is flagged, got %v", got)
	}
	if got := flaggedIDs(nil); len(got) != 0 {
		t.Fatalf("empty thread: %v", got)
	}

	// Jira: every item but events counts, whatever its bulk field says.
	item := with(flag(inbound("c", 2, "alice@users.jira.invalid", "me@example.org")), func(m *Member) { m.Bulk, m.IssueKind = "", api.IssueItemComment })
	event := with(flag(inbound("e", 1, "alice@users.jira.invalid")), func(m *Member) { m.IssueKind = api.IssueItemEvent })
	jt := Thread{Members: []Member{item, event}, Issue: &Issue{}}
	if got := FlaggedCopies(jt, me, now); !slices.Equal(got, []api.MessageID{"c"}) {
		t.Fatalf("jira flagged copies = %v", got)
	}
}

// Whatever the thread, clearing the flags FlaggedCopies lists leaves no
// hot.flagged verdict, and it only ever lists flagged members the rules
// can see.
func TestFlaggedCopiesClearTheStar(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	roles := []api.FolderRole{api.RoleInbox, api.RoleArchive, api.RoleSent, api.RoleTrash, api.RoleJunk, api.RoleDrafts, ""}
	bulks := []string{BulkNone, BulkNone, BulkUnclassified, "newsletter", "list"}
	ids := []string{"<m1@x>", "<M1@x>", "<m2@x>", "", "<m3@x>"}
	for i := range 3000 {
		n := 1 + r.IntN(8)
		var members []Member
		for j := range n {
			m := inbound(string(rune('a'+j)), 1+r.IntN(50), "bob@example.com", "me@example.org")
			m.MessageID = ids[r.IntN(len(ids))]
			m.FolderRole = roles[r.IntN(len(roles))]
			m.Mine = m.FolderRole == api.RoleSent
			m.Bulk = bulks[r.IntN(len(bulks))]
			m.Flagged = r.IntN(2) == 0
			m.Hidden = r.IntN(10) == 0
			m.Virtual = r.IntN(10) == 0
			members = append(members, m)
		}
		got := flaggedIDs(members)
		for _, id := range got {
			k := slices.IndexFunc(members, func(m Member) bool { return m.ID == id })
			m := members[k]
			if !m.Flagged || m.Hidden || m.Virtual || m.FolderRole == api.RoleTrash || m.FolderRole == api.RoleJunk || m.FolderRole == api.RoleDrafts {
				t.Fatalf("case %d: listed %+v", i, m)
			}
		}
		cleared := slices.Clone(members)
		for k := range cleared {
			if slices.Contains(got, cleared[k].ID) {
				cleared[k].Flagged = false
			}
		}
		// Classify what waited as personal: the star must not come back.
		for k := range cleared {
			if cleared[k].Bulk == BulkUnclassified {
				cleared[k].Bulk = BulkNone
			}
		}
		if v := Evaluate(Thread{Members: cleared}, me, now); v.Reason == api.BoardReasonHotFlagged {
			t.Fatalf("case %d: still hot.flagged after clearing %v of %+v", i, got, members)
		}
	}
}
