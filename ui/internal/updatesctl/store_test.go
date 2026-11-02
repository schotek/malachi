// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package updatesctl

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// msgids is a Translator that returns the msgid.
type msgids struct{}

func (msgids) T(m string) string { return m }
func (msgids) N(s, p string, n int) string {
	if n == 1 {
		return s
	}
	return p
}
func (msgids) C(_, m string) string { return m }

// loop queues posted callbacks; run drains them on the test goroutine.
type loop struct {
	mu sync.Mutex
	q  []func()
}

func (l *loop) Post(f func()) {
	l.mu.Lock()
	l.q = append(l.q, f)
	l.mu.Unlock()
}

func (l *loop) After(time.Duration, func()) {}

func (l *loop) take() []func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	q := l.q
	l.q = nil
	return q
}

// wait runs the posted callbacks until n have run.
func (l *loop) wait(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for n > 0 {
		for _, f := range l.take() {
			f()
			n--
		}
		if n > 0 {
			if time.Now().After(deadline) {
				t.Fatalf("%d callbacks did not come", n)
			}
			time.Sleep(time.Millisecond)
		}
	}
}

type call struct {
	method string
	params string
}

// daemon answers every call with the status its reply gives; gate, when
// set, holds a call until it is closed.
type daemon struct {
	mu    sync.Mutex
	calls []call
	reply func(method string) (*api.UpdateStatus, error)
	gates map[int]chan struct{}
}

func (d *daemon) Call(_ context.Context, method string, params any, result any) error {
	b, _ := json.Marshal(params)
	d.mu.Lock()
	n := len(d.calls)
	d.calls = append(d.calls, call{method, string(b)})
	gate := d.gates[n]
	d.mu.Unlock()
	if gate != nil {
		<-gate
	}
	st, err := d.reply(method)
	if err != nil {
		return err
	}
	*(result.(*api.UpdateStatus)) = *st
	return nil
}

func (d *daemon) log() []call {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]call(nil), d.calls...)
}

func security() *api.UpdateStatus {
	return &api.UpdateStatus{Enabled: true, State: api.UpdateAvailable, Available: &api.UpdateInfo{
		Version: "1.0.1", Security: true, Prompt: true,
		URL: "https://github.com/schotek/malachi/releases/download/v1.0.1/x.dmg"}}
}

func TestConnectingAndNotificationReadTheStatus(t *testing.T) {
	d := &daemon{reply: func(string) (*api.UpdateStatus, error) {
		return &api.UpdateStatus{Enabled: true, State: api.UpdateUpToDate}, nil
	}}
	l := &loop{}
	s := NewStore(d, l, "cs", nil)
	changes := 0
	s.Observe(func() { changes++ })
	s.ConnectionChanged(true)
	s.ConnectionChanged(true) // no second read
	l.wait(t, 1)
	if s.Status() == nil || s.Status().State != api.UpdateUpToDate || changes != 1 {
		t.Fatalf("status %+v, %d changes", s.Status(), changes)
	}
	if s.Handle(api.NotifyBoardChanged) {
		t.Error("another notification handled")
	}
	if !s.Handle(api.NotifyUpdateChanged) {
		t.Error("notify.updateChanged not handled")
	}
	l.wait(t, 1)
	s.ConnectionChanged(false)
	if s.Status() == nil {
		t.Error("the status went with the connection")
	}
	s.ConnectionChanged(true)
	l.wait(t, 1)
	got := d.log()
	if len(got) != 3 {
		t.Fatalf("calls %+v", got)
	}
	for _, c := range got {
		if c.method != api.MethodUpdateStatus || c.params != `{"locale":"cs"}` {
			t.Errorf("call %+v", c)
		}
	}
}

func TestWritesCarryTheirParams(t *testing.T) {
	d := &daemon{reply: func(string) (*api.UpdateStatus, error) { return &api.UpdateStatus{}, nil }}
	l := &loop{}
	s := NewStore(d, l, "en", nil)
	s.Check()
	l.wait(t, 1)
	s.SetEnabled(false)
	l.wait(t, 1)
	s.Dismiss("1.0.0", api.UpdateLater)
	l.wait(t, 1)
	s.DismissNotice("n-1")
	l.wait(t, 1)
	want := []call{
		{api.MethodUpdateCheck, `{"locale":"en"}`},
		{api.MethodUpdateSetPrefs, `{"enabled":false,"locale":"en"}`},
		{api.MethodUpdateDismiss, `{"version":"1.0.0","action":"later","locale":"en"}`},
		{api.MethodUpdateDismissNotice, `{"id":"n-1","locale":"en"}`},
	}
	got := d.log()
	if len(got) != len(want) {
		t.Fatalf("calls %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestAnOlderAnswerIsDropped(t *testing.T) {
	gate := make(chan struct{})
	d := &daemon{gates: map[int]chan struct{}{0: gate}, reply: func(method string) (*api.UpdateStatus, error) {
		if method == api.MethodUpdateStatus {
			return &api.UpdateStatus{Enabled: true, State: api.UpdateChecking}, nil
		}
		return &api.UpdateStatus{State: api.UpdateDisabled}, nil
	}}
	l := &loop{}
	s := NewStore(d, l, "en", nil)
	s.Refresh() // held
	for len(d.log()) == 0 {
		time.Sleep(time.Millisecond)
	}
	s.SetEnabled(false)
	l.wait(t, 1)
	close(gate)
	l.wait(t, 1)
	if s.Status() == nil || s.Status().State != api.UpdateDisabled {
		t.Errorf("status %+v: the older answer won", s.Status())
	}
}

func TestAFailureKeepsTheStatusAndTellsTheObservers(t *testing.T) {
	fail := false
	d := &daemon{reply: func(string) (*api.UpdateStatus, error) {
		if fail {
			return nil, errors.New("gone")
		}
		return &api.UpdateStatus{Enabled: true, State: api.UpdateUpToDate}, nil
	}}
	l := &loop{}
	s := NewStore(d, l, "en", nil)
	s.Refresh()
	l.wait(t, 1)
	fail = true
	changes := 0
	remove := s.Observe(func() { changes++ })
	s.SetEnabled(false)
	l.wait(t, 1)
	if s.Status() == nil || !s.Status().Enabled || changes != 1 {
		t.Errorf("status %+v, %d changes", s.Status(), changes)
	}
	remove()
	s.Refresh()
	l.wait(t, 1)
	if changes != 1 {
		t.Error("a removed observer was called")
	}
}

func TestThePromptIsTakenOncePerVersion(t *testing.T) {
	st := security()
	d := &daemon{reply: func(string) (*api.UpdateStatus, error) { c := *st; return &c, nil }}
	l := &loop{}
	s := NewStore(d, l, "en", nil)
	if s.TakePrompt(msgids{}) != nil {
		t.Error("a prompt without a status")
	}
	s.Refresh()
	l.wait(t, 1)
	p := s.TakePrompt(msgids{})
	if p == nil || p.Version != "1.0.1" {
		t.Fatalf("prompt %+v", p)
	}
	// The daemon still says prompt (no dismiss yet): not asked again.
	s.Refresh()
	l.wait(t, 1)
	if s.TakePrompt(msgids{}) != nil {
		t.Error("asked twice")
	}
	// A newer security release asks again.
	st.Available.Version = "1.0.2"
	s.Refresh()
	l.wait(t, 1)
	if p := s.TakePrompt(msgids{}); p == nil || p.Version != "1.0.2" {
		t.Errorf("prompt of a newer version %+v", p)
	}
	// Prompted elsewhere: nothing to ask.
	st.Available.Version = "1.0.3"
	st.Available.Prompt = false
	s.Refresh()
	l.wait(t, 1)
	if s.TakePrompt(msgids{}) != nil {
		t.Error("asked a release another client asked about")
	}
}

func TestLocale(t *testing.T) {
	cat := []string{"cs"}
	for _, c := range []struct {
		langs []string
		want  string
	}{
		{[]string{"cs_CZ.UTF-8", "cs_CZ", "cs.UTF-8", "cs", "C"}, "cs"},
		{[]string{"en_US.UTF-8", "en_US", "en", "C"}, "en"},
		{[]string{"de_DE.UTF-8", "de_DE", "de", "C"}, "en"},
		{[]string{"de_DE", "cs_CZ", "cs", "C"}, "cs"},
		{nil, "en"},
	} {
		if got := Locale(c.langs, cat); got != c.want {
			t.Errorf("Locale(%v) = %q, want %q", c.langs, got, c.want)
		}
	}
}
