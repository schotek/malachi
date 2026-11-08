// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantPrompter.swift
// (AssistantPrompter, DaemonPrompter); GTK: ui/internal/assistantpanel/
// provider.go (Prompter, NewPrompter, daemonPrompter). The daemon builds
// every prompt of the assistant (docs/api.md §4.18 assistant.prompt,
// docs/plans/ai-api.md D6): the system prompt with the user's profile, the
// tools, the bridge's arguments, the answer's schema and the message of a
// turn. A provider that runs the model itself (Claude Code, Codex) takes
// them from here; a session the daemon runs over an ai account needs none.
// PromptReason is assistantpanel.promptReason (Swift
// AssistantRequest.promptReason). This file holds no translatable text.

using System;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Transport;

namespace Malachi.Core.Assistants;

/// <summary>assistantpanel.Prompter: builds the prompt of a purpose.</summary>
public interface IAssistantPrompter
{
    /// <summary>
    /// The prompt of <paramref name="parameters"/>: a daemon error arrives
    /// as <see cref="RpcException"/> (an input it refuses is
    /// <see cref="ErrorCode.InvalidArgument"/>), a lost connection as
    /// <see cref="RpcClientException"/>.
    /// </summary>
    Task<PromptResult> PromptAsync(AssistantPromptParams parameters, CancellationToken cancellationToken);
}

/// <summary>
/// assistantpanel.daemonPrompter: the prompter over the daemon
/// (<see cref="API.AssistantPrompt"/>, <see cref="Platform.RpcTimeouts.AssistantPrompt"/> a call).
/// </summary>
public sealed class DaemonPrompter : IAssistantPrompter
{
    private readonly RpcClient client;

    /// <summary>The prompter over <paramref name="client"/>.</summary>
    public DaemonPrompter(RpcClient client)
    {
        ArgumentNullException.ThrowIfNull(client);
        this.client = client;
    }

    /// <inheritdoc/>
    public Task<PromptResult> PromptAsync(AssistantPromptParams parameters, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        return client.CallAsync(API.AssistantPrompt, parameters, cancellationToken);
    }
}

public static partial class Assistant
{
    /// <summary>
    /// assistantpanel.errNoPrompter: the reason of a request, or of a
    /// question of the panel, without a prompter.
    /// </summary>
    public const string NoPrompter = "the assistant has no prompter";

    /// <summary>
    /// assistantpanel.promptReason: a prompter's failure as a reason to show:
    /// the daemon's message of an <see cref="RpcException"/> (its code is
    /// nothing to the user), else the exception's message.
    /// </summary>
    public static string PromptReason(Exception e)
    {
        ArgumentNullException.ThrowIfNull(e);
        return e is RpcException rpc ? rpc.Error.Message : e.Message;
    }

    /// <summary>
    /// Whether <paramref name="e"/> is what a prompter fails with: the
    /// daemon's refusal, a lost or silent connection.
    /// </summary>
    public static bool IsPromptFailure(Exception e) =>
        e is RpcException or RpcClientException or TimeoutException;
}
