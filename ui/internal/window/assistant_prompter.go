// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
)

// The prompts of the assistant come from the daemon (assistant.prompt,
// docs/api.md §4.18, docs/plans/ai-api.md D6): the panel, the compose
// window's rewrite and writing bar, the search in the user's own words,
// the board's triage and suggested reply ask through one Prompter over
// the application's RPC client, whichever provider then runs the model
// (Claude Code, Codex, or the daemon itself over an ai account).

// errNoDaemon is the prompter's answer before the RPC client exists.
var errNoDaemon = errors.New("the daemon is not connected")

// prompter is the Prompter over the application's RPC client
// (assistantpanel.NewPrompter), which AttachBoardTriage gives; before
// that every prompt fails with errNoDaemon. It may be called from any
// goroutine.
func (a *Assistant) prompter() assistantpanel.Prompter { return lazyPrompter{a} }

type lazyPrompter struct{ a *Assistant }

func (p lazyPrompter) Prompt(ctx context.Context, params api.AssistantPromptParams) (api.PromptResult, error) {
	rpc := p.a.rpc.Load()
	if rpc == nil {
		return api.PromptResult{}, errNoDaemon
	}
	return assistantpanel.NewPrompter(rpc).Prompt(ctx, params)
}
