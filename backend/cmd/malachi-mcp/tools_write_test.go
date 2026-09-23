// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

var readTools = []string{"create_draft", "get_attachment", "list_accounts", "list_folders", "list_messages", "read_message", "sync_status", "trigger_sync"}

func TestToolGatingByFlags(t *testing.T) {
	cases := []struct {
		modify, send bool
		want         []string
	}{
		{false, false, readTools},
		{true, false, append(append([]string{}, readTools...), "delete_messages", "mark_messages", "move_messages")},
		{false, true, append(append([]string{}, readTools...), "send_message")},
		{true, true, append(append([]string{}, readTools...), "delete_messages", "mark_messages", "move_messages", "send_message")},
	}
	for _, c := range cases {
		sock := tempSocket(t)
		cs, _, _ := connectBridge(t, sock, c.modify, c.send)
		got := toolNames(t, cs)
		want := append([]string{}, c.want...)
		sortStrings(want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("modify=%v send=%v: tools %v, want %v", c.modify, c.send, got, want)
		}
	}
	// A gated tool is unknown to the server, not merely refused.
	cs, _, _ := connectBridge(t, tempSocket(t), false, false)
	params := mcpCallParams("mark_messages")
	if _, err := cs.CallTool(t.Context(), &params); err == nil {
		t.Error("calling an unregistered tool must fail at the protocol level")
	}
}

func TestAnnotations(t *testing.T) {
	cs, _, _ := connectBridge(t, tempSocket(t), true, true)
	tools := listTools(t, cs)
	type ann struct{ readOnly, destructive, idempotent, openWorld bool }
	want := map[string]ann{
		"list_accounts": {true, false, true, false}, "list_folders": {true, false, true, false},
		"list_messages": {true, false, true, false}, "read_message": {true, false, true, false},
		"get_attachment": {true, false, true, false}, "sync_status": {true, false, true, false},
		"trigger_sync":  {false, false, true, false},
		"create_draft":  {false, false, false, false},
		"mark_messages": {false, false, true, false}, "move_messages": {false, false, true, false},
		"delete_messages": {false, true, true, false},
		"send_message":    {false, true, true, true},
	}
	if len(tools) != len(want) {
		t.Fatalf("%d tools, want %d", len(tools), len(want))
	}
	for name, w := range want {
		tool, ok := tools[name]
		if !ok || tool.Annotations == nil {
			t.Errorf("%s: missing tool or annotations", name)
			continue
		}
		a := tool.Annotations
		if a.DestructiveHint == nil || a.OpenWorldHint == nil {
			t.Errorf("%s: DestructiveHint/OpenWorldHint must be explicit", name)
			continue
		}
		got := ann{a.ReadOnlyHint, *a.DestructiveHint, a.IdempotentHint, *a.OpenWorldHint}
		if got != w {
			t.Errorf("%s: annotations %+v, want %+v", name, got, w)
		}
	}
}

func TestCreateDraftReplyPrefill(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out := h.ok(t, "create_draft", map[string]any{"accountId": "a1", "replyTo": "m1", "body": "Thanks!"})
	mustContain(t, out, "draft d1 (version 1) stored in account a1; it is NOT sent.", "without --allow-send",
		"to: Alice Reply <reply@example.org>", "subject: Re: Quarterly numbers", "in-reply-to: m1")
	fenceNonce(t, out)

	h.ok(t, "create_draft", map[string]any{"accountId": "a1", "replyTo": "m1", "to": []string{"Carol <carol@example.net>"}, "subject": "re: own subject"})
	h.ok(t, "create_draft", map[string]any{"accountId": "a1", "replyTo": "m2"})

	h.fb.mu.Lock()
	saves := append([]api.DraftSaveParams(nil), h.fb.draftSaves...)
	h.fb.mu.Unlock()
	if len(saves) != 3 {
		t.Fatalf("%d draft saves, want 3", len(saves))
	}
	d := saves[0].Draft
	if d.AccountID != "a1" || d.TextBody != "Thanks!" || d.Subject != "Re: Quarterly numbers" || d.InReplyTo != "m1" ||
		!reflect.DeepEqual(d.To, []api.Address{{Name: "Alice Reply", Address: "reply@example.org"}}) {
		t.Errorf("reply draft: %+v", d)
	}
	if d.HTMLBody != "" || d.Forwarding != "" || len(d.Attachments) != 0 || d.ID != "" || d.Version != 0 {
		t.Errorf("draft must be plain text without forwarding/attachments: %+v", d)
	}
	d = saves[1].Draft
	if d.Subject != "re: own subject" || !reflect.DeepEqual(d.To, []api.Address{{Name: "Carol", Address: "carol@example.net"}}) {
		t.Errorf("explicit to/subject not honoured: %+v", d)
	}
	d = saves[2].Draft
	if !reflect.DeepEqual(d.To, []api.Address{{Name: "Eve", Address: "eve@example.net"}}) || d.Subject != "Re: Pending body" {
		t.Errorf("reply without Reply-To must use From: %+v", d)
	}
}

func TestCreateDraftRejectsBadAddress(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "to": []string{"not an address"}}, `to: invalid address "not an address"`)
	// A missing required field is refused by the SDK's schema validation
	// before the handler runs; the text is the SDK's, but it is a tool error.
	h.fail(t, "create_draft", map[string]any{"to": []string{"x@example.org"}}, `"accountId"`)
	h.fb.mu.Lock()
	n := len(h.fb.draftSaves)
	h.fb.mu.Unlock()
	if n != 0 {
		t.Errorf("%d drafts saved, want 0", n)
	}
}

func TestCreateDraftSessionCap(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	for i := 0; i < maxSessionDrafts; i++ {
		h.ok(t, "create_draft", map[string]any{"accountId": "a1", "to": []string{"x@example.org"}, "subject": fmt.Sprint(i)})
	}
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "to": []string{"x@example.org"}}, "which is its limit")
	h.fb.mu.Lock()
	n := len(h.fb.draftSaves)
	h.fb.mu.Unlock()
	if n != maxSessionDrafts {
		t.Errorf("%d drafts saved, want %d", n, maxSessionDrafts)
	}
}

func TestMarkFlagsAllowList(t *testing.T) {
	h := newHarness(t, newFixture(), true, false)
	ids := []string{"m1"}
	h.fail(t, "mark_messages", map[string]any{"accountId": "a1", "messageIds": ids, "set": []string{"deleted"}}, "use delete_messages")
	h.fail(t, "mark_messages", map[string]any{"accountId": "a1", "messageIds": ids, "set": []string{"draft"}}, "unknown flag")
	h.fail(t, "mark_messages", map[string]any{"accountId": "a1", "messageIds": ids, "set": []string{"seen"}, "clear": []string{"seen"}}, "both set and clear")
	h.fail(t, "mark_messages", map[string]any{"accountId": "a1", "messageIds": ids}, "nothing to change")
	h.fail(t, "mark_messages", map[string]any{"accountId": "a1", "messageIds": []string{}, "set": []string{"seen"}}, "at least one message")
	out := h.ok(t, "mark_messages", map[string]any{"accountId": "a1", "messageIds": ids, "set": []string{"SEEN"}, "clear": []string{"flagged"}})
	mustContain(t, out, "updated 1 messages: set=[seen] clear=[flagged]")
	h.fb.mu.Lock()
	calls := append([]api.MessageFlagParams(nil), h.fb.flagCalls...)
	h.fb.mu.Unlock()
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Set, []api.Flag{api.FlagSeen}) || !reflect.DeepEqual(calls[0].Clear, []api.Flag{api.FlagFlagged}) {
		t.Errorf("flag calls: %+v", calls)
	}
}

func TestMutationIDCap(t *testing.T) {
	h := newHarness(t, newFixture(), true, false)
	many := make([]string, maxMutateIDs+1)
	for i := range many {
		many[i] = fmt.Sprintf("m%d", i)
	}
	h.fail(t, "mark_messages", map[string]any{"accountId": "a1", "messageIds": many, "set": []string{"seen"}}, fmt.Sprintf("at most %d per call", maxMutateIDs))
	h.fail(t, "move_messages", map[string]any{"accountId": "a1", "messageIds": many, "targetFolderId": "f_trash"}, "at most")
	h.fail(t, "delete_messages", map[string]any{"accountId": "a1", "messageIds": many}, "at most")
	h.ok(t, "mark_messages", map[string]any{"accountId": "a1", "messageIds": many[:maxMutateIDs], "set": []string{"seen"}})
	h.fb.mu.Lock()
	flags, moves, deletes := len(h.fb.flagCalls), len(h.fb.moveCalls), len(h.fb.deleteCalls)
	h.fb.mu.Unlock()
	if flags != 1 || moves != 0 || deletes != 0 {
		t.Errorf("calls flag=%d move=%d delete=%d; want 1,0,0", flags, moves, deletes)
	}
}

func TestMoveTargetRules(t *testing.T) {
	h := newHarness(t, newFixture(), true, false)
	ids := []string{"m1"}
	h.fail(t, "move_messages", map[string]any{"accountId": "a1", "messageIds": ids, "targetFolderId": "f_outbox"}, "outbox")
	h.fail(t, "move_messages", map[string]any{"accountId": "a1", "messageIds": ids, "targetFolderId": "f_noselect"}, "cannot hold messages")
	h.fail(t, "move_messages", map[string]any{"accountId": "a1", "messageIds": ids, "targetFolderId": "f_unsynced"}, "not synchronised")
	h.fail(t, "move_messages", map[string]any{"accountId": "a1", "messageIds": ids, "targetFolderId": "f_nope"}, "folderNotFound")
	out := h.ok(t, "move_messages", map[string]any{"accountId": "a1", "messageIds": ids, "targetFolderId": "f_all"})
	mustContain(t, out, "moved 1 messages to folder f_all")
	h.fb.mu.Lock()
	calls := append([]api.MessageMoveParams(nil), h.fb.moveCalls...)
	h.fb.mu.Unlock()
	if len(calls) != 1 || calls[0].TargetFolderID != fxArchive || !reflect.DeepEqual(calls[0].MessageIDs, []api.MessageID{"m1"}) {
		t.Errorf("move calls: %+v", calls)
	}
}

func TestDeleteNeverPermanentAndRefusesTrashOutbox(t *testing.T) {
	h := newHarness(t, newFixture(), true, false)
	h.fail(t, "delete_messages", map[string]any{"accountId": "a1", "messageIds": []string{"m1", "m4"}}, "already in Trash")
	h.fail(t, "delete_messages", map[string]any{"accountId": "a1", "messageIds": []string{"m5"}}, "in the Outbox")
	h.fail(t, "delete_messages", map[string]any{"accountId": "a1", "messageIds": []string{"m9"}}, "messageNotFound (1102)")
	out := h.ok(t, "delete_messages", map[string]any{"accountId": "a1", "messageIds": []string{"m1", "m2"}})
	mustContain(t, out, "moved 2 messages to Trash")
	h.fb.mu.Lock()
	calls := append([]api.MessageDeleteParams(nil), h.fb.deleteCalls...)
	h.fb.mu.Unlock()
	if len(calls) != 1 || calls[0].Permanent || !reflect.DeepEqual(calls[0].MessageIDs, []api.MessageID{"m1", "m2"}) {
		t.Errorf("delete calls: %+v", calls)
	}
}

func TestSendRequiresSessionDraft(t *testing.T) {
	h := newHarness(t, newFixture(), false, true)
	h.fail(t, "send_message", map[string]any{"draftId": "d_ui"}, "was not created by this session")

	out := h.ok(t, "create_draft", map[string]any{"accountId": "a1", "to": []string{"carol@example.net"}, "subject": "hi", "body": "x"})
	mustContain(t, out, "send_message with draftId=d1 sends it")
	out = h.ok(t, "send_message", map[string]any{"draftId": "d1"})
	mustContain(t, out, "queued as outbox message o1 in account a1")
	h.fail(t, "send_message", map[string]any{"draftId": "d1"}, "was not created by this session")

	h.ok(t, "create_draft", map[string]any{"accountId": "a1", "to": []string{"carol@example.net"}})
	h.fb.setFail(api.MethodMessageSend, api.NewError(api.CodeConflict, "version mismatch"))
	h.fail(t, "send_message", map[string]any{"draftId": "d2"}, "conflict (1002): version mismatch; the draft changed")
	h.fail(t, "send_message", map[string]any{"draftId": "d2"}, "was not created by this session")

	h.fb.mu.Lock()
	sends := append([]api.MessageSendParams(nil), h.fb.sends...)
	h.fb.mu.Unlock()
	if len(sends) != 2 || sends[0] != (api.MessageSendParams{AccountID: "a1", DraftID: "d1", Version: 1}) {
		t.Errorf("send calls: %+v", sends)
	}
}
