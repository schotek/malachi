// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Provider adaptation of ui/internal/assistantpanel/controller.go and
// macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift;
// docs/chatgpt-integration.md §3. UI callbacks stay on the controller scope.
// The turn of a provider's session is Swift's providerPrompt and
// sendProvider (GTK providerPrompt): the daemon's prompt of the turn
// (ComposeTurnAsync) is asked for first; the running session then gets the
// turn's input, else a session is opened with the panel's prompt (the
// system prompt with the profile, the tools and the bridge's arguments) and
// gets it.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
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

    private async Task SubmitProviderAsync(IAssistantProvider selected, Turn turn, IReadOnlyList<int> told, int my)
    {
        if (Prompter is not { } prompter)
        {
            Fail(Assistant.StoppedText(Assistant.NoPrompter), retry: false);
            return;
        }
        PromptResult res;
        PromptInput input;
        try
        {
            (res, input) = await ComposeTurnAsync(prompter, PromptParams, turn, scope.Lifetime);
        }
        catch (Exception e) when (Assistant.IsPromptFailure(e) || e is OperationCanceledException)
        {
            if (my != gen)
            {
                return;
            }
            LogPromptCallFailed(logger, e.GetType().Name);
            Fail(Assistant.StoppedText(Assistant.PromptReason(e)), retry: true);
            return;
        }
        if (my != gen)
        {
            return;
        }
        try
        {
            if (ProviderSession?.IsRunning != true)
            {
                EndProviderSession();
                var stop = CancellationTokenSource.CreateLinkedTokenSource(scope.Lifetime);
                providerStop = stop;
                var session = await selected.OpenAsync(new AssistantSessionSpec
                {
                    Purpose = PromptPurpose.Panel,
                    Language = Language(),
                    BridgePath = bridge ?? "",
                    SystemPrompt = res.SystemPrompt,
                    ToolPolicy = AssistantToolPolicy.Panel,
                    Tools = Assistant.PrefixTools(res.Tools),
                    BridgeArgs = res.BridgeArgs,
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
            await active.SubmitAsync(input, providerStop?.Token ?? scope.Lifetime);
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

    // The error line of a provider's reason (GTK providerFail,
    // providerFailure; Swift providerFail): Codex not found, the connection
    // to make again (when the window gave ReconnectProvider), an ai account
    // whose consent is not given or whose key was refused with Set Up in
    // Integrations…, the plan's usage limit (in the board's words, or the
    // daemon session's sentence), or the failure's own line (a daemon
    // session's failure class has a sentence of its own) with retry; never
    // the raw code where a sentence exists.
    private ErrorContent ProviderFailureLine(string reason, bool retry)
    {
        var failure = AssistantRequest.ProviderFailure(reason);
        return failure switch
        {
            AssistantRequest.Failure.NotFound => new ErrorContent(L10n.T("Codex was not found. Choose a native Codex executable."), true),
            AssistantRequest.Failure.NotSignedIn { IsApiFailure: true } => new ErrorContent(failure.Text, false, ErrorOffer.SetUpInIntegrations),
            AssistantRequest.Failure.NotSignedIn => new ErrorContent(L10n.T("Reconnect to ChatGPT"), false,
                ReconnectProvider is null ? ErrorOffer.None : ErrorOffer.Reconnect),
            AssistantRequest.Failure.Limit => new ErrorContent(failure.Text, true),
            _ => new ErrorContent(failure.Text, retry),
        };
    }

    private void AppendProviderFailure(string reason, bool retry)
    {
        var line = ProviderFailureLine(reason, retry);
        if (line.Offer is ErrorOffer.Reconnect or ErrorOffer.SetUpInIntegrations)
        {
            // The next question needs a new session of the account.
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
