// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package assistant builds what the model reads: the system prompts, the
// message of a turn, the tools the model may call and the arguments the
// MCP bridge is started with, for every purpose of the assistant
// (api.PromptPurpose; docs/plans/ai-api.md D6 and §6.3). The daemon's own
// sessions over an ai account use it directly, and assistant.prompt gives
// the same result to the clients that run Claude Code or Codex
// themselves, so every path offers the model one and the same text.
//
// The package is pure: no store, no clock, no network, no logging. Every
// text here is for the model, in English, like the bridge's server
// instructions; none is shown to the user and none is translated (the
// backend stays language-neutral: the language the model answers in is a
// parameter of the client). The texts moved here byte for byte from
// ui/internal/assistant, whose golden file (testdata/prompts.golden) the
// parity test replays; a change of a prompt is a change of the golden.
//
// Build is the one entry point. The tools are named bare (read_message),
// as the bridge registers them; a client that runs Claude Code prefixes
// them with its MCP server's name itself.
package assistant
