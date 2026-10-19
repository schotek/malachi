// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Two clients of one account sharing a limiter never have more than 4
// requests in flight together.
func TestLimiterSharedByTwoClients(t *testing.T) {
	var inFlight, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		inFlight.Add(-1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[]}`))
	}))
	t.Cleanup(srv.Close)
	lim := NewLimiter(4)
	mk := func() *Client {
		return NewClient(Options{BaseURL: srv.URL, HTTP: srv.Client(), Limiter: lim,
			Token: func(context.Context) (string, error) { return "t", nil }})
	}
	mail, cal := mk(), mk()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			var out map[string]any
			if err := mail.Get(context.Background(), "me/mailFolders", &out); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			var out map[string]any
			if err := cal.calDo(context.Background(), calReq{method: "GET", url: "me/calendars", out: &out, prio: PriorityLow}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if p := peak.Load(); p > 4 || p < 2 {
		t.Fatalf("peak %d in flight", p)
	}
	if lim.InUse() != 0 {
		t.Fatalf("%d slots leaked", lim.InUse())
	}
}

// A released slot goes to a waiting high-priority request before an
// older low-priority one; a cancelled waiter leaves the queue.
func TestLimiterPriority(t *testing.T) {
	lim := NewLimiter(1)
	release, err := lim.Acquire(context.Background(), PriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	order := make(chan string, 3)
	cctx, cancel := context.WithCancel(context.Background())
	go func() {
		if _, err := lim.Acquire(cctx, PriorityLow); err == nil {
			order <- "cancelled one got a slot"
		}
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	start := func(name string, p Priority) {
		go func() {
			r, err := lim.Acquire(context.Background(), p)
			if err != nil {
				return
			}
			order <- name
			time.Sleep(5 * time.Millisecond)
			r()
		}()
	}
	start("low", PriorityLow)
	time.Sleep(10 * time.Millisecond)
	start("high", PriorityHigh)
	time.Sleep(10 * time.Millisecond)
	release()
	release() // a second call is harmless
	if a, b := <-order, <-order; a != "high" || b != "low" {
		t.Fatalf("order %s, %s", a, b)
	}
	// The last holder lets go after its 5 ms, which are 15 ms where the
	// clock ticks that coarsely (Windows): wait for it, up to a second.
	for deadline := time.Now().Add(time.Second); lim.InUse() != 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d slots in use", lim.InUse())
		}
	}
}
