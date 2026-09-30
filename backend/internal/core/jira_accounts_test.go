// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// jiraConfig is a valid cloud account of the fictional Acme site.
func jiraConfig() api.AccountConfig {
	return api.AccountConfig{
		Name: "Acme Jira", Email: "jana@acme.test", Kind: api.AccountJira,
		Jira: &api.JiraConfig{
			SiteURL: "https://acme.atlassian.net", Deployment: api.JiraCloud, Login: "jana@acme.test",
			CloudID: "3f1c2b7a-5d4e-4c2b-9a8f-1e2d3c4b5a69",
			Spaces:  []api.SpaceRef{{ID: "10000", Key: "ITSD", Name: "IT Service Desk"}, {ID: "10001", Key: "WEB"}},
		},
	}
}

// dcConfig is a valid Data Center account.
func dcConfig() api.AccountConfig {
	return api.AccountConfig{
		Name: "Acme DC", Email: "jana@acme.test", Kind: api.AccountJira,
		Jira: &api.JiraConfig{SiteURL: "https://jira.acme.test/jira", Deployment: api.JiraDataCenter,
			Spaces: []api.SpaceRef{{ID: "10000", Key: "ITSD"}}},
	}
}

func TestJiraAccountValidation(t *testing.T) {
	ctl := string(rune(0x07))
	many := func(n int, f func(i int) string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = f(i)
		}
		return out
	}
	cases := []struct {
		name string
		base func() api.AccountConfig
		mut  func(*api.AccountConfig)
		ok   bool
		want func(*testing.T, api.AccountConfig) // checks the normalised form
	}{
		{"valid cloud", jiraConfig, func(*api.AccountConfig) {}, true, nil},
		{"valid datacenter", dcConfig, func(*api.AccountConfig) {}, true, nil},
		{"site normalised", jiraConfig, func(c *api.AccountConfig) { c.Jira.SiteURL = " ACME.atlassian.net/browse/ITSD-1 " }, true,
			func(t *testing.T, c api.AccountConfig) {
				if c.Jira.SiteURL != "https://acme.atlassian.net" {
					t.Errorf("site = %q", c.Jira.SiteURL)
				}
			}},
		{"dc context path kept", dcConfig, func(c *api.AccountConfig) { c.Jira.SiteURL = "jira.acme.test/jira/" }, true,
			func(t *testing.T, c api.AccountConfig) {
				if c.Jira.SiteURL != "https://jira.acme.test/jira" {
					t.Errorf("site = %q", c.Jira.SiteURL)
				}
			}},
		{"trimmed ids, lower-cased cloud id", jiraConfig, func(c *api.AccountConfig) {
			c.Jira.CloudID = " 3F1C2B7A-5D4E-4C2B-9A8F-1E2D3C4B5A69 "
			c.Jira.Login = " jana@acme.test "
			c.Jira.Spaces[0].ID, c.Jira.Spaces[0].Key = " 10000 ", " ITSD "
			c.Jira.NotificationSenders = []string{" Jira@ACME.test ", "@Acme.Test"}
		}, true, func(t *testing.T, c api.AccountConfig) {
			j := c.Jira
			if j.CloudID != "3f1c2b7a-5d4e-4c2b-9a8f-1e2d3c4b5a69" || j.Login != "jana@acme.test" || j.Spaces[0].ID != "10000" ||
				j.Spaces[0].Key != "ITSD" || !slices.Equal(j.NotificationSenders, []string{"jira@acme.test", "@acme.test"}) {
				t.Errorf("normalised = %+v", j)
			}
		}},
		{"no jira block", jiraConfig, func(c *api.AccountConfig) { c.Jira = nil }, false, nil},
		{"imap block", jiraConfig, func(c *api.AccountConfig) { c.IMAP = validConfig().IMAP }, false, nil},
		{"smtp block", jiraConfig, func(c *api.AccountConfig) { c.SMTP = validConfig().SMTP }, false, nil},
		{"graph block", jiraConfig, func(c *api.AccountConfig) { c.Graph = graphConfig().Graph }, false, nil},
		{"oauth2 block", jiraConfig, func(c *api.AccountConfig) { c.OAuth2 = &api.OAuth2Config{Provider: "office365"} }, false, nil},
		{"empty site", jiraConfig, func(c *api.AccountConfig) { c.Jira.SiteURL = " " }, false, nil},
		{"ftp site", jiraConfig, func(c *api.AccountConfig) { c.Jira.SiteURL = "ftp://acme.atlassian.net" }, false, nil},
		{"user info", jiraConfig, func(c *api.AccountConfig) { c.Jira.SiteURL = "https://jana:pw@acme.atlassian.net" }, false, nil},
		{"query", jiraConfig, func(c *api.AccountConfig) { c.Jira.SiteURL = "https://acme.atlassian.net/?a=1" }, false, nil},
		{"control character", jiraConfig, func(c *api.AccountConfig) { c.Jira.SiteURL = "https://acme" + ctl + ".atlassian.net" }, false, nil},
		{"cloud over http", jiraConfig, func(c *api.AccountConfig) { c.Jira.SiteURL = "http://jira.acme.test" }, false, nil},
		{"cloud over http on loopback", jiraConfig, func(c *api.AccountConfig) { c.Jira.SiteURL = "http://127.0.0.1:8080" }, true, nil},
		{"datacenter over http", dcConfig, func(c *api.AccountConfig) { c.Jira.SiteURL = "http://jira.acme.test:8080/jira" }, true, nil},
		{"unknown deployment", jiraConfig, func(c *api.AccountConfig) { c.Jira.Deployment = "server" }, false, nil},
		{"cloud without login", jiraConfig, func(c *api.AccountConfig) { c.Jira.Login = "" }, false, nil},
		{"cloud login not an address", jiraConfig, func(c *api.AccountConfig) { c.Jira.Login = "jana" }, false, nil},
		{"cloud login with a name", jiraConfig, func(c *api.AccountConfig) { c.Jira.Login = "Jana <jana@acme.test>" }, false, nil},
		{"datacenter with login", dcConfig, func(c *api.AccountConfig) { c.Jira.Login = "jana@acme.test" }, false, nil},
		{"cloud id not a uuid", jiraConfig, func(c *api.AccountConfig) { c.Jira.CloudID = "acme" }, false, nil},
		{"cloud id on datacenter", dcConfig, func(c *api.AccountConfig) { c.Jira.CloudID = "3f1c2b7a-5d4e-4c2b-9a8f-1e2d3c4b5a69" }, false, nil},
		{"cloud without cloud id", jiraConfig, func(c *api.AccountConfig) { c.Jira.CloudID = "" }, true, nil},
		{"no spaces", jiraConfig, func(c *api.AccountConfig) { c.Jira.Spaces = nil }, false, nil},
		{"too many spaces", jiraConfig, func(c *api.AccountConfig) {
			c.Jira.Spaces = nil
			for i := range api.MaxJiraSpaces + 1 {
				c.Jira.Spaces = append(c.Jira.Spaces, api.SpaceRef{ID: fmt.Sprint(i), Key: fmt.Sprintf("K%d", i)})
			}
		}, false, nil},
		{"most spaces", jiraConfig, func(c *api.AccountConfig) {
			c.Jira.Spaces = nil
			for i := range api.MaxJiraSpaces {
				c.Jira.Spaces = append(c.Jira.Spaces, api.SpaceRef{ID: fmt.Sprint(i), Key: fmt.Sprintf("K%d", i)})
			}
		}, true, nil},
		{"space without id", jiraConfig, func(c *api.AccountConfig) { c.Jira.Spaces[0].ID = " " }, false, nil},
		{"space without key", jiraConfig, func(c *api.AccountConfig) { c.Jira.Spaces[1].Key = "" }, false, nil},
		{"space twice", jiraConfig, func(c *api.AccountConfig) { c.Jira.Spaces[1].ID = "10000" }, false, nil},
		{"space name with control", jiraConfig, func(c *api.AccountConfig) { c.Jira.Spaces[0].Name = "IT" + ctl }, false, nil},
		{"space name too long", jiraConfig, func(c *api.AccountConfig) { c.Jira.Spaces[0].Name = strings.Repeat("x", 257) }, false, nil},
		{"space key invalid utf-8", jiraConfig, func(c *api.AccountConfig) { c.Jira.Spaces[0].Key = "IT\xff" }, false, nil},
		{"offline days negative", jiraConfig, func(c *api.AccountConfig) { c.Jira.OfflineDays = -1 }, false, nil},
		{"offline days over", jiraConfig, func(c *api.AccountConfig) { c.Jira.OfflineDays = api.MaxJiraOfflineDays + 1 }, false, nil},
		{"offline days most", jiraConfig, func(c *api.AccountConfig) { c.Jira.OfflineDays = api.MaxJiraOfflineDays }, true, nil},
		{"views disabled", jiraConfig, func(c *api.AccountConfig) {
			c.Jira.DisabledFolders = []api.VirtualFolder{api.VirtualOpen, api.VirtualWatching, api.VirtualAssignedToMe}
		}, true, nil},
		{"unknown view", jiraConfig, func(c *api.AccountConfig) { c.Jira.DisabledFolders = []api.VirtualFolder{"inbox"} }, false, nil},
		{"view twice", jiraConfig, func(c *api.AccountConfig) {
			c.Jira.DisabledFolders = []api.VirtualFolder{api.VirtualOpen, api.VirtualOpen}
		}, false, nil},
		{"closed statuses", jiraConfig, func(c *api.AccountConfig) {
			c.Jira.ClosedStatuses = []api.StatusRef{{ID: "10001", Name: "Done"}, {ID: "6"}}
		}, true, nil},
		{"closed status without id", jiraConfig, func(c *api.AccountConfig) { c.Jira.ClosedStatuses = []api.StatusRef{{Name: "Done"}} }, false, nil},
		{"too many closed statuses", jiraConfig, func(c *api.AccountConfig) {
			for i := range api.MaxJiraStatuses + 1 {
				c.Jira.ClosedStatuses = append(c.Jira.ClosedStatuses, api.StatusRef{ID: fmt.Sprint(i)})
			}
		}, false, nil},
		{"notification modes", jiraConfig, func(c *api.AccountConfig) { c.Jira.NotificationMail = api.NotificationMailHide }, true, nil},
		{"unknown notification mode", jiraConfig, func(c *api.AccountConfig) { c.Jira.NotificationMail = "loud" }, false, nil},
		{"sender host only", jiraConfig, func(c *api.AccountConfig) { c.Jira.NotificationSenders = []string{"acme.test"} }, false, nil},
		{"sender at alone", jiraConfig, func(c *api.AccountConfig) { c.Jira.NotificationSenders = []string{"@"} }, false, nil},
		{"sender bad host", jiraConfig, func(c *api.AccountConfig) { c.Jira.NotificationSenders = []string{"@acme test"} }, false, nil},
		{"sender with name", jiraConfig, func(c *api.AccountConfig) { c.Jira.NotificationSenders = []string{"Jira <jira@acme.test>"} }, false, nil},
		{"bot names", jiraConfig, func(c *api.AccountConfig) {
			c.Jira.BotNames = []string{"Issue Sync – Synchronization for Jira"}
			c.Jira.AuthorPrefixes = []string{"ACME"}
		}, true, nil},
		{"too many bot names", jiraConfig, func(c *api.AccountConfig) {
			c.Jira.BotNames = many(api.MaxJiraListEntries+1, func(i int) string { return fmt.Sprint("bot", i) })
		}, false, nil},
		{"most filters", jiraConfig, func(c *api.AccountConfig) {
			c.Jira.MetadataFilters = many(api.MaxJiraListEntries, func(i int) string { return fmt.Sprintf(`^Sent by bot %d$`, i) })
		}, true, nil},
		{"entry too long", jiraConfig, func(c *api.AccountConfig) {
			c.Jira.AuthorPrefixes = []string{strings.Repeat("a", api.MaxJiraPatternBytes+1)}
		}, false, nil},
		{"entry with control", jiraConfig, func(c *api.AccountConfig) { c.Jira.BotNames = []string{"bot" + ctl} }, false, nil},
		{"entry invalid utf-8", jiraConfig, func(c *api.AccountConfig) { c.Jira.MetadataFilters = []string{"\xfe"} }, false, nil},
		{"filter not re2", jiraConfig, func(c *api.AccountConfig) { c.Jira.MetadataFilters = []string{`(unclosed`} }, false, nil},
		{"filter backreference", jiraConfig, func(c *api.AccountConfig) { c.Jira.MetadataFilters = []string{`(a)\1`} }, false, nil},
		{"interval too small", jiraConfig, func(c *api.AccountConfig) { c.SyncInterval = 30 }, false, nil},
		{"bad email", jiraConfig, func(c *api.AccountConfig) { c.Email = "jana" }, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.base()
			tc.mut(&c)
			err := validateAccountConfig(&c)
			switch {
			case tc.ok && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case !tc.ok && err == nil:
				t.Fatal("expected invalidArgument")
			case !tc.ok && errCode(t, err) != api.CodeInvalidArgument:
				t.Fatalf("code = %v", err)
			}
			if tc.want != nil {
				tc.want(t, c)
			}
		})
	}
}

// account.listSpaces checks the connection only: no name, address or
// spaces are needed yet (a Data Center wizard learns the address from the
// result).
func TestJiraConnectionValidation(t *testing.T) {
	c := dcConfig()
	c.Name, c.Email, c.Jira.Spaces = "", "", nil
	if err := validateJiraConnection(&c); err != nil {
		t.Fatalf("connection without spaces and address: %v", err)
	}
	c = jiraConfig()
	c.Jira.Spaces = nil
	c.Jira.MetadataFilters = []string{"("} // not the connection's business
	if err := validateJiraConnection(&c); err != nil {
		t.Fatalf("cloud connection: %v", err)
	}
	for name, mut := range map[string]func(*api.AccountConfig){
		"imap kind":    func(c *api.AccountConfig) { *c = validConfig() },
		"no block":     func(c *api.AccountConfig) { c.Jira = nil },
		"no login":     func(c *api.AccountConfig) { c.Jira.Login = "" },
		"bad site":     func(c *api.AccountConfig) { c.Jira.SiteURL = "::" },
		"window":       func(c *api.AccountConfig) { c.Jira.OfflineDays = 400 },
		"graph inside": func(c *api.AccountConfig) { c.Graph = &api.GraphConfig{} },
	} {
		c := jiraConfig()
		mut(&c)
		if err := validateJiraConnection(&c); err == nil || errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// An address is unique within its realm: a Jira account may share the
// address of a mailbox and of a Jira account of another site, not of one
// of the same site however it is spelt. Moving an account to another site
// needs its token again.
func TestJiraAccountRealms(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	b.Supervisor, b.Delivery = newFakeSupervisor(), newFakeOutbox()
	kr := newMemKeyring()
	b.Keyring = kr

	add := func(c api.AccountConfig, token string) (api.AccountID, error) {
		res, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: c, Credentials: api.Credentials{Password: token}})
		if err != nil {
			return "", err
		}
		return res.AccountID, nil
	}
	first, err := add(jiraConfig(), "tok-1")
	if err != nil {
		t.Fatal(err)
	}
	if kr.values[string(first)+"/password"] != "tok-1" {
		t.Fatalf("token not stored: %v", kr.values)
	}
	mail := validConfig()
	mail.Email = "jana@acme.test"
	mail.IMAP.Username, mail.SMTP.Username = mail.Email, mail.Email
	if _, err := add(mail, "pw"); err != nil {
		t.Fatalf("mailbox with the jira account's address: %v", err)
	}
	same := jiraConfig()
	same.Name = "Again"
	same.Jira.SiteURL = "HTTPS://Acme.Atlassian.net:443/"
	if _, err := add(same, "tok-2"); errCode(t, err) != api.CodeConflict {
		t.Fatalf("same site again: %v", err)
	}
	other := jiraConfig()
	other.Jira.SiteURL, other.Jira.CloudID = "https://beta.atlassian.net", ""
	second, err := add(other, "tok-3")
	if err != nil {
		t.Fatalf("another site: %v", err)
	}
	got, err := b.store.GetAccount(ctx, string(first))
	if err != nil || got.Realm != "acme.atlassian.net" || got.Config.Jira.SiteURL != "https://acme.atlassian.net" {
		t.Fatalf("stored = %+v, %v", got, err)
	}

	// Moving the second account onto the first's site conflicts; moving it
	// to a third site needs the token, the same site does not.
	moved := other
	moved.Jira.SiteURL = "https://acme.atlassian.net"
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: second, Config: moved, Credentials: api.Credentials{Password: "x"}}); errCode(t, err) != api.CodeConflict {
		t.Fatalf("moved onto a taken realm: %v", err)
	}
	moved.Jira.SiteURL = "https://gamma.atlassian.net"
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: second, Config: moved}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("moved without a token: %v", err)
	}
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: second, Config: moved, Credentials: api.Credentials{Password: "tok-4"}}); err != nil {
		t.Fatalf("moved with a token: %v", err)
	}
	renamed := moved
	renamed.Name = "Gamma"
	renamed.Jira.OnlyMine = true
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: second, Config: renamed}); err != nil {
		t.Fatalf("same site, no token: %v", err)
	}
	if kr.values[string(second)+"/password"] != "tok-4" {
		t.Fatalf("token after the updates: %v", kr.values)
	}
	// A mail account turned into a jira account never takes its password
	// along.
	mailID, err := add(func() api.AccountConfig { c := validConfig(); c.Email = "other@example.invalid"; return c }(), "mail-pw")
	if err != nil {
		t.Fatal(err)
	}
	turned := jiraConfig()
	turned.Email, turned.Jira.SiteURL, turned.Jira.CloudID = "other@example.invalid", "https://delta.atlassian.net", ""
	turned.Jira.Login = "other@example.invalid"
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: mailID, Config: turned}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("kind change without a token: %v", err)
	}
	// ... nor a jira account its token to a mail server.
	back := validConfig()
	back.Email = "other2@example.invalid"
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: second, Config: back}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("jira to imap without a password: %v", err)
	}
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: second, Config: graphConfig()}); err != nil {
		t.Fatalf("jira to graph (no password used): %v", err)
	}
	// An OAuth session means nothing to a jira account.
	if _, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: other, Credentials: api.Credentials{OAuthSession: "s_1"}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("oauthSession on jira: %v", err)
	}
}

// A jira account starts on the jira supervisor and outbox worker and can
// comment and have its messages forwarded; its messages cannot be moved or
// deleted, and it writes no e-mail.
func TestJiraAccountRoutingAndCapabilities(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	imapSup, graphSup, jiraSup := newFakeSupervisor(), newFakeSupervisor(), newFakeSupervisor()
	imapOut, graphOut, jiraOut := newFakeOutbox(), newFakeOutbox(), newFakeOutbox()
	b.Supervisor = newKindSupervisor(map[api.AccountKind]SyncSupervisor{
		api.AccountIMAP: imapSup, api.AccountGraph: graphSup, api.AccountJira: jiraSup,
	})
	b.Delivery = newKindOutbox(map[api.AccountKind]OutboxSupervisor{
		api.AccountIMAP: imapOut, api.AccountGraph: graphOut, api.AccountJira: jiraOut,
	})
	b.Keyring = newMemKeyring()

	res, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: jiraConfig(), Credentials: api.Credentials{Password: "tok"}})
	if err != nil {
		t.Fatal(err)
	}
	id := res.AccountID
	if fmt.Sprint(jiraSup.calls) != "[start:"+string(id)+"]" || len(imapSup.calls)+len(graphSup.calls) != 0 {
		t.Fatalf("sync routing: jira=%v imap=%v graph=%v", jiraSup.calls, imapSup.calls, graphSup.calls)
	}
	if len(imapOut.calls)+len(graphOut.calls) != 0 || fmt.Sprint(jiraOut.calls) != "[start:"+string(id)+"]" {
		t.Fatalf("outbox routing: jira=%v imap=%v graph=%v", jiraOut.calls, imapOut.calls, graphOut.calls)
	}
	list, err := b.Accounts().List(ctx, api.AccountListParams{})
	if err != nil || len(list.Accounts) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	acc := list.Accounts[0]
	if fmt.Sprint(acc.Capabilities) != "[comment forward transition]" || acc.Can(api.CapabilityMove) || acc.Can(api.CapabilityCompose) || acc.Can(api.CapabilityReply) {
		t.Fatalf("capabilities = %v", acc.Capabilities)
	}

	// A message of the account (the syncer's rows, stored directly).
	folders, _, err := b.store.UpsertFolders(ctx, string(id), []store.Folder{{Mailbox: "space:10000", Name: "IT Service Desk",
		Path: "IT Service Desk", Role: api.RoleNone, Subscribed: true, Selectable: true}})
	if err != nil {
		t.Fatal(err)
	}
	m := &store.Message{AccountID: string(id), FolderID: folders[0].ID, RemoteID: "c:1", ThreadID: store.IssueThreadID("20001"),
		Subject: "ITSD-1: Printer", RFCMessageID: "comment.1.issue.20001@acme.atlassian.net.malachi.invalid"}
	if err := b.store.UpsertMessages(ctx, []*store.Message{m}); err != nil {
		t.Fatal(err)
	}
	ids := []api.MessageID{api.MessageID(m.ID)}
	if _, err := b.Messages().Move(ctx, api.MessageMoveParams{AccountID: id, MessageIDs: ids, TargetFolderID: api.FolderID(folders[0].ID)}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("move: %v", err)
	}
	for _, permanent := range []bool{false, true} {
		if _, err := b.Messages().Delete(ctx, api.MessageDeleteParams{AccountID: id, MessageIDs: ids, Permanent: permanent}); errCode(t, err) != api.CodeInvalidArgument {
			t.Fatalf("delete permanent=%v: %v", permanent, err)
		}
	}
	// Reply is a comment (jira_send_test.go); nothing else is written in
	// the account.
	for _, mode := range []api.ComposeMode{api.ComposeNew, api.ComposeReplyAll, api.ComposeForward} {
		p := api.DraftCreateParams{AccountID: id, Mode: mode}
		if mode != api.ComposeNew {
			p.MessageID = api.MessageID(m.ID)
		}
		if _, err := b.Drafts().Create(ctx, p); errCode(t, err) != api.CodeInvalidArgument {
			t.Fatalf("draft.create %s: %v", mode, err)
		}
	}
	if _, err := b.Drafts().Save(ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: id, Subject: "x", TextBody: "y"}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("draft.save without the message it answers: %v", err)
	}
	if _, err := b.Messages().Send(ctx, api.MessageSendParams{AccountID: id, DraftID: "d_1", Version: 1}); errCode(t, err) != api.CodeDraftNotFound {
		t.Fatalf("message.send: %v", err)
	}
	// A mail account forwards a jira message; the message account is for a
	// forward only, and must exist.
	mail, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: validConfig(), Credentials: api.Credentials{Password: "pw"}})
	if err != nil {
		t.Fatal(err)
	}
	fwd, err := b.Drafts().Create(ctx, api.DraftCreateParams{AccountID: mail.AccountID, Mode: api.ComposeForward,
		MessageID: api.MessageID(m.ID), MessageAccountID: id})
	if err != nil || fwd.Draft.AccountID != mail.AccountID || fwd.Draft.Subject != "Fwd: ITSD-1: Printer" || fwd.Draft.Forwarding != api.MessageID(m.ID) {
		t.Fatalf("forward of a jira message: %+v %v", fwd, err)
	}
	if _, err := b.Drafts().Create(ctx, api.DraftCreateParams{AccountID: mail.AccountID, Mode: api.ComposeReply,
		MessageID: api.MessageID(m.ID), MessageAccountID: id}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("reply with a message account: %v", err)
	}
	if _, err := b.Drafts().Create(ctx, api.DraftCreateParams{AccountID: mail.AccountID, Mode: api.ComposeForward,
		MessageID: api.MessageID(m.ID), MessageAccountID: "acc_nobody"}); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("unknown message account: %v", err)
	}
	// Forwarded into the jira account: it writes no mail.
	if _, err := b.Drafts().Create(ctx, api.DraftCreateParams{AccountID: id, Mode: api.ComposeForward,
		MessageID: api.MessageID(m.ID), MessageAccountID: mail.AccountID}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("forward into the jira account: %v", err)
	}
	// Flags are allowed and ask no pass (they are local).
	jiraSup.mu.Lock()
	before := len(jiraSup.calls)
	jiraSup.mu.Unlock()
	if _, err := b.Messages().Flag(ctx, api.MessageFlagParams{AccountID: id, MessageIDs: ids, Set: []api.Flag{api.FlagSeen}}); err != nil {
		t.Fatalf("flag: %v", err)
	}
	jiraSup.mu.Lock()
	after := len(jiraSup.calls)
	jiraSup.mu.Unlock()
	if after != before {
		t.Fatalf("a local flag asked the jira syncer: %v", jiraSup.calls[before:])
	}
}

// account.detectSite through the daemon, against the fake sites.
func TestJiraDetectSite(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []jiratest.Mode{jiratest.Cloud, jiratest.DC} {
		t.Run(string(mode), func(t *testing.T) {
			f := jiratest.New(t, mode)
			b := newTestBackend(t, config.Default())
			b.JiraHTTP = f.HTTPClient()
			typed := strings.TrimPrefix(f.Site.String(), "https://") + "/browse/ITSD-1"
			res, err := b.Accounts().DetectSite(ctx, api.AccountDetectSiteParams{URL: typed})
			if err != nil {
				t.Fatal(err)
			}
			if res.Kind != api.AccountJira || res.SiteURL != f.Site.String() || res.Title == "" {
				t.Fatalf("result = %+v", res)
			}
			switch mode {
			case jiratest.Cloud:
				if res.Deployment != api.JiraCloud || res.CloudID != jiratest.CloudID {
					t.Fatalf("cloud = %+v", res)
				}
			case jiratest.DC:
				if res.Deployment != api.JiraDataCenter || res.CloudID != "" {
					t.Fatalf("dc = %+v", res)
				}
				f.Set(func(f *jiratest.Server) { f.AnonymousBlocked = true })
				res, err := b.Accounts().DetectSite(ctx, api.AccountDetectSiteParams{URL: f.Site.String()})
				if err != nil || res.Deployment != api.JiraDataCenter || res.Title != "" {
					t.Fatalf("anonymous blocked: %+v %v", res, err)
				}
			}
		})
	}
	b := newTestBackend(t, config.Default())
	b.JiraHTTP = jiratest.New(t, jiratest.Cloud).HTTPClient()
	for _, u := range []string{"", "  ", "ftp://acme.atlassian.net", "https://jana:pw@acme.atlassian.net"} {
		if _, err := b.Accounts().DetectSite(ctx, api.AccountDetectSiteParams{URL: u}); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%q: %v", u, err)
		}
	}
	// A host the fake network does not know is a network failure, not a
	// crash of the call.
	if _, err := b.Accounts().DetectSite(ctx, api.AccountDetectSiteParams{URL: "nowhere.test"}); err == nil {
		t.Error("unknown host detected")
	}
}

// account.listSpaces with a typed token and with the stored one.
func TestJiraListSpaces(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []jiratest.Mode{jiratest.Cloud, jiratest.DC} {
		t.Run(string(mode), func(t *testing.T) {
			f := jiratest.New(t, mode)
			f.AddIssue("WEB", "Landing page typo")
			b := newTestBackend(t, config.Default())
			b.Supervisor, b.Delivery = newFakeSupervisor(), newFakeOutbox()
			b.JiraHTTP = f.HTTPClient()
			kr := newMemKeyring()
			b.Keyring = kr

			cfg := api.AccountConfig{Name: "Acme", Kind: api.AccountJira, Jira: api.Ptr(f.Config())}
			cfg.Jira.Spaces = nil // being chosen
			res, err := b.Accounts().ListSpaces(ctx, api.AccountListSpacesParams{Config: cfg,
				Credentials: api.Credentials{Password: jiratest.Token}, Counts: true})
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, sp := range res.Spaces {
				names = append(names, sp.Name)
			}
			if res.User.Name != "Jana Dvořáková" || fmt.Sprint(names) != "[IT Service Desk Mobile Web]" || len(res.Statuses) != 4 {
				t.Fatalf("result = %+v", res)
			}
			if !res.Spaces[0].ServiceDesk || res.Spaces[2].Issues != 1 {
				t.Fatalf("spaces = %+v", res.Spaces)
			}

			// No token: authRequired; a wrong one: authFailed.
			if _, err := b.Accounts().ListSpaces(ctx, api.AccountListSpacesParams{Config: cfg}); errCode(t, err) != api.CodeAuthRequired {
				t.Fatalf("no token: %v", err)
			}
			if _, err := b.Accounts().ListSpaces(ctx, api.AccountListSpacesParams{Config: cfg, Credentials: api.Credentials{Password: "wrong"}}); errCode(t, err) != api.CodeAuthFailed {
				t.Fatalf("wrong token: %v", err)
			}
			if _, err := b.Accounts().ListSpaces(ctx, api.AccountListSpacesParams{Config: validConfig(), Credentials: api.Credentials{Password: "x"}}); errCode(t, err) != api.CodeInvalidArgument {
				t.Fatalf("imap config: %v", err)
			}

			// The stored token of an account of the site.
			full := cfg
			full.Email = jiratest.Login
			full.Jira = api.Ptr(f.Config())
			added, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: full, Credentials: api.Credentials{Password: jiratest.Token}})
			if err != nil {
				t.Fatal(err)
			}
			res, err = b.Accounts().ListSpaces(ctx, api.AccountListSpacesParams{AccountID: added.AccountID, Config: cfg})
			if err != nil || len(res.Spaces) != 3 || res.Spaces[0].Issues != -1 {
				t.Fatalf("stored token: %+v %v", res, err)
			}
			// ... never for another site.
			elsewhere := cfg
			elsewhere.Jira = api.Ptr(*cfg.Jira)
			elsewhere.Jira.SiteURL = "https://elsewhere.atlassian.net"
			if elsewhere.Jira.Deployment == api.JiraDataCenter {
				elsewhere.Jira.SiteURL = "https://jira.elsewhere.test"
			}
			if _, err := b.Accounts().ListSpaces(ctx, api.AccountListSpacesParams{AccountID: added.AccountID, Config: elsewhere}); errCode(t, err) != api.CodeInvalidArgument {
				t.Fatalf("stored token for another site: %v", err)
			}
			if _, err := b.Accounts().ListSpaces(ctx, api.AccountListSpacesParams{AccountID: "acc_nobody", Config: cfg}); errCode(t, err) != api.CodeAccountNotFound {
				t.Fatalf("unknown account: %v", err)
			}
			delete(kr.values, string(added.AccountID)+"/password")
			if _, err := b.Accounts().ListSpaces(ctx, api.AccountListSpacesParams{AccountID: added.AccountID, Config: cfg}); errCode(t, err) != api.CodeAuthRequired {
				t.Fatalf("no stored token: %v", err)
			}
		})
	}
}

// account.test of a jira account: the probe with the token given or
// stored, its outcome in the result's jira entry.
func TestJiraAccountTest(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []jiratest.Mode{jiratest.Cloud, jiratest.DC} {
		t.Run(string(mode), func(t *testing.T) {
			f := jiratest.New(t, mode)
			b := newTestBackend(t, config.Default())
			b.Supervisor, b.Delivery = newFakeSupervisor(), newFakeOutbox()
			b.JiraHTTP = f.HTTPClient()
			b.Keyring = newMemKeyring()
			cfg := api.AccountConfig{Name: "Acme", Email: jiratest.Login, Kind: api.AccountJira, Jira: api.Ptr(f.Config())}

			res, err := b.Accounts().Test(ctx, api.AccountTestParams{Config: cfg, Credentials: api.Credentials{Password: jiratest.Token}})
			if err != nil {
				t.Fatal(err)
			}
			want := "cloud"
			if mode == jiratest.DC {
				want = "datacenter"
			}
			if res.IMAP != nil || res.SMTP != nil || res.Graph != nil || res.Jira == nil || !res.Jira.OK || fmt.Sprint(res.Jira.Capabilities) != "["+want+"]" {
				t.Fatalf("result = %+v %+v", res, res.Jira)
			}
			res, err = b.Accounts().Test(ctx, api.AccountTestParams{Config: cfg, Credentials: api.Credentials{Password: "wrong"}})
			if err != nil || res.Jira.OK || res.Jira.Error == nil || res.Jira.Error.Code != api.CodeAuthFailed {
				t.Fatalf("wrong token: %+v %v", res.Jira, err)
			}
			res, err = b.Accounts().Test(ctx, api.AccountTestParams{Config: cfg})
			if err != nil || res.Jira.OK || res.Jira.Error == nil || res.Jira.Error.Code != api.CodeAuthRequired {
				t.Fatalf("no token: %+v %v", res.Jira, err)
			}
			added, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{Password: jiratest.Token}})
			if err != nil {
				t.Fatal(err)
			}
			res, err = b.Accounts().Test(ctx, api.AccountTestParams{AccountID: added.AccountID, Config: cfg})
			if err != nil || !res.Jira.OK {
				t.Fatalf("stored token: %+v %v", res.Jira, err)
			}
			if _, err := b.Accounts().Test(ctx, api.AccountTestParams{AccountID: "acc_nobody", Config: cfg}); errCode(t, err) != api.CodeAccountNotFound {
				t.Fatalf("unknown account: %v", err)
			}
			if mode == jiratest.Cloud {
				// A scoped token the site refuses goes through the gateway.
				f.Set(func(f *jiratest.Server) { f.GatewayOnly = true })
				res, err = b.Accounts().Test(ctx, api.AccountTestParams{Config: cfg, Credentials: api.Credentials{Password: jiratest.Token}})
				if err != nil || !res.Jira.OK || !slices.Contains(res.Jira.Capabilities, "gateway") {
					t.Fatalf("gateway: %+v %v", res.Jira, err)
				}
			}
		})
	}
}

// fakeIssueSupervisor is a fakeSupervisor that refreshes issues.
type fakeIssueSupervisor struct{ *fakeSupervisor }

func (f fakeIssueSupervisor) TriggerIssue(accountID, key string) bool {
	f.record("issue:" + accountID + ":" + key)
	return true
}

func (f fakeIssueSupervisor) RefreshIssueWait(_ context.Context, accountID, key string, d time.Duration) error {
	f.record(fmt.Sprintf("wait:%s:%s:%s", accountID, key, d))
	return nil
}

// The kind dispatcher runs, reloads and lists every distinct supervisor
// once, sends a kind without one nowhere and forwards the issue refresh
// to a supervisor that has it.
func TestKindDispatchTable(t *testing.T) {
	ctx := context.Background()
	shared := newFakeSupervisor()
	issues := fakeIssueSupervisor{newFakeSupervisor()}
	k := newKindSupervisor(map[api.AccountKind]SyncSupervisor{api.AccountIMAP: shared, api.AccountGraph: shared, api.AccountJira: issues})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { k.Run(runCtx); close(done) }() // a second Run of shared would close its channel twice
	<-shared.ran
	<-issues.ran
	cancel()
	<-done

	k.Start(store.Account{ID: "acc_mail", Config: validConfig()})
	k.Start(store.Account{ID: "acc_jira", Config: jiraConfig()})
	k.Start(store.Account{ID: "acc_pop", Config: api.AccountConfig{Kind: "pop3"}})
	issues.setState("acc_jira", api.SyncState{Status: api.SyncIdle})
	k.Reload()
	if shared.count("reload") != 1 || issues.count("reload") != 1 {
		t.Fatalf("reload: shared %v, issues %v", shared.recorded(), issues.recorded())
	}
	if k.Trigger("acc_pop", "", false) {
		t.Fatal("a kind without a supervisor was triggered")
	}
	if _, ok := k.State("acc_pop"); ok {
		t.Fatal("a kind without a supervisor has a state")
	}
	if !k.TriggerIssue("acc_jira", "ITSD-1") || k.TriggerIssue("acc_mail", "ITSD-1") || k.TriggerIssue("acc_unknown", "ITSD-1") {
		t.Fatal("TriggerIssue routing")
	}
	if err := k.RefreshIssueWait(ctx, "acc_jira", "ITSD-2", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := k.RefreshIssueWait(ctx, "acc_mail", "ITSD-2", time.Second); errCode(t, err) != api.CodeUnavailable {
		t.Fatalf("refresh on a mail account: %v", err)
	}
	if got := fmt.Sprint(issues.recorded()); got != "[start:acc_jira reload issue:acc_jira:ITSD-1 wait:acc_jira:ITSD-2:1s]" {
		t.Fatalf("issue supervisor calls = %s", got)
	}
	if got := fmt.Sprint(shared.recorded()); got != "[start:acc_mail reload]" {
		t.Fatalf("mail supervisor calls = %s", got)
	}
	if len(k.States()) != 1 {
		t.Fatalf("states = %v", k.States())
	}
}
