// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package quickreply

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/capabilities"
	"github.com/schotek/malachi/ui/internal/conversation"
)

// rpcCall is one call the fake host saw: the method and its params by value.
type rpcCall struct {
	method string
	params any
}

// fakeHost records what the controller asks of the window. Its main loop
// is the test's goroutine: Post queues, drain runs what was posted. The
// daemon answers from results (decoded into the call's result as the
// client would) or errs; gate, when set for a method, holds that call
// until the test closes it.
type fakeHost struct {
	mu      sync.Mutex
	calls   []rpcCall
	results map[string]any
	errs    map[string]error
	gate    map[string]chan struct{}

	posted chan func()

	// Main loop only.
	log          []string // the host's own methods and the calls, in order
	attributions []api.MessageID
	before       []api.MessageID
	holdBefore   bool
	beforeDone   []func()
	opens        []openCall
	sent         []bool
	failed       []error
}

type openCall struct {
	mode    conversation.QuickReplyMode
	id      api.MessageID
	ownHTML string
	opened  func()
}

func newFakeHost() *fakeHost {
	return &fakeHost{
		results: map[string]any{},
		errs:    map[string]error{},
		gate:    map[string]chan struct{}{},
		posted:  make(chan func(), 16),
	}
}

func (h *fakeHost) Call(ctx context.Context, method string, params any, result any) error {
	h.mu.Lock()
	h.calls = append(h.calls, rpcCall{method, params})
	gate, res, err := h.gate[method], h.results[method], h.errs[method]
	h.mu.Unlock()
	if _, ok := ctx.Deadline(); !ok {
		panic("a call without a deadline")
	}
	if gate != nil {
		<-gate
	}
	if err != nil {
		return err
	}
	if res != nil {
		b, _ := json.Marshal(res)
		return json.Unmarshal(b, result)
	}
	return nil
}

func (h *fakeHost) Post(f func()) { h.posted <- f }

func (h *fakeHost) Attribution(mode conversation.QuickReplyMode, id api.MessageID) string {
	h.log = append(h.log, "attribution")
	h.attributions = append(h.attributions, id)
	return "On Monday, Jana wrote:"
}

func (h *fakeHost) BeforeCreate(id api.MessageID, done func()) {
	h.log = append(h.log, "before")
	h.before = append(h.before, id)
	if h.holdBefore {
		h.beforeDone = append(h.beforeDone, done)
		return
	}
	done()
}

func (h *fakeHost) OpenCompose(mode conversation.QuickReplyMode, id api.MessageID, ownHTML string, opened func()) {
	h.log = append(h.log, "open")
	h.opens = append(h.opens, openCall{mode, id, ownHTML, opened})
}

func (h *fakeHost) Sent(comment bool) {
	h.log = append(h.log, "sent")
	h.sent = append(h.sent, comment)
}

func (h *fakeHost) Failed(err error) {
	h.log = append(h.log, "failed")
	h.failed = append(h.failed, err)
}

// drain runs one posted callback (the end of a Send), waiting for it.
func (h *fakeHost) drain(t *testing.T) {
	t.Helper()
	select {
	case f := <-h.posted:
		f()
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was posted")
	}
}

func (h *fakeHost) methods() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.calls))
	for i, c := range h.calls {
		out[i] = c.method
	}
	return out
}

func (h *fakeHost) params(method string) any {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.calls {
		if c.method == method {
			return c.params
		}
	}
	return nil
}

// The fixtures: a mail account and an issue tracker's, a conversation
// whose newest message of someone else is m2 (m3 is the user's).
var (
	mailAcc = api.Account{ID: "acc", Capabilities: api.MailCapabilities}
	jiraAcc = api.Account{ID: "jira", Capabilities: []api.AccountCapability{api.CapabilityComment, api.CapabilityForward, api.CapabilityTransition}}

	mailActions    = capabilities.Actions{Reply: true, ReplyAll: true, Forward: true}
	commentActions = capabilities.Actions{Reply: true, Forward: true, Comment: true}

	original = `<blockquote type="cite"><p>Hi</p><img src="cid:pic1"></blockquote>`
)

func item(id string, acc api.AccountID, mine bool) conversation.Item {
	return conversation.Item{Kind: conversation.ItemMessage, Sender: "Jana " + id, Mine: mine,
		Message: api.MessageSummary{ID: api.MessageID(id), AccountID: acc}}
}

func model(thread api.ThreadID, acc api.AccountID) conversation.Model {
	return conversation.Model{Thread: thread, Items: []conversation.Item{
		item("m1", acc, false), item("m2", acc, false), item("m3", acc, true),
	}}
}

func created(acc api.AccountID) api.DraftCreateResult {
	return api.DraftCreateResult{Draft: api.Draft{
		AccountID: acc,
		Subject:   "Re: Lunch",
		To:        []api.Address{{Name: "Jana", Address: "jana@example.org"}},
		CC:        []api.Address{{Address: "petr@example.org"}},
		HTMLBody:  original,
		TextBody:  "> Hi",
		InReplyTo: "m2",
		Attachments: []api.DraftAttachment{
			{ID: "att1", Filename: "pic.png", ContentType: "image/png", Size: 12, Inline: true, ContentID: "pic1"},
		},
	}}
}

// newShown is a controller showing thread t1 of the mail account as chat.
func newShown(h *fakeHost) (*Controller, *int) {
	c := New(h)
	n := 0
	c.OnChange = func() { n++ }
	c.SetShown(true)
	c.Show(mailAcc.ID, "t1")
	c.Update(model("t1", mailAcc.ID), mailActions, mailAcc)
	return c, &n
}

func mailHost() *fakeHost {
	h := newFakeHost()
	h.results[api.MethodDraftCreate] = created(mailAcc.ID)
	h.results[api.MethodDraftSave] = api.DraftSaveResult{DraftID: "d1", Version: 1}
	h.results[api.MethodMessageSend] = api.MessageSendResult{OutboxID: "o1"}
	return h
}

func TestTargetAndModes(t *testing.T) {
	h := newFakeHost()
	c, _ := newShown(h)
	s := c.State()
	if !s.Visible || s.Target.Message.ID != "m2" || s.Thread != "t1" || s.Comment {
		t.Fatalf("mail: %+v", s)
	}
	want := []conversation.QuickReplyMode{conversation.QuickReply, conversation.QuickReplyAll, conversation.QuickForward}
	if !reflect.DeepEqual(s.Modes, want) || s.Mode != conversation.QuickReply {
		t.Errorf("mail modes %v mode %v", s.Modes, s.Mode)
	}

	c.Update(model("t1", jiraAcc.ID), commentActions, jiraAcc)
	s = c.State()
	if !s.Visible || !s.Comment || !reflect.DeepEqual(s.Modes, []conversation.QuickReplyMode{conversation.QuickReply}) || s.Mode != conversation.QuickReply {
		t.Errorf("comment: %+v", s)
	}

	c.Update(model("t1", mailAcc.ID), capabilities.Actions{}, mailAcc)
	if s = c.State(); s.Visible || len(s.Modes) != 0 {
		t.Errorf("no actions: %+v", s)
	}

	// Forward without Reply: the menu would have it, the bar is hidden.
	c.Update(model("t1", mailAcc.ID), capabilities.Actions{Forward: true}, mailAcc)
	if s = c.State(); s.Visible {
		t.Errorf("forward only: %+v", s)
	}
}

func TestVisibility(t *testing.T) {
	h := newFakeHost()
	c := New(h)
	c.Update(model("t1", mailAcc.ID), mailActions, mailAcc)
	if c.State().Visible {
		t.Error("visible in the timeline")
	}
	c.SetShown(true)
	if s := c.State(); !s.Visible || s.Thread != "t1" {
		t.Errorf("Update did not show t1: %+v", s)
	}
	c.Show(mailAcc.ID, "t2")
	if s := c.State(); s.Visible || s.Target.Message.ID != "" {
		t.Errorf("a target before Update: %+v", s)
	}
	c.Update(conversation.Model{Thread: "t2"}, mailActions, mailAcc)
	if c.State().Visible {
		t.Error("visible without a target")
	}
	c.Show("", "")
	c.SetText("lost")
	if s := c.State(); s.Visible || s.Text != "" || len(c.threads) != 0 {
		t.Errorf("no conversation: %+v %v", s, c.threads)
	}
}

func TestBlank(t *testing.T) {
	c, _ := newShown(newFakeHost())
	for text, blank := range map[string]bool{
		"": true, " \n\t": true, "​⁦": true, "\x00": true, "a": false, " ok ": false,
	} {
		c.SetText(text)
		if got := c.State().Blank; got != blank {
			t.Errorf("%q: blank %v", text, got)
		}
	}
}

func TestOnChange(t *testing.T) {
	c, n := newShown(newFakeHost())
	before := *n
	c.SetText("a")
	c.SetText("a") // no change
	c.SetMode(conversation.QuickReplyAll)
	c.SetMode(conversation.QuickReplyAll)
	c.SetMode(conversation.QuickForward) // ignored
	c.SetShown(true)                     // no change
	c.Show(mailAcc.ID, "t1")             // no change
	if got := *n - before; got != 2 {
		t.Errorf("OnChange fired %d times, want 2", got)
	}
}

func TestTextAndModePerThread(t *testing.T) {
	c, _ := newShown(newFakeHost())
	c.SetText("for t1")
	c.SetMode(conversation.QuickReplyAll)

	c.Show(mailAcc.ID, "t2")
	c.Update(model("t2", mailAcc.ID), mailActions, mailAcc)
	if s := c.State(); s.Text != "" || s.Mode != conversation.QuickReply {
		t.Fatalf("t2 starts with %+v", s)
	}
	c.SetText("for t2")

	c.Show(mailAcc.ID, "t1")
	c.Update(model("t1", mailAcc.ID), mailActions, mailAcc)
	if s := c.State(); s.Text != "for t1" || s.Mode != conversation.QuickReplyAll {
		t.Errorf("t1 back: %+v", s)
	}
	// A mode no longer offered falls back to the first.
	c.Update(model("t1", mailAcc.ID), capabilities.Actions{Reply: true}, mailAcc)
	if s := c.State(); s.Mode != conversation.QuickReply {
		t.Errorf("mode not offered: %v", s.Mode)
	}
	c.SetMode(conversation.QuickReplyAll) // not offered: ignored
	c.Update(model("t1", mailAcc.ID), mailActions, mailAcc)
	if s := c.State(); s.Mode != conversation.QuickReplyAll {
		t.Errorf("the chosen mode was forgotten: %v", s.Mode)
	}
	c.Show(mailAcc.ID, "t2")
	if s := c.State(); s.Text != "for t2" {
		t.Errorf("t2 back: %+v", s)
	}
	// An emptied conversation is forgotten.
	c.SetText("")
	if _, ok := c.threads[key{mailAcc.ID, "t2"}]; ok {
		t.Error("t2 kept with nothing in it")
	}
	// Choosing the mode of the zero state keeps nothing either.
	c.SetMode(conversation.QuickReply)
	if _, ok := c.threads[key{mailAcc.ID, "t2"}]; ok {
		t.Error("t2 kept for the default mode")
	}
}

func TestSend(t *testing.T) {
	h := mailHost()
	c, _ := newShown(h)
	c.SetMode(conversation.QuickReplyAll)
	text := "Sure,\nsee you <there> & then"
	c.SetText(text)
	c.Send()
	if s := c.State(); !s.Sending {
		t.Fatal("not sending")
	}
	c.Send() // a second Send while one is on its way
	h.drain(t)

	if want := []string{api.MethodDraftCreate, api.MethodDraftSave, api.MethodMessageSend}; !reflect.DeepEqual(h.methods(), want) {
		t.Fatalf("calls %v, want %v", h.methods(), want)
	}
	if want := []string{"before", "attribution", "sent"}; !reflect.DeepEqual(h.log, want) {
		t.Errorf("host %v, want %v", h.log, want)
	}
	if !reflect.DeepEqual(h.before, []api.MessageID{"m2"}) {
		t.Errorf("downloaded %v", h.before)
	}
	wantCreate := api.DraftCreateParams{AccountID: "acc", Mode: api.ComposeReplyAll, MessageID: "m2", Attribution: "On Monday, Jana wrote:"}
	if got := h.params(api.MethodDraftCreate); !reflect.DeepEqual(got, wantCreate) {
		t.Errorf("draft.create %+v", got)
	}
	tmpl := created(mailAcc.ID).Draft
	wantDraft := api.Draft{
		AccountID:   "acc",
		To:          tmpl.To,
		CC:          tmpl.CC,
		Subject:     "Re: Lunch",
		TextBody:    text,
		HTMLBody:    conversation.QuickReplyBody(api.ComposeReplyAll, text, tmpl),
		InReplyTo:   "m2",
		Attachments: []api.DraftAttachment{{ID: "att1"}},
	}
	got := h.params(api.MethodDraftSave).(api.DraftSaveParams).Draft
	if !reflect.DeepEqual(got, wantDraft) {
		t.Errorf("draft.save\n%+v\nwant\n%+v", got, wantDraft)
	}
	if body := "<p>Sure,<br>see you &lt;there&gt; &amp; then</p>\n" + original; got.HTMLBody != body {
		t.Errorf("body %q, want %q", got.HTMLBody, body)
	}
	if want := (api.MessageSendParams{AccountID: "acc", DraftID: "d1", Version: 1}); h.params(api.MethodMessageSend) != want {
		t.Errorf("message.send %+v", h.params(api.MethodMessageSend))
	}
	s := c.State()
	if s.Sending || s.Text != "" || !reflect.DeepEqual(h.sent, []bool{false}) || len(h.failed) != 0 {
		t.Errorf("after: %+v sent %v failed %v", s, h.sent, h.failed)
	}
	// The mode stays for the next reply.
	if s.Mode != conversation.QuickReplyAll {
		t.Errorf("mode %v", s.Mode)
	}
}

// To is never nil on the wire (the daemon reads "to": null as no field).
func TestSaveDraftToNeverNil(t *testing.T) {
	d := SaveDraft(api.ComposeReply, "x", api.Draft{AccountID: "acc"})
	if d.To == nil || len(d.To) != 0 || d.Attachments != nil || d.Comment != nil {
		t.Errorf("%+v", d)
	}
	b, _ := json.Marshal(d)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if to, ok := m["to"].([]any); !ok || len(to) != 0 {
		t.Errorf("to on the wire: %s", b)
	}
}

func TestSendComment(t *testing.T) {
	h := newFakeHost()
	tmpl := api.Draft{AccountID: "jira", To: nil, Subject: "PRJ-1: Bug", InReplyTo: "m2",
		Comment: &api.DraftComment{Issue: api.IssueInfo{Key: "PRJ-1"}, Visibility: api.CommentInternal}}
	h.results[api.MethodDraftCreate] = api.DraftCreateResult{Draft: tmpl}
	h.results[api.MethodDraftSave] = api.DraftSaveResult{DraftID: "d1", Version: 1}
	c := New(h)
	c.SetShown(true)
	c.Update(model("t1", jiraAcc.ID), commentActions, jiraAcc)
	c.SetText("Fixed in 1.2\n\nThanks")
	c.Send()
	h.drain(t)

	if want := []string{"sent"}; !reflect.DeepEqual(h.log, want) {
		t.Errorf("host %v, want %v (no download, no attribution)", h.log, want)
	}
	if want := (api.DraftCreateParams{AccountID: "jira", Mode: api.ComposeReply, MessageID: "m2"}); !reflect.DeepEqual(h.params(api.MethodDraftCreate), want) {
		t.Errorf("draft.create %+v", h.params(api.MethodDraftCreate))
	}
	d := h.params(api.MethodDraftSave).(api.DraftSaveParams).Draft
	if d.HTMLBody != "<p>Fixed in 1.2</p><p>Thanks</p>" {
		t.Errorf("comment body %q", d.HTMLBody)
	}
	if d.Comment == nil || d.Comment.Visibility != api.CommentPublic || d.Comment.Issue.Key != "PRJ-1" {
		t.Errorf("comment %+v", d.Comment)
	}
	if !reflect.DeepEqual(h.sent, []bool{true}) {
		t.Errorf("sent %v", h.sent)
	}
}

func TestSendFailures(t *testing.T) {
	for _, c := range []struct {
		method string
		calls  []string
	}{
		{api.MethodDraftCreate, []string{api.MethodDraftCreate}},
		{api.MethodDraftSave, []string{api.MethodDraftCreate, api.MethodDraftSave}},
		{api.MethodMessageSend, []string{api.MethodDraftCreate, api.MethodDraftSave, api.MethodMessageSend, api.MethodDraftDelete}},
	} {
		t.Run(c.method, func(t *testing.T) {
			h := mailHost()
			fail := &api.Error{Code: api.CodeInternalError, Message: "boom"}
			h.errs[c.method] = fail
			h.errs[api.MethodDraftDelete] = errors.New("ignored")
			ctl, _ := newShown(h)
			ctl.SetText("keep me")
			ctl.Send()
			h.drain(t)
			if !reflect.DeepEqual(h.methods(), c.calls) {
				t.Errorf("calls %v, want %v", h.methods(), c.calls)
			}
			if len(h.failed) != 1 || h.failed[0] != error(fail) || len(h.sent) != 0 {
				t.Errorf("failed %v sent %v", h.failed, h.sent)
			}
			if s := ctl.State(); s.Text != "keep me" || s.Sending {
				t.Errorf("after: %+v", s)
			}
			if c.method == api.MethodMessageSend {
				if want := (api.DraftDeleteParams{AccountID: "acc", DraftID: "d1"}); h.params(api.MethodDraftDelete) != want {
					t.Errorf("draft.delete %+v", h.params(api.MethodDraftDelete))
				}
			}
		})
	}
}

// What Send does not do: blank text, a hidden bar, a Send on its way.
func TestSendGuards(t *testing.T) {
	h := mailHost()
	c, _ := newShown(h)
	c.SetText(" \n ")
	c.Send()
	c.SetText("text")
	c.SetShown(false)
	c.Send()
	c.SetShown(true)
	c.Update(model("t1", mailAcc.ID), capabilities.Actions{}, mailAcc)
	c.Send()
	if len(h.log) != 0 || len(h.methods()) != 0 {
		t.Errorf("sent anyway: %v %v", h.log, h.methods())
	}
}

// A Send ends in the conversation it started in, even when another is
// shown by then; that one's text and bar are left alone.
func TestSendingPerThreadAcrossASwitch(t *testing.T) {
	h := mailHost()
	gate := make(chan struct{})
	h.gate[api.MethodMessageSend] = gate
	c, _ := newShown(h)
	c.SetText("to t1")
	c.Send()

	c.Show(mailAcc.ID, "t2")
	c.Update(model("t2", mailAcc.ID), mailActions, mailAcc)
	if s := c.State(); s.Sending || s.Text != "" {
		t.Fatalf("t2 sending: %+v", s)
	}
	c.SetText("to t2")
	c.Send() // t2 sends on its own
	c.Show(mailAcc.ID, "t1")
	if s := c.State(); !s.Sending {
		t.Fatal("t1 not sending")
	}
	c.Show(mailAcc.ID, "t2")
	close(gate)
	h.drain(t)
	h.drain(t)
	if s := c.State(); s.Sending || s.Text != "" {
		t.Errorf("t2 after: %+v", s)
	}
	c.Show(mailAcc.ID, "t1")
	if s := c.State(); s.Sending || s.Text != "" {
		t.Errorf("t1 after: %+v", s)
	}
	if len(h.sent) != 2 || len(c.threads) != 0 {
		t.Errorf("sent %v threads %v", h.sent, c.threads)
	}
}

// One conversation's failure while another is shown keeps its text there.
func TestFailureAfterASwitch(t *testing.T) {
	h := mailHost()
	h.holdBefore = true
	h.errs[api.MethodDraftCreate] = errors.New("down")
	c, _ := newShown(h)
	c.SetText("to t1")
	c.Send()
	c.Show(mailAcc.ID, "t2")
	c.Update(model("t2", mailAcc.ID), mailActions, mailAcc)
	c.SetText("to t2")
	h.beforeDone[0]() // the download ends while t2 is shown
	if !reflect.DeepEqual(h.attributions, []api.MessageID{"m2"}) {
		t.Errorf("attribution of %v", h.attributions)
	}
	h.drain(t)
	if s := c.State(); s.Text != "to t2" || s.Sending {
		t.Errorf("t2: %+v", s)
	}
	c.Show(mailAcc.ID, "t1")
	if s := c.State(); s.Text != "to t1" || s.Sending || len(h.failed) != 1 {
		t.Errorf("t1: %+v failed %v", s, h.failed)
	}
}

// What was typed while sending stays after a success.
func TestTextTypedWhileSendingStays(t *testing.T) {
	h := mailHost()
	gate := make(chan struct{})
	h.gate[api.MethodDraftSave] = gate
	c, _ := newShown(h)
	c.SetText("first")
	c.Send()
	c.SetText("first and more")
	close(gate)
	h.drain(t)
	if s := c.State(); s.Text != "first and more" || len(h.sent) != 1 {
		t.Errorf("after: %+v", s)
	}
}

func TestOpenWindowAndForward(t *testing.T) {
	h := newFakeHost()
	c, _ := newShown(h)
	c.SetMode(conversation.QuickReplyAll)
	c.SetText("a < b\n\nc")

	c.OpenWindow()
	if len(h.opens) != 1 {
		t.Fatalf("opens %v", h.opens)
	}
	o := h.opens[0]
	if o.mode != conversation.QuickReplyAll || o.id != "m2" || o.ownHTML != "<p>a &lt; b</p><p>c</p>" {
		t.Errorf("open %+v", o)
	}
	// No window yet: the text stays.
	if s := c.State(); s.Text != "a < b\n\nc" {
		t.Fatalf("cleared before the window opened: %+v", s)
	}
	o.opened()
	if s := c.State(); s.Text != "" {
		t.Errorf("not cleared once opened: %+v", s)
	}

	// Forward: the forward mode, the text above it; a window that never
	// opens leaves the text.
	c.SetText("fyi")
	c.Forward()
	if o = h.opens[1]; o.mode != conversation.QuickForward || o.ownHTML != "<p>fyi</p>" {
		t.Errorf("forward %+v", o)
	}
	if s := c.State(); s.Text != "fyi" || s.Mode != conversation.QuickReplyAll {
		t.Errorf("after forward: %+v", s)
	}

	// Opened after the conversation changed: it clears its own text only.
	c.Show(mailAcc.ID, "t2")
	c.Update(model("t2", mailAcc.ID), mailActions, mailAcc)
	c.SetText("t2 text")
	o.opened()
	if s := c.State(); s.Text != "t2 text" {
		t.Errorf("t2: %+v", s)
	}
	c.Show(mailAcc.ID, "t1")
	if s := c.State(); s.Text != "" {
		t.Errorf("t1: %+v", s)
	}

	// An empty bar opens an empty editor; no draft is saved by the bar.
	c.Update(model("t1", mailAcc.ID), mailActions, mailAcc)
	c.OpenWindow()
	if o = h.opens[2]; o.ownHTML != "" {
		t.Errorf("empty: %+v", o)
	}
	if len(h.methods()) != 0 {
		t.Errorf("calls %v", h.methods())
	}
}

func TestOpenGuards(t *testing.T) {
	h := mailHost()
	c := New(h)
	c.Update(model("t1", mailAcc.ID), mailActions, mailAcc)
	c.OpenWindow() // timeline: hidden
	c.SetShown(true)
	c.Update(model("t1", mailAcc.ID), capabilities.Actions{Reply: true}, mailAcc)
	c.Forward() // the account does not forward
	if len(h.opens) != 0 {
		t.Fatalf("opened %v", h.opens)
	}
	// While sending neither opens.
	gate := make(chan struct{})
	h.gate[api.MethodDraftCreate] = gate
	c.SetText("x")
	c.Send()
	c.OpenWindow()
	close(gate)
	h.drain(t)
	if len(h.opens) != 0 {
		t.Errorf("opened while sending %v", h.opens)
	}
	// A comment opens the window as a reply (the window decides it is a comment).
	c.Update(model("t1", jiraAcc.ID), commentActions, jiraAcc)
	c.OpenWindow()
	if len(h.opens) != 1 || h.opens[0].mode != conversation.QuickReply {
		t.Errorf("comment %v", h.opens)
	}
}

// single is message id of thread t of the mail account shown alone.
func single(id string, t api.ThreadID, mine bool) conversation.Model {
	return conversation.SingleModel(api.MessageSummary{
		ID: api.MessageID(id), AccountID: mailAcc.ID, ThreadID: t,
		From: []api.Address{{Name: "Jana " + id, Address: "jana@example.org"}},
	}, mine)
}

// showSingle is what the host does for the page of a single message.
func showSingle(c *Controller, m conversation.Model) {
	c.Show(mailAcc.ID, m.Thread)
	c.Update(m, mailActions, mailAcc)
	c.SetShown(true)
}

// The page of a single message (docs/plans/chat-view.md §12): the bar
// answers the message shown, the user's own too, and Send goes to it.
func TestSingleMessage(t *testing.T) {
	h := mailHost()
	c := New(h)
	showSingle(c, single("m7", "t1", false))
	s := c.State()
	if !s.Visible || s.Thread != "t1" || s.Target.Message.ID != "m7" || s.Target.Sender != "Jana m7" || s.Comment {
		t.Fatalf("single: %+v", s)
	}
	if len(s.Modes) != 3 {
		t.Errorf("modes %v", s.Modes)
	}
	c.SetText("Thanks")
	c.Send()
	h.drain(t)
	if p, ok := h.params(api.MethodDraftCreate).(api.DraftCreateParams); !ok || p.MessageID != "m7" || p.Mode != api.ComposeReply || p.AccountID != mailAcc.ID {
		t.Errorf("draft.create %+v", h.params(api.MethodDraftCreate))
	}
	if len(h.sent) != 1 || c.State().Text != "" {
		t.Errorf("sent %v, text %q", h.sent, c.State().Text)
	}

	// The user's own message (Sent): a reply to it.
	showSingle(c, single("s1", "t2", true))
	if s := c.State(); !s.Visible || s.Target.Message.ID != "s1" || !s.Target.Mine {
		t.Errorf("own: %+v", s)
	}

	// An issue's event alone has no target: hidden whatever the host says.
	ev := conversation.SingleModel(api.MessageSummary{ID: "e1", AccountID: jiraAcc.ID, ThreadID: "tj",
		Issue: &api.MessageIssue{Item: api.IssueItemEvent}}, false)
	c.Show(jiraAcc.ID, ev.Thread)
	c.Update(ev, commentActions, jiraAcc)
	if s := c.State(); s.Visible {
		t.Errorf("event: %+v", s)
	}
	// An issue's comment: a comment.
	cm := conversation.SingleModel(api.MessageSummary{ID: "c1", AccountID: jiraAcc.ID, ThreadID: "tj",
		Issue: &api.MessageIssue{Item: api.IssueItemComment}}, false)
	c.Update(cm, commentActions, jiraAcc)
	if s := c.State(); !s.Visible || !s.Comment || s.Target.Message.ID != "c1" {
		t.Errorf("comment: %+v", s)
	}
}

// SetShown alone hides and shows the bar; what was typed stays.
func TestSetShown(t *testing.T) {
	c := New(newFakeHost())
	n := 0
	c.OnChange = func() { n++ }
	showSingle(c, single("m1", "t1", false))
	c.SetText("draft")
	before := n
	c.SetShown(false)
	if s := c.State(); s.Visible || s.Text != "draft" {
		t.Errorf("hidden: %+v", s)
	}
	c.SetShown(false) // no change
	c.SetShown(true)
	if s := c.State(); !s.Visible || s.Text != "draft" {
		t.Errorf("shown again: %+v", s)
	}
	if got := n - before; got != 2 {
		t.Errorf("OnChange fired %d times, want 2", got)
	}
}

// The text typed under a single message is its thread's: it stays through
// the conversation of the same thread in the timeline (no bar) and in the
// chat (the bar with the text), back to another message of the thread, and
// through a message of another thread.
func TestTextKeptAcrossPages(t *testing.T) {
	c := New(newFakeHost())
	showSingle(c, single("m2", "t1", false))
	c.SetText("half a reply")
	c.SetMode(conversation.QuickReplyAll)

	// The grouped list: the conversation t1, in the timeline.
	c.Show(mailAcc.ID, "t1")
	c.Update(model("t1", mailAcc.ID), mailActions, mailAcc)
	c.SetShown(false)
	if s := c.State(); s.Visible || s.Text != "half a reply" {
		t.Errorf("timeline: %+v", s)
	}
	// The chat layout.
	c.SetShown(true)
	if s := c.State(); !s.Visible || s.Text != "half a reply" || s.Mode != conversation.QuickReplyAll || s.Target.Message.ID != "m2" {
		t.Errorf("chat: %+v", s)
	}
	// The timeline again, then the flat list with m1 of t1 alone.
	c.SetShown(false)
	showSingle(c, single("m1", "t1", false))
	if s := c.State(); !s.Visible || s.Text != "half a reply" || s.Target.Message.ID != "m1" {
		t.Errorf("m1: %+v", s)
	}
	// A message of another thread starts empty; back to t1 the text is there.
	showSingle(c, single("x1", "t9", false))
	if s := c.State(); s.Text != "" || s.Mode != conversation.QuickReply {
		t.Errorf("t9: %+v", s)
	}
	showSingle(c, single("m2", "t1", false))
	if s := c.State(); s.Text != "half a reply" || s.Mode != conversation.QuickReplyAll {
		t.Errorf("t1 back: %+v", s)
	}
}

// The Outbox and Drafts have no bar: the host says SetShown(false) for a
// message there, and nothing sends or opens; the text of its thread stays.
func TestOutboxAndDraftsHaveNoBar(t *testing.T) {
	h := mailHost()
	c := New(h)
	showSingle(c, single("m1", "t1", false))
	c.SetText("kept")
	// A queued message of the same thread in the Outbox.
	c.Show(mailAcc.ID, "t1")
	c.Update(single("o1", "t1", true), mailActions, mailAcc)
	c.SetShown(false)
	c.Send()
	c.OpenWindow()
	c.Forward()
	if s := c.State(); s.Visible || s.Text != "kept" {
		t.Errorf("outbox: %+v", s)
	}
	if len(h.log) != 0 || len(h.methods()) != 0 || len(h.opens) != 0 {
		t.Errorf("acted while hidden: %v %v %v", h.log, h.methods(), h.opens)
	}
}

// A Send on its way when the host hides the bar (the user selected the
// empty page or several messages) ends as usual: the text clears, the
// toast says so.
func TestSendEndsWhileHidden(t *testing.T) {
	h := mailHost()
	gate := make(chan struct{})
	h.gate[api.MethodDraftCreate] = gate
	c := New(h)
	showSingle(c, single("m1", "t1", false))
	c.SetText("go")
	c.Send()
	c.Show("", "")
	c.SetShown(false)
	close(gate)
	h.drain(t)
	if len(h.sent) != 1 || len(h.failed) != 0 {
		t.Fatalf("sent %v failed %v", h.sent, h.failed)
	}
	showSingle(c, single("m1", "t1", false))
	if s := c.State(); s.Text != "" || s.Sending || !s.Visible {
		t.Errorf("after: %+v", s)
	}
}

// A thread id is unique only within its account (an issue's jira:<id> of
// two sites, a search over all accounts): the same id of two accounts has
// its own text, mode and Send, and a Send ends in the account it started
// in.
func TestSameThreadOfTwoAccounts(t *testing.T) {
	other := api.Account{ID: "acc2", Capabilities: api.MailCapabilities}
	h := mailHost()
	gate := make(chan struct{})
	h.gate[api.MethodDraftCreate] = gate
	c, _ := newShown(h)
	c.SetText("for acc")
	c.SetMode(conversation.QuickReplyAll)

	c.Show(other.ID, "t1")
	c.Update(model("t1", other.ID), mailActions, other)
	s := c.State()
	if s.Account != other.ID || s.Thread != "t1" || s.Text != "" || s.Mode != conversation.QuickReply || s.Sending {
		t.Fatalf("acc2 starts with %+v", s)
	}
	c.SetText("for acc2")
	c.Send()
	if !c.State().Sending {
		t.Fatal("acc2 not sending")
	}

	c.Show(mailAcc.ID, "t1")
	c.Update(model("t1", mailAcc.ID), mailActions, mailAcc)
	if s := c.State(); s.Account != mailAcc.ID || s.Text != "for acc" || s.Mode != conversation.QuickReplyAll || s.Sending {
		t.Errorf("acc back: %+v", s)
	}
	close(gate)
	h.drain(t)
	if p, ok := h.params(api.MethodDraftCreate).(api.DraftCreateParams); !ok || p.AccountID != other.ID {
		t.Errorf("draft.create %+v", h.params(api.MethodDraftCreate))
	}
	if s := c.State(); s.Text != "for acc" || s.Sending {
		t.Errorf("acc after acc2's send: %+v", s)
	}
	c.Show(other.ID, "t1")
	c.Update(model("t1", other.ID), mailActions, other)
	if s := c.State(); s.Text != "" || s.Sending {
		t.Errorf("acc2 after its send: %+v", s)
	}
	if _, ok := c.threads[key{other.ID, "t1"}]; ok {
		t.Error("acc2 kept after its send")
	}
}

// A conversation shown without its account takes the account of the
// first Update; one shown with its account keeps it.
func TestUpdateAdoptsTheAccount(t *testing.T) {
	c := New(newFakeHost())
	c.SetShown(true)
	c.Update(model("t1", mailAcc.ID), mailActions, mailAcc)
	if s := c.State(); s.Account != mailAcc.ID || s.Thread != "t1" {
		t.Errorf("adopted %+v", s)
	}
	c.Show("", "t2")
	c.Update(model("t2", mailAcc.ID), mailActions, mailAcc)
	if s := c.State(); s.Account != mailAcc.ID || s.Thread != "t2" {
		t.Errorf("adopted the account: %+v", s)
	}
	c.Show(jiraAcc.ID, "t3")
	c.Update(model("t3", mailAcc.ID), mailActions, mailAcc)
	if s := c.State(); s.Account != jiraAcc.ID {
		t.Errorf("replaced the account: %+v", s)
	}
	c.Show(jiraAcc.ID, "")
	if s := c.State(); s.Account != "" || s.Thread != "" {
		t.Errorf("a thread \"\" is none: %+v", s)
	}
}

// While a conversation's members are on their way the host gives the
// empty model: the bar hides (it must not answer what was shown before,
// such as the single message of the same thread) and the text stays.
func TestEmptyModelHidesAndKeepsText(t *testing.T) {
	c, _ := newShown(newFakeHost())
	c.SetText("draft")
	c.Update(conversation.Model{}, capabilities.Actions{}, api.Account{})
	s := c.State()
	if s.Visible || s.Target.Message.ID != "" || s.Text != "draft" || s.Thread != "t1" || s.Account != mailAcc.ID {
		t.Fatalf("empty model: %+v", s)
	}
	c.Send()
	c.OpenWindow()
	c.Update(model("t1", mailAcc.ID), mailActions, mailAcc)
	if s := c.State(); !s.Visible || s.Text != "draft" || s.Target.Message.ID != "m2" {
		t.Errorf("model back: %+v", s)
	}
}
