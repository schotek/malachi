// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Provider adaptation of ui/internal/assistantpanel/controller.go and
// macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift;
// docs/chatgpt-integration.md §3. UI callbacks stay on the controller scope.
// The board's use (usesBoardConsent, providerModelID, the call's tools,
// timeout and model) is macos/Sources/MalachiCore/Controllers/
// AssistantRequest.swift launchProvider's: the session is opened with the
// call's resolved prompt (the daemon's system prompt, schema, tools and
// bridge arguments) and its purpose, input and bound, and the input is
// submitted (Codex takes the turn's message from the daemon itself); a
// provider starts the bridge and its socket from its own options, so only
// the prompt's tools, bridge arguments and the call's policy reach it. A provider's failure codes are classified as Go's
// assistantpanel.ProviderFailure does (ProviderFailure).

using System;
using System.Collections.Generic;
using System.IO;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
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

    /// <summary>
    /// The failure a provider's code stands for (an error of opening or
    /// submitting, a failed result's text, an exit's reason): Codex missing
    /// is <see cref="Failure.NotFound"/>; a ChatGPT connection that is
    /// missing, lapsed, refused or without consent is
    /// <see cref="Failure.NotSignedIn"/> with the code (the board offers to
    /// connect again, as it offers Claude Code's sign-in); the plan's usage
    /// limit is <see cref="Failure.Limit"/>; anything else
    /// <see cref="Failure.Stopped"/> with the code as it is. The reasons of a
    /// session the daemon runs over an ai account are "api_&lt;class&gt;"
    /// (<see cref="Assistant.ApiFailurePrefix"/>): a missing consent or a
    /// refused key is <see cref="Failure.NotSignedIn"/> with the reason (the
    /// panel offers Set Up in Integrations…), a rate limit, an overloaded
    /// provider or an account without credit <see cref="Failure.Limit"/>, a
    /// model without tools, a server without the model or a bridge that
    /// could not start <see cref="Failure.ToolsMissing"/> with the reason,
    /// the rest <see cref="Failure.Stopped"/> (Go and Swift
    /// <c>ProviderFailure</c>).
    /// </summary>
    public static Failure ProviderFailure(string code) => code switch
    {
        "codex_not_found" => new Failure.NotFound(),
        "chatgpt_not_connected" or "chatgpt_reconnect_required" or "chatgpt_consent_required"
            or "chatgpt_permission_denied" or "chatgpt_identity_mismatch"
            or "api_consent_required" or "api_auth" => new Failure.NotSignedIn(code),
        "chatgpt_usage_limit" or "api_rateLimit" or "api_overloaded" or "api_quota" => new Failure.Limit(code),
        "api_toolsUnsupported" or "api_bridge" or "api_model" => new Failure.ToolsMissing(code),
        _ => new Failure.Stopped(code),
    };

    // assistantpanel.launchProvider: a one-shot request through the provider:
    // a session opened with the call's resolved prompt (the purpose's input
    // given with the start; a provider that takes its turns by SubmitAsync
    // alone ignores it), its input submitted.
    private async Task RunProviderAsync(IAssistantProvider selected, int my, Job job, PromptResult prompt)
    {
        var completion = job.Completion;
        var call = job.Call;
        try
        {
            var stop = CancellationTokenSource.CreateLinkedTokenSource(scope.Lifetime);
            providerStop = stop;
            StartTimer(my, job.Timeout, completion);
            var session = await selected.OpenAsync(new AssistantSessionSpec
            {
                Purpose = call.Purpose,
                Language = Language(),
                NoteAccountId = call.NoteAccountId,
                Trigger = call.Trigger,
                RunId = call.RunId,
                BridgePath = call.Tools?.Bridge ?? "",
                Input = call.Purpose == PromptPurpose.Panel ? null : call.Input,
                SystemPrompt = prompt.SystemPrompt,
                JsonSchema = prompt.JsonSchemaText,
                ToolPolicy = call.Tools?.Policy ?? AssistantToolPolicy.None,
                Tools = call.Tools is null ? null : Assistant.PrefixTools(prompt.Tools),
                BridgeArgs = call.Tools is null ? [] : prompt.BridgeArgs,
                ModelId = call.ModelId.Length > 0 ? call.ModelId : ProviderModelId?.Invoke() ?? "",
                Timeout = job.Timeout,
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
                    HandleEvents(my, batch, job);
                }
            };
            session.Exited += (_, exit) =>
            {
                if (ReferenceEquals(session, providerSession) && my == gen && Running)
                {
                    Finish(my, new Outcome.Failed(ProviderFailure(exit.Description)), completion);
                }
            };
            await session.SubmitAsync(call.Input, stop.Token);
        }
        catch (OperationCanceledException)
        {
            if (my == gen && Running) { Finish(my, new Outcome.Failed(new Failure.Stopped("chatgpt_request_cancelled")), completion); }
        }
        catch (Exception e) when (e is AssistantProviderException or IOException or UnauthorizedAccessException or InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            if (my == gen && Running)
            {
                Finish(my, new Outcome.Failed(ProviderFailure(e is AssistantProviderException failure ? failure.Code : "chatgpt_runtime_failed")), completion);
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
