// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"

	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

type draftService struct{ b *Backend }

// Save validates, sanitises the HTML body (compose mode), reconciles the
// attachment list and stores the draft in one transaction. A sanitiser
// failure stores nothing: the previous version stays intact so autosave can
// retry without the UI losing its text.
func (s *draftService) Save(ctx context.Context, p api.DraftSaveParams) (*api.DraftSaveResult, error) {
	d := p.Draft
	if err := validateDraft(&d); err != nil {
		return nil, err
	}

	ids := dedupe(attachmentIDs(d.Attachments))
	atts, err := s.b.store.GetAttachments(ctx, string(d.AccountID), ids)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeAttachmentNotFound, "unknown attachment")
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	var total int64
	for _, a := range atts {
		total += a.Size
	}
	if total > api.MaxDraftAttachmentBytes {
		return nil, tooBig(api.MaxDraftAttachmentBytes, total)
	}

	text, html := d.TextBody, ""
	var blocked api.BlockedContent
	if d.HTMLBody != "" {
		known := make(map[string]string)
		for _, a := range atts {
			if a.Inline {
				known[a.ContentID] = a.ID
			}
		}
		out, err := s.b.Sanitize(sanitize.Input{
			HTML:          d.HTMLBody,
			Mode:          sanitize.ModeCompose,
			Policy:        api.RemoteBlock,
			KnownCIDs:     known,
			MaxOutputSize: api.MaxDraftBodyBytes,
		})
		if err != nil {
			var apiErr *api.Error
			if errors.As(err, &apiErr) {
				return nil, apiErr
			}
			return nil, api.NewError(api.CodeSanitizeFailed, "%v", err)
		}
		html, text, blocked = out.HTML, out.Text, out.Blocked
		ids = keepReferencedInline(ids, atts, out.CIDs)
	} else {
		ids = dropInline(ids, atts)
	}

	row := store.Draft{
		ID:         string(d.ID),
		AccountID:  string(d.AccountID),
		Version:    d.Version,
		Subject:    d.Subject,
		To:         d.To,
		CC:         d.CC,
		BCC:        d.BCC,
		TextBody:   text,
		HTMLBody:   html,
		InReplyTo:  string(d.InReplyTo),
		Forwarding: string(d.Forwarding),
	}
	switch err := s.b.store.SaveDraft(ctx, &row, ids); {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeDraftNotFound, "draft %s not found", d.ID)
	case errors.Is(err, store.ErrVersionConflict):
		return nil, api.NewError(api.CodeConflict, "draft %s was modified; reload it", d.ID)
	case errors.Is(err, store.ErrAttachmentBound):
		return nil, api.NewError(api.CodeAttachmentNotFound, "attachment belongs to another draft")
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	return &api.DraftSaveResult{
		DraftID:     api.DraftID(row.ID),
		Version:     row.Version,
		TextBody:    text,
		HTMLBody:    html,
		Blocked:     blocked,
		Attachments: toAPIAttachments(row.Attachments),
	}, nil
}

func (s *draftService) List(ctx context.Context, p api.DraftListParams) (*api.DraftListResult, error) {
	if p.AccountID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId is required")
	}
	limit := p.Page.Limit
	if limit <= 0 {
		limit = api.DefaultPageLimit
	}
	if limit > api.MaxPageLimit {
		limit = api.MaxPageLimit
	}
	items, next, total, err := s.b.store.ListDrafts(ctx, string(p.AccountID), p.Page.Cursor, limit)
	switch {
	case errors.Is(err, store.ErrBadCursor):
		return nil, api.NewError(api.CodeInvalidArgument, "invalid page cursor")
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	out := make([]api.Draft, 0, len(items))
	for _, it := range items {
		out = append(out, toAPIDraft(it))
	}
	return &api.DraftListResult{Drafts: out, Page: api.PageInfo{NextCursor: next, Total: total}}, nil
}

// Delete removes the draft and its attachments; unknown ids are ignored.
func (s *draftService) Delete(ctx context.Context, p api.DraftDeleteParams) (*api.DraftDeleteResult, error) {
	if p.AccountID == "" || p.DraftID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and draftId are required")
	}
	if err := s.b.store.DeleteDraft(ctx, string(p.AccountID), string(p.DraftID)); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	return &api.DraftDeleteResult{}, nil
}

// Create needs the message store to quote and address replies.
// TODO(phase-1): implement over internal/store messages; until then the UI
// falls back to its own prefill for placeholder data.
func (s *draftService) Create(context.Context, api.DraftCreateParams) (*api.DraftCreateResult, error) {
	return nil, api.ErrNotImplemented
}

func attachmentIDs(atts []api.DraftAttachment) []string {
	out := make([]string, 0, len(atts))
	for _, a := range atts {
		out = append(out, a.ID)
	}
	return out
}

func dedupe(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := ids[:0:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// keepReferencedInline drops inline attachments whose contentId the
// sanitised HTML no longer references (the user deleted the picture).
func keepReferencedInline(ids []string, atts []store.Attachment, cids []string) []string {
	referenced := make(map[string]bool, len(cids))
	for _, c := range cids {
		referenced[c] = true
	}
	byID := make(map[string]store.Attachment, len(atts))
	for _, a := range atts {
		byID[a.ID] = a
	}
	out := ids[:0:0]
	for _, id := range ids {
		if a := byID[id]; a.Inline && !referenced[a.ContentID] {
			continue
		}
		out = append(out, id)
	}
	return out
}

// dropInline releases inline attachments from a plain-text draft: nothing
// can reference them.
func dropInline(ids []string, atts []store.Attachment) []string {
	inline := make(map[string]bool)
	for _, a := range atts {
		if a.Inline {
			inline[a.ID] = true
		}
	}
	out := ids[:0:0]
	for _, id := range ids {
		if !inline[id] {
			out = append(out, id)
		}
	}
	return out
}

func toAPIAttachments(atts []store.Attachment) []api.DraftAttachment {
	if len(atts) == 0 {
		return nil
	}
	out := make([]api.DraftAttachment, 0, len(atts))
	for _, a := range atts {
		out = append(out, toAPIAttachment(a))
	}
	return out
}

func toAPIAttachment(a store.Attachment) api.DraftAttachment {
	return api.DraftAttachment{
		ID:          a.ID,
		Filename:    a.Filename,
		ContentType: a.ContentType,
		Size:        a.Size,
		Inline:      a.Inline,
		ContentID:   a.ContentID,
	}
}

func toAPIDraft(d store.Draft) api.Draft {
	return api.Draft{
		ID:          api.DraftID(d.ID),
		AccountID:   api.AccountID(d.AccountID),
		Version:     d.Version,
		To:          d.To,
		CC:          d.CC,
		BCC:         d.BCC,
		Subject:     d.Subject,
		TextBody:    d.TextBody,
		HTMLBody:    d.HTMLBody,
		InReplyTo:   api.MessageID(d.InReplyTo),
		Forwarding:  api.MessageID(d.Forwarding),
		Attachments: toAPIAttachments(d.Attachments),
		UpdatedAt:   d.UpdatedAt,
	}
}

func tooBig(limit, size int64) *api.Error {
	e := api.NewError(api.CodeAttachmentTooBig, "size %d exceeds the limit of %d bytes", size, limit)
	e.Data = map[string]int64{"limit": limit, "size": size}
	return e
}
