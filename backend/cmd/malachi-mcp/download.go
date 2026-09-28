// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Attachments on demand (docs/api.md §3, Attachment.remote): the large
// attachments of older messages, or under neverStoreAttachments every
// attachment, may be kept on the account's mail server only. The bridge
// has the daemon fetch such a message (message.download) for the two
// tools that need a file's bytes, get_attachment (only when message.part
// says the part is not there, since the daemon may hold the message in
// memory) and create_draft forwarding it, and for nothing else: reading a
// message never downloads anything. The daemon fetches from the account's
// own server, never from a URL in the mail, and a process may make it
// download at most maxSessionDownloadBytes.

// sessionDownloads counts, by message size, what this process made the
// daemon download: every message.download it asks for counts, the same
// message again too, since the daemon may have let go of what it fetched
// before (under neverStoreAttachments it holds a message in memory only,
// for a while) and fetch it anew.
type sessionDownloads struct {
	mu   sync.Mutex
	used int64
}

// reserve counts n bytes; false when they would exceed the budget.
func (s *sessionDownloads) reserve(n int64) bool {
	n = max(n, 0)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used+n > maxSessionDownloadBytes {
		return false
	}
	s.used += n
	return true
}

// refund gives back what a download that fetched nothing had reserved.
func (s *sessionDownloads) refund(n int64) {
	n = max(n, 0)
	s.mu.Lock()
	s.used = max(s.used-n, 0)
	s.mu.Unlock()
}

// downloadFailure is why a message could not be made whole: text is the
// whole explanation for a tool error, reason a few words for a result that
// goes on without the files. Both are the bridge's own words and the
// daemon's error code, never mail content.
type downloadFailure struct {
	text   string
	reason string
}

// download has the daemon fetch message m from its mail server
// (message.download) within downloadTimeout and the session's budget, and
// returns the message as the daemon reports it afterwards: with no
// attachment remote (under neverStoreAttachments the parts stay remote and
// the daemon serves them from memory), and on Microsoft 365 possibly with
// other part ids. Every call counts m's size. Only a download that
// certainly fetched nothing gives back what this call counted
// (fetchedNothing); one the bridge stops waiting for, by its timeout or
// because the tool call was cancelled, keeps it counted: the daemon
// finishes it.
func (b *bridge) download(ctx context.Context, acc api.AccountID, m api.MessageSummary) (*api.Message, *downloadFailure) {
	if !b.downloads.reserve(m.Size) {
		return nil, &downloadFailure{
			text: fmt.Sprintf("the attachment is on the mail server only, and this session already had %d MiB downloaded from it, its limit; the user can open it in Malachi Mail",
				maxSessionDownloadBytes>>20),
			reason: "this session's download limit is spent",
		}
	}
	dctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	res, err := callRPC[api.MessageDownloadResult](dctx, b.rpc, api.MethodMessageDownload, api.MessageDownloadParams{AccountID: acc, MessageID: m.ID})
	switch {
	case err == nil:
		return &res.Message, nil
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
		return nil, &downloadFailure{
			text: fmt.Sprintf("downloading the message from the mail server did not finish within %s; the daemon keeps going, call again in a few minutes",
				downloadTimeout),
			reason: "the download did not finish in time",
		}
	}
	if fetchedNothing(err) {
		b.downloads.refund(m.Size)
	}
	reason := "the download failed"
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		reason = fmt.Sprintf("the download failed: %s (%d)", apiErr.Code, int(apiErr.Code))
	}
	return nil, &downloadFailure{text: errorText(err), reason: reason}
}

// fetchedNothing reports whether a failed message.download certainly
// downloaded nothing: the daemon answered with an error of its own, other
// than cancelled (which it also answers when the call ended while the
// download goes on), or the request never reached it. A call that ended on
// the bridge's side (its context: a timeout, a cancelled tool call) or a
// connection lost after the request went out leaves the daemon
// downloading.
func fetchedNothing(err error) bool {
	var (
		apiErr *api.Error
		down   *daemonDownError
		hs     *api.HandshakeError
	)
	switch {
	case errors.As(err, &apiErr):
		return apiErr.Code != api.CodeCancelled
	case errors.As(err, &down), errors.As(err, &hs), errors.Is(err, errWriteFailed):
		return true
	}
	return false
}

// anyRemote reports whether a message has an attachment kept on the mail
// server only.
func anyRemote(m api.Message) bool {
	for _, a := range m.Attachments {
		if a.Remote {
			return true
		}
	}
	return false
}
