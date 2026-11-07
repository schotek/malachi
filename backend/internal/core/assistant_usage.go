// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"time"

	"github.com/schotek/malachi/backend/internal/agent"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The tokens the daemon's assistant used (docs/api.md §4.18, D9): written
// after every turn to the store (assistant_usage, by account, local day
// and model), summed for a triage session's board run (D19), and read by
// assistant.usage.

// recordUsage writes what a turn added to the session's usage.
func (b *Backend) recordUsage(sess *agent.Session, info *sessionInfo, newSession bool) {
	total := sess.Usage()
	b.assist.mu.Lock()
	delta := api.AssistantUsage{
		InputTokens:              total.InputTokens - info.usage.InputTokens,
		OutputTokens:             total.OutputTokens - info.usage.OutputTokens,
		CacheReadInputTokens:     total.CacheReadInputTokens - info.usage.CacheReadInputTokens,
		CacheCreationInputTokens: total.CacheCreationInputTokens - info.usage.CacheCreationInputTokens,
	}
	info.usage = total
	if info.runID != "" {
		b.assist.runUsage[info.runID] = b.assist.runUsage[info.runID].Add(delta)
	}
	b.assist.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	day := time.Now().Format("2006-01-02")
	if err := b.store.AddAssistantUsage(ctx, string(info.account), day, info.model, delta, newSession); err != nil {
		b.log.Warn("record assistant usage", "err", err)
		return
	}
	if n, ok := b.getNotifier().(api.AssistantEventNotifier); ok {
		n.AssistantUsageChanged(api.AssistantUsageChangedNotification{AccountID: info.account})
	}
}

// Usage is assistant.usage.
func (s *assistantService) Usage(ctx context.Context, p api.AssistantUsageParams) (*api.AssistantUsageResult, error) {
	days := p.Days
	switch {
	case days == 0:
		days = 1
	case days < 0 || days > api.MaxAssistantUsageDays:
		return nil, api.NewError(api.CodeInvalidArgument, "days must be 0..%d", api.MaxAssistantUsageDays)
	}
	if p.AccountID != "" {
		a, err := s.b.requireAccount(ctx, string(p.AccountID))
		if err != nil {
			return nil, err
		}
		if a.Config.Protocol() != api.AccountAI {
			return nil, api.NewError(api.CodeInvalidArgument, "account %q is not an ai account", p.AccountID)
		}
	}
	since := time.Now().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.b.store.AssistantUsage(ctx, string(p.AccountID), since)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if rows == nil {
		rows = []api.AssistantUsageDay{}
	}
	return &api.AssistantUsageResult{Days: rows}, nil
}
