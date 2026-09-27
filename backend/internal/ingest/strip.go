// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
)

// Outcome is what Strip did with a message.
type Outcome int

const (
	// Reduced: the message's large attachments are on the server only now.
	Reduced Outcome = iota + 1
	// Whole: nothing of the message is to be left on the server, and it
	// is marked so (strippable 0) until a download stores it again.
	Whole
	// Later: the message has parts the rule will leave on the server once
	// it applies to the message, not now.
	Later
)

// Strip is Store for a message already stored whole, as the background
// pass finds it once it aged past the policy: under the message's write
// lock it reads the stored file, decides as Store does and, when parts are
// to go, replaces the file with a verified skeleton. Only the file and the
// partial-state columns change; the body columns, and so the search index,
// stay as they are. The commit expects the row as m describes it (fetched,
// whole, downloaded at m.HydratedAt): a message downloaded or changed
// meanwhile is store.ErrConflict. The decision is taken on the row as it
// is under the lock, in the folder it is in then, not on m: a message the
// user moved into Drafts after the pass listed it keeps every part (and a
// move after the decision fails the commit, store.ErrConflict). A message
// that cannot be reduced safely (a damaged file, a skeleton that does not
// verify) is marked Whole, so the pass does not come back to it. Nothing
// about the content is logged.
func Strip(ctx context.Context, st *store.Store, m store.Message, pol Policy, now time.Time, log *slog.Logger) (Outcome, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	var out Outcome
	settle := func(n int64) error {
		out = Later
		if n == 0 {
			out = Whole
		}
		return st.SetStrippableBytes(ctx, m.ID, n)
	}
	err := st.WithMessageRaw(ctx, m.AccountID, m.ID, func(tx *store.RawTx) error {
		// The file only changes under this lock: a row that still says what
		// m says belongs to the file about to be read.
		cur, err := st.GetMessage(ctx, m.AccountID, m.ID)
		if err != nil {
			return err
		}
		if cur.BodyState != store.BodyFetched || cur.RawState != store.RawFull || !cur.HydratedAt.Equal(m.HydratedAt) {
			return fmt.Errorf("%w: body %s, file %s", store.ErrConflict, cur.BodyState, cur.RawState)
		}
		folder, err := st.GetFolder(ctx, cur.AccountID, cur.FolderID)
		if err != nil {
			return err
		}
		raw, err := tx.Open()
		switch {
		case errors.Is(err, store.ErrNotFound):
			return settle(0)
		case err != nil:
			return err
		}
		defer raw.Close()
		parsed, err := mime.Parse(raw, mime.DefaultLimits())
		if err != nil {
			return settle(0)
		}
		plan := Decide(parsed, Target{
			AccountID: cur.AccountID, MessageID: cur.ID, Role: folder.Role,
			HasServerCopy: cur.UID > 0 || cur.RemoteID != "",
			InternalDate:  cur.InternalDate, Date: cur.Date, HydratedAt: cur.HydratedAt,
		}, pol, now, false)
		if len(plan.Omit) == 0 {
			return settle(plan.CandidateBytes)
		}
		if err := raw.Rewind(); err != nil {
			return err
		}
		skel, omitted, err := reduce(ctx, st, raw, parsed, plan.Omit, MaxMessageBytes)
		switch {
		case errors.Is(err, mime.ErrNotReducible), errors.Is(err, store.ErrRawCorrupt):
			log.Info("message kept whole: its parts cannot be left on the server safely", "message", m.ID)
			return settle(0)
		case err != nil:
			return err
		}
		defer skel.Remove()
		hydrated := m.HydratedAt
		if _, err := tx.Commit(store.RawCommit{
			Source:          skel,
			RemoteParts:     omitted,
			RemoteBytes:     sizeOf(parsed, omitted),
			StrippableBytes: plan.CandidateBytes,
			Expect:          store.RawExpect{BodyState: store.BodyFetched, RawState: store.RawFull, HydratedAt: &hydrated},
		}); err != nil {
			return err
		}
		out = Reduced
		return nil
	})
	if err != nil {
		return 0, err
	}
	return out, nil
}
