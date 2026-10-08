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
using Malachi.Core.I18n;

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
                        FailProvider(exit.Description, retry: true);
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
            FailProvider(e is AssistantProviderException failure ? failure.Code : "chatgpt_runtime_failed", retry: true);
        }
    }

    /// <summary>
    /// Connects the provider's ChatGPT account again (the window runs its
    /// sign-in); true when it worked. With it, a connection that is missing
    /// or lapsed ends the turn with Reconnect to ChatGPT (GTK
    /// <c>ReconnectProvider</c>).
    /// </summary>
    public Func<Task<bool>>? ReconnectProvider { get; set; }

    // The error line of a provider's reason (GTK providerFailure): Codex not
    // found, the connection to make again, the plan's usage limit (in the
    // board's words), or the assistant stopped with the reason; never the
    // raw code where a sentence exists.
    private ErrorContent ProviderFailureLine(string reason, bool retry)
    {
        var failure = AssistantRequest.ProviderFailure(reason);
        return failure switch
        {
            AssistantRequest.Failure.NotFound => new ErrorContent(L10n.T("Codex was not found. Choose a native Codex executable."), true),
            AssistantRequest.Failure.NotSignedIn => new ErrorContent(L10n.T("Reconnect to ChatGPT"), false,
                ReconnectProvider is null ? ErrorOffer.None : ErrorOffer.Reconnect),
            AssistantRequest.Failure.Limit => new ErrorContent(failure.Text, true),
            _ => new ErrorContent(failure.Text, retry),
        };
    }

    private void AppendProviderFailure(string reason, bool retry)
    {
        var line = ProviderFailureLine(reason, retry);
        if (line.Offer == ErrorOffer.Reconnect)
        {
            // The next question needs a new session of the connected account.
            EndProviderSession();
        }
        Append(line);
    }

    private void FailProvider(string reason, bool retry)
    {
        CloseTurn();
        CurrentPhase = Phase.Idle;
        AppendProviderFailure(reason, retry);
        RaiseState();
    }

    // Reconnect to ChatGPT first (GTK reconnectFirst): shown as an activity
    // line; true once it worked, otherwise the turn ended with Could not
    // connect and Reconnect again.
    private async Task<bool> ReconnectFirstAsync(int my)
    {
        EndProviderSession(); // a session of the account that lapsed is no use
        signingIn = Append(new ActivityContent(L10n.T("Connecting…"), false));
        bool ok;
        try
        {
            ok = await ReconnectProvider!();
        }
        catch (Exception e) when (e is IOException or InvalidOperationException or AssistantProviderException or OperationCanceledException)
        {
            ok = false;
        }
        if (my != gen)
        {
            return false;
        }
        CloseSignIn();
        if (!ok)
        {
            Fail(L10n.T("Could not connect to ChatGPT."), retry: false, ErrorOffer.Reconnect);
        }
        return ok;
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
