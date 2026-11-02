// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gomail "github.com/emersion/go-message/mail"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// rulesEnv is a backend with a mail account and its folders, a clock the
// test moves and a recorder of notify.rulesChanged.
type rulesEnv struct {
	t       *testing.T
	b       *Backend
	acc     api.AccountID
	folder  map[string]store.Folder
	nextUID atomic.Uint32
	rec     *rulesRecorder

	mu  sync.Mutex
	now time.Time
}

type rulesRecorder struct {
	recNotifier
	mu      sync.Mutex
	changed int
}

func (r *rulesRecorder) RulesChanged(api.RulesChangedNotification) {
	r.mu.Lock()
	r.changed++
	r.mu.Unlock()
}

func (r *rulesRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.changed
}

func newRulesEnv(t *testing.T) *rulesEnv {
	t.Helper()
	b, _ := newSyncBackend(t)
	// The test's clock starts two days after the account was added, so
	// that mail of the day before a rule was enabled is not older than the
	// account.
	e := &rulesEnv{t: t, b: b, rec: &rulesRecorder{}, now: time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)}
	b.SetNotifier(e.rec)
	b.rules.now = e.clock
	e.acc = api.AccountID(seedAccount(t, b, "me@example.invalid"))
	e.folder = seedFolders(t, b, string(e.acc), []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Archive", Name: "Archive", Path: "Archive", Role: api.RoleArchive, Subscribed: true, Selectable: true},
		{Mailbox: "Projects", Name: "Projects", Path: "Projects", Subscribed: true, Selectable: true},
		{Mailbox: "Junk", Name: "Junk", Path: "Junk", Role: api.RoleJunk, Subscribed: true, Selectable: true},
		{Mailbox: "Trash", Name: "Trash", Path: "Trash", Role: api.RoleTrash, Subscribed: true, Selectable: true},
		{Mailbox: "Sent", Name: "Sent", Path: "Sent", Role: api.RoleSent, Subscribed: true, Selectable: true},
	})
	e.nextUID.Store(1)
	return e
}

func (e *rulesEnv) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *rulesEnv) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

// ruleMail is a message from "from" with the given subject, Message-ID and
// extra header lines.
func ruleMail(from, subject, msgID string, extra ...string) string {
	h := []string{"From: " + from, "To: me@example.invalid", "Subject: " + subject,
		"Date: Mon, 01 Sep 2026 11:00:00 +0000", "Message-ID: <" + msgID + ">"}
	h = append(h, extra...)
	h = append(h, "MIME-Version: 1.0", "Content-Type: text/plain; charset=utf-8")
	return strings.Join(h, "\r\n") + "\r\n\r\nHello, the body of " + subject + ".\r\n"
}

// store puts a downloaded message in a folder (by mailbox) with the given
// arrival.
func (e *rulesEnv) store(folder, raw string, arrived time.Time, flags ...api.Flag) string {
	e.t.Helper()
	ctx := context.Background()
	p, err := mime.Parse(strings.NewReader(raw), mime.DefaultLimits())
	if err != nil {
		e.t.Fatal(err)
	}
	m := &store.Message{AccountID: string(e.acc), FolderID: e.folder[folder].ID, UID: e.nextUID.Add(1), Flags: flags,
		Subject: p.Subject, From: p.From, To: p.To, CC: p.CC, ReplyTo: p.ReplyTo, Date: arrived, InternalDate: arrived,
		RFCMessageID: p.MessageID, InReplyTo: p.InReplyTo, References: p.References, Size: int64(len(raw)), Headers: p.Headers}
	if err := e.b.store.UpsertMessages(ctx, []*store.Message{m}); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.b.store.WriteMessageRaw(ctx, string(e.acc), m.ID, strings.NewReader(raw), 1<<20); err != nil {
		e.t.Fatal(err)
	}
	if err := e.b.store.SetMessageBody(ctx, m.ID, store.BodyUpdate{Text: p.Text, Headers: p.Headers, State: store.BodyFetched,
		Size: int64(len(raw)), HasAttachments: len(p.Attachments) > 0, Attachments: p.Attachments, References: p.References}); err != nil {
		e.t.Fatal(err)
	}
	return m.ID
}

// arrive stores a message as the syncer does and runs its Stored hook.
func (e *rulesEnv) arrive(folder, raw string, arrived time.Time, flags ...api.Flag) string {
	e.t.Helper()
	id := e.store(folder, raw, arrived, flags...)
	e.b.afterMailStored(context.Background(), string(e.acc), id, ingest.Policy{})
	return id
}

func (e *rulesEnv) get(id string) store.Message {
	e.t.Helper()
	m, err := e.b.store.GetMessage(context.Background(), string(e.acc), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *rulesEnv) create(in api.RuleInput, confirm bool) api.Rule {
	e.t.Helper()
	res, err := e.b.Rules().Create(context.Background(), api.RulesCreateParams{Rule: in, ConfirmOutgoing: confirm})
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Rule
}

func (e *rulesEnv) rule(id string) api.Rule {
	e.t.Helper()
	r, err := e.b.store.GetRule(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return r.Rule
}

func (e *rulesEnv) outbox() []api.MessageSummary {
	e.t.Helper()
	of, ok := outboxFolder(e.t, e.b, e.acc)
	if !ok {
		return nil
	}
	list, err := e.b.Messages().List(context.Background(), api.MessageListParams{AccountID: e.acc, FolderID: of.ID})
	if err != nil {
		e.t.Fatal(err)
	}
	return list.Messages
}

func (e *rulesEnv) raw(id api.MessageID) string {
	e.t.Helper()
	f, err := e.b.store.OpenMessageRaw(context.Background(), string(e.acc), string(id))
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	return string(data)
}

func subjectRule(name, subject string, acc api.AccountID, actions ...api.RuleAction) api.RuleInput {
	return api.RuleInput{Name: name, Enabled: true, AccountID: acc, Match: api.RuleMatchAll,
		Conditions: []api.RuleCondition{{Field: api.RuleFieldSubject, Op: api.RuleOpContains, Value: subject}},
		Actions:    actions}
}

func invalidField(t *testing.T, err error) api.RuleInvalidData {
	t.Helper()
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.CodeInvalidArgument {
		t.Fatalf("want invalidArgument, got %v", err)
	}
	d, ok := ae.Data.(api.RuleInvalidData)
	if !ok {
		t.Fatalf("data %#v", ae.Data)
	}
	return d
}

func TestRulesServiceMethods(t *testing.T) {
	e := newRulesEnv(t)
	ctx := context.Background()
	svc := e.b.Rules()

	// Refusals carry the field.
	bad := subjectRule("", "x", "", api.RuleAction{Kind: api.RuleActionFlag})
	_, err := svc.Create(ctx, api.RulesCreateParams{Rule: bad})
	if d := invalidField(t, err); d.Field != "name" {
		t.Errorf("data %+v", d)
	}
	_, err = svc.Create(ctx, api.RulesCreateParams{Rule: subjectRule("n", "x", "acc_nope", api.RuleAction{Kind: api.RuleActionFlag})})
	if errCode(t, err) != api.CodeAccountNotFound {
		t.Errorf("unknown account: %v", err)
	}
	_, err = svc.Create(ctx, api.RulesCreateParams{Rule: subjectRule("n", "x", e.acc, api.RuleAction{Kind: api.RuleActionMove, FolderID: "f_nope"})})
	if errCode(t, err) != api.CodeFolderNotFound {
		t.Errorf("unknown folder: %v", err)
	}
	of, err := e.b.store.OutboxFolder(ctx, string(e.acc))
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Create(ctx, api.RulesCreateParams{Rule: subjectRule("n", "x", e.acc, api.RuleAction{Kind: api.RuleActionMove, FolderID: api.FolderID(of.ID)})})
	if d := invalidField(t, err); d.Field != "actions" {
		t.Errorf("move to the outbox: %+v", d)
	}
	fwd := subjectRule("fwd", "x", e.acc, api.RuleAction{Kind: api.RuleActionForward, To: "boss@example.org"})
	_, err = svc.Create(ctx, api.RulesCreateParams{Rule: fwd})
	if d := invalidField(t, err); d.Field != "confirmOutgoing" {
		t.Errorf("unconfirmed: %+v", d)
	}
	off := fwd
	off.Enabled = false
	r1, err := svc.Create(ctx, api.RulesCreateParams{Rule: off})
	if err != nil || !r1.Rule.Outgoing || r1.Rule.Enabled {
		t.Fatalf("a disabled outgoing rule needs no confirmation: %+v %v", r1, err)
	}
	// Enabling it does; so does changing the address of an enabled one.
	_, err = svc.Update(ctx, api.RulesUpdateParams{ID: r1.Rule.ID, Rule: fwd})
	if d := invalidField(t, err); d.Field != "confirmOutgoing" {
		t.Errorf("enable unconfirmed: %+v", d)
	}
	if _, err = svc.Update(ctx, api.RulesUpdateParams{ID: r1.Rule.ID, Rule: fwd, ConfirmOutgoing: true}); err != nil {
		t.Fatal(err)
	}
	renamed := fwd
	renamed.Name = "renamed"
	if _, err = svc.Update(ctx, api.RulesUpdateParams{ID: r1.Rule.ID, Rule: renamed}); err != nil {
		t.Errorf("a rename needs no confirmation: %v", err)
	}
	other := renamed
	other.Actions = []api.RuleAction{{Kind: api.RuleActionForward, To: "other@example.org"}}
	_, err = svc.Update(ctx, api.RulesUpdateParams{ID: r1.Rule.ID, Rule: other})
	if d := invalidField(t, err); d.Field != "confirmOutgoing" {
		t.Errorf("new address unconfirmed: %+v", d)
	}
	_, err = svc.Update(ctx, api.RulesUpdateParams{ID: "r_nope", Rule: renamed})
	if errCode(t, err) != api.CodeRuleNotFound {
		t.Errorf("unknown rule: %v", err)
	}

	r2 := e.create(subjectRule("all", "y", "", api.RuleAction{Kind: api.RuleActionFlag}), false)
	list, _ := svc.List(ctx, api.RulesListParams{})
	if len(list.Rules) != 2 || list.Rules[0].ID != r1.Rule.ID || list.Rules[0].Name != "renamed" {
		t.Fatalf("list %+v", list)
	}
	if _, err := svc.Reorder(ctx, api.RulesReorderParams{IDs: []string{r2.ID}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("partial reorder: %v", err)
	}
	re, err := svc.Reorder(ctx, api.RulesReorderParams{IDs: []string{r2.ID, r1.Rule.ID}})
	if err != nil || re.Rules[0].ID != r2.ID {
		t.Fatalf("reorder %+v %v", re, err)
	}
	if _, err := svc.Delete(ctx, api.RulesDeleteParams{ID: r2.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Delete(ctx, api.RulesDeleteParams{ID: r2.ID}); errCode(t, err) != api.CodeRuleNotFound {
		t.Errorf("delete twice: %v", err)
	}
	// The notifications are coalesced: several writes, few notifications.
	time.Sleep(1500 * time.Millisecond)
	if n := e.rec.count(); n < 1 || n > 3 {
		t.Errorf("%d notifications", n)
	}
}

// The local actions run at once, in the fixed order, over several rules:
// the first move wins, later rules still mark; the counts follow.
func TestRulesLocalActions(t *testing.T) {
	e := newRulesEnv(t)
	r1 := e.create(subjectRule("move", "invoice", e.acc,
		api.RuleAction{Kind: api.RuleActionMove, FolderID: api.FolderID(e.folder["Projects"].ID)},
		api.RuleAction{Kind: api.RuleActionMarkRead}), false)
	r2 := e.create(subjectRule("star", "invoice", "",
		api.RuleAction{Kind: api.RuleActionTrash}, api.RuleAction{Kind: api.RuleActionFlag}), false)
	id := e.arrive("INBOX", ruleMail("Shop <shop@example.org>", "Your invoice", "i1@example.org"), e.clock())
	m := e.get(id)
	if m.FolderID != e.folder["Projects"].ID || !slices.Contains(m.Flags, api.FlagSeen) || !slices.Contains(m.Flags, api.FlagFlagged) {
		t.Fatalf("message %+v", m)
	}
	if got := e.rule(r1.ID); got.Applied != 1 || got.LastAppliedAt == nil {
		t.Errorf("r1 %+v", got)
	}
	if got := e.rule(r2.ID); got.Applied != 1 {
		t.Errorf("r2 %+v", got)
	}
	// The changes are queued for the server as the user's own.
	ops, err := e.b.store.CountPendingOps(context.Background(), string(e.acc))
	if err != nil || ops == 0 {
		t.Errorf("pending ops %d %v", ops, err)
	}
	// A message nothing matches is left alone.
	other := e.arrive("INBOX", ruleMail("A <a@example.org>", "Hello", "h1@example.org"), e.clock())
	if m := e.get(other); m.FolderID != e.folder["INBOX"].ID || len(m.Flags) != 0 {
		t.Errorf("untouched %+v", m)
	}
}

// Old mail, the first sync and a backfill never run rules; a message
// whose Message-ID went through them never again, also after a move back
// into the inbox or as a new row; only the inbox counts.
func TestRulesOnlyNewInboxMailOnce(t *testing.T) {
	e := newRulesEnv(t)
	e.create(subjectRule("arch", "news", e.acc, api.RuleAction{Kind: api.RuleActionMove, FolderID: api.FolderID(e.folder["Archive"].ID)}), false)
	inbox, archive := e.folder["INBOX"].ID, e.folder["Archive"].ID

	old := e.arrive("INBOX", ruleMail("A <a@example.org>", "news old", "o1@example.org"), e.clock().Add(-25*time.Hour))
	if e.get(old).FolderID != inbox {
		t.Error("mail older than the day before the rule was enabled was moved")
	}
	recent := e.arrive("INBOX", ruleMail("A <a@example.org>", "news recent", "o2@example.org"), e.clock().Add(-23*time.Hour))
	if e.get(recent).FolderID != archive {
		t.Error("mail of the day before the rule was enabled was not moved")
	}
	// Back into the inbox (the user, or another client): left alone.
	if _, err := e.b.store.MoveMessages(context.Background(), string(e.acc), []string{recent}, inbox); err != nil {
		t.Fatal(err)
	}
	e.b.afterMailStored(context.Background(), string(e.acc), recent, ingest.Policy{})
	if e.get(recent).FolderID != inbox {
		t.Error("the rule ran twice on a message")
	}
	// A new row with the same Message-ID (a move by another client).
	again := e.arrive("INBOX", ruleMail("A <a@example.org>", "news recent", "o2@example.org"), e.clock())
	if e.get(again).FolderID != inbox {
		t.Error("the rule ran twice on a Message-ID")
	}
	// Not the inbox: nothing.
	elsewhere := e.arrive("Projects", ruleMail("A <a@example.org>", "news", "o3@example.org"), e.clock())
	if e.get(elsewhere).FolderID != e.folder["Projects"].ID {
		t.Error("a rule ran outside the inbox")
	}
	// Re-enabling counts from then on.
	list, _ := e.b.Rules().List(context.Background(), api.RulesListParams{})
	r := list.Rules[0]
	in := r.RuleInput
	in.Enabled = false
	if _, err := e.b.Rules().Update(context.Background(), api.RulesUpdateParams{ID: r.ID, Rule: in}); err != nil {
		t.Fatal(err)
	}
	e.advance(72 * time.Hour)
	in.Enabled = true
	if _, err := e.b.Rules().Update(context.Background(), api.RulesUpdateParams{ID: r.ID, Rule: in}); err != nil {
		t.Fatal(err)
	}
	before := e.arrive("INBOX", ruleMail("A <a@example.org>", "news between", "o4@example.org"), e.clock().Add(-48*time.Hour))
	if e.get(before).FolderID != inbox {
		t.Error("mail from before the re-enabling was moved")
	}
}

// A rule for every account does not run on the first sync of an account
// added after it was enabled: mail from before the account was added is
// left alone, later mail is not.
func TestRulesNotOnANewAccountsHistory(t *testing.T) {
	e := newRulesEnv(t)
	e.mu.Lock()
	e.now = time.Now().UTC().Add(-30 * 24 * time.Hour)
	e.mu.Unlock()
	e.create(subjectRule("all", "hello", "", api.RuleAction{Kind: api.RuleActionFlag}), false)
	other := seedAccount(t, e.b, "second@example.invalid")
	inbox := seedFolders(t, e.b, other, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
	})["INBOX"]
	put := func(subject, id string, arrived time.Time) string {
		m := &store.Message{AccountID: other, FolderID: inbox.ID, UID: e.nextUID.Add(1), Subject: subject,
			From: []api.Address{{Address: "a@example.org"}}, Date: arrived, InternalDate: arrived, RFCMessageID: id}
		if err := e.b.store.UpsertMessages(context.Background(), []*store.Message{m}); err != nil {
			t.Fatal(err)
		}
		e.b.afterMailStored(context.Background(), other, m.ID, ingest.Policy{})
		got, err := e.b.store.GetMessage(context.Background(), other, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(func() []string {
			var out []string
			for _, f := range got.Flags {
				out = append(out, string(f))
			}
			return out
		}(), ",")
	}
	if flags := put("hello history", "hist@x", time.Now().Add(-time.Hour)); flags != "" {
		t.Errorf("a rule ran on the new account's history: %q", flags)
	}
	if flags := put("hello new", "new@x", time.Now().Add(time.Minute)); flags != string(api.FlagFlagged) {
		t.Errorf("new mail of the new account: %q", flags)
	}
}

// A move to a folder that is gone disables the rule with folderGone and
// leaves the message; junk and trash without such a folder are skipped.
func TestRulesFolderGone(t *testing.T) {
	e := newRulesEnv(t)
	r := e.create(subjectRule("p", "x", e.acc,
		api.RuleAction{Kind: api.RuleActionMove, FolderID: api.FolderID(e.folder["Projects"].ID)},
		api.RuleAction{Kind: api.RuleActionFlag}), false)
	if err := e.b.store.DeleteFolder(context.Background(), string(e.acc), e.folder["Projects"].ID); err != nil {
		t.Fatal(err)
	}
	id := e.arrive("INBOX", ruleMail("A <a@example.org>", "x", "g1@example.org"), e.clock())
	m := e.get(id)
	if m.FolderID != e.folder["INBOX"].ID || !slices.Contains(m.Flags, api.FlagFlagged) {
		t.Errorf("message %+v", m)
	}
	got := e.rule(r.ID)
	if got.Enabled || got.Problem != api.RuleProblemFolderGone {
		t.Fatalf("rule %+v", got)
	}
	// Saving it again (with a folder that exists) clears the problem.
	in := got.RuleInput
	in.Enabled = true
	in.Actions[0].FolderID = api.FolderID(e.folder["Archive"].ID)
	res, err := e.b.Rules().Update(context.Background(), api.RulesUpdateParams{ID: r.ID, Rule: in})
	if err != nil || res.Rule.Problem != "" || !res.Rule.Enabled {
		t.Fatalf("saved %+v %v", res, err)
	}
}

// rules.apply runs the local actions on a folder or a selection, never the
// outgoing ones, ignores the once-per-Message-ID record and the age, and
// never deletes what is in the trash already.
func TestRulesApply(t *testing.T) {
	e := newRulesEnv(t)
	ctx := context.Background()
	old := e.store("INBOX", ruleMail("A <a@example.org>", "sale 1", "a1@example.org"), e.clock().Add(-400*24*time.Hour))
	e.store("INBOX", ruleMail("A <a@example.org>", "sale 2", "a2@example.org"), e.clock())
	e.store("INBOX", ruleMail("A <a@example.org>", "other", "a3@example.org"), e.clock())
	r := e.create(subjectRule("sale", "sale", e.acc,
		api.RuleAction{Kind: api.RuleActionFlag},
		api.RuleAction{Kind: api.RuleActionAutoReply, Text: "Away."},
		api.RuleAction{Kind: api.RuleActionMove, FolderID: api.FolderID(e.folder["Archive"].ID)}), true)

	svc := e.b.Rules()
	for _, p := range []api.RulesApplyParams{
		{AccountID: e.acc},
		{AccountID: e.acc, FolderID: api.FolderID(e.folder["INBOX"].ID), MessageIDs: []api.MessageID{api.MessageID(old)}},
	} {
		if _, err := svc.Apply(ctx, p); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%+v: %v", p, err)
		}
	}
	if _, err := svc.Apply(ctx, api.RulesApplyParams{AccountID: e.acc, MessageIDs: []api.MessageID{"m_nope"}}); errCode(t, err) != api.CodeMessageNotFound {
		t.Errorf("unknown message: %v", err)
	}
	if _, err := svc.Apply(ctx, api.RulesApplyParams{AccountID: e.acc, FolderID: api.FolderID(e.folder["INBOX"].ID), RuleIDs: []string{"r_nope"}}); errCode(t, err) != api.CodeRuleNotFound {
		t.Errorf("unknown rule: %v", err)
	}

	res, err := svc.Apply(ctx, api.RulesApplyParams{AccountID: e.acc, FolderID: api.FolderID(e.folder["INBOX"].ID)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Examined != 3 || res.Matched != 2 || res.Changed != 2 || res.SkippedOutgoing != 2 {
		t.Errorf("result %+v", res)
	}
	if m := e.get(old); m.FolderID != e.folder["Archive"].ID || !slices.Contains(m.Flags, api.FlagFlagged) {
		t.Errorf("old message %+v", m)
	}
	e.b.waitRuleOutgoing()
	if q := e.outbox(); len(q) != 0 {
		t.Errorf("rules.apply sent %+v", q)
	}
	if got := e.rule(r.ID); got.Applied != 2 {
		t.Errorf("applied %d", got.Applied)
	}

	// Trash rules on the trash: nothing is deleted for good.
	inTrash := e.store("Trash", ruleMail("A <a@example.org>", "junkish", "t1@example.org"), e.clock())
	tr := e.create(subjectRule("trash", "junkish", "", api.RuleAction{Kind: api.RuleActionTrash}), false)
	res, err = svc.Apply(ctx, api.RulesApplyParams{AccountID: e.acc, FolderID: api.FolderID(e.folder["Trash"].ID), RuleIDs: []string{tr.ID}})
	if err != nil || res.Matched != 1 || res.Changed != 0 {
		t.Fatalf("trash apply %+v %v", res, err)
	}
	if m := e.get(inTrash); m.FolderID != e.folder["Trash"].ID {
		t.Errorf("trash message %+v", m)
	}
}

// rules.test counts the matches of a rule as edited and lists the newest.
func TestRulesTest(t *testing.T) {
	e := newRulesEnv(t)
	for i := 0; i < api.MaxRuleTestMessages+5; i++ {
		e.store("INBOX", ruleMail("A <a@example.org>", fmt.Sprintf("report %d", i), fmt.Sprintf("r%d@example.org", i)), e.clock().Add(time.Duration(i)*time.Minute))
	}
	e.store("INBOX", ruleMail("A <a@example.org>", "other", "x@example.org"), e.clock())
	res, err := e.b.Rules().Test(context.Background(), api.RulesTestParams{
		Rule:      api.RuleInput{Name: "t", Conditions: []api.RuleCondition{{Field: api.RuleFieldBody, Op: api.RuleOpContains, Value: "BODY OF REPORT"}}, Actions: []api.RuleAction{{Kind: api.RuleActionFlag}}},
		AccountID: e.acc, FolderID: api.FolderID(e.folder["INBOX"].ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != api.MaxRuleTestMessages+5 || len(res.Messages) != api.MaxRuleTestMessages || res.Messages[0].Subject != fmt.Sprintf("report %d", api.MaxRuleTestMessages+4) {
		t.Fatalf("count %d, %d messages, first %+v", res.Count, len(res.Messages), res.Messages[0])
	}
	if _, err := e.b.Rules().Test(context.Background(), api.RulesTestParams{Rule: api.RuleInput{Name: "t"}, AccountID: e.acc, FolderID: api.FolderID(e.folder["INBOX"].ID)}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("invalid rule: %v", err)
	}
}

// Removing an account removes its rules, not those for every account.
func TestRulesGoWithTheAccount(t *testing.T) {
	e := newRulesEnv(t)
	own := e.create(subjectRule("own", "x", e.acc, api.RuleAction{Kind: api.RuleActionFlag}), false)
	all := e.create(subjectRule("all", "x", "", api.RuleAction{Kind: api.RuleActionFlag}), false)
	if _, err := e.b.Rules().List(context.Background(), api.RulesListParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.b.Accounts().Remove(context.Background(), api.AccountRemoveParams{AccountID: e.acc, DeleteLocalData: true}); err != nil {
		t.Fatal(err)
	}
	list, _ := e.b.Rules().List(context.Background(), api.RulesListParams{})
	if len(list.Rules) != 1 || list.Rules[0].ID != all.ID {
		t.Fatalf("left %+v (own %s)", list.Rules, own.ID)
	}
	// The cache went too: nothing runs the removed rule.
	rs, _ := e.b.rulesFor(context.Background(), string(e.acc))
	if len(rs) != 1 {
		t.Errorf("cached %+v", rs)
	}
}

// The protections of the outgoing actions (R8): each refusal counts in
// skipped, nothing is queued; what passes is queued with Auto-Submitted
// and threading, and a sender is answered once in the interval.
func TestRulesOutgoingProtections(t *testing.T) {
	e := newRulesEnv(t)
	r := e.create(subjectRule("away", "hi", e.acc, api.RuleAction{Kind: api.RuleActionAutoReply, Text: "I am away."}), true)
	junk := e.create(subjectRule("junk", "spamhi", e.acc, api.RuleAction{Kind: api.RuleActionJunk}), false)
	_ = junk
	refused := []string{
		ruleMail("News <news@shop.example>", "hi bulk", "b1@x", "List-Unsubscribe: <https://shop.example/u>"),
		ruleMail("Bot <bot@x.example>", "hi auto", "b2@x", "Auto-Submitted: auto-replied"),
		ruleMail("Bot <bot@x.example>", "hi loop", "b3@x", "X-Loop: other@x.example"),
		ruleMail("Me <me@example.invalid>", "hi self", "b4@x"),
		ruleMail("Me <ME@Example.Invalid>", "hi self case", "b5@x"),
		ruleMail("Daemon <MAILER-DAEMON@x.example>", "hi bounce", "b6@x"),
		ruleMail("PM <postmaster@x.example>", "hi pm", "b7@x"),
		ruleMail("Shop <no-reply@x.example>", "hi noreply", "b8@x"),
		ruleMail("Shop <do_not_reply@x.example>", "hi dnr", "b9@x"),
		ruleMail("Bob <bob@x.example>", "hi reply to me", "b10@x", "Reply-To: me@example.invalid"),
		ruleMail("Spammer <s@x.example>", "spamhi", "b11@x"),
	}
	for i, raw := range refused {
		e.arrive("INBOX", raw, e.clock())
		e.b.waitRuleOutgoing()
		if got := e.rule(r.ID); got.Skipped != i+1 {
			t.Fatalf("%d (%s): skipped %d", i, strings.SplitN(raw, "\r\n", 3)[2], got.Skipped)
		}
	}
	if q := e.outbox(); len(q) != 0 {
		t.Fatalf("queued %+v", q)
	}

	// An ordinary message is answered, to Reply-To, with the threading.
	e.arrive("INBOX", ruleMail("Carol <carol@x.example>", "hi there", "c1@x", "Reply-To: Carol Desk <desk@x.example>", "References: <c0@x>"), e.clock())
	e.b.waitRuleOutgoing()
	q := e.outbox()
	if len(q) != 1 || q[0].Subject != "Re: hi there" || q[0].To[0].Address != "desk@x.example" {
		t.Fatalf("queued %+v", q)
	}
	raw := e.raw(q[0].ID)
	for _, want := range []string{"Auto-Submitted: auto-replied", "X-Loop: me@example.invalid", "In-Reply-To: <c1@x>", "<c0@x> <c1@x>", "I am away."} {
		if !strings.Contains(raw, want) {
			t.Errorf("reply lacks %q:\n%s", want, raw)
		}
	}
	// The same sender again: once in the interval.
	e.arrive("INBOX", ruleMail("Carol <carol@x.example>", "hi again", "c2@x", "Reply-To: desk@x.example"), e.clock())
	e.b.waitRuleOutgoing()
	if q := e.outbox(); len(q) != 1 {
		t.Fatalf("answered twice: %+v", q)
	}
	e.advance(api.RuleReplyInterval + time.Minute)
	e.arrive("INBOX", ruleMail("Carol <carol@x.example>", "hi later", "c3@x", "Reply-To: desk@x.example"), e.clock())
	e.b.waitRuleOutgoing()
	if q := e.outbox(); len(q) != 2 {
		t.Fatalf("not answered after the interval: %+v", q)
	}
}

// A rule sends at most RuleOutgoingPerHour messages an hour; a forward
// attaches the original whole.
func TestRulesForwardHourlyLimit(t *testing.T) {
	e := newRulesEnv(t)
	r := e.create(subjectRule("fwd", "order", e.acc, api.RuleAction{Kind: api.RuleActionForward, To: "boss@example.org"}), true)
	for i := 0; i < api.RuleOutgoingPerHour+2; i++ {
		e.arrive("INBOX", ruleMail(fmt.Sprintf("C <c%d@x.example>", i), fmt.Sprintf("order %d", i), fmt.Sprintf("o%d@x", i)), e.clock())
	}
	e.b.waitRuleOutgoing()
	q := e.outbox()
	if len(q) != api.RuleOutgoingPerHour {
		t.Fatalf("%d queued", len(q))
	}
	if got := e.rule(r.ID); got.Skipped != 2 || got.Applied != api.RuleOutgoingPerHour+2 {
		t.Errorf("rule %+v", got)
	}
	var first api.MessageSummary
	for _, m := range q {
		if m.Subject == "Fwd: order 0" {
			first = m
		}
	}
	if first.ID == "" {
		t.Fatalf("no forward of order 0 in %+v", q)
	}
	raw := e.raw(first.ID)
	for _, want := range []string{"Auto-Submitted: auto-forwarded", "X-Loop: me@example.invalid", "boss@example.org", `filename="order 0.eml"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("forward lacks %q:\n%s", want, raw)
		}
	}
	if att := attachedOriginal(t, raw); !strings.Contains(att, "Message-ID: <o0@x>") || !strings.Contains(att, "Hello, the body of order 0.") {
		t.Errorf("the original is not attached whole:\n%s", att)
	}
	e.advance(time.Hour + time.Second)
	e.arrive("INBOX", ruleMail("C <late@x.example>", "order late", "late@x"), e.clock())
	e.b.waitRuleOutgoing()
	if q := e.outbox(); len(q) != api.RuleOutgoingPerHour+1 {
		t.Errorf("after the hour: %d", len(q))
	}
}

// A message that cannot be queued disables the rule with sendFailed.
func TestRulesSendFailed(t *testing.T) {
	e := newRulesEnv(t)
	r := e.create(subjectRule("away", "hi", e.acc, api.RuleAction{Kind: api.RuleActionAutoReply, Text: "Away."}), true)
	ctx := context.Background()
	a, err := e.b.store.GetAccount(ctx, string(e.acc))
	if err != nil {
		t.Fatal(err)
	}
	a.Config.Email = "not an address"
	if err := e.b.store.UpdateAccount(ctx, &a); err != nil {
		t.Fatal(err)
	}
	e.arrive("INBOX", ruleMail("Carol <carol@x.example>", "hi", "s1@x"), e.clock())
	e.b.waitRuleOutgoing()
	got := e.rule(r.ID)
	if got.Enabled || got.Problem != api.RuleProblemSendFailed {
		t.Fatalf("rule %+v", got)
	}
}

// The hook runs concurrently for many messages and for the same one: each
// Message-ID once, a sender answered once.
func TestRulesConcurrent(t *testing.T) {
	e := newRulesEnv(t)
	r := e.create(subjectRule("away", "hi", e.acc, api.RuleAction{Kind: api.RuleActionAutoReply, Text: "Away."},
		api.RuleAction{Kind: api.RuleActionFlag}), true)
	var ids []string
	for i := 0; i < 8; i++ {
		ids = append(ids, e.store("INBOX", ruleMail("Carol <carol@x.example>", fmt.Sprintf("hi %d", i), fmt.Sprintf("p%d@x", i)), e.clock()))
	}
	same := e.store("INBOX", ruleMail("Dan <dan@x.example>", "hi same", "same@x"), e.clock())
	var wg sync.WaitGroup
	for _, id := range ids {
		for k := 0; k < 2; k++ {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				e.b.afterMailStored(context.Background(), string(e.acc), id, ingest.Policy{})
			}(id)
		}
	}
	for k := 0; k < 4; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.b.afterMailStored(context.Background(), string(e.acc), same, ingest.Policy{})
		}()
	}
	wg.Wait()
	e.b.waitRuleOutgoing()
	if got := e.rule(r.ID); got.Applied != 9 || got.Skipped != 7 {
		t.Errorf("rule %+v", got)
	}
	if q := e.outbox(); len(q) != 2 {
		t.Errorf("%d queued", len(q))
	}
}

// attachedOriginal is the body of the first attachment of a message.
func attachedOriginal(t *testing.T, raw string) string {
	t.Helper()
	mr, err := gomail.CreateReader(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for {
		p, err := mr.NextPart()
		if err != nil {
			return ""
		}
		if _, ok := p.Header.(*gomail.AttachmentHeader); ok {
			data, _ := io.ReadAll(p.Body)
			return string(data)
		}
	}
}
