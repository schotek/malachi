// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

var readTools = []string{"create_draft", "get_attachment", "list_accounts", "list_folders", "list_messages", "read_message", "search_messages", "sync_status", "trigger_sync"}

const (
	fxReplyAttribution   = "On Wed, 23 Sep 2026 10:00 UTC, Alice Example <alice@example.org> wrote:"
	fxForwardAttribution = "---------- Forwarded message ----------\nFrom: Alice Example <alice@example.org>\nDate: Wed, 23 Sep 2026 10:00 UTC\nSubject: Quarterly numbers\nTo: Bob <bob@example.com>"
)

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
		"search_messages": {true, false, true, false},
		"get_attachment":  {true, false, true, false}, "sync_status": {true, false, true, false},
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

// draftCalls snapshots the fake's draft-related records.
func draftCalls(h *harness) (creates []api.DraftCreateParams, saves []api.DraftSaveParams, removes []api.AttachmentRemoveParams, gets int) {
	h.fb.mu.Lock()
	defer h.fb.mu.Unlock()
	return append([]api.DraftCreateParams(nil), h.fb.draftCreates...),
		append([]api.DraftSaveParams(nil), h.fb.draftSaves...),
		append([]api.AttachmentRemoveParams(nil), h.fb.attachmentRemoves...),
		h.fb.getCalls
}

func ids(atts []api.DraftAttachment) []string {
	var out []string
	for _, a := range atts {
		out = append(out, a.ID)
	}
	return out
}

func TestCreateDraftReplyRich(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out := h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "reply", "messageId": "m1", "body": "Thanks <b>!</b>\nBye"})
	mustContain(t, out,
		"draft d1 (version 1) stored in account a1; it is NOT sent.", "without --allow-send",
		"mode: reply; quoted: html; attachments: 1 bound (1 inline), 0 skipped",
		"to: Alice Reply <reply@example.org>", "subject: Re: Quarterly numbers", "in-reply-to: m1",
		`- id=att_in filename="logo.png" type=image/png size=68 inline`)
	mustNotContain(t, out, "no recipients yet", "blocked in save", "forwarding:")
	fenceNonce(t, out)

	creates, saves, removes, _ := draftCalls(h)
	if len(creates) != 1 || len(saves) != 1 || len(removes) != 0 {
		t.Fatalf("creates=%d saves=%d removes=%d", len(creates), len(saves), len(removes))
	}
	if c := creates[0]; c.AccountID != "a1" || c.Mode != api.ComposeReply || c.MessageID != "m1" || c.Attribution != fxReplyAttribution {
		t.Errorf("draft.create params: %+v", c)
	}
	d := saves[0].Draft
	wantHTML := `<p>Thanks &lt;b&gt;!&lt;/b&gt;<br/>Bye</p><div>On Wed, 23 Sep 2026 10:00 UTC, Alice Example &lt;alice@example.org&gt; wrote:</div><blockquote type="cite"><p>original</p></blockquote>`
	if d.HTMLBody != wantHTML {
		t.Errorf("saved HTMLBody:\n%s\nwant:\n%s", d.HTMLBody, wantHTML)
	}
	if d.TextBody != "" || d.Subject != "Re: Quarterly numbers" || d.InReplyTo != "m1" || d.Forwarding != "" ||
		!reflect.DeepEqual(d.To, []api.Address{{Name: "Alice Reply", Address: "reply@example.org"}}) ||
		!reflect.DeepEqual(ids(d.Attachments), []string{fxInlineID}) || d.ID != "" || d.Version != 0 {
		t.Errorf("saved draft: %+v", d)
	}
}

func TestCreateDraftOverrides(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out := h.ok(t, "create_draft", map[string]any{
		"accountId": "a1", "mode": "reply", "messageId": "m1", "body": "x",
		"to": []string{"Carol <carol@example.net>"}, "cc": []string{"dave@example.net"}, "bcc": []string{"me@example.com"},
		"subject": "re: own subject", "attribution": "custom",
	})
	mustContain(t, out, "to: Carol <carol@example.net>", "cc: dave@example.net", "bcc: me@example.com", "subject: re: own subject", "in-reply-to: m1")
	creates, saves, _, gets := draftCalls(h)
	if gets != 0 {
		t.Errorf("message.get called %d times although the attribution was given", gets)
	}
	if creates[0].Attribution != "custom" {
		t.Errorf("attribution not forwarded: %+v", creates[0])
	}
	d := saves[0].Draft
	if d.Subject != "re: own subject" || d.InReplyTo != "m1" ||
		!reflect.DeepEqual(d.To, []api.Address{{Name: "Carol", Address: "carol@example.net"}}) ||
		!reflect.DeepEqual(d.CC, []api.Address{{Address: "dave@example.net"}}) ||
		!reflect.DeepEqual(d.BCC, []api.Address{{Address: "me@example.com"}}) {
		t.Errorf("overrides not honoured: %+v", d)
	}
	if !strings.Contains(d.HTMLBody, "<div>custom</div>") || !strings.HasPrefix(d.HTMLBody, "<p>x</p>") {
		t.Errorf("HTMLBody: %s", d.HTMLBody)
	}
}

func TestCreateDraftReplyAll(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out := h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "replyAll", "messageId": "m6", "body": "all"})
	mustContain(t, out, "mode: replyAll", "to: Alice Example <alice@example.org>", "cc: Carol <carol@example.net>", "subject: Re: Withheld html")
	creates, saves, _, _ := draftCalls(h)
	if creates[0].Mode != api.ComposeReplyAll {
		t.Errorf("mode: %+v", creates[0])
	}
	if d := saves[0].Draft; !reflect.DeepEqual(d.CC, []api.Address{{Name: "Carol", Address: "carol@example.net"}}) {
		t.Errorf("cc from the template: %+v", d.CC)
	}
}

func TestCreateDraftForward(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out := h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "forward", "messageId": "m1", "body": "FYI"})
	mustContain(t, out,
		"mode: forward; quoted: html; attachments: 2 bound (1 inline), 1 skipped",
		"no recipients yet", "to: \n", "subject: Fwd: Quarterly numbers", "forwarding: m1",
		`- id=att_in filename="logo.png" type=image/png size=68 inline`,
		`- id=att_pdf filename="report.pdf" type=application/pdf size=5242880`,
		"skipped:\n- filename=\"big.png\" type=image/png size=3145729")
	mustNotContain(t, out, "in-reply-to:")
	creates, saves, removes, _ := draftCalls(h)
	if creates[0].Mode != api.ComposeForward || creates[0].Attribution != fxForwardAttribution {
		t.Errorf("forward create params: %+v", creates[0])
	}
	d := saves[0].Draft
	if len(d.To) != 0 || d.Subject != "Fwd: Quarterly numbers" || d.Forwarding != "m1" || d.InReplyTo != "" ||
		!reflect.DeepEqual(ids(d.Attachments), []string{fxInlineID, fxPDFID}) || !strings.HasPrefix(d.HTMLBody, "<p>FYI</p><div>") {
		t.Errorf("forward draft: %+v", d)
	}
	if len(removes) != 0 {
		t.Errorf("nothing should be removed after a successful save: %+v", removes)
	}
}

func TestCreateDraftOmitQuote(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out := h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "forward", "messageId": "m1", "body": "FYI", "omitQuote": true})
	mustContain(t, out, "quoted: omitted (omitQuote)", "attachments: 1 bound (0 inline), 1 skipped", `- id=att_pdf`)
	mustNotContain(t, out, "att_in")
	creates, saves, removes, gets := draftCalls(h)
	if gets != 0 || creates[0].Attribution != "" {
		t.Errorf("omitQuote must not build an attribution: gets=%d create=%+v", gets, creates[0])
	}
	d := saves[0].Draft
	if d.HTMLBody != "" || d.TextBody != "FYI" || !reflect.DeepEqual(ids(d.Attachments), []string{fxPDFID}) {
		t.Errorf("omitQuote forward draft: %+v", d)
	}
	if !reflect.DeepEqual(removes, []api.AttachmentRemoveParams{{AccountID: "a1", AttachmentID: fxInlineID}}) {
		t.Errorf("inline copy not removed: %+v", removes)
	}

	h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "reply", "messageId": "m1", "omitQuote": true})
	_, saves, removes, _ = draftCalls(h)
	if d := saves[1].Draft; d.HTMLBody != "" || d.TextBody != "" || len(d.Attachments) != 0 || d.InReplyTo != "m1" {
		t.Errorf("omitQuote reply draft: %+v", d)
	}
	if len(removes) != 2 || removes[1].AttachmentID != fxInlineID {
		t.Errorf("removes: %+v", removes)
	}
}

func TestCreateDraftQuoteDegradation(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out := h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "reply", "messageId": "m2", "body": "Hi"})
	mustContain(t, out, "quoted: none (the original's body is not downloaded", "to: Eve <eve@example.net>")
	mustNotContain(t, out, "attachments:")
	_, saves, removes, _ := draftCalls(h)
	if d := saves[0].Draft; d.HTMLBody != "" || d.TextBody != "Hi" || len(d.Attachments) != 0 || d.InReplyTo != "m2" {
		t.Errorf("none draft: %+v", d)
	}
	if len(removes) != 0 {
		t.Errorf("nothing to remove for quoted none: %+v", removes)
	}

	h.fb.mu.Lock()
	h.fb.quoteForm["m1"] = api.QuoteText
	h.fb.mu.Unlock()
	out = h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "reply", "messageId": "m1", "body": "Hi"})
	mustContain(t, out, "quoted: text (the original's HTML could not be used")
	_, saves, _, _ = draftCalls(h)
	if d := saves[1].Draft; d.HTMLBody != "" || d.TextBody != "Hi\n\n"+fxReplyAttribution+"\n> original" || len(d.Attachments) != 0 {
		t.Errorf("text draft: %+v", d)
	}
	out = h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "forward", "messageId": "m1", "body": "Hi"})
	mustContain(t, out, "quoted: text", "attachments: 1 bound (0 inline), 1 skipped")
	_, saves, _, _ = draftCalls(h)
	if d := saves[2].Draft; !reflect.DeepEqual(ids(d.Attachments), []string{fxPDFID}) || !strings.HasPrefix(d.TextBody, "Hi\n\n---------- Forwarded message") {
		t.Errorf("text forward draft: %+v", d)
	}
}

func TestCreateDraftEmptyBodyKeepsParagraph(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	h.ok(t, "create_draft", map[string]any{"accountId": "a1", "mode": "reply", "messageId": "m1"})
	_, saves, _, _ := draftCalls(h)
	if d := saves[0].Draft; !strings.HasPrefix(d.HTMLBody, "<p><br/></p><div>") {
		t.Errorf("empty body must keep the template's empty paragraph: %s", d.HTMLBody)
	}
}

func TestCreateDraftCreateErrorNoSave(t *testing.T) {
	fb := newFixture()
	fb.setFail(api.MethodDraftCreate, api.NewError(api.CodeMessageNotFound, "gone"))
	h := newHarness(t, fb, false, false)
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "mode": "reply", "messageId": "m1"}, "messageNotFound (1102): gone")
	_, saves, removes, _ := draftCalls(h)
	if len(saves) != 0 || len(removes) != 0 {
		t.Errorf("saves=%d removes=%d, want 0/0", len(saves), len(removes))
	}
	h.b.drafts.mu.Lock()
	created := h.b.drafts.created
	h.b.drafts.mu.Unlock()
	if created != 0 {
		t.Errorf("a failed create consumed a session slot")
	}
}

func TestCreateDraftSaveErrorCleansUp(t *testing.T) {
	fb := newFixture()
	fb.setFail(api.MethodDraftSave, api.NewError(api.CodeStorageError, "disk"))
	h := newHarness(t, fb, false, false)
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "mode": "forward", "messageId": "m1", "body": "x"}, "storageError (1400): disk")
	_, _, removes, _ := draftCalls(h)
	var got []string
	for _, r := range removes {
		got = append(got, r.AttachmentID)
	}
	if !reflect.DeepEqual(got, []string{fxInlineID, fxPDFID}) {
		t.Errorf("imported copies not cleaned up: %v", got)
	}
	h.b.drafts.mu.Lock()
	n := len(h.b.drafts.drafts)
	h.b.drafts.mu.Unlock()
	if n != 0 {
		t.Errorf("a failed save must not be sendable")
	}
}

func TestCreateDraftModeValidation(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "mode": "bogus", "messageId": "m1"}, `unknown mode "bogus"`)
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "messageId": "m1"}, "messageId needs mode")
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "mode": "reply"}, "mode reply needs messageId")
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "attribution": "x"}, "apply only to reply, replyAll and forward")
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "omitQuote": true}, "apply only to reply, replyAll and forward")
	creates, saves, _, _ := draftCalls(h)
	if len(creates) != 0 || len(saves) != 0 {
		t.Errorf("creates=%d saves=%d, want 0/0", len(creates), len(saves))
	}
}

func TestCreateDraftRejectsBadAddress(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "to": []string{"not an address"}}, `to: invalid address "not an address"`)
	// A missing required field is refused by the SDK's schema validation
	// before the handler runs; the text is the SDK's, but it is a tool error.
	h.fail(t, "create_draft", map[string]any{"to": []string{"x@example.org"}}, `"accountId"`)
	_, saves, _, _ := draftCalls(h)
	if len(saves) != 0 {
		t.Errorf("%d drafts saved, want 0", len(saves))
	}
}

func TestCreateDraftSessionCap(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	for i := 0; i < maxSessionDrafts; i++ {
		h.ok(t, "create_draft", map[string]any{"accountId": "a1", "to": []string{"x@example.org"}, "subject": fmt.Sprint(i)})
	}
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "to": []string{"x@example.org"}}, "which is its limit")
	h.fail(t, "create_draft", map[string]any{"accountId": "a1", "mode": "reply", "messageId": "m1"}, "which is its limit")
	creates, saves, _, _ := draftCalls(h)
	if len(saves) != maxSessionDrafts || len(creates) != 0 {
		t.Errorf("saves=%d creates=%d, want %d/0", len(saves), len(creates), maxSessionDrafts)
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
