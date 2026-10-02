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
	if d.Local && d.Replaces != "" {
		// A local draft never holds a copy in the Drafts folder.
		return nil, api.NewError(api.CodeInvalidArgument, "local and replaces exclude each other")
	}
	// The drafts of an issue tracker are comments: their own rules, then
	// the same sanitising and storing as mail.
	var visibility api.CommentVisibility
	if a, err := s.b.store.GetAccount(ctx, string(d.AccountID)); err == nil && isIssueAccount(a) {
		if visibility, err = s.b.checkCommentDraft(ctx, a, &d); err != nil {
			return nil, err
		}
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

		CommentVisibility: visibility,
		// Read on the first save only: SaveDraft never changes it later.
		Local: d.Local && d.ID == "",
	}
	// The threading headers are kept with the draft, for a parent that
	// leaves the local store before the draft is sent.
	if d.InReplyTo != "" {
		row.ReplyRFCID, row.References = s.b.threadingHeaders(ctx, string(d.AccountID), string(d.InReplyTo))
	}
	adoptLinked := false
	if d.Replaces != "" {
		m, f, err := s.b.draftsMessage(ctx, string(d.AccountID), string(d.Replaces))
		if err != nil {
			return nil, err
		}
		c := store.CopyOf(m, f)
		row.Adopt = &c
		// Adopting the copy deletes the draft that held it before; a case
		// that links that one shows it.
		adoptLinked = s.b.boardCopyLinked(ctx, string(d.AccountID), m)
		if row.ReplyRFCID == "" && m.InReplyTo != "" {
			row.ReplyRFCID, row.References = m.InReplyTo, m.References
		}
	}
	switch err := s.b.store.SaveDraft(ctx, &row, ids); {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeDraftNotFound, "draft %s not found", d.ID)
	case errors.Is(err, store.ErrVersionConflict):
		return nil, api.NewError(api.CodeConflict, "draft %s was modified; reload it", d.ID)
	case errors.Is(err, store.ErrAttachmentBound):
		return nil, api.NewError(api.CodeAttachmentNotFound, "attachment belongs to another draft")
	case errors.Is(err, store.ErrDraftLocal):
		return nil, api.NewError(api.CodeInvalidArgument, "draft %s is local; it cannot replace a message of the Drafts folder", d.ID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	s.b.scheduleDraftSync(row.AccountID)
	s.b.boardDraftTouched(ctx, row.AccountID, row.ID)
	if adoptLinked {
		s.b.notifyBoard(false, row.AccountID)
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
	s.b.decorateDrafts(ctx, string(p.AccountID), items, out)
	return &api.DraftListResult{Drafts: out, Page: api.PageInfo{NextCursor: next, Total: total}}, nil
}

// Get returns one stored draft as draft.list lists it (draft.get): the
// board's editor opens a case's suggested reply by id without paging.
func (s *draftService) Get(ctx context.Context, p api.DraftGetParams) (*api.DraftGetResult, error) {
	if p.AccountID == "" || p.DraftID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and draftId are required")
	}
	d, err := s.b.store.GetDraft(ctx, string(p.AccountID), string(p.DraftID))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeDraftNotFound, "draft %s not found", p.DraftID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	out := []api.Draft{toAPIDraft(d)}
	s.b.decorateDrafts(ctx, string(p.AccountID), []store.Draft{d}, out)
	return &api.DraftGetResult{Draft: out[0]}, nil
}

// decorateDrafts adds what a listed draft shows beyond its row: the
// comment of an issue-tracker account's draft, and local for every draft
// of such an account (none reaches a server folder).
func (b *Backend) decorateDrafts(ctx context.Context, accountID string, rows []store.Draft, out []api.Draft) {
	if len(rows) == 0 {
		return
	}
	if b.localDraftsOnly(accountID) {
		for i := range out {
			out[i].Local = true
		}
	}
	b.decorateCommentDrafts(ctx, accountID, rows, out)
}

// Delete removes the draft and its attachments, and its copy in the
// Drafts folder through a queued delete that the syncer is woken for;
// unknown ids are ignored.
func (s *draftService) Delete(ctx context.Context, p api.DraftDeleteParams) (*api.DraftDeleteResult, error) {
	if p.AccountID == "" || p.DraftID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and draftId are required")
	}
	d, err := s.b.store.GetDraft(ctx, string(p.AccountID), string(p.DraftID))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if err := s.b.store.DeleteDraft(ctx, string(p.AccountID), string(p.DraftID)); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if !d.Copy.IsZero() {
		s.b.triggerDrafts(string(p.AccountID))
	}
	s.b.boardDraftTouched(ctx, string(p.AccountID), string(p.DraftID))
	return &api.DraftDeleteResult{}, nil
}

// Create returns the unsaved template of a reply, a reply to all, a
// forward or a new message (docs/api.md §4.5): the recipients and the
// subject derived from the original, the original quoted as sanitised
// HTML with its pictures copied into the attachment store, or a parsed
// mailto: URI. Nothing is stored but the copied parts; the quote degrades
// (HTML → text → plain) rather than failing. The helpers are in quote.go.
// A reply on an account that comments (an issue tracker) is a comment
// draft of the message's issue instead (comments.go); a forward may take
// its original from another account (messageAccountId), whose parts it
// copies into the draft's account.
func (s *draftService) Create(ctx context.Context, p api.DraftCreateParams) (*api.DraftCreateResult, error) {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}
	switch p.Mode {
	case api.ComposeNew, api.ComposeReply, api.ComposeReplyAll, api.ComposeForward:
	default:
		return nil, bad("mode must be new, reply, replyAll or forward")
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	// src is the account of the original: another of the user's accounts
	// only for a forward (a jira message forwarded from a mail account).
	src := a
	if p.MessageAccountID != "" && p.MessageAccountID != p.AccountID {
		if p.Mode != api.ComposeForward {
			return nil, bad("messageAccountId is for mode forward")
		}
		if src, err = s.b.requireAccount(ctx, string(p.MessageAccountID)); err != nil {
			return nil, err
		}
	}
	comment := p.Mode == api.ComposeReply && can(a.Config, api.CapabilityComment)
	switch {
	case comment:
	case p.Mode == api.ComposeForward:
		// The original's account must let its messages go, the draft's
		// must be able to write mail.
		if err := requireCapability(src, api.CapabilityForward); err != nil {
			return nil, err
		}
		if err := requireCapability(a, api.CapabilityCompose); err != nil {
			return nil, err
		}
	default:
		if err := requireCapability(a, modeCapability(p.Mode)); err != nil {
			return nil, err
		}
	}
	if p.Mode == api.ComposeNew {
		if p.MessageID != "" {
			return nil, bad("messageId is for reply and forward")
		}
		d, err := newDraft(s.b, a, p.Mailto)
		if err != nil {
			return nil, err
		}
		return &api.DraftCreateResult{Draft: d, Quoted: api.QuoteNone}, nil
	}
	if p.MessageID == "" {
		return nil, bad("messageId is required for mode %s", p.Mode)
	}
	if p.Mailto != "" {
		return nil, bad("mailto is for mode new")
	}
	attribution, err := validateAttribution(p.Attribution)
	if err != nil {
		return nil, err
	}
	if comment {
		return s.b.createCommentDraft(ctx, a, string(p.MessageID))
	}
	m, err := s.b.getMessage(ctx, src.ID, string(p.MessageID))
	if err != nil {
		return nil, err
	}

	d := api.Draft{AccountID: api.AccountID(a.ID)}
	forward := p.Mode == api.ComposeForward
	if forward {
		d.Subject = forwardSubject(m.Subject)
		d.Forwarding = api.MessageID(m.ID)
	} else {
		d.To, d.CC = replyRecipients(m, selfAddresses(a), p.Mode == api.ComposeReplyAll)
		d.Subject = replySubject(m.Subject)
		d.InReplyTo = api.MessageID(m.ID)
	}
	q := &quoter{b: s.b, account: a.ID, source: src.ID, forward: forward, attribution: attribution}
	res, err := q.quote(ctx, m)
	if err != nil {
		return nil, err
	}
	d.HTMLBody, d.TextBody = res.html, res.text
	d.Attachments = toAPIAttachments(res.atts)
	return &api.DraftCreateResult{Draft: d, Quoted: res.form, Blocked: res.blocked, Skipped: res.skipped}, nil
}

// modeCapability is the capability a draft.create mode needs of the
// account (Account.Capabilities).
func modeCapability(m api.ComposeMode) api.AccountCapability {
	switch m {
	case api.ComposeReply:
		return api.CapabilityReply
	case api.ComposeReplyAll:
		return api.CapabilityReplyAll
	case api.ComposeForward:
		return api.CapabilityForward
	default:
		return api.CapabilityCompose
	}
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
		Local:       d.Local,
		UpdatedAt:   d.UpdatedAt,
	}
}

func tooBig(limit, size int64) *api.Error {
	e := api.NewError(api.CodeAttachmentTooBig, "size %d exceeds the limit of %d bytes", size, limit)
	e.Data = map[string]int64{"limit": limit, "size": size}
	return e
}
