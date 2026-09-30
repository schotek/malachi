// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/capabilities"
)

// The per-message actions of a Jira account (action_rules.go over
// ui/internal/capabilities; the port of macOS JiraActionRulesTests): what
// the account does not offer is off and unsupported, Reply of a commenting
// account is comment, Forward needs an account to send from, and with
// nothing selected the listed folder's account decides.

func ruleAccount(id api.AccountID, kind api.AccountKind, caps []api.AccountCapability) api.Account {
	return api.Account{ID: id, Enabled: true, Config: api.AccountConfig{Email: "jana@acme.example", Kind: kind}, Capabilities: caps}
}

var (
	// A mail account of a daemon from before capabilities.
	ruleOldMail = ruleAccount("a", "", nil)
	ruleMail    = ruleAccount("m", "", api.MailCapabilities)
	// Reads and flags only.
	ruleJiraReadOnly = ruleAccount("j", api.AccountJira, []api.AccountCapability{})
	// Comments and forwards into a mail account.
	ruleJira = ruleAccount("j", api.AccountJira, []api.AccountCapability{api.CapabilityComment, api.CapabilityForward})
)

// everythingJiraLacks is every capability-bound action off.
var everythingJiraLacks = capabilities.Actions{}

// ruleModel lists folder "in" of account acc with one message "1" in it;
// every account's folders include Archive, Junk and Outbox so that only
// the capabilities can switch those off.
func ruleModel(accounts []api.Account, acc api.AccountID, flags ...api.Flag) *mailModel {
	m := &mailModel{accounts: accounts, folders: map[api.AccountID][]api.Folder{}}
	for _, a := range accounts {
		m.folders[a.ID] = []api.Folder{
			{ID: "in", Path: "INBOX", Role: api.RoleInbox, Selectable: true},
			{ID: "arch", Path: "Archive", Role: api.RoleArchive, Selectable: true},
			{ID: "junk", Path: "Junk", Role: api.RoleJunk, Selectable: true},
			{ID: "out", Path: "Outbox", Role: api.RoleOutbox, Selectable: true, Total: 1},
		}
	}
	m.listFolder = folderKey{Account: acc, Folder: "in"}
	s := summary("1", flags...)
	s.AccountID, s.FolderID = acc, "in"
	m.setMessages([]api.MessageSummary{s}, api.PageInfo{Total: 1})
	return m
}

func firstRow(m *mailModel) listRow {
	return listRow{Key: listKey{Message: m.messages[0].ID}, Message: m.messages[0]}
}

func TestActionRulesMailAccountsOfferEverything(t *testing.T) {
	for _, acc := range []api.Account{ruleOldMail, ruleMail} {
		m := ruleModel([]api.Account{acc}, acc.ID, api.FlagSeen)
		st := m.messageActionState(firstRow(m), true)
		if !(st.reply && st.replyAll && st.forward && st.trash && st.archive && st.junk) {
			t.Errorf("%s: %+v", acc.ID, st)
		}
		all := capabilities.Actions{Reply: true, ReplyAll: true, Forward: true, Move: true, Trash: true, Archive: true, Junk: true}
		if st.supported != all || st.comment {
			t.Errorf("%s: supported %+v, comment %v", acc.ID, st.supported, st.comment)
		}
		if idle := m.messageActionState(listRow{}, false); idle.on || idle.supported != all || idle.comment {
			t.Errorf("%s: nothing selected in a mail folder: %+v", acc.ID, idle)
		}
	}
}

func TestActionRulesJiraWithoutCapabilities(t *testing.T) {
	m := ruleModel([]api.Account{ruleMail, ruleJiraReadOnly}, "j")
	st := m.messageActionState(firstRow(m), true)
	if !st.on {
		t.Fatal("not on")
	}
	if st.reply || st.replyAll || st.forward || st.trash || st.archive || st.junk {
		t.Errorf("an action the account lacks is on: %+v", st)
	}
	if st.supported != everythingJiraLacks || st.comment {
		t.Errorf("supported %+v, comment %v", st.supported, st.comment)
	}
	// Flags are local and always allowed.
	if !st.star || !st.toggleFlag || !st.markRead || st.markUnread || !st.loadImages || !st.trustSender {
		t.Errorf("flags %+v", st)
	}
	if st.changeStatus {
		t.Error("Change Status without the capability (and without an issue)")
	}
}

func TestActionRulesJiraThatComments(t *testing.T) {
	m := ruleModel([]api.Account{ruleMail, ruleJira}, "j", api.FlagSeen)
	st := m.messageActionState(firstRow(m), true)
	if !st.reply || !st.comment {
		t.Error("Reply writes a comment")
	}
	if !st.forward {
		t.Error("forwarded from the mail account")
	}
	if st.replyAll || st.trash || st.archive || st.junk {
		t.Errorf("%+v", st)
	}
	if want := (capabilities.Actions{Reply: true, Forward: true, Comment: true}); st.supported != want {
		t.Errorf("supported %+v", st.supported)
	}
	if !st.markUnread || st.markRead {
		t.Errorf("seen flags %+v", st)
	}
}

func TestActionRulesForwardNeedsAnAccountThatComposes(t *testing.T) {
	// Only the Jira account: nothing to forward from.
	m := ruleModel([]api.Account{ruleJira}, "j")
	st := m.messageActionState(firstRow(m), true)
	if st.forward || st.supported.Forward || !st.reply || !st.comment {
		t.Errorf("only Jira: %+v", st)
	}
	// A paused mail account does not count.
	paused := ruleMail
	paused.Enabled = false
	m = ruleModel([]api.Account{paused, ruleJira}, "j")
	if st := m.messageActionState(firstRow(m), true); st.forward || st.supported.Forward {
		t.Errorf("paused mail: %+v", st)
	}
	// An enabled one does, a mail account of an old daemon too.
	m = ruleModel([]api.Account{ruleOldMail, ruleJira}, "j")
	if st := m.messageActionState(firstRow(m), true); !st.forward || !st.supported.Forward {
		t.Errorf("old mail: %+v", st)
	}
}

func TestActionRulesNothingSelectedFollowsTheListedFolder(t *testing.T) {
	m := ruleModel([]api.Account{ruleMail, ruleJiraReadOnly}, "j")
	st := m.messageActionState(listRow{}, false)
	if st.on || st.reply || st.trash || st.supported != everythingJiraLacks {
		t.Errorf("the toolbar of a Jira folder has no Reply: %+v", st)
	}
	if st := m.messageActionState(firstRow(m), false); st.supported != everythingJiraLacks {
		t.Errorf("a row, but off: %+v", st)
	}

	m = ruleModel([]api.Account{ruleMail, ruleJira}, "j")
	st = m.messageActionState(listRow{}, false)
	if !st.comment {
		t.Error("Reply is not labelled Comment before a row is chosen")
	}
	if want := (capabilities.Actions{Reply: true, Forward: true, Comment: true}); st.supported != want {
		t.Errorf("supported %+v", st.supported)
	}
	// The Jira account's outbox: Trash cancels a queued comment.
	m.listFolder = folderKey{Account: "j", Folder: "out"}
	if st := m.messageActionState(listRow{}, false); !st.supported.Trash {
		t.Error("Trash does not cancel a queued comment")
	}
	// A mail folder, or none (a search over every account).
	all := capabilities.Actions{Reply: true, ReplyAll: true, Forward: true, Move: true, Trash: true, Archive: true, Junk: true}
	m.listFolder = folderKey{Account: "m", Folder: "in"}
	if st := m.messageActionState(listRow{}, false); st.supported != all || st.comment {
		t.Errorf("mail folder: %+v", st)
	}
	m.listFolder = folderKey{}
	if st := m.messageActionState(listRow{}, false); st.supported != all || st.comment {
		t.Errorf("no folder: %+v", st)
	}
}

func TestActionRulesTheRowsAccountDecidesInASearch(t *testing.T) {
	// A search result of the Jira account while nothing is listed.
	m := ruleModel([]api.Account{ruleMail, ruleJiraReadOnly}, "j")
	m.listFolder = folderKey{}
	if st := m.messageActionState(firstRow(m), true); st.supported != everythingJiraLacks {
		t.Errorf("supported %+v", st.supported)
	}
	// An account the window does not know (a message window outliving it)
	// has the mail default.
	s := summary("2", api.FlagSeen)
	s.AccountID, s.FolderID = "gone", "in"
	gone := m.messageActionState(listRow{Key: listKey{Message: "2"}, Message: s}, true)
	if !gone.reply || !gone.trash || !gone.supported.Trash {
		t.Errorf("unknown account: %+v", gone)
	}
}

func TestActionRulesAQueuedJiraCommentCanBeCancelled(t *testing.T) {
	m := ruleModel([]api.Account{ruleMail, ruleJira}, "j")
	m.messages[0].FolderID = "out"
	m.messages[0].Outbox = &api.OutboxInfo{State: api.OutboxQueued}
	st := m.messageActionState(firstRow(m), true)
	if !st.outbox || !st.trash || !st.supported.Trash {
		t.Errorf("Trash cancels the send: %+v", st)
	}
	if st.star || st.archive || st.junk {
		t.Errorf("%+v", st)
	}
}

func TestActionRulesChangeStatus(t *testing.T) {
	jira := ruleAccount("j", api.AccountJira, []api.AccountCapability{api.CapabilityComment, api.CapabilityTransition})
	m := ruleModel([]api.Account{ruleMail, jira}, "j")
	if st := m.messageActionState(firstRow(m), true); st.changeStatus {
		t.Error("Change Status for a message without an issue")
	}
	m.messages[0].Issue = &api.MessageIssue{IssueInfo: api.IssueInfo{Key: "ITSD-42"}, Item: api.IssueItemComment}
	if st := m.messageActionState(firstRow(m), true); !st.changeStatus {
		t.Error("no Change Status on an issue of an account that changes statuses")
	}
	if st := m.messageActionState(firstRow(m), false); st.changeStatus {
		t.Error("Change Status with nothing selected")
	}
	m = ruleModel([]api.Account{ruleMail, ruleJira}, "j")
	m.messages[0].Issue = &api.MessageIssue{IssueInfo: api.IssueInfo{Key: "ITSD-42"}, Item: api.IssueItemComment}
	if st := m.messageActionState(firstRow(m), true); st.changeStatus {
		t.Error("Change Status without the capability")
	}
}
