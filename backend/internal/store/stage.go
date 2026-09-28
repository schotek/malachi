// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// The staging area: a message is received into a file of its own under
// <data dir>/staging before anything decides what to keep of it (the
// attachments kept on the server, internal/ingest) and before it is
// committed (RawTx.Replace, CommitMessageRaw). A staged file is private
// to its creator; the daemon removes whatever is left there when it opens
// the store (a staged file never outlives its process) and the sweep
// removes stale ones. A message that must not reach the disk whole
// (Preferences.NeverStoreAttachments) is staged in memory instead
// (StageMemory): the same Staged, whose commit writes the message's own
// file from memory.

// Staged is a message received into the staging area: it takes the bytes
// (Write, ReadFrom) up to its limit, reads them back from the start as
// often as needed (Reader), and is removed by Remove, which is always to
// be called. Committing a plain one renames it into the message's place,
// after which it only closes; one staged in memory is copied. It is not
// safe for concurrent use.
type Staged struct {
	ctx      context.Context
	f        rawFile // nil when staged in memory
	mem      []byte  // the bytes when staged in memory
	inMemory bool
	path     string
	limit    int64
	n        int64
	exceeded bool
	werr     error
	consumed bool // renamed into a message's place
	removed  bool
}

var errStagedDone = errors.New("store: staged message already committed or removed")

// StageRaw creates an empty staged message of at most limit bytes (<= 0
// or over MaxRawBytes: MaxRawBytes): a 0600 file with a random name,
// created exclusively, in the 0700 staging directory. ctx bounds ReadFrom.
func (s *Store) StageRaw(ctx context.Context, limit int64) (*Staged, error) {
	if limit <= 0 || limit > MaxRawBytes {
		limit = MaxRawBytes
	}
	dir := s.stagingDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, noSpace(fmt.Errorf("create staging directory: %w", err))
	}
	for attempt := 0; ; attempt++ {
		path := filepath.Join(dir, newID("st_"))
		f, err := s.createRawFile(path)
		if err == nil {
			return &Staged{ctx: ctx, f: f, path: path, limit: limit}, nil
		}
		if !errors.Is(err, fs.ErrExist) || attempt == 2 {
			return nil, noSpace(fmt.Errorf("create staged message: %w", err))
		}
	}
}

// StageMemory is StageRaw for a message that must not be written to disk
// whole (Preferences.NeverStoreAttachments, internal/ingest): the bytes
// stay in memory, at most limit of them (<= 0 or over MaxRawBytes:
// MaxRawBytes), and nothing is created; only a commit writes a file, the
// message's own, from them. hint is the size the sender announced, if any
// (<= 0: unknown): room for it, within limit, is taken up front, so that
// the bytes are not copied as they grow. ctx bounds ReadFrom.
func (s *Store) StageMemory(ctx context.Context, limit, hint int64) *Staged {
	if limit <= 0 || limit > MaxRawBytes {
		limit = MaxRawBytes
	}
	st := &Staged{ctx: ctx, limit: limit, inMemory: true}
	if hint > 0 {
		st.mem = make([]byte, 0, min(hint, limit))
	}
	return st
}

// Write appends p. A write past the limit fails with ErrTooBig and stores
// nothing of p; like a failure of the file, it sticks, so a staged message
// that did not take everything cannot be committed.
func (st *Staged) Write(p []byte) (int, error) {
	switch {
	case st.consumed || st.removed:
		return 0, errStagedDone
	case st.werr != nil:
		return 0, st.werr
	case st.exceeded || st.n+int64(len(p)) > st.limit:
		st.exceeded = true
		return 0, ErrTooBig
	}
	if st.inMemory {
		st.mem = append(st.mem, p...)
		st.n += int64(len(p))
		return len(p), nil
	}
	n, err := st.f.Write(p)
	st.n += int64(n)
	if err != nil {
		st.werr = noSpace(fmt.Errorf("write staged message: %w", err))
		return n, st.werr
	}
	return n, nil
}

// ReadFrom appends everything r yields until EOF, as Write does, and
// returns how much it took; the staging context ending stops it.
func (st *Staged) ReadFrom(r io.Reader) (int64, error) {
	buf := make([]byte, 64<<10)
	var total int64
	for {
		if err := st.ctx.Err(); err != nil {
			return total, err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			w, err := st.Write(buf[:n])
			total += int64(w)
			if err != nil {
				return total, err
			}
		}
		switch {
		case errors.Is(rerr, io.EOF):
			return total, nil
		case rerr != nil:
			return total, rerr
		}
	}
}

// Reader reads the staged bytes from the first one, independently of other
// readers and of the writes that follow.
func (st *Staged) Reader() *io.SectionReader {
	if st.inMemory {
		return io.NewSectionReader(bytes.NewReader(st.mem[:st.n]), 0, st.n)
	}
	return io.NewSectionReader(st.f, 0, st.n)
}

// Size is how many bytes are staged.
func (st *Staged) Size() int64 { return st.n }

// Bytes are the staged bytes of a message staged in memory (StageMemory),
// shared, not copied: the caller must not change them. nil for one staged
// on disk, and once removed.
func (st *Staged) Bytes() []byte {
	switch {
	case !st.inMemory || st.removed:
		return nil
	case st.mem == nil:
		return []byte{}
	}
	return st.mem[:st.n:st.n]
}

// Remove closes the staged message and deletes its file, unless a commit
// made it a message's file; removing twice is harmless. One staged in
// memory lets go of its bytes (a slice Bytes returned keeps them).
func (st *Staged) Remove() error {
	if st.removed {
		return nil
	}
	st.removed = true
	if st.inMemory {
		st.mem = nil
		return nil
	}
	err := st.f.Close()
	if !st.consumed {
		if rerr := os.Remove(st.path); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			err = errors.Join(err, rerr)
		}
	}
	if err != nil {
		return fmt.Errorf("remove staged message: %w", err)
	}
	return nil
}

func (*Staged) rawSource() {}

// usable says whether the staged message can be committed: it holds all it
// was given and is neither removed nor committed already.
func (st *Staged) usable() error {
	switch {
	case st.consumed || st.removed:
		return errStagedDone
	case st.exceeded:
		return ErrTooBig
	case st.werr != nil:
		return st.werr
	}
	return nil
}

// sweepStaging removes staged files last modified before cutoff (the zero
// time: all of them) and returns how many went.
func (s *Store) sweepStaging(cutoff time.Time) int {
	entries, err := os.ReadDir(s.stagingDir())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("read staging directory", "err", err)
		}
		return 0
	}
	removed := 0
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if !cutoff.IsZero() {
			info, err := e.Info()
			if err != nil || !info.ModTime().Before(cutoff) {
				continue
			}
		}
		if err := os.Remove(filepath.Join(s.stagingDir(), e.Name())); err == nil {
			removed++
		}
	}
	return removed
}
