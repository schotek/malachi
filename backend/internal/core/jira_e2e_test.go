// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/pkg/api"
)

// jiraE2E is the production stack (store, the supervisors from New,
// keyring, services) with one jira account on a fake site: the path a
// client takes, from account.add to the issues as folders, threads and
// messages.
type jiraE2E struct {
	t   *testing.T
	ctx context.Context
	f   *jiratest.Server
	b   *Backend
	id  api.AccountID
	rec *newsRecorder
}

// newsRecorder keeps the notifications that reach the RPC layer.
type newsRecorder struct {
	mu   sync.Mutex
	news []api.NewMessageNotification
}

func (r *newsRecorder) NewMessage(n api.NewMessageNotification) {
	r.mu.Lock()
	r.news = append(r.news, n)
	r.mu.Unlock()
}
func (*newsRecorder) SyncState(api.SyncStateNotification)             {}
func (*newsRecorder) AuthRequired(api.AuthRequiredNotification)       {}
func (*newsRecorder) AccountsChanged(api.AccountsChangedNotification) {}
func (*newsRecorder) MessagesChanged(api.MessagesChangedNotification) {}

func (r *newsRecorder) snapshot() []api.NewMessageNotification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.news)
}

// startJiraE2E seeds the site, starts the daemon's engines with manual
// sync, adds the account through account.add and waits for its first
// pass. prefs, when given, are set before anything syncs.
func startJiraE2E(t *testing.T, mode jiratest.Mode, prefs *api.Preferences, seed func(f *jiratest.Server)) *jiraE2E {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	f := jiratest.New(t, mode)
	if seed != nil {
		seed(f)
	}
	cfg := config.Default()
	cfg.Sync.IntervalSeconds = 0 // passes only when triggered
	b := newTestBackend(t, cfg)
	b.Keyring = newMemKeyring()
	b.JiraHTTP = f.HTTPClient()
	rec := &newsRecorder{}
	b.SetNotifier(rec)
	if prefs != nil {
		if _, err := b.Config().Set(ctx, api.ConfigSetParams{Preferences: *prefs}); err != nil {
			t.Fatal(err)
		}
	}
	syncCtx, stopSync := context.WithCancel(ctx)
	done := b.StartSync(syncCtx)
	t.Cleanup(func() {
		stopSync()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("supervisors did not stop")
		}
	})
	e := &jiraE2E{t: t, ctx: ctx, f: f, b: b, rec: rec}
	start := time.Now()
	acc := api.AccountConfig{Name: "Acme Jira", Email: jiratest.Login, Kind: api.AccountJira, Jira: api.Ptr(f.Config())}
	added, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: acc, Credentials: api.Credentials{Password: jiratest.Token}})
	if err != nil {
		t.Fatal(err)
	}
	e.id = added.AccountID
	e.waitPass(start)
	return e
}

// waitPass waits for a pass of the account that ended after since.
func (e *jiraE2E) waitPass(since time.Time) {
	e.t.Helper()
	var st api.SyncState
	waitUntil(e.t, e.ctx, "a jira pass", func() bool {
		st, _ = e.b.Supervisor.State(string(e.id))
		return st.Status == api.SyncIdle && st.LastSync != nil && st.LastSync.After(since)
	}, func() string { return fmt.Sprintf("state %+v, error %v", st, st.Error) })
}

// sync runs one more pass and waits for it.
func (e *jiraE2E) sync() {
	e.t.Helper()
	since := time.Now()
	if _, err := e.b.Sync().Trigger(e.ctx, api.SyncTriggerParams{AccountID: e.id}); err != nil {
		e.t.Fatal(err)
	}
	e.waitPass(since)
}

// folders returns folder.list, and the folders by name.
func (e *jiraE2E) folders() ([]api.Folder, map[string]api.Folder) {
	e.t.Helper()
	res, err := e.b.Folders().List(e.ctx, api.FolderListParams{AccountID: e.id})
	if err != nil {
		e.t.Fatal(err)
	}
	by := map[string]api.Folder{}
	for _, f := range res.Folders {
		by[f.Name] = f
	}
	return res.Folders, by
}

func (e *jiraE2E) list(folder api.FolderID) []api.MessageSummary {
	e.t.Helper()
	res, err := e.b.Messages().List(e.ctx, api.MessageListParams{AccountID: e.id, FolderID: folder})
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Messages
}

// item finds the message of an issue's item kind in a listing.
func item(t *testing.T, list []api.MessageSummary, key string, kind api.IssueItemKind) api.MessageSummary {
	t.Helper()
	for _, m := range list {
		if m.Issue != nil && m.Issue.Key == key && m.Issue.Item == kind {
			return m
		}
	}
	t.Fatalf("no %s of %s among %d messages", kind, key, len(list))
	return api.MessageSummary{}
}

// A comment whose rendered HTML is hostile: scripts, handlers, a frame and
// a picture from outside the site.
const hostileComment = `<p onclick="steal()">Paper tray <script>alert(1)</script>replaced` +
	`<img src="https://` + jiratest.OffSiteHost + `/pixel.png"></p><iframe src="https://evil.example/"></iframe>`

func TestJiraEndToEnd(t *testing.T) {
	for _, mode := range []jiratest.Mode{jiratest.Cloud, jiratest.DC} {
		t.Run(string(mode), func(t *testing.T) { testJiraEndToEnd(t, mode) })
	}
}

func testJiraEndToEnd(t *testing.T, mode jiratest.Mode) {
	var printer, typo, release *jiratest.Issue
	e := startJiraE2E(t, mode, nil, func(f *jiratest.Server) {
		printer = f.AddIssue("ITSD", "Printer on the 2nd floor", func(is *jiratest.Issue) {
			is.Assignee, is.Watching, is.Description = f.Me, true, "<p>The printer <b>jams</b>.</p>"
		})
		f.AddComment(printer.ID, f.Petr, hostileComment)
		f.SetStatus(printer.ID, "3", f.Petr)
		typo = f.AddIssue("WEB", "Landing page typo")
		release = f.AddIssue("WEB", "Old release", func(is *jiratest.Issue) { is.Status = "10001" })
	})
	ctx, b, f, id := e.ctx, e.b, e.f, e.id
	printerKey := f.Site.String() + "/browse/" + printer.Key

	// folder.list: the views in their order, then the spaces by name.
	folders, byName := e.folders()
	var order []string
	for _, fo := range folders {
		order = append(order, fo.Name+"/"+string(fo.Virtual))
		if fo.Role != api.RoleNone {
			t.Errorf("folder %s has role %s", fo.Name, fo.Role)
		}
	}
	if got := strings.Join(order, ", "); got != "Assigned to Me/assignedToMe, Watching/watching, Open/open, IT Service Desk/, Mobile/, Web/" {
		t.Fatalf("folders = %s", got)
	}
	itsd, web := byName["IT Service Desk"], byName["Web"]
	mine, watching, open := byName["Assigned to Me"], byName["Watching"], byName["Open"]

	// thread.list per space and view, with the issue.
	threads := func(folder api.FolderID) []api.ThreadSummary {
		res, err := b.Threads().List(ctx, api.ThreadListParams{AccountID: id, FolderID: folder})
		if err != nil {
			t.Fatal(err)
		}
		return res.Threads
	}
	th := threads(itsd.ID)
	if len(th) != 1 || th[0].Issue == nil {
		t.Fatalf("ITSD threads = %+v", th)
	}
	is := th[0].Issue
	if is.Key != printer.Key || is.URL != printerKey || is.Summary != "Printer on the 2nd floor" || is.Status != "In Progress" ||
		is.StatusCategory != api.StatusCategoryInProgress || !is.AssignedToMe || !is.Watching || is.Assignee != "Jana Dvořáková" ||
		!slices.Equal(is.CommentVisibilities, []api.CommentVisibility{api.CommentPublic, api.CommentInternal}) {
		t.Fatalf("issue = %+v", is)
	}
	if th[0].Latest.Issue == nil || th[0].Latest.Issue.Key != printer.Key || th[0].MessageCount != 3 {
		t.Fatalf("thread = %+v", th[0])
	}
	keys := func(list []api.ThreadSummary) []string {
		var out []string
		for _, t := range list {
			out = append(out, t.Issue.Key)
		}
		slices.Sort(out)
		return out
	}
	if got := keys(threads(web.ID)); !slices.Equal(got, sortedStrings(typo.Key, release.Key)) {
		t.Fatalf("WEB threads = %v", got)
	}
	for _, tc := range []struct {
		folder api.Folder
		want   []string
	}{{mine, []string{printer.Key}}, {watching, []string{printer.Key}}, {open, sortedStrings(printer.Key, typo.Key)}} {
		if got := keys(threads(tc.folder.ID)); !slices.Equal(got, tc.want) {
			t.Errorf("%s threads = %v, want %v", tc.folder.Name, got, tc.want)
		}
	}
	if webThreads := threads(web.ID); webThreads[0].Issue.CommentVisibilities != nil {
		t.Errorf("a non-service-desk issue offers %v", webThreads[0].Issue.CommentVisibilities)
	}

	// thread.get: the description (the user's own, read), the comment
	// (someone else's, recent: unread) and the status change (read).
	members := func(folder api.FolderID) []api.MessageSummary {
		res, err := b.Threads().Get(ctx, api.ThreadGetParams{AccountID: id, ThreadID: th[0].ID, FolderID: folder})
		if err != nil {
			t.Fatal(err)
		}
		if res.Thread.Issue == nil || res.Thread.Issue.Key != printer.Key {
			t.Fatalf("thread.get issue = %+v", res.Thread.Issue)
		}
		return res.Messages
	}
	for _, folder := range []api.FolderID{itsd.ID, "", mine.ID} {
		got := members(folder)
		if len(got) != 3 {
			t.Fatalf("thread.get %q: %d members", folder, len(got))
		}
		kinds := map[api.IssueItemKind]api.MessageSummary{}
		for _, m := range got {
			if m.Issue == nil {
				t.Fatalf("member without issue: %+v", m)
			}
			kinds[m.Issue.Item] = m
			if folder == "" && m.FolderID != itsd.ID {
				t.Errorf("account-wide thread.get returned a view's copy in %s", m.FolderID)
			}
		}
		desc, comment, event := kinds[api.IssueItemDescription], kinds[api.IssueItemComment], kinds[api.IssueItemEvent]
		if !hasFlagE2E(desc.Flags, api.FlagSeen) || hasFlagE2E(comment.Flags, api.FlagSeen) || !hasFlagE2E(event.Flags, api.FlagSeen) {
			t.Fatalf("flags: description %v, comment %v, event %v", desc.Flags, comment.Flags, event.Flags)
		}
		if len(event.Issue.Changes) != 1 || event.Issue.Changes[0] != (api.IssueChange{Field: api.IssueFieldStatus, From: "To Do", To: "In Progress"}) {
			t.Fatalf("event = %+v", event.Issue)
		}
		for _, m := range got {
			if m.Subject != printer.Key+": Printer on the 2nd floor" {
				t.Errorf("subject %q", m.Subject)
			}
		}
	}

	// message.get and message.body of the comment: the issue, and the
	// site's hostile HTML sanitised; nothing outside the site was fetched.
	comment := item(t, e.list(itsd.ID), printer.Key, api.IssueItemComment)
	got, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: id, MessageID: comment.ID})
	if err != nil || got.Message.Issue == nil || got.Message.Issue.Item != api.IssueItemComment || got.Message.From[0].Name != "Petr Svoboda" {
		t.Fatalf("message.get = %+v, %v", got, err)
	}
	body, err := b.Messages().Body(ctx, api.MessageBodyParams{AccountID: id, MessageID: comment.ID})
	if err != nil || body.BodyState != api.BodyFetched || !body.HasHTML {
		t.Fatalf("body = %+v, %v", body, err)
	}
	for _, bad := range []string{"<script", "alert(1)", "onclick", "steal", "<iframe", "evil.example"} {
		if strings.Contains(body.HTML, bad) {
			t.Errorf("sanitised HTML keeps %q: %s", bad, body.HTML)
		}
	}
	if !strings.Contains(body.HTML, "Paper tray") || !strings.Contains(body.Text, "replaced") {
		t.Fatalf("body lost its text: %q / %q", body.HTML, body.Text)
	}
	if n := f.OffSiteRequests(); n != 0 {
		t.Fatalf("%d requests left the site", n)
	}

	// message.flag is local: the copies follow at once, the site hears
	// nothing, the counts follow.
	requests := len(f.RequestsTo("", ""))
	if _, err := b.Messages().Flag(ctx, api.MessageFlagParams{AccountID: id, MessageIDs: []api.MessageID{comment.ID}, Set: []api.Flag{api.FlagSeen, api.FlagFlagged}}); err != nil {
		t.Fatal(err)
	}
	for _, view := range []api.Folder{mine, watching, open} {
		c := item(t, e.list(view.ID), printer.Key, api.IssueItemComment)
		if c.ID == comment.ID || !hasFlagE2E(c.Flags, api.FlagSeen) || !hasFlagE2E(c.Flags, api.FlagFlagged) {
			t.Fatalf("copy in %s: %+v", view.Name, c)
		}
	}
	if n := len(f.RequestsTo("", "")); n != requests {
		t.Fatalf("a local flag made %d requests to the site", n-requests)
	}
	if _, byName = e.folders(); byName["Assigned to Me"].Unread != 0 || byName["IT Service Desk"].Unread != 0 {
		t.Fatalf("counts after the flag: %+v", byName)
	}
	// The queued operation changes nothing when the next pass drops it.
	e.sync()
	if c := item(t, e.list(open.ID), printer.Key, api.IssueItemComment); !hasFlagE2E(c.Flags, api.FlagFlagged) {
		t.Fatalf("flags after a pass: %v", c.Flags)
	}

	// Move and delete are not for this account.
	if _, err := b.Messages().Move(ctx, api.MessageMoveParams{AccountID: id, MessageIDs: []api.MessageID{comment.ID}, TargetFolderID: web.ID}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("move: %v", err)
	}
	if _, err := b.Messages().Delete(ctx, api.MessageDeleteParams{AccountID: id, MessageIDs: []api.MessageID{comment.ID}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("delete: %v", err)
	}

	// search.query finds the comment once account-wide and everywhere,
	// in a view only when the scope names it.
	search := func(p api.SearchQueryParams) []api.SearchResult {
		p.Query = "replaced"
		res, err := b.Search().Query(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return res.Results
	}
	for name, p := range map[string]api.SearchQueryParams{"account": {AccountID: id}, "everywhere": {}, "space": {AccountID: id, FolderID: itsd.ID}} {
		res := search(p)
		if len(res) != 1 || res[0].Message.FolderID != itsd.ID || res[0].Message.Issue == nil || res[0].Message.Issue.Key != printer.Key {
			t.Fatalf("search %s = %+v", name, res)
		}
	}
	if res := search(api.SearchQueryParams{AccountID: id, FolderID: watching.ID}); len(res) != 1 || res[0].Message.FolderID != watching.ID || res[0].Message.Issue == nil {
		t.Fatalf("search in a view = %+v", res)
	}

	// A new comment of someone else after the first pass is announced
	// once, from the space folder, with its issue; the user's own is not.
	f.AddComment(printer.ID, f.Petr, "<p>Toner ordered.</p>")
	f.AddComment(printer.ID, f.Me, "<p>Thanks.</p>")
	e.sync()
	var news []api.NewMessageNotification
	waitUntil(t, ctx, "notify.newMessage", func() bool {
		news = e.rec.snapshot()
		return len(news) > 0
	})
	time.Sleep(100 * time.Millisecond) // anything else would have come by now
	news = e.rec.snapshot()
	if len(news) != 1 {
		t.Fatalf("news = %+v", news)
	}
	n := news[0]
	if n.AccountID != id || n.FolderID != itsd.ID || n.Message.Issue == nil || n.Message.Issue.Key != printer.Key ||
		n.Message.Issue.Item != api.IssueItemComment || n.Message.Issue.URL != printerKey || n.Message.To == nil ||
		!strings.Contains(n.Message.Snippet, "Toner") {
		t.Fatalf("newMessage = %+v (issue %+v)", n, n.Message.Issue)
	}
	if n.Message.Issue.Mine {
		t.Fatalf("a colleague's comment is marked as the user's own: %+v", n.Message.Issue)
	}

	// The user's own comment says so; nobody else's does.
	listed, err := b.Threads().List(ctx, api.ThreadListParams{AccountID: id, FolderID: itsd.ID})
	if err != nil {
		t.Fatalf("thread.list: %v", err)
	}
	var ownComments, otherComments int
	for _, t0 := range listed.Threads {
		if t0.Issue == nil || t0.Issue.Key != printer.Key {
			continue
		}
		res, err := b.Threads().Get(ctx, api.ThreadGetParams{AccountID: id, ThreadID: t0.ID, FolderID: itsd.ID})
		if err != nil {
			t.Fatalf("thread.get: %v", err)
		}
		for _, m := range res.Messages {
			if m.Issue == nil || m.Issue.Item != api.IssueItemComment {
				continue
			}
			switch {
			case strings.Contains(m.Snippet, "Thanks") && m.Issue.Mine:
				ownComments++
			case m.Issue.Mine:
				t.Fatalf("not the user's comment, yet mine: %+v", m)
			default:
				otherComments++
			}
		}
	}
	if ownComments != 1 || otherComments == 0 {
		t.Fatalf("own comments = %d, others = %d", ownComments, otherComments)
	}
}

func sortedStrings(s ...string) []string {
	slices.Sort(s)
	return s
}
