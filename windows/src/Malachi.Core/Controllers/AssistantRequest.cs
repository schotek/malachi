// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantRequest.swift
// (AssistantRequest: start, cancel, run, launch, finish, retire,
// defaultTimeout, timedOut, Tools, the per-call timeout and model, onTool,
// onUsage, toolsMissing); GTK: ui/internal/assistantpanel/oneshot.go
// (Request, NewRequest, Start, Cancel, askConsent, locate, launch, finish,
// retire).
//
// Windows differences: the private directory is made by an
// IPrivateDirectoryFactory (a protected DACL, as the open directory's)
// instead of mode 0700, Directory.CreateDirectory without one (the tests).
// The timeout, and the kill grace of the request's processes, run on the
// injected TimeProvider. The consent hook is a Func<Task<bool>>; a hook
// that throws counts as declined, and its failure is reported as a
// callback's is (docs/windows-port.md §7.5). Swift's onText and completion
// are Action parameters, run guarded. The steps run as one tracked task of
// the controller infrastructure (§7.2), every process of the request is
// tracked from its start until its end was reported (a process that does
// not end by itself ends at the kill, after the grace on the clock), and
// the timeout waits detached on the clock. Close (IDisposable, as the
// controllers own their scope) is Windows' own: Swift's windows only
// cancel. Only kinds and numbers are logged; model text never.

using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.IO;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Daemon;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// One question to the user's Claude Code: the one-shot requests of the In
/// App target, the compose window's rewrite
/// (<see cref="ComposeRewriteController"/>) and the search in the user's own
/// words (<see cref="SearchConversion"/>), which read no mail, and the
/// board's triage run and suggested reply, which read it through the bridge.
/// </summary>
/// <remarks>
/// <para>
/// It runs the panel's protocol once (<see cref="ClaudeCodeProcess"/>): the
/// command line of <see cref="Assistant.Args"/> without the bridge (no MCP
/// server, no tool), or with the bridge, its extra arguments and the tools of
/// <see cref="Tools"/> when the caller passes them, and with
/// <c>--json-schema</c> when the answer has a shape,
/// in the panel's private directory and with
/// <see cref="Assistant.ChildEnvironment"/>; one
/// <see cref="Assistant.UserMessage"/> on stdin, which is then closed; the
/// answer is the result event. The steps, each of which may end it:
/// </para>
/// <list type="number">
/// <item>The first request ever asks for consent (<see cref="Consent"/>,
/// the panel's "Send Mail to Claude?" on the window that asks; the answer
/// is the shared <c>assistant-consent</c>, kept even when the request was
/// cancelled meanwhile). Declined: <see cref="Outcome.Declined"/>, nothing
/// is sent.</item>
/// <item>Claude Code is located (none: <see cref="Failure.NotFound"/>) and
/// must not say it is signed out (<see cref="ClaudeCodeLocator.SignedInAsync"/>,
/// asked afresh: <see cref="Failure.NotSignedIn"/>; not known counts as
/// signed in, and the process then says what is wrong: a turn the API
/// refused for its sign-in, <see cref="AssistantEventKind.Failure"/>, is
/// <see cref="Failure.NotSignedIn"/> too).</item>
/// <item>The process starts; text deltas stream to <c>onText</c> (a whole
/// text block replaces the deltas before it), the result ends it: a
/// success is <see cref="Outcome.Answered"/> with the result's text and
/// <c>structured_output</c>, anything else <see cref="Failure.Stopped"/>
/// with the result's text or subtype. The process ending before its result
/// is <see cref="Failure.Stopped"/> with its stderr's first line, and no
/// result within <see cref="Timeout"/> (or the call's own) ends it the same
/// way. With <see cref="Tools"/>, the init event must report the bridge
/// connected (else <see cref="Failure.ToolsMissing"/>), and every tool call
/// and tool result goes to <c>onTool</c>. Every event that carries usage (an
/// API message's, the result's) goes to <c>onUsage</c> first, before the
/// event is handled (<see cref="AssistantUsageTally"/> adds them up); none
/// arrives once the request ended or was cancelled.</item>
/// </list>
/// <para>
/// One request at a time: <see cref="Start"/> cancels the one under way,
/// and <see cref="Cancel"/> ends it (its process terminated); a cancelled
/// request never calls its completion. Nothing is kept on disk; model text
/// is never logged. Create it, and call it, on the UI thread.
/// </para>
/// </remarks>
public sealed partial class AssistantRequest : IDisposable
{
    /// <summary>How long the answer is waited for.</summary>
    public static readonly TimeSpan DefaultTimeout = TimeSpan.FromSeconds(120);

    /// <summary>The reason when the answer was not there in time.</summary>
    public const string TimedOut = "no answer in time";

    private readonly string directory;
    private readonly IReadOnlyDictionary<string, string> environment;
    private readonly TimeSpan killGrace;
    private readonly IPrivateDirectoryFactory? directories;
    private readonly ILogger logger;
    private readonly ILogger<ClaudeCodeProcess>? processLogger;
    private readonly ControllerScope scope;

    // Processes that were ended and have not reported their exit yet, kept
    // until they do.
    private readonly List<ClaudeCodeProcess> retired = [];

    // Bumped by every Start and Cancel: the steps of an older request stop
    // at their next await, its events and its end are dropped.
    private int gen;
    private ClaudeCodeProcess? process;
    private EventHandler<IReadOnlyList<AssistantEvent>>? processEvents;
    private CancellationTokenSource? timer;

    // The answer so far: the text blocks that are whole, and the deltas
    // since the last of them.
    private string blocks = "";
    private string streamed = "";

    /// <summary>A request with nothing asked yet, on the calling (UI) thread.</summary>
    /// <param name="settings">The model and the consent.</param>
    /// <param name="locator">Finds and asks Claude Code (the application's, shared with the panel).</param>
    /// <param name="directory">Claude Code's working directory, empty and private (<see cref="Paths.AssistantDir"/>).</param>
    /// <param name="environment">The application's environment, filtered by <see cref="Assistant.ChildEnvironment"/>; this process's when null.</param>
    /// <param name="killGrace">From the end of stdin to the kill (<see cref="ClaudeCodeProcess.DefaultKillGrace"/>).</param>
    /// <param name="timeout">How long the answer is waited for (<see cref="DefaultTimeout"/>).</param>
    /// <param name="time">The clock of the timeout and of the kill; the system's when null.</param>
    /// <param name="directories">Makes <paramref name="directory"/> private; Directory.CreateDirectory when null.</param>
    /// <param name="logger">Receives the steps' kinds and numbers, never model text.</param>
    /// <param name="processLogger">The logger of the request's processes.</param>
    /// <param name="pending">Counts the request's background work and processes; one of its own when null.</param>
    public AssistantRequest(
        SettingsStore settings,
        ClaudeCodeLocator locator,
        string directory,
        IReadOnlyDictionary<string, string>? environment = null,
        TimeSpan? killGrace = null,
        TimeSpan? timeout = null,
        TimeProvider? time = null,
        IPrivateDirectoryFactory? directories = null,
        ILogger<AssistantRequest>? logger = null,
        ILogger<ClaudeCodeProcess>? processLogger = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(settings);
        ArgumentNullException.ThrowIfNull(locator);
        ArgumentException.ThrowIfNullOrEmpty(directory);
        Settings = settings;
        Locator = locator;
        this.directory = directory;
        this.environment = environment ?? ProcessEnvironment.Copy(ProcessEnvironment.Current());
        this.killGrace = killGrace ?? ClaudeCodeProcess.DefaultKillGrace;
        Timeout = timeout ?? DefaultTimeout;
        Time = time ?? TimeProvider.System;
        this.directories = directories;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        this.processLogger = processLogger;
        scope = new ControllerScope(pending);
    }

    /// <summary>The model and the consent.</summary>
    public SettingsStore Settings { get; }

    /// <summary>Finds and asks Claude Code.</summary>
    public ClaudeCodeLocator Locator { get; }

    /// <summary>The clock of the timeout and of the kill.</summary>
    public TimeProvider Time { get; }

    /// <summary>How long the answer is waited for (tests shorten it).</summary>
    public TimeSpan Timeout { get; set; }

    /// <summary>
    /// Asks the user before the first request ever (the panel's consent,
    /// <c>assistant-consent</c>); true allows. Without a hook nothing is
    /// ever sent.
    /// </summary>
    public Func<Task<bool>>? Consent { get; set; }

    /// <summary>A request is under way.</summary>
    public bool Running { get; private set; }

    /// <summary>Whether <see cref="Close"/> ran: nothing starts or completes any more.</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>
    /// Asks Claude Code once: <paramref name="message"/> as the one turn
    /// under <paramref name="systemPrompt"/>, with <paramref name="model"/>
    /// (null: the <c>assistant-model</c> setting, read now) and, when
    /// <paramref name="jsonSchema"/> is set, that shape of answer. A request
    /// under way is cancelled first. <paramref name="tools"/> gives Claude
    /// Code the bridge (null: no tool at all); <paramref name="timeout"/>
    /// replaces the request's own for this call. <paramref name="onText"/>
    /// gets the answer's text as it streams (all of it so far),
    /// <paramref name="onTool"/> every tool call and tool result,
    /// <paramref name="onUsage"/> every event that carries usage;
    /// <paramref name="completion"/> is called once with the outcome, unless
    /// the request is cancelled. All on the UI thread.
    /// </summary>
    public void Start(
        string systemPrompt,
        string message,
        Action<Outcome> completion,
        string jsonSchema = "",
        Action<string>? onText = null,
        Tools? tools = null,
        TimeSpan? timeout = null,
        AssistantModel? model = null,
        Action<AssistantEvent>? onTool = null,
        Action<AssistantEvent>? onUsage = null)
    {
        ArgumentNullException.ThrowIfNull(systemPrompt);
        ArgumentNullException.ThrowIfNull(message);
        ArgumentNullException.ThrowIfNull(completion);
        ArgumentNullException.ThrowIfNull(jsonSchema);
        scope.VerifyAccess();
        Cancel();
        if (IsClosed)
        {
            return;
        }
        var my = ++gen;
        Running = true;
        var call = new Call(
            systemPrompt, message, jsonSchema, tools, timeout ?? Timeout, model ?? Settings.AssistantModel, onText, onTool, onUsage, completion);
        scope.Run(_ => RunAsync(my, call));
    }

    /// <summary>Ends the request under way: its process is terminated and its completion never called.</summary>
    public void Cancel()
    {
        scope.VerifyAccess();
        gen++;
        if (!Running)
        {
            return;
        }
        Running = false;
        StopTimer();
        Retire();
    }

    /// <summary>
    /// Cancels the request under way and ends the request for good: a
    /// later <see cref="Start"/> does nothing (Windows: the window that owns
    /// it closed; Swift's windows only cancel).
    /// </summary>
    public void Close()
    {
        scope.VerifyAccess();
        Cancel();
        scope.Close();
    }

    /// <summary>Closes the request.</summary>
    public void Dispose() => Close();

    private async Task RunAsync(int my, Call call)
    {
        var completion = call.Completion;
        if (my != gen)
        {
            return;
        }
        var selectedProvider = Provider;
        // 1. Consent, once ever; an answer counts even when the request was
        // cancelled while the question was up. A board request of a provider
        // needs its board consent, which the board asks for itself.
        if (selectedProvider is not null && UsesBoardConsent)
        {
            if (!selectedProvider.HasBoardConsent)
            {
                Finish(my, new Outcome.Declined(), completion);
                return;
            }
        }
        else if (!(selectedProvider?.HasConsent ?? Settings.AssistantConsent))
        {
            var allowed = await AskConsentAsync();
            if (allowed)
            {
                if (selectedProvider is null) { Settings.AssistantConsent = true; }
                else if (my == gen && !IsClosed && ReferenceEquals(selectedProvider, Provider)) { selectedProvider.AcceptConsent(); }
            }
            if (my != gen)
            {
                return;
            }
            if (!allowed)
            {
                Finish(my, new Outcome.Declined(), completion);
                return;
            }
        }
        if (selectedProvider is not null)
        {
            await RunProviderAsync(selectedProvider, my, call);
            return;
        }
        // 2. Claude Code, signed in.
        if (Locator.Locate() is not { } path)
        {
            Finish(my, new Outcome.Failed(new Failure.NotFound()), completion);
            return;
        }
        Locator.Refresh();
        var signedIn = await Locator.SignedInAsync();
        if (my != gen)
        {
            return;
        }
        if (signedIn == false)
        {
            Finish(my, new Outcome.Failed(new Failure.NotSignedIn()), completion);
            return;
        }
        // 3. The process, the turn, the answer.
        ClaudeCodeProcess p;
        try
        {
            p = Launch(my, path, call);
        }
        catch (ClaudeCodeStartException e)
        {
            LogStartFailed(logger, e.Failure);
            Finish(my, new Outcome.Failed(new Failure.Stopped(e.Message)), completion);
            return;
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException or ArgumentException or NotSupportedException or InvalidOperationException or Win32Exception)
        {
            LogDirectoryFailed(logger, e.GetType().Name, e.HResult);
            Finish(my, new Outcome.Failed(new Failure.Stopped("the assistant's directory: " + e.Message)), completion);
            return;
        }
        if (!p.Send(Assistant.UserMessage(call.Message)))
        {
            Finish(my, new Outcome.Failed(new Failure.Stopped("claude is not running")), completion);
            return;
        }
        p.CloseInput();
        StartTimer(my, call.Timeout, completion);
    }

    // The hook's answer; no hook, or one that fails, declines.
    private async Task<bool> AskConsentAsync()
    {
        if (Consent is not { } consent)
        {
            return false;
        }
        try
        {
            return await consent();
        }
#pragma warning disable CA1031 // A failed hook is a declined consent, and reported.
        catch (Exception e)
#pragma warning restore CA1031
        {
            scope.Pending.Report(e);
            return false;
        }
    }

    // Step 3: the private directory and the process.
    private ClaudeCodeProcess Launch(int my, string path, Call call)
    {
        if (directories is null)
        {
            Directory.CreateDirectory(directory);
        }
        else
        {
            directories.Ensure(directory);
        }
        var options = new AssistantOptions
        {
            Bridge = call.Tools?.Bridge ?? "",
            Socket = call.Tools?.Socket ?? "",
            Model = call.Model,
            SystemPrompt = call.SystemPrompt,
            JsonSchema = call.JsonSchema,
            BridgeArgs = call.Tools?.BridgeArgs ?? [],
            Tools = call.Tools?.Allowed,
        };
        var p = new ClaudeCodeProcess(
            path, Assistant.Args(options), Assistant.ChildEnvironment(environment, path), directory, killGrace, Time, processLogger);
        blocks = "";
        streamed = "";
        EventHandler<IReadOnlyList<AssistantEvent>> events = (_, batch) => Handle(p, my, batch, call);
        p.EventsReceived += events;
        p.Exited += (_, exit) => Ended(p, my, exit, call.Completion);
        p.Start();
        // Counted until its end was reported (after the handler above).
        var ended = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        p.Exited += (_, _) => ended.TrySetResult();
        scope.Pending.Track(ended.Task);
        process = p;
        processEvents = events;
        return p;
    }

    private void Handle(ClaudeCodeProcess p, int my, IReadOnlyList<AssistantEvent> events, Call call)
    {
        if (p != process || my != gen)
        {
            return;
        }
        HandleEvents(my, events, call);
    }

    private void HandleEvents(int my, IReadOnlyList<AssistantEvent> events, Call call)
    {
        var completion = call.Completion;
        foreach (var e in events)
        {
            if (e.Usage is not null && call.OnUsage is { } onUsage)
            {
                scope.Guard(() => onUsage(e));
                // The handler may have cancelled the request.
                if (my != gen || !Running)
                {
                    return;
                }
            }
            switch (e.Kind)
            {
                case AssistantEventKind.SystemInit:
                    if (call.Tools is not null && !e.BridgeConnected)
                    {
                        LogToolsMissing(logger);
                        Finish(my, new Outcome.Failed(new Failure.ToolsMissing()), completion);
                        return;
                    }
                    break;
                case AssistantEventKind.ToolUse or AssistantEventKind.ToolResult:
                    if (call.OnTool is { } onTool)
                    {
                        scope.Guard(() => onTool(e));
                        // The handler may have cancelled the request.
                        if (my != gen || !Running)
                        {
                            return;
                        }
                    }
                    break;
                case AssistantEventKind.TextDelta:
                    streamed += e.Text;
                    Text(call.OnText, blocks + streamed);
                    break;
                case AssistantEventKind.Text:
                    blocks += e.Text;
                    streamed = "";
                    Text(call.OnText, blocks);
                    break;
                case AssistantEventKind.Failure:
                    // Claude Code's own words for a turn the API refused,
                    // which the result repeats; a refused sign-in ends it.
                    if (e.NotSignedIn)
                    {
                        Finish(my, new Outcome.Failed(new Failure.NotSignedIn()), completion);
                        return;
                    }
                    break;
                case AssistantEventKind.Result:
                    LogResult(logger, e.Success, e.CostUsd);
                    var text = e.ResultText.Length == 0 ? blocks + streamed : e.ResultText;
                    Outcome outcome = e.Success
                        ? new Outcome.Answered(text, e.Structured)
                        : new Outcome.Failed(providerSession is not null ? ProviderFailure(e.ResultText) : new Failure.Stopped(e.ResultText));
                    Finish(my, outcome, completion);
                    return;
                default:
                    continue;
            }
        }
    }

    private void Text(Action<string>? onText, string text)
    {
        if (onText is not null)
        {
            scope.Guard(() => onText(text));
        }
    }

    private void Ended(ClaudeCodeProcess p, int my, ClaudeCodeExit exit, Action<Outcome> completion)
    {
        retired.Remove(p);
        if (p != process || my != gen)
        {
            return;
        }
        LogEnded(logger, exit.Status);
        Finish(my, new Outcome.Failed(new Failure.Stopped(exit.Description)), completion);
    }

    // No result within Timeout ends request my. The wait is made now, on the
    // clock, so that a fake clock's next step is sure to see it.
    private void StartTimer(int my, TimeSpan timeout, Action<Outcome> completion)
    {
        var stop = CancellationTokenSource.CreateLinkedTokenSource(scope.Lifetime);
        timer = stop;
        var delay = Task.Delay(timeout, Time, stop.Token);
        scope.RunDetached(async _ =>
        {
            try
            {
                await delay;
            }
            catch (OperationCanceledException)
            {
                return;
            }
            if (my != gen || !Running)
            {
                return;
            }
            LogTimedOut(logger);
            Finish(my, new Outcome.Failed(new Failure.Stopped(TimedOut)), completion);
        });
    }

    private void StopTimer()
    {
        if (timer is { } t)
        {
            timer = null;
            t.Cancel();
            t.Dispose();
        }
    }

    // Ends request my with outcome, once.
    private void Finish(int my, Outcome outcome, Action<Outcome> completion)
    {
        if (my != gen || !Running)
        {
            return;
        }
        Running = false;
        StopTimer();
        Retire();
        scope.Guard(() => completion(outcome));
    }

    // Terminates the request's process (after its answer it is about to end
    // anyway) and keeps it until it reports its exit.
    private void Retire()
    {
        EndProviderSession();
        if (process is not { } p)
        {
            return;
        }
        process = null;
        p.EventsReceived -= processEvents;
        processEvents = null;
        if (p.Running)
        {
            retired.Add(p);
            p.Terminate();
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "assistant request: success {Success}, cost {CostUsd} USD")]
    private static partial void LogResult(ILogger logger, bool success, double costUsd);

    [LoggerMessage(Level = LogLevel.Information, Message = "assistant request: claude ended with status {Status}")]
    private static partial void LogEnded(ILogger logger, int status);

    [LoggerMessage(Level = LogLevel.Warning, Message = "assistant request: the malachi MCP server is not connected")]
    private static partial void LogToolsMissing(ILogger logger);

    [LoggerMessage(Level = LogLevel.Warning, Message = "assistant request: no answer in time")]
    private static partial void LogTimedOut(ILogger logger);

    [LoggerMessage(Level = LogLevel.Warning, Message = "assistant request: claude could not be started: {Failure}")]
    private static partial void LogStartFailed(ILogger logger, ClaudeCodeStartFailure failure);

    [LoggerMessage(Level = LogLevel.Warning, Message = "assistant request: the directory could not be made: {Kind} 0x{HResult:X8}")]
    private static partial void LogDirectoryFailed(ILogger logger, string kind, int hResult);
}
