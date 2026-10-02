// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// fxCaseWithDraft is a case the fake board answers as linking the user's
// draft d_user already.
const fxCaseWithDraft = "c_hasdraft"

// --reply-only: a valid opaque id, never with another tier, no
// environment default; a bad value stops the bridge at startup.
func TestReplyOnlyServerFlags(t *testing.T) {
	parse := func(args ...string) (config, error) {
		cfg, _, err := parseServerFlags(args, &syncWriter{})
		return cfg, err
	}
	if cfg, err := parse(); err != nil || cfg.replyOnly != "" {
		t.Fatalf("without the flag: %+v %v", cfg, err)
	}
	for _, id := range []string{"m1", "m_0123456789abcdef0123456789abcdef", "AQMk:AD.a-b_c", strings.Repeat("a", maxReplyOnlyIDBytes)} {
		if cfg, err := parse("--reply-only", id); err != nil || cfg.replyOnly != api.MessageID(id) {
			t.Errorf("%q: %+v %v", id, cfg, err)
		}
	}
	for _, id := range []string{"", " ", "m 1", "m\n1", "m\x001", "m‮1", "m​1", "ü", "m1;rm", "../m1", "m/1",
		strings.Repeat("a", maxReplyOnlyIDBytes+1)} {
		_, err := parse("--reply-only", id)
		if err == nil || !strings.Contains(err.Error(), "--reply-only must be a message id") {
			t.Errorf("%q: %v", id, err)
			continue
		}
		if strings.TrimSpace(id) != "" && strings.Contains(err.Error(), id) {
			t.Errorf("%q echoed in %q", id, err)
		}
	}
	for _, other := range []string{"--allow-modify", "--allow-send", "--allow-triage"} {
		for _, args := range [][]string{{other, "--reply-only", "m1"}, {"--reply-only", "m1", other}} {
			if _, err := parse(args...); err == nil || err.Error() != "--reply-only cannot be combined with "+other {
				t.Errorf("%v: %v", args, err)
			}
		}
	}
	// No environment variable stands for it.
	t.Setenv("MALACHI_MCP_REPLY_ONLY", "m1")
	if cfg, err := parse(); err != nil || cfg.replyOnly != "" {
		t.Fatalf("from the environment: %+v %v", cfg, err)
	}
	if err := run([]string{"--reply-only", "bad id"}, &syncWriter{}, &syncWriter{}); err == nil || !strings.Contains(err.Error(), "--reply-only") {
		t.Fatalf("run with a bad id: %v", err)
	}
}

func newReplyOnlyHarness(t *testing.T, fb *fakeBackend, cfg config) *harness {
	t.Helper()
	cfg.socket = tempSocket(t)
	startFakeDaemon(t, fb, cfg.socket)
	cs, logs, b := connectBridgeConfig(t, cfg)
	return &harness{fb: fb, sock: cfg.socket, cs: cs, logs: logs, b: b}
}

// Under --reply-only create_draft makes one reply to the named message:
// everything else, and a second draft, is refused with a fixed text before
// the daemon is asked; a reply the daemon refuses gives the draft back.
func TestReplyOnlyCreateDraft(t *testing.T) {
	fb := withJiraAccount(newFixture())
	// A config built around the startup check still gets no other tier.
	h := newReplyOnlyHarness(t, fb, config{replyOnly: "m1", allowModify: true, allowSend: true, allowTriage: true})
	tools := listTools(t, h.cs)
	for _, name := range []string{"send_message", "mark_messages", "move_messages", "delete_messages", "list_triage_queue", "annotate_case", "add_commitment", "unsubscribe"} {
		if _, ok := tools[name]; ok {
			t.Errorf("tool %s offered under --reply-only", name)
		}
	}
	for _, name := range []string{"create_draft", "read_message", "list_messages"} {
		if _, ok := tools[name]; !ok {
			t.Errorf("tool %s missing", name)
		}
	}
	mustContain(t, h.cs.InitializeResult().Instructions, serverInstructions, "--reply-only", "to message m1")

	refused := []map[string]any{
		{"accountId": "a1", "to": []string{"x@evil.example"}, "subject": "Hi", "body": "Hello"},         // a new message
		{"accountId": "a1", "body": "Hello"},                                                            // a new message without recipients
		{"accountId": "a1", "mode": "forward", "messageId": "m1", "to": []string{"x@evil.example"}},     // a forward
		{"accountId": "a1", "mode": "forward", "messageId": "m1"},                                       // a forward without recipients
		{"accountId": "a1", "mode": "reply", "messageId": "m2", "body": "Thanks"},                       // another message
		{"accountId": "a1", "mode": "reply", "messageId": "M1", "body": "Thanks"},                       // not the same id
		{"accountId": "a1", "mode": "reply", "messageId": "m1 ", "body": "Thanks"},                      // not the same id
		{"accountId": "a1", "mode": "reply", "body": "Thanks"},                                          // no message
		{"accountId": "a1", "mode": "reply", "messageId": "m1", "to": []string{"x@evil.example"}},       // other recipients
		{"accountId": "a1", "mode": "replyAll", "messageId": "m1", "cc": []string{"x@evil.example"}},    // added recipients
		{"accountId": "a1", "mode": "replyAll", "messageId": "m1", "bcc": []string{"x@evil.example"}},   // a hidden recipient
		{"accountId": "a1", "mode": "reply", "messageId": "m1", "subject": "Invoice"},                   // another subject
		{"accountId": "a1", "mode": "reply", "messageId": "m1", "messageAccountId": "j1"},               // another account's parts
		{"accountId": "a1", "mode": "reply", "messageId": "m1", "visibility": "internal", "body": "Hi"}, // not the default visibility
		{"accountId": "a1", "mode": "evil", "messageId": "m1", "body": "Thanks"},                        // an unknown mode
	}
	for _, args := range refused {
		out := h.fail(t, "create_draft", args, replyOnlyRefusal)
		mustNotContain(t, out, "evil", "Invoice", "Thanks", "M1", "j1")
	}
	fb.mu.Lock()
	creates, saves := len(fb.draftCreates), len(fb.draftSaves)
	fb.mu.Unlock()
	if creates+saves != 0 {
		t.Fatalf("the daemon was asked: %d draft.create, %d draft.save", creates, saves)
	}

	// The daemon refuses the first try: the draft is not used up.
	fb.setFail(api.MethodDraftCreate, api.NewError(api.CodeMessageNotFound, "gone"))
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "mode": "reply", "messageId": "m1", "body": "Thanks"}, "messageNotFound")
	fb.setFail(api.MethodDraftCreate, nil)

	out := h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "reply", "messageId": "m1", "body": "Thanks, will do."})
	mustContain(t, out, "draft d1 (version 1) stored in account a1; it is NOT sent. ", "on the board only")
	for _, args := range []map[string]any{
		{"accountId": "a1", "mode": "reply", "messageId": "m1", "body": "Another"},
		{"accountId": "a1", "mode": "replyAll", "messageId": "m1", "body": "Another"},
	} {
		mustNotContain(t, h.fail(t, "create_draft", args, replyOnlyDoneRefusal), "Another")
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if len(fb.draftCreates) != 2 || len(fb.draftSaves) != 1 || fb.draftCreates[1].MessageID != "m1" || fb.draftCreates[1].Mode != api.ComposeReply {
		t.Fatalf("draft.create %+v, draft.save %d", fb.draftCreates, len(fb.draftSaves))
	}
	// The suggested reply is local: never uploaded to the Drafts folder.
	if !fb.draftSaves[0].Draft.Local {
		t.Fatalf("draft.save of the suggested reply: %+v", fb.draftSaves[0])
	}
}

// Without --reply-only create_draft is whole, and a replyOnce is per
// process: a second bridge may make its own reply.
func TestReplyOnlyOffLeavesCreateDraft(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	mustContain(t, h.ok(t, "create_draft", map[string]any{"accountId": "a1", "to": []string{"alice@example.org"}, "body": "Hi"}), "draft d1 ")
	h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "reply", "messageId": "m2", "body": "Hi"})
	h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "forward", "messageId": "m1", "to": []string{"bob@example.org"}})
	// A plain bridge never makes a local draft.
	h.fb.mu.Lock()
	for _, p := range h.fb.draftSaves {
		if p.Draft.Local {
			h.fb.mu.Unlock()
			t.Fatalf("a plain bridge made a local draft: %+v", p)
		}
	}
	h.fb.mu.Unlock()
	if strings.Contains(h.cs.InitializeResult().Instructions, "--reply-only") {
		t.Fatal("reply-only instructions without the flag")
	}
}

// annotate_case tells the model when the case kept another suggested
// reply: the annotation is stored, its draft not linked.
func TestAnnotateCaseKeepsExistingDraft(t *testing.T) {
	fb := newFixture()
	fb.queueResult = &api.BoardQueueResult{Items: []api.BoardQueueItem{queueItem(fxCaseWithDraft, fxAccount)}}
	fb.queueResult.Items[0].HasDraft = true
	h := newTriageHarness(t, fb, "")
	mustContain(t, h.ok(t, "list_triage_queue", nil), `"hasDraft": true`)
	mustContain(t, h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "reply", "messageId": "m1", "body": "Thanks"}), "draft d1 ")
	out := h.ok(t, "annotate_case", map[string]any{"caseId": fxCaseWithDraft, "inputKey": fxInputKey, "draftId": "d1"})
	mustContain(t, out, "annotated case "+fxCaseWithDraft, "your reply draft d1 was NOT linked", "already has a suggested reply")
	mustNotContain(t, out, "reply draft d1 linked")
	out = h.ok(t, "annotate_case", map[string]any{"caseId": fxCaseWithDraft, "inputKey": fxInputKey})
	mustNotContain(t, out, "NOT linked")
}
