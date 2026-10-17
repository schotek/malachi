// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Paced writers run one at a time, in the order they asked, and the error
// of the write comes back as it is.
func TestPacedTakesTurns(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	var inside atomic.Int32
	var overlapped atomic.Bool
	var order []int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.Paced(ctx, func() error {
				if inside.Add(1) != 1 {
					overlapped.Store(true)
				}
				mu.Lock()
				order = append(order, i)
				mu.Unlock()
				time.Sleep(5 * time.Millisecond)
				inside.Add(-1)
				return errors.New("write failed")
			})
			if err == nil || err.Error() != "write failed" {
				t.Errorf("writer %d: err %v", i, err)
			}
		}(i)
		time.Sleep(2 * time.Millisecond) // ask in order
	}
	wg.Wait()
	if overlapped.Load() {
		t.Error("two paced writes ran at once")
	}
	if len(order) != 4 {
		t.Fatalf("order %v", order)
	}
}

// The turn stays held after the write for as long as the write took, and
// a writer waiting for the turn leaves when its context ends.
func TestPacedPausesAsLongAsTheWrite(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const work = 60 * time.Millisecond
	var firstDone, secondStart time.Time
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Paced(ctx, func() error {
			time.Sleep(work)
			firstDone = time.Now()
			return nil
		})
	}()
	time.Sleep(10 * time.Millisecond) // the first holds the turn
	wctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	ran := false
	if err := s.Paced(wctx, func() error { ran = true; return nil }); !errors.Is(err, context.DeadlineExceeded) || ran {
		t.Fatalf("waiting writer: err %v ran %v", err, ran)
	}
	if err := s.Paced(ctx, func() error { secondStart = time.Now(); return nil }); err != nil {
		t.Fatal(err)
	}
	<-done
	if gap := secondStart.Sub(firstDone); gap < work-10*time.Millisecond {
		t.Errorf("the next turn came %v after the write ended; the pause should last about %v", gap, work)
	}
}

// TestWriteLockStress is the measurement behind Paced (pace.go): loops of
// back-to-back short transactions beside one writer holding the lock 500
// ms at a time starve every writer past busy_timeout; the same loops
// through Paced do not. It takes about a minute and real time, so it runs
// only with MALACHI_STORE_LOCK_STRESS=1:
//
//	MALACHI_STORE_LOCK_STRESS=1 go test ./internal/store/ -run TestWriteLockStress -v
func TestWriteLockStress(t *testing.T) {
	if os.Getenv("MALACHI_STORE_LOCK_STRESS") == "" {
		t.Skip("set MALACHI_STORE_LOCK_STRESS=1 to run the write-lock stress measurement")
	}
	unpaced := writeLockStress(t, false, 20*time.Second)
	t.Logf("unpaced: %s", unpaced)
	paced := writeLockStress(t, true, 20*time.Second)
	t.Logf("paced:   %s", paced)
	if paced.busy > 0 || paced.maxWait > 2*time.Second {
		t.Errorf("paced writers starved the polling writer: %s", paced)
	}
}

type stressResult struct {
	busy    int           // SQLITE_BUSY failures of any writer
	maxWait time.Duration // the longest wait of the polling writer
	writes  int           // transactions the loops committed
}

func (r stressResult) String() string {
	return "busy=" + itoa(r.busy) + " maxWait=" + r.maxWait.String() + " loopWrites=" + itoa(r.writes)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// writeLockStress runs, for dur, one writer holding the lock 500 ms at a
// time (as the board drain's budget allows), four loops of back-to-back
// 2 ms transactions (as the calendar sync writes a re-list, one
// transaction per event), and one polling writer that writes every 100 ms
// outside any turn (an RPC mutation); paced runs the first five through
// Paced.
func writeLockStress(t *testing.T, paced bool, dur time.Duration) stressResult {
	s := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var res stressResult
	var mu sync.Mutex
	note := func(err error, w time.Duration, polling bool) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			if !strings.Contains(err.Error(), "locked") && !strings.Contains(err.Error(), "busy") {
				t.Errorf("unexpected error: %v", err)
			}
			res.busy++
		}
		if polling && w > res.maxWait {
			res.maxWait = w
		}
	}
	write := func(key string, hold time.Duration) error {
		st := time.Now()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			note(err, time.Since(st), false)
			return err
		}
		_, _ = tx.ExecContext(ctx, `INSERT OR REPLACE INTO meta (key, value) VALUES (?, 'x')`, key)
		time.Sleep(hold)
		if err := tx.Commit(); err != nil {
			note(err, time.Since(st), false)
			return err
		}
		mu.Lock()
		res.writes++
		mu.Unlock()
		return nil
	}
	loop := func(key string, hold, pause time.Duration) {
		for ctx.Err() == nil {
			if paced {
				_ = s.Paced(ctx, func() error { return write(key, hold) })
			} else {
				_ = write(key, hold)
			}
			if pause > 0 {
				select {
				case <-ctx.Done():
				case <-time.After(pause):
				}
			}
		}
	}
	var wg sync.WaitGroup
	wg.Add(5)
	go func() { defer wg.Done(); loop("drain", 500*time.Millisecond, 20*time.Millisecond) }()
	for i := range 4 {
		go func() { defer wg.Done(); loop("burst"+itoa(i), 2*time.Millisecond, 0) }()
	}
	deadline := time.Now().Add(dur)
	for time.Now().Before(deadline) {
		st := time.Now()
		_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO meta (key, value) VALUES ('polling', 'y')`)
		note(err, time.Since(st), true)
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	return res
}
