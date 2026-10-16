// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/store"
)

// The D17 scan of the mail stored before the calendar: migration 0020
// creates cal_invitations empty, and the Stored hook only sees mail that
// arrives after it. The pass reads, in batches of messages by rowid, the
// messages whose attachments list names a calendar part, within the
// offlineDays window, and writes their rows; its cursor is meta
// calendar.invites.scan ("<parser version>:<last rowid>",
// "<v>:done"). A higher calendar.ParserVersion starts the scan over and
// reads again every row an older parser wrote (InvitationsBelowVersion).

// The pace of the pass; variables so that tests can change them.
var (
	invitationBackfillRows  = 2000
	invitationBackfillPause = 20 * time.Millisecond
)

func invitationScanDone() string { return strconv.Itoa(calendar.ParserVersion) + ":done" }

// backfillInvitations runs the scan to its end (core.Maintain); an
// interrupted pass resumes at its cursor on the next start.
func (b *Backend) backfillInvitations(ctx context.Context) error {
	cursor, found, err := b.store.GetMeta(ctx, store.MetaCalendarInvitesScan)
	if err != nil {
		return err
	}
	prefix := strconv.Itoa(calendar.ParserVersion) + ":"
	var after int64
	if found && cursor == invitationScanDone() {
		return b.rereadInvitations(ctx)
	}
	if found && strings.HasPrefix(cursor, prefix) {
		after, _ = strconv.ParseInt(strings.TrimPrefix(cursor, prefix), 10, 64)
	}
	_, offlineDays := b.SyncPrefs()
	var since time.Time
	if offlineDays > 0 {
		since = time.Now().AddDate(0, 0, -offlineDays)
	}
	accounts := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, next, err := b.store.InvitationScanBatch(ctx, after, invitationBackfillRows)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.HasInvite || !since.IsZero() && invitationScanDate(r).Before(since) {
				continue
			}
			if b.backfillInvitation(ctx, r.AccountID, r.MessageID) {
				accounts[r.AccountID] = true
			}
		}
		if next == 0 {
			break
		}
		after = next
		if err := b.store.SetMeta(ctx, store.MetaCalendarInvitesScan, prefix+strconv.FormatInt(after, 10)); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(invitationBackfillPause):
		}
	}
	if err := b.rereadInvitations(ctx); err != nil {
		return err
	}
	if err := b.store.SetMeta(ctx, store.MetaCalendarInvitesScan, invitationScanDone()); err != nil {
		return err
	}
	for acc := range accounts {
		b.invitationsChanged(acc)
	}
	return nil
}

func invitationScanDate(r store.InvitationScanRow) time.Time {
	if !r.InternalDate.IsZero() {
		return r.InternalDate
	}
	return r.Date
}

// rereadInvitations reads again the rows an older parser wrote.
func (b *Backend) rereadInvitations(ctx context.Context) error {
	accounts := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ids, err := b.store.InvitationsBelowVersion(ctx, calendar.ParserVersion, invitationBackfillRows)
		if err != nil || len(ids) == 0 {
			break
		}
		progress := false
		for _, id := range ids {
			inv, err := b.store.GetInvitation(ctx, id)
			if err != nil {
				continue
			}
			if b.backfillInvitation(ctx, inv.AccountID, id) {
				accounts[inv.AccountID] = true
			}
			if after, err := b.store.GetInvitation(ctx, id); err == nil && after.ParserVersion < calendar.ParserVersion {
				// The message could not be read again (no body, a
				// paused account): forget the row rather than loop.
				if err := b.store.DeleteInvitation(ctx, id); err != nil {
					return err
				}
			}
			progress = true
		}
		if !progress {
			break
		}
	}
	for acc := range accounts {
		b.invitationsChanged(acc)
	}
	return nil
}

// backfillInvitation reads one message's invitation; true when a row was
// written. The board's threads are marked by readInvitation; a reply or
// cancel applied here announces its calendars at once.
func (b *Backend) backfillInvitation(ctx context.Context, accountID, messageID string) bool {
	a, err := b.store.GetAccount(ctx, accountID)
	if err != nil {
		return false
	}
	m, err := b.store.GetMessage(ctx, accountID, messageID)
	if err != nil {
		return false
	}
	res, err := b.readInvitation(ctx, a, m)
	if err != nil {
		if !isCancelled(err) {
			b.log.Warn("backfill invitation", "account", accountID, "message", messageID, "err", err)
		}
		return false
	}
	if res.wrote {
		b.wakeBoard()
	}
	for acc, cals := range res.calendars {
		b.invitationCalendarChanged(acc, cals)
	}
	return res.wrote
}
