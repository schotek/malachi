// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"time"

	"github.com/schotek/malachi/backend/internal/assistant"
	"github.com/schotek/malachi/backend/pkg/api"
)

// assistant.prompt (docs/api.md §4.18, D6): the prompts the daemon builds
// for its own sessions, handed to the clients that run Claude Code or
// Codex themselves, so that every provider gets one text.

// promptOptions gathers what internal/assistant needs beside the input:
// the language, today's date in the daemon's zone, the triage run, the
// profile and the accounts for its notes.
func (b *Backend) promptOptions(ctx context.Context, language string, noteAccount api.AccountID, trigger api.BoardTrigger, runID string) (assistant.Options, error) {
	if language == "" {
		language = "en"
	}
	accounts, err := b.store.ListAccounts(ctx)
	if err != nil {
		return assistant.Options{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	sp, err := b.loadProfile(ctx)
	if err != nil {
		return assistant.Options{}, err
	}
	apiAccounts := make([]api.Account, 0, len(accounts))
	for _, a := range accounts {
		apiAccounts = append(apiAccounts, b.toAPIAccount(a))
	}
	return assistant.Options{
		Language:  language,
		Today:     time.Now().Format("2006-01-02"),
		Trigger:   trigger,
		RunID:     runID,
		Profile:   publicProfile(sp, accounts),
		Accounts:  apiAccounts,
		AccountID: noteAccount,
	}, nil
}

// Prompt is assistant.prompt.
func (s *assistantService) Prompt(ctx context.Context, p api.AssistantPromptParams) (*api.PromptResult, error) {
	if !p.Purpose.Valid() {
		return nil, api.NewError(api.CodeInvalidArgument, "unknown purpose %q", p.Purpose)
	}
	if p.AccountID != "" {
		if _, err := s.b.requireAccount(ctx, string(p.AccountID)); err != nil {
			return nil, err
		}
	}
	opts, err := s.b.promptOptions(ctx, p.Language, p.AccountID, p.Trigger, p.RunID)
	if err != nil {
		return nil, err
	}
	res, err := assistant.Build(p.Purpose, p.Input, opts)
	if err != nil {
		return nil, err
	}
	return &res, nil
}
