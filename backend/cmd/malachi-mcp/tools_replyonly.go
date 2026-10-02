// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"regexp"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/schotek/malachi/backend/pkg/api"
)

// One suggested reply on the user's request (--reply-only, docs/mcp.md):
// the app starts a bridge for a single board case and lets the model read
// the conversation and make one reply draft, which it then links to the
// case (board.setDraft). The conversation is third-party text, so
// create_draft in such a process only answers the one message the app
// named, prefilled by the daemon, at most once; every refusal is one fixed
// text that echoes nothing of the call.

// maxReplyOnlyIDBytes bounds --reply-only.
const maxReplyOnlyIDBytes = 128

// replyOnlyIDRE is what --reply-only accepts: an opaque message id
// (daemon ids are "m_" and hex), no spaces, control or other characters.
var replyOnlyIDRE = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// validReplyOnly checks the value of --reply-only.
func validReplyOnly(id string) error {
	if id == "" || len(id) > maxReplyOnlyIDBytes || !replyOnlyIDRE.MatchString(id) {
		return fmt.Errorf("--reply-only must be a message id of 1 to %d letters, digits and . _ : -", maxReplyOnlyIDBytes)
	}
	return nil
}

// replyOnlyRefusal is create_draft's answer to anything but the one reply.
const replyOnlyRefusal = "this request drafts only a reply to the one message it is about: " +
	"mode reply (or replyAll), that messageId, the message's accountId and your body; " +
	"to, cc, bcc, subject, messageAccountId and visibility internal are refused, and no other draft is made"

// replyOnlyDoneRefusal is its answer once the reply exists (or is being
// made).
const replyOnlyDoneRefusal = "this request's one reply draft already exists (or is being made): " +
	"no other draft is made and it is not replaced; tell the user the draft is ready in Malachi Mail"

// replyOnlyInstructions is appended to serverInstructions under the flag.
func replyOnlyInstructions(id api.MessageID) string {
	return "\nThis bridge was started for one suggested reply (--reply-only): create_draft makes exactly one draft, " +
		"a reply (mode reply or replyAll) to message " + string(id) + " with your body; the daemon fills in the recipients, subject and quote. " +
		"Every other draft is refused, and so is a second one. The draft is the suggested reply on the user's board, kept in Malachi Mail " +
		"(not in the Drafts folder on the mail server): it reaches the mail server only when the user sends it, or, if the user edited it " +
		"and its conversation later disappears or is merged into another, as one of the user's ordinary drafts; left untouched, it never does."
}

// replyOnce is the one draft of a --reply-only process: free, being made,
// or made.
type replyOnce struct {
	mu          sync.Mutex
	busy, taken bool
}

// begin claims the draft; false when it is being made or was made.
func (o *replyOnce) begin() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.busy || o.taken {
		return false
	}
	o.busy = true
	return true
}

// end releases the claim; made marks the draft as made for good.
func (o *replyOnce) end(made bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.busy = false
	o.taken = o.taken || made
}

// createReplyOnly is create_draft under --reply-only: the confined reply
// (confinedReply) to the named message, once. A call the daemon refuses
// gives the draft back, so the model may correct it.
func (b *bridge) createReplyOnly(ctx context.Context, in createDraftIn) (*mcp.CallToolResult, any, error) {
	mode, ok := parseComposeMode(in.Mode)
	if !ok || !confinedReply(in, mode) || api.MessageID(in.MessageID) != b.cfg.replyOnly {
		return toolErrorf("%s", replyOnlyRefusal), nil, nil
	}
	if !b.reply.begin() {
		return toolErrorf("%s", replyOnlyDoneRefusal), nil, nil
	}
	res, out, err := b.createDraftChecked(ctx, in)
	b.reply.end(err == nil && res != nil && !res.IsError)
	return res, out, err
}
