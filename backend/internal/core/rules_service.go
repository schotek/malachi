// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/rules"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The mail rules (docs/api.md §4.21, docs/plans/rules.md). This file is
// the rules.* methods; rules_engine.go runs the rules on stored mail
// (automatically and by rules.apply), rules_outgoing.go sends what their
// forward and autoReply actions queue. internal/rules is the pure matcher
// and the validation.

const (
	// rulesNotifyEvery is the least time between two notify.rulesChanged.
	rulesNotifyEvery = time.Second
	// ruleEnabledSlack is how much older than the rule's enabling a
	// message may be and still go through the automatic run (R3).
	ruleEnabledSlack = 24 * time.Hour
	// ruleSeenAge is how long a key of rule_seen is kept.
	ruleSeenAge = 400 * 24 * time.Hour
)

// rulesState is the backend's part of the mail rules.
type rulesState struct {
	mu sync.Mutex
	// list is the rules as last read from the store; loaded false makes
	// the next reader read them again (every write invalidates it).
	list   []store.Rule
	loaded bool
	gen    uint64 // bumped by every invalidation

	// The coalesced notify.rulesChanged.
	lastNotify time.Time
	timer      *time.Timer

	// The outgoing actions' queues, one worker per mail account
	// (rules_outgoing.go); pending counts the jobs queued or running.
	queues   map[string]*ruleOutQueue
	rawBytes int64 // the originals the queued jobs hold
	pending  sync.WaitGroup

	// now is the clock of the engine and the outgoing limits (tests); nil
	// = time.Now.
	now func() time.Time
}

func (b *Backend) rulesNow() time.Time {
	if f := b.rules.now; f != nil {
		return f()
	}
	return time.Now()
}

// Rules returns the mail rules service.
func (b *Backend) Rules() api.RulesService { return &rulesService{b} }

type rulesService struct{ b *Backend }

// cachedRules returns every rule in its order, from the cache when it is
// loaded.
func (b *Backend) cachedRules(ctx context.Context) ([]store.Rule, error) {
	r := &b.rules
	r.mu.Lock()
	if r.loaded {
		out := r.list
		r.mu.Unlock()
		return out, nil
	}
	gen := r.gen
	r.mu.Unlock()
	list, err := b.store.ListRules(ctx)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	// A write that committed while this read ran invalidated the cache:
	// what was read may be older, so it is used once but not kept.
	if r.gen == gen {
		r.list, r.loaded = list, true
	}
	r.mu.Unlock()
	return list, nil
}

// rulesInvalidate forgets the cached rules.
func (b *Backend) rulesInvalidate() {
	b.rules.mu.Lock()
	b.rules.loaded, b.rules.list = false, nil
	b.rules.gen++
	b.rules.mu.Unlock()
}

// rulesChanged sends notify.rulesChanged, at most once every
// rulesNotifyEvery: what comes sooner goes out once, at the end of it.
func (b *Backend) rulesChanged() {
	r := &b.rules
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		return
	}
	wait := max(rulesNotifyEvery-time.Since(r.lastNotify), 0)
	r.timer = time.AfterFunc(wait, func() {
		r.mu.Lock()
		r.timer, r.lastNotify = nil, time.Now()
		r.mu.Unlock()
		if n, ok := b.getNotifier().(api.RulesNotifier); ok {
			n.RulesChanged(api.RulesChangedNotification{})
		}
	})
}

// rulesWritten is every write's tail: the cache goes, the clients hear.
func (b *Backend) rulesWritten() {
	b.rulesInvalidate()
	b.rulesChanged()
}

func apiRules(list []store.Rule) []api.Rule {
	out := make([]api.Rule, 0, len(list))
	for _, r := range list {
		out = append(out, r.Rule)
	}
	return out
}

// ruleInvalid is the invalidArgument of a refused rule.
func ruleInvalid(field string, index int, format string, args ...any) error {
	e := api.NewError(api.CodeInvalidArgument, format, args...)
	e.Data = api.RuleInvalidData{Field: field, Index: index}
	return e
}

// isMailKind reports whether rules run on the account's mail.
func isMailKind(a store.Account) bool {
	k := a.Config.Protocol()
	return k == api.AccountIMAP || k == api.AccountGraph
}

// checkRuleInput normalises a rule as the user edited it and checks what
// the store has to say: the account is a mail account, a move goes to a
// selectable folder of it other than its outbox and drafts.
func (b *Backend) checkRuleInput(ctx context.Context, in api.RuleInput) (api.RuleInput, error) {
	out, ierr := rules.Normalize(in)
	if ierr != nil {
		e := api.NewError(api.CodeInvalidArgument, "%s", ierr.Reason)
		e.Data = ierr.Data
		return out, e
	}
	if out.AccountID == "" {
		return out, nil
	}
	a, err := b.requireAccount(ctx, string(out.AccountID))
	if err != nil {
		return out, err
	}
	if !isMailKind(a) {
		return out, ruleInvalid("accountId", 0, "rules run on mail accounts only")
	}
	for i, act := range out.Actions {
		if act.Kind != api.RuleActionMove {
			continue
		}
		f, err := b.store.GetFolder(ctx, a.ID, string(act.FolderID))
		switch {
		case errors.Is(err, store.ErrNotFound):
			return out, api.NewError(api.CodeFolderNotFound, "unknown folder %q", act.FolderID)
		case err != nil:
			return out, api.NewError(api.CodeStorageError, "%v", err)
		}
		if !ruleMoveTarget(f) {
			return out, ruleInvalid("actions", i, "the folder cannot take a rule's messages")
		}
	}
	return out, nil
}

// ruleMoveTarget reports whether a move action may go to the folder.
func ruleMoveTarget(f store.Folder) bool {
	return f.Selectable && f.Virtual == "" && f.Role != api.RoleOutbox && f.Role != api.RoleDrafts
}

func (s *rulesService) List(ctx context.Context, _ api.RulesListParams) (*api.RulesListResult, error) {
	list, err := s.b.store.ListRules(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	return &api.RulesListResult{Rules: apiRules(list)}, nil
}

func (s *rulesService) Create(ctx context.Context, p api.RulesCreateParams) (*api.RulesCreateResult, error) {
	in, err := s.b.checkRuleInput(ctx, p.Rule)
	if err != nil {
		return nil, err
	}
	if rules.NeedsConfirm(nil, in) && !p.ConfirmOutgoing {
		return nil, ruleInvalid("confirmOutgoing", 0, "a rule that sends mail needs confirmOutgoing")
	}
	r, err := s.b.store.CreateRule(ctx, in, s.b.rulesNow())
	switch {
	case errors.Is(err, store.ErrRuleLimit):
		return nil, api.NewError(api.CodeInvalidArgument, "at most %d rules", api.MaxRules)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	s.b.rulesWritten()
	s.b.log.Info("rule created", "rule", r.ID, "outgoing", r.Outgoing)
	return &api.RulesCreateResult{Rule: r.Rule}, nil
}

func (s *rulesService) Update(ctx context.Context, p api.RulesUpdateParams) (*api.RulesUpdateResult, error) {
	if p.ID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "id is required")
	}
	in, err := s.b.checkRuleInput(ctx, p.Rule)
	if err != nil {
		return nil, err
	}
	r, err := s.b.store.UpdateRule(ctx, p.ID, in, s.b.rulesNow(), func(old store.Rule) error {
		if rules.NeedsConfirm(&old.RuleInput, in) && !p.ConfirmOutgoing {
			return ruleInvalid("confirmOutgoing", 0, "a rule that sends mail needs confirmOutgoing")
		}
		return nil
	})
	var ae *api.Error
	switch {
	case errors.As(err, &ae):
		return nil, err
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeRuleNotFound, "unknown rule %q", p.ID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	s.b.rulesWritten()
	s.b.log.Info("rule updated", "rule", r.ID, "enabled", r.Enabled, "outgoing", r.Outgoing)
	return &api.RulesUpdateResult{Rule: r.Rule}, nil
}

func (s *rulesService) Delete(ctx context.Context, p api.RulesDeleteParams) (*api.RulesDeleteResult, error) {
	if p.ID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "id is required")
	}
	err := s.b.store.DeleteRule(ctx, p.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeRuleNotFound, "unknown rule %q", p.ID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	s.b.rulesWritten()
	s.b.log.Info("rule deleted", "rule", p.ID)
	return &api.RulesDeleteResult{}, nil
}

func (s *rulesService) Reorder(ctx context.Context, p api.RulesReorderParams) (*api.RulesReorderResult, error) {
	list, err := s.b.store.ReorderRules(ctx, p.IDs)
	switch {
	case errors.Is(err, store.ErrRuleOrder):
		return nil, api.NewError(api.CodeInvalidArgument, "ids must name every rule exactly once")
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	s.b.rulesWritten()
	return &api.RulesReorderResult{Rules: apiRules(list)}, nil
}

// Test reports which stored messages of a folder the rule, as edited,
// matches; nothing changes.
func (s *rulesService) Test(ctx context.Context, p api.RulesTestParams) (*api.RulesTestResult, error) {
	in, ierr := rules.Normalize(p.Rule)
	if ierr != nil {
		e := api.NewError(api.CodeInvalidArgument, "%s", ierr.Reason)
		e.Data = ierr.Data
		return nil, e
	}
	a, f, err := s.b.requireFolder(ctx, p.AccountID, p.FolderID)
	if err != nil {
		return nil, err
	}
	if !isMailKind(a) {
		return nil, api.NewError(api.CodeInvalidArgument, "rules run on mail accounts only")
	}
	ids, err := s.b.store.FolderMessageIDs(ctx, f.ID, false, false)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	body := rules.HasBodyCondition(in)
	res := &api.RulesTestResult{Messages: []api.MessageSummary{}}
	var hits []store.Message
	for _, id := range ids { // newest first
		if err := ctx.Err(); err != nil {
			return nil, api.NewError(api.CodeCancelled, "the call ended")
		}
		m, err := s.b.store.GetMessage(ctx, a.ID, id)
		if err != nil {
			continue // gone meanwhile
		}
		if !rules.Matches(in, s.b.ruleMessage(ctx, m, body)) {
			continue
		}
		res.Count++
		if len(hits) < api.MaxRuleTestMessages {
			hits = append(hits, m)
		}
	}
	for _, m := range hits {
		res.Messages = append(res.Messages, toAPISummary(m))
	}
	if err := s.b.decorateRows(ctx, a, hits, res.Messages); err != nil {
		return nil, err
	}
	return res, nil
}

// ruleMessage is what the conditions see of a stored message; the body
// only when asked for.
func (b *Backend) ruleMessage(ctx context.Context, m store.Message, body bool) rules.Message {
	rm := rules.Message{
		From: m.From, To: m.To, CC: m.CC, Subject: m.Subject, ListID: m.ListID,
		HasAttachments: m.HasAttachments,
	}
	switch k := api.BulkKind(m.Bulk); k {
	case api.BulkNewsletter, api.BulkList, api.BulkAutomated:
		rm.Bulk = k
	}
	if body {
		text, ok, err := b.store.RuleBodyText(ctx, m.AccountID, m.ID, api.MaxRuleBodyBytes)
		if err == nil && ok {
			rm.Body, rm.HasBody = rules.Truncate(text, api.MaxRuleBodyBytes), true
		}
	}
	return rm
}
