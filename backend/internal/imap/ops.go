// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// opsPerRound bounds one NextOps read.
const opsPerRound = 500

// opGroup is a run of operations with the same kind, source folder and
// payload, pushed as one command over a UID set.
type opGroup struct {
	kind     store.OpKind
	folderID string
	set      []api.Flag
	clear    []api.Flag
	targetID string
	ops      []store.Op
}

// pushOps pushes the account's due local operations in order. An
// operation whose UID snapshot is 0 (blocked on an earlier, unreconciled
// move) is skipped together with every later operation of the same
// message. A NO/BAD marks the group's operations failed with backoff and
// drops them after maxOpAttempts; a dead connection ends the push.
func (s *Syncer) pushOps(ctx context.Context, sess *session) error {
	for round := 0; round < 20; round++ {
		ops, err := s.deps.Store.NextOps(ctx, s.account.ID, s.now(), opsPerRound)
		if err != nil {
			return storageError(err)
		}
		groups := groupOps(ops)
		for _, g := range groups {
			if err := s.pushGroup(ctx, sess, g); err != nil {
				return err
			}
		}
		if len(ops) < opsPerRound || len(groups) == 0 {
			return nil
		}
	}
	return nil
}

// groupOps merges operations into groups while keeping the order of each
// message's own operations: an operation may join an existing group only
// when the message has no operation in a later group.
func groupOps(ops []store.Op) []*opGroup {
	var groups []*opGroup
	blocked := map[string]bool{}
	lastGroup := map[string]int{}
	byKey := map[string]int{}
	for _, op := range ops {
		if blocked[op.MessageID] {
			continue
		}
		if op.UID == 0 {
			blocked[op.MessageID] = true
			continue
		}
		key := opKey(op)
		gi, ok := byKey[key]
		last, seen := lastGroup[op.MessageID]
		if !ok || (seen && gi < last) {
			groups = append(groups, &opGroup{kind: op.Kind, folderID: op.FolderID, set: op.Set, clear: op.Clear, targetID: op.TargetFolderID})
			gi = len(groups) - 1
			byKey[key] = gi
		}
		groups[gi].ops = append(groups[gi].ops, op)
		lastGroup[op.MessageID] = gi
	}
	return groups
}

func opKey(op store.Op) string {
	var b strings.Builder
	b.WriteString(string(op.Kind))
	b.WriteByte(0)
	b.WriteString(op.FolderID)
	b.WriteByte(0)
	b.WriteString(op.TargetFolderID)
	b.WriteByte(0)
	for _, f := range op.Set {
		b.WriteString(string(f))
		b.WriteByte(',')
	}
	b.WriteByte(0)
	for _, f := range op.Clear {
		b.WriteString(string(f))
		b.WriteByte(',')
	}
	return b.String()
}

// pushGroup executes one group. Store failures are returned; command
// refusals are recorded on the operations and swallowed.
func (s *Syncer) pushGroup(ctx context.Context, sess *session, g *opGroup) error {
	folder, err := s.deps.Store.GetFolder(ctx, s.account.ID, g.folderID)
	if errors.Is(err, store.ErrNotFound) {
		return s.dropGroup(ctx, g, "source folder no longer exists")
	}
	if err != nil {
		return storageError(err)
	}
	var target store.Folder
	if g.kind == store.OpMove {
		target, err = s.deps.Store.GetFolder(ctx, s.account.ID, g.targetID)
		if errors.Is(err, store.ErrNotFound) {
			return s.dropGroup(ctx, g, "target folder no longer exists")
		}
		if err != nil {
			return storageError(err)
		}
	}

	if _, err := sess.selectMailbox(ctx, folder.Mailbox); err != nil {
		return s.groupFailed(ctx, g, err)
	}
	uids := make([]uint32, 0, len(g.ops))
	for _, op := range g.ops {
		uids = append(uids, op.UID)
	}
	set := uidSet(uids)
	permanent := []imap.Flag(nil)
	if mb := sess.Mailbox(); mb != nil {
		permanent = mb.PermanentFlags
	}

	switch g.kind {
	case store.OpFlag:
		if add := permittedFlags(permanent, toIMAPFlags(g.set)); len(add) > 0 {
			err = s.storeFlags(ctx, sess, set, imap.StoreFlagsAdd, add)
		}
		if del := permittedFlags(permanent, toIMAPFlags(g.clear)); err == nil && len(del) > 0 {
			err = s.storeFlags(ctx, sess, set, imap.StoreFlagsDel, del)
		}
	case store.OpDelete:
		err = s.storeFlags(ctx, sess, set, imap.StoreFlagsAdd, []imap.Flag{imap.FlagDeleted})
		if err == nil {
			err = s.expunge(ctx, sess, set)
		}
	case store.OpMove:
		err = s.moveGroup(ctx, sess, g, set, target.Mailbox)
	default:
		return s.dropGroup(ctx, g, "unknown operation kind")
	}
	if err != nil {
		return s.groupFailed(ctx, g, err)
	}
	for _, op := range g.ops {
		if err := s.deps.Store.MarkOpDone(ctx, op.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return storageError(err)
		}
	}
	return nil
}

// moveGroup uses UID MOVE when offered, otherwise UID COPY, \Deleted and
// (UID) EXPUNGE. COPYUID, when the server sends one, assigns the new UIDs
// to the moved rows at once; otherwise the target folder's next pass
// reconciles them through the Message-ID.
func (s *Syncer) moveGroup(ctx context.Context, sess *session, g *opGroup, set imap.UIDSet, target string) error {
	var src, dst imap.UIDSet
	if sess.caps.Has(imap.CapMove) {
		err := sess.do(ctx, commandTimeout, func() error {
			data, err := sess.Move(set, target).Wait()
			if err != nil {
				return err
			}
			if data != nil {
				src, _ = data.SourceUIDs.(imap.UIDSet)
				dst, _ = data.DestUIDs.(imap.UIDSet)
			}
			return nil
		})
		if err != nil {
			return err
		}
	} else {
		err := sess.do(ctx, commandTimeout, func() error {
			data, err := sess.Copy(set, target).Wait()
			if err != nil {
				return err
			}
			if data != nil {
				src, dst = data.SourceUIDs, data.DestUIDs
			}
			return nil
		})
		if err != nil {
			return err
		}
		if err := s.storeFlags(ctx, sess, set, imap.StoreFlagsAdd, []imap.Flag{imap.FlagDeleted}); err != nil {
			return err
		}
		if err := s.expunge(ctx, sess, set); err != nil {
			return err
		}
	}
	return s.assignCopied(ctx, g, src, dst)
}

// assignCopied maps COPYUID source→destination pairs onto the moved rows.
func (s *Syncer) assignCopied(ctx context.Context, g *opGroup, src, dst imap.UIDSet) error {
	if len(src) == 0 || len(dst) == 0 {
		return nil
	}
	from, err := uidsFromSet(src)
	if err != nil {
		return nil
	}
	to, err := uidsFromSet(dst)
	if err != nil || len(from) != len(to) {
		return nil
	}
	byUID := make(map[uint32]string, len(g.ops))
	for _, op := range g.ops {
		byUID[op.UID] = op.MessageID
	}
	for i, u := range from {
		id, ok := byUID[u]
		if !ok {
			continue
		}
		m, err := s.deps.Store.GetMessage(ctx, s.account.ID, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return storageError(err)
		}
		if err := s.deps.Store.AssignUID(ctx, id, to[i], 0, m.Flags); err != nil && !errors.Is(err, store.ErrNotFound) {
			return storageError(err)
		}
	}
	return nil
}

func (s *Syncer) storeFlags(ctx context.Context, sess *session, set imap.UIDSet, op imap.StoreFlagsOp, flags []imap.Flag) error {
	return sess.do(ctx, commandTimeout, func() error {
		return sess.Store(set, &imap.StoreFlags{Op: op, Silent: true, Flags: flags}, nil).Close()
	})
}

func (s *Syncer) expunge(ctx context.Context, sess *session, set imap.UIDSet) error {
	return sess.do(ctx, commandTimeout, func() error {
		if sess.caps.Has(imap.CapUIDPlus) {
			return sess.UIDExpunge(set).Close()
		}
		return sess.Expunge().Close()
	})
}

// groupFailed handles a command error: a status error is recorded on the
// operations (dropped after maxOpAttempts), anything else is returned.
func (s *Syncer) groupFailed(ctx context.Context, g *opGroup, err error) error {
	if !isStatusError(err) {
		return err
	}
	text := transport.CleanMessage(err.Error())
	now := s.now()
	for _, op := range g.ops {
		if op.Attempts+1 >= maxOpAttempts {
			s.log.Warn("dropping local operation", "op", op.ID, "kind", op.Kind, "err", text)
			if err := s.deps.Store.DropOp(ctx, op.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
				return storageError(err)
			}
			continue
		}
		retry := now.Add(opBackoff(op.Attempts))
		if err := s.deps.Store.MarkOpFailed(ctx, op.ID, text, retry); err != nil && !errors.Is(err, store.ErrNotFound) {
			return storageError(err)
		}
	}
	s.log.Warn("local operation refused", "kind", g.kind, "count", len(g.ops), "err", text)
	return nil
}

func (s *Syncer) dropGroup(ctx context.Context, g *opGroup, why string) error {
	s.log.Warn("dropping local operations", "kind", g.kind, "count", len(g.ops), "reason", why)
	for _, op := range g.ops {
		if err := s.deps.Store.DropOp(ctx, op.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return storageError(err)
		}
	}
	return nil
}

// opBackoff is 30 s × 2^attempts, capped at one hour.
func opBackoff(attempts int) time.Duration {
	d := opBackoffMin
	for i := 0; i < attempts && d < opBackoffMax; i++ {
		d *= 2
	}
	return min(d, opBackoffMax)
}
