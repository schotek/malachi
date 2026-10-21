// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// profileDaemon answers the store's calls from canned results.
type profileDaemon struct {
	mu      sync.Mutex
	get     api.AssistantProfileGetResult
	getErr  error
	sets    []api.AssistantProfileSetParams
	resolve []api.AssistantProfileResolveParams
	failSet error
}

func (d *profileDaemon) Call(_ context.Context, method string, params any, result any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out any
	switch method {
	case api.MethodAssistantProfileGet:
		if d.getErr != nil {
			return d.getErr
		}
		out = d.get
	case api.MethodAccountList:
		out = api.AccountListResult{Accounts: []api.Account{{ID: "acc_1", Config: api.AccountConfig{Name: "Studio", Email: "s@example.org"}}}}
	case api.MethodAssistantProfileSet:
		p := params.(api.AssistantProfileSetParams)
		d.sets = append(d.sets, p)
		if d.failSet != nil {
			return d.failSet
		}
		if p.Text != nil {
			d.get.Profile.Text = *p.Text
		}
		out = api.AssistantProfileSetResult{Profile: d.get.Profile}
	case api.MethodAssistantProfileResolve:
		p := params.(api.AssistantProfileResolveParams)
		d.resolve = append(d.resolve, p)
		d.get.Proposals = nil
		if p.Accept {
			d.get.Profile.Text += "\nSchůzky v pondělí."
		}
		out = api.AssistantProfileResolveResult{Profile: d.get.Profile, Proposals: d.get.Proposals}
	default:
		return errors.New("unexpected " + method)
	}
	raw, _ := json.Marshal(out)
	return json.Unmarshal(raw, result)
}

func TestProfileStore(t *testing.T) {
	loop := newTestLoop()
	d := &profileDaemon{get: api.AssistantProfileGetResult{
		Profile: api.AssistantProfile{Text: "Píšu stručně.", Gender: api.GenderMasculine, ShareWithMCP: true,
			Accounts: []api.ProfileAccountNote{{AccountID: "acc_1", Note: "Studio."}}},
		Proposals: []api.ProfileProposal{{ID: "p_1", Text: "Schůzky v pondělí.", Source: api.ProfileFromPanel}},
		Limits:    api.DefaultProfileLimits(),
	}}
	s := NewProfileStore(d, loop, nil)
	changes := 0
	s.OnChange = func() { changes++ }
	if s.Loaded() || s.Supported() || !s.Profile().Empty() {
		t.Fatal("a new store is not empty")
	}
	s.Refresh()
	loop.runUntil(t, s.Loaded)
	p := s.Profile()
	if !s.Supported() || changes != 1 || p.Text != "Píšu stručně." || len(p.Accounts) != 1 || p.Accounts[0].Name != "Studio" || p.Accounts[0].Email != "s@example.org" {
		t.Fatalf("profile %+v", p)
	}
	if q, ok := s.Proposal("p_1"); !ok || q.Text != "Schůzky v pondělí." {
		t.Fatalf("proposal %+v", q)
	}
	if _, ok := s.Proposal("p_x"); ok {
		t.Fatal("an unknown proposal")
	}

	text := "Píšu velmi stručně."
	var setErr error = errors.New("pending")
	s.Set(api.AssistantProfileSetParams{Text: &text}, func(err error) { setErr = err })
	loop.runUntil(t, func() bool { return setErr == nil || setErr.Error() != "pending" })
	if setErr != nil || s.Stored().Text != text || len(s.Proposals()) != 1 {
		t.Fatalf("after set %v %+v", setErr, s.Stored())
	}

	resolved := false
	s.Resolve("p_1", true, nil, func(err error) { resolved = err == nil })
	loop.runUntil(t, func() bool { return resolved })
	if len(s.Proposals()) != 0 || s.Stored().Text != text+"\nSchůzky v pondělí." {
		t.Fatalf("after resolve %+v %+v", s.Stored(), s.Proposals())
	}

	// A refused write keeps what the store has.
	d.failSet = api.NewError(api.CodeInvalidArgument, "too long")
	var refused error
	other := "x"
	s.Set(api.AssistantProfileSetParams{Text: &other}, func(err error) { refused = err })
	loop.runUntil(t, func() bool { return refused != nil })
	if s.Stored().Text == other {
		t.Fatal("a refused write changed the store")
	}
}

func TestProfileStoreOlderDaemon(t *testing.T) {
	loop := newTestLoop()
	d := &profileDaemon{getErr: api.NewError(api.CodeMethodNotFound, "no such method")}
	s := NewProfileStore(d, loop, nil)
	s.Refresh()
	loop.runUntil(t, s.Loaded)
	if s.Supported() || !s.Profile().Empty() || s.Limits() != api.DefaultProfileLimits() || !s.Stored().ShareWithMCP {
		t.Fatalf("older daemon: supported %v, %+v", s.Supported(), s.Stored())
	}
	// A transient failure keeps what was read before.
	d.getErr = nil
	d.get = api.AssistantProfileGetResult{Profile: api.AssistantProfile{Text: "T", Gender: api.GenderUnset}}
	s.Refresh()
	loop.runUntil(t, s.Supported)
	d.getErr = errors.New("connection lost")
	changed := false
	s.OnChange = func() { changed = true }
	s.Refresh()
	loop.runUntil(t, func() bool { return changed })
	if !s.Supported() || s.Stored().Text != "T" || s.Stored().Accounts == nil || s.Limits() != api.DefaultProfileLimits() {
		t.Fatalf("after a failure %+v", s.Stored())
	}
}
