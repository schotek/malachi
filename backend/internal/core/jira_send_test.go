// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/jira"
	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Comments on the production stack: draft.create reply makes a comment
// draft, draft.save keeps it one, message.send queues it, the jira outbox
// worker posts it in the site's format and the refresh stores it in the
// issue's thread before the outbox row goes. An internal comment goes to a
// service-desk issue only. Recipients are never collected.
func TestJiraComments(t *testing.T) {
	for _, mode := range []jiratest.Mode{jiratest.Cloud, jiratest.DC} {
		t.Run(string(mode), func(t *testing.T) { testJiraComments(t, mode) })
	}
}

func testJiraComments(t *testing.T, mode jiratest.Mode) {
	var printer, web *jiratest.Issue
	e := startJiraE2E(t, mode, nil, func(f *jiratest.Server) {
		printer = f.AddIssue("ITSD", "Printer on the 2nd floor", func(is *jiratest.Issue) { is.Reporter = f.Petr })
		f.AddComment(printer.ID, f.Petr, "<p>Still jammed.</p>")
		web = f.AddIssue("WEB", "Landing page typo", func(is *jiratest.Issue) { is.Description = "<p>Fix the typo.</p>" })
	})
	ctx, b, f, id := e.ctx, e.b, e.f, e.id

	list, err := b.Accounts().List(ctx, api.AccountListParams{})
	if err != nil || fmt.Sprint(list.Accounts[0].Capabilities) != "[comment forward transition]" {
		t.Fatalf("capabilities: %+v %v", list, err)
	}
	_, byName := e.folders()
	webFolder, itsd := byName["Web"], byName["IT Service Desk"]
	desc := item(t, e.list(webFolder.ID), web.Key, api.IssueItemDescription)

	// draft.create reply: a comment draft of the issue.
	created, err := b.Drafts().Create(ctx, api.DraftCreateParams{AccountID: id, Mode: api.ComposeReply, MessageID: desc.ID,
		Attribution: "On Monday, Jana wrote:"})
	if err != nil {
		t.Fatal(err)
	}
	d := created.Draft
	if created.Quoted != api.QuoteNone || d.Comment == nil || d.Comment.Issue.Key != web.Key || d.Comment.Visibility != "" ||
		d.Comment.Issue.URL != f.Site.String()+"/browse/"+web.Key || len(d.Comment.Issue.CommentVisibilities) != 0 ||
		d.Subject != web.Key+": Landing page typo" || d.InReplyTo != desc.ID || len(d.To) != 0 || d.HTMLBody != "" || d.TextBody != "" {
		t.Fatalf("comment template = %+v %+v", created, d.Comment)
	}

	// draft.save: what a comment cannot carry is refused.
	base := d
	base.HTMLBody = `<p>Fixed in <b>r42</b>, see <a href="https://acme.example/r42">the change</a>.</p><script>x()</script>`
	att := importAttachment(t, b, id, "notes.txt", []byte("notes"))
	for name, edit := range map[string]func(*api.Draft){
		"recipients":         func(d *api.Draft) { d.To = []api.Address{{Address: "petr@acme.test"}} },
		"bcc":                func(d *api.Draft) { d.BCC = []api.Address{{Address: "petr@acme.test"}} },
		"attachment":         func(d *api.Draft) { d.Attachments = []api.DraftAttachment{{ID: att.ID}} },
		"no inReplyTo":       func(d *api.Draft) { d.InReplyTo = "" },
		"unknown inReplyTo":  func(d *api.Draft) { d.InReplyTo = "m_nobody" },
		"forwarding":         func(d *api.Draft) { d.InReplyTo, d.Forwarding = "", desc.ID },
		"replaces":           func(d *api.Draft) { d.Replaces = desc.ID },
		"internal on WEB":    func(d *api.Draft) { d.Comment = &api.DraftComment{Visibility: api.CommentInternal} },
		"unknown visibility": func(d *api.Draft) { d.Comment = &api.DraftComment{Visibility: "team"} },
	} {
		bad := base
		edit(&bad)
		if _, err := b.Drafts().Save(ctx, api.DraftSaveParams{Draft: bad}); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("draft.save with %s: %v", name, err)
		}
	}
	// The subject is the issue's whatever is sent; the HTML is sanitised
	// as always.
	base.Subject = "Something else"
	saved, err := b.Drafts().Save(ctx, api.DraftSaveParams{Draft: base})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(saved.HTMLBody, "script") || saved.Blocked.Scripts != 1 {
		t.Errorf("saved html %q blocked %+v", saved.HTMLBody, saved.Blocked)
	}
	drafts, err := b.Drafts().List(ctx, api.DraftListParams{AccountID: id})
	if err != nil || len(drafts.Drafts) != 1 {
		t.Fatalf("draft.list: %+v %v", drafts, err)
	}
	if ld := drafts.Drafts[0]; ld.Comment == nil || ld.Comment.Issue.Key != web.Key || ld.Subject != web.Key+": Landing page typo" || ld.InReplyTo != desc.ID {
		t.Fatalf("listed comment draft = %+v %+v", ld, ld.Comment)
	}
	// draft.get: the same draft as listed, comment included; an issue
	// tracker's drafts are local by kind.
	got, err := b.Drafts().Get(ctx, api.DraftGetParams{AccountID: id, DraftID: saved.DraftID})
	if err != nil || got.Draft.Comment == nil || got.Draft.Comment.Issue.Key != web.Key || !got.Draft.Local ||
		!drafts.Drafts[0].Local || got.Draft.HTMLBody != drafts.Drafts[0].HTMLBody {
		t.Fatalf("draft.get of a comment draft = %+v %v", got, err)
	}
	if _, has := outboxFolder(t, b, id); has {
		t.Fatal("an outbox folder before anything was sent")
	}

	// message.send: queued, posted, stored, the row gone.
	sent, err := b.Messages().Send(ctx, api.MessageSendParams{AccountID: id, DraftID: saved.DraftID, Version: saved.Version})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, ctx, "the comment delivered", func() bool {
		_, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: id, MessageID: sent.OutboxID})
		return codeOf(err) == api.CodeMessageNotFound
	})
	posted := f.RequestsTo(http.MethodPost, "/comment")
	if len(posted) != 1 {
		t.Fatalf("%d posts", len(posted))
	}
	var body struct {
		Body       json.RawMessage `json:"body"`
		Properties []struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(posted[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Properties) != 1 || body.Properties[0].Key != jira.OutboxPropertyKey ||
		string(body.Properties[0].Value) != `{"id":"`+string(sent.OutboxID)+`"}` {
		t.Errorf("properties = %s", posted[0].Body)
	}
	if mode == jiratest.Cloud {
		if err := jiratest.ValidateADF(body.Body); err != nil || !strings.Contains(string(body.Body), `"text":"r42","marks":[{"type":"strong"}]`) ||
			!strings.Contains(string(body.Body), `{"type":"link","attrs":{"href":"https://acme.example/r42"}}`) {
			t.Errorf("ADF %v: %s", err, body.Body)
		}
	} else {
		var wiki string
		if err := json.Unmarshal(body.Body, &wiki); err != nil || wiki != "Fixed in *r42*, see [the change|https://acme.example/r42]." {
			t.Errorf("wiki %v: %q", err, wiki)
		}
	}
	thread, err := b.Threads().Get(ctx, api.ThreadGetParams{AccountID: id, ThreadID: desc.ThreadID, FolderID: webFolder.ID, WithSent: true})
	if err != nil {
		t.Fatal(err)
	}
	// An issue tracker has no sent folder: the user's comment is a member.
	if thread.Sent != nil || thread.Thread.SentCount != 0 {
		t.Errorf("sent of an issue = %+v, count %d", thread.Sent, thread.Thread.SentCount)
	}
	var mine *api.MessageSummary
	for i, m := range thread.Messages {
		if m.Issue != nil && m.Issue.Item == api.IssueItemComment {
			mine = &thread.Messages[i]
		}
	}
	if mine == nil || len(mine.From) != 1 || mine.From[0].Name != "Jana Dvořáková" || !hasFlag(mine.Flags, api.FlagSeen) {
		t.Fatalf("the comment in the thread: %+v", thread.Messages)
	}
	for _, n := range e.rec.snapshot() {
		if n.Message.ID == mine.ID {
			t.Error("the user's own comment was announced")
		}
	}
	if st := outboxStateOf(t, b, id); st.PendingOutbox != 0 || st.FailedOutbox != 0 {
		t.Errorf("outbox counts after delivery: %+v", st)
	}
	if drafts, _ := b.Drafts().List(ctx, api.DraftListParams{AccountID: id}); len(drafts.Drafts) != 0 {
		t.Errorf("the draft stayed: %+v", drafts.Drafts)
	}
	if senders, _ := b.store.ListKnownSenders(ctx); len(senders) != 0 {
		t.Errorf("known senders after a comment: %+v", senders)
	}
	if got, _ := b.store.SearchCollectedAddresses(ctx, "acme", 10); len(got) != 0 {
		t.Errorf("collected addresses after a comment: %+v", got)
	}

	// An internal comment on the service-desk issue.
	petr := item(t, e.list(itsd.ID), printer.Key, api.IssueItemComment)
	created, err = b.Drafts().Create(ctx, api.DraftCreateParams{AccountID: id, Mode: api.ComposeReply, MessageID: petr.ID})
	if err != nil {
		t.Fatal(err)
	}
	if vs := created.Draft.Comment.Issue.CommentVisibilities; len(vs) != 2 || vs[1] != api.CommentInternal {
		t.Fatalf("service-desk visibilities = %v", vs)
	}
	internal := created.Draft
	internal.TextBody = "Toner ordered; do not tell the customer yet."
	internal.Comment.Visibility = api.CommentInternal
	id2, v2 := saveDraft(t, b, internal)
	if drafts, _ := b.Drafts().List(ctx, api.DraftListParams{AccountID: id}); len(drafts.Drafts) != 1 || drafts.Drafts[0].Comment.Visibility != api.CommentInternal {
		t.Fatalf("listed visibility: %+v", drafts.Drafts)
	}
	sent, err = b.Messages().Send(ctx, api.MessageSendParams{AccountID: id, DraftID: id2, Version: v2})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, ctx, "the internal comment delivered", func() bool {
		_, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: id, MessageID: sent.OutboxID})
		return codeOf(err) == api.CodeMessageNotFound
	})
	if posted := f.RequestsTo(http.MethodPost, "/comment"); len(posted) != 2 || !strings.Contains(string(posted[1].Body), `{"key":"sd.public.comment","value":{"internal":true}}`) {
		t.Fatalf("internal post: %d %s", len(posted), posted[len(posted)-1].Body)
	}
	found := false
	for _, m := range e.list(itsd.ID) {
		if m.Issue != nil && m.Issue.Item == api.IssueItemComment && m.Issue.Visibility == api.CommentInternal && len(m.From) == 1 && m.From[0].Name == "Jana Dvořáková" {
			found = true
		}
	}
	if !found {
		t.Fatal("the internal comment is not stored as internal")
	}

	// A comment the site will not take for now stays queued, in the
	// issue's thread, and can be taken back; issue messages still cannot
	// be moved or deleted.
	f.FailNext(jiratest.Failure{Method: http.MethodPost, Path: "/comment", Status: http.StatusInternalServerError, Times: 100})
	d3 := created.Draft
	d3.TextBody = "Queued for later."
	id3, v3 := saveDraft(t, b, d3)
	sent, err = b.Messages().Send(ctx, api.MessageSendParams{AccountID: id, DraftID: id3, Version: v3})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, ctx, "the failed attempt", func() bool {
		res, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: id, MessageID: sent.OutboxID})
		return err == nil && res.Message.Outbox != nil && res.Message.Outbox.State == api.OutboxQueued && res.Message.Outbox.Attempts > 0
	})
	queued, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: id, MessageID: sent.OutboxID})
	if err != nil || queued.Message.ThreadID != petr.ThreadID || len(queued.Message.To) != 0 || queued.Message.Subject != printer.Key+": Printer on the 2nd floor" {
		t.Fatalf("queued comment: %+v %v", queued.Message.MessageSummary, err)
	}
	full, err := b.Threads().Get(ctx, api.ThreadGetParams{AccountID: id, ThreadID: petr.ThreadID})
	if err != nil {
		t.Fatal(err)
	}
	inThread := false
	for _, m := range full.Messages {
		inThread = inThread || m.ID == sent.OutboxID
	}
	if !inThread {
		t.Error("the queued comment is not in the issue's thread")
	}
	e.sync() // a pass leaves the queued row alone
	if _, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: id, MessageID: sent.OutboxID}); err != nil {
		t.Fatalf("a pass removed the queued comment: %v", err)
	}
	for _, permanent := range []bool{false, true} {
		if _, err := b.Messages().Delete(ctx, api.MessageDeleteParams{AccountID: id, MessageIDs: []api.MessageID{petr.ID}, Permanent: permanent}); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("delete an issue message: %v", err)
		}
	}
	if _, err := b.Messages().Delete(ctx, api.MessageDeleteParams{AccountID: id, MessageIDs: []api.MessageID{sent.OutboxID, petr.ID}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("delete a queued comment with an issue message: %v", err)
	}
	if _, err := b.Messages().Move(ctx, api.MessageMoveParams{AccountID: id, MessageIDs: []api.MessageID{petr.ID}, TargetFolderID: webFolder.ID}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("move: %v", err)
	}
	if _, err := b.Messages().Delete(ctx, api.MessageDeleteParams{AccountID: id, MessageIDs: []api.MessageID{sent.OutboxID}}); err != nil {
		t.Fatalf("take back a queued comment: %v", err)
	}
	if _, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: id, MessageID: sent.OutboxID}); errCode(t, err) != api.CodeMessageNotFound {
		t.Fatalf("the queued comment stayed: %v", err)
	}
	if n := len(f.CommentsOf(printer.ID)); n != 2 {
		t.Errorf("the site has %d comments on the printer", n)
	}

	// Refused before anything is queued: an empty comment, a visibility
	// the issue no longer allows.
	empty := created.Draft
	empty.HTMLBody = "<p>&nbsp;</p><hr>"
	idE, vE := saveDraft(t, b, empty)
	if _, err := b.Messages().Send(ctx, api.MessageSendParams{AccountID: id, DraftID: idE, Version: vE}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("empty comment: %v", err)
	}
	long := created.Draft
	long.TextBody = strings.Repeat("x", jira.MaxCommentChars+1)
	idL, vL := saveDraft(t, b, long)
	if _, err := b.Messages().Send(ctx, api.MessageSendParams{AccountID: id, DraftID: idL, Version: vL}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("too long a comment: %v", err)
	}
	if _, err := b.Messages().Send(ctx, api.MessageSendParams{AccountID: id, DraftID: idL, Version: vL + 1}); errCode(t, err) != api.CodeConflict {
		t.Fatalf("stale version: %v", err)
	}
	if drafts, _ := b.Drafts().List(ctx, api.DraftListParams{AccountID: id}); len(drafts.Drafts) != 2 {
		t.Errorf("refused drafts are kept: %d", len(drafts.Drafts))
	}
}

func hasFlag(flags []api.Flag, want api.Flag) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

// A message of a jira account forwarded from a mail account: the draft is
// the mail account's, with the Fwd: subject, the quoted sanitised HTML and
// the issue's picture and file copied into the mail account's attachment
// store; it saves as it came and queues as mail.
func TestJiraForwardFromMailAccount(t *testing.T) {
	for _, mode := range []jiratest.Mode{jiratest.Cloud, jiratest.DC} {
		t.Run(string(mode), func(t *testing.T) { testJiraForwardFromMailAccount(t, mode) })
	}
}

func testJiraForwardFromMailAccount(t *testing.T, mode jiratest.Mode) {
	var issue *jiratest.Issue
	e := startJiraE2E(t, mode, nil, func(f *jiratest.Server) {
		issue = f.AddIssue("WEB", "Broken banner")
		pic := f.AddAttachment(issue.ID, "banner.png", "image/png", jiratest.PNG)
		f.AddAttachment(issue.ID, "spec.txt", "text/plain", []byte("Banner 1200×300."))
		f.Update(issue.ID, func(is *jiratest.Issue) {
			is.Description = `<p>The banner <b>is cut</b>:</p><p><img src="` + f.Site.String() + `/secure/attachment/` + pic + `/banner.png"></p>` +
				`<p onclick="x()">Thanks<script>alert(1)</script></p>`
		})
	})
	ctx, b, id := e.ctx, e.b, e.id
	_, byName := e.folders()
	desc := item(t, e.list(byName["Web"].ID), issue.Key, api.IssueItemDescription)

	// The mail account, stored directly: nothing syncs or delivers for it.
	mail := store.Account{Name: "Work", Enabled: true, Config: validConfig()}
	mail.Config.Email = jiratest.Login // the same address, another realm
	if err := b.store.AddAccount(ctx, &mail); err != nil {
		t.Fatal(err)
	}
	mailID := api.AccountID(mail.ID)
	res, err := b.Drafts().Create(ctx, api.DraftCreateParams{AccountID: mailID, Mode: api.ComposeForward, MessageID: desc.ID,
		MessageAccountID: id, Attribution: "---------- Forwarded message ----------\nSubject: " + desc.Subject})
	if err != nil {
		t.Fatal(err)
	}
	d := res.Draft
	if d.AccountID != mailID || d.Subject != "Fwd: "+issue.Key+": Broken banner" || d.Forwarding != desc.ID || d.Comment != nil ||
		len(d.To) != 0 || res.Quoted != api.QuoteHTML || len(res.Skipped) != 0 {
		t.Fatalf("forward = %+v quoted %s skipped %+v", d, res.Quoted, res.Skipped)
	}
	checkQuoteClean(t, d)
	if !strings.Contains(d.HTMLBody, "<b>is cut</b>") || strings.Contains(d.HTMLBody, "alert") || res.Blocked.Scripts == 0 {
		t.Errorf("quote = %q blocked %+v", d.HTMLBody, res.Blocked)
	}
	var pic, file *api.DraftAttachment
	for i, a := range d.Attachments {
		switch {
		case a.Inline && a.Filename == "banner.png":
			pic = &d.Attachments[i]
		case !a.Inline && a.Filename == "spec.txt":
			file = &d.Attachments[i]
		}
	}
	if pic == nil || file == nil || !strings.Contains(d.HTMLBody, `src="cid:`+pic.ContentID+`"`) {
		t.Fatalf("attachments = %+v\n%s", d.Attachments, d.HTMLBody)
	}
	// The picture travels inline and, as the issue's file, attached (the
	// jira message has it both ways); the copies are the mail account's,
	// not the jira account's.
	if len(d.Attachments) != 3 {
		t.Errorf("attachments = %+v", d.Attachments)
	}
	if _, err := b.Attachments().Get(ctx, api.AttachmentGetParams{AccountID: mailID, AttachmentID: file.ID}); err != nil {
		t.Errorf("the mail account's copy: %v", err)
	}
	if _, err := b.Attachments().Get(ctx, api.AttachmentGetParams{AccountID: id, AttachmentID: file.ID}); errCode(t, err) != api.CodeAttachmentNotFound {
		t.Errorf("the copy in the jira account: %v", err)
	}
	d.To = []api.Address{{Name: "Petr Svoboda", Address: "petr.svoboda@acme.test"}}
	saved, err := b.Drafts().Save(ctx, api.DraftSaveParams{Draft: d})
	if err != nil || saved.HTMLBody != d.HTMLBody || len(saved.Attachments) != len(d.Attachments) || saved.Blocked != (api.BlockedContent{}) {
		t.Fatalf("save of the forward: %+v %v", saved, err)
	}
	sent, err := b.Messages().Send(ctx, api.MessageSendParams{AccountID: mailID, DraftID: saved.DraftID, Version: saved.Version})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: mailID, MessageID: sent.OutboxID})
	if err != nil || queued.Message.Outbox == nil || len(queued.Message.Attachments) != len(d.Attachments) || queued.Message.To[0].Address != "petr.svoboda@acme.test" {
		t.Fatalf("queued forward: %+v %v", queued.Message, err)
	}
	entry, err := b.store.GetOutbox(ctx, mail.ID, string(sent.OutboxID))
	if err != nil || entry.Comment != nil || len(entry.Recipients) != 1 {
		t.Fatalf("outbox entry: %+v %v", entry, err)
	}
	if n := len(e.f.RequestsTo(http.MethodPost, "/comment")); n != 0 {
		t.Fatalf("a forward posted %d comments", n)
	}
}
