// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// setMemCacheCap bounds the cache for one test.
func setMemCacheCap(t *testing.T, n int64) {
	t.Helper()
	old := memCacheCap
	memCacheCap = n
	t.Cleanup(func() { memCacheCap = old })
}

func filled(n int, b byte) []byte { return bytes.Repeat([]byte{b}, n) }

// has reports whether the cache holds the message; like get, it marks
// the message used.
func (c *memCache) has(accountID, id string) bool {
	_, ok := c.get(accountID, id)
	return ok
}

// The cache holds what fits into its bytes, dropping the least recently
// used message first; a message over the whole cap is not held.
func TestMemCacheLRU(t *testing.T) {
	setMemCacheCap(t, 100)
	var c memCache
	defer c.close()
	gen := c.generation()
	atts := []api.Attachment{{PartID: "2", Filename: "a.pdf", ContentType: "application/pdf", Size: 3}}
	if !c.put(gen, "a", "1", filled(40, '1'), atts) || !c.put(gen, "a", "2", filled(40, '2'), nil) {
		t.Fatal("put refused")
	}
	if n, size := c.stats(); n != 2 || size != 80 {
		t.Fatalf("stats %d %d", n, size)
	}
	msg, ok := c.get("a", "1") // now the most recently used
	if !ok || !bytes.Equal(msg.raw, filled(40, '1')) || len(msg.attachments) != 1 || msg.attachments[0].Filename != "a.pdf" {
		t.Fatalf("get %+v %v", msg, ok)
	}
	c.put(gen, "b", "3", filled(40, '3'), nil)
	if c.has("a", "2") || !c.has("a", "1") || !c.has("b", "3") {
		t.Fatal("the least recently used message did not go first")
	}
	// The same key again replaces the message.
	c.put(gen, "a", "1", filled(60, '4'), nil)
	if n, size := c.stats(); n != 2 || size != 100 {
		t.Fatalf("after a replacement: %d %d", n, size)
	}
	if msg, _ := c.get("a", "1"); !bytes.Equal(msg.raw, filled(60, '4')) {
		t.Fatal("not replaced")
	}
	if c.put(gen, "c", "4", filled(101, '5'), nil) || !c.has("a", "1") {
		t.Fatal("a message over the cap was held, or pushed others out")
	}
	if !c.put(gen, "c", "4", filled(100, '5'), nil) {
		t.Fatal("a message of the whole cap was refused")
	}
	if n, size := c.stats(); n != 1 || size != 100 || !c.has("c", "4") {
		t.Fatalf("after the largest: %d %d", n, size)
	}

	// The cache takes the bytes over as they are, without a copy (a
	// download hands over bytes of its own), and keeps an attachment list
	// of its own.
	src := filled(10, 'x')
	c.put(gen, "d", "5", src, atts)
	atts[0].Filename = "changed.pdf"
	if msg, _ := c.get("d", "5"); &msg.raw[0] != &src[0] || msg.attachments[0].Filename != "a.pdf" {
		t.Fatal("the cache copied the bytes, or shares the attachment list")
	}
}

// A clear drops everything, and what a download started before it put
// afterwards is not held; an account goes on its own; after close nothing
// is held any more.
func TestMemCacheClearDropClose(t *testing.T) {
	var c memCache
	gen := c.generation()
	for _, k := range []struct{ acc, id string }{{"a", "1"}, {"a", "2"}, {"b", "3"}} {
		c.put(gen, k.acc, k.id, filled(10, 'x'), nil)
	}
	c.dropAccount("a")
	if c.has("a", "1") || c.has("a", "2") || !c.has("b", "3") {
		t.Fatal("dropAccount")
	}
	if n, size := c.stats(); n != 1 || size != 10 {
		t.Fatalf("stats after dropAccount: %d %d", n, size)
	}
	c.clear()
	if n, size := c.stats(); n != 0 || size != 0 {
		t.Fatalf("stats after clear: %d %d", n, size)
	}
	if c.put(gen, "b", "4", filled(10, 'x'), nil) || c.has("b", "4") {
		t.Fatal("a put of before the clear was held")
	}
	if !c.put(c.generation(), "b", "4", filled(10, 'x'), nil) {
		t.Fatal("a put after the clear was refused")
	}
	c.close()
	if n, _ := c.stats(); n != 0 || c.put(c.generation(), "b", "5", filled(10, 'x'), nil) {
		t.Fatal("held after close")
	}
	c.close() // twice is harmless
}

// A message unused for memCacheIdle goes: at once when asked for, and by
// the sweep without being asked for.
func TestMemCacheIdle(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	c := memCache{now: func() time.Time { return now }}
	gen := c.generation()
	c.put(gen, "a", "1", filled(10, 'x'), nil)
	now = now.Add(memCacheIdle - time.Minute)
	if !c.has("a", "1") {
		t.Fatal("gone before its time")
	}
	now = now.Add(memCacheIdle - time.Minute) // used a moment ago: still there
	if !c.has("a", "1") {
		t.Fatal("using it did not keep it")
	}
	now = now.Add(memCacheIdle)
	if c.has("a", "1") {
		t.Fatal("held after being idle for memCacheIdle")
	}
	if n, _ := c.stats(); n != 0 {
		t.Fatalf("an expired message is still counted: %d", n)
	}

	c.put(gen, "a", "1", filled(10, 'x'), nil)
	c.put(gen, "a", "2", filled(10, 'x'), nil)
	now = now.Add(20 * time.Minute)
	c.has("a", "2")
	now = now.Add(15 * time.Minute)
	c.sweep()
	if n, size := c.stats(); n != 1 || size != 10 {
		t.Fatalf("after the sweep: %d %d", n, size)
	}
	c.close()
}

// The timer sweeps what nobody asks for any more, and stops with nothing
// left to hold.
func TestMemCacheIdleTimer(t *testing.T) {
	old := memCacheIdle
	memCacheIdle = 20 * time.Millisecond
	t.Cleanup(func() { memCacheIdle = old })
	var c memCache
	c.put(c.generation(), "a", "1", filled(10, 'x'), nil)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if n, _ := c.stats(); n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the idle message was never swept")
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.close()
}

// Every method may be called from many goroutines at once (run with
// -race); the bytes held never pass the cap.
func TestMemCacheConcurrent(t *testing.T) {
	setMemCacheCap(t, 1000)
	var c memCache
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 400 {
				acc, id := fmt.Sprint("a", g%3), fmt.Sprint(i%17)
				switch i % 11 {
				case 0:
					c.dropAccount(acc)
				case 1:
					c.clear()
				case 2:
					c.sweep()
				case 3, 4:
					if msg, ok := c.get(acc, id); ok && len(msg.raw) == 0 {
						t.Error("an empty message held")
					}
				default:
					c.put(c.generation(), acc, id, filled(10+i%90, byte(i)), nil)
				}
				if _, size := c.stats(); size > memCacheCap {
					t.Errorf("%d bytes held over the cap", size)
				}
			}
		}()
	}
	wg.Wait()
	c.mu.Lock()
	var sum int64
	for el := c.lru.Front(); el != nil; el = el.Next() {
		sum += int64(len(el.Value.(*memEntry).msg.raw))
	}
	n, bytesHeld := c.lru.Len(), c.bytes
	c.mu.Unlock()
	if sum != bytesHeld || n != len(c.entries) {
		t.Fatalf("accounting: %d bytes counted, %d held; %d listed, %d keyed", bytesHeld, sum, n, len(c.entries))
	}
	c.close()
}

// A part of the stored message is found in the held copy by its id, name,
// type and size; renumbered, as the only one with its name, type and size,
// else as the only one with its name and type; never by a guess, and never
// a part of another size while one of its size is there.
func TestHeldPartID(t *testing.T) {
	want := api.Attachment{PartID: "3", Filename: "report.pdf", ContentType: "application/pdf", Size: 100}
	pdf := func(id string, size int64) api.Attachment {
		return api.Attachment{PartID: id, Filename: "report.pdf", ContentType: "application/pdf", Size: size}
	}
	other := api.Attachment{PartID: "3", Filename: "notes.txt", ContentType: "text/plain", Size: 100}
	for _, c := range []struct {
		name string
		held []api.Attachment
		id   string
	}{
		{"same id", []api.Attachment{pdf("2", 90), pdf("3", 100)}, "3"},
		{"same id among several of its size", []api.Attachment{pdf("2", 100), pdf("3", 100)}, "3"},
		{"same id of another size, another of its size", []api.Attachment{pdf("2", 100), pdf("3", 99)}, "2"},
		{"same id of another size, alone", []api.Attachment{other, pdf("3", 99)}, "3"},
		{"same id of another size, among others", []api.Attachment{pdf("3", 99), pdf("4", 98)}, ""},
		{"renumbered", []api.Attachment{other, pdf("4", 90)}, "4"},
		{"renumbered, of its size", []api.Attachment{other, pdf("4", 100)}, "4"},
		{"several, one of its size", []api.Attachment{pdf("4", 90), pdf("5", 100)}, "5"},
		{"several of its size", []api.Attachment{pdf("4", 100), pdf("5", 100)}, ""},
		{"several, none of its size", []api.Attachment{pdf("4", 1), pdf("5", 2)}, ""},
		{"another type", []api.Attachment{{PartID: "3", Filename: "report.pdf", ContentType: "application/octet-stream", Size: 100}}, ""},
		{"none", nil, ""},
	} {
		if got := heldPartID(want, c.held); got != c.id {
			t.Errorf("%s: %q, want %q", c.name, got, c.id)
		}
	}
}
