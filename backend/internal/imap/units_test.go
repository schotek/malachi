// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestFlagsRoundTrip(t *testing.T) {
	in := []imap.Flag{imap.FlagSeen, "\\SEEN", imap.FlagFlagged, "$junk", "$Forwarded", "$Label1", imap.FlagDeleted}
	got := toAPIFlags(in)
	want := []api.Flag{api.FlagDeleted, api.FlagFlagged, api.FlagForwarded, api.FlagJunk, api.FlagSeen}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("toAPIFlags = %v", got)
	}
	back := toIMAPFlags(append(got, "bogus"))
	if len(back) != 5 || back[0] != imap.FlagDeleted || back[4] != imap.FlagSeen {
		t.Fatalf("toIMAPFlags = %v", back)
	}
	if got := toAPIFlags(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil in → %v", got)
	}
}

func TestPermittedFlags(t *testing.T) {
	flags := []imap.Flag{imap.FlagSeen, imap.FlagJunk, imap.FlagForwarded}
	if got := permittedFlags(nil, flags); len(got) != 3 {
		t.Fatalf("unknown permanent flags must pass everything: %v", got)
	}
	if got := permittedFlags([]imap.Flag{imap.FlagSeen}, flags); len(got) != 1 || got[0] != imap.FlagSeen {
		t.Fatalf("keywords not permitted: %v", got)
	}
	if got := permittedFlags([]imap.Flag{imap.FlagSeen, "$JUNK"}, flags); len(got) != 2 {
		t.Fatalf("listed keyword: %v", got)
	}
	if got := permittedFlags([]imap.Flag{imap.FlagSeen, imap.FlagWildcard}, flags); len(got) != 3 {
		t.Fatalf("wildcard: %v", got)
	}
}

func TestRoleHeuristics(t *testing.T) {
	cases := map[string]api.FolderRole{
		"Sent": api.RoleSent, "Sent Items": api.RoleSent, "Odeslaná pošta": api.RoleSent, "Gesendete Elemente": api.RoleSent,
		"Éléments envoyés": api.RoleSent, "Drafts": api.RoleDrafts, "Koncepty": api.RoleDrafts, "Entwürfe": api.RoleDrafts,
		"Brouillons": api.RoleDrafts, "Trash": api.RoleTrash, "Deleted Items": api.RoleTrash, "Koš": api.RoleTrash,
		"Papierkorb": api.RoleTrash, "Corbeille": api.RoleTrash, "Junk": api.RoleJunk, "Spam": api.RoleJunk,
		"Nevyžádaná pošta": api.RoleJunk, "Archive": api.RoleArchive, "Archiv": api.RoleArchive,
		"Projects": "", "sent": api.RoleSent, "  SPAM  ": api.RoleJunk,
	}
	for name, want := range cases {
		if got := roleForName(name); got != want {
			t.Errorf("roleForName(%q) = %q, want %q", name, got, want)
		}
	}
	if roleFromAttrs([]imap.MailboxAttr{imap.MailboxAttrHasChildren, "\\TRASH"}) != api.RoleTrash {
		t.Fatal("special-use attr")
	}
}

func TestAssignRolesOnePerRole(t *testing.T) {
	folders := []*store.Folder{
		{Mailbox: "INBOX", Name: "INBOX", Role: api.RoleInbox},
		{Mailbox: "Sent", Name: "Sent"},
		{Mailbox: "INBOX/Sent Items", Name: "Sent Items", ParentMailbox: "INBOX"},
		{Mailbox: "Projects/Trash", Name: "Trash", ParentMailbox: "Projects"},
		{Mailbox: "Projects", Name: "Projects"},
		{Mailbox: "Special", Name: "Special", Role: api.RoleTrash},
		{Mailbox: "Bin", Name: "Bin"},
	}
	byMailbox := map[string]*store.Folder{}
	for _, f := range folders {
		byMailbox[f.Mailbox] = f
	}
	assignRoles(folders, byMailbox)
	got := map[string]api.FolderRole{}
	for _, f := range folders {
		got[f.Mailbox] = f.Role
	}
	want := map[string]api.FolderRole{
		"INBOX": api.RoleInbox, "Sent": api.RoleSent, "INBOX/Sent Items": api.RoleNone, "Projects/Trash": api.RoleNone,
		"Projects": api.RoleNone, "Special": api.RoleTrash, "Bin": api.RoleNone,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roles = %v", got)
	}
}

func TestSplitMailbox(t *testing.T) {
	for _, tc := range []struct{ in, delim, name, parent string }{
		{"INBOX", "/", "INBOX", ""},
		{"a/b/c", "/", "c", "a/b"},
		{"a.b", ".", "b", "a"},
		{"flat", "", "flat", ""},
		{"trail/", "/", "trail", ""},
	} {
		name, parent := splitMailbox(tc.in, tc.delim)
		if name != tc.name || parent != tc.parent {
			t.Errorf("splitMailbox(%q,%q) = %q,%q", tc.in, tc.delim, name, parent)
		}
	}
	if displayPath("a.b.c", ".") != "a/b/c" || displayPath("a/b", "/") != "a/b" {
		t.Fatal("displayPath")
	}
}

func TestAttachmentsFromBodyStructure(t *testing.T) {
	bs := &imap.BodyStructureMultiPart{
		Subtype: "mixed",
		Children: []imap.BodyStructure{
			&imap.BodyStructureMultiPart{
				Subtype: "alternative",
				Children: []imap.BodyStructure{
					&imap.BodyStructureSinglePart{Type: "text", Subtype: "plain", Size: 10},
					&imap.BodyStructureSinglePart{Type: "text", Subtype: "html", Size: 20},
				},
			},
			&imap.BodyStructureSinglePart{Type: "image", Subtype: "PNG", ID: "<img1@x>", Size: 300,
				Extended: &imap.BodyStructureSinglePartExt{Disposition: &imap.BodyStructureDisposition{Value: "inline", Params: map[string]string{"filename": "../../etc/passwd.png"}}}},
			&imap.BodyStructureSinglePart{Type: "application", Subtype: "pdf", Size: 5000,
				Extended: &imap.BodyStructureSinglePartExt{Disposition: &imap.BodyStructureDisposition{Value: "attachment", Params: map[string]string{"filename": "report\x00.pdf"}}}},
			&imap.BodyStructureSinglePart{Type: "text", Subtype: "plain", Size: 7,
				Extended: &imap.BodyStructureSinglePartExt{Disposition: &imap.BodyStructureDisposition{Value: "attachment"}}},
			&imap.BodyStructureSinglePart{Type: "message", Subtype: "rfc822", Size: 900},
			&imap.BodyStructureSinglePart{Type: "multipart", Subtype: "x", Size: 1},
		},
	}
	atts, has := attachmentsFromBodyStructure(bs)
	if !has || len(atts) != 5 {
		t.Fatalf("attachments = %+v", atts)
	}
	want := []api.Attachment{
		{PartID: "2", Filename: "passwd.png", ContentType: "image/png", Size: 300, Inline: true, ContentID: "img1@x"},
		{PartID: "3", Filename: "report.pdf", ContentType: "application/pdf", Size: 5000},
		{PartID: "4", Filename: "attachment-4.txt", ContentType: "text/plain", Size: 7},
		{PartID: "5", Filename: "attachment-5.eml", ContentType: "message/rfc822", Size: 900},
		{PartID: "6", Filename: "attachment-6", ContentType: "application/octet-stream", Size: 1},
	}
	if !reflect.DeepEqual(atts, want) {
		t.Fatalf("attachments =\n%+v\nwant\n%+v", atts, want)
	}
	if atts, has := attachmentsFromBodyStructure(&imap.BodyStructureSinglePart{Type: "text", Subtype: "plain"}); has || len(atts) != 0 {
		t.Fatalf("plain body: %+v", atts)
	}
	if atts, has := attachmentsFromBodyStructure(nil); has || atts != nil {
		t.Fatal("nil")
	}
}

func TestCleanField(t *testing.T) {
	got := cleanField("  Sub\x00ject\r\nline \xff "+strings.Repeat("ž", 2000), 64)
	if strings.ContainsAny(got, "\x00\r\n") || len(got) > 64 || !strings.HasPrefix(got, "Sub ject") {
		t.Fatalf("got %q (%d)", got, len(got))
	}
	if cleanID(" <abc\t@x.y> ") != "abc@x.y" {
		t.Fatalf("cleanID = %q", cleanID(" <abc\t@x.y> "))
	}
}

func TestDiffUIDs(t *testing.T) {
	gone, fresh, common := diffUIDs([]uint32{1, 2, 5, 9}, []uint32{2, 3, 9, 10})
	if !reflect.DeepEqual(gone, []uint32{1, 5}) || !reflect.DeepEqual(fresh, []uint32{3, 10}) || !reflect.DeepEqual(common, []uint32{2, 9}) {
		t.Fatalf("diff = %v %v %v", gone, fresh, common)
	}
}

func TestUIDsFromSetGuards(t *testing.T) {
	if _, err := uidsFromSet(imap.UIDSet{{Start: 1, Stop: 0}}); err == nil {
		t.Fatal("dynamic set accepted")
	}
	if _, err := uidsFromSet(imap.UIDSet{{Start: 1, Stop: 4_000_000_000}}); err == nil {
		t.Fatal("huge set accepted")
	}
	got, err := uidsFromSet(imap.UIDSet{{Start: 3, Stop: 5}, {Start: 9, Stop: 9}})
	if err != nil || !reflect.DeepEqual(got, []uint32{3, 4, 5, 9}) {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestGroupOps(t *testing.T) {
	ops := []store.Op{
		{ID: 1, Kind: store.OpFlag, MessageID: "a", FolderID: "f", UID: 1, Set: []api.Flag{api.FlagSeen}},
		{ID: 2, Kind: store.OpFlag, MessageID: "b", FolderID: "f", UID: 2, Set: []api.Flag{api.FlagSeen}},
		{ID: 3, Kind: store.OpDelete, MessageID: "a", FolderID: "f", UID: 1},
		{ID: 4, Kind: store.OpFlag, MessageID: "a", FolderID: "f", UID: 1, Set: []api.Flag{api.FlagSeen}}, // after a's delete: new group
		{ID: 5, Kind: store.OpMove, MessageID: "c", FolderID: "f", UID: 0, TargetFolderID: "g"},           // blocked
		{ID: 6, Kind: store.OpFlag, MessageID: "c", FolderID: "g", UID: 7, Set: []api.Flag{api.FlagSeen}}, // blocked by 5
		{ID: 7, Kind: store.OpFlag, MessageID: "d", FolderID: "f", UID: 4, Set: []api.Flag{api.FlagSeen}}, // joins the latest flag group
	}
	groups := groupOps(ops)
	var got [][]int64
	for _, g := range groups {
		var ids []int64
		for _, op := range g.ops {
			ids = append(ids, op.ID)
		}
		got = append(got, ids)
	}
	want := [][]int64{{1, 2}, {3}, {4, 7}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
}

func TestOpBackoffAndReconnectBackoff(t *testing.T) {
	if opBackoff(0) != 30*time.Second || opBackoff(1) != time.Minute || opBackoff(20) != time.Hour {
		t.Fatalf("opBackoff = %v %v %v", opBackoff(0), opBackoff(1), opBackoff(20))
	}
	s := NewSyncer(store.Account{ID: "acc"}, Deps{})
	for attempt, want := range []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second} {
		d := s.backoff(attempt)
		if d < time.Duration(float64(want)*0.79) || d > time.Duration(float64(want)*1.21) {
			t.Fatalf("backoff(%d) = %v", attempt, d)
		}
	}
	if d := s.backoff(30); d > time.Duration(float64(backoffMax)*1.21) {
		t.Fatalf("backoff cap = %v", d)
	}
}

func TestSameStateAndTriggerCoalescing(t *testing.T) {
	now := time.Now()
	a := api.SyncState{Status: api.SyncIdle, Progress: -1, LastSync: &now}
	b := a
	if !sameState(a, b) {
		t.Fatal("identical states differ")
	}
	b.Progress = 3
	if sameState(a, b) {
		t.Fatal("progress change unnoticed")
	}
	b = a
	b.Error = api.NewError(api.CodeNetworkError, "x")
	if sameState(a, b) {
		t.Fatal("error change unnoticed")
	}

	s := NewSyncer(store.Account{ID: "acc"}, Deps{})
	s.Trigger("f1", false)
	req, ok := s.takePending()
	if !ok || req.folder != "f1" || req.full {
		t.Fatalf("single folder = %+v %v", req, ok)
	}
	s.Trigger("f1", true)
	s.Trigger("f2", false)
	req, ok = s.takePending()
	if !ok || req.folder != "" || !req.full {
		t.Fatalf("two folders = %+v %v", req, ok)
	}
	if _, ok := s.takePending(); ok {
		t.Fatal("pending not cleared")
	}
	if st := s.State(); st.AccountID != "acc" || st.Status != api.SyncIdle || st.Progress != -1 {
		t.Fatalf("initial state = %+v", st)
	}
}
