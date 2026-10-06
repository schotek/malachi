// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Provider adaptation of ui/internal/assistantpanel/controller.go and
// macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift;
// docs/chatgpt-integration.md §3. UI callbacks stay on the controller scope.
// The board's use (usesBoardConsent, providerModelID, the call's tools,
// timeout and model) is macos/Sources/MalachiCore/Controllers/
// AssistantRequest.swift runProvider's; a provider starts the bridge and its
// socket from its own options, so only the call's tools, bridge arguments
// and policy reach it.

using System;
using System.Collections.Generic;
using System.IO;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Assistants;

namespace Malachi.Core.Controllers;

public sealed partial class AssistantRequest
{
    private IAssistantProvider? provider;
    private IAssistantSession? providerSession;
    private CancellationTokenSource? providerStop;

    /// <summary>
    /// Whether a provider's request needs the provider's board consent
    /// (<see cref="IAssistantProvider.HasBoardConsent"/>) instead of the
    /// panel's: the board's triage and suggested reply. Without it the
    /// request is declined at once, never asked here.
    /// </summary>
    public bool UsesBoardConsent { get; set; }

    /// <summary>The provider's model of the next request (the board's setting); null or "" lets the provider choose.</summary>
    public Func<string>? ProviderModelId { get; set; }

    /// <summary>Optional runtime; changing it cancels the request and its late events.</summary>
    public IAssistantProvider? Provider
    {
        get => provider;
        set
        {
            scope.VerifyAccess();
            if (ReferenceEquals(provider, value)) { return; }
            Cancel();
            provider = value;
        }
    }

    private async Task RunProviderAsync(IAssistantProvider selected, int my, Call call)
    {
        var completion = call.Completion;
        try
        {
            var stop = CancellationTokenSource.CreateLinkedTokenSource(scope.Lifetime);
            providerStop = stop;
            StartTimer(my, call.Timeout, completion);
            var session = await selected.OpenAsync(new AssistantSessionSpec
            {
                SystemPrompt = call.SystemPrompt,
                JsonSchema = call.JsonSchema,
                ToolPolicy = call.Tools?.Policy ?? AssistantToolPolicy.None,
                Tools = call.Tools?.Allowed,
                BridgeArgs = call.Tools?.BridgeArgs ?? [],
                ModelId = ProviderModelId?.Invoke() ?? "",
                Timeout = call.Timeout,
                BoardConsent = UsesBoardConsent,
            }, stop.Token);
            scope.Pending.Track(session.Completion);
            if (my != gen || !Running || !ReferenceEquals(selected, Provider))
            {
                session.Terminate();
                return;
            }
            providerSession = session;
            blocks = "";
            streamed = "";
            session.EventsReceived += (_, batch) =>
            {
                if (ReferenceEquals(session, providerSession) && my == gen && Running)
                {
                    HandleEvents(my, batch, call);
                }
            };
            session.Exited += (_, exit) =>
            {
                if (ReferenceEquals(session, providerSession) && my == gen && Running)
                {
                    Finish(my, new Outcome.Failed(new Failure.Stopped(exit.Description)), completion);
                }
            };
            await session.SubmitAsync(call.Message, stop.Token);
        }
        catch (OperationCanceledException)
        {
            if (my == gen && Running) { Finish(my, new Outcome.Failed(new Failure.Stopped("chatgpt_request_cancelled")), completion); }
        }
        catch (Exception e) when (e is AssistantProviderException or IOException or UnauthorizedAccessException or InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            if (my == gen && Running)
            {
                Finish(my, new Outcome.Failed(new Failure.Stopped(e is AssistantProviderException failure ? failure.Code : "chatgpt_runtime_failed")), completion);
            }
        }
    }

    private void EndProviderSession()
    {
        var session = providerSession;
        providerSession = null;
        session?.Terminate();
        var stop = providerStop;
        providerStop = null;
        stop?.Cancel();
        stop?.Dispose();
    }
}
