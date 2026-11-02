// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package rulesctl

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The fake daemon and loop of the tests: calls run in place (Spawn), their
// answers wait on the loop until run.

type en struct{}

func (en) T(msgid string) string { return msgid }
func (en) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (en) C(_, msgid string) string { return msgid }

type loop struct{ queue []func() }

func (l *loop) Post(f func())                   { l.queue = append(l.queue, f) }
func (l *loop) After(_ time.Duration, f func()) { l.queue = append(l.queue, f) }

// run runs what was posted, and what that posted, until nothing is left.
func (l *loop) run() {
	for len(l.queue) > 0 {
		f := l.queue[0]
		l.queue = l.queue[1:]
		f()
	}
}

type handler func(params json.RawMessage) (any, error)

type daemon struct {
	handlers map[string]handler
	calls    []string
	params   map[string][]json.RawMessage
}

func newDaemon() *daemon {
	return &daemon{handlers: map[string]handler{}, params: map[string][]json.RawMessage{}}
}

func (d *daemon) on(method string, h handler) { d.handlers[method] = h }

func (d *daemon) answer(method string, v any) {
	d.on(method, func(json.RawMessage) (any, error) { return v, nil })
}

func (d *daemon) fail(method string, e *api.Error) {
	d.on(method, func(json.RawMessage) (any, error) { return nil, e })
}

func (d *daemon) Call(_ context.Context, method string, params, result any) error {
	raw, _ := json.Marshal(params)
	d.calls = append(d.calls, method)
	d.params[method] = append(d.params[method], raw)
	h, ok := d.handlers[method]
	if !ok {
		return &api.Error{Code: api.CodeMethodNotFound, Message: method}
	}
	v, err := h(raw)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(v)
	return json.Unmarshal(b, result)
}

// count is how many times method was called.
func (d *daemon) count(method string) int {
	n := 0
	for _, m := range d.calls {
		if m == method {
			n++
		}
	}
	return n
}

// last decodes method's last params into v.
func (d *daemon) last(t *testing.T, method string, v any) {
	t.Helper()
	ps := d.params[method]
	if len(ps) == 0 {
		t.Fatalf("no call of %s", method)
	}
	if err := json.Unmarshal(ps[len(ps)-1], v); err != nil {
		t.Fatal(err)
	}
}

func options(d *daemon, l *loop) Options {
	return Options{Caller: d, Loop: l, Tr: en{}, Spawn: func(f func()) { f() },
		Date:    func(t time.Time) string { return "date" },
		RPCText: func(what string, err error) string { return fmt.Sprintf("%s failed", what) }}
}

// mailFixture is a daemon with two mail accounts, a Jira account, their
// folders and two rules.
func mailFixture() *daemon {
	d := newDaemon()
	d.answer(api.MethodAccountList, api.AccountListResult{Accounts: []api.Account{
		{ID: "acc1", Enabled: true, Config: api.AccountConfig{Name: "Work", Email: "w@example.com", Kind: api.AccountGraph}},
		{ID: "acc2", Enabled: true, Config: api.AccountConfig{Name: "Home", Email: "h@example.com"}},
		{ID: "acc3", Enabled: true, Config: api.AccountConfig{Name: "Issues", Kind: api.AccountJira}},
	}})
	d.on(api.MethodFolderList, func(raw json.RawMessage) (any, error) {
		var p api.FolderListParams
		_ = json.Unmarshal(raw, &p)
		mk := func(id, name string, role api.FolderRole) api.Folder {
			return api.Folder{ID: api.FolderID(string(p.AccountID) + id), AccountID: p.AccountID, Name: name, Path: name, Role: role, Selectable: true}
		}
		return api.FolderListResult{Folders: []api.Folder{
			mk("i", "INBOX", api.RoleInbox), mk("o", "Outbox", api.RoleOutbox), mk("d", "Drafts", api.RoleDrafts),
			mk("a", "Archive", api.RoleArchive), mk("x", "Invoices", api.RoleNone),
		}}, nil
	})
	d.answer(api.MethodRulesList, api.RulesListResult{Rules: []api.Rule{
		storedRule("r1", "Newsletters", "acc1", true, api.RuleAction{Kind: api.RuleActionMarkRead}),
		storedRule("r2", "Forward bills", "acc1", false, api.RuleAction{Kind: api.RuleActionForward, To: "a@b.cz"}),
		storedRule("r3", "Flag", "", true, api.RuleAction{Kind: api.RuleActionFlag}),
	}})
	return d
}

func storedRule(id, name string, account api.AccountID, enabled bool, a api.RuleAction) api.Rule {
	in := api.RuleInput{Name: name, Enabled: enabled, AccountID: account, Match: api.RuleMatchAll,
		Conditions: []api.RuleCondition{{Field: api.RuleFieldFrom, Op: api.RuleOpContains, Value: "x"}},
		Actions:    []api.RuleAction{a}}
	return api.Rule{ID: id, RuleInput: in, Outgoing: a.Kind.IsOutgoing()}
}
