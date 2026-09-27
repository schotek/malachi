// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// attachmentStepName names the step in the meta table.
const attachmentStepName = "attachments"

// stripBatch is how many messages one batch of the step reduces at most.
const stripBatch = 50

// attachmentStep is the raw maintenance step that leaves on the server the
// large attachments of the stored messages that aged past
// Preferences.AttachmentOfflineDays (or whose grace after a download
// ended), oldest first: ingest.Strip, which works on the stored files
// only. Loosening the preference brings nothing back from the server:
// that applies to mail downloaded from then on, and an older attachment is
// fetched when the user opens it (message.download).
type attachmentStep struct {
	b   *Backend
	now func() time.Time // a test hook

	mu     sync.Mutex
	failed map[string]bool // messages whose reduction failed once
}

// newAttachmentStep returns the raw maintenance step that keeps large
// attachments of older messages on the server only
// (Preferences.AttachmentOfflineDays).
func newAttachmentStep(b *Backend) RawStep {
	return &attachmentStep{b: b, now: time.Now, failed: map[string]bool{}}
}

func (*attachmentStep) Name() string { return attachmentStepName }

// Key is "1:<days>:<date>": the policy, and the day, so that the step
// runs again every day as messages age past the cutoff and the grace of
// downloaded messages ends. "1:0" (keep everything) needs no pass at all.
// It only reads the preference, as system.storage asks for it too.
func (s *attachmentStep) Key(_ context.Context, now time.Time) (string, error) {
	days := s.b.attachmentOfflineDays()
	if days == 0 {
		return "1:0", nil
	}
	return fmt.Sprintf("1:%d:%s", days, now.Format(time.DateOnly)), nil
}

// Batch reduces up to stripBatch of the oldest messages the policy
// applies to. On the first batch of a pass it first marks, from the
// attachment lists alone, the messages that have nothing to leave on the
// server. A message that changed or went meanwhile (a download, a
// deletion) is passed over, as the next listing shows it as it is now;
// one whose reduction fails is retried once, in the next pass, then kept
// whole for good. The pass ends when nothing is left, or when a batch could
// do nothing with what it found.
func (s *attachmentStep) Batch(ctx context.Context, cursor string) (string, error) {
	days := s.b.attachmentOfflineDays()
	if days == 0 {
		return "", nil
	}
	pol := ingest.Policy{AttachmentOfflineDays: days}
	now := s.now()
	st := s.b.store
	if cursor == "" {
		if _, err := st.ClassifySmall(ctx, api.LargeAttachmentMinBytes); err != nil {
			return cursor, err
		}
	}
	cands, err := st.ListStripCandidates(ctx, store.StripQuery{
		Cutoff:         pol.Cutoff(now),
		All:            days == api.AttachmentOfflineNone,
		HydratedBefore: now.Add(-ingest.HydratedKeep),
		Limit:          stripBatch,
	})
	if err != nil {
		return cursor, err
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
		case errors.Is(err, store.ErrNoSpace), ctx.Err() != nil:
			return cursor, err
		case s.failedBefore(c.ID):
			s.b.log.Warn("large attachments kept: reducing the message failed again", "message", c.ID, "err", err)
			if err := st.SetStrippableBytes(ctx, c.ID, 0); err != nil && !errors.Is(err, store.ErrNotFound) {
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
