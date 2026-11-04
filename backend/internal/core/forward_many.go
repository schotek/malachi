// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"

	"github.com/schotek/malachi/backend/internal/safename"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// forwardMany is draft.create's forward of several messages (the clients'
// selection of several rows; docs/api.md §4.5 messageIds): one draft of
// account a whose subject is the newest message's with "Fwd: ", whose body
// quotes nothing, which names the newest as the message it forwards (the
// compose window keeps From on the account the copies are in), and which
// carries each message of account src whole as a message/rfc822
// attachment named after its subject. Nothing is
// downloaded (the call stays within the client's usual timeout): a
// message is attached from the copy message.download holds in memory, else
// from the stored file when it is whole; one that is not (parts kept on
// the mail server only, a body not downloaded) is reported in Skipped with
// Remote set, as is one over the caps (one part's size, a draft's
// attachments, their number), without. The SMTP builder sends such an
// attachment as application/octet-stream, as it does every message/*
// part (smtp attachmentContentType).
func (b *Backend) forwardMany(ctx context.Context, a, src store.Account, ids []api.MessageID) (*api.DraftCreateResult, error) {
	seen := make(map[api.MessageID]bool, len(ids))
	var unique []api.MessageID
	for _, id := range ids {
		if id == "" {
			return nil, api.NewError(api.CodeInvalidArgument, "empty message id")
		}
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) < 2 || len(unique) > api.MaxForwardMessages {
		return nil, api.NewError(api.CodeInvalidArgument, "messageIds needs 2 to %d messages, got %d", api.MaxForwardMessages, len(unique))
	}
	rows := make([]store.Message, 0, len(unique))
	for _, id := range unique {
		m, err := b.getMessage(ctx, src.ID, string(id))
		if err != nil {
			return nil, err
		}
		rows = append(rows, m)
	}
	// Newest first: the subject is the newest message's, and the
	// attachments follow the list's order.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Date.After(rows[j].Date) })

	d := api.Draft{AccountID: api.AccountID(a.ID), Subject: forwardSubject(rows[0].Subject),
		Forwarding: api.MessageID(rows[0].ID)}
	var (
		atts    []store.Attachment
		skipped []api.Attachment
		total   int64
	)
	for _, m := range rows {
		name := safename.Filename(cleanSubject(m.Subject) + ".eml")
		entry := api.Attachment{Filename: name, ContentType: "message/rfc822", Size: m.Size}
		raw, ok := b.storedWhole(ctx, src.ID, m)
		switch {
		case !ok:
			entry.Remote = true
			skipped = append(skipped, entry)
			continue
		case int64(len(raw)) > quotedPartLimit || total+int64(len(raw)) > api.MaxDraftAttachmentBytes ||
			len(atts) >= api.MaxDraftAttachments:
			entry.Size = int64(len(raw))
			skipped = append(skipped, entry)
			continue
		}
		att := store.Attachment{AccountID: a.ID, Filename: name, ContentType: "message/rfc822"}
		switch err := b.store.ImportAttachment(ctx, &att, bytes.NewReader(raw), quotedPartLimit); {
		case errors.Is(err, store.ErrTooBig):
			skipped = append(skipped, entry)
			continue
		case err != nil:
			for _, done := range atts {
				if err := b.store.RemoveAttachment(ctx, a.ID, done.ID); err != nil {
					b.log.Warn("forward: cannot remove a copy", "id", done.ID, "err", err)
				}
			}
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		total += att.Size
		atts = append(atts, att)
	}
	d.Attachments = toAPIAttachments(atts)
	return &api.DraftCreateResult{Draft: d, Quoted: api.QuoteNone, Skipped: skipped}, nil
}

// storedWhole is m as it came from the server without downloading it
// (wholeMessage downloads): the copy message.download holds in memory,
// else the stored file when it is not a reduced skeleton. false when
// neither has it whole; a file larger than quotedPartLimit comes back cut
// one byte over it, for the caller's cap.
func (b *Backend) storedWhole(ctx context.Context, accountID string, m store.Message) ([]byte, bool) {
	if held, ok := b.mem.get(accountID, m.ID); ok {
		return held.raw, true
	}
	if m.RawState == store.RawPartial || len(m.RemoteParts) > 0 {
		return nil, false
	}
	f, err := b.store.OpenMessageRaw(ctx, accountID, m.ID)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, quotedPartLimit+1))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	return raw, true
}
