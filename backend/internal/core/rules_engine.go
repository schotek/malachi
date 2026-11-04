// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/schotek/malachi/backend/internal/rules"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Running the mail rules on stored mail (docs/api.md §4.21, R1–R9).
//
// Automatically: noteRules is the last step of the mail syncers' Stored
// hook (afterMailStored), so it runs before the syncer announces the
// message (notify.newMessage reads the message again and leaves out one
// that is no longer in its folder or is read). Only a mail account's
// inbox, each Message-ID once (rule_seen), never mail older than the day
// before the rule was enabled. The local actions go through the store's
// own paths (FlagMessages, MoveMessages, TrashMessages) at once, as the
// user's would; the outgoing ones are checked against the fixed
// protections here and queued for rules_outgoing.go.
//
// By hand: rules.apply plans a folder's or a selection's messages in
// batches of api.MaxMessageIDsPerCall and runs the local actions only.

// rulesFor returns the enabled rules that run on the account's mail, in
// their order.
func (b *Backend) rulesFor(ctx context.Context, accountID string) ([]store.Rule, error) {
	list, err := b.cachedRules(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.Rule
	for _, r := range list {
		if r.Enabled && (r.AccountID == "" || string(r.AccountID) == accountID) {
			out = append(out, r)
		}
	}
	return out, nil
}

// noteRules is the Stored hook's part (see the file comment). It never
// fails: a message the rules cannot judge is left as it is.
func (b *Backend) noteRules(ctx context.Context, accountID, messageID string) {
	list, err := b.rulesFor(ctx, accountID)
	if err != nil {
		if !isCancelled(err) {
			b.log.Warn("rules: read the rules", "err", err)
		}
		return
	}
	if len(list) == 0 {
		return
	}
	a, err := b.store.GetAccount(ctx, accountID)
	if err != nil || !isMailKind(a) || !a.Enabled {
		return
	}
	m, err := b.store.GetMessage(ctx, accountID, messageID)
	if err != nil {
		return
	}
	f, err := b.store.GetFolder(ctx, accountID, m.FolderID)
	if err != nil || f.Role != api.RoleInbox {
		return
	}
	arrived := m.InternalDate
	if arrived.IsZero() {
		arrived = m.Date
	}
	now := b.rulesNow()
	if arrived.IsZero() {
		arrived = now
	}
	// A rule runs on mail that arrived at most a day before it was
	// enabled, and never on mail from before the account was added (the
	// first sync of an account added after a rule for every account).
	var inTime []store.Rule
	for _, r := range list {
		if r.EnabledAt.IsZero() {
			continue
		}
		from := r.EnabledAt.Add(-ruleEnabledSlack)
		if created := a.CreatedAt.Truncate(time.Second); created.After(from) {
			from = created
		}
		if !arrived.Before(from) {
			inTime = append(inTime, r)
		}
	}
	if len(inTime) == 0 {
		return // backfill, the first sync, or mail from before the rules
	}
	claimed, err := b.store.ClaimRuleSeen(ctx, accountID, store.RuleMessageKey(m), now)
	if err != nil {
		if !isCancelled(err) {
			b.log.Warn("rules: record the message", "err", err)
		}
		return
	}
	if !claimed {
		return // its Message-ID went through the rules before
	}
	inputs := apiRules(inTime)
	steps := rules.Plan(inputs, b.ruleMessage(ctx, m, rules.NeedsBody(inputs)))
	if len(steps) == 0 {
		return
	}
	eff := rules.EffectsOf(steps)
	run := b.newRuleRun(a)

	// The outgoing actions are judged before the message moves. The worker
	// reads a forward's original by id, after its limits; only when the
	// message goes to a folder that is never downloaded (Gmail's All
	// Mail), which takes the row along, is the original read here, when
	// it is whole on disk and the queue's memory allows.
	var jobs []ruleOutJob
	skipped := map[string]int{}
	if len(eff.Outgoing) > 0 {
		target, unsynced := run.placeRole(ctx, eff.Place)
		self := b.allSelfAddresses(ctx, a)
		for _, o := range eff.Outgoing {
			if reason := ruleOutgoingRefusal(m, f.Role, target, o.Action, self); reason != "" {
				skipped[o.RuleID]++
				b.log.Info("rules: outgoing action refused", "rule", o.RuleID, "kind", o.Action.Kind, "reason", reason)
				continue
			}
			job := ruleOutJob{ruleID: o.RuleID, action: o.Action, accountID: a.ID, msg: m}
			if o.Action.Kind == api.RuleActionForward && unsynced {
				if raw, ok := b.storedWhole(ctx, a.ID, m); ok && int64(len(raw)) <= quotedPartLimit {
					job.raw = raw
				}
			}
			jobs = append(jobs, job)
		}
	}

	if _, err := run.apply(ctx, []rulePlan{{m: m, eff: eff}}); err != nil && !isCancelled(err) {
		b.log.Warn("rules: actions on a new message", "err", err)
	}
	for _, j := range jobs {
		b.ruleEnqueue(j)
	}
	b.noteRuleCounts(ctx, steps, skipped, now)
}

// noteRuleCounts records which rules acted (applied) and how many outgoing
// actions they had refused, and tells the clients.
func (b *Backend) noteRuleCounts(ctx context.Context, steps []rules.Step, skipped map[string]int, now time.Time) {
	applied := map[string]int{}
	for _, st := range steps {
		applied[st.RuleID]++
	}
	b.noteRuleTotals(ctx, applied, skipped, now)
}

func (b *Backend) noteRuleTotals(ctx context.Context, applied, skipped map[string]int, now time.Time) {
	if len(applied) == 0 && len(skipped) == 0 {
		return
	}
	if err := b.store.NoteRulesApplied(ctx, applied, now); err != nil {
		b.log.Warn("rules: count", "err", err)
	}
	for id, n := range skipped {
		if err := b.store.NoteRuleSkipped(ctx, id, n); err != nil {
			b.log.Warn("rules: count", "err", err)
		}
	}
	b.rulesChanged()
}

// rulePlan is one message and what its rules do to it.
type rulePlan struct {
	m   store.Message
	eff rules.Effects
}

// ruleRun carries the local actions of one account's messages out,
// resolving the target folders once.
type ruleRun struct {
	b       *Backend
	a       store.Account
	folders map[string]store.Folder
	loaded  bool
	roles   map[api.FolderRole]*store.Folder
	gone    map[string]bool // rules disabled by this run
}

func (b *Backend) newRuleRun(a store.Account) *ruleRun {
	return &ruleRun{b: b, a: a, roles: map[api.FolderRole]*store.Folder{}, gone: map[string]bool{}}
}

// folder looks one of the account's folders up; an error when the store
// could not say (then nothing is decided about the folder).
func (r *ruleRun) folder(ctx context.Context, id string) (store.Folder, bool, error) {
	if !r.loaded {
		list, err := r.b.store.ListFolders(ctx, r.a.ID)
		if err != nil {
			return store.Folder{}, false, err
		}
		r.folders = make(map[string]store.Folder, len(list))
		for _, f := range list {
			r.folders[f.ID] = f
		}
		r.loaded = true
	}
	f, ok := r.folders[id]
	return f, ok, nil
}

// byRole is the account's folder of a role; nil without one.
func (r *ruleRun) byRole(ctx context.Context, role api.FolderRole) *store.Folder {
	if f, ok := r.roles[role]; ok {
		return f
	}
	var out *store.Folder
	if f, err := r.b.store.FolderByRole(ctx, r.a.ID, role); err == nil && f.Selectable {
		out = &f
	}
	r.roles[role] = out
	return out
}

// target is the folder a placing action sends the message to; false when
// it has none (no trash or junk folder: the action is skipped; a move's
// folder gone: its rule is disabled with folderGone).
func (r *ruleRun) target(ctx context.Context, act *api.RuleAction, ruleID string) (store.Folder, bool) {
	switch act.Kind {
	case api.RuleActionTrash:
		if f := r.byRole(ctx, api.RoleTrash); f != nil {
			return *f, true
		}
	case api.RuleActionJunk:
		if f := r.byRole(ctx, api.RoleJunk); f != nil {
			return *f, true
		}
	case api.RuleActionMove:
		f, ok, err := r.folder(ctx, string(act.FolderID))
		if err != nil {
			return store.Folder{}, false // the store could not say: not gone
		}
		if ok && ruleMoveTarget(f) {
			return f, true
		}
		if !r.gone[ruleID] {
			r.gone[ruleID] = true
			r.b.disableRule(ctx, ruleID, api.RuleProblemFolderGone)
		}
	}
	return store.Folder{}, false
}

// placeRole is the role of the folder a placing action would send the
// message to ("" for none or an ordinary folder) and whether that folder
// is never downloaded, without disabling anything.
func (r *ruleRun) placeRole(ctx context.Context, act *api.RuleAction) (api.FolderRole, bool) {
	if act == nil {
		return "", false
	}
	switch act.Kind {
	case api.RuleActionJunk:
		return api.RoleJunk, false
	case api.RuleActionTrash:
		return api.RoleTrash, false
	}
	if f, ok, _ := r.folder(ctx, string(act.FolderID)); ok {
		return f.Role, f.Unsynced
	}
	return "", false
}

// disableRule stops a rule the daemon cannot run any more.
func (b *Backend) disableRule(ctx context.Context, id string, problem api.RuleProblem) {
	ok, err := b.store.DisableRule(ctx, id, problem, b.rulesNow())
	if err != nil {
		b.log.Warn("rules: disable a rule", "rule", id, "err", err)
		return
	}
	if ok {
		b.log.Info("rule disabled", "rule", id, "problem", string(problem))
		b.rulesWritten()
	}
}

// apply carries the local effects of the plans out: read and star first,
// then the moves, each kind as one store call per target where it can.
// It returns how many messages an action changed. A message that went
// meanwhile is left out; the server learns the rest as the user's own
// changes (Supervisor.Trigger), the clients through
// notify.messagesChanged.
func (r *ruleRun) apply(ctx context.Context, plans []rulePlan) (int, error) {
	changed := map[string]bool{}
	touched := map[string]bool{}
	var read, star []string
	type move struct {
		target store.Folder
		trash  bool
		ids    []string
	}
	moves := map[string]*move{}
	var order []string
	for _, p := range plans {
		m := p.m
		src, ok, err := r.folder(ctx, m.FolderID)
		if err != nil {
			return 0, err
		}
		if !ok || src.Role == api.RoleOutbox || src.Role == api.RoleDrafts {
			continue
		}
		if p.eff.MarkRead && !slices.Contains(m.Flags, api.FlagSeen) {
			read = append(read, m.ID)
		}
		if p.eff.Flag && !slices.Contains(m.Flags, api.FlagFlagged) {
			star = append(star, m.ID)
		}
		if p.eff.Place == nil {
			continue
		}
		t, ok := r.target(ctx, p.eff.Place, p.eff.PlaceRule)
		if !ok || t.ID == m.FolderID {
			continue
		}
		key := string(p.eff.Place.Kind) + "\x00" + t.ID
		mv := moves[key]
		if mv == nil {
			mv = &move{target: t, trash: p.eff.Place.Kind == api.RuleActionTrash}
			moves[key] = mv
			order = append(order, key)
		}
		mv.ids = append(mv.ids, m.ID)
		touched[m.FolderID] = true
	}
	folderOf := map[string]string{}
	for _, p := range plans {
		folderOf[p.m.ID] = p.m.FolderID
	}
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, set := range []struct {
		ids  []string
		flag api.Flag
	}{{read, api.FlagSeen}, {star, api.FlagFlagged}} {
		done, err := r.inGroup(set.ids, func(ids []string) error {
			return r.b.store.FlagMessages(ctx, r.a.ID, ids, []api.Flag{set.flag}, nil)
		})
		note(err)
		for _, id := range done {
			changed[id] = true
			touched[folderOf[id]] = true
		}
	}
	for _, key := range order {
		mv := moves[key]
		done, err := r.inGroup(mv.ids, func(ids []string) error {
			var dropped []string
			var err error
			if mv.trash {
				dropped, err = r.b.store.TrashMessages(ctx, r.a.ID, ids, mv.target.ID)
			} else {
				dropped, err = r.b.store.MoveMessages(ctx, r.a.ID, ids, mv.target.ID)
			}
			if err == nil {
				r.b.boardDraftsDropped(ctx, r.a.ID, dropped)
			}
			return err
		})
		note(err)
		for _, id := range done {
			changed[id] = true
		}
		if len(done) > 0 {
			touched[mv.target.ID] = true
		}
	}
	if len(changed) > 0 {
		r.b.Supervisor.Trigger(r.a.ID, "", false)
		folders := make([]string, 0, len(touched))
		for f := range touched {
			if f != "" {
				folders = append(folders, f)
			}
		}
		slices.Sort(folders)
		r.b.messagesChanged(map[string][]string{r.a.ID: folders})
	}
	return len(changed), firstErr
}

// inGroup runs change on all ids at once and, when the store refuses the
// group because one of them went meanwhile, on each id alone, leaving the
// gone ones out. It returns the ids changed.
func (r *ruleRun) inGroup(ids []string, change func(ids []string) error) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	err := change(ids)
	if err == nil {
		return ids, nil
	}
	if !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrOutbox) {
		return nil, err
	}
	var done []string
	var firstErr error
	for _, id := range ids {
		switch err := change([]string{id}); {
		case err == nil:
			done = append(done, id)
		case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrOutbox):
		default:
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return done, firstErr
}

// Apply runs rules by hand on stored mail of one account: the local
// actions only (R9), without rule_seen and the age limit.
func (s *rulesService) Apply(ctx context.Context, p api.RulesApplyParams) (*api.RulesApplyResult, error) {
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	if !isMailKind(a) {
		return nil, api.NewError(api.CodeInvalidArgument, "rules run on mail accounts only")
	}
	if (p.FolderID == "") == (len(p.MessageIDs) == 0) {
		return nil, api.NewError(api.CodeInvalidArgument, "exactly one of folderId and messageIds is required")
	}
	if len(p.MessageIDs) > api.MaxMessageIDsPerCall {
		return nil, api.NewError(api.CodeInvalidArgument, "at most %d messageIds", api.MaxMessageIDsPerCall)
	}
	list, err := s.b.applyRuleSet(ctx, a, p.RuleIDs)
	if err != nil {
		return nil, err
	}
	var ids []string
	if p.FolderID != "" {
		_, f, err := s.b.requireFolder(ctx, p.AccountID, p.FolderID)
		if err != nil {
			return nil, err
		}
		if f.Role == api.RoleOutbox || f.Role == api.RoleDrafts {
			return nil, api.NewError(api.CodeInvalidArgument, "rules do not run on the outbox or the drafts")
		}
		if ids, err = s.b.store.FolderMessageIDs(ctx, f.ID, false, false); err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
	} else {
		seen := map[string]bool{}
		for _, id := range p.MessageIDs {
			if id == "" {
				return nil, api.NewError(api.CodeInvalidArgument, "empty message id")
			}
			if seen[string(id)] {
				continue
			}
			seen[string(id)] = true
			if _, err := s.b.getMessage(ctx, a.ID, string(id)); err != nil {
				return nil, err
			}
			ids = append(ids, string(id))
		}
	}
	res := &api.RulesApplyResult{}
	if len(list) == 0 {
		res.Examined = len(ids)
		return res, nil
	}
	inputs := apiRules(list)
	body := rules.NeedsBody(inputs)
	run := s.b.newRuleRun(a)
	applied := map[string]int{}
	var firstErr error
	for len(ids) > 0 {
		n := min(len(ids), api.MaxMessageIDsPerCall)
		batch := ids[:n]
		ids = ids[n:]
		var plans []rulePlan
		for _, id := range batch {
			if err := ctx.Err(); err != nil {
				firstErr = api.NewError(api.CodeCancelled, "the call ended")
				break
			}
			m, err := s.b.store.GetMessage(ctx, a.ID, id)
			if err != nil {
				continue // gone meanwhile
			}
			res.Examined++
			steps := rules.Plan(inputs, s.b.ruleMessage(ctx, m, body))
			if len(steps) == 0 {
				continue
			}
			res.Matched++
			eff := rules.EffectsOf(steps)
			res.SkippedOutgoing += len(eff.Outgoing)
			for _, st := range steps {
				applied[st.RuleID]++
			}
			plans = append(plans, rulePlan{m: m, eff: eff})
		}
		changed, err := run.apply(ctx, plans)
		res.Changed += changed
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if firstErr != nil {
			break
		}
	}
	s.b.noteRuleTotals(ctx, applied, nil, s.b.rulesNow())
	if firstErr != nil {
		var ae *api.Error
		if errors.As(firstErr, &ae) {
			return nil, firstErr
		}
		return nil, mutationError(firstErr)
	}
	return res, nil
}

// applyRuleSet is the rules rules.apply runs on the account, in their
// order: those named (enabled or not; each must run on the account) or
// else every enabled rule of the account.
func (b *Backend) applyRuleSet(ctx context.Context, a store.Account, ids []string) ([]store.Rule, error) {
	if len(ids) == 0 {
		list, err := b.rulesFor(ctx, a.ID)
		if err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		return list, nil
	}
	all, err := b.cachedRules(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []store.Rule
	for _, r := range all {
		if !want[r.ID] {
			continue
		}
		delete(want, r.ID)
		if r.AccountID != "" && string(r.AccountID) != a.ID {
			return nil, api.NewError(api.CodeInvalidArgument, "rule %q is for another account", r.ID)
		}
		out = append(out, r)
	}
	for id := range want {
		return nil, api.NewError(api.CodeRuleNotFound, "unknown rule %q", id)
	}
	return out, nil
}

// pruneRuleLedgers is the hourly upkeep of the rules' ledgers.
func (b *Backend) pruneRuleLedgers(ctx context.Context) {
	if err := b.store.PruneRuleLedgers(ctx, b.rulesNow(), api.RuleReplyInterval, ruleSeenAge); err != nil && !isCancelled(err) {
		b.log.Warn("rules: prune the ledgers", "err", err)
	}
}
