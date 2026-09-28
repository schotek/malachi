// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// RawStep is a background job the raw maintenance loop (started by
// Maintain) runs to completion over the stored raw messages: the loop keeps
// its cursor in the meta table under "raw.step.<Name>" as "<key>|<cursor>",
// the cursor "done" once finished, paces it, restarts it from the
// beginning when Key changes and stops it on shutdown. Every change to a
// raw file goes through store.WithMessageRaw; long network transfers never
// belong in a step.
type RawStep interface {
	// Name is stable: it is part of the meta key.
	Name() string
	// Key describes the state the step brings the store to now; a change
	// restarts the step. It is cheap and has no effect of its own:
	// system.storage calls it too, concurrently with Batch.
	Key(ctx context.Context, now time.Time) (string, error)
	// Batch does one bounded unit of work after cursor and returns the
	// cursor to continue from; "" means finished, and "done" is never a
	// cursor. After an error the loop keeps the earlier cursor:
	// store.ErrNoSpace stops the loop until a preference changes or the
	// daemon restarts, store.ErrConflict starts the step over (at the
	// start of a pass it counts as a failure), and after a failure the
	// step waits for the next sweep or preference change.
	Batch(ctx context.Context, cursor string) (next string, err error)
}

// AddRawStep registers a step, run after the ones registered before it; nil
// is ignored.
func (b *Backend) AddRawStep(s RawStep) {
	if s != nil {
		b.rawSteps = append(b.rawSteps, s)
	}
}

// The pace of the loop; variables so that tests can speed it up.
var (
	// rawBatchRows is how many raw files one conversion batch deals with.
	rawBatchRows = 64
	// rawMinPause is the shortest pause after a batch; a longer batch is
	// followed by a pause as long as it took, so the loop never takes more
	// than half a core.
	rawMinPause = 20 * time.Millisecond
	// rawRecheck is the period of the sweep of the raw files, after which
	// the steps that failed are tried again.
	rawRecheck = time.Hour
	// rawKeyPoll is how often an idle loop evaluates the keys again: a key
	// may follow the date, and system.storage reports a step whose key
	// moved as running until the loop has looked.
	rawKeyPoll = time.Minute
)

// rawSweepAge spares what a writer may still be at work on from the sweep.
const rawSweepAge = time.Hour

// Step progress in the meta table.
const (
	rawStepMetaPrefix = "raw.step."
	rawStepDone       = "done"
)

func rawStepMeta(s RawStep) string { return rawStepMetaPrefix + s.Name() }

// kickRaw wakes the raw maintenance loop to evaluate the keys again (a
// preference changed); after a full disk it also makes the loop try again.
// It never blocks.
func (b *Backend) kickRaw() {
	select {
	case b.rawKick <- struct{}{}:
	default: // a kick is pending already
	}
}

// restartRawStep makes the loop run a step from the start again, although
// it finished for its current key: work turned up that the key does not
// tell (a message stored under a policy that no longer holds). The
// progress is cleared at once, so that the restart outlives the daemon,
// and once more by the loop before it next reads it (takeRawRestart), so
// that a batch running meanwhile cannot store its own progress over it.
func (b *Backend) restartRawStep(ctx context.Context, name string) {
	b.rawRestartMu.Lock()
	if b.rawRestart == nil {
		b.rawRestart = map[string]bool{}
	}
	b.rawRestart[name] = true
	b.rawRestartMu.Unlock()
	if err := b.store.SetMeta(ctx, rawStepMetaPrefix+name, ""); err != nil {
		b.log.Warn("restart a raw maintenance step", "step", name, "err", err)
	}
	b.kickRaw()
}

// takeRawRestart reports whether a restart of the step was asked for
// since the loop last looked (restartRawStep), and forgets it.
func (b *Backend) takeRawRestart(name string) bool {
	b.rawRestartMu.Lock()
	defer b.rawRestartMu.Unlock()
	if !b.rawRestart[name] {
		return false
	}
	delete(b.rawRestart, name)
	return true
}

// rawStepState reads a step's progress towards key: the cursor to go on
// from ("" = from the start), or done.
func (b *Backend) rawStepState(ctx context.Context, s RawStep, key string) (cursor string, done bool, err error) {
	v, _, err := b.store.GetMeta(ctx, rawStepMeta(s))
	if err != nil {
		return "", false, err
	}
	rest, ok := strings.CutPrefix(v, key+"|")
	if !ok {
		return "", false, nil // another key's, or none: from the start
	}
	if rest == rawStepDone {
		return "", true, nil
	}
	return rest, false, nil
}

// rawConversion is system.storage's conversion state: noSpace while the
// loop waits after a full disk, running while any step has work left for
// its current key, idle otherwise.
func (b *Backend) rawConversion(ctx context.Context) (api.StorageConversion, error) {
	if b.rawNoSpace.Load() {
		return api.StorageConversionNoSpace, nil
	}
	now := time.Now()
	for _, s := range b.rawSteps {
		key, err := s.Key(ctx, now)
		if err != nil {
			return "", err
		}
		_, done, err := b.rawStepState(ctx, s, key)
		if err != nil {
			return "", err
		}
		if !done {
			return api.StorageConversionRunning, nil
		}
	}
	return api.StorageConversionIdle, nil
}

// maintainRaw runs the raw maintenance loop until ctx ends: a sweep of the
// raw files, then every step in order, each paced and continued from its
// cursor until done; then it waits. A kick (kickRaw) evaluates the keys
// again at once, the idle loop does so every rawKeyPoll, and every
// rawRecheck it sweeps again and retries the steps that failed. A full
// disk stops it until a kick.
func (b *Backend) maintainRaw(ctx context.Context) {
	l := &rawLoop{b: b, log: b.log.With("loop", "raw"), failed: map[string]bool{}, passes: map[string]int{}}
	for ctx.Err() == nil {
		if l.sweepDue() {
			l.sweep(ctx)
		}
		switch l.round(ctx) {
		case roundDone:
			l.idle(ctx)
		case roundNoSpace:
			l.waitForSpace(ctx)
		case roundAgain, roundStopped:
		}
	}
}

// rawLoop is the state of maintainRaw, owned by its goroutine.
type rawLoop struct {
	b       *Backend
	log     *slog.Logger
	sweepAt time.Time       // when the next sweep is due
	failed  map[string]bool // steps skipped until the next sweep or kick
	passes  map[string]int  // batches of each step's current pass, for the log
}

type roundResult int

const (
	roundDone    roundResult = iota // every step is done or failed
	roundAgain                      // a kick or the sweep: evaluate again
	roundNoSpace                    // the disk is full
	roundStopped                    // ctx ended
)

// round runs the steps in order, each until it is done, fails or the loop
// is interrupted.
func (l *rawLoop) round(ctx context.Context) roundResult {
	for _, s := range l.b.rawSteps {
		if r, again := l.runStep(ctx, s); again {
			return r
		}
	}
	return roundDone
}

// runStep runs one step's batches. again is true when the round has to
// end: r says why.
func (l *rawLoop) runStep(ctx context.Context, s RawStep) (r roundResult, again bool) {
	name := s.Name()
	for !l.failed[name] {
		if ctx.Err() != nil {
			return roundStopped, true
		}
		if l.sweepDue() {
			return roundAgain, true
		}
		if l.b.takeRawRestart(name) {
			l.save(ctx, s, "")
		}
		key, err := s.Key(ctx, time.Now())
		if err != nil {
			l.fail(ctx, s, "evaluate", err)
			return 0, false
		}
		cursor, done, err := l.b.rawStepState(ctx, s, key)
		if err != nil {
			l.fail(ctx, s, "read the progress of", err)
			return 0, false
		}
		if done {
			return 0, false
		}
		if cursor == "" {
			l.passes[name] = 0
		}
		start := time.Now()
		next, err := s.Batch(ctx, cursor)
		took := time.Since(start)
		switch {
		case ctx.Err() != nil:
			return roundStopped, true
		case errors.Is(err, store.ErrNoSpace):
			l.b.rawNoSpace.Store(true)
			l.log.Warn("stored mail cannot be converted: the disk is full; trying again after a preference change or a restart",
				"step", name, "err", err)
			return roundNoSpace, true
		case errors.Is(err, store.ErrConflict) && cursor != "":
			// What the pass works towards changed under it: start over.
			l.log.Debug("raw maintenance step starts over", "step", name, "err", err)
			l.save(ctx, s, "")
		case err != nil:
			l.fail(ctx, s, "run", err)
		case next == "":
			l.finish(ctx, s, key)
		default:
			l.passes[name]++
			l.save(ctx, s, key+"|"+next)
		}
		if !l.pause(ctx, max(rawMinPause, took)) {
			if ctx.Err() != nil {
				return roundStopped, true
			}
			return roundAgain, true
		}
	}
	return 0, false
}

// finish records that the step is done for key.
func (l *rawLoop) finish(ctx context.Context, s RawStep, key string) {
	name := s.Name()
	if l.passes[name] > 0 {
		l.log.Info("stored mail brought up to date", "step", name, "key", key, "batches", l.passes[name]+1)
	}
	delete(l.passes, name)
	l.save(ctx, s, key+"|"+rawStepDone)
}

// save stores a step's progress; a step whose progress cannot be stored
// waits for the next sweep.
func (l *rawLoop) save(ctx context.Context, s RawStep, state string) {
	if err := l.b.store.SetMeta(ctx, rawStepMeta(s), state); err != nil {
		l.fail(ctx, s, "store the progress of", err)
	}
}

// fail logs a step's error and skips the step until the next sweep or
// kick.
func (l *rawLoop) fail(ctx context.Context, s RawStep, what string, err error) {
	if ctx.Err() == nil {
		l.log.Warn(what+" a raw maintenance step", "step", s.Name(), "err", err)
	}
	l.failed[s.Name()] = true
}

// pause waits d after a batch. It returns false when the round has to end
// early: ctx ended, or a kick asks for the keys again.
func (l *rawLoop) pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-l.b.rawKick:
		l.kicked()
		return false
	case <-t.C:
		return true
	}
}

// idle waits for the next round: a kick, the key poll or the sweep.
func (l *rawLoop) idle(ctx context.Context) {
	t := time.NewTimer(min(rawKeyPoll, time.Until(l.sweepAt)))
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-l.b.rawKick:
		l.kicked()
	case <-t.C:
	}
}

// waitForSpace waits after a full disk until a kick; neither the key poll
// nor the sweep tries again.
func (l *rawLoop) waitForSpace(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-l.b.rawKick:
		l.kicked()
	}
}

// kicked handles a kick: every step may run again, a full disk included.
func (l *rawLoop) kicked() {
	l.b.rawNoSpace.Store(false)
	clear(l.failed)
}

// sweepDue reports whether the sweep is due (at the start, then every
// rawRecheck).
func (l *rawLoop) sweepDue() bool { return !time.Now().Before(l.sweepAt) }

// sweep puts the raw files and their accounting in order
// (store.SweepMessageFiles), lets the failed steps run again and, when
// files are found in the other codec, starts the conversion over.
func (l *rawLoop) sweep(ctx context.Context) {
	l.sweepAt = time.Now().Add(rawRecheck)
	clear(l.failed)
	res, err := l.b.store.SweepMessageFiles(ctx, rawSweepAge)
	if err != nil {
		if ctx.Err() == nil {
			l.log.Warn("sweep the raw message files", "err", err)
		}
		return
	}
	level := slog.LevelDebug
	if res.Temps+res.Staged+res.Orphans+res.Dirs+res.Resolved+res.Fixed+res.Dropped+res.Corrupt > 0 {
		level = slog.LevelInfo
	}
	l.log.Log(ctx, level, "swept the raw message files", "temporary", res.Temps, "staged", res.Staged,
		"orphans", res.Orphans, "directories", res.Dirs, "resolved", res.Resolved, "accounted", res.Backfilled,
		"fixed", res.Fixed, "dropped", res.Dropped, "misplaced", res.Misplaced, "damaged", res.Corrupt, "busy", res.Busy)
	if res.Misplaced == 0 {
		return
	}
	for _, s := range l.b.rawSteps {
		if s.Name() == codecStepName {
			l.save(ctx, s, "")
		}
	}
}

// codecStepName names the conversion of the raw files to the store's
// codec.
const codecStepName = "codec"

// codecStep converts the stored raw files to the codec the store writes
// (Preferences.CompressStore, applyRawCodec), either way; outbox messages
// stay plain. Its cursor is "<codec>:<message id>": a pass keeps its
// target, and when the store's codec changes under it the conversion
// reports store.ErrConflict and the pass starts over.
type codecStep struct {
	st  *store.Store
	log *slog.Logger
}

func newCodecStep(b *Backend) RawStep { return &codecStep{st: b.store, log: b.log} }

func (*codecStep) Name() string { return codecStepName }

// Key is the codec the files are converted to.
func (s *codecStep) Key(context.Context, time.Time) (string, error) {
	return "1:" + s.st.RawCodec().String(), nil
}

func (s *codecStep) Batch(ctx context.Context, cursor string) (string, error) {
	to, after := s.st.RawCodec(), ""
	if name, id, ok := strings.Cut(cursor, ":"); ok {
		for _, c := range []store.RawCodec{store.RawPlain, store.RawZstd} {
			if c.String() == name {
				to, after = c, id
			}
		}
	}
	last, res, err := s.st.ConvertRawBatch(ctx, after, to, rawBatchRows)
	if res.Visited > 0 {
		s.log.Debug("convert raw messages", "codec", to.String(), "visited", res.Visited, "converted", res.Converted,
			"busy", res.Busy, "damaged", res.Corrupt, "missing", res.Missing, "failed", res.Failed)
	}
	if err != nil || last == "" {
		return "", err
	}
	return to.String() + ":" + last, nil
}
