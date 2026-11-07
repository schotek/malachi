// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// connBackend records the connection id of every assistant.prompt call
// and every connection that closed.
type connBackend struct {
	StubBackend
	seen   chan api.ConnID
	closed chan api.ConnID
}

func (b *connBackend) Assistant() api.AssistantService { return connAssistant{b: b} }
func (b *connBackend) ConnClosed(id api.ConnID)        { b.closed <- id }

type connAssistant struct {
	stubAssistant
	b *connBackend
}

func (a connAssistant) Prompt(ctx context.Context, _ api.AssistantPromptParams) (*api.PromptResult, error) {
	id, _ := api.ConnFromContext(ctx)
	a.b.seen <- id
	return &api.PromptResult{SystemPrompt: "x"}, nil
}

func waitID(t *testing.T, ch <-chan api.ConnID, what string) api.ConnID {
	t.Helper()
	select {
	case id := <-ch:
		return id
	case <-time.After(waitLimit):
		t.Fatalf("no %s within %v", what, waitLimit)
		return ""
	}
}

// A call carries its connection's id; NotifyConn reaches that connection
// alone; an unknown id is refused; a closed connection is reported to
// the backend once its calls are done.
func TestNotifyConnReachesOneConnection(t *testing.T) {
	b := &connBackend{seen: make(chan api.ConnID, 4), closed: make(chan api.ConnID, 4)}
	ts := startServer(t, b, nil, nil)
	a, ra := dialAuthed(t, ts.sock)
	other, ro := dialAuthed(t, ts.sock)

	resp := call(t, a, ra, api.MethodAssistantPrompt, map[string]any{"purpose": "panel", "language": "cs"})
	if resp.Error != nil {
		t.Fatalf("prompt: %+v", resp.Error)
	}
	idA := waitID(t, b.seen, "connection id")
	if idA == "" {
		t.Fatal("the call carried no connection id")
	}
	resp = call(t, other, ro, api.MethodAssistantPrompt, map[string]any{"purpose": "panel", "language": "cs"})
	if resp.Error != nil {
		t.Fatalf("prompt: %+v", resp.Error)
	}
	if idB := waitID(t, b.seen, "connection id"); idB == idA || idB == "" {
		t.Fatalf("the other connection has id %q, the first %q", idB, idA)
	}

	ev := api.AssistantEvent{SessionID: "as_1", Kind: api.EventInit, Tools: []string{"read_message"}}
	if !ts.AssistantEvent(idA, ev) {
		t.Fatal("the event was not delivered")
	}
	line, err := readLine(a, ra, waitLimit)
	if err != nil {
		t.Fatalf("read notification: %v", err)
	}
	var n api.Notification
	if err := json.Unmarshal(line, &n); err != nil || n.Method != api.NotifyAssistantEvent {
		t.Fatalf("got %s (%v)", line, err)
	}
	var got api.AssistantEvent
	if err := json.Unmarshal(n.Params, &got); err != nil || got.SessionID != "as_1" || got.Kind != api.EventInit {
		t.Fatalf("params %s", n.Params)
	}
	if line, err := readLine(other, ro, 200*time.Millisecond); err == nil || !isTimeout(err) {
		t.Fatalf("the other connection got %q (%v)", line, err)
	}
	if ts.NotifyConn("c_999999", api.NotifyAssistantEvent, ev) {
		t.Fatal("an unknown connection took the event")
	}

	// The broadcast of the usage change reaches both.
	ts.AssistantUsageChanged(api.AssistantUsageChangedNotification{AccountID: "acc_1"})
	for _, pair := range []struct {
		c net.Conn
		r *bufio.Reader
	}{{a, ra}, {other, ro}} {
		line, err := readLine(pair.c, pair.r, waitLimit)
		if err != nil {
			t.Fatalf("read broadcast: %v", err)
		}
		if err := json.Unmarshal(line, &n); err != nil || n.Method != api.NotifyAssistantUsageChanged {
			t.Fatalf("got %s (%v)", line, err)
		}
	}

	// Closing a connection reaches the backend, with its id, once.
	_ = a.Close()
	if id := waitID(t, b.closed, "closed connection"); id != idA {
		t.Fatalf("closed %q, want %q", id, idA)
	}
	select {
	case id := <-b.closed:
		t.Fatalf("a second closed connection %q", id)
	case <-time.After(100 * time.Millisecond):
	}
	if ts.NotifyConn(idA, api.NotifyAssistantEvent, ev) {
		t.Fatal("a closed connection took the event")
	}
}

// A connection that never authenticated is not reported as closed.
func TestConnClosedOnlyWhenAuthenticated(t *testing.T) {
	b := &connBackend{seen: make(chan api.ConnID, 4), closed: make(chan api.ConnID, 4)}
	ts := startServer(t, b, nil, nil)
	c, _ := dial(t, ts.sock)
	_ = c.Close()
	select {
	case id := <-b.closed:
		t.Fatalf("an unauthenticated connection was reported closed as %q", id)
	case <-time.After(200 * time.Millisecond):
	}
}
