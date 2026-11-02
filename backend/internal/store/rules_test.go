// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func testRuleInput(name string, account api.AccountID, enabled bool) api.RuleInput {
	return api.RuleInput{
		Name: name, Enabled: enabled, AccountID: account, Match: api.RuleMatchAll,
		Conditions: []api.RuleCondition{{Field: api.RuleFieldSubject, Op: api.RuleOpContains, Value: "x"}},
		Actions:    []api.RuleAction{{Kind: api.RuleActionFlag}},
	}
}

func TestRulesCRUD(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Unix(1_800_000_000, 0)
	a, err := s.CreateRule(ctx, testRuleInput("a", "", true), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.ID) != 34 || a.ID[:2] != "r_" || !a.EnabledAt.Equal(now) || a.Outgoing || a.Applied != 0 || a.LastAppliedAt != nil {
		t.Fatalf("created %+v", a)
	}
	in := testRuleInput("b", "acc_1", false)
	in.Actions = append(in.Actions, api.RuleAction{Kind: api.RuleActionForward, To: "x@y.zz"})
	b, err := s.CreateRule(ctx, in, now)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Outgoing || !b.EnabledAt.IsZero() || b.AccountID != "acc_1" {
		t.Fatalf("created %+v", b)
	}

	// Enabling sets EnabledAt; the check sees the stored rule and can
	// refuse.
	later := now.Add(time.Hour)
	refuse := errors.New("no")
	if _, err := s.UpdateRule(ctx, b.ID, testRuleInput("b2", "acc_1", true), later, func(old Rule) error {
		if old.Name != "b" {
			t.Errorf("old %+v", old)
		}
		return refuse
	}); !errors.Is(err, refuse) {
		t.Fatalf("check: %v", err)
	}
	if ok, err := s.DisableRule(ctx, a.ID, api.RuleProblemFolderGone, later); err != nil || !ok {
		t.Fatalf("disable %v %v", ok, err)
	}
	if ok, _ := s.DisableRule(ctx, a.ID, api.RuleProblemFolderGone, later); ok {
		t.Error("disabled twice")
	}
	got, _ := s.GetRule(ctx, a.ID)
	if got.Enabled || got.Problem != api.RuleProblemFolderGone {
		t.Fatalf("disabled %+v", got)
	}
	got, err = s.UpdateRule(ctx, a.ID, testRuleInput("a", "", true), later, nil)
	if err != nil || !got.Enabled || got.Problem != "" || !got.EnabledAt.Equal(later) {
		t.Fatalf("re-enabled %+v %v", got, err)
	}
	// Staying enabled keeps EnabledAt.
	got, _ = s.UpdateRule(ctx, a.ID, testRuleInput("a3", "", true), later.Add(time.Hour), nil)
	if !got.EnabledAt.Equal(later) || got.Name != "a3" {
		t.Fatalf("kept %+v", got)
	}
	if _, err := s.UpdateRule(ctx, "r_nope", testRuleInput("x", "", true), later, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("update unknown: %v", err)
	}

	// Counts.
	if err := s.NoteRulesApplied(ctx, map[string]int{a.ID: 3, b.ID: 0}, later); err != nil {
		t.Fatal(err)
	}
	if err := s.NoteRuleSkipped(ctx, a.ID, 2); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetRule(ctx, a.ID)
	if got.Applied != 3 || got.Skipped != 2 || got.LastAppliedAt == nil || !got.LastAppliedAt.Equal(later) {
		t.Fatalf("counts %+v", got)
	}

	// Order.
	list, _ := s.ListRules(ctx)
	if len(list) != 2 || list[0].ID != a.ID || list[1].ID != b.ID {
		t.Fatalf("order %+v", list)
	}
	for _, bad := range [][]string{{a.ID}, {a.ID, a.ID}, {a.ID, "r_x"}, {a.ID, b.ID, "r_x"}} {
		if _, err := s.ReorderRules(ctx, bad); !errors.Is(err, ErrRuleOrder) {
			t.Errorf("reorder %v: %v", bad, err)
		}
	}
	list, err = s.ReorderRules(ctx, []string{b.ID, a.ID})
	if err != nil || list[0].ID != b.ID {
		t.Fatalf("reorder %+v %v", list, err)
	}
	c, _ := s.CreateRule(ctx, testRuleInput("c", "", true), now)
	list, _ = s.ListRules(ctx)
	if list[2].ID != c.ID {
		t.Fatalf("a new rule goes last: %+v", list)
	}
	if err := s.DeleteRule(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRule(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}

func TestRulesLimit(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	for i := 0; i < api.MaxRules; i++ {
		if _, err := s.CreateRule(ctx, testRuleInput("r", "", false), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateRule(ctx, testRuleInput("r", "", false), time.Now()); !errors.Is(err, ErrRuleLimit) {
		t.Fatalf("over the limit: %v", err)
	}
}

func TestRuleLedgers(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Unix(1_800_000_000, 0)
	r, _ := s.CreateRule(ctx, testRuleInput("r", "acc", true), now)

	// Seen: once per key, also under concurrency.
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.ClaimRuleSeen(ctx, "acc", "<m1@x>", now)
			if err != nil {
				t.Error(err)
			}
			if ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d claims won", wins)
	}
	if ok, _ := s.ClaimRuleSeen(ctx, "other", "<m1@x>", now); !ok {
		t.Error("the key is per account")
	}

	// The hourly limit.
	for i := 0; i < 3; i++ {
		v, err := s.ReserveRuleOutgoing(ctx, r.ID, "", now, 3, time.Hour)
		if err != nil || v != OutgoingAllowed {
			t.Fatalf("%d: %v %v", i, v, err)
		}
	}
	if v, _ := s.ReserveRuleOutgoing(ctx, r.ID, "", now, 3, time.Hour); v != OutgoingHourly {
		t.Fatalf("over the hour: %v", v)
	}
	if v, _ := s.ReserveRuleOutgoing(ctx, r.ID, "", now.Add(time.Hour+time.Second), 3, time.Hour); v != OutgoingAllowed {
		t.Fatalf("an hour later: %v", v)
	}
	// The reply interval.
	later := now.Add(2 * time.Hour)
	if v, _ := s.ReserveRuleOutgoing(ctx, r.ID, "a@b.c", later, 20, 4*24*time.Hour); v != OutgoingAllowed {
		t.Fatal(v)
	}
	if v, _ := s.ReserveRuleOutgoing(ctx, r.ID, "a@b.c", later.Add(time.Hour), 20, 4*24*time.Hour); v != OutgoingReplied {
		t.Fatalf("again: %v", v)
	}
	if v, _ := s.ReserveRuleOutgoing(ctx, r.ID, "other@b.c", later, 20, 4*24*time.Hour); v != OutgoingAllowed {
		t.Fatalf("another sender: %v", v)
	}
	if v, _ := s.ReserveRuleOutgoing(ctx, r.ID, "a@b.c", later.Add(4*24*time.Hour), 20, 4*24*time.Hour); v != OutgoingAllowed {
		t.Fatalf("after the interval: %v", v)
	}

	if err := s.PruneRuleLedgers(ctx, later.Add(10*24*time.Hour), 4*24*time.Hour, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`SELECT COUNT(*) FROM rule_outgoing`, `SELECT COUNT(*) FROM rule_replies`, `SELECT COUNT(*) FROM rule_seen`} {
		if n := countRows(t, s, q); n != 0 {
			t.Errorf("%s: %d after the prune", q, n)
		}
	}
}

// Removing an account deletes its rules and their ledgers; rules for every
// account stay.
func TestRulesGoWithTheAccount(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	acc := Account{ID: "acc", Name: "Mail", Enabled: true, Config: api.AccountConfig{Name: "Mail", Email: "me@example.org"}}
	if err := s.AddAccount(ctx, &acc); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	own, _ := s.CreateRule(ctx, testRuleInput("own", "acc", true), now)
	all, _ := s.CreateRule(ctx, testRuleInput("all", "", true), now)
	_, _ = s.ReserveRuleOutgoing(ctx, own.ID, "a@b.c", now, 20, time.Hour)
	_, _ = s.ClaimRuleSeen(ctx, "acc", "k", now)
	if err := s.DeleteAccount(ctx, "acc", true); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListRules(ctx)
	if len(list) != 1 || list[0].ID != all.ID {
		t.Fatalf("left %+v", list)
	}
	for _, q := range []string{`SELECT COUNT(*) FROM rule_outgoing`, `SELECT COUNT(*) FROM rule_replies`, `SELECT COUNT(*) FROM rule_seen`} {
		if n := countRows(t, s, q); n != 0 {
			t.Errorf("%s: %d", q, n)
		}
	}
}
