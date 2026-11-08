// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Use-case policy from docs/chatgpt-integration.md §6; the daemon's tools of
// each purpose (backend/internal/assistant tools.go, the local copies of Go
// ui/internal/chatgpt toolPolicy and Swift CodexPolicy) are the reference
// for the semantic allow lists (Assistant.PolicyTools). Here the policy is
// the fixed authority the daemon's prompt must fit in
// (Assistant.PolicyAllows).

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
    /// three triage tools, no create_draft; the bridge gets the daemon's
    /// <c>--allow-triage --triage-run &lt;runId&gt; --triage-max &lt;n&gt;</c>.
    /// </summary>
    Triage,

    /// <summary>
    /// A manual board triage run: <see cref="Triage"/> with create_draft.
    /// </summary>
    TriageDrafts,

    /// <summary>
    /// The board's suggested reply: read_message, list_messages and
    /// create_draft, the bridge started with the daemon's
    /// <c>--reply-only &lt;messageId&gt;</c>.
    /// </summary>
    ReplyOnly,
}
