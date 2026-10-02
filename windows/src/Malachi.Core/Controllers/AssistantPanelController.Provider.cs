// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Provider adaptation of ui/internal/assistantpanel/controller.go and
// macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift;
// docs/chatgpt-integration.md §3. UI callbacks stay on the controller scope.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Assistants;

namespace Malachi.Core.Controllers;

public sealed partial class AssistantPanelController
{
    private IAssistantProvider? provider;
    private CancellationTokenSource? providerStop;

    /// <summary>Optional runtime; null retains the existing Claude adapter.</summary>
    public IAssistantProvider? Provider
    {
        get => provider;
        set
        {
            scope.VerifyAccess();
            if (ReferenceEquals(provider, value)) { return; }
            NewConversation();
            provider = value;
            RaiseState();
        }
    }

    /// <summary>The provider's current session; no credential is exposed.</summary>
    public IAssistantSession? ProviderSession { get; private set; }
    private bool HasRunningSession => Process?.Running == true || ProviderSession?.IsRunning == true;

    private async Task SubmitProviderAsync(IAssistantProvider selected, string prompt, IReadOnlyList<int> told, int my)
    {
        try
        {
            if (ProviderSession?.IsRunning != true)
            {
                EndProviderSession();
                var stop = CancellationTokenSource.CreateLinkedTokenSource(scope.Lifetime);
                providerStop = stop;
                var session = await selected.OpenAsync(new AssistantSessionSpec
                {
                    SystemPrompt = Assistant.SystemPrompt(Language(), Today()),
                    ToolPolicy = AssistantToolPolicy.Panel,
                }, stop.Token);
                if (my != gen || !ReferenceEquals(selected, Provider))
                {
                    session.Terminate();
                    scope.Pending.Track(session.Completion);
                    return;
                }
                ProviderSession = session;
                scope.Pending.Track(session.Completion);
                session.EventsReceived += (_, batch) =>
                {
                    if (ReferenceEquals(session, ProviderSession)) { Handle(batch); }
                };
                session.Exited += (_, exit) =>
                {
                    if (!ReferenceEquals(session, ProviderSession)) { return; }
                    ProviderSession = null;
                    if (CurrentPhase == Phase.Running)
                    {
                        Fail(Assistant.StoppedText(exit.Description), retry: true);
                    }
                };
            }
            if (my != gen || ProviderSession is not { } active) { return; }
            authFailed = false;
            refreshFailed = false;
            CurrentPhase = Phase.Running;
            RaiseState();
            await active.SubmitAsync(prompt, providerStop?.Token ?? scope.Lifetime);
            if (my != gen) { return; }
            for (var i = 0; i < pinned.Count; i++)
            {
                if (told.Contains(pinned[i].Key)) { pinned[i] = pinned[i] with { Announced = true }; }
            }
        }
        catch (OperationCanceledException)
        {
            if (my == gen) { Fail(Assistant.StoppedText("chatgpt_request_cancelled"), retry: false); }
        }
        catch (Exception e) when (e is AssistantProviderException or IOException or UnauthorizedAccessException or InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            if (my != gen) { return; }
            EndProviderSession();
            Fail(Assistant.StoppedText(e is AssistantProviderException failure ? failure.Code : "chatgpt_runtime_failed"), retry: true);
        }
    }

    private void EndProviderSession()
    {
        var session = ProviderSession;
        ProviderSession = null;
        session?.Terminate();
        var stop = providerStop;
        providerStop = null;
        stop?.Cancel();
        stop?.Dispose();
    }
}
