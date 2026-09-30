// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// noteRecorder keeps what reaches the clients.
type noteRecorder struct {
	mu      sync.Mutex
	news    []api.NewMessageNotification
	changed []api.MessagesChangedNotification
}

func (r *noteRecorder) NewMessage(n api.NewMessageNotification) {
	r.mu.Lock()
	r.news = append(r.news, n)
	r.mu.Unlock()
}
func (r *noteRecorder) MessagesChanged(n api.MessagesChangedNotification) {
	r.mu.Lock()
	r.changed = append(r.changed, n)
	r.mu.Unlock()
}
func (*noteRecorder) SyncState(api.SyncStateNotification)             {}
func (*noteRecorder) AuthRequired(api.AuthRequiredNotification)       {}
func (*noteRecorder) AccountsChanged(api.AccountsChangedNotification) {}

// announced lists the subjects of the messages announced so far.
func (r *noteRecorder) announced() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.news))
	for _, n := range r.news {
		out = append(out, n.Message.Subject)
	}
	return out
}

// takeChanged returns the notify.messagesChanged so far, as
// "account:folder,folder", and forgets them.
func (r *noteRecorder) takeChanged() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.changed))
	for _, n := range r.changed {
		folders := make([]string, len(n.FolderIDs))
		for i, f := range n.FolderIDs {
			folders[i] = string(f)
		}
		out = append(out, string(n.AccountID)+":"+strings.Join(folders, ","))
	}
	r.changed = nil
	return out
}

// issueSup is a supervisor that refreshes issues: it records what it is
// asked for, and refresh says what a waited-for refresh does.
type issueSup struct {
	*fakeSupervisor
	refresh func(accountID, key string) error
	refuse  bool // no syncer runs for the account
}

func (f *issueSup) TriggerIssue(accountID, key string) bool {
	f.record("issue:" + accountID + ":" + key)
	return !f.refuse
}

func (f *issueSup) RefreshIssueWait(_ context.Context, accountID, key string, d time.Duration) error {
	f.record(fmt.Sprintf("wait:%s:%s:%s", accountID, key, d))
	switch {
	case f.refuse:
		return api.NewError(api.CodeUnavailable, "not synchronising")
	case f.refresh != nil:
		return f.refresh(accountID, key)
	}
	return api.NewError(api.CodeServerTimeout, "not in time")
}

// asked lists the refreshes asked for so far ("issue:KEY", "wait:KEY"),
// and forgets them.
func (f *issueSup) asked(accountID string) []string {
	var out []string
	for _, c := range f.recorded() {
		kind, rest, _ := strings.Cut(c, ":")
		if kind != "issue" && kind != "wait" {
			continue
		}
		acc, key, _ := strings.Cut(rest, ":")
		if acc != accountID {
			continue
		}
		key, _, _ = strings.Cut(key, ":")
		out = append(out, kind+":"+key)
	}
	f.reset()
	return out
}

// issueMailBox is a mail account (inbox, archive) and a second one next
// to a cloud jira account of acme.atlassian.net with the spaces ITSD and
// WEB and the stored issue ITSD-42, over fake supervisors: the syncers'
// calls are made by the test.
type issueMailBox struct {
	t   *testing.T
	ctx context.Context
	b   *Backend
	sup *issueSup
	rec *noteRecorder

	mail, other     string
	jira            api.AccountID
	inbox, archive  store.Folder
	otherInbox      store.Folder
	uid             uint32
	coalesce, quiet time.Duration
}

// fastIssueMail shortens the feature's waits for a test.
func fastIssueMail(t *testing.T) {
	t.Helper()
	coalesce, pauseWas, debounce := issueMailCoalesce, issueMailPause, issueMailDebounce
	issueMailCoalesce, issueMailPause, issueMailDebounce = 10*time.Millisecond, 0, 20*time.Millisecond
	t.Cleanup(func() { issueMailCoalesce, issueMailPause, issueMailDebounce = coalesce, pauseWas, debounce })
}

func newIssueMailBox(t *testing.T, edit ...func(*api.AccountConfig)) *issueMailBox {
	t.Helper()
	fastIssueMail(t)
	b := newTestBackend(t, config.Default())
	b.Keyring = newMemKeyring()
	sup := &issueSup{fakeSupervisor: newFakeSupervisor()}
	b.Supervisor = sup
	b.Delivery = newFakeOutbox()
	rec := &noteRecorder{}
	b.SetNotifier(rec)
	t.Cleanup(b.Close)
	x := &issueMailBox{t: t, ctx: context.Background(), b: b, sup: sup, rec: rec, quiet: 80 * time.Millisecond}
	x.mail = seedAccount(t, b, "jana.dvorakova@example.invalid")
	x.other = seedAccount(t, b, "jana@example.invalid")
	folders := seedFolders(t, b, x.mail, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Archive", Name: "Archive", Path: "Archive", Role: api.RoleArchive, Subscribed: true, Selectable: true},
	})
	x.inbox, x.archive = folders["INBOX"], folders["Archive"]
	x.otherInbox = seedFolders(t, b, x.other, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
	})["INBOX"]
	cfg := jiraConfig()
	for _, e := range edit {
		e(&cfg)
	}
	x.jira = x.addTracker(cfg)
	x.putIssue(x.jira, "10042", "ITSD-42", time.Now().Add(-24*time.Hour))
	sup.reset()
	return x
}

// addTracker adds a jira account through the API and stores the spaces
// its first pass would.
func (x *issueMailBox) addTracker(cfg api.AccountConfig) api.AccountID {
	x.t.Helper()
	res, err := x.b.Accounts().Add(x.ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{Password: "tok-1"}})
	if err != nil {
		x.t.Fatal(err)
	}
	var spaces []store.IssueSpace
	for _, sp := range cfg.Jira.Spaces {
		spaces = append(spaces, store.IssueSpace{SpaceID: sp.ID, Key: sp.Key, Name: sp.Name})
	}
	if err := x.b.store.SetIssueSpaces(x.ctx, string(res.AccountID), spaces); err != nil {
		x.t.Fatal(err)
	}
	return res.AccountID
}

// putIssue stores an issue as the syncer does, last changed at updated.
func (x *issueMailBox) putIssue(account api.AccountID, id, key string, updated time.Time) {
	x.t.Helper()
	space := "10000"
	if strings.HasPrefix(key, "WEB-") {
		space = "10001"
	}
	if err := x.b.store.PutIssue(x.ctx, store.Issue{AccountID: string(account), IssueID: id, Key: key, SpaceID: space,
		Summary: "Printer on the 2nd floor", Updated: updated, SyncedUpdated: updated}); err != nil {
		x.t.Fatal(err)
	}
}

// update changes the jira account through account.update.
func (x *issueMailBox) update(edit func(*api.JiraConfig)) {
	x.t.Helper()
	a, err := x.b.store.GetAccount(x.ctx, string(x.jira))
	if err != nil {
		x.t.Fatal(err)
	}
	cfg := *cloneConfig(a.Config)
	edit(cfg.Jira)
	if _, err := x.b.Accounts().Update(x.ctx, api.AccountUpdateParams{AccountID: x.jira, Config: cfg}); err != nil {
		x.t.Fatal(err)
	}
}

// fixture stores a testdata/mime sample in f the way a syncer does (the
// envelope, the body, the raw file), arrived age ago.
func (x *issueMailBox) fixture(f store.Folder, name string, age time.Duration) store.Message {
	x.t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "mime", name))
	if err != nil {
		x.t.Fatal(err)
	}
	parsed, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
	if err != nil {
		x.t.Fatal(err)
	}
	x.uid++
	row := &store.Message{AccountID: f.AccountID, FolderID: f.ID, UID: x.uid, Subject: parsed.Subject, From: parsed.From, To: parsed.To,
		Date: parsed.Date, InternalDate: time.Now().Add(-age), RFCMessageID: fmt.Sprintf("<%d.%s>", x.uid, name), Size: int64(len(raw))}
	if err := x.b.store.UpsertMessages(x.ctx, []*store.Message{row}); err != nil {
		x.t.Fatal(err)
	}
	if err := x.b.store.SetMessageBody(x.ctx, row.ID, store.BodyUpdate{Text: parsed.Text, HasHTML: parsed.HasHTML, Snippet: parsed.Snippet,
		Headers: parsed.Headers, State: store.BodyFetched}); err != nil {
		x.t.Fatal(err)
	}
	if _, err := x.b.store.WriteMessageRaw(x.ctx, f.AccountID, row.ID, bytes.NewReader(raw), 25<<20); err != nil {
		x.t.Fatal(err)
	}
	return x.recount(*row)
}

// note stores a notification of the site about the issue with the key.
func (x *issueMailBox) note(f store.Folder, key string, age time.Duration) store.Message {
	x.t.Helper()
	x.uid++
	row := &store.Message{AccountID: f.AccountID, FolderID: f.ID, UID: x.uid,
		From:    []api.Address{{Name: "Petr Svoboda (Jira)", Address: "jira@acme.atlassian.net"}},
		Subject: "[JIRA] (" + key + ") Printer on the 2nd floor", Date: time.Now().Add(-age), InternalDate: time.Now().Add(-age),
		RFCMessageID: fmt.Sprintf("<note-%d@acme.atlassian.net>", x.uid), Size: 100}
	if err := x.b.store.UpsertMessages(x.ctx, []*store.Message{row}); err != nil {
		x.t.Fatal(err)
	}
	if err := x.b.store.SetMessageBody(x.ctx, row.ID, store.BodyUpdate{Text: "Paper tray replaced.", Snippet: "Paper tray replaced.",
		State: store.BodyFetched}); err != nil {
		x.t.Fatal(err)
	}
	return x.recount(*row)
}

func (x *issueMailBox) recount(m store.Message) store.Message {
	x.t.Helper()
	if _, _, err := x.b.store.RecountFolder(x.ctx, m.FolderID); err != nil {
		x.t.Fatal(err)
	}
	return m
}

// receive does what a mail syncer does once it stored a body: the hook,
// then the announcement.
func (x *issueMailBox) receive(m store.Message) store.Message {
	x.t.Helper()
	x.b.afterMailStored(x.ctx, m.AccountID, m.ID, ingest.Policy{})
	got := x.message(m)
	x.b.SyncNotifier().NewMessage(api.NewMessageNotification{AccountID: api.AccountID(m.AccountID),
		FolderID: api.FolderID(m.FolderID), Message: toAPISummary(got)})
	return got
}

func (x *issueMailBox) message(m store.Message) store.Message {
	x.t.Helper()
	got, err := x.b.store.GetMessage(x.ctx, m.AccountID, m.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	return got
}

func (x *issueMailBox) hidden(m store.Message) bool { return x.message(m).Hidden }

// link is the link of the message, nil when it has none.
func (x *issueMailBox) link(m store.Message) *store.IssueMailLink {
	x.t.Helper()
	accounts, err := x.b.store.ListAccounts(x.ctx)
	if err != nil {
		x.t.Fatal(err)
	}
	for _, a := range accounts {
		after := ""
		for {
			links, err := x.b.store.IssueMailLinks(x.ctx, a.ID, after, 100)
			if err != nil {
				x.t.Fatal(err)
			}
			for _, l := range links {
				after = l.MessageID
				if l.MessageID == m.ID {
					return &l
				}
			}
			if len(links) < 100 {
				break
			}
		}
	}
	return nil
}

// settle waits for the notifications gathered to go out.
func (x *issueMailBox) settle() {
	time.Sleep(x.quiet)
}

// changed returns what the notify.messagesChanged since the last call
// named, as "account:folder,folder" an account in the accounts' order
// (several notifications of one account are one entry: how many went out
// depends on how long the work took).
func (x *issueMailBox) changed() []string {
	x.settle()
	x.rec.mu.Lock()
	defer x.rec.mu.Unlock()
	folders := map[string]map[string]bool{}
	for _, n := range x.rec.changed {
		acc := string(n.AccountID)
		if folders[acc] == nil {
			folders[acc] = map[string]bool{}
		}
		if len(n.FolderIDs) == 0 {
			folders[acc][""] = true
		}
		for _, f := range n.FolderIDs {
			folders[acc][string(f)] = true
		}
	}
	x.rec.changed = nil
	out := make([]string, 0, len(folders))
	for acc, set := range folders {
		var ids []string
		if !set[""] {
			for f := range set {
				ids = append(ids, f)
			}
			slices.Sort(ids)
		}
		out = append(out, acc+":"+strings.Join(ids, ","))
	}
	slices.Sort(out)
	return out
}

// passed tells core that the jira account's syncer finished a pass.
func (x *issueMailBox) passed(account api.AccountID) {
	now := time.Now()
	x.b.SyncNotifier().SyncState(api.SyncStateNotification{State: api.SyncState{AccountID: account, Status: api.SyncIdle, Progress: -1, LastSync: &now}})
}

func (x *issueMailBox) counts(account string, folder store.Folder) (unread, total int) {
	x.t.Helper()
	res, err := x.b.Folders().List(x.ctx, api.FolderListParams{AccountID: api.AccountID(account)})
	if err != nil {
		x.t.Fatal(err)
	}
	for _, f := range res.Folders {
		if string(f.ID) == folder.ID {
			return f.Unread, f.Total
		}
	}
	x.t.Fatalf("no folder %s", folder.ID)
	return 0, 0
}

func hides(c *api.AccountConfig) { c.Jira.NotificationMail = api.NotificationMailHide }

// A notification is linked to its issue, and the issue refreshed: waited
// for when the mail is fresh and the issue not stored, asked for
// otherwise, left alone when nothing new can come of it.
func TestIssueMailLinksAndRefreshes(t *testing.T) {
	x := newIssueMailBox(t)
	jira := string(x.jira)
	x.sup.refresh = func(_, key string) error {
		x.putIssue(x.jira, "1"+strings.TrimPrefix(key, "ITSD-"), key, time.Now())
		return nil
	}

	// An hour old, its issue stored a day ago: asked for.
	m := x.receive(x.fixture(x.inbox, "jira-notification-cloud.eml", time.Hour))
	if l := x.link(m); l == nil || l.IssueAccountID != jira || l.IssueKey != "ITSD-42" || l.IssueID != "10042" {
		t.Fatalf("link = %+v", l)
	}
	if got := x.sup.asked(jira); !slices.Equal(got, []string{"issue:ITSD-42"}) {
		t.Fatalf("asked %v", got)
	}
	if is, _ := x.b.store.IssueByKey(x.ctx, jira, "ITSD-42"); !is.ViaMail {
		t.Fatal("the issue is not marked as named by a mail")
	}
	// Fresh, its issue not stored: waited for, and the link learns it.
	m = x.receive(x.fixture(x.inbox, "jira-notification-automation.eml", time.Minute))
	if got := x.sup.asked(jira); !slices.Equal(got, []string{"wait:ITSD-20"}) {
		t.Fatalf("asked %v", got)
	}
	if l := x.link(m); l == nil || l.IssueKey != "ITSD-20" {
		t.Fatalf("link = %+v", l)
	}
	if is, err := x.b.store.IssueByKey(x.ctx, jira, "ITSD-20"); err != nil || !is.ViaMail {
		t.Fatalf("the issue that arrived: %+v, %v", is, err)
	}
	// Fresh, its issue stored meanwhile and newer than the mail: nothing.
	x.receive(x.note(x.inbox, "ITSD-20", time.Minute))
	// Fresh, its issue stored but older than the mail: asked for, not
	// waited for.
	x.receive(x.note(x.inbox, "ITSD-42", time.Minute))
	// Not fresh, not stored: asked for.
	x.receive(x.note(x.archive, "WEB-7", 16*time.Minute))
	// In another mail account.
	x.receive(x.note(x.otherInbox, "WEB-8", 2*time.Hour))
	// Older than the account's window (30 days): linked, not asked for.
	old := x.receive(x.note(x.inbox, "WEB-9", 31*24*time.Hour))
	if got := x.sup.asked(jira); !slices.Equal(got, []string{"issue:ITSD-42", "issue:WEB-7", "issue:WEB-8"}) {
		t.Fatalf("asked %v", got)
	}
	if l := x.link(old); l == nil || l.IssueKey != "WEB-9" || l.IssueID != "" {
		t.Fatalf("link of the old mail = %+v", l)
	}
	// A date in the future is no news either way, and no reason to wait.
	x.receive(x.note(x.inbox, "WEB-10", -48*time.Hour))
	if got := x.sup.asked(jira); !slices.Equal(got, []string{"issue:WEB-10"}) {
		t.Fatalf("asked for a mail from the future: %v", got)
	}

	// What is no notification is left alone.
	for _, name := range []string{
		"jira-notification-spoofed-name.eml", "jira-notification-two-senders.eml", "jira-notification-duplicate-headers.eml",
		"jira-notification-lookalike-key.eml", "jira-notification-lookalike-sender.eml", "jira-notification-subdomain.eml",
		"jira-notification-unselected.eml", "jira-notification-long-subject.eml", "jira-notification-header-injection.eml",
		"simple-text.eml", "no-envelope.eml", "huge-header.eml", "crlf-injection.eml",
	} {
		m := x.receive(x.fixture(x.inbox, name, time.Minute))
		if l := x.link(m); l != nil {
			t.Errorf("%s is linked: %+v", name, l)
		}
	}
	if got := x.sup.asked(jira); len(got) != 0 {
		t.Fatalf("asked %v for mail that is no notification", got)
	}
	// What is one, however it was written.
	for name, key := range map[string]string{"jira-notification-encoded.eml": "ITSD-42", "jira-notification-folded.eml": "ITSD-42"} {
		m := x.receive(x.fixture(x.inbox, name, time.Hour))
		if l := x.link(m); l == nil || l.IssueKey != key {
			t.Errorf("%s: link %+v", name, l)
		}
	}
	// Nothing was hidden, and everything announced: the account only
	// synchronises.
	if got := x.changed(); len(got) != 0 {
		t.Fatalf("messagesChanged %v", got)
	}
	if n := len(x.rec.announced()); n != 8+13+2 {
		t.Fatalf("%d messages announced: %v", n, x.rec.announced())
	}
	if u, n := x.counts(x.mail, x.inbox); u != n || n != 6+13+2 {
		t.Fatalf("inbox counts %d/%d", u, n)
	}
}

// A mail syncer waits for few issues, and for none twice.
func TestIssueMailWaitsAreBounded(t *testing.T) {
	x := newIssueMailBox(t, hides)
	jira := string(x.jira)
	for i := range issueMailWaits + 3 {
		x.receive(x.note(x.inbox, fmt.Sprintf("WEB-%d", i+1), time.Minute))
	}
	got := x.sup.asked(jira)
	waits := 0
	for _, c := range got {
		if strings.HasPrefix(c, "wait:") {
			waits++
		}
	}
	if waits != issueMailWaits || len(got) != issueMailWaits+3 || got[len(got)-1] != fmt.Sprintf("issue:WEB-%d", issueMailWaits+3) {
		t.Fatalf("asked %v", got)
	}
	// An issue the site does not give is asked for once, however many
	// messages name it: waited for, or asked for.
	y := newIssueMailBox(t, hides)
	for range 5 {
		y.receive(y.note(y.inbox, "WEB-1", time.Minute))
		y.receive(y.note(y.inbox, "WEB-2", time.Hour))
	}
	for _, c := range y.sup.recorded() {
		// Every wait is the bounded one.
		if strings.HasPrefix(c, "wait:") && !strings.HasSuffix(c, ":"+issueMailWait.String()) {
			t.Fatalf("wait %q", c)
		}
	}
	if got := y.sup.asked(string(y.jira)); !slices.Equal(got, []string{"wait:WEB-1", "issue:WEB-2"}) {
		t.Fatalf("asked %v", got)
	}
	// Once it is there, every message newer than it asks again.
	y.putIssue(y.jira, "20001", "WEB-1", time.Now().Add(-time.Hour))
	y.receive(y.note(y.inbox, "WEB-1", time.Minute))
	y.receive(y.note(y.inbox, "WEB-1", time.Minute))
	y.receive(y.note(y.inbox, "WEB-1", 2*time.Hour))
	if got := y.sup.asked(string(y.jira)); !slices.Equal(got, []string{"issue:WEB-1", "issue:WEB-1"}) {
		t.Fatalf("asked %v for the stored issue", got)
	}
	// A request nobody heard (no syncer runs for the account) is made
	// again by the next message.
	y.sup.refuse = true
	for range 2 {
		y.receive(y.note(y.inbox, "WEB-3", time.Minute))
		y.receive(y.note(y.inbox, "WEB-4", time.Hour))
	}
	y.sup.refuse = false
	if got := y.sup.asked(string(y.jira)); !slices.Equal(got, []string{"wait:WEB-3", "issue:WEB-4", "wait:WEB-3", "issue:WEB-4"}) {
		t.Fatalf("asked %v of an account that does not synchronise", got)
	}
	// And after a while the site is asked again for the other.
	again := issueMailAskAgain
	issueMailAskAgain = 0
	t.Cleanup(func() { issueMailAskAgain = again })
	y.receive(y.note(y.inbox, "WEB-2", time.Hour))
	if got := y.sup.asked(string(y.jira)); !slices.Equal(got, []string{"issue:WEB-2"}) {
		t.Fatalf("asked %v after a while", got)
	}
}

// A message is hidden only when its sender is the site's, its subject
// names an issue of a selected space, the issue is stored, and the
// account hides its notifications.
func TestIssueMailHidesOnlyWhenAllHold(t *testing.T) {
	type tc struct {
		name    string
		edit    func(*api.AccountConfig)
		before  func(x *issueMailBox)
		fixture string
		key     string // a note about this issue instead of a fixture
		linked  bool
		hidden  bool
	}
	disable := func(x *issueMailBox) {
		if _, err := x.b.Accounts().SetEnabled(x.ctx, api.AccountSetEnabledParams{AccountID: x.jira, Enabled: false}); err != nil {
			x.t.Fatal(err)
		}
	}
	for _, c := range []tc{
		{name: "all hold", edit: hides, fixture: "jira-notification-cloud.eml", linked: true, hidden: true},
		{name: "all hold, an automation's mail", edit: hides, fixture: "jira-notification-automation.eml", linked: true, hidden: true,
			before: func(x *issueMailBox) { x.putIssue(x.jira, "10020", "ITSD-20", time.Now()) }},
		{name: "all hold, an encoded subject", edit: hides, fixture: "jira-notification-encoded.eml", linked: true, hidden: true},
		{name: "the account only synchronises", fixture: "jira-notification-cloud.eml", linked: true},
		{name: "the account synchronises by name", edit: func(c *api.AccountConfig) { c.Jira.NotificationMail = api.NotificationMailSync },
			fixture: "jira-notification-cloud.eml", linked: true},
		{name: "the account ignores its mail", edit: func(c *api.AccountConfig) { c.Jira.NotificationMail = api.NotificationMailIgnore },
			fixture: "jira-notification-cloud.eml"},
		{name: "the account is paused", edit: hides, before: disable, fixture: "jira-notification-cloud.eml"},
		{name: "the issue is not stored", edit: hides, key: "ITSD-43", linked: true},
		{name: "the issue is not stored and does not arrive", edit: hides, fixture: "jira-notification-automation.eml", linked: true},
		{name: "another account's issue has the key", edit: hides, key: "WEB-5", linked: true,
			before: func(x *issueMailBox) {
				other := jiraConfig()
				other.Email, other.Jira.Login, other.Jira.SiteURL, other.Jira.CloudID = "jana@other.test", "jana@other.test", "https://other.atlassian.net", ""
				x.putIssue(x.addTracker(other), "20005", "WEB-5", time.Now())
			}},
		{name: "a space that is not selected", edit: hides, fixture: "jira-notification-unselected.eml",
			before: func(x *issueMailBox) { x.putIssue(x.jira, "10107", "OPS-7", time.Now()) }},
		{name: "a stranger under the site's name", edit: hides, fixture: "jira-notification-spoofed-name.eml"},
		{name: "a stranger beside the site", edit: hides, fixture: "jira-notification-two-senders.eml"},
		{name: "a stranger's header first", edit: hides, fixture: "jira-notification-duplicate-headers.eml"},
		{name: "a host that looks like the site's", edit: hides, fixture: "jira-notification-lookalike-sender.eml"},
		{name: "a host that starts like the site's", edit: hides, fixture: "jira-notification-subdomain.eml"},
		{name: "a key that looks like the issue's", edit: hides, fixture: "jira-notification-lookalike-key.eml"},
		{name: "a key past the subject's bound", edit: hides, fixture: "jira-notification-long-subject.eml"},
		{name: "headers in an encoded word", edit: hides, fixture: "jira-notification-header-injection.eml"},
		{name: "senders that are not the site's", fixture: "jira-notification-cloud.eml",
			edit: func(c *api.AccountConfig) { hides(c); c.Jira.NotificationSenders = []string{"jira@acme.example.org"} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			var edits []func(*api.AccountConfig)
			if c.edit != nil {
				edits = append(edits, c.edit)
			}
			x := newIssueMailBox(t, edits...)
			if c.before != nil {
				c.before(x)
			}
			x.rec.takeChanged()
			var m store.Message
			if c.key != "" {
				m = x.note(x.inbox, c.key, time.Minute)
			} else {
				m = x.fixture(x.inbox, c.fixture, time.Minute)
			}
			plain := x.receive(x.fixture(x.inbox, "simple-text.eml", time.Minute))
			m = x.receive(m)
			if got := x.link(m) != nil; got != c.linked {
				t.Errorf("linked = %v", got)
			}
			if m.Hidden != c.hidden {
				t.Errorf("hidden = %v", m.Hidden)
			}
			if x.hidden(plain) {
				t.Error("an ordinary message is hidden")
			}
			// Whatever happened to it, the message is what the server
			// sent: its flags and folder are untouched, nothing is queued
			// for the server, and it is found by its id.
			if len(m.Flags) != 0 || m.FolderID != x.inbox.ID || m.BodyState != store.BodyFetched {
				t.Errorf("the message changed: %+v", m)
			}
			var ops int
			if err := x.b.store.DB().QueryRowContext(x.ctx, `SELECT COUNT(*) FROM message_ops`).Scan(&ops); err != nil || ops != 0 {
				t.Errorf("%d operations queued for the server, %v", ops, err)
			}
			if _, err := x.b.Messages().Get(x.ctx, api.MessageGetParams{AccountID: api.AccountID(x.mail), MessageID: api.MessageID(m.ID)}); err != nil {
				t.Errorf("message.get: %v", err)
			}
			wantAnnounced, wantTotal, wantChanged := 2, 2, []string{}
			if c.hidden {
				wantAnnounced, wantTotal, wantChanged = 1, 1, []string{x.mail + ":" + x.inbox.ID}
			}
			if got := x.changed(); !slices.Equal(got, wantChanged) {
				t.Errorf("messagesChanged %v, want %v", got, wantChanged)
			}
			if got := x.rec.announced(); len(got) != wantAnnounced || (c.hidden && got[0] != plain.Subject) {
				t.Errorf("announced %v", got)
			}
			if u, n := x.counts(x.mail, x.inbox); u != wantTotal || n != wantTotal {
				t.Errorf("counts %d/%d, want %d", u, n, wantTotal)
			}
		})
	}
}

// The first of several accounts that know a message takes it.
func TestIssueMailFirstAccountWins(t *testing.T) {
	x := newIssueMailBox(t) // the first account only synchronises
	second := jiraConfig()
	second.Email, second.Jira.Login = "petr@acme.test", "petr@acme.test" // another user of the same site
	hides(&second)
	other := x.addTracker(second)
	x.putIssue(other, "10042", "ITSD-42", time.Now())
	x.putIssue(other, "10077", "MOB-77", time.Now())
	a, _ := x.b.store.GetAccount(x.ctx, string(other))
	a.Config.Jira.Spaces = append(a.Config.Jira.Spaces, api.SpaceRef{ID: "10002", Key: "MOB"})
	if _, err := x.b.Accounts().Update(x.ctx, api.AccountUpdateParams{AccountID: other, Config: a.Config}); err != nil {
		t.Fatal(err)
	}
	if err := x.b.store.SetIssueSpaces(x.ctx, string(other), []store.IssueSpace{{SpaceID: "10000", Key: "ITSD"}, {SpaceID: "10001", Key: "WEB"}, {SpaceID: "10002", Key: "MOB"}}); err != nil {
		t.Fatal(err)
	}
	x.b.invalidateIssueTrackers()

	both := x.receive(x.note(x.inbox, "ITSD-42", time.Hour))
	if l := x.link(both); l == nil || l.IssueAccountID != string(x.jira) || both.Hidden {
		t.Fatalf("a message both know: %+v, hidden %v", l, both.Hidden)
	}
	only := x.receive(x.note(x.inbox, "MOB-77", time.Hour))
	if l := x.link(only); l == nil || l.IssueAccountID != string(other) || !only.Hidden {
		t.Fatalf("a message the second knows: %+v, hidden %v", l, only.Hidden)
	}
	// A jira account's own messages are never anybody's notification.
	folders := seedFolders(t, x.b, string(x.jira), []store.Folder{{Mailbox: "space:10000", Name: "IT Service Desk", Path: "IT Service Desk", Subscribed: true, Selectable: true}})
	own := x.note(folders["space:10000"], "ITSD-42", time.Hour)
	x.b.noteIssueMail(x.ctx, string(x.jira), own.ID)
	if x.link(own) != nil || x.hidden(own) {
		t.Fatal("an issue-tracker account's own message was taken for a notification")
	}
}

// A hidden message is never announced; everything else is.
func TestHiddenMailIsNotAnnounced(t *testing.T) {
	x := newIssueMailBox(t, hides)
	hidden := x.receive(x.fixture(x.inbox, "jira-notification-cloud.eml", time.Minute))
	shown := x.receive(x.fixture(x.inbox, "jira-notification-unselected.eml", time.Minute))
	if !hidden.Hidden || shown.Hidden {
		t.Fatalf("hidden %v, shown %v", hidden.Hidden, shown.Hidden)
	}
	// An issue-tracker account's own news, and one about a message the
	// store does not have.
	n := x.b.SyncNotifier()
	n.NewMessage(api.NewMessageNotification{AccountID: x.jira, FolderID: "f_space", Message: api.MessageSummary{ID: "m_item", ThreadID: "jira:10042", Subject: "ITSD-42: Printer"}})
	n.NewMessage(api.NewMessageNotification{AccountID: api.AccountID(x.mail), FolderID: api.FolderID(x.inbox.ID), Message: api.MessageSummary{ID: "m_gone", Subject: "gone"}})
	x.settle()
	if got := x.rec.announced(); !slices.Equal(got, []string{shown.Subject, "ITSD-42: Printer", "gone"}) {
		t.Fatalf("announced %v", got)
	}
	// Shown again, it is announced like any other (a syncer that stored
	// its body late).
	x.update(func(c *api.JiraConfig) { c.NotificationMail = api.NotificationMailSync })
	n.NewMessage(api.NewMessageNotification{AccountID: api.AccountID(x.mail), FolderID: api.FolderID(x.inbox.ID), Message: toAPISummary(x.message(hidden))})
	x.settle()
	if got := x.rec.announced(); len(got) != 4 || got[3] != hidden.Subject {
		t.Fatalf("announced %v", got)
	}
}

// What is hidden follows the account: its mode, whether it is enabled,
// its spaces, its senders, its issues, and the account itself.
func TestIssueMailFollowsTheAccount(t *testing.T) {
	x := newIssueMailBox(t, hides)
	x.putIssue(x.jira, "20007", "WEB-7", time.Now())
	itsd := x.receive(x.fixture(x.inbox, "jira-notification-cloud.eml", time.Hour))
	filed := x.receive(x.note(x.archive, "ITSD-42", 2*time.Hour))
	web := x.receive(x.note(x.otherInbox, "WEB-7", time.Hour))
	pending := x.receive(x.note(x.inbox, "WEB-8", time.Hour)) // its issue is not stored
	plain := x.receive(x.fixture(x.inbox, "simple-text.eml", time.Hour))
	all := []store.Message{itsd, filed, web, pending, plain}
	state := func() string {
		var sb strings.Builder
		for _, m := range all {
			if x.hidden(m) {
				sb.WriteByte('h')
			} else {
				sb.WriteByte('-')
			}
		}
		return sb.String()
	}
	folders := func(account string, fs ...store.Folder) string {
		ids := make([]string, len(fs))
		for i, f := range fs {
			ids[i] = f.ID
		}
		slices.Sort(ids)
		return account + ":" + strings.Join(ids, ",")
	}
	mailBoth, mailInbox, otherOnly := folders(x.mail, x.inbox, x.archive), folders(x.mail, x.inbox), folders(x.other, x.otherInbox)
	step := func(name, want string, changed ...string) {
		t.Helper()
		slices.Sort(changed)
		got := x.changed()
		if s := state(); s != want || !slices.Equal(got, changed) {
			t.Fatalf("%s: hidden %s, want %s; messagesChanged %v, want %v", name, s, want, got, changed)
		}
	}
	step("received", "hhh--", mailBoth, otherOnly)
	if u, n := x.counts(x.mail, x.inbox); u != 2 || n != 2 {
		t.Fatalf("inbox counts %d/%d", u, n)
	}

	x.update(func(c *api.JiraConfig) { c.NotificationMail = api.NotificationMailSync })
	step("the account only synchronises", "-----", mailBoth, otherOnly)
	if u, n := x.counts(x.mail, x.inbox); u != 3 || n != 3 {
		t.Fatalf("inbox counts %d/%d", u, n)
	}
	x.update(func(c *api.JiraConfig) { c.NotificationMail = api.NotificationMailHide })
	step("the account hides again", "hhh--", mailBoth, otherOnly)
	x.update(func(c *api.JiraConfig) { c.NotificationMail = api.NotificationMailIgnore })
	step("the account ignores its mail", "-----", mailBoth, otherOnly)
	x.update(func(c *api.JiraConfig) { c.NotificationMail = api.NotificationMailHide })
	step("the account hides once more", "hhh--", mailBoth, otherOnly)
	x.update(func(c *api.JiraConfig) { c.HideEvents = true })
	step("a change that is none", "hhh--")

	setEnabled := func(on bool) {
		t.Helper()
		if _, err := x.b.Accounts().SetEnabled(x.ctx, api.AccountSetEnabledParams{AccountID: x.jira, Enabled: on}); err != nil {
			t.Fatal(err)
		}
	}
	setEnabled(false)
	step("the account is paused", "-----", mailBoth, otherOnly)
	setEnabled(true)
	step("the account is resumed", "hhh--", mailBoth, otherOnly)

	// A space is deselected: its mail is shown at once, whatever the
	// stored issues still say; selected again, hidden.
	x.update(func(c *api.JiraConfig) { c.Spaces = c.Spaces[:1] })
	step("a space is deselected", "hh---", otherOnly)
	if x.link(web) != nil {
		t.Fatal("the link of a deselected space's mail stays")
	}
	x.update(func(c *api.JiraConfig) { c.Spaces = append(c.Spaces, api.SpaceRef{ID: "10001", Key: "WEB"}) })
	step("the space is selected again", "hhh--", otherOnly)

	// The senders change: the site's address is not among them.
	x.update(func(c *api.JiraConfig) { c.NotificationSenders = []string{"jira@acme.example.org"} })
	step("other senders", "-----", mailBoth, otherOnly)
	x.update(func(c *api.JiraConfig) { c.NotificationSenders = []string{"JIRA@acme.atlassian.net"} })
	step("the site's address", "hhh--", mailBoth, otherOnly)

	// An issue arrives (the syncer stored it), another leaves.
	x.putIssue(x.jira, "20008", "WEB-8", time.Now())
	x.b.afterIssueStored(x.ctx, string(x.jira), "m_item", ingest.Policy{})
	step("an issue arrived", "hhhh-", mailInbox)
	touched, err := x.b.store.DeleteIssues(x.ctx, string(x.jira), []string{"10042"})
	if err != nil {
		t.Fatal(err)
	}
	// As the syncer reports it.
	for acc, fs := range touched {
		ids := make([]api.FolderID, len(fs))
		for i, f := range fs {
			ids[i] = api.FolderID(f)
		}
		x.b.SyncNotifier().MessagesChanged(api.MessagesChangedNotification{AccountID: api.AccountID(acc), FolderIDs: ids})
	}
	step("an issue left", "--hh-", mailBoth)

	// The account is removed: everything is shown, and its links go.
	if _, err := x.b.Accounts().Remove(x.ctx, api.AccountRemoveParams{AccountID: x.jira, DeleteLocalData: true}); err != nil {
		t.Fatal(err)
	}
	step("the account is removed", "-----", mailInbox, otherOnly)
	for _, m := range all {
		if x.link(m) != nil {
			t.Fatalf("a link of the removed account stays: %s", m.Subject)
		}
	}
	if _, ok, _ := x.b.store.GetMeta(x.ctx, metaIssueMailSettled+string(x.jira)); ok {
		t.Fatal("the removed account's record stays")
	}
	if u, n := x.counts(x.mail, x.inbox); u != 3 || n != 3 {
		t.Fatalf("inbox counts at the end %d/%d", u, n)
	}
	x.sup.reset()
	late := x.receive(x.note(x.inbox, "ITSD-42", time.Minute))
	if got := x.sup.asked(string(x.jira)); len(got) != 0 || late.Hidden || x.link(late) != nil {
		t.Fatalf("asked %v of a removed account", got)
	}
}

// Mail that was stored before the account took it is found when the
// account's first pass ends, and when the account is told to hide.
func TestIssueMailBackfill(t *testing.T) {
	x := newIssueMailBox(t, hides)
	jira := string(x.jira)
	x.putIssue(x.jira, "20007", "WEB-7", time.Now())
	inWindow := []store.Message{
		x.fixture(x.inbox, "jira-notification-cloud.eml", 3*24*time.Hour),
		x.fixture(x.archive, "jira-notification-encoded.eml", 29*24*time.Hour),
		x.note(x.otherInbox, "WEB-7", time.Hour),
	}
	notStored := x.note(x.inbox, "WEB-8", 5*24*time.Hour)
	tooOld := x.note(x.inbox, "ITSD-42", 31*24*time.Hour)
	strangers := []store.Message{
		x.fixture(x.inbox, "jira-notification-spoofed-name.eml", time.Hour),
		x.fixture(x.inbox, "jira-notification-two-senders.eml", time.Hour),
		x.fixture(x.inbox, "jira-notification-unselected.eml", time.Hour),
		x.fixture(x.inbox, "jira-notification-lookalike-key.eml", time.Hour),
		x.fixture(x.inbox, "simple-text.eml", time.Hour),
	}
	check := func(name string, hidden bool) {
		t.Helper()
		for _, m := range inWindow {
			if x.hidden(m) != hidden {
				t.Fatalf("%s: %q hidden = %v", name, m.Subject, !hidden)
			}
		}
		for _, m := range append(strangers, notStored, tooOld) {
			if x.hidden(m) {
				t.Fatalf("%s: %q is hidden", name, m.Subject)
			}
		}
	}
	check("before the first pass", false)
	if got := x.changed(); len(got) != 0 {
		t.Fatalf("messagesChanged %v", got)
	}

	// A pass of a mail account is none of the feature's business.
	x.passed(api.AccountID(x.mail))
	check("after a mail account's pass", false)
	// A failed or unfinished pass of the account neither.
	x.b.SyncNotifier().SyncState(api.SyncStateNotification{State: api.SyncState{AccountID: x.jira, Status: api.SyncSyncing, Progress: 50}})
	x.b.SyncNotifier().SyncState(api.SyncStateNotification{State: api.SyncState{AccountID: x.jira, Status: api.SyncIdle, Progress: -1}})
	check("before the pass ends", false)

	x.passed(x.jira)
	check("after the first pass", true)
	want := sortedStrings(x.mail+":"+strings.Join(sortedStrings(x.inbox.ID, x.archive.ID), ","), x.other+":"+x.otherInbox.ID)
	if got := x.changed(); !slices.Equal(got, want) { // the accounts in order
		t.Fatalf("messagesChanged %v, want %v", got, want)
	}
	// The issue that is not stored was asked for, once; nothing else.
	if got := x.sup.asked(jira); !slices.Equal(got, []string{"issue:WEB-8"}) {
		t.Fatalf("asked %v", got)
	}
	if l := x.link(notStored); l == nil || l.IssueKey != "WEB-8" {
		t.Fatalf("link of the mail whose issue is not stored: %+v", l)
	}
	if x.link(tooOld) != nil || x.link(strangers[0]) != nil {
		t.Fatal("mail out of the window or of a stranger is linked")
	}
	if u, n := x.counts(x.mail, x.inbox); u != 7 || n != 7 {
		t.Fatalf("inbox counts %d/%d", u, n)
	}

	// Once is enough: later passes read nothing (a message put into the
	// store behind the feature's back stays as it is).
	late := x.note(x.inbox, "ITSD-42", time.Hour)
	x.passed(x.jira)
	if x.hidden(late) || x.link(late) != nil {
		t.Fatal("a later pass looked through the mail again")
	}
	// A configuration that changes what is matched looks again: here the
	// window grows to take the old message in.
	x.update(func(c *api.JiraConfig) { c.OfflineDays = 60 })
	if !x.hidden(late) || !x.hidden(tooOld) {
		t.Fatal("the grown window did not take the older mail in")
	}
	check2 := x.changed()
	if !slices.Equal(check2, []string{x.mail + ":" + x.inbox.ID}) {
		t.Fatalf("messagesChanged %v", check2)
	}
	// And a restart of the daemon does not: the record is in the store.
	x.b.invalidateIssueTrackers()
	again := x.note(x.inbox, "ITSD-42", time.Hour)
	x.passed(x.jira)
	if x.hidden(again) {
		t.Fatal("the pass after a restart looked through the mail again")
	}
}

// The scan reads in batches and asks for a bounded number of issues.
func TestIssueMailBackfillIsBounded(t *testing.T) {
	batch, limit, triggers := issueMailBatch, issueMailScanLimit, issueMailTriggerLimit
	issueMailBatch, issueMailScanLimit, issueMailTriggerLimit = 7, 40, 5
	t.Cleanup(func() { issueMailBatch, issueMailScanLimit, issueMailTriggerLimit = batch, limit, triggers })
	x := newIssueMailBox(t, hides)
	x.putIssue(x.jira, "10042", "ITSD-42", time.Now()) // newer than any mail about it
	var stored, unknown []store.Message
	for i := range 30 {
		stored = append(stored, x.note(x.inbox, "ITSD-42", time.Duration(i+1)*time.Hour))
	}
	for i := range 25 {
		unknown = append(unknown, x.note(x.inbox, fmt.Sprintf("WEB-%d", i+1), time.Hour))
		if i%2 == 0 {
			unknown = append(unknown, x.note(x.inbox, fmt.Sprintf("WEB-%d", i+1), 2*time.Hour)) // the same issue twice
		}
	}
	x.passed(x.jira)
	hidden, linked := 0, 0
	for _, m := range stored {
		if x.hidden(m) {
			hidden++
		}
	}
	for _, m := range append(stored, unknown...) {
		if x.link(m) != nil {
			linked++
		}
	}
	// 42 messages are read (six batches of seven reach the limit of 40).
	if linked != 42 || hidden == 0 || hidden > 30 {
		t.Fatalf("%d linked, %d hidden", linked, hidden)
	}
	asked := x.sup.asked(string(x.jira))
	if len(asked) != 5 {
		t.Fatalf("asked for %d issues: %v", len(asked), asked)
	}
	seen := map[string]bool{}
	for _, a := range asked {
		if seen[a] || !strings.HasPrefix(a, "issue:WEB-") {
			t.Fatalf("asked %v", asked)
		}
		seen[a] = true
	}
	if got := x.changed(); !slices.Equal(got, []string{x.mail + ":" + x.inbox.ID}) {
		t.Fatalf("messagesChanged %v", got)
	}
}

// Whatever is hidden in many folders of several accounts at once is told
// once an account, with its folders in order.
func TestMessagesChangedIsGathered(t *testing.T) {
	x := newIssueMailBox(t, hides)
	issueMailCoalesce = 2 * time.Second // longer than the work below takes, on any machine
	for i := range 20 {
		f := []store.Folder{x.inbox, x.archive, x.otherInbox}[i%3]
		x.receive(x.note(f, "ITSD-42", time.Hour))
	}
	// The syncer's own reports join them, a report without folders means
	// every folder.
	n := x.b.SyncNotifier()
	n.MessagesChanged(api.MessagesChangedNotification{AccountID: api.AccountID(x.mail), FolderIDs: []api.FolderID{"f_other", api.FolderID(x.inbox.ID)}})
	n.MessagesChanged(api.MessagesChangedNotification{AccountID: "acc_third"})
	n.MessagesChanged(api.MessagesChangedNotification{AccountID: "acc_third", FolderIDs: []api.FolderID{"f_1"}})
	n.MessagesChanged(api.MessagesChangedNotification{})
	want := []string{
		"acc_third:",
		x.mail + ":" + strings.Join(sortedStrings(x.inbox.ID, x.archive.ID, "f_other"), ","),
		x.other + ":" + x.otherInbox.ID,
	}
	slices.Sort(want)
	if got := x.rec.takeChanged(); len(got) != 0 {
		t.Fatalf("messagesChanged before its time: %v", got)
	}
	var got []string
	deadline := time.Now().Add(10 * time.Second)
	for len(got) < len(want) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		got = append(got, x.rec.takeChanged()...)
	}
	time.Sleep(100 * time.Millisecond)
	got = append(got, x.rec.takeChanged()...)
	if !slices.Equal(got, want) { // once an account, the accounts in order
		t.Fatalf("messagesChanged %v, want %v", got, want)
	}
	// After Close nothing goes out.
	issueMailCoalesce, x.quiet = 300*time.Millisecond, 500*time.Millisecond
	x.receive(x.note(x.inbox, "ITSD-42", time.Hour))
	x.b.Close()
	if got := x.changed(); len(got) != 0 {
		t.Fatalf("messagesChanged after Close: %v", got)
	}
}

// A hidden message is in no listing, conversation, search result or
// count, and still there by its id.
func TestHiddenMailLeavesEveryListing(t *testing.T) {
	old := searchTimeZone
	searchTimeZone = time.UTC
	t.Cleanup(func() { searchTimeZone = old })
	x := newIssueMailBox(t, hides)
	mail := api.AccountID(x.mail)
	plain := x.receive(x.fixture(x.inbox, "simple-text.eml", 3*time.Hour))
	// A conversation of three: a question by a colleague, the site's
	// notification in reply, and the colleague's answer.
	question := &store.Message{AccountID: x.mail, FolderID: x.inbox.ID, UID: 900, Subject: "Printer on the 2nd floor",
		From: []api.Address{{Name: "Petr Svoboda", Address: "petr.svoboda@example.invalid"}}, Date: time.Now().Add(-2 * time.Hour),
		InternalDate: time.Now().Add(-2 * time.Hour), RFCMessageID: "<q@example.invalid>", Flags: []api.Flag{api.FlagSeen}}
	if err := x.b.store.UpsertMessages(x.ctx, []*store.Message{question}); err != nil {
		t.Fatal(err)
	}
	if err := x.b.store.SetMessageBody(x.ctx, question.ID, store.BodyUpdate{Text: "Is the printer jammed again?", Snippet: "Is the printer", State: store.BodyFetched}); err != nil {
		t.Fatal(err)
	}
	note := x.note(x.inbox, "ITSD-42", time.Hour)
	if _, err := x.b.store.DB().ExecContext(x.ctx, `UPDATE messages SET thread_id = ?, in_reply_to = ? WHERE id = ?`, question.ThreadID, question.RFCMessageID, note.ID); err != nil {
		t.Fatal(err)
	}
	note = x.receive(note)
	flagged := x.note(x.inbox, "ITSD-42", 30*time.Minute)
	if _, err := x.b.Messages().Flag(x.ctx, api.MessageFlagParams{AccountID: mail, MessageIDs: []api.MessageID{api.MessageID(flagged.ID)}, Set: []api.Flag{api.FlagFlagged}}); err != nil {
		t.Fatal(err)
	}
	flagged = x.receive(flagged)
	filed := x.receive(x.note(x.archive, "ITSD-42", time.Hour))
	for _, m := range []store.Message{note, flagged, filed} {
		if !m.Hidden {
			t.Fatalf("%s (%s) is not hidden", m.Subject, m.ID)
		}
	}
	hidden := map[api.MessageID]bool{api.MessageID(note.ID): true, api.MessageID(flagged.ID): true, api.MessageID(filed.ID): true}

	// folder.list
	if u, n := x.counts(x.mail, x.inbox); u != 1 || n != 2 {
		t.Fatalf("inbox counts %d/%d", u, n)
	}
	if u, n := x.counts(x.mail, x.archive); u != 0 || n != 0 {
		t.Fatalf("archive counts %d/%d", u, n)
	}
	// message.list, every filter and order.
	for _, p := range []api.MessageListParams{
		{}, {Filter: api.FilterUnread}, {Filter: api.FilterFlagged}, {Sort: api.SortDateAsc}, {Page: api.Page{Limit: 1}},
	} {
		for _, folder := range []store.Folder{x.inbox, x.archive} {
			p.AccountID, p.FolderID = mail, api.FolderID(folder.ID)
			p.Page.Cursor = ""
			seen := 0
			for {
				res, err := x.b.Messages().List(x.ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range res.Messages {
					seen++
					if hidden[m.ID] {
						t.Fatalf("message.list %+v shows %q", p, m.Subject)
					}
				}
				want := map[api.MessageFilter]int{"": 2, api.FilterUnread: 1, api.FilterFlagged: 0}[p.Filter]
				if folder.ID == x.archive.ID {
					want = 0
				}
				if res.Page.Total != want {
					t.Fatalf("message.list %+v: total %d, want %d", p, res.Page.Total, want)
				}
				if res.Page.NextCursor == "" {
					if seen != want {
						t.Fatalf("message.list %+v: %d messages, want %d", p, seen, want)
					}
					break
				}
				p.Page.Cursor = res.Page.NextCursor
			}
		}
	}
	// thread.list and thread.get.
	threads, err := x.b.Threads().List(x.ctx, api.ThreadListParams{AccountID: mail, FolderID: api.FolderID(x.inbox.ID)})
	if err != nil || len(threads.Threads) != 2 || threads.Page.Total != 2 {
		t.Fatalf("thread.list = %+v, %v", threads, err)
	}
	for _, th := range threads.Threads {
		if th.MessageCount != 1 || hidden[th.Latest.ID] {
			t.Fatalf("thread %+v", th)
		}
	}
	if archived, err := x.b.Threads().List(x.ctx, api.ThreadListParams{AccountID: mail, FolderID: api.FolderID(x.archive.ID)}); err != nil || len(archived.Threads) != 0 {
		t.Fatalf("thread.list of the archive = %+v, %v", archived, err)
	}
	for _, folder := range []api.FolderID{api.FolderID(x.inbox.ID), ""} {
		th, err := x.b.Threads().Get(x.ctx, api.ThreadGetParams{AccountID: mail, ThreadID: api.ThreadID(question.ThreadID), FolderID: folder})
		if err != nil || len(th.Messages) != 1 || string(th.Messages[0].ID) != question.ID || th.Thread.MessageCount != 1 || th.Thread.UnreadCount != 0 {
			t.Fatalf("thread.get in %q = %+v, %v", folder, th, err)
		}
	}
	if _, err := x.b.Threads().Get(x.ctx, api.ThreadGetParams{AccountID: mail, ThreadID: api.ThreadID(filed.ThreadID)}); errCode(t, err) != api.CodeThreadNotFound {
		t.Fatalf("thread.get of a conversation of hidden mail: %v", err)
	}
	// search.query, every scope.
	for _, q := range []string{"printer", "Paper tray", "from:jira@acme.atlassian.net", "subject:ITSD-42", "ITSD", "in:archive printer", "is:unread"} {
		for name, p := range map[string]api.SearchQueryParams{
			"folder": {AccountID: mail, FolderID: api.FolderID(x.inbox.ID)}, "archive": {AccountID: mail, FolderID: api.FolderID(x.archive.ID)},
			"account": {AccountID: mail}, "everywhere": {},
		} {
			p.Query = q
			res, err := x.b.Search().Query(x.ctx, p)
			if err != nil {
				t.Fatalf("search %q in %s: %v", q, name, err)
			}
			for _, r := range res.Results {
				if hidden[r.Message.ID] {
					t.Fatalf("search %q in %s finds the hidden %q", q, name, r.Message.Subject)
				}
			}
			if q == "printer" && name != "archive" && (len(res.Results) != 1 || string(res.Results[0].Message.ID) != question.ID || res.Page.Total != 1) {
				t.Fatalf("search %q in %s = %+v", q, name, res)
			}
		}
	}
	// By its id it is there, with its body, and can be flagged.
	got, err := x.b.Messages().Get(x.ctx, api.MessageGetParams{AccountID: mail, MessageID: api.MessageID(note.ID)})
	if err != nil || got.Message.Subject != note.Subject {
		t.Fatalf("message.get = %+v, %v", got, err)
	}
	if body, err := x.b.Messages().Body(x.ctx, api.MessageBodyParams{AccountID: mail, MessageID: api.MessageID(note.ID)}); err != nil || !strings.Contains(body.Text, "Paper tray") {
		t.Fatalf("message.body = %+v, %v", body, err)
	}
	if _, err := x.b.Messages().Flag(x.ctx, api.MessageFlagParams{AccountID: mail, MessageIDs: []api.MessageID{api.MessageID(note.ID)}, Set: []api.Flag{api.FlagSeen}}); err != nil {
		t.Fatal(err)
	}
	if u, n := x.counts(x.mail, x.inbox); u != 1 || n != 2 {
		t.Fatalf("inbox counts after a flag on hidden mail %d/%d", u, n)
	}

	// Shown again, everything is back, as the user left it.
	x.update(func(c *api.JiraConfig) { c.NotificationMail = api.NotificationMailSync })
	if u, n := x.counts(x.mail, x.inbox); u != 2 || n != 4 {
		t.Fatalf("inbox counts when shown %d/%d", u, n)
	}
	list, err := x.b.Messages().List(x.ctx, api.MessageListParams{AccountID: mail, FolderID: api.FolderID(x.inbox.ID)})
	if err != nil || len(list.Messages) != 4 {
		t.Fatalf("message.list when shown: %d, %v", len(list.Messages), err)
	}
	for _, m := range list.Messages {
		if string(m.ID) == note.ID && !hasFlagE2E(m.Flags, api.FlagSeen) {
			t.Fatal("the flag set while hidden is lost")
		}
	}
	th, err := x.b.Threads().Get(x.ctx, api.ThreadGetParams{AccountID: mail, ThreadID: api.ThreadID(question.ThreadID)})
	if err != nil || len(th.Messages) != 2 {
		t.Fatalf("thread.get when shown = %+v, %v", th, err)
	}
	res, err := x.b.Search().Query(x.ctx, api.SearchQueryParams{AccountID: mail, Query: "printer"})
	if err != nil || len(res.Results) != 4 {
		t.Fatalf("search when shown: %d, %v", len(res.Results), err)
	}
	_ = plain
}

// A Data Center site's mail comes from an address only its
// administrators know: nothing is a notification until it is configured.
func TestDataCenterNeedsSenders(t *testing.T) {
	x := newIssueMailBox(t, func(c *api.AccountConfig) {
		*c = dcConfig()
		c.Jira.Spaces = []api.SpaceRef{{ID: "10000", Key: "ITSD"}, {ID: "10001", Key: "WEB"}}
		hides(c)
	})
	jira := string(x.jira)
	from := []api.Address{{Name: "Jira", Address: "jira@acme.test"}}
	site := []api.Address{{Name: "Jira", Address: "jira@jira.acme.test"}}
	own := func(f store.Folder, from []api.Address, age time.Duration) store.Message {
		m := x.note(f, "ITSD-42", age)
		raw := `[{"name":"` + from[0].Name + `","address":"` + from[0].Address + `"}]`
		if _, err := x.b.store.DB().ExecContext(x.ctx, `UPDATE messages SET from_json = ? WHERE id = ?`, raw, m.ID); err != nil {
			t.Fatal(err)
		}
		return x.message(m)
	}
	first := x.receive(own(x.inbox, from, time.Hour))
	second := x.receive(own(x.inbox, site, time.Hour))
	cloud := x.receive(x.note(x.inbox, "ITSD-42", time.Hour)) // from a cloud site of the same name
	x.passed(x.jira)
	for _, m := range []store.Message{first, second, cloud} {
		if x.hidden(m) || x.link(m) != nil {
			t.Fatalf("%v matched without a configured sender", m.From)
		}
	}
	if got := x.sup.asked(jira); len(got) != 0 {
		t.Fatalf("asked %v", got)
	}
	if got := x.changed(); len(got) != 0 {
		t.Fatalf("messagesChanged %v", got)
	}
	if n := len(x.rec.announced()); n != 3 {
		t.Fatalf("%d announced", n)
	}

	x.update(func(c *api.JiraConfig) { c.NotificationSenders = []string{"jira@acme.test"} })
	if !x.hidden(first) || x.hidden(second) || x.hidden(cloud) {
		t.Fatalf("with the sender configured: %v %v %v", x.hidden(first), x.hidden(second), x.hidden(cloud))
	}
	if got := x.changed(); !slices.Equal(got, []string{x.mail + ":" + x.inbox.ID}) {
		t.Fatalf("messagesChanged %v", got)
	}
	third := x.receive(own(x.inbox, from, time.Minute))
	if !third.Hidden {
		t.Fatal("new mail of the configured sender is not hidden")
	}
	// The senders are taken away again.
	x.update(func(c *api.JiraConfig) { c.NotificationSenders = nil })
	if x.hidden(first) || x.hidden(third) || x.link(first) != nil {
		t.Fatal("mail stays hidden without a sender to know it by")
	}
}

// The hourly evaluation brings what is hidden in step with the store,
// whatever happened behind its back.
func TestMaintainReevaluatesIssueMail(t *testing.T) {
	x := newIssueMailBox(t, hides)
	x.putIssue(x.jira, "20007", "WEB-7", time.Now())
	gone := x.receive(x.note(x.inbox, "ITSD-42", time.Hour))
	kept := x.receive(x.note(x.inbox, "WEB-7", time.Hour))
	arrives := x.receive(x.note(x.archive, "WEB-8", time.Hour))
	stray := x.note(x.inbox, "WEB-7", time.Hour)
	if _, _, err := x.b.store.SetMessageHidden(x.ctx, stray.ID, true); err != nil { // hidden, no link: not the feature's
		t.Fatal(err)
	}
	x.changed()
	db := x.b.store.DB()
	if _, err := db.ExecContext(x.ctx, `DELETE FROM issues WHERE account_id = ? AND key = 'ITSD-42'`, x.jira); err != nil {
		t.Fatal(err)
	}
	x.putIssue(x.jira, "20008", "WEB-8", time.Now())
	// A link the matcher no longer agrees with: the subject is another.
	moved := x.receive(x.note(x.inbox, "WEB-7", time.Hour))
	if _, err := db.ExecContext(x.ctx, `UPDATE messages SET subject = '[JIRA] (WEB-8) Moved' WHERE id = ?`, moved.ID); err != nil {
		t.Fatal(err)
	}
	stale := x.receive(x.note(x.inbox, "WEB-7", time.Hour))
	if _, err := db.ExecContext(x.ctx, `UPDATE messages SET subject = 'Lunch?' WHERE id = ?`, stale.ID); err != nil {
		t.Fatal(err)
	}
	x.changed()

	x.b.reevaluateIssueMail(x.ctx)
	for m, want := range map[*store.Message]bool{&gone: false, &kept: true, &arrives: true, &stray: true, &moved: true, &stale: false} {
		if got := x.hidden(*m); got != want {
			t.Errorf("%q (%s) hidden = %v, want %v", m.Subject, m.ID, got, want)
		}
	}
	if l := x.link(moved); l == nil || l.IssueKey != "WEB-8" || l.IssueID != "20008" {
		t.Errorf("the link that moved: %+v", l)
	}
	if x.link(stale) != nil {
		t.Error("the link the matcher no longer agrees with stays")
	}
	if l := x.link(gone); l == nil {
		t.Error("the link of an issue that went is gone: the issue may come back")
	}
	want := []string{x.mail + ":" + strings.Join(sortedStrings(x.inbox.ID, x.archive.ID), ",")}
	if got := x.changed(); !slices.Equal(got, want) {
		t.Fatalf("messagesChanged %v, want %v", got, want)
	}
	// Nothing to do: nothing is told, and the stored mail is not read.
	late := x.note(x.inbox, "WEB-7", time.Hour)
	x.b.reevaluateIssueMail(x.ctx)
	if got := x.changed(); len(got) != 0 || x.hidden(late) {
		t.Fatalf("an evaluation with nothing to do: %v", got)
	}
	// The issue comes back.
	x.putIssue(x.jira, "10042", "ITSD-42", time.Now())
	x.b.reevaluateIssueMail(x.ctx)
	if !x.hidden(gone) {
		t.Fatal("the issue came back, its mail is shown")
	}
	// Cancelled, it stops without a word.
	ctx, cancel := context.WithCancel(x.ctx)
	cancel()
	x.b.reevaluateIssueMail(ctx)
}

// issueMailState tells the configurations apart that match differently,
// and no others.
func TestIssueMailState(t *testing.T) {
	base := func() store.Account {
		cfg := jiraConfig()
		return store.Account{ID: "acc_1", Enabled: true, Config: cfg}
	}
	same := []func(a *store.Account){
		func(a *store.Account) { a.Config.Name = "Other" },
		func(a *store.Account) { a.Config.Jira.NotificationMail = api.NotificationMailSync },
		func(a *store.Account) { a.Config.Jira.NotificationSenders = []string{"@acme.atlassian.net"} },
		func(a *store.Account) { a.Config.Jira.OfflineDays = api.DefaultJiraOfflineDays },
		func(a *store.Account) { a.Config.Jira.HideEvents = true },
		func(a *store.Account) { a.Config.Jira.Spaces[0].Name = "Help" },
	}
	differs := []func(a *store.Account){
		func(a *store.Account) { a.Enabled = false },
		func(a *store.Account) { a.Config.Jira.NotificationMail = api.NotificationMailHide },
		func(a *store.Account) { a.Config.Jira.NotificationMail = api.NotificationMailIgnore },
		func(a *store.Account) { a.Config.Jira.NotificationSenders = []string{"jira@acme.atlassian.net"} },
		func(a *store.Account) { a.Config.Jira.OfflineDays = 60 },
		func(a *store.Account) { a.Config.Jira.Spaces = a.Config.Jira.Spaces[:1] },
		func(a *store.Account) { a.Config.Jira.Spaces[0].Key = "HELP" },
		func(a *store.Account) { a.Config.Jira.SiteURL = "https://other.atlassian.net" },
	}
	want := issueMailState(base())
	if want == "" {
		t.Fatal("no state")
	}
	for i, edit := range same {
		a := base()
		edit(&a)
		if got := issueMailState(a); got != want {
			t.Errorf("same %d: another state", i)
		}
	}
	seen := map[string]int{want: -1}
	for i, edit := range differs {
		a := base()
		edit(&a)
		got := issueMailState(a)
		if j, dup := seen[got]; dup {
			t.Errorf("differs %d: the state of %d", i, j)
		}
		seen[got] = i
	}
	if issueMailState(store.Account{ID: "acc_mail", Config: validConfig()}) != "" {
		t.Error("a mail account has a state")
	}
	if !reflect.DeepEqual(base(), base()) {
		t.Fatal("the base is not stable")
	}
}
