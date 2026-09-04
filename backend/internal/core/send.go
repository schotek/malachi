// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// outgoingLimit caps the built RFC 5322 message (docs/api.md §4.3
// message.send). A variable so tests can lower it.
var outgoingLimit int64 = api.MaxOutgoingMessageBytes

// maxOutgoingReferences caps the References header of a reply: the newest
// identifiers are kept, the chain's tail is what threads on.
const maxOutgoingReferences = 30

// snippetRunes is the length of MessageSummary.snippet for an outgoing
// message, as for received ones.
const snippetRunes = 200

// Send builds the message from the stored draft and queues it into the
// outbox (docs/api.md §4.3): the draft is verified against the version the
// client last saw, its recipients re-checked, the RFC 5322 message written
// under the size cap and, in one transaction, the outbox message created
// and the draft removed. The result only confirms enqueueing; the outbox
// worker delivers asynchronously.
func (s *messageService) Send(ctx context.Context, p api.MessageSendParams) (*api.MessageSendResult, error) {
	if p.AccountID == "" || p.DraftID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and draftId are required")
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	d, err := s.b.store.GetDraft(ctx, a.ID, string(p.DraftID))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeDraftNotFound, "draft %s not found", p.DraftID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if d.Version != p.Version {
		return nil, api.NewError(api.CodeConflict, "draft %s was modified; reload it", p.DraftID)
	}

	var recipients []string
	for _, list := range [][]api.Address{d.To, d.CC, d.BCC} {
		for _, addr := range list {
			if err := validateAddress(addr); err != nil {
				return nil, err
			}
			recipients = append(recipients, addr.Address)
		}
	}
	if len(recipients) == 0 {
		return nil, api.NewError(api.CodeInvalidArgument, "the draft has no recipients")
	}

	now := time.Now()
	from := api.Address{Name: a.Config.DisplayName, Address: a.Config.Email}
	inReplyTo, references := s.b.threadingHeaders(ctx, a.ID, d.InReplyTo)
	in := smtp.BuildInput{
		From:       from,
		To:         d.To,
		CC:         d.CC,
		Subject:    d.Subject,
		Text:       d.TextBody,
		InReplyTo:  inReplyTo,
		References: references,
		Date:       now,
		MessageID:  smtp.NewMessageID(a.Config.Email),
	}
	var parts []api.Attachment
	for i, att := range d.Attachments {
		path := s.b.store.AttachmentPath(att.ID)
		in.Attachments = append(in.Attachments, smtp.Attachment{
			Filename:    att.Filename,
			ContentType: att.ContentType,
			Size:        att.Size,
			Open:        func() (io.ReadCloser, error) { return os.Open(path) },
		})
		// The text is part 1 of the multipart/mixed; attachments follow.
		parts = append(parts, api.Attachment{
			PartID:      strconv.Itoa(i + 2),
			Filename:    att.Filename,
			ContentType: att.ContentType,
			Size:        att.Size,
		})
	}

	limit := outgoingLimit
	m, err := s.b.store.EnqueueOutbox(ctx, store.EnqueueInput{
		DraftID:      d.ID,
		DraftVersion: p.Version,
		Message: store.Message{
			AccountID:      a.ID,
			From:           []api.Address{from},
			To:             d.To,
			CC:             d.CC,
			BCC:            d.BCC,
			Subject:        d.Subject,
			Date:           now,
			InternalDate:   now,
			RFCMessageID:   in.MessageID,
			InReplyTo:      inReplyTo,
			References:     references,
			Snippet:        mime.Snippet(d.TextBody, snippetRunes),
			HasAttachments: len(parts) > 0,
			Attachments:    parts,
		},
		Text:         d.TextBody,
		EnvelopeFrom: a.Config.Email,
		Recipients:   recipients,
		Build:        func(w io.Writer) error { return smtp.BuildMessage(w, in) },
		Limit:        limit,
	})
	var apiErr *api.Error
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeDraftNotFound, "draft %s not found", p.DraftID)
	case errors.Is(err, store.ErrVersionConflict):
		return nil, api.NewError(api.CodeConflict, "draft %s was modified; reload it", p.DraftID)
	case errors.Is(err, store.ErrTooBig):
		// The store stops at the first byte over the cap, so the exact size
		// is unknown; report the estimate when it explains the excess.
		return nil, tooBig(limit, max(estimateOutgoingSize(d), limit+1))
	case errors.As(err, &apiErr):
		return nil, apiErr
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	s.b.log.Info("message queued", "account", a.ID, "message", m.ID, "recipients", len(recipients), "size", m.Size)
	s.b.Delivery.Wake(a.ID)
	s.b.outboxChanged(a.ID)
	return &api.MessageSendResult{OutboxID: api.MessageID(m.ID)}, nil
}

// threadingHeaders derives In-Reply-To and References for a reply: the
// stored message the draft answers supplies its Message-ID and its own
// References chain. A draft whose inReplyTo names no stored message (it
// was deleted meanwhile) simply sends without threading headers.
func (b *Backend) threadingHeaders(ctx context.Context, accountID, inReplyTo string) (string, []string) {
	if inReplyTo == "" {
		return "", nil
	}
	m, err := b.store.GetMessage(ctx, accountID, inReplyTo)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			b.log.Warn("look up replied-to message", "message", inReplyTo, "err", err)
		}
		return "", nil
	}
	if m.RFCMessageID == "" {
		return "", nil
	}
	refs := make([]string, 0, len(m.References)+1)
	for _, r := range m.References {
		if r != "" && r != m.RFCMessageID {
			refs = append(refs, r)
		}
	}
	refs = append(refs, m.RFCMessageID)
	if len(refs) > maxOutgoingReferences {
		refs = refs[len(refs)-maxOutgoingReferences:]
	}
	return m.RFCMessageID, refs
}

// estimateOutgoingSize approximates the built message: the text as is,
// each attachment base64-encoded with line breaks, plus header overhead.
func estimateOutgoingSize(d store.Draft) int64 {
	n := int64(len(d.TextBody)) + 2048
	for _, a := range d.Attachments {
		n += (a.Size+2)/3*4 + a.Size/57*2 + 512
	}
	return n
}
