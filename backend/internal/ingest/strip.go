// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
)

// Outcome is what Strip did with a message.
type Outcome int

const (
	// Reduced: the message's attachments the policy leaves on the server
	// are there only now.
	Reduced Outcome = iota + 1
	// Whole: nothing (more) of the message is to be left on the server,
	// and it is marked so (strippable 0, or store.StrippableNever when it
	// cannot be reduced safely) until a download stores it again.
	Whole
	// Later: the message has parts the rule will leave on the server once
	// it applies to the message, not now.
	Later
)

// Strip is Store for a message already stored, as the background pass
// finds it: stored whole once it aged past the policy, or, under
// pol.NeverStore, stored partial while its file still holds attachments
// (reduced under attachmentOfflineDays, which keeps small ones). Under the
// message's write lock it reads the stored file, decides as Store does
// and, when parts are to go, replaces the file with a verified skeleton;
// the skeleton of a partial message is made from the one stored, whose
// omitted parts are empty and stay omitted, and checked against it, and
// the row's remote set grows by the parts omitted now. Only the file and
// the partial-state columns change; the body columns, and so the search
// index, stay as they are. The commit expects the row as m describes it
// (fetched, whole or partial as m is, downloaded at m.HydratedAt): a
// message downloaded or changed meanwhile is store.ErrConflict, and so is
// a partial message without pol.NeverStore. The decision is taken on the
// row as it is under the lock, in the folder it is in then, not on m: a
// message the user moved into Drafts after the pass listed it keeps every
// part (and a move after the decision fails the commit,
// store.ErrConflict). A message that cannot be reduced safely (a missing
// or damaged file, a skeleton that does not verify, a signed or encrypted
// message) is marked Whole, as never to be reduced
// (store.StrippableNever), so the pass does not come back to it under any
// policy; a partial message with nothing left to omit is marked Whole with
// 0. A reader that keeps the stored file open for longer than the store
// waits (Windows refuses to replace an open file) leaves the message as it
// was: store.ErrBusy, for a later pass. Under pol.NeverStore the skeleton
// is staged in memory. Nothing about the content is logged.
func Strip(ctx context.Context, st *store.Store, m store.Message, pol Policy, now time.Time, log *slog.Logger) (Outcome, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	var out Outcome
	settle := func(n int64) error {
		out = Later
		if n == 0 || n == store.StrippableNever {
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
		switch {
		case cur.BodyState != store.BodyFetched || cur.RawState != m.RawState || !cur.HydratedAt.Equal(m.HydratedAt):
			return fmt.Errorf("%w: body %s, file %s", store.ErrConflict, cur.BodyState, cur.RawState)
		case cur.RawState == store.RawPartial && !pol.NeverStore:
			return fmt.Errorf("%w: a partial message is reduced further only under NeverStore", store.ErrConflict)
		case cur.RawState != store.RawFull && cur.RawState != store.RawPartial:
			return fmt.Errorf("%w: file %s", store.ErrConflict, cur.RawState)
		}
		folder, err := st.GetFolder(ctx, cur.AccountID, cur.FolderID)
		if err != nil {
			return err
		}
		raw, err := tx.Open()
		switch {
		case errors.Is(err, store.ErrNotFound):
			return settle(store.StrippableNever)
		case err != nil:
			return err
		}
		defer raw.Close()
		// A partial message's file parses with its omitted parts empty:
		// Decide finds no candidate among them.
		parsed, err := mime.Parse(raw, mime.DefaultLimits())
		if err != nil {
			return settle(store.StrippableNever)
		}
		plan := Decide(parsed, Target{
			AccountID: cur.AccountID, MessageID: cur.ID, Role: folder.Role,
			HasServerCopy: cur.UID > 0 || cur.RemoteID != "",
			InternalDate:  cur.InternalDate, Date: cur.Date, HydratedAt: cur.HydratedAt,
		}, pol, now, false)
		if len(plan.Omit) == 0 {
			return settle(plan.Strippable())
		}
		if err := raw.Rewind(); err != nil {
			return err
		}
		skel, omitted, err := reduce(ctx, st, raw, parsed, plan.Omit, MaxMessageBytes, pol)
		// That was the last read of the stored file: it is closed before the
		// commit renames the skeleton over it, which Windows refuses while a
		// handle of the file is open, this one included.
		raw.Close()
		switch {
		case errors.Is(err, mime.ErrNotReducible), errors.Is(err, store.ErrRawCorrupt):
			log.Info("message kept as stored: its parts cannot be left on the server safely", "message", m.ID)
			return settle(store.StrippableNever)
		case err != nil:
			return err
		}
		defer skel.Remove()
		// The parts omitted before stay omitted; the sizes of those
		// omitted now come from the stored file, which holds them whole.
		remote := append(slices.Clone(cur.RemoteParts), omitted...)
		hydrated := m.HydratedAt
		if _, err := tx.Commit(store.RawCommit{
			Source:          skel,
			RemoteParts:     remote,
			RemoteBytes:     cur.RemoteBytes + sizeOf(parsed, omitted),
			StrippableBytes: plan.ReducedStrippable(pol),
			Expect:          store.RawExpect{BodyState: store.BodyFetched, RawState: cur.RawState, HydratedAt: &hydrated},
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
