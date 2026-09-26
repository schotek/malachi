// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/schotek/malachi/backend/pkg/api"
)

var nonceRE = regexp.MustCompile(`BEGIN UNTRUSTED MAIL CONTENT ([0-9a-f]{12}) `)

// fenceNonce returns the nonce of the (single) fence in out.
func fenceNonce(t *testing.T, out string) string {
	t.Helper()
	m := nonceRE.FindAllStringSubmatch(out, -1)
	if len(m) != 1 {
		t.Fatalf("expected exactly one fence, found %d:\n%s", len(m), out)
	}
	return m[0][1]
}

// fencedBody returns what lies between the fence lines.
func fencedBody(t *testing.T, out string) string {
	t.Helper()
	nonce := fenceNonce(t, out)
	open, close := fenceOpen(nonce), fenceClose(nonce)
	i := strings.Index(out, open)
	j := strings.LastIndex(out, close)
	if i < 0 || j < 0 || j < i {
		t.Fatalf("fence lines missing:\n%s", out)
	}
	// fenced() puts one newline after the opening line and one before the
	// closing line; neither belongs to the body.
	return strings.TrimSuffix(out[i+len(open)+1:j], "\n")
}

func TestListAccountsProjectsSafeFieldsOnly(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out := h.ok(t, "list_accounts", nil)
	mustContain(t, out, `"id": "a1"`, `"email": "bob@example.com"`, `"kind": "imap"`, `"status": "idle"`)
	mustNotContain(t, out, fxSecretHost, `"host"`, `"port"`, `"oauth2"`, `"smtp"`)
}

func TestListFolders(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out := h.ok(t, "list_folders", map[string]any{"accountId": "a1"})
	mustContain(t, out, "account a1: 6 folders", `"id": "f_in"`, `"path": "Inbox"`, `"role": "inbox"`, `"role": "archive"`)
	fenceNonce(t, out)
	h.fail(t, "list_folders", map[string]any{"accountId": "nope"}, "accountNotFound (1100)")
}

func TestListMessagesClampsLimitAndPassesCursor(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out := h.ok(t, "list_messages", map[string]any{"accountId": "a1", "folderId": "f_in"})
	mustContain(t, out, "4 messages on this page, 4 in the folder", "last page", `"id": "m1"`, `"subject": "Quarterly numbers"`, `"from": [`, "Alice Example <alice@example.org>")
	fenceNonce(t, out)

	h.ok(t, "list_messages", map[string]any{"accountId": "a1", "folderId": "f_in", "limit": 1000, "cursor": "abc"})
	out = h.ok(t, "list_messages", map[string]any{"accountId": "a1", "folderId": "f_in", "limit": 1})
	mustContain(t, out, "1 messages on this page, 4 in the folder", "next page: call again with cursor=next")

	h.fb.mu.Lock()
	calls := append([]api.MessageListParams(nil), h.fb.listCalls...)
	h.fb.mu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("expected 3 list calls, got %d", len(calls))
	}
	if calls[0].Page.Limit != defaultListLimit {
		t.Errorf("default limit: got %d, want %d", calls[0].Page.Limit, defaultListLimit)
	}
	if calls[1].Page.Limit != maxListLimit || calls[1].Page.Cursor != "abc" {
		t.Errorf("clamped call: got limit %d cursor %q", calls[1].Page.Limit, calls[1].Page.Cursor)
	}

	out = h.ok(t, "list_messages", map[string]any{"accountId": "a1", "folderId": "f_outbox"})
	mustContain(t, out, `"state": "failed"`, `"attempts": 3`, `"error": "serverError"`)
}

func TestReadMessageNeverContainsHTML(t *testing.T) {
	h := newHarness(t, newFixture(), true, false)
	out := h.ok(t, "read_message", map[string]any{"accountId": "a1", "messageId": "m1"})
	mustContain(t, out,
		"id: m1", "folder: f_in", "body-state: fetched", "flags: seen",
		"from: Alice Example <alice@example.org>", "reply-to: Alice Reply <reply@example.org>",
		"subject: Quarterly numbers", `partId=2 filename="notes.txt" type=text/plain size=12`,
		"body: chars 0-29 of 29", "Hello Bob,\nnumbers attached.")
	mustNotContain(t, out, "NEVER_SHOWN", "<div", "<script", "links:", "headers:", "List-Unsubscribe")

	h.fb.mu.Lock()
	bodyCalls, flagCalls := len(h.fb.bodyCalls), len(h.fb.flagCalls)
	remote := h.fb.bodyCalls[0].RemoteContent
	h.fb.mu.Unlock()
	if bodyCalls != 1 || remote != api.RemoteBlock {
		t.Errorf("message.body calls: %d, remoteContent %q; want 1 call with block", bodyCalls, remote)
	}
	if flagCalls != 0 {
		t.Errorf("read_message must not flag; %d flag calls", flagCalls)
	}

	out = h.ok(t, "read_message", map[string]any{"accountId": "a1", "messageId": "m1", "includeLinks": true, "includeHeaders": true})
	mustContain(t, out, "links:\n  - Click -> https://example.org/x", "headers:\n  List-Unsubscribe: <mailto:u@example.org>")
	body := fencedBody(t, out)
	mustContain(t, body, "from:", "subject:", "links:", "headers:", "body:")
}

func TestReadMessageTruncationAndOffset(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	total := utf8.RuneCountInString(fxLongBody)

	out := h.ok(t, "read_message", map[string]any{"accountId": "a1", "messageId": "m3"})
	mustContain(t, out, fmt.Sprintf("body: chars 0-%d of %d (truncated; call again with offset=%d)", defaultBodyChars, total, defaultBodyChars))
	body := fencedBody(t, out)
	slice := body[strings.Index(body, "body:\n")+len("body:\n"):]
	if !utf8.ValidString(slice) || utf8.RuneCountInString(slice) != defaultBodyChars {
		t.Errorf("slice: valid=%v runes=%d, want %d", utf8.ValidString(slice), utf8.RuneCountInString(slice), defaultBodyChars)
	}

	out = h.ok(t, "read_message", map[string]any{"accountId": "a1", "messageId": "m3", "offset": total - 10})
	mustContain(t, out, fmt.Sprintf("body: chars %d-%d of %d\n", total-10, total, total))
	mustNotContain(t, out, "truncated")
	body = fencedBody(t, out)
	slice = body[strings.Index(body, "body:\n")+len("body:\n"):]
	if utf8.RuneCountInString(slice) != 10 {
		t.Errorf("tail slice has %d runes, want 10", utf8.RuneCountInString(slice))
	}

	out = h.ok(t, "read_message", map[string]any{"accountId": "a1", "messageId": "m3", "maxChars": 1_000_000})
	mustContain(t, out, fmt.Sprintf("body: chars 0-%d of %d (truncated", maxBodyChars, total))

	out = h.ok(t, "read_message", map[string]any{"accountId": "a1", "messageId": "m3", "offset": total + 5})
	mustContain(t, out, fmt.Sprintf("body: chars %d-%d of %d\n", total, total, total))
}

func TestReadMessageBodyStates(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out := h.ok(t, "read_message", map[string]any{"accountId": "a1", "messageId": "m2"})
	mustContain(t, out, "body-state: pending (not downloaded yet", "body: empty")
	out = h.ok(t, "read_message", map[string]any{"accountId": "a1", "messageId": "m6"})
	mustContain(t, out, "html-withheld: true", "text rendering")
	mustNotContain(t, out, "NEVER_SHOWN")
}

func TestReadMessageFramesInjectedSubject(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out1 := h.ok(t, "read_message", map[string]any{"accountId": "a1", "messageId": "m3"})
	out2 := h.ok(t, "read_message", map[string]any{"accountId": "a1", "messageId": "m3"})
	n1, n2 := fenceNonce(t, out1), fenceNonce(t, out2)
	if n1 == n2 {
		t.Errorf("nonce did not change between calls: %s", n1)
	}
	body := fencedBody(t, out1)
	mustContain(t, body, "subject: "+fxInjected)
	head := out1[:strings.Index(out1, fenceOpen(n1))]
	mustNotContain(t, head, fxInjected)
}

func TestBodyCannotCloseFence(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out := h.ok(t, "read_message", map[string]any{"accountId": "a1", "messageId": "m3"})
	nonce := fenceNonce(t, out)
	if nonce == "000000000000" {
		t.Fatal("nonce collided with the forged one")
	}
	if got := strings.Count(out, fenceClose(nonce)); got != 1 {
		t.Errorf("real END line appears %d times, want 1", got)
	}
	if got := strings.Count(out, "--- END UNTRUSTED MAIL CONTENT"); got != 2 {
		t.Errorf("expected the forged and the real END line (2), got %d", got)
	}
	if strings.Index(out, fxFakeEnd) > strings.Index(out, fenceClose(nonce)) {
		t.Error("forged END line lies outside the fence")
	}
}

func TestGetAttachmentText(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	res := callRaw(t, h.cs, "get_attachment", map[string]any{"accountId": "a1", "messageId": "m1", "partId": "2"})
	if res.IsError || len(res.Content) != 2 {
		t.Fatalf("unexpected result: err=%v content=%d %s", res.IsError, len(res.Content), textOf(res))
	}
	meta := res.Content[0].(*mcp.TextContent).Text
	mustContain(t, meta, `partId=2 filename="notes.txt" contentType=text/plain size=12`, "text: bytes 0-12 of 12")
	mustNotContain(t, meta, "truncated", "replacedBytes")
	body := res.Content[1].(*mcp.TextContent).Text
	fenceNonce(t, body)
	mustContain(t, body, "hello, notes")
	h.fb.mu.Lock()
	partCalls := len(h.fb.partCalls)
	h.fb.mu.Unlock()
	if partCalls != 1 {
		t.Errorf("message.part calls: %d, want 1", partCalls)
	}
}

func TestGetAttachmentImage(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	res := callRaw(t, h.cs, "get_attachment", map[string]any{"accountId": "a1", "messageId": "m1", "partId": "3"})
	if res.IsError || len(res.Content) != 2 {
		t.Fatalf("unexpected result: err=%v content=%d %s", res.IsError, len(res.Content), textOf(res))
	}
	img, ok := res.Content[1].(*mcp.ImageContent)
	if !ok {
		t.Fatalf("second block is %T, want ImageContent", res.Content[1])
	}
	if img.MIMEType != "image/png" || !bytes.Equal(img.Data, onePixelPNG()) {
		t.Errorf("image: type %q, %d bytes", img.MIMEType, len(img.Data))
	}
}

func TestGetAttachmentWithheldWithoutFetching(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	cases := map[string]string{
		"4":  "unsupported type application/pdf",
		"5":  "HTML attachments are never returned",
		"6":  "too big",
		"11": "SVG is never returned",
	}
	for part, want := range cases {
		out := h.ok(t, "get_attachment", map[string]any{"accountId": "a1", "messageId": "m1", "partId": part})
		mustContain(t, out, "content not returned: "+want)
	}
	h.fb.mu.Lock()
	partCalls := len(h.fb.partCalls)
	h.fb.mu.Unlock()
	if partCalls != 0 {
		t.Errorf("withheld attachments must not be fetched; %d message.part calls", partCalls)
	}
	h.fail(t, "get_attachment", map[string]any{"accountId": "a1", "messageId": "m1", "partId": "99"}, `no attachment with partId "99"`)
}

func TestGetAttachmentSniffMismatch(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out := h.ok(t, "get_attachment", map[string]any{"accountId": "a1", "messageId": "m1", "partId": "7"})
	mustContain(t, out, "content not returned: content does not look like image/png")
	// A bare <svg …> has no sniffer signature and is detected as plain text;
	// what matters is that it never passes as an image.
	out = h.ok(t, "get_attachment", map[string]any{"accountId": "a1", "messageId": "m1", "partId": "13"})
	mustContain(t, out, "content not returned: content does not look like image/png (detected text/")
	out = h.ok(t, "get_attachment", map[string]any{"accountId": "a1", "messageId": "m1", "partId": "10"})
	mustContain(t, out, "content not returned: content does not look like text")
}

func TestGetAttachmentInvalidUTF8(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	res := callRaw(t, h.cs, "get_attachment", map[string]any{"accountId": "a1", "messageId": "m1", "partId": "8"})
	if res.IsError || len(res.Content) != 2 {
		t.Fatalf("unexpected result: %s", textOf(res))
	}
	mustContain(t, res.Content[0].(*mcp.TextContent).Text, "replacedBytes: 1")
	mustContain(t, res.Content[1].(*mcp.TextContent).Text, "caf\uFFFD au lait")

	out := h.ok(t, "get_attachment", map[string]any{"accountId": "a1", "messageId": "m1", "partId": "9"})
	mustContain(t, out, "content not returned: not text: 1 of 4 characters are not valid UTF-8")
}

func TestGetAttachmentTextPaging(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	res := callRaw(t, h.cs, "get_attachment", map[string]any{"accountId": "a1", "messageId": "m1", "partId": "12"})
	if res.IsError || len(res.Content) != 2 {
		t.Fatalf("unexpected result: %s", textOf(res))
	}
	mustContain(t, res.Content[0].(*mcp.TextContent).Text,
		fmt.Sprintf("text: bytes 0-%d of %d (truncated; call again with offset=%d)", defaultAttachmentTextBytes, 100<<10, defaultAttachmentTextBytes))
	body := fencedBody(t, res.Content[1].(*mcp.TextContent).Text)
	if len(body) != defaultAttachmentTextBytes {
		t.Errorf("slice is %d bytes, want %d", len(body), defaultAttachmentTextBytes)
	}
	res = callRaw(t, h.cs, "get_attachment", map[string]any{"accountId": "a1", "messageId": "m1", "partId": "12", "offset": 100<<10 - 5, "limit": 1_000_000})
	mustContain(t, res.Content[0].(*mcp.TextContent).Text, fmt.Sprintf("text: bytes %d-%d of %d", 100<<10-5, 100<<10, 100<<10))
}

func TestSyncStatusAndTrigger(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	out := h.ok(t, "sync_status", nil)
	mustContain(t, out, `"status": "error"`, `"pendingOutbox": 1`, `"failedOutbox": 2`, `"error": "networkError: dial failed"`)

	out = h.ok(t, "trigger_sync", map[string]any{"accountId": "a1", "folderId": "f_in", "full": true})
	mustContain(t, out, "sync triggered for account a1, folder f_in (full=true)")
	h.fb.mu.Lock()
	tr := h.fb.triggers
	h.fb.mu.Unlock()
	if len(tr) != 1 || tr[0].AccountID != "a1" || tr[0].FolderID != "f_in" || !tr[0].Full {
		t.Errorf("trigger params: %+v", tr)
	}
}

func TestSearchMessages(t *testing.T) {
	h := newHarness(t, newFixture(), false, false)
	h.fail(t, "search_messages", map[string]any{"query": "  "}, "query is required")
	h.fail(t, "search_messages", map[string]any{"query": "x", "folderId": "f_in"}, "folderId needs accountId")

	out := h.ok(t, "search_messages", map[string]any{"query": "zzquery", "accountId": "a1"})
	mustContain(t, out, "3 results on this page, 3 in all", "last page",
		`"id": "m1"`, `"accountId": "a1"`, `"folderId": "f_in"`, `"folder": "Inbox"`, `"folder": "Trash"`,
		`"snippet": "Hello Bob, numbers attached."`)
	if head := out[:strings.Index(out, "--- BEGIN")]; strings.Contains(head, "zzquery") {
		t.Errorf("the header repeats the query: %q", head)
	}
	mustNotContain(t, out, `"ranges"`, `"start"`)

	// A snippet cannot close the fence.
	nonce := fenceNonce(t, out)
	if got := strings.Count(out, fenceClose(nonce)); got != 1 {
		t.Errorf("real END line appears %d times", got)
	}
	if strings.Index(out, fxFakeEnd) > strings.Index(out, fenceClose(nonce)) {
		t.Error("forged END line lies outside the fence")
	}

	h.ok(t, "search_messages", map[string]any{"query": "x", "limit": 1000, "cursor": "abc"})
	out = h.ok(t, "search_messages", map[string]any{"query": "x", "limit": 1})
	mustContain(t, out, "1 results on this page, 3 in all", "next page: call again with the same query and cursor=next")
	out = h.ok(t, "search_messages", map[string]any{"query": "many"})
	mustContain(t, out, "more than 1000 in all")

	h.fb.mu.Lock()
	calls := append([]api.SearchQueryParams(nil), h.fb.searchCalls...)
	h.fb.mu.Unlock()
	if len(calls) != 4 || calls[0].Page.Limit != defaultListLimit || calls[0].AccountID != "a1" || calls[0].Query != "zzquery" {
		t.Fatalf("calls %+v", calls)
	}
	if calls[1].Page.Limit != maxListLimit || calls[1].Page.Cursor != "abc" || calls[1].AccountID != "" {
		t.Errorf("clamped call %+v", calls[1])
	}

	h.fb.setFail(api.MethodSearchQuery, api.NewError(api.CodeInvalidArgument, "query is longer than 1024 bytes"))
	h.fail(t, "search_messages", map[string]any{"query": "x"}, "invalidArgument (1001): query is longer than 1024 bytes")
}
