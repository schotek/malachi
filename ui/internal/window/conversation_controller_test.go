// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/capabilities"
	"github.com/schotek/malachi/ui/internal/conversation"
)

// The conversation view's controller half (conversation_controller.go),
// ported with the macOS client's ConversationControllerTests over a fake
// window: a conversation row shows the whole conversation through the
// list's folder-scoped thread.get and marks only its newest message read;
// a member row keeps the single message; arrivals, flag changes and
// removals reach the model; bodies are fetched per card, message.get only
// when needed. The fake window answers through a fake RPC caller and runs
// the answers from its own main loop (flush), as glib.IdleAdd would.

// convCall is one call the fake caller took.
type convCall struct {
	method string
	params any
}

// convCaller is a fake RPC caller: each method answers from its handler,
// and every call is recorded.
type convCaller struct {
	calls    []convCall
	handlers map[string]func(params any) (any, error)
}

func (f *convCaller) Call(_ context.Context, method string, params, result any) error {
	f.calls = append(f.calls, convCall{method, params})
	h := f.handlers[method]
	if h == nil {
		return api.NewError(api.CodeMethodNotFound, "%s", method)
	}
	res, err := h(params)
	if err != nil {
		return err
	}
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, result)
}

func (f *convCaller) count(method string) int {
	n := 0
	for _, c := range f.calls {
		if c.method == method {
			n++
		}
	}
	return n
}

// convWindow is the fake window: the grouped list's conversations and
// members as thread_model.go keeps them, the message cache, the accounts,
// and a main loop.
type convWindow struct {
	t        *testing.T
	caller   *convCaller
	folder   folderKey
	threads  map[api.ThreadID]api.ThreadSummary
	members  map[api.ThreadID]*threadMembers
	messages map[api.MessageID]api.MessageSummary // what the daemon holds
	loaded   map[api.MessageID]*loadedMessage
	accounts []api.Account
	queue    []func()
	ctrl     *conversationController
	changes  []convChange
	loads    []api.MessageID
	// switched are the messages whose held variant convFetch showed.
	switched []api.MessageID
}

var convBase = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

func convMsg(id string, hours int, thread, from string, attachments bool, flags ...api.Flag) api.MessageSummary {
	return api.MessageSummary{
		ID: api.MessageID(id), AccountID: "a", FolderID: "in", ThreadID: api.ThreadID(thread),
		From:    []api.Address{{Name: from, Address: from + "@example.invalid"}},
		Subject: "s-" + id, Date: convBase.Add(time.Duration(hours) * time.Hour), Snippet: "p-" + id,
		Flags: append([]api.Flag{}, flags...), HasAttachments: attachments,
	}
}

// convMessages: t1 has three members, the newest unread and an older one
// unread too; t2 one member; t3 two members, all read.
func convMessages() []api.MessageSummary {
	return []api.MessageSummary{
		convMsg("a1", 1, "t1", "bob", false),
		convMsg("a2", 2, "t1", "alice", false, api.FlagSeen),
		convMsg("a3", 3, "t1", "carol", true),
		convMsg("b1", 5, "t2", "dave", false, api.FlagSeen),
		convMsg("c1", 4, "t3", "erin", false, api.FlagSeen),
		convMsg("c2", 6, "t3", "frank", false, api.FlagSeen),
	}
}

func newConvWindow(t *testing.T, list []api.MessageSummary) *convWindow {
	w := &convWindow{
		t:        t,
		folder:   folderKey{Account: "a", Folder: "in"},
		messages: map[api.MessageID]api.MessageSummary{},
		loaded:   map[api.MessageID]*loadedMessage{},
		accounts: []api.Account{{ID: "a", Enabled: true, Config: api.AccountConfig{Name: "Work", Email: "a@example.invalid"}}},
	}
	for _, s := range list {
		w.messages[s.ID] = s
	}
	w.caller = &convCaller{handlers: map[string]func(any) (any, error){
		api.MethodThreadGet: func(p any) (any, error) {
			params := p.(api.ThreadGetParams)
			res := api.ThreadGetResult{Thread: w.threadSummary(params.ThreadID), Messages: w.folderMembers(params.ThreadID)}
			if params.WithSent {
				res.Sent = w.sentMembers(params.ThreadID)
			}
			return res, nil
		},
		// Trimmed with trimQuoted (something was cut), else whole.
		api.MethodMessageBody: func(p any) (any, error) {
			params := p.(api.MessageBodyParams)
			id := params.MessageID
			if params.TrimQuoted {
				return api.MessageBodyResult{MessageID: id, BodyState: api.BodyFetched, Text: "body of " + string(id), QuotedTrimmed: true}, nil
			}
			return api.MessageBodyResult{MessageID: id, BodyState: api.BodyFetched, Text: "body of " + string(id) + "\n> old"}, nil
		},
		api.MethodMessageGet: func(p any) (any, error) {
			id := p.(api.MessageGetParams).MessageID
			return api.MessageGetResult{Message: api.Message{MessageSummary: w.messages[id]}}, nil
		},
	}}
	w.list()
	w.ctrl = newConversationController(w, convIdentity{})
	w.ctrl.onChange = func(ch convChange) { w.changes = append(w.changes, ch) }
	w.ctrl.onLoaded = func(id api.MessageID, _ *loadedMessage) { w.loads = append(w.loads, id) }
	return w
}

// folderMembers are the daemon's members of tid in the folder, oldest
// first.
func (w *convWindow) folderMembers(tid api.ThreadID) []api.MessageSummary {
	return w.membersIn(tid, w.folder.Folder)
}

// sentMembers are the daemon's members of tid in the account's sent
// folder ("sent"), which the folder lacks (the fake has no Message-IDs:
// by id), oldest first.
func (w *convWindow) sentMembers(tid api.ThreadID) []api.MessageSummary {
	return w.membersIn(tid, convSentFolder)
}

// membersIn are the daemon's members of tid in folder, oldest first.
func (w *convWindow) membersIn(tid api.ThreadID, folder api.FolderID) []api.MessageSummary {
	var out []api.MessageSummary
	for _, s := range w.messages {
		if s.ThreadID == tid && s.FolderID == folder {
			out = append(out, s)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && (out[j].Date.Before(out[j-1].Date) || out[j].Date.Equal(out[j-1].Date) && out[j].ID < out[j-1].ID); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// convSentFolder is the account's sent folder.
const convSentFolder api.FolderID = "sent"

// convReply is the user's reply in Sent, in conversation thread.
func convReply(id string, hours int, thread string) api.MessageSummary {
	s := convMsg(id, hours, thread, "a", false, api.FlagSeen)
	s.FolderID = convSentFolder
	return s
}

// refetch is the list's refetchMembers: thread.get for tid again, its
// answer (members, replies, summary) into the list, which the
// conversation hears.
func (w *convWindow) refetch(tid api.ThreadID) {
	params := api.ThreadGetParams{AccountID: w.folder.Account, ThreadID: tid, FolderID: w.folder.Folder, WithSent: true}
	var res api.ThreadGetResult
	if err := w.caller.Call(context.Background(), api.MethodThreadGet, params, &res); err != nil {
		return
	}
	w.queue = append(w.queue, func() {
		w.threads[tid] = res.Thread
		w.members[tid] = &threadMembers{list: res.Messages, sent: res.Sent, complete: true}
		w.listChanged()
	})
}

// threadSummary is the daemon's summary of tid in the folder.
func (w *convWindow) threadSummary(tid api.ThreadID) api.ThreadSummary {
	list := w.folderMembers(tid)
	t := api.ThreadSummary{ID: tid, AccountID: "a", MessageCount: len(list), SentCount: len(w.sentMembers(tid))}
	for _, s := range list {
		if !hasFlag(s.Flags, api.FlagSeen) {
			t.UnreadCount++
		}
	}
	if len(list) > 0 {
		t.Latest = list[len(list)-1]
		t.LatestDate = t.Latest.Date
		t.Subject = t.Latest.Subject
	}
	return t
}

// list is thread.list: every conversation from the daemon's messages. A
// conversation listed before with the same shape keeps its members, as
// mailModel.setThreads does.
func (w *convWindow) list() {
	prev, prevMembers := w.threads, w.members
	w.threads = map[api.ThreadID]api.ThreadSummary{}
	w.members = map[api.ThreadID]*threadMembers{}
	for _, s := range w.messages {
		if _, ok := w.threads[s.ThreadID]; ok || s.FolderID != w.folder.Folder {
			continue
		}
		t := w.threadSummary(s.ThreadID)
		w.threads[t.ID] = t
		if m := prevMembers[t.ID]; m != nil && m.complete && sameShape(prev[t.ID], t) {
			w.members[t.ID] = m
		} else {
			w.members[t.ID] = membersFromListing(t)
		}
	}
	w.listChanged()
}

// listChanged is syncRows: the list changed, and the conversation hears.
func (w *convWindow) listChanged() {
	if w.ctrl != nil {
		w.ctrl.membersChanged()
	}
}

// flush runs the main loop until nothing is queued.
func (w *convWindow) flush() {
	for len(w.queue) > 0 {
		fn := w.queue[0]
		w.queue = w.queue[1:]
		fn()
	}
}

// row is the list row of a conversation (or its single message) by key.
func (w *convWindow) row(tid api.ThreadID, message api.MessageID, member bool) listRow {
	t := w.threads[tid]
	if message == "" {
		return listRow{Key: listKey{Thread: tid}, Thread: true, Summary: t, Message: t.Latest}
	}
	return listRow{Key: listKey{Thread: tid, Message: message}, Member: member, Message: w.messages[message]}
}

// select shows a row and runs the main loop.
func (w *convWindow) selectRow(r listRow) bool {
	shown := w.ctrl.show(r)
	w.flush()
	return shown
}

// convHost, as ensureMembers and fetchMessage do it.

func (w *convWindow) convMembers(tid api.ThreadID) *threadMembers { return w.members[tid] }

func (w *convWindow) convSummary(tid api.ThreadID) (api.ThreadSummary, bool) {
	t, ok := w.threads[tid]
	return t, ok
}

func (w *convWindow) convEnsureMembers(tid api.ThreadID, then func()) {
	mem := w.members[tid]
	if mem == nil {
		return
	}
	if mem.complete {
		then()
		return
	}
	mem.waiters = append(mem.waiters, then)
	if mem.fetching {
		return
	}
	mem.fetching = true
	params := api.ThreadGetParams{AccountID: w.folder.Account, ThreadID: tid, FolderID: w.folder.Folder, WithSent: true}
	var res api.ThreadGetResult
	err := w.caller.Call(context.Background(), api.MethodThreadGet, params, &res)
	w.queue = append(w.queue, func() {
		mem := w.members[tid]
		if mem == nil {
			return
		}
		mem.fetching = false
		waiters := mem.waiters
		mem.waiters = nil
		if err != nil {
			w.listChanged()
			return
		}
		w.threads[tid] = res.Thread
		w.members[tid] = &threadMembers{list: res.Messages, sent: res.Sent, complete: true}
		w.listChanged()
		for _, fn := range waiters {
			fn()
		}
	})
}

func (w *convWindow) convAccount(id api.AccountID) (api.Account, bool) {
	for _, a := range w.accounts {
		if a.ID == id {
			return a, true
		}
	}
	return api.Account{}, false
}

func (w *convWindow) convComposeAccount() bool {
	return len(capabilities.ForwardAccounts(w.accounts)) > 0
}

func (w *convWindow) convFetch(s api.MessageSummary, full, quoted bool, then func(*loadedMessage)) {
	lm := w.loaded[s.ID]
	if lm == nil {
		lm = &loadedMessage{}
		w.loaded[s.ID] = lm
	}
	// switchQuoted: a variant held shows at once wherever the message is
	// (the window's fan-out reaches the conversation through adopt).
	if lm.showQuoted(quoted) && lm.body != nil {
		w.switched = append(w.switched, s.ID)
		w.ctrl.adopt(s.ID, lm)
	}
	if lm.body != nil && (!full || lm.msg != nil) {
		then(lm)
		return
	}
	lm.waiters = append(lm.waiters, then)
	settle := func() {
		waiters := lm.waiters
		if !lm.getting && !lm.fetching {
			lm.waiters = nil
		}
		for _, fn := range waiters {
			fn(lm)
		}
	}
	if full && lm.msg == nil && !lm.getting {
		lm.getting = true
		var res api.MessageGetResult
		err := w.caller.Call(context.Background(), api.MethodMessageGet, api.MessageGetParams{AccountID: s.AccountID, MessageID: s.ID}, &res)
		w.queue = append(w.queue, func() {
			lm.getting = false
			if err == nil {
				lm.msg = &res.Message
			}
			settle()
		})
	}
	if lm.body == nil && !lm.fetching {
		lm.fetching = true
		lm.err = nil
		q := lm.quotedShown
		var res api.MessageBodyResult
		err := w.caller.Call(context.Background(), api.MethodMessageBody, bodyParams(s.AccountID, s.ID, q, lm.switchPolicy), &res)
		w.queue = append(w.queue, func() {
			current := lm.bodyAnswered(q)
			if err != nil {
				if current {
					lm.err = err
				}
			} else {
				lm.store(&res, q, false)
			}
			settle()
		})
	}
}

// shape is the model's stack: member ids, "more" for the row of older
// members.
func convShape(m *conversation.Model) []string {
	if m == nil {
		return nil
	}
	out := []string{}
	for _, it := range m.Items {
		if it.Kind == conversation.ItemTruncated {
			out = append(out, "more")
		} else {
			out = append(out, string(it.Message.ID))
		}
	}
	return out
}

func TestConversationRowShowsTheWholeConversation(t *testing.T) {
	w := newConvWindow(t, convMessages())
	if rowShowsConversation(w.row("t2", "b1", false)) {
		t.Error("a single-message conversation is a plain row")
	}
	if !w.selectRow(w.row("t1", "", false)) {
		t.Fatal("the conversation row is not shown as a conversation")
	}
	if w.ctrl.thread != "t1" {
		t.Errorf("thread %q", w.ctrl.thread)
	}
	if want := []convChange{convLoading, convOpened}; !reflect.DeepEqual(w.changes, want) {
		t.Errorf("changes %v", w.changes)
	}
	if got := w.caller.calls; len(got) != 1 || !reflect.DeepEqual(got[0].params, api.ThreadGetParams{AccountID: "a", ThreadID: "t1", FolderID: "in", WithSent: true}) {
		t.Errorf("thread.get: %+v", got)
	}
	m := w.ctrl.model
	if got := convShape(m); !reflect.DeepEqual(got, []string{"a1", "a2", "a3"}) {
		t.Errorf("shape %v", got)
	}
	if m.MarkRead != "a3" || m.ScrollTo != 2 {
		t.Errorf("mark read %q, scroll to %d", m.MarkRead, m.ScrollTo)
	}
	var senders []string
	for _, it := range m.Items {
		senders = append(senders, it.Sender)
	}
	if !reflect.DeepEqual(senders, []string{"bob", "alice", "carol"}) {
		t.Errorf("senders %v", senders)
	}

	// The same row announced again keeps the model.
	if !w.selectRow(w.row("t1", "", false)) || len(w.changes) != 2 {
		t.Errorf("announced again: %v", w.changes)
	}

	// Another row clears it; known members: no second thread.get for
	// another visit, and the model opens at once.
	if w.selectRow(w.row("t2", "b1", false)) {
		t.Error("a plain row shows a conversation")
	}
	if w.ctrl.thread != "" || w.ctrl.model != nil || w.changes[len(w.changes)-1] != convCleared {
		t.Errorf("not cleared: %q %v", w.ctrl.thread, w.changes)
	}
	w.ctrl.show(w.row("t1", "", false))
	if got := convShape(w.ctrl.model); !reflect.DeepEqual(got, []string{"a1", "a2", "a3"}) {
		t.Errorf("revisit: %v", got)
	}
	if n := w.caller.count(api.MethodThreadGet); n != 1 {
		t.Errorf("thread.get %d times", n)
	}
}

func TestConversationMarksOnlyTheNewestUnreadMemberOnce(t *testing.T) {
	w := newConvWindow(t, convMessages())
	w.ctrl.show(w.row("t1", "", false))
	if got := w.ctrl.takeMarkRead(); got != "" {
		t.Errorf("before the members: %q", got)
	}
	w.flush()
	if got := w.ctrl.takeMarkRead(); got != "a3" {
		t.Errorf("a1 is unread too and stays so: %q", got)
	}
	if got := w.ctrl.takeMarkRead(); got != "" {
		t.Errorf("a second time: %q", got)
	}
	// A conversation whose newest member is read marks nothing.
	w.selectRow(w.row("t3", "", false))
	if got := w.ctrl.takeMarkRead(); got != "" {
		t.Errorf("all read: %q", got)
	}
}

func TestMemberRowKeepsTheSingleMessage(t *testing.T) {
	w := newConvWindow(t, convMessages())
	if w.selectRow(w.row("t1", "a1", true)) {
		t.Error("a member row shows the conversation")
	}
	if w.ctrl.thread != "" || len(w.changes) != 0 || len(w.caller.calls) != 0 {
		t.Errorf("member row: %q %v %v", w.ctrl.thread, w.changes, w.caller.calls)
	}
	// A flat list's row neither.
	if w.selectRow(listRow{Key: listKey{Message: "a3"}, Message: w.messages["a3"]}) {
		t.Error("a flat row shows the conversation")
	}
}

func TestConversationMembersFollowTheList(t *testing.T) {
	w := newConvWindow(t, convMessages())
	w.selectRow(w.row("t1", "", false))
	opened := len(w.changes)

	// A new arrival in the conversation (applyNewMessage): the list takes
	// the member, the model follows.
	late := convMsg("a4", 7, "t1", "gina", false)
	w.messages[late.ID] = late
	mem := w.members["t1"]
	mem.list = append(mem.list, late)
	w.listChanged()
	if got := convShape(w.ctrl.model); !reflect.DeepEqual(got, []string{"a1", "a2", "a3", "a4"}) {
		t.Errorf("arrival: %v", got)
	}
	if len(w.changes) != opened+1 || w.changes[len(w.changes)-1] != convUpdated {
		t.Errorf("changes %v", w.changes)
	}
	if w.ctrl.model.MarkRead != "a4" {
		t.Errorf("mark read %q", w.ctrl.model.MarkRead)
	}

	// A flag change (applyFlags): the member's card follows.
	mem.list[3].Flags, _ = applyFlagChange(mem.list[3].Flags, []api.Flag{api.FlagSeen}, nil)
	w.listChanged()
	if w.ctrl.model.Items[3].Unread || w.ctrl.model.MarkRead != "" {
		t.Errorf("seen: %+v, mark read %q", w.ctrl.model.Items[3], w.ctrl.model.MarkRead)
	}

	// A removal drops the card; its undo brings it back.
	saved := append([]api.MessageSummary(nil), mem.list...)
	mem.list = append(append([]api.MessageSummary(nil), mem.list[:1]...), mem.list[2:]...)
	w.listChanged()
	if got := convShape(w.ctrl.model); !reflect.DeepEqual(got, []string{"a1", "a3", "a4"}) {
		t.Errorf("removal: %v", got)
	}
	mem.list = saved
	w.listChanged()
	if got := convShape(w.ctrl.model); !reflect.DeepEqual(got, []string{"a1", "a2", "a3", "a4"}) {
		t.Errorf("undo: %v", got)
	}

	// A change of another conversation changes nothing here.
	count := len(w.changes)
	w.members["t3"].list = append(w.members["t3"].list, convMsg("c3", 8, "t3", "hana", false))
	w.listChanged()
	if len(w.changes) != count {
		t.Errorf("another conversation: %v", w.changes[count:])
	}
}

// The account of the conversation tells the user's own messages
// (conversation.Item.Mine): mail by the address of its first sender, an
// item of an issue by what the site says.
func TestConversationTellsOwnMessages(t *testing.T) {
	own := convMsg("a2", 2, "t1", "a", false, api.FlagSeen)
	own.From = []api.Address{{Name: "Alena", Address: " A@Example.INVALID "}}
	w := newConvWindow(t, []api.MessageSummary{
		convMsg("a1", 1, "t1", "bob", false, api.FlagSeen), own, convMsg("a3", 3, "t1", "carol", false, api.FlagSeen),
	})
	w.selectRow(w.row("t1", "", false))
	mine := func() []bool {
		var out []bool
		for _, it := range w.ctrl.model.Items {
			out = append(out, it.Mine)
		}
		return out
	}
	if got := mine(); !reflect.DeepEqual(got, []bool{false, true, false}) {
		t.Errorf("mine %v", got)
	}
	if got := w.ctrl.model.Items[1].Sender; got != "Alena" {
		t.Errorf("the name stays the sender's: %q", got)
	}
	// An arrival while the conversation is shown.
	late := convMsg("a4", 7, "t1", "a", false)
	w.members["t1"].list = append(w.members["t1"].list, late)
	w.listChanged()
	if got := mine(); !reflect.DeepEqual(got, []bool{false, true, false, true}) {
		t.Errorf("mine after the arrival %v", got)
	}

	// The items of an issue: what the site says, and a relayed comment is
	// never the user's.
	info := api.IssueInfo{Key: "MOB-3", URL: "https://acme.atlassian.net/browse/MOB-3", Summary: "Login screen flickers", Status: "To Do", StatusCategory: api.StatusCategoryTodo}
	comment := func(id string, hours int, from string, mine bool, via string) api.MessageSummary {
		s := convMsg(id, hours, "issue-MOB-3", from, false, api.FlagSeen)
		s.Issue = &api.MessageIssue{IssueInfo: info, Item: api.IssueItemComment, Via: via, Mine: mine}
		return s
	}
	j := newConvWindow(t, []api.MessageSummary{
		comment("c1", 1, "Jana Dvořáková", false, ""),
		comment("c2", 2, "a", true, ""),
		// The sender's address is the account's, the site says nothing: not the user's.
		comment("c3", 3, "a", false, ""),
		comment("c4", 4, "Petr Svoboda", true, "Issue Sync"),
	})
	j.selectRow(j.row("issue-MOB-3", "", false))
	var got []bool
	for _, it := range j.ctrl.model.Items {
		got = append(got, it.Mine)
	}
	if !reflect.DeepEqual(got, []bool{false, true, false, false}) {
		t.Errorf("issue items mine %v", got)
	}
	if j.ctrl.issue == nil || j.ctrl.issue.Key != "MOB-3" || j.ctrl.model.Issue == nil {
		t.Errorf("issue card: %+v %+v", j.ctrl.issue, j.ctrl.model.Issue)
	}
}

func TestConversationReloadThatChangesItFetchesItAgain(t *testing.T) {
	w := newConvWindow(t, convMessages())
	w.selectRow(w.row("t1", "", false))

	// A reload with the same shape keeps the members: no thread.get.
	w.list()
	w.flush()
	if n := w.caller.count(api.MethodThreadGet); n != 1 {
		t.Errorf("thread.get %d times after a reload of the same shape", n)
	}
	// A member arrived meanwhile (a sync): asked for again and merged.
	w.messages["a5"] = convMsg("a5", 9, "t1", "hana", false)
	w.list()
	w.flush()
	if got := convShape(w.ctrl.model); !reflect.DeepEqual(got, []string{"a1", "a2", "a3", "a5"}) {
		t.Errorf("after the reload: %v", got)
	}
	if n := w.caller.count(api.MethodThreadGet); n != 2 {
		t.Errorf("thread.get %d times", n)
	}
	if w.ctrl.thread != "t1" || w.changes[len(w.changes)-1] != convUpdated {
		t.Errorf("thread %q, changes %v", w.ctrl.thread, w.changes)
	}
}

func TestConversationFailedThreadGetShowsWhatTheListingKnows(t *testing.T) {
	w := newConvWindow(t, convMessages())
	w.caller.handlers[api.MethodThreadGet] = func(any) (any, error) {
		return nil, api.NewError(api.CodeStorageError, "disk")
	}
	w.selectRow(w.row("t1", "", false))
	if got := convShape(w.ctrl.model); !reflect.DeepEqual(got, []string{"more", "a3"}) {
		t.Fatalf("shape %v", got)
	}
	if w.ctrl.model.Earlier != 2 {
		t.Errorf("earlier %d", w.ctrl.model.Earlier)
	}
	if want := []convChange{convLoading, convOpened}; !reflect.DeepEqual(w.changes, want) {
		t.Errorf("changes %v", w.changes)
	}
	if got := w.ctrl.takeMarkRead(); got != "" {
		t.Errorf("nothing is marked without the members: %q", got)
	}
	// The list changing for other reasons does not ask again.
	w.listChanged()
	w.flush()
	if n := w.caller.count(api.MethodThreadGet); n != 1 {
		t.Errorf("thread.get %d times", n)
	}
	// A reload lists the conversation anew and asks again; the members
	// build the model anew and mark.
	w.caller.handlers[api.MethodThreadGet] = func(p any) (any, error) {
		tid := p.(api.ThreadGetParams).ThreadID
		return api.ThreadGetResult{Thread: w.threadSummary(tid), Messages: w.folderMembers(tid)}, nil
	}
	w.messages["a4"] = convMsg("a4", 8, "t1", "gina", false)
	w.list()
	w.flush()
	if got := convShape(w.ctrl.model); !reflect.DeepEqual(got, []string{"a1", "a2", "a3", "a4"}) {
		t.Errorf("after the reload: %v", got)
	}
	if w.changes[len(w.changes)-1] != convOpened {
		t.Errorf("changes %v", w.changes)
	}
	if got := w.ctrl.takeMarkRead(); got != "a4" {
		t.Errorf("mark read after the members arrived: %q", got)
	}
}

func TestIssueConversationMarksItsNewestCommentNeverAnEvent(t *testing.T) {
	info := api.IssueInfo{Key: "WEB-7", URL: "https://acme.atlassian.net/browse/WEB-7", Summary: "Checkout fails", Status: "In Progress", StatusCategory: api.StatusCategoryInProgress}
	item := func(id string, hours int, kind api.IssueItemKind, changes []api.IssueChange, flags ...api.Flag) api.MessageSummary {
		s := convMsg(id, hours, "issue-WEB-7", "Jana Dvořáková", false, flags...)
		s.Issue = &api.MessageIssue{IssueInfo: info, Item: kind, Changes: changes}
		return s
	}
	w := newConvWindow(t, []api.MessageSummary{
		item("d", 1, api.IssueItemDescription, nil, api.FlagSeen),
		item("c1", 2, api.IssueItemComment, nil),
		item("c2", 3, api.IssueItemComment, nil),
		item("e1", 4, api.IssueItemEvent, []api.IssueChange{{Field: api.IssueFieldStatus, From: "To Do", To: "In Progress"}}, api.FlagSeen),
	})
	w.selectRow(w.row("issue-WEB-7", "", false))
	m := w.ctrl.model
	if got := convShape(m); !reflect.DeepEqual(got, []string{"d", "c1", "c2", "e1"}) {
		t.Fatalf("shape %v", got)
	}
	if m.Items[3].Kind != conversation.ItemEvent || !reflect.DeepEqual(m.Items[3].EventLines, []string{"Status: To Do → In Progress"}) {
		t.Errorf("event %+v", m.Items[3])
	}
	if m.Issue == nil || m.Issue.Key != "WEB-7" || m.Issue.Status != "In Progress" {
		t.Errorf("issue card %+v", m.Issue)
	}
	if got := w.ctrl.takeMarkRead(); got != "c2" {
		t.Errorf("c1 stays unread; the event is never marked: %q", got)
	}
	// An event has no body to fetch.
	w.ctrl.needsBody("e1", false)
	w.flush()
	if n := w.caller.count(api.MethodMessageBody); n != 0 || w.ctrl.loaded["e1"] != nil {
		t.Errorf("event body fetched: %d", n)
	}
	// Its card offers nothing; a comment offers what the account allows.
	if a := w.ctrl.actions(m.Items[3].Message); a != (capabilities.Actions{}) {
		t.Errorf("event actions %+v", a)
	}
	w.accounts = []api.Account{{ID: "a", Enabled: true, Config: api.AccountConfig{Kind: api.AccountJira},
		Capabilities: []api.AccountCapability{api.CapabilityComment, api.CapabilityForward}}}
	if a := w.ctrl.actions(m.Items[1].Message); !a.Reply || !a.Comment || a.ReplyAll || a.Forward {
		t.Errorf("comment actions without a mail account %+v", a)
	}
}

func TestConversationBodiesAreFetchedPerCard(t *testing.T) {
	w := newConvWindow(t, convMessages())
	w.selectRow(w.row("t1", "", false))
	if n := w.caller.count(api.MethodMessageBody); n != 0 {
		t.Errorf("bodies before the pane asks: %d", n)
	}

	// Without attachments: message.body alone.
	w.ctrl.needsBody("a1", false)
	w.ctrl.needsBody("a1", false)
	w.flush()
	if w.caller.count(api.MethodMessageBody) != 1 || w.caller.count(api.MethodMessageGet) != 0 {
		t.Errorf("a1: body %d, get %d", w.caller.count(api.MethodMessageBody), w.caller.count(api.MethodMessageGet))
	}
	if lm := w.ctrl.loaded["a1"]; lm == nil || lm.body == nil || lm.body.Text != "body of a1" {
		t.Errorf("a1 not held: %+v", lm)
	}
	if !reflect.DeepEqual(w.loads, []api.MessageID{"a1"}) {
		t.Errorf("loads %v", w.loads)
	}

	// Held: no second request.
	w.ctrl.needsBody("a1", false)
	w.flush()
	if n := w.caller.count(api.MethodMessageBody); n != 1 {
		t.Errorf("held body asked again: %d", n)
	}

	// With attachments (the chips): message.get too.
	w.ctrl.needsBody("a3", false)
	w.flush()
	if lm := w.ctrl.loaded["a3"]; lm == nil || !lm.complete() || w.caller.count(api.MethodMessageGet) != 1 {
		t.Errorf("a3: %+v, get %d", lm, w.caller.count(api.MethodMessageGet))
	}

	// The recipients' disclosure (Cc): message.get for a card without
	// attachments too; the body comes from the cache.
	w.ctrl.needsBody("a1", true)
	w.flush()
	if lm := w.ctrl.loaded["a1"]; lm == nil || lm.msg == nil {
		t.Errorf("a1 details: %+v", lm)
	}
	if w.caller.count(api.MethodMessageGet) != 2 || w.caller.count(api.MethodMessageBody) != 2 {
		t.Errorf("get %d, body %d", w.caller.count(api.MethodMessageGet), w.caller.count(api.MethodMessageBody))
	}

	// An id that is not shown, and an entry adopted from the window's
	// fan-out.
	w.ctrl.needsBody("b1", false)
	w.flush()
	if w.ctrl.loaded["b1"] != nil {
		t.Error("b1 is not shown")
	}
	lm := &loadedMessage{}
	w.ctrl.adopt("b1", lm)
	if w.ctrl.loaded["b1"] != nil {
		t.Error("b1 adopted")
	}
	w.ctrl.adopt("a2", lm)
	if w.ctrl.loaded["a2"] != lm {
		t.Error("a2 not adopted")
	}

	// Over the budget, far entries go, the largest first; near ones stay.
	w.ctrl.trim(map[api.MessageID]bool{"a1": true}, 0)
	if len(w.ctrl.loaded) != 1 || w.ctrl.loaded["a1"] == nil {
		t.Errorf("after trim: %v", w.ctrl.loaded)
	}

	// Another conversation forgets the entries.
	w.selectRow(w.row("t3", "", false))
	if len(w.ctrl.loaded) != 0 {
		t.Errorf("entries of the conversation left: %v", w.ctrl.loaded)
	}
}

// A failed message.get is not asked again on every scroll; the summary
// serves the card.
func TestConversationFailedGetIsNotAskedAgain(t *testing.T) {
	w := newConvWindow(t, convMessages())
	w.caller.handlers[api.MethodMessageGet] = func(any) (any, error) { return nil, errors.New("gone") }
	w.selectRow(w.row("t1", "", false))
	for range 3 {
		w.ctrl.needsBody("a3", false)
		w.flush()
		delete(w.loaded, "a3") // the cache let go of it meanwhile
	}
	if n := w.caller.count(api.MethodMessageGet); n != 1 {
		t.Errorf("message.get %d times", n)
	}
	if lm := w.ctrl.loaded["a3"]; lm == nil || lm.body == nil {
		t.Errorf("the body is held all the same: %+v", lm)
	}
}

// The daemon rebuilt the conversation's messages: the held entries go, the
// pane is told, and a body asked for before arrives into nothing.
func TestConversationRefreshLetsGoOfTheBodies(t *testing.T) {
	w := newConvWindow(t, convMessages())
	w.selectRow(w.row("t1", "", false))
	w.ctrl.needsBody("a1", false)
	w.flush()
	w.ctrl.needsBody("a2", false) // on its way
	count := len(w.changes)
	w.ctrl.refresh("t2")
	if len(w.changes) != count {
		t.Error("another conversation's refresh changed this one")
	}
	w.ctrl.refresh("t1")
	if len(w.ctrl.loaded) != 0 || w.changes[len(w.changes)-1] != convUpdated {
		t.Errorf("after refresh: %v %v", w.ctrl.loaded, w.changes)
	}
	w.flush()
	if w.ctrl.loaded["a2"] != nil {
		t.Error("a body asked for before the refresh landed")
	}
}

// sentShape is convShape with the sent cards marked "sent:".
func sentShape(m *conversation.Model) []string {
	if m == nil {
		return nil
	}
	out := []string{}
	for _, it := range m.Items {
		switch {
		case it.Kind == conversation.ItemTruncated:
			out = append(out, "more")
		case it.Sent:
			out = append(out, "sent:"+string(it.Message.ID))
		default:
			out = append(out, string(it.Message.ID))
		}
	}
	return out
}

// bodyTrims is whether each message.body call asked for trimQuoted.
func (f *convCaller) bodyTrims() []bool {
	out := []bool{}
	for _, c := range f.calls {
		if c.method == api.MethodMessageBody {
			out = append(out, c.params.(api.MessageBodyParams).TrimQuoted)
		}
	}
	return out
}

// The user's replies in Sent stand among the members by date as sent
// cards: never marked read, never among the members; one message and the
// user's reply to it are a conversation row. A port of the macOS client's
// repliesInSentAreSentCards.
func TestConversationRepliesInSentAreSentCards(t *testing.T) {
	w := newConvWindow(t, append(convMessages(), convReply("r1", 2, "t1"), convReply("r2", 6, "t2")))
	row := w.row("t2", "", false)
	if !rowShowsConversation(row) || row.Summary.SentCount != 1 {
		t.Errorf("a message and the user's reply are no conversation row: %+v", row.Summary)
	}

	w.selectRow(w.row("t1", "", false))
	if got := sentShape(w.ctrl.model); !reflect.DeepEqual(got, []string{"a1", "a2", "sent:r1", "a3"}) {
		t.Errorf("t1 shape %v", got)
	}
	if w.ctrl.model.MarkRead != "a3" {
		t.Errorf("mark read %q", w.ctrl.model.MarkRead)
	}
	var ids []api.MessageID
	for _, s := range w.members["t1"].list {
		ids = append(ids, s.ID)
	}
	if !reflect.DeepEqual(ids, []api.MessageID{"a1", "a2", "a3"}) {
		t.Errorf("the reply is a member: %v", ids)
	}
	if s, ok := w.ctrl.member("r1"); !ok || s.FolderID != convSentFolder {
		t.Errorf("its card has no body to ask for: %+v %v", s, ok)
	}

	w.selectRow(w.row("t2", "", false))
	if got := sentShape(w.ctrl.model); !reflect.DeepEqual(got, []string{"b1", "sent:r2"}) {
		t.Errorf("t2 shape %v", got)
	}
	for _, c := range w.caller.calls {
		if p, ok := c.params.(api.ThreadGetParams); ok && !p.WithSent {
			t.Errorf("thread.get without withSent: %+v", p)
		}
	}
}

// A reply that lands in Sent has the list ask for the conversation again
// (refetchMembers): a single message the user answered becomes a
// conversation row, and a conversation on show gets the reply; gone from
// Sent, the next answer drops the card. A port of the macOS client's
// replyArrivingInSentJoinsTheConversation.
func TestConversationReplyArrivingInSentJoinsIt(t *testing.T) {
	w := newConvWindow(t, convMessages())
	if w.selectRow(w.row("t2", "b1", false)) {
		t.Fatal("a single message shows a conversation")
	}
	w.messages["r9"] = convReply("r9", 7, "t2")
	w.refetch("t2")
	w.flush()
	row := w.row("t2", "", false)
	if !rowShowsConversation(row) {
		t.Fatalf("the answered message is no conversation row: %+v", row.Summary)
	}
	w.selectRow(row)
	if got := sentShape(w.ctrl.model); !reflect.DeepEqual(got, []string{"b1", "sent:r9"}) {
		t.Errorf("t2 shape %v", got)
	}

	// Into a conversation on show.
	w.selectRow(w.row("t1", "", false))
	w.messages["r10"] = convReply("r10", 8, "t1")
	w.refetch("t1")
	w.flush()
	if got := sentShape(w.ctrl.model); !reflect.DeepEqual(got, []string{"a1", "a2", "a3", "sent:r10"}) {
		t.Errorf("t1 shape %v", got)
	}
	if w.changes[len(w.changes)-1] != convUpdated {
		t.Errorf("changes %v", w.changes)
	}

	// Gone from Sent: the next answer drops the card.
	delete(w.messages, "r10")
	w.refetch("t1")
	w.flush()
	if got := sentShape(w.ctrl.model); !reflect.DeepEqual(got, []string{"a1", "a2", "a3"}) {
		t.Errorf("after the reply went %v", got)
	}
}

// Show Quoted Text on a card: the whole body is asked for and the choice
// holds through the pane's asking again (every scroll) until another
// conversation is shown; Hide shows the trimmed body held. A port of the
// macOS client's quotedTextHoldsForTheConversation.
func TestConversationQuotedTextHoldsForTheConversation(t *testing.T) {
	w := newConvWindow(t, convMessages())
	w.selectRow(w.row("t1", "", false))
	w.ctrl.needsBody("a1", false)
	w.flush()
	if got := w.caller.bodyTrims(); !reflect.DeepEqual(got, []bool{true}) {
		t.Errorf("trimmed by default: %v", got)
	}
	if lm := w.ctrl.loaded["a1"]; lm == nil || quotedOffer(lm) != conversation.QuotedShow {
		t.Errorf("no Show Quoted Text: %+v", lm)
	}
	if w.ctrl.quotedRevealed("a1") {
		t.Error("revealed before the user asked")
	}

	w.ctrl.setQuoted("a1", true, false)
	if lm := w.ctrl.loaded["a1"]; lm == nil || quotedOffer(lm) != conversation.QuotedHide {
		t.Errorf("the whole body on its way offers no way back: %+v", lm)
	}
	w.flush()
	if lm := w.ctrl.loaded["a1"]; lm == nil || !lm.quotedShown || lm.body == nil || lm.body.Text != "body of a1\n> old" {
		t.Errorf("the whole body: %+v", lm)
	}
	if !w.ctrl.quotedRevealed("a1") {
		t.Error("not revealed")
	}
	w.ctrl.needsBody("a1", false)
	w.flush()
	if got := w.caller.bodyTrims(); !reflect.DeepEqual(got, []bool{true, false}) {
		t.Errorf("held: not asked again: %v", got)
	}

	w.ctrl.setQuoted("a1", false, false)
	if lm := w.ctrl.loaded["a1"]; lm == nil || lm.quotedShown || lm.body == nil || lm.body.Text != "body of a1" {
		t.Errorf("the trimmed body at once: %+v", lm)
	}
	w.ctrl.setQuoted("a1", true, false)
	w.flush()
	if n := w.caller.count(api.MethodMessageBody); n != 2 {
		t.Errorf("both variants held: %d requests", n)
	}
	w.ctrl.setQuoted("zz", true, false)
	if w.ctrl.quotedRevealed("zz") {
		t.Error("not a member")
	}

	w.selectRow(w.row("t3", "", false))
	w.selectRow(w.row("t1", "", false))
	if w.ctrl.quotedRevealed("a1") {
		t.Error("another conversation forgot it")
	}
	w.ctrl.needsBody("a1", false)
	w.flush()
	if lm := w.ctrl.loaded["a1"]; lm == nil || lm.quotedShown {
		t.Errorf("trimmed again: %+v", lm)
	}
	if n := w.caller.count(api.MethodMessageBody); n != 2 {
		t.Errorf("the trimmed body was held by the cache: %d requests", n)
	}
}
