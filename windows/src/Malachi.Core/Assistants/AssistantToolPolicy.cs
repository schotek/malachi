// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Use-case policy from docs/chatgpt-integration.md §6; ui/internal/assistant/
// claude.go, triage.go and suggest_reply.go, and
// macos/Sources/MalachiCore/Assistant/AssistantPanel.swift,
// AssistantTriage.swift and AssistantSuggestReply.swift are the reference
// for the semantic allow lists (Assistant.PolicyTools). Swift's Codex
// session takes the request's allowed tools as they come; here the policy
// is the fixed authority they must fit in (Assistant.PolicyAllows).

namespace Malachi.Core.Assistants;

/// <summary>Immutable tool authority of one isolated provider session.</summary>
public enum AssistantToolPolicy
{
    /// <summary>Rewrite/search conversion: no bridge or external tools.</summary>
    None,

    /// <summary>The panel's read operations and create_draft, never send/modify.</summary>
    Panel,

    /// <summary>
    /// An automatic board triage run: the panel's read operations and the
    /// three triage tools, no create_draft (<see cref="Assistant.TriageTools"/>
    /// without drafts); the bridge gets <see cref="Assistant.TriageBridgeArgs"/>.
    /// </summary>
    Triage,

    /// <summary>
    /// A manual board triage run: <see cref="Triage"/> with create_draft
    /// (<see cref="Assistant.TriageTools"/> with drafts).
    /// </summary>
    TriageDrafts,

    /// <summary>
    /// The board's suggested reply: <see cref="Assistant.SuggestReplyTools"/>,
    /// the bridge started with <see cref="Assistant.SuggestReplyBridgeArgs"/>.
    /// </summary>
    ReplyOnly,
}
