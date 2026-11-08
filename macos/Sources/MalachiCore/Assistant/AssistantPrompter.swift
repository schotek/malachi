// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/assistantpanel/provider.go `Prompter` and `NewPrompter`: the
// daemon builds every prompt of the assistant (docs/api.md §4.18
// assistant.prompt, docs/plans/ai-api.md D6): the system prompt with the
// user's profile, the tools, the bridge's arguments, the answer's schema
// and the message of a turn. A provider that runs the model itself (Claude
// Code, Codex) takes them from here; a session the daemon runs over an
// `ai` account needs none.

import Foundation

/// assistantpanel.Prompter: builds the prompt of a purpose.
public protocol AssistantPrompter: AnyObject, Sendable {
    func prompt(_ params: AssistantPromptParams) async throws -> PromptResult
}

/// assistantpanel.daemonPrompter: the prompter over the daemon
/// (`API.AssistantPrompt`, 15 s a call).
public final class DaemonPrompter: AssistantPrompter {
    private let client: RPCClient

    public init(client: RPCClient) {
        self.client = client
    }

    public func prompt(_ params: AssistantPromptParams) async throws -> PromptResult {
        try await client.call(API.AssistantPrompt.self, params)
    }
}
