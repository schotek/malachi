// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// draftSyncQuiet is how long a draft rests after a save before the syncer
// stores it in the Drafts folder: long enough that a window saving on
// Ctrl+S or its close dialog right after an autosave uploads once, short
// enough that a draft closed just before the application quits has made
// it (each upload replaces the whole message, attachments included). A
// variable so tests can lower it.
var draftSyncQuiet = 30 * time.Second

// buildDraft writes the server copy of a stored draft (the syncers'
// BuildDraft): the message message.send would build, plus a Bcc header,
// under a fresh Message-ID, dated when the draft was last saved. A draft
// that is gone is store.ErrNotFound; a storage failure is an *api.Error
// with CodeStorageError; anything else (an attachment file missing, an
// address the builder refuses, a message over the size cap) is this
// draft's problem.
func (b *Backend) buildDraft(ctx context.Context, accountID, draftID string) (store.DraftUpload, error) {
	a, err := b.store.GetAccount(ctx, accountID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.DraftUpload{}, fmt.Errorf("account %s: %w", accountID, err)
		}
		return store.DraftUpload{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	d, err := b.store.GetDraft(ctx, accountID, draftID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.DraftUpload{}, fmt.Errorf("draft %s: %w", draftID, err)
		}
		return store.DraftUpload{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	inReplyTo, references := b.draftThreading(ctx, d)
	in := smtp.BuildInput{
		From:        api.Address{Name: a.Config.DisplayName, Address: a.Config.Email},
		To:          d.To,
		CC:          d.CC,
		Subject:     d.Subject,
		Text:        d.TextBody,
		HTML:        d.HTMLBody, // the sanitiser's output, stored by draft.save
		InReplyTo:   inReplyTo,
		References:  references,
		Date:        d.UpdatedAt,
		MessageID:   smtp.NewMessageID(a.Config.Email),
		Attachments: b.smtpAttachments(d.Attachments),
	}
	var buf bytes.Buffer
	w := &cappedBuffer{buf: &buf, limit: outgoingLimit}
	if err := smtp.BuildDraftMessage(w, in, d.BCC); err != nil {
		return store.DraftUpload{}, err
	}
	return store.DraftUpload{
		DraftID:      d.ID,
		Version:      d.Version,
		RFCMessageID: in.MessageID,
		Date:         d.UpdatedAt,
		Raw:          buf.Bytes(),
	}, nil
}

// cappedBuffer refuses to grow past limit bytes.
type cappedBuffer struct {
	buf   *bytes.Buffer
	limit int64
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if int64(c.buf.Len()+len(p)) > c.limit {
		return 0, api.NewError(api.CodeAttachmentTooBig, "draft larger than %d bytes", c.limit)
	}
	return c.buf.Write(p)
}

// smtpAttachments are the builder's view of a draft's attachments, read
// from the attachment store when the message is written.
func (b *Backend) smtpAttachments(atts []store.Attachment) []smtp.Attachment {
	out := make([]smtp.Attachment, 0, len(atts))
	for _, att := range atts {
		path := b.store.AttachmentPath(att.ID)
		out = append(out, smtp.Attachment{
			Filename:    att.Filename,
			ContentType: att.ContentType,
			Size:        att.Size,
			Inline:      att.Inline,
			ContentID:   att.ContentID,
			Open:        func() (io.ReadCloser, error) { return os.Open(path) },
		})
	}
	return out
}

// draftThreading is threadingHeaders for a draft, falling back on the
// headers the draft keeps for a parent that is no longer, or never was,
// in the local store.
func (b *Backend) draftThreading(ctx context.Context, d store.Draft) (string, []string) {
	if irt, refs := b.threadingHeaders(ctx, d.AccountID, d.InReplyTo); irt != "" {
		return irt, refs
	}
	if d.ReplyRFCID == "" {
		return "", nil
	}
	refs := d.References
	if len(refs) > maxOutgoingReferences {
		refs = refs[len(refs)-maxOutgoingReferences:]
	}
	return d.ReplyRFCID, refs
}

// scheduleDraftSync arms a wake-up of the account's syncer for when its
// next draft upload falls due (store.NextDraftUpload), replacing the one
// armed before: a syncer idling on IDLE, or with polling off, would
// otherwise only upload with the next unrelated pass.
func (b *Backend) scheduleDraftSync(accountID string) {
	due, err := b.store.NextDraftUpload(context.Background(), accountID, draftSyncQuiet)
	if err != nil {
		b.log.Warn("schedule draft upload", "account", accountID, "err", err)
		return
	}
	b.draftMu.Lock()
	defer b.draftMu.Unlock()
	if b.draftTimers == nil {
		b.draftTimers = map[string]*time.Timer{}
	}
	if t := b.draftTimers[accountID]; t != nil {
		t.Stop()
		delete(b.draftTimers, accountID)
	}
	if due.IsZero() {
		return
	}
	// A second late, so the syncer's own clock sees the draft due.
	delay := max(time.Until(due)+time.Second, 0)
	b.draftTimers[accountID] = time.AfterFunc(delay, func() { b.triggerDrafts(accountID) })
}

// triggerDrafts wakes the account's syncer for its Drafts folder: a pass
// always pushes the queued operations and due drafts first.
func (b *Backend) triggerDrafts(accountID string) {
	folder := api.FolderID("")
	if f, err := b.store.FolderByRole(context.Background(), accountID, api.RoleDrafts); err == nil {
		folder = api.FolderID(f.ID)
	}
	b.Supervisor.Trigger(accountID, folder, false)
}

// stopDraftTimers cancels every armed wake-up (shutdown).
func (b *Backend) stopDraftTimers() {
	b.draftMu.Lock()
	defer b.draftMu.Unlock()
	for id, t := range b.draftTimers {
		t.Stop()
		delete(b.draftTimers, id)
	}
}
