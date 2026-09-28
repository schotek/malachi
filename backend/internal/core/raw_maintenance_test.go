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
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// callLog records the batches of several steps in the order they ran.
type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(s string) {
	l.mu.Lock()
	l.calls = append(l.calls, s)
	l.mu.Unlock()
}

func (l *callLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls)
}

// fakeStep is a scripted RawStep: a pass is n batches with the cursors
// "", "c1", … "c<n-1>"; errs answers a cursor once with an error instead;
// every batch takes at least sleep.
type fakeStep struct {
	name string
	n    int
	log  *callLog

	mu     sync.Mutex
	key    string
	keyErr error
	errs   map[string]error
	sleep  time.Duration
	spans  [][2]time.Time // start and end of every batch
	block  bool           // a batch waits for the end of its context
	ended  atomic.Bool    // a blocked batch has returned
}

func (s *fakeStep) Name() string { return s.name }

func (s *fakeStep) Key(context.Context, time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.key, s.keyErr
}

func (s *fakeStep) setKey(k string) {
	s.mu.Lock()
	s.key = k
	s.mu.Unlock()
}

func (s *fakeStep) Batch(ctx context.Context, cursor string) (string, error) {
	start := time.Now()
	s.mu.Lock()
	err, scripted := s.errs[cursor]
	delete(s.errs, cursor)
	sleep, block := s.sleep, s.block
	s.mu.Unlock()
	if s.log != nil {
		s.log.add(s.name + ":" + cursor)
	}
	if block {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // a batch finishing its last file
		s.ended.Store(true)
		return "", ctx.Err()
	}
	time.Sleep(sleep)
	s.mu.Lock()
	s.spans = append(s.spans, [2]time.Time{start, time.Now()})
	s.mu.Unlock()
	if scripted {
		return "", err
	}
	i := 0
	if cursor != "" {
		i, _ = strconv.Atoi(strings.TrimPrefix(cursor, "c"))
	}
	if i+1 >= s.n {
		return "", nil
	}
	return "c" + strconv.Itoa(i+1), nil
}

// fastRawLoop gives the loop a pace for tests and restores it afterwards
// (after the loop has stopped: cleanups run last first).
func fastRawLoop(t *testing.T, pause, poll, recheck time.Duration) {
	t.Helper()
	oldPause, oldPoll, oldRecheck := rawMinPause, rawKeyPoll, rawRecheck
	rawMinPause, rawKeyPoll, rawRecheck = pause, poll, recheck
	t.Cleanup(func() { rawMinPause, rawKeyPoll, rawRecheck = oldPause, oldPoll, oldRecheck })
}

// startRawLoop runs maintainRaw over steps (nil: the backend's own) and
// returns a function that stops it and waits; the test's cleanup calls it
// too.
func startRawLoop(t *testing.T, b *Backend, steps ...RawStep) func() {
	t.Helper()
	if steps != nil {
		b.rawSteps = steps
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.maintainRaw(ctx)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("the raw maintenance loop did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// eventually waits up to 10 s for cond.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func conversionOf(t *testing.T, b *Backend) api.StorageConversion {
	t.Helper()
	c, err := b.rawConversion(context.Background())
	if err != nil {
		t.Fatalf("conversion state: %v", err)
	}
	return c
}

func waitIdle(t *testing.T, b *Backend) {
	t.Helper()
	eventually(t, "the raw maintenance loop to be idle", func() bool { return conversionOf(t, b) == api.StorageConversionIdle })
}

func metaOf(t *testing.T, b *Backend, key string) string {
	t.Helper()
	v, _, err := b.store.GetMeta(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func expectLog(t *testing.T, l *callLog, want ...string) {
	t.Helper()
	if got := l.get(); !slices.Equal(got, want) {
		t.Fatalf("batches = %q, want %q", got, want)
	}
}

// The steps run in the order they were registered, each to its end, and
// each is recorded as done for its key; an idle loop keeps them done.
func TestRawLoopRunsStepsInOrder(t *testing.T) {
	fastRawLoop(t, time.Millisecond, 5*time.Millisecond, time.Hour)
	b := newTestBackend(t, config.Default())
	log := &callLog{}
	a := &fakeStep{name: "a", n: 3, key: "ka", log: log}
	c := &fakeStep{name: "b", n: 2, key: "kb", log: log}
	b.rawSteps = []RawStep{a, c}
	if got := conversionOf(t, b); got != api.StorageConversionRunning {
		t.Fatalf("before the first run: %s, want running", got)
	}
	startRawLoop(t, b)
	waitIdle(t, b)
	time.Sleep(30 * time.Millisecond) // several key polls
	expectLog(t, log, "a:", "a:c1", "a:c2", "b:", "b:c1")
	if got := metaOf(t, b, "raw.step.a"); got != "ka|done" {
		t.Errorf("progress of a = %q", got)
	}
	if got := metaOf(t, b, "raw.step.b"); got != "kb|done" {
		t.Errorf("progress of b = %q", got)
	}
}

// A step goes on from its stored cursor when its key is the same, and
// starts over when it is another key's.
func TestRawLoopResumesFromCursor(t *testing.T) {
	fastRawLoop(t, time.Millisecond, time.Hour, time.Hour)
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	for k, v := range map[string]string{"raw.step.a": "ka|c2", "raw.step.b": "old|c1"} {
		if err := b.store.SetMeta(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	log := &callLog{}
	startRawLoop(t, b, &fakeStep{name: "a", n: 4, key: "ka", log: log}, &fakeStep{name: "b", n: 2, key: "kb", log: log})
	waitIdle(t, b)
	expectLog(t, log, "a:c2", "a:c3", "b:", "b:c1")
}

// A new key restarts the step: at once after a kick, and without one at
// the next key poll; system.storage reports running from the moment the
// key moved.
func TestRawLoopRestartsOnKeyChange(t *testing.T) {
	for _, kick := range []bool{true, false} {
		t.Run(fmt.Sprintf("kick=%v", kick), func(t *testing.T) {
			poll := time.Hour
			if !kick {
				poll = 10 * time.Millisecond
			}
			fastRawLoop(t, time.Millisecond, poll, time.Hour)
			b := newTestBackend(t, config.Default())
			log := &callLog{}
			a := &fakeStep{name: "a", n: 2, key: "k1", log: log}
			startRawLoop(t, b, a)
			waitIdle(t, b)
			a.setKey("k2")
			if got := conversionOf(t, b); got != api.StorageConversionRunning {
				t.Fatalf("after the key moved: %s, want running", got)
			}
			if kick {
				b.kickRaw()
			}
			waitIdle(t, b)
			expectLog(t, log, "a:", "a:c1", "a:", "a:c1")
			if got := metaOf(t, b, "raw.step.a"); got != "k2|done" {
				t.Fatalf("progress = %q", got)
			}
		})
	}
}

// restartRawStep runs a step done for its key once more from the start,
// also when it is asked for while the last batch of a pass runs, whose
// progress the loop stores after the request.
func TestRawLoopRestartStep(t *testing.T) {
	fastRawLoop(t, time.Millisecond, time.Hour, time.Hour)
	b := newTestBackend(t, config.Default())
	log := &callLog{}
	a := &fakeStep{name: "a", n: 1, key: "k", log: log}
	startRawLoop(t, b, a)
	waitIdle(t, b)
	b.restartRawStep(context.Background(), "a")
	eventually(t, "the second pass", func() bool { return len(log.get()) == 2 })
	waitIdle(t, b)

	a.mu.Lock()
	a.sleep = 200 * time.Millisecond
	a.mu.Unlock()
	b.restartRawStep(context.Background(), "a")
	eventually(t, "the third pass to start", func() bool { return len(log.get()) == 3 })
	b.restartRawStep(context.Background(), "a") // while its only batch runs
	eventually(t, "the fourth pass", func() bool { return len(log.get()) == 4 })
	waitIdle(t, b)
	expectLog(t, log, "a:", "a:", "a:", "a:")
	if got := metaOf(t, b, "raw.step.a"); got != "k|done" {
		t.Fatalf("progress = %q", got)
	}
}

// A full disk stops the loop: noSpace, no retry from the key poll or the
// sweep, until a kick; then the step goes on from its cursor.
func TestRawLoopNoSpace(t *testing.T) {
	fastRawLoop(t, time.Millisecond, 2*time.Millisecond, 10*time.Millisecond)
	b := newTestBackend(t, config.Default())
	log := &callLog{}
	full := fmt.Errorf("write: %w", store.ErrNoSpace)
	a := &fakeStep{name: "a", n: 3, key: "k", log: log, errs: map[string]error{"c1": full}}
	next := &fakeStep{name: "b", n: 1, key: "kb", log: log}
	startRawLoop(t, b, a, next)
	eventually(t, "noSpace", func() bool { return conversionOf(t, b) == api.StorageConversionNoSpace })
	time.Sleep(60 * time.Millisecond) // many key polls and sweeps
	expectLog(t, log, "a:", "a:c1")
	if got := metaOf(t, b, "raw.step.a"); got != "k|c1" {
		t.Fatalf("progress after the full disk = %q, want the cursor before it", got)
	}
	b.kickRaw()
	waitIdle(t, b)
	expectLog(t, log, "a:", "a:c1", "a:c1", "a:c2", "b:")
	if b.rawNoSpace.Load() {
		t.Fatal("noSpace still set")
	}
}

// ErrConflict starts a step over; at the start of a pass it counts as a
// failure, so a step cannot spin on it.
func TestRawLoopConflict(t *testing.T) {
	fastRawLoop(t, time.Millisecond, 2*time.Millisecond, time.Hour)
	b := newTestBackend(t, config.Default())
	log := &callLog{}
	a := &fakeStep{name: "a", n: 3, key: "k", log: log, errs: map[string]error{"c1": store.ErrConflict}}
	startRawLoop(t, b, a)
	waitIdle(t, b)
	expectLog(t, log, "a:", "a:c1", "a:", "a:c1", "a:c2")

	log2 := &callLog{}
	b2 := newTestBackend(t, config.Default())
	a2 := &fakeStep{name: "a", n: 2, key: "k", log: log2, errs: map[string]error{"": fmt.Errorf("x: %w", store.ErrConflict)}}
	startRawLoop(t, b2, a2)
	eventually(t, "the first batch", func() bool { return len(log2.get()) == 1 })
	time.Sleep(30 * time.Millisecond)
	expectLog(t, log2, "a:")
	b2.kickRaw()
	waitIdle(t, b2)
	expectLog(t, log2, "a:", "a:", "a:c1")
}

// Any other error skips the step, the next one still runs, and the failed
// one is tried again after a kick or at the next sweep, not at every key
// poll.
func TestRawLoopFailure(t *testing.T) {
	for _, via := range []string{"kick", "sweep"} {
		t.Run(via, func(t *testing.T) {
			recheck := time.Hour
			if via == "sweep" {
				recheck = 150 * time.Millisecond
			}
			fastRawLoop(t, time.Millisecond, 2*time.Millisecond, recheck)
			b := newTestBackend(t, config.Default())
			log := &callLog{}
			a := &fakeStep{name: "a", n: 2, key: "ka", log: log, errs: map[string]error{"": errors.New("boom")}}
			startRawLoop(t, b, a, &fakeStep{name: "b", n: 1, key: "kb", log: log})
			eventually(t, "both steps tried", func() bool { return len(log.get()) == 2 })
			time.Sleep(30 * time.Millisecond)
			expectLog(t, log, "a:", "b:")
			if got := conversionOf(t, b); got != api.StorageConversionRunning {
				t.Fatalf("with a failed step: %s, want running", got)
			}
			if via == "kick" {
				b.kickRaw()
			}
			waitIdle(t, b)
			expectLog(t, log, "a:", "b:", "a:", "a:c1")
		})
	}
}

// A step whose key cannot be evaluated is skipped; system.storage fails.
func TestRawLoopKeyError(t *testing.T) {
	fastRawLoop(t, time.Millisecond, 2*time.Millisecond, time.Hour)
	b := newTestBackend(t, config.Default())
	log := &callLog{}
	startRawLoop(t, b, &fakeStep{name: "a", n: 1, keyErr: errors.New("no key"), log: log}, &fakeStep{name: "b", n: 1, key: "kb", log: log})
	eventually(t, "the second step", func() bool { return len(log.get()) == 1 })
	time.Sleep(20 * time.Millisecond)
	expectLog(t, log, "b:")
	if _, err := b.System().Storage(context.Background(), api.SystemStorageParams{}); errCode(t, err) != api.CodeStorageError {
		t.Fatalf("system.storage = %v, want storageError", err)
	}
}

// After a batch the loop pauses at least rawMinPause, and at least as long
// as the batch took.
func TestRawLoopPace(t *testing.T) {
	fastRawLoop(t, 15*time.Millisecond, time.Hour, time.Hour)
	b := newTestBackend(t, config.Default())
	quick := &fakeStep{name: "quick", n: 4, key: "k"}
	slow := &fakeStep{name: "slow", n: 3, key: "k", sleep: 30 * time.Millisecond}
	startRawLoop(t, b, quick, slow)
	waitIdle(t, b)
	for _, s := range []*fakeStep{quick, slow} {
		s.mu.Lock()
		spans := slices.Clone(s.spans)
		s.mu.Unlock()
		if len(spans) != s.n {
			t.Fatalf("%s ran %d batches, want %d", s.name, len(spans), s.n)
		}
		for i := 1; i < len(spans); i++ {
			took := spans[i-1][1].Sub(spans[i-1][0])
			if gap := spans[i][0].Sub(spans[i-1][1]); gap < max(rawMinPause, took) {
				t.Errorf("%s: batch %d started %v after the one before, which took %v", s.name, i, gap, took)
			}
		}
	}
}

// Stopping ends a batch through its context and waits for it; Maintain
// returns only after the loop has.
func TestMaintainJoinsRawLoop(t *testing.T) {
	fastRawLoop(t, time.Millisecond, time.Hour, time.Hour)
	b := newTestBackend(t, config.Default())
	a := &fakeStep{name: "a", n: 1, key: "k", block: true, log: &callLog{}}
	b.rawSteps = []RawStep{a}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Maintain(ctx)
	}()
	eventually(t, "the blocking batch", func() bool { return len(a.log.get()) == 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Maintain did not return")
	}
	if !a.ended.Load() {
		t.Fatal("Maintain returned before the raw loop's batch")
	}
}

// New registers the conversion first; the attachment step, once it
// exists, after it.
func TestRawStepsRegistered(t *testing.T) {
	b := newTestBackend(t, config.Default())
	if len(b.rawSteps) == 0 || b.rawSteps[0].Name() != codecStepName {
		t.Fatalf("steps = %v", b.rawSteps)
	}
	names := map[string]bool{}
	for _, s := range b.rawSteps {
		if names[s.Name()] {
			t.Fatalf("step %q registered twice", s.Name())
		}
		names[s.Name()] = true
	}
}

// rawFiles reads every stored message of the mailbox's account back and
// tells which are compressed.
func rawContents(t *testing.T, b *Backend, acc string, ids []string) (content map[string][]byte, zst map[string]bool) {
	t.Helper()
	content, zst = map[string][]byte{}, map[string]bool{}
	for _, id := range ids {
		r, err := b.store.OpenMessageRaw(context.Background(), acc, id)
		if err != nil {
			t.Fatalf("open %s: %v", id, err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		content[id] = data
		path := b.store.MessageRawPath(acc, id)
		_, plainErr := os.Stat(path)
		_, zstErr := os.Stat(path + store.RawZstSuffix)
		if (plainErr == nil) == (zstErr == nil) {
			t.Fatalf("%s: plain file %v, compressed file %v: want exactly one", id, plainErr, zstErr)
		}
		zst[id] = zstErr == nil
	}
	return content, zst
}

// The conversion follows compressStore both ways, byte for byte, and
// leaves the outbox plain.
func TestCodecStepConvertsBothWays(t *testing.T) {
	fastRawLoop(t, time.Millisecond, 5*time.Millisecond, time.Hour)
	m := seedMailbox(t)
	b, acc := m.b, string(m.acc)
	ids := []string{string(m.msgs[0]), string(m.seedRaw(t, "attached-html-message.eml")), string(m.seedQuoted(t, "html-inline-cid.eml"))}
	draft, version := saveDraft(t, b, api.Draft{AccountID: m.acc, To: []api.Address{{Address: "to@example.invalid"}},
		Subject: "queued", TextBody: strings.Repeat("queued text\n", 200)})
	sent, err := b.Messages().Send(context.Background(), api.MessageSendParams{AccountID: m.acc, DraftID: draft, Version: version})
	if err != nil {
		t.Fatal(err)
	}
	outbox := string(sent.OutboxID)
	all := append(slices.Clone(ids), outbox)
	want, zst := rawContents(t, b, acc, all)
	for id, z := range zst {
		if z {
			t.Fatalf("%s compressed before compressStore", id)
		}
	}

	startRawLoop(t, b, newCodecStep(b))
	for _, compress := range []bool{true, false, true} {
		p := basePrefs()
		p.CompressStore = api.Ptr(compress)
		setPrefs(t, b, p)
		waitIdle(t, b)
		got, zst := rawContents(t, b, acc, all)
		for _, id := range all {
			if !bytes.Equal(got[id], want[id]) {
				t.Fatalf("compress %v: %s changed", compress, id)
			}
			if wantZst := compress && id != outbox; zst[id] != wantZst {
				t.Fatalf("compress %v: %s compressed %v", compress, id, zst[id])
			}
		}
		res, err := b.System().Storage(context.Background(), api.SystemStorageParams{})
		if err != nil {
			t.Fatal(err)
		}
		wantCompressed := 0
		if compress {
			wantCompressed = len(ids)
		}
		if res.Messages != len(all) || res.CompressedMessages != wantCompressed || res.Conversion != api.StorageConversionIdle ||
			(res.SavedBytes > 0) != compress {
			t.Fatalf("compress %v: storage = %+v", compress, res)
		}
	}
}

// A pass keeps its codec: when the store's changes under it, the next
// batch reports ErrConflict, and the loop starts the step over.
func TestCodecStepCursorKeepsTarget(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	s := newCodecStep(b)
	if key, err := s.Key(ctx, time.Now()); err != nil || key != "1:plain" {
		t.Fatalf("key = %q, %v", key, err)
	}
	if _, err := s.Batch(ctx, "zstd:m_0"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a zstd pass over a plain store: %v, want ErrConflict", err)
	}
	for _, cursor := range []string{"", "junk", "gzip:m_0"} {
		if next, err := s.Batch(ctx, cursor); err != nil || next != "" {
			t.Errorf("cursor %q over an empty store: %q, %v", cursor, next, err)
		}
	}
	b.store.SetRawCodec(store.RawZstd)
	if key, _ := s.Key(ctx, time.Now()); key != "1:zstd" {
		t.Fatalf("key = %q", key)
	}
}

// The sweep finds files in the other codec although the conversion was
// recorded as done, and starts it over.
func TestRawSweepRestartsConversion(t *testing.T) {
	fastRawLoop(t, time.Millisecond, time.Hour, time.Hour)
	m := seedMailbox(t)
	b, acc, id := m.b, string(m.acc), string(m.msgs[0])
	b.store.SetRawCodec(store.RawZstd)
	if err := b.store.SetMeta(context.Background(), "raw.step."+codecStepName, "1:zstd|done"); err != nil {
		t.Fatal(err)
	}
	b.rawSteps = []RawStep{newCodecStep(b)}
	if got := conversionOf(t, b); got != api.StorageConversionIdle {
		t.Fatalf("before the sweep: %s", got)
	}
	startRawLoop(t, b)
	eventually(t, "the plain file the sweep found converted", func() bool {
		path := b.store.MessageRawPath(acc, id)
		_, plainErr := os.Stat(path)
		_, zstErr := os.Stat(path + store.RawZstSuffix)
		return errors.Is(plainErr, os.ErrNotExist) && zstErr == nil
	})
	waitIdle(t, b)
}
