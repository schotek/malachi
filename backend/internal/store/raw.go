// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/schotek/malachi/backend/internal/fsretry"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Raw message files.
//
// A fetched message has one file under MessageDir()/<account>/: <id> holds
// the bytes as received, <id>.zst the same bytes as one zstd frame
// (rawcodec.go); the name, never the content, says which. New files are
// written in the store's codec (SetRawCodec) and the background conversion
// (ConvertRawBatch) brings the older ones over, while a reader takes
// whichever it finds, so a message stays readable through a conversion.
// Outbox messages are always plain and flushed to disk: they are the only
// copy of mail not sent yet, and they are never converted.
//
// Every write goes to a temporary file beside the final name, is flushed
// where it replaces a file and is renamed into place, so a crash leaves the
// old or the new file whole; a .zst file is decoded and checked before its
// rename. Two variants of one message exist only for a moment (a
// conversion between its two phases), after a crash, or on Windows while a
// reader holds the replaced one open; readers then take the newer (the
// store codec's when their times are equal), and RawTx.Stat and the sweep
// keep the newest valid one. message_files accounts for every file
// (migration 0014).
//
// Locks, per message (rawLock): its writers go one at a time (mutate), and
// the short names lock orders a reader's choice of file against renames
// and removals. A reader holds nothing once its file is open, because a
// rename or a removal leaves an open file's contents alone, so a reader
// never blocks a writer. That is POSIX: Windows refuses to rename over or
// to remove a file while any handle of it is open (Go opens files without
// delete sharing). There a rename or a removal is retried for a moment
// (fsretry) under the names lock, so that the readers that have the file
// open finish meanwhile and no new one opens it; and nothing in the store
// or its callers renames over or removes a file it still has open itself.
// The rules:
//  1. never take a raw lock while a store transaction is open;
//  2. never hold two messages' locks at once;
//  3. WithMessageRaw is not re-entrant: no PutMessageRaw or WithMessageRaw
//     of the same message inside it (OpenMessageRaw and RawTx are fine);
//  4. the conversion and the sweep only try the lock and skip a busy
//     message.
//
// A deletion removes the files after its rows are committed
// (removeMessageFiles). While a writer holds the message it does not wait
// but marks the lock doomed, and the writer removes both files before it
// lets go, so no file outlives its row by more than its writer.

// RawCodec is how a raw message file is stored.
type RawCodec int32

const (
	RawPlain RawCodec = iota // <id>: the message as received
	RawZstd                  // <id>.zst: one zstd frame
)

// String is the codec's name, as message_files.codec stores it.
func (c RawCodec) String() string {
	switch c {
	case RawPlain:
		return "plain"
	case RawZstd:
		return "zstd"
	}
	return fmt.Sprintf("RawCodec(%d)", int32(c))
}

func (c RawCodec) valid() bool { return c == RawPlain || c == RawZstd }

// other is the other codec: the variant a write in c replaces.
func (c RawCodec) other() RawCodec {
	if c == RawZstd {
		return RawPlain
	}
	return RawZstd
}

// parseRawCodec reads message_files.codec.
func parseRawCodec(s string) (RawCodec, bool) {
	switch s {
	case "plain":
		return RawPlain, true
	case "zstd":
		return RawZstd, true
	}
	return 0, false
}

// MaxRawBytes caps the content of a raw message in any codec: above the
// largest message the daemon builds (api.MaxOutgoingMessageBytes) and the
// most the MIME parser reads (mime.MaxInputBytes). A write never keeps
// more, and a stored file that would decode to more is ErrRawCorrupt.
const MaxRawBytes = 64 << 20

// RawZstSuffix ends the name of a compressed raw message file.
const RawZstSuffix = ".zst"

// tmpSuffix ends the name of a raw file being written.
const tmpSuffix = ".tmp"

// SetRawCodec sets the codec new raw files are written in; the core sets it
// at start and whenever the preference changes. A codec change during a
// write is honoured by the write itself (it converts what it wrote).
func (s *Store) SetRawCodec(c RawCodec) {
	if !c.valid() {
		s.log.Warn("ignoring unknown raw codec", "codec", int32(c))
		return
	}
	s.rawCodec.Store(int32(c))
}

// RawCodec is the codec new raw files are written in (RawPlain until
// SetRawCodec).
func (s *Store) RawCodec() RawCodec { return RawCodec(s.rawCodec.Load()) }

// RawMessage is a stored raw message being read, in whatever codec it is
// stored: Read yields the message exactly as received. A damaged
// compressed file fails a Read with ErrRawCorrupt rather than yield a
// shorter or longer message.
type RawMessage interface {
	io.Reader
	io.Closer
	// Size is the message's length (its content, not the file's).
	Size() (int64, error)
	// Rewind starts the message again from its first byte.
	Rewind() error
}

// RawInfo describes a stored raw file: its codec, the message's length
// (Bytes) and the file's (DiskBytes).
type RawInfo struct {
	Codec     RawCodec
	Bytes     int64
	DiskBytes int64
}

// RawWrite says how PutMessageRaw and RawTx.Replace write a message.
type RawWrite struct {
	// Limit caps the message's length in bytes, whatever the codec (<= 0 or
	// over MaxRawBytes: MaxRawBytes). More is ErrTooBig, and nothing of the
	// write is kept.
	Limit int64
	// Size is the message's exact length when the caller knows it (an IMAP
	// literal, a staged file); 0 = unknown. A producer that writes another
	// number of bytes fails the write. A compressed file records its size,
	// so a write of unknown size is spooled plain first.
	Size int64
	// Plain stores the message uncompressed whatever the store's codec
	// (the outbox). RawTx.Replace refuses it.
	Plain bool
	// Sync flushes the file and its directory to disk before the write
	// returns (the outbox). A replacement always flushes the file.
	Sync bool
	// RequireRow keeps the file only if the message's row still exists
	// when it is renamed into place; otherwise the write removes both of
	// the message's files and is ErrNotFound.
	RequireRow bool
}

// bounds checks the sizes of w and returns its effective limit.
func (w RawWrite) bounds() (int64, error) {
	limit := w.Limit
	if limit <= 0 || limit > MaxRawBytes {
		limit = MaxRawBytes
	}
	switch {
	case w.Size < 0:
		return 0, fmt.Errorf("write message file: negative size %d", w.Size)
	case w.Size > limit:
		return 0, ErrTooBig
	}
	return limit, nil
}

// RawSource is what RawTx.Replace writes: a *Staged file or a RawProducer.
type RawSource interface{ rawSource() }

// RawProducer streams a raw message into the writer it is given.
type RawProducer func(w io.Writer) error

func (RawProducer) rawSource() {}

// MessageRawPath is the plain raw file of a message,
// MessageDir()/<accountID>/<id>; the compressed one is that plus
// RawZstSuffix. Callers read through OpenMessageRaw, which finds either.
func (s *Store) MessageRawPath(accountID, id string) string {
	return filepath.Join(s.MessageDir(), accountID, id)
}

// accountDir is the directory of an account's raw files.
func (s *Store) accountDir(accountID string) string {
	return filepath.Join(s.MessageDir(), accountID)
}

// rawName is the file name of message id in codec c.
func rawName(id string, c RawCodec) string {
	if c == RawZstd {
		return id + RawZstSuffix
	}
	return id
}

// WriteMessageRaw stores the raw message read from r in the store's codec
// (at most limit bytes of message, <= 0 meaning MaxRawBytes; more is
// ErrTooBig and nothing is kept): written to a temporary name and renamed
// into place, replacing an existing file of either codec. It returns the
// message's length.
func (s *Store) WriteMessageRaw(ctx context.Context, accountID, id string, r io.Reader, limit int64) (int64, error) {
	return s.WriteMessageRawFunc(ctx, accountID, id, limit, func(w io.Writer) error {
		_, err := io.Copy(w, r)
		return err
	})
}

// WriteMessageRawFunc is WriteMessageRaw for a producer: fn streams the
// raw message into the writer it is given. The writer refuses the first
// byte past limit with ErrTooBig, so a runaway builder stops early; the
// result is then ErrTooBig (whatever fn returned) and nothing is kept. Any
// other error from fn is returned wrapped, again with nothing kept.
func (s *Store) WriteMessageRawFunc(ctx context.Context, accountID, id string, limit int64, fn func(w io.Writer) error) (int64, error) {
	info, err := s.PutMessageRaw(ctx, accountID, id, RawWrite{Limit: limit}, fn)
	if err != nil {
		return 0, err
	}
	return info.Bytes, nil
}

// PutMessageRaw stores the raw message fn streams, as w says: in the
// store's codec unless w.Plain, replacing whatever file the message had in
// either codec, and records it in message_files when the message has a
// row. It waits for the message's other writers. A failed write keeps the
// earlier file as it was; a full disk is ErrNoSpace.
func (s *Store) PutMessageRaw(ctx context.Context, accountID, id string, w RawWrite, fn func(io.Writer) error) (RawInfo, error) {
	if err := checkMessagePath(accountID, id); err != nil {
		return RawInfo{}, fmt.Errorf("write message file: %w", err)
	}
	if fn == nil {
		return RawInfo{}, fmt.Errorf("write message file: nil producer")
	}
	limit, err := w.bounds()
	if err != nil {
		return RawInfo{}, err
	}
	h, err := s.lockRaw(ctx, accountID, id)
	if err != nil {
		return RawInfo{}, err
	}
	defer h.unlock()
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return RawInfo{}, noSpace(fmt.Errorf("create message directory: %w", err))
	}
	existing, err := statRaw(h.dir, id)
	if err != nil {
		return RawInfo{}, err
	}
	codec := RawPlain
	if !w.Plain {
		codec = s.RawCodec()
	}
	return s.writeLocked(ctx, h, rawJob{
		codec: codec, fixed: w.Plain, limit: limit, size: w.Size, produce: fn,
		// A fresh file is not flushed (a crash only costs a download, as
		// before compression); a replacement is, so the old one is not
		// traded for nothing.
		syncFile: existing.any() || w.Sync, syncDir: w.Sync, requireRow: w.RequireRow,
	})
}

// OpenMessageRaw opens the raw file of a message for reading, whichever
// codec it is stored in; ErrNotFound when there is none. The reader holds
// no lock: the file may be replaced or removed meanwhile, and the reader
// keeps reading what it opened. On Windows, where an open file can be
// neither, a writer waits a moment for the reader to close it and then
// gives up (ErrBusy): a reader closes the file as soon as it has read what
// it needs.
func (s *Store) OpenMessageRaw(ctx context.Context, accountID, id string) (RawMessage, error) {
	if err := checkMessagePath(accountID, id); err != nil {
		return nil, fmt.Errorf("open message file: %w", err)
	}
	key := rawKey(accountID, id)
	l := s.rawEntry(key)
	defer s.rawRelease(key, l)
	return s.openRaw(key, l, s.accountDir(accountID), id)
}

// openRaw opens message id in dir (openNewest). The reader counts among
// the file's readers (rawLock.readers) until it is closed.
func (s *Store) openRaw(key string, l *rawLock, dir, id string) (RawMessage, error) {
	l.names.RLock()
	f, codec, err := s.openNewest(dir, id)
	if err == nil {
		// Counted before the names lock goes, so that a writer that finds
		// the file held can tell whose it is.
		s.rawMu.Lock()
		l.readers[codec]++
		l.refs++
		s.rawMu.Unlock()
	}
	l.names.RUnlock()
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("open message file: %w", err)
	}
	release := func() { s.readerDone(key, l, codec) }
	if codec == RawZstd {
		z, err := openZstdRaw(f)
		if err != nil {
			release()
			return nil, err
		}
		return &countedRaw{RawMessage: z, release: release}, nil
	}
	return &countedRaw{RawMessage: &plainRaw{f: f}, release: release}, nil
}

// openNewest opens the file of message id in dir: its only one, or of
// both variants (a conversion between its phases, a crash between a rename
// and a removal, or a replaced file whose removal a reader held up on
// Windows) the newer, the store codec's when their times are equal, which
// is the one resolveLocked keeps. So a reader never gets the replaced
// content of a message whose codec was switched back before the pair was
// settled. The caller holds the names lock at least for reading.
func (s *Store) openNewest(dir, id string) (*os.File, RawCodec, error) {
	first := s.RawCodec()
	second := first.other()
	f, err := os.Open(filepath.Join(dir, rawName(id, first)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		f, err = os.Open(filepath.Join(dir, rawName(id, second)))
		return f, second, err
	case err != nil:
		return nil, first, err
	}
	path := filepath.Join(dir, rawName(id, second))
	other, err := os.Lstat(path)
	if err != nil || !other.Mode().IsRegular() {
		return f, first, nil
	}
	mine, err := f.Stat()
	if err != nil || !other.ModTime().After(mine.ModTime()) {
		return f, first, nil
	}
	g, err := os.Open(path)
	if err != nil {
		return f, first, nil
	}
	f.Close()
	return g, second, nil
}

// countedRaw is a stored message being read that counts among its file's
// readers until it is closed.
type countedRaw struct {
	RawMessage
	release func() // nil once closed
}

// Close closes the file and counts the reader out; closing twice is
// harmless.
func (r *countedRaw) Close() error {
	err := r.RawMessage.Close()
	if r.release != nil {
		r.release()
		r.release = nil
	}
	return err
}

// readerDone counts a reader of the message's file in codec c out, and
// the caller out of the message's lock (rawRelease).
func (s *Store) readerDone(key string, l *rawLock, c RawCodec) {
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	l.readers[c]--
	l.refs--
	if l.refs == 0 {
		delete(s.rawLocks, key)
	}
}

// fileOp runs op, a rename over or a removal of path, the message's file
// in codec c, through retry (fsretry.Do, or a batch's Do). The caller holds
// the names lock, so the retries wait for the readers that have the file
// open while no new one opens it: their count only falls meanwhile. Each
// attempt counts them as it starts, and a failure that outlasts the
// retries is ErrBusy when one of them had the file open at the last
// attempt: Windows refuses both while a handle of the file is open, and
// that reader is the cause (a message.part streaming a large attachment,
// say), however soon after the attempt it lets go; the next attempt, once
// it is done, goes through. Any other failure is returned as it is: one
// that lasts (a directory without write permission), or a handle the store
// does not count (another process, a virus scanner). Elsewhere a reader
// stops neither, and such a failure is a coincidence that the next attempt
// settles as well.
func (s *Store) fileOp(l *rawLock, c RawCodec, path string, retry func(func() error) error, op func() error) error {
	held := 0
	err := retry(func() error {
		s.rawMu.Lock()
		n := l.readers[c]
		s.rawMu.Unlock()
		err := s.refused(n)
		if err == nil {
			err = op()
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			held = n
			if s.attemptFailed != nil {
				s.attemptFailed(path)
			}
		}
		return err
	})
	if err == nil || errors.Is(err, fs.ErrNotExist) || held == 0 {
		return err
	}
	return fmt.Errorf("%w (%d readers): %w", ErrBusy, held, err)
}

// renameRaw renames tmp over final, the message's file in codec c, and
// removeRaw removes path, its file in codec c, as part of b (nil: on its
// own; a missing file is not an error), both through fileOp: the caller
// holds the names lock, and a reader that outlasts the retries makes it
// ErrBusy. A rename that fails leaves final as it was.
func (s *Store) renameRaw(l *rawLock, c RawCodec, tmp, final string) error {
	return s.fileOp(l, c, final, fsretry.Do, func() error { return os.Rename(tmp, final) })
}

func (s *Store) removeRaw(l *rawLock, c RawCodec, path string, b *fsretry.Batch) error {
	retry := fsretry.Do
	if b != nil {
		retry = b.Do
	}
	err := s.fileOp(l, c, path, retry, func() error { return os.Remove(path) })
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// errNotReplaced is in the failure of a write that left the message's
// stored file as it was, because the new file never took its name: the
// write failed before it renamed the new file into place, or that rename
// failed, which leaves the file it would have replaced whole. On Windows a
// reader the store does not count (another process, a virus scanner)
// causes that as well as one of its own (ErrBusy). CommitMessageRaw undoes
// its phase A on it, whatever the cause. A write that fails after the
// rename (the directory's flush) is not one: the new file is in place.
var errNotReplaced = errors.New("store: the stored message file stays as it was")

// notReplaced wraps the failure err of a write that left the stored file
// as it was (errNotReplaced), keeping err's text.
type notReplaced struct{ err error }

func (e notReplaced) Error() string        { return e.err.Error() }
func (e notReplaced) Unwrap() error        { return e.err }
func (e notReplaced) Is(target error) bool { return target == errNotReplaced }

// errRefused is the refusal of refused.
var errRefused = errors.New("store test: the file is open")

// refused is what a test that has the store behave as on Windows
// (Store.refuseOpen) gets for a rename over or a removal of a message's
// file while readers of the store have it open, on any system; nil
// otherwise.
func (s *Store) refused(readers int) error {
	if s.refuseOpen && readers > 0 {
		return errRefused
	}
	return nil
}

// logRemoval logs a file of message id that what could not remove: a
// reader's (ErrBusy) is left to the sweep, anything else is a warning.
func (s *Store) logRemoval(what, id string, err error) {
	if errors.Is(err, ErrBusy) {
		s.log.Info(what+": in use by a reader, left for the sweep", "id", id, "err", err)
		return
	}
	s.log.Warn(what, "id", id, "err", err)
}

// plainRaw is a stored plain message being read (RawMessage).
type plainRaw struct {
	f      *os.File
	closed bool
}

func (p *plainRaw) Read(b []byte) (int, error) { return p.f.Read(b) }

func (p *plainRaw) Size() (int64, error) {
	info, err := p.f.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat message file: %w", err)
	}
	return info.Size(), nil
}

func (p *plainRaw) Rewind() error {
	if _, err := p.f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind message file: %w", err)
	}
	return nil
}

// Close closes the file; closing twice is harmless.
func (p *plainRaw) Close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	return p.f.Close()
}

// WithMessageRaw runs fn while holding the message's write lock, waiting
// for its other writers: the way to read a message and replace it with a
// changed version (RawTx.Replace) without another writer in between. The
// transaction's methods fail once fn has returned.
func (s *Store) WithMessageRaw(ctx context.Context, accountID, id string, fn func(tx *RawTx) error) error {
	if err := checkMessagePath(accountID, id); err != nil {
		return fmt.Errorf("message file: %w", err)
	}
	h, err := s.lockRaw(ctx, accountID, id)
	if err != nil {
		return err
	}
	tx := &RawTx{s: s, ctx: ctx, h: h}
	defer func() {
		tx.done = true
		h.unlock()
	}()
	return fn(tx)
}

// RawTx is a message's raw file under its write lock (WithMessageRaw).
type RawTx struct {
	s    *Store
	ctx  context.Context
	h    *rawHold
	done bool
}

var errRawTxDone = errors.New("store: raw file transaction used after its end")

// Open opens the message's raw file for reading, as OpenMessageRaw.
func (tx *RawTx) Open() (RawMessage, error) {
	if tx.done {
		return nil, errRawTxDone
	}
	return tx.s.openRaw(tx.h.key, tx.h.l, tx.h.dir, tx.h.id)
}

// Stat describes the message's raw file; false when it has none. A message
// left with both variants (a crash between a rename and a removal) is
// settled first, keeping the newest valid file.
func (tx *RawTx) Stat() (RawInfo, bool, error) {
	if tx.done {
		return RawInfo{}, false, errRawTxDone
	}
	files, err := statRaw(tx.h.dir, tx.h.id)
	if err != nil {
		return RawInfo{}, false, err
	}
	if files.both() {
		if files, err = tx.s.resolveLocked(tx.ctx, tx.h, files); err != nil {
			return RawInfo{}, false, err
		}
	}
	if !files.any() {
		return RawInfo{}, false, nil
	}
	info, err := files.info(tx.h.dir, tx.h.id)
	return info, err == nil, err
}

// Replace writes the message anew from src, a *Staged file or a
// RawProducer, in the store's codec, as w says (Plain is refused), and
// replaces its file in either codec. It refuses a message without a row
// (ErrNotFound) and an outbox message (ErrOutbox). Replacing a file
// flushes the new one and the directory before the old one goes; a
// message's first file (a download) is not flushed, and only it may create
// the account's directory. A plain *Staged file is renamed into place
// rather than copied, and is used up; one staged in memory is copied.
func (tx *RawTx) Replace(w RawWrite, src RawSource) (RawInfo, error) {
	if tx.done {
		return RawInfo{}, errRawTxDone
	}
	if w.Plain {
		return RawInfo{}, fmt.Errorf("replace message file: always in the store's codec")
	}
	limit, err := w.bounds()
	if err != nil {
		return RawInfo{}, err
	}
	s, h := tx.s, tx.h
	var role string
	err = s.db.QueryRowContext(tx.ctx, `SELECT f.role FROM messages m JOIN folders f ON f.id = m.folder_id
		WHERE m.id = ? AND m.account_id = ?`, h.id, h.accountID).Scan(&role)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return RawInfo{}, ErrNotFound
	case err != nil:
		return RawInfo{}, fmt.Errorf("replace message file: %w", err)
	case role == string(api.RoleOutbox):
		return RawInfo{}, ErrOutbox
	}
	job := rawJob{codec: s.RawCodec(), limit: limit, size: w.Size, requireRow: w.RequireRow}
	switch src := src.(type) {
	case *Staged:
		if src == nil {
			return RawInfo{}, fmt.Errorf("replace message file: nil source")
		}
		job.staged = src
	case RawProducer:
		if src == nil {
			return RawInfo{}, fmt.Errorf("replace message file: nil source")
		}
		job.produce = src
	default:
		return RawInfo{}, fmt.Errorf("replace message file: source %T", src)
	}
	existing, err := statRaw(h.dir, h.id)
	if err != nil {
		return RawInfo{}, err
	}
	created := false
	if existing.any() {
		job.syncFile, job.syncDir = true, true
	} else if _, err := os.Lstat(h.dir); errors.Is(err, fs.ErrNotExist) {
		// The message's first file may be its account's first. The row was
		// there a moment ago, so the account was too; an account deleted
		// since is caught below.
		if err := os.MkdirAll(h.dir, 0o700); err != nil {
			return RawInfo{}, noSpace(fmt.Errorf("create message directory: %w", err))
		}
		created = true
	}
	info, err := s.writeLocked(tx.ctx, h, job)
	if err == nil && created {
		if ok, _ := s.messageExists(context.WithoutCancel(tx.ctx), h.accountID, h.id); !ok {
			// DeleteAccount removed the directory in between; do not
			// leave it behind.
			s.unlinkRaw(h.l, h.dir, h.id, nil)
			os.Remove(h.dir)
			return RawInfo{}, ErrNotFound
		}
	}
	return info, err
}

// rawJob is one write of a message's raw file (writeLocked).
type rawJob struct {
	codec      RawCodec
	fixed      bool // the codec is the caller's (Plain), not the store's
	limit      int64
	size       int64 // exact length, 0 = unknown
	produce    func(io.Writer) error
	staged     *Staged // instead of produce
	syncFile   bool    // flush the new file
	syncDir    bool    // flush the directory after the rename
	requireRow bool
}

// writeLocked runs a write under the message's write lock: the new file is
// written beside the old, checked, and renamed into place, the other
// variant removed and the accounting updated. A codec change during the
// write converts the new file at once, since the background conversion
// may already have passed the message. A failure before the new file is
// in place leaves the stored one as it was (errNotReplaced).
func (s *Store) writeLocked(ctx context.Context, h *rawHold, j rawJob) (RawInfo, error) {
	tmp, info, err := s.produceTemp(ctx, h, j)
	if err != nil {
		return RawInfo{}, notReplaced{noSpace(err)}
	}
	if err := ctx.Err(); err != nil {
		os.Remove(tmp)
		return RawInfo{}, notReplaced{err}
	}
	if j.requireRow {
		ok, err := s.messageExists(ctx, h.accountID, h.id)
		if err != nil || !ok {
			os.Remove(tmp)
			if err != nil {
				return RawInfo{}, err
			}
			s.unlinkRaw(h.l, h.dir, h.id, nil)
			return RawInfo{}, ErrNotFound
		}
	}
	if err := s.placeLocked(h, tmp, info.Codec, j.syncDir, false); err != nil {
		return RawInfo{}, noSpace(err)
	}
	s.recordRaw(ctx, h.id, info)
	if !j.fixed {
		if now := s.RawCodec(); now != info.Codec {
			converted, err := s.convertLocked(ctx, h, info.Codec, now, false)
			if err != nil {
				s.log.Warn("convert message file after a codec change", "id", h.id, "err", err)
			} else {
				info = converted
			}
		}
	}
	return info, nil
}

// produceTemp writes the new file next to its final name and returns the
// temporary path and what it holds. A compressed file of unknown size is
// spooled plain first, since the frame records the size; the store's codec
// is read again once the spool is complete, so a change meanwhile counts.
func (s *Store) produceTemp(ctx context.Context, h *rawHold, j rawJob) (string, RawInfo, error) {
	switch {
	case j.staged != nil:
		return s.tempFromStaged(h, j)
	case j.codec == RawPlain || j.size > 0:
		return s.writeTemp(h, j.codec, j.size, j.limit, j.produce, j.syncFile)
	}
	spool, info, err := s.writeTemp(h, RawPlain, 0, j.limit, j.produce, false)
	if err != nil {
		return "", RawInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		os.Remove(spool)
		return "", RawInfo{}, err
	}
	if s.RawCodec() == RawPlain {
		if j.syncFile {
			if err := syncFile(spool); err != nil {
				os.Remove(spool)
				return "", RawInfo{}, err
			}
		}
		return spool, info, nil
	}
	tmp, info, err := s.writeTemp(h, RawZstd, info.Bytes, info.Bytes, copyFile(spool), j.syncFile)
	os.Remove(spool)
	return tmp, info, err
}

// tempFromStaged turns a staged file into the temporary file of the write:
// compressed from it, or, plain, the staged file itself moved over. A
// message staged in memory is written out in either codec.
func (s *Store) tempFromStaged(h *rawHold, j rawJob) (string, RawInfo, error) {
	st := j.staged
	if err := st.usable(); err != nil {
		return "", RawInfo{}, err
	}
	n := st.Size()
	switch {
	case n > j.limit:
		return "", RawInfo{}, ErrTooBig
	case j.size > 0 && j.size != n:
		return "", RawInfo{}, fmt.Errorf("%w: staged %d bytes, %d declared", errSizeMismatch, n, j.size)
	}
	fromStaged := func(w io.Writer) error {
		_, err := io.Copy(w, st.Reader())
		return err
	}
	if j.codec == RawZstd || st.inMemory {
		return s.writeTemp(h, j.codec, n, n, fromStaged, j.syncFile)
	}
	if j.syncFile {
		if err := st.f.Sync(); err != nil {
			return "", RawInfo{}, fmt.Errorf("flush staged message: %w", err)
		}
	}
	tmp := filepath.Join(h.dir, h.id+tmpSuffix)
	_ = os.Remove(tmp)
	// The staged file's own handle goes first: Windows refuses to rename a
	// file that is open, even by the process renaming it.
	if err := st.closeFile(); err != nil {
		return "", RawInfo{}, err
	}
	if err := os.Rename(st.path, tmp); err != nil {
		// The message directory is on another file system than the
		// staging area (a linked directory): copy instead.
		if err := st.reopen(); err != nil {
			return "", RawInfo{}, err
		}
		return s.writeTemp(h, RawPlain, n, n, fromStaged, j.syncFile)
	}
	st.consumed = true
	// The file keeps the time it was received; it takes the time it is
	// placed, since a reader and the sweep take the newer of a message's
	// two files (openNewest, resolveLocked) and a variant written while it
	// downloaded (the codec step) must not pass for the newer one. Best
	// effort: a file that keeps its old time is what a copy would not be.
	now := time.Now()
	_ = os.Chtimes(tmp, now, now)
	return tmp, RawInfo{Codec: RawPlain, Bytes: n, DiskBytes: n}, nil
}

// writeTemp writes what produce streams into the temporary file of codec
// (<id>.tmp, <id>.zst.tmp): at most limit bytes, and exactly size when
// size > 0; a compressed file always has an exact size (0 = empty) and is
// decoded and checked before it counts as written. sync flushes it.
func (s *Store) writeTemp(h *rawHold, codec RawCodec, size, limit int64, produce func(io.Writer) error, sync bool) (string, RawInfo, error) {
	tmp := filepath.Join(h.dir, rawName(h.id, codec)+tmpSuffix)
	// A crashed earlier attempt may have left the temporary file behind;
	// O_EXCL then only guards against a concurrent writer.
	_ = os.Remove(tmp)
	f, err := s.createRawFile(tmp)
	if err != nil {
		return "", RawInfo{}, fmt.Errorf("create message file: %w", err)
	}
	info, err := fillTemp(f, codec, size, limit, produce, sync)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("close message file: %w", cerr)
	}
	if err != nil {
		os.Remove(tmp)
		return "", RawInfo{}, err
	}
	return tmp, info, nil
}

func fillTemp(f rawFile, codec RawCodec, size, limit int64, produce func(io.Writer) error, sync bool) (RawInfo, error) {
	info := RawInfo{Codec: codec}
	switch codec {
	case RawPlain:
		lw := &limitedWriter{w: f, limit: limit}
		if size > 0 {
			lw.limit, lw.over = size, errSizeMismatch
		}
		if err := lw.result(produce(lw), size); err != nil {
			return info, err
		}
		info.Bytes, info.DiskBytes = lw.n, lw.n
	case RawZstd:
		cw := &countWriter{w: f}
		n, err := writeZstdFrame(cw, size, produce)
		if err != nil {
			if cw.err != nil {
				return info, cw.err // the file's own error (a full disk) explains best
			}
			return info, err
		}
		if err := verifyZstd(f, cw.n, n); err != nil {
			return info, err
		}
		info.Bytes, info.DiskBytes = n, cw.n
	default:
		return info, fmt.Errorf("write message file: codec %s", codec)
	}
	if sync {
		if err := f.Sync(); err != nil {
			return info, fmt.Errorf("flush message file: %w", err)
		}
	}
	return info, nil
}

// placeLocked renames the finished temporary file over the message's name
// for codec and removes the other variant, flushing the directory first so
// that a crash cannot lose both; syncDir flushes it even when nothing else
// goes. keepOther leaves the other variant (the conversion's first phase).
// A reader choosing a file waits for all of it. A reader that has the old
// file open holds the rename up on Windows: renameRaw waits for it, and
// when it outlasts the wait the write fails with ErrBusy. A failed rename,
// whatever the cause, leaves the old file as it was (errNotReplaced).
func (s *Store) placeLocked(h *rawHold, tmp string, codec RawCodec, syncDir, keepOther bool) error {
	final := filepath.Join(h.dir, rawName(h.id, codec))
	other := filepath.Join(h.dir, rawName(h.id, codec.other()))
	h.l.names.Lock()
	defer h.l.names.Unlock()
	if err := s.renameRaw(h.l, codec, tmp, final); err != nil {
		os.Remove(tmp)
		return notReplaced{fmt.Errorf("finalise message file: %w", err)}
	}
	otherExists := false
	if !keepOther {
		if _, err := os.Lstat(other); err == nil {
			otherExists = true
		}
	}
	if otherExists || syncDir {
		if err := syncDirectory(h.dir); err != nil {
			// The new file is in place and the old one, if any, stays
			// beside it for the sweep; the write is not reported done.
			return err
		}
	}
	if otherExists {
		if err := s.removeRaw(h.l, codec.other(), other, nil); err != nil {
			s.logRemoval("remove replaced message file", h.id, err)
		}
	}
	return nil
}

// convertLocked rewrites the message's file from codec from into codec to
// under the held lock: written with its known size, checked, flushed and
// renamed into place beside the source. Unless keepSource, the source then
// goes (after the directory is flushed) and the accounting follows; with
// keepSource the caller finishes (ConvertRawBatch's second phase). A
// damaged source is ErrRawCorrupt and stays as it is.
func (s *Store) convertLocked(ctx context.Context, h *rawHold, from, to RawCodec, keepSource bool) (RawInfo, error) {
	src := filepath.Join(h.dir, rawName(h.id, from))
	f, err := os.Open(src)
	if err != nil {
		return RawInfo{}, fmt.Errorf("open message file: %w", err)
	}
	var r RawMessage = &plainRaw{f: f}
	if from == RawZstd {
		if r, err = openZstdRaw(f); err != nil {
			return RawInfo{}, err
		}
	}
	defer r.Close()
	size, err := r.Size()
	switch {
	case err != nil:
		return RawInfo{}, err
	case size > MaxRawBytes:
		return RawInfo{}, corrupt(fmt.Sprintf("%d bytes, over the cap", size), nil)
	}
	tmp, info, err := s.writeTemp(h, to, size, size, func(w io.Writer) error {
		_, err := io.Copy(w, r)
		return err
	}, true)
	// The source is read in full: closed before placeLocked removes it
	// (unless keepSource), which Windows refuses while it is open, even by
	// the process removing it.
	r.Close()
	if err != nil {
		if errors.Is(err, ErrRawCorrupt) {
			return RawInfo{}, err
		}
		return RawInfo{}, noSpace(err)
	}
	if err := s.placeLocked(h, tmp, to, false, keepSource); err != nil {
		return RawInfo{}, noSpace(err)
	}
	if !keepSource {
		s.recordRaw(ctx, h.id, info)
	}
	return info, nil
}

// resolveLocked settles a message that has both files (a conversion
// between its phases, or a crash between a rename and a removal): the
// newer stays unless it is a damaged .zst, the other goes, and the
// accounting follows the survivor, which it returns. Equal times keep the
// store codec's file.
func (s *Store) resolveLocked(ctx context.Context, h *rawHold, files rawFiles) (rawFiles, error) {
	keep := s.RawCodec()
	switch zstPath := filepath.Join(h.dir, rawName(h.id, RawZstd)); {
	case !zstIntact(zstPath, files.zst.Size()):
		keep = RawPlain
	case files.zst.ModTime().After(files.plain.ModTime()):
		keep = RawZstd
	case files.plain.ModTime().After(files.zst.ModTime()):
		keep = RawPlain
	}
	h.l.names.Lock()
	err := syncDirectory(h.dir)
	if err == nil {
		err = s.removeRaw(h.l, keep.other(), filepath.Join(h.dir, rawName(h.id, keep.other())), nil)
	}
	h.l.names.Unlock()
	if err != nil {
		return files, fmt.Errorf("settle message files: %w", err)
	}
	out := rawFiles{}
	if keep == RawZstd {
		out.zst = files.zst
	} else {
		out.plain = files.plain
	}
	if info, err := out.info(h.dir, h.id); err == nil {
		s.recordRaw(ctx, h.id, info)
	}
	return out, nil
}

// zstIntact reports whether the .zst file at path decodes whole.
func zstIntact(path string, diskSize int64) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	size, err := zstdFrameSize(f)
	if err != nil {
		return false
	}
	max := int64(MaxRawBytes)
	if size >= 0 {
		max = size
	}
	n, err := decodeCount(f, diskSize, max)
	return err == nil && (size < 0 || n == size)
}

// rawFiles is what exists of a message's two variants (regular files only).
type rawFiles struct{ plain, zst fs.FileInfo }

func (f rawFiles) any() bool  { return f.plain != nil || f.zst != nil }
func (f rawFiles) both() bool { return f.plain != nil && f.zst != nil }

// info describes the file of a message that has exactly one; a .zst file's
// content length is what its frame records.
func (f rawFiles) info(dir, id string) (RawInfo, error) {
	if f.zst == nil {
		return RawInfo{Codec: RawPlain, Bytes: f.plain.Size(), DiskBytes: f.plain.Size()}, nil
	}
	file, err := os.Open(filepath.Join(dir, rawName(id, RawZstd)))
	if err != nil {
		return RawInfo{}, fmt.Errorf("open message file: %w", err)
	}
	z, err := openZstdRaw(file)
	if err != nil {
		return RawInfo{}, err
	}
	defer z.Close()
	n, err := z.Size()
	if err != nil {
		return RawInfo{}, err
	}
	return RawInfo{Codec: RawZstd, Bytes: n, DiskBytes: f.zst.Size()}, nil
}

// statRaw finds which of a message's variants exist.
func statRaw(dir, id string) (rawFiles, error) {
	var out rawFiles
	for _, c := range []RawCodec{RawPlain, RawZstd} {
		info, err := os.Lstat(filepath.Join(dir, rawName(id, c)))
		switch {
		case err == nil:
			if !info.Mode().IsRegular() {
				continue
			}
			if c == RawZstd {
				out.zst = info
			} else {
				out.plain = info
			}
		case errors.Is(err, fs.ErrNotExist):
		default:
			return out, fmt.Errorf("stat message file: %w", err)
		}
	}
	return out, nil
}

// recordRaw updates the message's accounting row after its file changed.
// It is best effort, and not cancelled with the write that just finished:
// the file is in place whatever happens here, and the sweep repairs a
// missing or wrong row. A message without a row gets none.
func (s *Store) recordRaw(ctx context.Context, id string, info RawInfo) {
	_, err := s.db.ExecContext(context.WithoutCancel(ctx), `
		INSERT INTO message_files (message_id, codec, bytes, disk_bytes)
		SELECT ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM messages WHERE id = ?)
		ON CONFLICT (message_id) DO UPDATE SET
			codec = excluded.codec, bytes = excluded.bytes, disk_bytes = excluded.disk_bytes`,
		id, info.Codec.String(), info.Bytes, info.DiskBytes, id)
	if err != nil {
		s.log.Warn("record message file", "id", id, "err", err)
	}
}

// messageExists reports whether the account still has the message's row.
func (s *Store) messageExists(ctx context.Context, accountID, id string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM messages WHERE id = ? AND account_id = ?`, id, accountID).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("look up message: %w", err)
	}
	return true, nil
}

// limitedWriter counts what it passes on and fails as soon as a write would
// take the total past limit (nothing of that write is stored), with over or
// else ErrTooBig. exceeded and werr, the first error of the writer beneath,
// stay set even if the producer ignores the error, so a producer that
// swallows it cannot store a truncated message.
type limitedWriter struct {
	w        io.Writer
	n, limit int64
	over     error
	exceeded bool
	werr     error
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.werr != nil {
		return 0, l.werr
	}
	if l.exceeded || l.n+int64(len(p)) > l.limit {
		l.exceeded = true
		if l.over != nil {
			return 0, l.over
		}
		return 0, ErrTooBig
	}
	n, err := l.w.Write(p)
	l.n += int64(n)
	if err != nil {
		l.werr = err
	}
	return n, err
}

// result is the outcome of a write through l whose producer returned err:
// a write past the limit wins (whatever the producer made of it), then a
// failure beneath, then the producer's own error, then a count other than
// want (0 = any count).
func (l *limitedWriter) result(err error, want int64) error {
	switch {
	case l.exceeded && l.over != nil:
		return fmt.Errorf("%w: more than %d bytes", l.over, l.limit)
	case l.exceeded || errors.Is(err, ErrTooBig):
		return ErrTooBig
	case l.werr != nil:
		return l.werr
	case err != nil:
		return fmt.Errorf("write message: %w", err)
	case want > 0 && l.n != want:
		return fmt.Errorf("%w: %d bytes, %d declared", errSizeMismatch, l.n, want)
	}
	return nil
}

// countWriter counts the bytes it passes on and keeps the first error.
type countWriter struct {
	w   io.Writer
	n   int64
	err error
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if err != nil && c.err == nil {
		c.err = err
	}
	return n, err
}

// copyFile is a producer that streams the file at path.
func copyFile(path string) func(io.Writer) error {
	return func(w io.Writer) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	}
}

// rawFile is a file the raw writers create: an *os.File, or in tests one
// that runs out of space.
type rawFile interface {
	io.Writer
	io.ReaderAt
	Sync() error
	Close() error
}

// createRawFile creates a new private file for reading and writing; it
// fails if the file exists.
func (s *Store) createRawFile(path string) (rawFile, error) {
	if s.createFile != nil {
		return s.createFile(path)
	}
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
}

// syncFile flushes the file at path to disk. The handle is opened for
// writing, though nothing is written: Windows flushes only through a
// handle that may write (FlushFileBuffers), and denies it to a read-only
// one.
func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("flush message file: %w", err)
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("flush message file: %w", err)
	}
	return nil
}

// syncDirectory flushes a directory, making the renames and removals in it
// durable. A system that refuses to flush a directory at all (Windows
// denies it) has nothing better to offer, and that is not an error.
func syncDirectory(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("flush message directory: %w", err)
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	if err != nil && !errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("flush message directory: %w", err)
	}
	return nil
}

// noSpace marks a failure caused by a full disk or quota as ErrNoSpace,
// keeping the system error wrapped too.
func noSpace(err error) error {
	if err == nil || errors.Is(err, ErrNoSpace) {
		return err
	}
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) {
		return fmt.Errorf("%w: %w", ErrNoSpace, err)
	}
	return err
}

// checkPathSegment rejects ids that could escape their directory. Ids are
// generated here, account ids may come from config.toml.
func checkPathSegment(seg string) error {
	if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, "/\x00") {
		return fmt.Errorf("invalid path segment %q", seg)
	}
	return nil
}

// checkMessagePath checks an account id and a message id for use as file
// names. A message id may not end in a suffix of the raw files' own, so
// that no id's file can be taken for another's.
func checkMessagePath(accountID, id string) error {
	if err := checkPathSegment(accountID); err != nil {
		return err
	}
	if err := checkPathSegment(id); err != nil {
		return err
	}
	if strings.HasSuffix(id, RawZstSuffix) || strings.HasSuffix(id, tmpSuffix) {
		return fmt.Errorf("invalid message id %q", id)
	}
	return nil
}

// rawLock orders the writers and readers of one message's files.
type rawLock struct {
	mutate  chan struct{} // holds one token while a writer changes the files
	names   sync.RWMutex  // write-held around renames and removals, read-held while a reader picks a name
	refs    int           // under Store.rawMu
	readers [2]int        // under Store.rawMu: open readers of the file of each codec (openRaw)
	doomed  bool          // under Store.rawMu: the row is gone; the holder removes the files
}

// rawHold is a held write lock of one message.
type rawHold struct {
	s             *Store
	key           string
	l             *rawLock
	accountID, id string
	dir           string
}

func rawKey(accountID, id string) string { return accountID + "/" + id }

// rawEntry returns the lock of a message, counting the caller in.
func (s *Store) rawEntry(key string) *rawLock {
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	return s.rawEntryLocked(key)
}

func (s *Store) rawEntryLocked(key string) *rawLock {
	if s.rawLocks == nil {
		s.rawLocks = map[string]*rawLock{}
	}
	l := s.rawLocks[key]
	if l == nil {
		l = &rawLock{mutate: make(chan struct{}, 1)}
		s.rawLocks[key] = l
	}
	l.refs++
	return l
}

// rawRelease counts the caller out of a message's lock; the last one out
// drops it.
func (s *Store) rawRelease(key string, l *rawLock) {
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	l.refs--
	if l.refs == 0 {
		delete(s.rawLocks, key)
	}
}

func (s *Store) newHold(key string, l *rawLock, accountID, id string) *rawHold {
	return &rawHold{s: s, key: key, l: l, accountID: accountID, id: id, dir: s.accountDir(accountID)}
}

// lockRaw takes a message's write lock, waiting for its current holder or
// until ctx ends.
func (s *Store) lockRaw(ctx context.Context, accountID, id string) (*rawHold, error) {
	key := rawKey(accountID, id)
	l := s.rawEntry(key)
	select {
	case l.mutate <- struct{}{}:
		return s.newHold(key, l, accountID, id), nil
	case <-ctx.Done():
		s.rawRelease(key, l)
		return nil, ctx.Err()
	}
}

// tryLockRaw takes a message's write lock only if it is free.
func (s *Store) tryLockRaw(accountID, id string) (*rawHold, bool) {
	key := rawKey(accountID, id)
	l := s.rawEntry(key)
	select {
	case l.mutate <- struct{}{}:
		return s.newHold(key, l, accountID, id), true
	default:
		s.rawRelease(key, l)
		return nil, false
	}
}

// unlock lets the message go. When a deletion marked it doomed meanwhile,
// both of its files go first; deciding that under rawMu, where the
// deletion looks, means one of the two always removes them.
func (h *rawHold) unlock() {
	s := h.s
	s.rawMu.Lock()
	doomed := h.l.doomed
	if !doomed {
		<-h.l.mutate
	}
	s.rawMu.Unlock()
	if doomed {
		s.unlinkRaw(h.l, h.dir, h.id, nil)
		<-h.l.mutate
	}
	s.rawRelease(h.key, h.l)
}

// unlinkRaw removes both variants of a message; a missing file is fine. The
// removals wait out a reader that has a file open (removeRaw) as part of
// b, the batch of removals they belong to (nil: a batch of their own). A
// file that stays is the sweep's, as an orphan; the error says why
// (ErrBusy: a reader held it).
func (s *Store) unlinkRaw(l *rawLock, dir, id string, b *fsretry.Batch) error {
	if b == nil {
		b = new(fsretry.Batch)
	}
	l.names.Lock()
	defer l.names.Unlock()
	var errs []error
	for _, c := range []RawCodec{RawPlain, RawZstd} {
		if err := s.removeRaw(l, c, filepath.Join(dir, rawName(id, c)), b); err != nil {
			s.logRemoval("remove message file", id, err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// messageFile locates one raw message file.
type messageFile struct{ accountID, id string }

// removeMessageFiles unlinks raw files, both variants, after their rows
// are gone; a missing file is not an error (the row is authoritative). A
// message a writer holds is left to that writer (doomed, see unlock). The
// removals share an fsretry.Batch: once one fails even after the retries,
// which points at something lasting such as a directory without write
// permission rather than at a reader, the rest get a single attempt each,
// so that a folder of many messages does not wait for every one.
func (s *Store) removeMessageFiles(files []messageFile) {
	var b fsretry.Batch
	for _, mf := range files {
		if checkMessagePath(mf.accountID, mf.id) != nil {
			continue
		}
		key := rawKey(mf.accountID, mf.id)
		s.rawMu.Lock()
		l := s.rawEntryLocked(key)
		held := false
		select {
		case l.mutate <- struct{}{}:
			held = true
		default:
			l.doomed = true
		}
		s.rawMu.Unlock()
		if !held {
			s.rawRelease(key, l)
			continue
		}
		h := s.newHold(key, l, mf.accountID, mf.id)
		s.unlinkRaw(l, h.dir, mf.id, &b)
		h.unlock()
	}
}

// removeMessageDir drops the whole raw-message directory of an account
// DeleteAccount deleted, which recorded it for the sweep in the same
// transaction (metaDeletedDir); once it is gone, the record goes too.
// Background writers never create one (RawTx.Replace makes it only for a
// message's first file and removes it again if the row is gone), so none
// comes back. A reader that still has a file open holds the removal up on
// Windows (removeAccountDir); what one keeps for longer stays, and the
// sweep removes it once the reader is done.
func (s *Store) removeMessageDir(ctx context.Context, accountID string) {
	if checkPathSegment(accountID) != nil {
		return
	}
	if err := s.removeAccountDir(accountID); err != nil {
		if s.accountRead(accountID) {
			s.log.Info("message directory in use by a reader: left for the sweep", "account", accountID, "err", err)
		} else {
			s.log.Warn("remove message directory; the sweep tries again", "account", accountID, "err", err)
		}
		return
	}
	if err := s.forgetDeletedDir(context.WithoutCancel(ctx), accountID); err != nil {
		s.log.Warn("forget removed message directory", "account", accountID, "err", err)
	}
}

// removeAccountDir removes the directory of an account's raw files and all
// in it: an attempt removes what it can, the next (fsretry) what a reader
// held up on Windows. Under Store.refuseOpen it refuses, as that reader
// would, while one of the store's readers has a file of the account open.
func (s *Store) removeAccountDir(accountID string) error {
	dir := s.accountDir(accountID)
	return fsretry.Do(func() error {
		if s.refuseOpen && s.accountRead(accountID) {
			return errRefused
		}
		return os.RemoveAll(dir)
	})
}

// accountRead reports whether one of the store's readers has a file of the
// account open.
func (s *Store) accountRead(accountID string) bool {
	prefix := rawKey(accountID, "")
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	for key, l := range s.rawLocks {
		if strings.HasPrefix(key, prefix) && l.readers[RawPlain]+l.readers[RawZstd] > 0 {
			return true
		}
	}
	return false
}

// metaDeletedDir prefixes the meta keys of the accounts DeleteAccount
// deleted whose directory may still hold files (removeMessageDir), by
// account id: the sweep removes those directories whole, which it never
// does for an unknown account's, and forgets them once they are gone.
const metaDeletedDir = "raw.deleted."

// deletedDirs lists the accounts recorded under metaDeletedDir.
func (s *Store) deletedDirs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT substr(key, ?) FROM meta WHERE substr(key, 1, ?) = ?`,
		len(metaDeletedDir)+1, len(metaDeletedDir), metaDeletedDir)
	if err != nil {
		return nil, fmt.Errorf("list deleted message directories: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var acc string
		if err := rows.Scan(&acc); err != nil {
			return nil, fmt.Errorf("list deleted message directories: %w", err)
		}
		out[acc] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list deleted message directories: %w", err)
	}
	return out, nil
}

// forgetDeletedDir drops the account's metaDeletedDir record.
func (s *Store) forgetDeletedDir(ctx context.Context, accountID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM meta WHERE key = ?`, metaDeletedDir+accountID); err != nil {
		return fmt.Errorf("forget deleted message directory: %w", err)
	}
	return nil
}
