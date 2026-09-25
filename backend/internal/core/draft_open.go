// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// copyEditSlack absorbs the clock skew between a Graph item's date and the
// local time its upload was recorded at (newerCopy).
const copyEditSlack = 2 * time.Minute

// Open answers draft.open (docs/api.md §4.5): a message of the account's
// Drafts folder as a draft to edit. The saved draft it is the copy of is
// returned as it is — unless another client stored a newer copy of it
// after Malachi's last upload, and the draft has nothing left to upload.
// Anything else is built from the message like a draft.create template,
// unsaved, and carries Replaces when nothing was lost on the way, so that
// its first upload replaces the message instead of leaving a second copy.
// Nothing is persisted but the imported attachments.
func (s *draftService) Open(ctx context.Context, p api.DraftOpenParams) (*api.DraftOpenResult, error) {
	if p.MessageID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "messageId is required")
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	m, f, err := s.b.draftsMessage(ctx, a.ID, string(p.MessageID))
	if err != nil {
		return nil, err
	}
	linked, err := s.b.store.DraftForMessage(ctx, a.ID, m)
	found := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if found && (linked.SyncedVersion < linked.Version || !newerCopy(m, f, linked)) {
		return &api.DraftOpenResult{Draft: toAPIDraft(linked)}, nil
	}

	res, err := s.b.draftFromMessage(ctx, a.ID, m)
	if err != nil {
		return nil, err
	}
	if found {
		// The newer copy replaces the draft's text through the ordinary
		// version check of its first save.
		res.draft.ID, res.draft.Version = api.DraftID(linked.ID), linked.Version
	}
	if res.lossless {
		res.draft.Replaces = api.MessageID(m.ID)
	}
	return &api.DraftOpenResult{Draft: res.draft, Blocked: res.blocked, Skipped: res.skipped}, nil
}

// draftsMessage loads a message that must lie in a folder with role
// drafts: messageNotFound, or invalidArgument for any other folder.
func (b *Backend) draftsMessage(ctx context.Context, accountID, id string) (store.Message, store.Folder, error) {
	m, err := b.getMessage(ctx, accountID, id)
	if err != nil {
		return store.Message{}, store.Folder{}, err
	}
	f, err := b.store.GetFolder(ctx, accountID, m.FolderID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.Message{}, store.Folder{}, api.NewError(api.CodeInvalidArgument, "message %s is not in a Drafts folder", id)
	case err != nil:
		return store.Message{}, store.Folder{}, api.NewError(api.CodeStorageError, "%v", err)
	case f.Role != api.RoleDrafts:
		return store.Message{}, store.Folder{}, api.NewError(api.CodeInvalidArgument, "message %s is not in a Drafts folder", id)
	}
	return m, f, nil
}

// newerCopy reports whether m, a message the draft d is linked to, is a
// copy another client stored after Malachi's last upload: a higher UID in
// the same folder generation (IMAP), or a different item dated after the
// upload (Graph). A copy with d's own Message-ID and no better evidence is
// d's.
func newerCopy(m store.Message, f store.Folder, d store.Draft) bool {
	c := d.Copy
	if m.RemoteID != "" {
		return c.RemoteID != "" && m.RemoteID != c.RemoteID &&
			!d.SyncedAt.IsZero() && m.InternalDate.After(d.SyncedAt.Add(copyEditSlack))
	}
	return c.UID != 0 && m.UID != 0 && m.FolderID == c.FolderID &&
		f.UIDValidity == c.UIDValidity && m.UID > c.UID
}

// importedDraft is a draft built from a stored message.
type importedDraft struct {
	draft    api.Draft
	blocked  api.BlockedContent
	skipped  []api.Attachment
	lossless bool // nothing was dropped, capped or sanitised away
}

// draftFromMessage builds an unsaved draft from a stored message of a
// Drafts folder, the way draft.create builds a forward but without the
// quote around it: the recipients (Bcc included) and subject of its
// header, its sanitised HTML with its pictures copied into the attachment
// store under new ids and its other parts as attachments, or its text.
// The parent of a reply is looked up by its Message-ID. unavailable while
// the body is not downloaded yet.
func (b *Backend) draftFromMessage(ctx context.Context, accountID string, m store.Message) (importedDraft, error) {
	text, _, state, err := b.store.GetMessageText(ctx, accountID, m.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return importedDraft{}, api.NewError(api.CodeMessageNotFound, "unknown message %q", m.ID)
	case err != nil:
		return importedDraft{}, api.NewError(api.CodeStorageError, "%v", err)
	case state == store.BodyNone:
		return importedDraft{}, api.NewError(api.CodeUnavailable, "message %s is not downloaded yet", m.ID)
	case state != store.BodyFetched:
		return importedDraft{}, api.NewError(api.CodeInvalidArgument, "the content of message %s is not stored", m.ID)
	}

	q := &quoter{b: b, account: accountID, forward: true}
	raw, parsed := q.openRaw(ctx, m.ID)
	if raw != nil {
		defer raw.Close()
	}
	res := importedDraft{draft: api.Draft{AccountID: api.AccountID(accountID)}, lossless: parsed != nil && !parsed.Truncated}
	d := &res.draft

	to, cc, bcc, subject, inReplyTo := m.To, m.CC, m.BCC, m.Subject, m.InReplyTo
	if parsed != nil {
		to, cc, bcc, subject, inReplyTo = parsed.To, parsed.CC, parsed.BCC, parsed.Subject, parsed.InReplyTo
	}
	for _, list := range []struct {
		in  []api.Address
		out *[]api.Address
	}{{to, &d.To}, {cc, &d.CC}, {bcc, &d.BCC}} {
		for _, addr := range list.in {
			if clean, ok := cleanAddress(addr); ok {
				*list.out = append(*list.out, clean)
			} else {
				res.lossless = false
			}
		}
	}
	d.Subject = capSubject(cleanSubject(subject))
	if d.Subject != subject {
		res.lossless = false
	}
	if parent, err := b.store.MessageIDByRFC(ctx, accountID, inReplyTo); err == nil {
		d.InReplyTo = api.MessageID(parent)
	} else if !errors.Is(err, store.ErrNotFound) {
		return importedDraft{}, api.NewError(api.CodeStorageError, "%v", err)
	}

	if parsed != nil && parsed.HasHTML {
		if first, referenced, ok := q.sanitizeOriginal(parsed); ok {
			imp, err := q.importParts(ctx, raw, parsed, referenced)
			if err != nil {
				return importedDraft{}, err
			}
			if out, ok := q.sanitizeQuote(first.HTML, imp); ok {
				atts := q.keepReferenced(ctx, imp, out.CIDs)
				d.HTMLBody, d.TextBody = out.HTML, out.Text
				d.Attachments = toAPIAttachments(atts)
				res.blocked, res.skipped = first.Blocked, imp.skipped
				res.lossless = res.lossless && first.Blocked == (api.BlockedContent{}) && len(imp.skipped) == 0
				return res, nil
			}
			q.remove(ctx, imp.all())
		}
		res.lossless = false // the formatting is gone
	}
	if parsed != nil {
		imp, err := q.importParts(ctx, raw, parsed, nil)
		if err != nil {
			return importedDraft{}, err
		}
		d.Attachments, res.skipped = toAPIAttachments(imp.regular), imp.skipped
		if len(imp.skipped) > 0 {
			res.lossless = false
		}
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	d.TextBody = capBytes(text, api.MaxDraftBodyBytes)
	if d.TextBody != text {
		res.lossless = false
	}
	return res, nil
}
