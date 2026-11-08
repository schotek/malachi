// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/APIAssistant/DaemonAssistantSession.swift;
// GTK: ui/internal/apiassistant/session.go (Session, newSession, Running,
// Submit, Terminate, Disconnected, end, SetHandlers, endsSession, Handle,
// translate, deliver). One session of the daemon's assistant
// (assistant.sessionStart … sessionStop) as the panel's protocol sees it.
//
// Windows: Go's SetHandlers and Swift's onEvents/onExit are the events
// EventsReceived and Exited of IAssistantSession: what arrives before
// anybody subscribed (a turn sent with the start answers before the caller
// attached) is kept and handed over, in order, once somebody does, the
// events first and the exit after them. Handle may run on any thread (the
// application's notification dispatch); the events and the exit are raised
// on the synchronization context the session was opened on (inline without
// one), in the order the daemon sent them. Completion completes when the
// session ended, as Go's done closes.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using PanelEvent = Malachi.Core.Assistants.AssistantEvent;
using PanelKind = Malachi.Core.Assistants.AssistantEventKind;
using PanelUsage = Malachi.Core.Assistants.AssistantUsage;
using WireEvent = Malachi.Core.Api.AssistantEvent;
using WireKind = Malachi.Core.Api.AssistantEventKind;

namespace Malachi.Core.ApiAssistants;

/// <summary>
/// apiassistant.Session: one session of the daemon's assistant. The daemon's
/// events of the session (<see cref="Handle"/>, from
/// <see cref="AssistantSessions"/>) become the panel's events:
/// <list type="bullet">
/// <item>init: SystemInit with the bridge connected and the tools;</item>
/// <item>textDelta, text: TextDelta, Text (the text blocks of the turn are kept for the result);</item>
/// <item>toolUse, toolResult: ToolUse (the tool, the call's id), ToolResult (the id, whether it failed, the bridge's text);</item>
/// <item>usage: Other with the usage so far as the whole, final usage of the
/// turn's message (its id is the session's and the turn's number, so that
/// <see cref="AssistantUsageTally"/> keeps the last value);</item>
/// <item>result: Result with the success, the turn's text blocks joined by a newline and the structured answer;</item>
/// <item>failure: Failure with the reason "api_&lt;class&gt;", and, as the
/// daemon ends the turn with it, a Result that failed with that reason; a
/// class that ends the session (auth, permission, toolsUnsupported,
/// contextFull, bridge, stopped) ends it here too: <see cref="Exited"/> with
/// that reason.</item>
/// </list>
/// <see cref="Terminate"/> is <c>assistant.sessionStop</c> (in the
/// background; <see cref="AssistantSessions.WaitAsync"/> waits for it) and an
/// exit of "api_stopped"; <see cref="Disconnected"/> (the connection went)
/// the same without the call.
/// </summary>
public sealed partial class DaemonAssistantSession : IAssistantSession
{
    private readonly DaemonAssistantProvider provider;
    private readonly SynchronizationContext? context;
    private readonly Lock gate = new();
    private readonly TaskCompletionSource completion = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private readonly List<PanelEvent> queued = [];
    private EventHandler<IReadOnlyList<PanelEvent>>? eventsReceived;
    private EventHandler<AssistantSessionExit>? exited;
    private string? queuedExit;
    private bool draining;
    private bool ended;
    // The first turn went with assistant.sessionStart (a one-shot purpose's
    // input), so the first SubmitAsync sends nothing.
    private bool sent;
    // Counts the turns for the usage's message id; blocks are the text
    // blocks of the turn under way.
    private int turn;
    private List<string> blocks = [];

    internal DaemonAssistantSession(DaemonAssistantProvider provider, string id, IReadOnlyList<string> tools, bool sent, SynchronizationContext? context)
    {
        this.provider = provider;
        this.context = context;
        Id = id;
        Tools = [.. tools];
        this.sent = sent;
        if (sent)
        {
            turn = 1;
        }
    }

    /// <summary>The daemon's session id.</summary>
    public string Id { get; }

    /// <summary>The bare tools the daemon gave the model.</summary>
    public IReadOnlyList<string> Tools { get; }

    /// <inheritdoc/>
    public event EventHandler<IReadOnlyList<PanelEvent>>? EventsReceived
    {
        add
        {
            lock (gate)
            {
                eventsReceived += value;
            }
            Schedule();
        }
        remove
        {
            lock (gate)
            {
                eventsReceived -= value;
            }
        }
    }

    /// <inheritdoc/>
    public event EventHandler<AssistantSessionExit>? Exited
    {
        add
        {
            lock (gate)
            {
                exited += value;
            }
            Schedule();
        }
        remove
        {
            lock (gate)
            {
                exited -= value;
            }
        }
    }

    /// <inheritdoc/>
    public Task Completion => completion.Task;

    /// <summary>apiassistant.Session.Running: the session is open.</summary>
    public bool IsRunning
    {
        get
        {
            lock (gate)
            {
                return !ended;
            }
        }
    }

    /// <summary>
    /// apiassistant.Session.Submit: sends one turn
    /// (<c>assistant.sessionSend</c>). The daemon's refusal is thrown as the
    /// reason AssistantRequest.ProviderFailure reads: a session it no longer
    /// has ends this one.
    /// </summary>
    public async Task SubmitAsync(PromptInput input, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(input);
        lock (gate)
        {
            if (ended)
            {
                throw new AssistantProviderException(DaemonAssistantProvider.ReasonStopped);
            }
            if (sent)
            {
                // The start sent this turn; its events are on their way.
                sent = false;
                return;
            }
            turn++;
            blocks = [];
        }
        try
        {
            await provider.Client.CallAsync(
                API.AssistantSessionSend, new AssistantSessionSendParams { SessionId = Id, Input = input },
                DaemonAssistantProvider.CallTimeout, cancellationToken).ConfigureAwait(false);
        }
        catch (Exception e) when (e is not OperationCanceledException || !cancellationToken.IsCancellationRequested)
        {
            if (RpcErrorText.DaemonError(e) is { Code.Value: ErrorCode.AssistantSessionNotFound })
            {
                End(DaemonAssistantProvider.ReasonStopped, stop: false);
            }
            throw DaemonAssistantProvider.Failure(e);
        }
    }

    /// <summary>
    /// apiassistant.Session.Terminate: ends the session:
    /// <c>assistant.sessionStop</c> in the background (its answer is not
    /// waited for), the exit at once.
    /// </summary>
    public void Terminate() => End(DaemonAssistantProvider.ReasonStopped, stop: true);

    /// <summary>
    /// apiassistant.Session.Disconnected: the connection to the daemon went,
    /// and the session with it (the daemon ends it itself).
    /// </summary>
    public void Disconnected() => End(DaemonAssistantProvider.ReasonStopped, stop: false);

    /// <summary>
    /// apiassistant.endsSession: whether a failure of class
    /// <paramref name="c"/> ends the session (docs/api.md §4.18
    /// assistant.sessionSend).
    /// </summary>
    public static bool EndsSession(AssistantFailureClass c) => c.Value switch
    {
        AssistantFailureClass.Auth or AssistantFailureClass.Permission or AssistantFailureClass.ToolsUnsupported
            or AssistantFailureClass.ContextFull or AssistantFailureClass.Bridge or AssistantFailureClass.Stopped => true,
        _ => false,
    };

    /// <summary>
    /// apiassistant.Session.Handle: takes one event of the session
    /// (<c>notify.assistantEvent</c>) and delivers what it stands for; false
    /// for another session's.
    /// </summary>
    public bool Handle(WireEvent e)
    {
        ArgumentNullException.ThrowIfNull(e);
        if (e.SessionId != Id)
        {
            return false;
        }
        string? exitReason;
        lock (gate)
        {
            if (ended)
            {
                return true;
            }
            var (events, reason) = Translate(e);
            queued.AddRange(events);
            exitReason = reason;
        }
        if (exitReason is not null)
        {
            End(exitReason, stop: false);
        }
        else
        {
            Schedule();
        }
        return true;
    }

    // Ends the session once with reason, telling the daemon when stop.
    private void End(string reason, bool stop)
    {
        lock (gate)
        {
            if (ended)
            {
                return;
            }
            ended = true;
            queuedExit = reason;
        }
        provider.Sessions?.Remove(this);
        if (stop)
        {
            var sessions = provider.Sessions;
            sessions?.StopBegan();
            _ = StopAsync(sessions);
        }
        completion.TrySetResult();
        Schedule();
    }

    private async Task StopAsync(AssistantSessions? sessions)
    {
        try
        {
            await provider.Client.CallAsync(
                API.AssistantSessionStop, new AssistantSessionStopParams { SessionId = Id }, DaemonAssistantProvider.CallTimeout).ConfigureAwait(false);
        }
        catch (Exception e) when (e is RpcException or RpcClientException or OperationCanceledException or TimeoutException)
        {
            LogStopFailed(provider.Logger, RpcErrorText.DaemonError(e)?.Code.Value ?? 0);
        }
        finally
        {
            sessions?.StopEnded();
        }
    }

    // Hands what was kept to the subscribers on the session's context.
    private void Schedule()
    {
        if (context is null)
        {
            Drain();
        }
        else
        {
            context.Post(_ => Drain(), null);
        }
    }

    // Hands over what arrived while nobody listened, in order: the events
    // first, then the exit once no event is left. One drain at a time: a
    // drain that finds another under way leaves the work to it.
    private void Drain()
    {
        lock (gate)
        {
            if (draining)
            {
                return;
            }
            draining = true;
        }
        while (true)
        {
            EventHandler<IReadOnlyList<PanelEvent>>? onEvents = null;
            EventHandler<AssistantSessionExit>? onExit = null;
            PanelEvent[]? events = null;
            string? exit = null;
            lock (gate)
            {
                if (queued.Count > 0 && eventsReceived is not null)
                {
                    events = [.. queued];
                    queued.Clear();
                    onEvents = eventsReceived;
                }
                else if (queued.Count == 0 && queuedExit is not null && exited is not null)
                {
                    exit = queuedExit;
                    queuedExit = null;
                    onExit = exited;
                }
                else
                {
                    draining = false;
                    return;
                }
            }
            try
            {
                if (events is not null)
                {
                    onEvents!(this, events);
                }
                else
                {
                    onExit!(this, new AssistantSessionExit(1, exit!));
                }
            }
            catch
            {
                lock (gate)
                {
                    draining = false;
                }
                throw;
            }
        }
    }

    // apiassistant.Session.translate: the panel's events of e, and the
    // reason the session ends with (null when it goes on). Under the lock.
    private (IReadOnlyList<PanelEvent> Events, string? Exit) Translate(WireEvent e)
    {
        switch (e.Kind.Value)
        {
            case WireKind.Init:
                return ([new PanelEvent(PanelKind.SystemInit) { BridgeConnected = true, Tools = e.Tools.Count > 0 ? [.. e.Tools] : Tools }], null);
            case WireKind.TextDelta:
                return ([new PanelEvent(PanelKind.TextDelta) { Text = e.Text ?? "" }], null);
            case WireKind.Text:
                blocks.Add(e.Text ?? "");
                return ([new PanelEvent(PanelKind.Text) { Text = e.Text ?? "" }], null);
            case WireKind.ToolUse:
                return ([new PanelEvent(PanelKind.ToolUse) { Tool = e.Tool ?? "", ToolUseId = e.Id ?? "" }], null);
            case WireKind.ToolResult:
                return ([new PanelEvent(PanelKind.ToolResult) { ToolUseId = e.Id ?? "", IsError = e.IsError, ResultText = e.Text ?? "" }], null);
            case WireKind.Usage:
                if (e.Usage is not { } u)
                {
                    return ([], null);
                }
                return ([new PanelEvent(PanelKind.Other)
                {
                    Usage = new PanelUsage(u.InputTokens, u.OutputTokens, u.CacheCreationInputTokens, u.CacheReadInputTokens),
                    UsageFinal = true,
                    MessageId = Id + ":" + turn.ToString(System.Globalization.CultureInfo.InvariantCulture),
                }], null);
            case WireKind.Result:
                {
                    var text = string.Join("\n", blocks);
                    blocks = [];
                    var r = e.Result;
                    return ([new PanelEvent(PanelKind.Result)
                {
                    ResultText = text,
                    Success = r?.Success ?? false,
                    IsError = r is not null && !r.Success,
                    Structured = r?.Structured is { } s ? Encoding.UTF8.GetBytes(s.GetRawText()) : null,
                }], null);
                }
            case WireKind.Failure:
                {
                    var c = e.Failure?.Class ?? new AssistantFailureClass(AssistantFailureClass.Stopped);
                    var reason = DaemonAssistantProvider.ReasonOf(c);
                    blocks = [];
                    PanelEvent[] events =
                    [
                        new PanelEvent(PanelKind.Failure) { Failure = reason },
                    new PanelEvent(PanelKind.Result) { Success = false, IsError = true, ResultText = reason },
                ];
                    return (events, EndsSession(c) ? reason : null);
                }
            default:
                return ([], null);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "ai assistant: sessionStop failed, code {Code}")]
    private static partial void LogStopFailed(ILogger logger, int code);
}
