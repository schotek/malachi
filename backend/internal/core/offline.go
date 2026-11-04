// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// attachmentStepName names the step in the meta table.
const attachmentStepName = "attachments"

// metaReevaluated records in the meta table the rule
// (ingest.NeverStoreRule) by which the step, under
// Preferences.NeverStoreAttachments, last judged the settled messages
// again (reevaluateOnce): its number, or reevaluatedRule2. It is cleared
// whenever the preference is off, so that every switch-on does it once,
// and a store in the mode does it once more when the rule grows.
const metaReevaluated = "attachments.never_store.reevaluated"

// reevaluatedRule2 is what metaReevaluated says after the judging again
// under rule 2 of ingest.NeverStoreRule, which came before the rule's
// number was recorded.
const reevaluatedRule2 = "done"

// stripBatch is how many messages one batch of the step reduces at most.
const stripBatch = 50

// attachmentStep is the raw maintenance step that leaves on the server the
// large attachments of the stored messages that aged past
// Preferences.AttachmentOfflineDays (or whose grace after a download
// ended), oldest first: ingest.Strip, which works on the stored files
// only. Under Preferences.NeverStoreAttachments it leaves every
// attachment of every stored message there, and every picture the HTML
// shows of api.LargeAttachmentMinBytes and more (ingest.Decide), whatever
// its age and however recently it was downloaded, the small attachments
// and the large pictures of the messages the other rule reduced included
// (the whole messages first, then the partial ones). Loosening either
// preference brings nothing back from the server: that applies to mail
// downloaded from then on, and an older attachment is fetched when the
// user opens it (message.download).
type attachmentStep struct {
	b   *Backend
	now func() time.Time // a test hook

	mu     sync.Mutex
	failed map[string]bool // messages whose reduction failed once
}

// newAttachmentStep returns the raw maintenance step that keeps large
// attachments of older messages, or every attachment, on the server only
// (Preferences.AttachmentOfflineDays, NeverStoreAttachments).
func newAttachmentStep(b *Backend) RawStep {
	return &attachmentStep{b: b, now: time.Now, failed: map[string]bool{}}
}

func (*attachmentStep) Name() string { return attachmentStepName }

// Key is "1:<days>:<date>": the policy, and the day, so that the step
// runs again every day as messages age past the cutoff and the grace of
// downloaded messages ends. "1:0" (keep everything) needs no pass at all.
// Under NeverStoreAttachments it is "<rule>:never:<date>", rule being
// ingest.NeverStoreRule ("3:never:2026-09-27"), so that a daemon with a
// new rule runs the pass at once: neither age nor a download matters
// there, but whole messages still turn up after a pass (one moved out of
// Drafts, one a batch passed over while it changed), so a pass runs every
// day too, cheap through the partial index when there is nothing to do;
// the judging again of the settled messages is not repeated
// (reevaluateOnce). What must not wait for the next day starts a
// pass at once (restartRawStep): switching the preference either way, an
// account enabled again, a message stored whole while the preference was
// being switched on (storedUnder). It only reads the preferences, as
// system.storage asks for it too.
func (s *attachmentStep) Key(_ context.Context, now time.Time) (string, error) {
	pol := s.b.attachmentPolicy()
	switch {
	case pol.NeverStore:
		return fmt.Sprintf("%d:never:%s", ingest.NeverStoreRule, now.Format(time.DateOnly)), nil
	case pol.AttachmentOfflineDays == 0:
		return "1:0", nil
	}
	return fmt.Sprintf("1:%d:%s", pol.AttachmentOfflineDays, now.Format(time.DateOnly)), nil
}

// Batch reduces up to stripBatch of the oldest messages the policy
// applies to; under NeverStoreAttachments the messages stored whole come
// first and the partial ones that still hold an attachment fill the rest
// (store.StripQuery.Partial). On the first batch of a pass it first
// marks, from the attachment lists alone, the whole messages that have
// nothing to leave on the server; under NeverStoreAttachments, once per
// switch-on, it first makes the messages settled while their file still
// holds an attachment be judged again (reevaluateOnce). A message that changed or went meanwhile (a download,
// a deletion) is passed over, as the next listing shows it as it is now,
// and so is one whose file a reader kept open (store.ErrBusy, Windows),
// for the next pass; one whose reduction fails is retried once, in the
// next pass, then kept whole for good (store.StrippableNever). The pass
// ends when nothing is left, or when a batch could do nothing with what it
// found.
func (s *attachmentStep) Batch(ctx context.Context, cursor string) (string, error) {
	pol := s.b.attachmentPolicy()
	st := s.b.store
	if pol.NeverStore {
		if err := s.reevaluateOnce(ctx); err != nil {
			return cursor, err
		}
	} else {
		if cursor == "" {
			s.b.forgetReevaluation(ctx)
		}
		if pol.AttachmentOfflineDays == 0 {
			return "", nil
		}
	}
	now := s.now()
	if cursor == "" {
		if _, err := st.ClassifySmall(ctx, pol.MinBytes()); err != nil {
			return cursor, err
		}
	}
	q := store.StripQuery{Limit: stripBatch}
	if pol.NeverStore {
		q.All, q.AnyHydrated = true, true
	} else {
		q.Cutoff, q.All, q.HydratedBefore = pol.Cutoff(now), pol.AttachmentOfflineDays == api.AttachmentOfflineNone, now.Add(-ingest.HydratedKeep)
	}
	cands, err := st.ListStripCandidates(ctx, q)
	if err != nil {
		return cursor, err
	}
	if pol.NeverStore && len(cands) < stripBatch {
		// Then the messages stored partial that still hold attachments.
		more, err := st.ListStripCandidates(ctx, store.StripQuery{Partial: true, Limit: stripBatch - len(cands)})
		if err != nil {
			return cursor, err
		}
		cands = append(cands, more...)
	}
	progress, last := 0, ""
	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			return cursor, err
		}
		last = c.ID
		out, err := ingest.Strip(ctx, st, c.Message, pol, now, s.b.log)
		switch {
		case err == nil:
			if out != ingest.Later {
				progress++
			}
		case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrNotFound):
		case errors.Is(err, store.ErrBusy):
			// A reader kept the message's file open all the while (Windows
			// refuses to replace an open file): nothing changed, and the
			// next pass tries again. Not a failure of the reduction.
			s.b.log.Info("attachments kept for now: the message's file is in use; the next pass reduces it", "message", c.ID)
		case errors.Is(err, store.ErrNoSpace), ctx.Err() != nil:
			return cursor, err
		case s.failedBefore(c.ID):
			s.b.log.Warn("attachments kept: reducing the message failed again", "message", c.ID, "err", err)
			if err := st.SetStrippableBytes(ctx, c.ID, store.StrippableNever); err != nil && !errors.Is(err, store.ErrNotFound) {
				return cursor, err
			}
			progress++
		default:
			return cursor, fmt.Errorf("reduce message %s: %w", c.ID, err)
		}
	}
	if progress == 0 {
		return "", nil
	}
	return last, nil
}

// failedBefore records a failed reduction of the message and reports
// whether it had failed before.
func (s *attachmentStep) failedBefore(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed[id] {
		delete(s.failed, id)
		return true
	}
	s.failed[id] = true
	return false
}

// reevaluateOnce makes the messages settled as having nothing to leave on
// the server while their file still holds an attachment (all small under
// the size threshold, pictures the HTML shows, or a partial message a
// repair or a reduction left so) be judged again under
// NeverStoreAttachments (store.ReevaluateSettled), once per switch-on and
// rule: metaReevaluated records the rule (ingest.NeverStoreRule) it was
// done by. A store judged again under rule 2 in this switch-on holds, in
// its settled messages, only pictures the HTML shows: of those rule 3
// leaves on the server only the ones of api.LargeAttachmentMinBytes and
// more, so only a message that holds such a part is judged again.
func (s *attachmentStep) reevaluateOnce(ctx context.Context) error {
	st := s.b.store
	v, _, err := st.GetMeta(ctx, metaReevaluated)
	rule := strconv.Itoa(ingest.NeverStoreRule)
	if err != nil || v == rule {
		return err
	}
	minBytes := ingest.Policy{NeverStore: true}.MinBytes()
	if v == reevaluatedRule2 && ingest.NeverStoreRule == 3 {
		// Rule 3 adds to rule 2 only the pictures the HTML shows of
		// api.LargeAttachmentMinBytes and more; from any other rule every
		// settled message that holds an attachment is judged again.
		minBytes = api.LargeAttachmentMinBytes
	}
	n, err := st.ReevaluateSettled(ctx, minBytes)
	if err != nil {
		return err
	}
	if n > 0 {
		s.b.log.Info("stored messages to judge again: no attachment is to be kept", "messages", n, "rule", rule)
	}
	return st.SetMeta(ctx, metaReevaluated, rule)
}

// forgetReevaluation clears metaReevaluated while NeverStoreAttachments is
// off, so that the next switch-on judges the settled messages again; a
// failure is logged, and the attachment step tries again.
func (b *Backend) forgetReevaluation(ctx context.Context) {
	v, _, err := b.store.GetMeta(ctx, metaReevaluated)
	if err == nil && v == "" {
		return
	}
	if err == nil {
		err = b.store.SetMeta(ctx, metaReevaluated, "")
	}
	if err != nil {
		b.log.Warn("clear the re-evaluation mark of the attachments", "err", err)
	}
}

// restartNeverStorePass makes the raw maintenance loop run the attachment
// step again under NeverStoreAttachments rather than on the next day: an
// account enabled again has stored messages the pass passed over while it
// was paused.
func (b *Backend) restartNeverStorePass(ctx context.Context) {
	if b.neverStoreAttachments() {
		b.restartRawStep(ctx, attachmentStepName)
	}
}

// storedUnder is told that a message was stored under pol, by a syncer
// or message.download, which read the preferences when the message
// arrived. When NeverStoreAttachments is on by now but pol did not say so
// (the preference was switched on while the message was being received,
// perhaps after the pass of the switch-on had judged the stored mail), the
// message may be stored whole: the attachment pass is to judge it again,
// and starts over at once (rejudge).
func (b *Backend) storedUnder(ctx context.Context, id string, pol ingest.Policy) {
	if pol.NeverStore || !b.neverStoreAttachments() {
		return
	}
	b.rejudge(context.WithoutCancel(ctx), id)
}

// rejudge makes the attachment pass judge a stored message again: one
// settled as having nothing to leave on the server by the size threshold
// is marked not evaluated (store.ReevaluateSettledMessage), as the
// switch-on does for all of them once, and the pass starts over, since it
// may have finished for today already.
func (b *Backend) rejudge(ctx context.Context, id string) {
	if _, err := b.store.ReevaluateSettledMessage(ctx, id, ingest.Policy{NeverStore: true}.MinBytes()); err != nil {
		b.log.Warn("judge a stored message again", "message", id, "err", err)
	}
	b.restartRawStep(ctx, attachmentStepName)
}
