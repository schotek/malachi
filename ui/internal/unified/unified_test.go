// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package unified

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func account(id api.AccountID, kind api.AccountKind, enabled bool) api.Account {
	return api.Account{ID: id, Enabled: enabled, Config: api.AccountConfig{Kind: kind}}
}

func inbox(acc api.AccountID, id api.FolderID, unread, total int) api.Folder {
	return api.Folder{ID: id, AccountID: acc, Role: api.RoleInbox, Synced: true, Selectable: true, Unread: unread, Total: total}
}

var sample = []api.Account{
	account("jira", api.AccountJira, true),
	account("b", api.AccountGraph, true),
	account("off", api.AccountIMAP, false),
	account("cal", api.AccountCalDAV, true),
	account("a", "", true), // an empty kind is imap
	account("crm", api.AccountEway, true),
}

func TestAccountsAndShown(t *testing.T) {
	if got := Accounts(sample); !reflect.DeepEqual(got, []api.AccountID{"b", "a"}) {
		t.Errorf("Accounts = %v", got)
	}
	if !Shown(sample, true) || Shown(sample, false) {
		t.Error("two mail accounts: shown only with the setting on")
	}
	one := []api.Account{account("a", api.AccountIMAP, true), account("off", api.AccountGraph, false), account("j", api.AccountJira, true)}
	if Shown(one, true) {
		t.Error("one mail account: not shown")
	}
	if Shown(nil, true) {
		t.Error("no account: not shown")
	}
}

func TestInboxesAndCounts(t *testing.T) {
	folders := map[api.AccountID][]api.Folder{
		"a": {
			{ID: "a-sent", AccountID: "a", Role: api.RoleSent, Synced: true, Unread: 9},
			inbox("a", "a-in", 2, 10),
		},
		"b": {
			inbox("b", "b-in", 3, 5),
			{ID: "b-arch", AccountID: "b", Role: api.RoleInbox, Synced: false, Unread: 7, Total: 7},
		},
		"off":  {inbox("off", "off-in", 4, 4)},
		"jira": {{ID: "j-v", AccountID: "jira", Role: api.RoleInbox, Synced: true, Virtual: api.VirtualOpen, Unread: 1}},
	}
	got := Inboxes(sample, folders)
	if len(got) != 2 || got[0].ID != "b-in" || got[1].ID != "a-in" {
		t.Fatalf("Inboxes = %+v", got)
	}
	if Unread(got) != 5 || Total(got) != 15 {
		t.Errorf("Unread %d, Total %d", Unread(got), Total(got))
	}
	if Unread(nil) != 0 || Total(nil) != 0 {
		t.Error("empty counts")
	}
	if !Covers(true, "a", "a-in", got) || Covers(false, "a", "a-in", got) {
		t.Error("Covers of an inbox")
	}
	if Covers(true, "a", "a-sent", got) || Covers(true, "b", "a-in", got) || Covers(true, "off", "off-in", got) {
		t.Error("Covers of a folder out of scope")
	}
	if !CoversAny(true, "b", nil, got) || CoversAny(true, "off", nil, got) || CoversAny(false, "b", nil, got) {
		t.Error("CoversAny of an account")
	}
	if !CoversAny(true, "a", []api.FolderID{"a-sent", "a-in"}, got) || CoversAny(true, "a", []api.FolderID{"a-sent"}, got) {
		t.Error("CoversAny of folders")
	}
}

func TestKey(t *testing.T) {
	if !Key.IsUnified() || !IsKey("", KeyFolder) {
		t.Error("the key is unified")
	}
	for _, k := range []FolderKey{{}, {Account: "a", Folder: KeyFolder}, {Folder: "f"}, {Account: "a", Folder: "a-in"}} {
		if k.IsUnified() {
			t.Errorf("%+v is not the key", k)
		}
	}
	if !strings.HasPrefix(string(KeyFolder), "@") {
		t.Error("the key's folder starts with @")
	}
}

func TestParams(t *testing.T) {
	m := MessageParams(api.FilterUnread, "c")
	want := api.MessageListParams{Unified: api.RoleInbox, Page: api.Page{Limit: api.DefaultPageLimit, Cursor: "c"}, Sort: api.SortDateDesc, Filter: api.FilterUnread}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("MessageParams = %+v", m)
	}
	th := ThreadParams("", "")
	if th.Unified != api.RoleInbox || th.AccountID != "" || th.FolderID != "" || th.Page.Limit != api.DefaultPageLimit || th.Sort != api.SortDateDesc {
		t.Errorf("ThreadParams = %+v", th)
	}
	found := false
	for _, r := range api.UnifiedRoles {
		found = found || r == Role
	}
	if !found {
		t.Error("Role is not one the API accepts")
	}
}

func TestGroupByAccount(t *testing.T) {
	type row struct {
		id  string
		acc api.AccountID
	}
	rows := []row{{"1", "b"}, {"2", "a"}, {"3", "b"}, {"4", "a"}, {"5", "c"}}
	of := func(r row) api.AccountID { return r.acc }
	got := GroupByAccount(rows, of)
	want := []Group[row]{
		{Account: "b", Items: []row{{"1", "b"}, {"3", "b"}}},
		{Account: "a", Items: []row{{"2", "a"}, {"4", "a"}}},
		{Account: "c", Items: []row{{"5", "c"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GroupByAccount = %+v", got)
	}
	if GroupByAccount[row](nil, of) != nil {
		t.Error("no rows: no groups")
	}
	if !Mixed(rows, of) || Mixed(rows[:1], of) || Mixed([]row{{"1", "a"}, {"2", "a"}}, of) || Mixed[row](nil, of) {
		t.Error("Mixed")
	}
}

type echo struct{}

func (echo) T(msgid string) string { return msgid }
func (echo) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (echo) C(_, msgid string) string { return msgid }

// TestTextsInCatalogues keeps po/malachi.pot and po/cs.po in step with
// the texts where make po cannot run: each msgid is in the template and
// has a Czech translation.
func TestTextsInCatalogues(t *testing.T) {
	texts := []string{Title(echo{}), PreferenceTitle(echo{}), PreferenceSubtitle(echo{})}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "po", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	pot, cs := read("malachi.pot"), read("cs.po")
	for _, s := range texts {
		id := "msgid \"" + s + "\"\n"
		if !strings.Contains(pot, id+"msgstr \"\"\n") {
			t.Errorf("po/malachi.pot lacks %q", s)
		}
		i := strings.Index(cs, id)
		if i < 0 {
			t.Errorf("po/cs.po lacks %q", s)
			continue
		}
		rest := cs[i+len(id):]
		if strings.HasPrefix(rest, "msgstr \"\"\n") || !strings.HasPrefix(rest, "msgstr \"") {
			t.Errorf("po/cs.po does not translate %q", s)
		}
	}
}
