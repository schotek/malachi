// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/BoardReplyController.swift
// (BoardReplyController: endWait, state, signedIn, consent, onRefresh,
// timeout, observe, isIdle, availabilityChanged, checkSignIn, canRun, view,
// start, cancel, cancelAndCleanUp, providerChanged, run, tool, answered,
// link, fail, delete, track); GTK: ui/internal/boardreply/controller.go
// (Controller, NewController, Close, Created, Idle, CanRun, View,
// AvailabilityChanged, CheckSignIn, Start, Cancel, CancelAndCleanUp).
//
// Windows differences: the timeout (Swift's injected sleep, Go's
// loop.After) and the wait of CancelAndCleanUpAsync run on the injected
// TimeProvider. The consent hook is a Func<Task<bool>>; a hook that throws
// declines, and its failure is reported as a callback's is
// (docs/windows-port.md §7.5). The locator's sign-in changes arrive as its
// event SigningInChanged (Swift onSignInChange). The calls go through the
// controller infrastructure (§7.2): board.get with Perform, board.setDraft
// and draft.delete with PerformPastClose (sent even when the controller is
// disposed right after), each kept in the pending calls until its answer
// was handled. The ChatGPT branches are Swift's (the selected provider's
// Available, Connected and consent; Go has no provider); a provider's
// session gets AssistantToolPolicy.ReplyOnly. Dispose (stop listening,
// cancel) is Windows' own, as Go's Close. Only kinds are logged, never what
// the model wrote nor an address.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Boards;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The board's Suggest Reply (docs/mcp.md "A suggested reply on the board",
/// docs/security.md §10.2), once for the whole application: on the user's
/// click in a case's detail, the user's Claude Code writes one reply draft
/// for that case through the bridge (<see cref="AssistantRequest"/> with
/// <see cref="AssistantRequest.Tools"/>), and the draft is linked as the
/// case's suggested reply.
/// </summary>
/// <remarks>
/// <para>A request, each step of which may end it (<see cref="State"/>):</para>
/// <list type="number">
/// <item>It needs what the compose rewrite needs (<see cref="CanRun"/>: the
/// assistant shown with the In App target, the bridge beside the
/// application, Claude Code found), a case with a message to reply to and no
/// suggested reply yet, and no other request running (one at a time for the
/// application; <see cref="Start"/> refuses another). The first request ever
/// asks the assistant's consent (<see cref="Consent"/>, the panel's question,
/// <c>assistant-consent</c>); the board's triage consent is not needed.
/// Declined: back to idle. The time (<see cref="Timeout"/>) runs from here.</item>
/// <item><c>board.get</c> for the case: its newest members' ids and, fresh,
/// its reply target; a case that has a suggested reply by now ends it
/// quietly.</item>
/// <item>The request: <see cref="Assistant.SuggestReplyMessage"/> under
/// <see cref="Assistant.SuggestReplySystemPrompt"/>, with the assistant's
/// model, the bridge started as <c>--socket &lt;socket&gt; --reply-only
/// &lt;replyMessageId&gt;</c> and only <see cref="Assistant.SuggestReplyTools"/>.</item>
/// <item>The draft is the one a successful create_draft result names
/// (<see cref="Assistant.ParseDraftResult"/>). Without one the request failed
/// (<see cref="Board.SuggestReplyFailure.NoDraft"/>). With one it is linked
/// with <c>board.setDraft</c>, and the board lists again
/// (<see cref="OnRefresh"/>); a link the daemon refuses deletes the draft
/// (<c>draft.delete</c>, so no orphan stays in Drafts), a refusal because the
/// case got a suggested reply meanwhile (<c>conflict</c>) ends quietly, any
/// other as <see cref="Board.SuggestReplyFailure.Backend"/>.</item>
/// </list>
/// <para>
/// Stop (<see cref="Cancel"/>) and the timeout delete a draft that was
/// created and not linked yet; a link already on its way is left to finish,
/// and deletes the draft only when it fails. Losing what it needs while it
/// runs (<see cref="AvailabilityChanged"/>) stops it like Stop. Quitting
/// stops it and waits for those deletes, at most <see cref="EndWait"/>
/// (<see cref="CancelAndCleanUpAsync"/>). Usage is not recorded: the 24-hour
/// row of tokens is the triage's, and a suggested reply is no run. Nothing
/// the model writes is shown or logged. Create it, and call it, on the UI
/// thread.
/// </para>
/// </remarks>
public sealed partial class BoardReplyController : IDisposable
{
    /// <summary>How long <see cref="CancelAndCleanUpAsync"/> waits for the deletes at most (the triage's bound at quit).</summary>
    public static readonly TimeSpan EndWait = TimeSpan.FromSeconds(2);

    private readonly RpcClient client;
    private readonly string? bridge;
    private readonly string socket;
    private readonly Func<bool> available;
    private readonly TimeProvider time;
    private readonly ILogger logger;
    private readonly ControllerScope scope;
    private readonly BoardObservers observers = new();
    private readonly List<Action> unsubscribe = [];

    // The board.setDraft and draft.delete calls on their way.
    private readonly Dictionary<int, Task> pending = [];
    private int nextPending;

    // Bumped by every Start and every end (Cancel, the timeout): the steps
    // of an older request stop at their next callback.
    private int gen;

    // The create_draft calls of the request under way, by tool use id.
    private HashSet<string> draftCalls = new(StringComparer.Ordinal);

    // Ends the request when Timeout passed.
    private CancellationTokenSource? timer;

    // board.setDraft is on its way: Stop leaves the draft to it.
    private bool linking;

    // Bumped by every sign-in check and every sign-in a request learnt.
    private int signInGen;

    private Board.SuggestReplyState state = new Board.SuggestReplyState.Idle();
    private bool? signedIn;

    /// <summary>The suggested reply with <paramref name="available"/> deciding whether the feature can exist.</summary>
    /// <param name="client">The daemon (<c>board.get</c>, <c>board.setDraft</c>, <c>draft.delete</c>).</param>
    /// <param name="settings">The assistant's model, provider and consent.</param>
    /// <param name="locator">The application's Claude Code (shared with the panel).</param>
    /// <param name="request">The one-shot request of this feature alone; its consent hook is cleared (the controller asks itself).</param>
    /// <param name="bridge"><c>malachi-mcp.exe</c> beside the application, null without.</param>
    /// <param name="socket">The daemon's socket, for the bridge.</param>
    /// <param name="available">Whether the feature can exist; its changes come through <see cref="AvailabilityChanged"/>.</param>
    /// <param name="time">The clock of the timeout and of the quit's wait; the request's when null.</param>
    /// <param name="logger">Receives the steps' kinds, never what the model wrote.</param>
    /// <param name="pending">Counts the background work; one of its own when null.</param>
    public BoardReplyController(
        RpcClient client,
        SettingsStore settings,
        ClaudeCodeLocator locator,
        AssistantRequest request,
        string? bridge,
        string socket,
        Func<bool> available,
        TimeProvider? time = null,
        ILogger<BoardReplyController>? logger = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(settings);
        ArgumentNullException.ThrowIfNull(locator);
        ArgumentNullException.ThrowIfNull(request);
        ArgumentNullException.ThrowIfNull(socket);
        ArgumentNullException.ThrowIfNull(available);
        this.client = client;
        Settings = settings;
        Locator = locator;
        Request = request;
        this.bridge = bridge;
        this.socket = socket;
        this.available = available;
        this.time = time ?? request.Time;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
        // The controller asks consent itself, before its own time runs.
        Request.Consent = null;
        // A provider's model is the assistant's, as the panel's (Swift
        // providerModelID ?? assistantChatGPTModel).
        Request.ProviderModelId = () => Settings.AssistantChatGptModel;
        EventHandler onSignIn = (_, _) =>
        {
            CheckSignIn();
            Notify();
        };
        Locator.SigningInChanged += onSignIn;
        unsubscribe.Add(() => Locator.SigningInChanged -= onSignIn);
    }

    /// <summary>
    /// The application's: available as the compose rewrite is, with the
    /// assistant's <see cref="AssistantController.PanelShown"/>, whose changes
    /// and those of the <c>assistant-target</c> preference are reported here.
    /// </summary>
    public BoardReplyController(
        RpcClient client,
        SettingsStore settings,
        ClaudeCodeLocator locator,
        AssistantRequest request,
        AssistantController assistant,
        string? bridge,
        string socket,
        TimeProvider? time = null,
        ILogger<BoardReplyController>? logger = null,
        PendingWork? pending = null)
        : this(client, settings, locator, request, bridge, socket, () => assistant?.PanelShown ?? false, time, logger, pending)
    {
        ArgumentNullException.ThrowIfNull(assistant);
        EventHandler onAssistant = (_, _) => AvailabilityChanged();
        assistant.Changed += onAssistant;
        unsubscribe.Add(() => assistant.Changed -= onAssistant);
        var token = settings.OnChange(SettingsKey.AssistantTarget, AvailabilityChanged);
        unsubscribe.Add(token.Cancel);
    }

    /// <summary>The assistant's model, provider and consent.</summary>
    public SettingsStore Settings { get; }

    /// <summary>The application's Claude Code.</summary>
    public ClaudeCodeLocator Locator { get; }

    /// <summary>The one-shot request.</summary>
    public AssistantRequest Request { get; }

    /// <summary>What the application's one suggested reply is doing.</summary>
    public Board.SuggestReplyState State => state;

    /// <summary>Whether Claude Code is signed in, as last asked; null when not known.</summary>
    public bool? SignedIn => signedIn;

    /// <summary>
    /// Asks the user before the first request ever (the panel's consent,
    /// <c>assistant-consent</c>, shared with the panel and the compose
    /// rewrite); true allows. Without it a request that needs consent ends
    /// quietly. The board's triage consent is not asked: this request is the
    /// user's own, for one case.
    /// </summary>
    public Func<Task<bool>>? Consent { get; set; }

    /// <summary>Asks the board to list again (after a draft was linked): the board source's refresh.</summary>
    public Action? OnRefresh { get; set; }

    /// <summary>
    /// How long a request may take (<see cref="Assistant.SuggestReplyTimeout"/>).
    /// The controller keeps the time itself, so the end of a request that
    /// runs out of it is the controller's, created draft and all; the
    /// request's own bound is ten seconds longer.
    /// </summary>
    public TimeSpan Timeout { get; set; } = Assistant.SuggestReplyTimeout;

    /// <summary>The draft the request under way created and that is not linked yet (the tests read it).</summary>
    public DraftRef? Created { get; private set; }

    /// <summary>Nothing runs and no call is on its way (the tests wait for it).</summary>
    public bool IsIdle => !state.IsRunning && pending.Count == 0;

    /// <summary>The feature can run: available, the bridge and Claude Code (or the selected provider) there.</summary>
    public bool CanRun => available() && bridge is not null && RuntimeAvailable;

    private bool ChatGpt => Settings.AssistantProvider == AssistantProviderID.ChatGpt;

    private bool RuntimeAvailable => ChatGpt ? Request.Provider?.Available == true : Locator.Locate() is not null;

    /// <summary>Calls <paramref name="f"/> after any change of what the control shows; the token removes it.</summary>
    public BoardObserverToken Observe(Action f) => observers.Add(f);

    // Inputs

    /// <summary>
    /// Whether the feature is available, or Claude Code where it is, may have
    /// changed: a request that lost it stops, the sign-in is asked again, and
    /// the change reported.
    /// </summary>
    public void AvailabilityChanged()
    {
        scope.VerifyAccess();
        if (state.IsRunning && !CanRun)
        {
            LogStep(logger, "stopped, no longer available");
            Cancel();
        }
        CheckSignIn();
        Notify();
    }

    /// <summary>The in-app provider changed: a request under way stops, and the availability is asked again (Swift <c>providerChanged</c>).</summary>
    public void ProviderChanged()
    {
        Cancel();
        AvailabilityChanged();
    }

    /// <summary>
    /// Asks Claude Code whether it is signed in (its cached answer unless a
    /// sign-in or <see cref="ClaudeCodeLocator.Refresh"/> dropped it); a late
    /// answer that a later check, or a request's own finding, overtook is
    /// dropped.
    /// </summary>
    public void CheckSignIn()
    {
        scope.VerifyAccess();
        signInGen++;
        if (ChatGpt)
        {
            SetSignedIn(Request.Provider?.Connected ?? false);
            return;
        }
        var g = signInGen;
        if (!available() || !RuntimeAvailable)
        {
            SetSignedIn(null);
            return;
        }
        var ask = Locator.SignedInAsync();
        scope.Run(async _ =>
        {
            var s = await ask;
            if (g == signInGen && !scope.IsClosed)
            {
                SetSignedIn(s);
            }
        });
    }

    // The view

    /// <summary>
    /// The control for case <paramref name="c"/> of snapshot <paramref name="s"/>
    /// (<see cref="Board.SuggestReplyViewOf"/>); <paramref name="samples"/>:
    /// the board shows the invented samples.
    /// </summary>
    public Board.SuggestReplyView View(Board.Case c, Board.Snapshot s, bool samples)
    {
        ArgumentNullException.ThrowIfNull(c);
        ArgumentNullException.ThrowIfNull(s);
        return Board.SuggestReplyViewOf(new Board.SuggestReplyInputs
        {
            Offered = Board.SuggestReplyOffered(c, s, samples),
            Available = available() && bridge is not null,
            ClaudeFound = RuntimeAvailable,
            SignedIn = signedIn,
            State = state,
            CaseId = c.Id,
            Provider = Settings.AssistantProvider,
        });
    }

    // A request

    /// <summary>
    /// Starts a suggested reply for <paramref name="c"/> with the user's
    /// <paramref name="instruction"/> (may be empty). False, and nothing
    /// changes, when one runs already or it cannot run: no reply target (the
    /// invented samples never have one), a suggested reply already, or the
    /// feature unavailable.
    /// </summary>
    public bool Start(Board.Case c, string instruction)
    {
        ArgumentNullException.ThrowIfNull(c);
        ArgumentNullException.ThrowIfNull(instruction);
        scope.VerifyAccess();
        if (state.IsRunning || !CanRun || c.Reply is null || c.Draft is not null || scope.IsClosed)
        {
            return false;
        }
        var my = ++gen;
        draftCalls = new(StringComparer.Ordinal);
        Created = null;
        linking = false;
        SetState(new Board.SuggestReplyState.Running(c.Id));
        LogStep(logger, "started");
        var id = c.Id;
        scope.Run(_ => RunAsync(my, id, instruction));
        return true;
    }

    /// <summary>Stop: the request under way ends as cancelled; a draft it created and that is not being linked is deleted.</summary>
    public void Cancel()
    {
        scope.VerifyAccess();
        if (state is not Board.SuggestReplyState.Running running)
        {
            return;
        }
        LogStep(logger, "stopped");
        End(running.Case, Board.SuggestReplyFailure.Cancelled);
    }

    /// <summary>
    /// <see cref="Cancel"/>, then waits until the <c>board.setDraft</c> and
    /// <c>draft.delete</c> calls on their way were answered, at most
    /// <paramref name="wait"/> (<see cref="EndWait"/> when null) on the
    /// controller's clock: quitting awaits it before the connection stops,
    /// and never hangs on a daemon that does not answer.
    /// </summary>
    public async Task CancelAndCleanUpAsync(TimeSpan? wait = null)
    {
        Cancel();
        var tasks = pending.Values.ToList();
        if (tasks.Count == 0)
        {
            return;
        }
        using var stop = new CancellationTokenSource();
        var bound = Task.Delay(wait ?? EndWait, time, stop.Token);
        await Task.WhenAny(Task.WhenAll(tasks), bound);
        await stop.CancelAsync();
    }

    /// <summary>Stops listening to the locator, the assistant and the settings, and cancels the request under way; the calls on their way still go.</summary>
    public void Dispose()
    {
        if (scope.IsClosed)
        {
            return;
        }
        Cancel();
        foreach (var u in unsubscribe)
        {
            u();
        }
        unsubscribe.Clear();
        scope.Close();
    }

    // Step 1: the assistant's consent, once ever; an answer counts even when
    // the request was stopped while the question was up.
    private async Task RunAsync(int my, BoardCaseId id, string instruction)
    {
        if (!HasConsent())
        {
            var allowed = await AskConsentAsync();
            if (allowed)
            {
                AcceptConsent();
            }
            if (my != gen)
            {
                return;
            }
            if (!allowed)
            {
                SetState(new Board.SuggestReplyState.Idle());
                return;
            }
        }
        AfterConsent(my, id, instruction);
    }

    private bool HasConsent() => ChatGpt ? Request.Provider?.HasConsent == true : Settings.AssistantConsent;

    private void AcceptConsent()
    {
        if (ChatGpt)
        {
            Request.Provider?.AcceptConsent();
        }
        else
        {
            Settings.AssistantConsent = true;
        }
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

    // The time runs from here, not while the question was up: the timer
    // starts, then step 2.
    private void AfterConsent(int my, BoardCaseId id, string instruction)
    {
        if (my != gen)
        {
            return;
        }
        StartTimer(my);
        scope.Perform(client, API.BoardGet, new BoardGetParams { CaseId = id }, o => GotCase(my, id, instruction, o));
    }

    private void StartTimer(int my)
    {
        StopTimer();
        var stop = CancellationTokenSource.CreateLinkedTokenSource(scope.Lifetime);
        timer = stop;
        // Made now, on the clock, so that a fake clock's next step sees it.
        var delay = Task.Delay(Timeout, time, stop.Token);
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
            TimedOut(my);
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

    // Step 2's answer: a case with a suggested reply already ends quietly,
    // otherwise step 3.
    private void GotCase(int my, BoardCaseId id, string instruction, Outcome<BoardGetResult> o)
    {
        if (!o.TryGetValue(out var r, out var error))
        {
            CallFailed("board.get", error);
            Fail(my, id, Board.SuggestReplyFailure.Backend);
            return;
        }
        if (my != gen || bridge is null)
        {
            return;
        }
        if (r.Case.Draft is not null)
        {
            // A suggested reply came meanwhile (the triage): it shows.
            LogStep(logger, "the case has a suggested reply already");
            StopTimer();
            SetState(new Board.SuggestReplyState.Idle());
            Refresh();
            return;
        }
        var message = Assistant.SuggestReplyMessage(
            r.Case.AccountId.Value, r.Case.ReplyMessageId.Value, [.. r.Messages.Select(m => m.Id.Value)], instruction);
        Request.Start(
            Assistant.SuggestReplySystemPrompt(),
            message,
            outcome => Answered(my, id, outcome),
            tools: new AssistantRequest.Tools
            {
                Bridge = bridge,
                Socket = socket,
                BridgeArgs = Assistant.SuggestReplyBridgeArgs(r.Case.ReplyMessageId.Value),
                Allowed = Assistant.SuggestReplyTools,
                Policy = AssistantToolPolicy.ReplyOnly,
            },
            timeout: Timeout + TimeSpan.FromSeconds(10),
            // The assistant's model (assistant-model), not the board's
            // triage model: the user asked for this reply and waits for it,
            // as for the panel and the compose rewrite.
            model: Settings.AssistantModel,
            onTool: e => Tool(my, e),
            // No usage is recorded (see the remarks of the class).
            onUsage: null);
    }

    // Notes the draft a create_draft of request my created.
    private void Tool(int my, AssistantEvent e)
    {
        if (my != gen)
        {
            return;
        }
        if (e.Kind == AssistantEventKind.ToolUse && e.Tool == Assistant.SuggestReplyDraftTool)
        {
            draftCalls.Add(e.ToolUseId);
        }
        else if (e.Kind == AssistantEventKind.ToolResult && draftCalls.Remove(e.ToolUseId))
        {
            if (!e.IsError && Created is null && Assistant.ParseDraftResult(e.ResultText) is { } created)
            {
                Created = created;
            }
        }
    }

    // The request of my ended.
    private void Answered(int my, BoardCaseId id, AssistantRequest.Outcome outcome)
    {
        if (my != gen)
        {
            return;
        }
        var created = Created;
        StopTimer();
        switch (outcome)
        {
            case AssistantRequest.Outcome.Declined:
                if (created is not null)
                {
                    Delete(created);
                }
                Created = null;
                SetState(new Board.SuggestReplyState.Idle());
                return;
            case AssistantRequest.Outcome.Failed { Failure: AssistantRequest.Failure.Stopped { Detail: AssistantRequest.TimedOut } }:
                if (created is not null)
                {
                    Delete(created);
                }
                Fail(my, id, Board.SuggestReplyFailure.Timeout);
                return;
            case AssistantRequest.Outcome.Answered:
                LearnSignedIn(true);
                break;
            case AssistantRequest.Outcome.Failed { Failure: AssistantRequest.Failure.NotSignedIn }:
                LearnSignedIn(false);
                break;
        }
        if (created is not null)
        {
            // The draft is there, whatever the turn did after it.
            Link(my, id, created);
            return;
        }
        Fail(my, id, outcome is AssistantRequest.Outcome.Failed failed ? FailureOf(failed.Failure) : Board.SuggestReplyFailure.NoDraft);
    }

    private static Board.SuggestReplyFailure FailureOf(AssistantRequest.Failure f) => f switch
    {
        AssistantRequest.Failure.NotFound => Board.SuggestReplyFailure.NotFound,
        AssistantRequest.Failure.NotSignedIn => Board.SuggestReplyFailure.NotSignedIn,
        AssistantRequest.Failure.ToolsMissing => Board.SuggestReplyFailure.ToolsMissing,
        AssistantRequest.Failure.Stopped { Detail: AssistantRequest.TimedOut } => Board.SuggestReplyFailure.Timeout,
        _ => Board.SuggestReplyFailure.Stopped,
    };

    // Step 4: board.setDraft for the created draft, then the board lists
    // again; a refused link deletes the draft.
    private void Link(int my, BoardCaseId id, DraftRef created)
    {
        linking = true;
        StopTimer();
        Track(API.BoardSetDraft, new BoardSetDraftParams { CaseId = id, DraftId = new DraftId(created.DraftId) }, o =>
        {
            ErrorCode? refused = null;
            if (!o.IsSuccess)
            {
                refused = (o.Error as RpcException)?.Error.Code ?? ErrorCode.InternalError;
                CallFailed("board.setDraft", o.Error);
                Delete(created);
            }
            Refresh();
            if (my != gen)
            {
                return;
            }
            linking = false;
            Created = null;
            if (refused is null)
            {
                LogStep(logger, "linked");
                SetState(new Board.SuggestReplyState.Idle());
            }
            else if (refused == ErrorCode.Conflict)
            {
                // The case got a suggested reply meanwhile: that one shows.
                SetState(new Board.SuggestReplyState.Idle());
            }
            else
            {
                SetState(new Board.SuggestReplyState.Failed(id, Board.SuggestReplyFailure.Backend));
            }
        });
    }

    // The request of my ran out of time.
    private void TimedOut(int my)
    {
        if (my != gen || state is not Board.SuggestReplyState.Running running)
        {
            return;
        }
        LogStep(logger, "no draft in time");
        End(running.Case, Board.SuggestReplyFailure.Timeout);
    }

    // Ends the request under way from outside its own steps (Stop, the
    // timeout): its process goes, a draft it created and that is not being
    // linked is deleted.
    private void End(BoardCaseId id, Board.SuggestReplyFailure failure)
    {
        gen++;
        StopTimer();
        Request.Cancel();
        if (Created is { } created && !linking)
        {
            Delete(created);
        }
        Created = null;
        SetState(new Board.SuggestReplyState.Failed(id, failure));
    }

    // Ends request my with failure.
    private void Fail(int my, BoardCaseId id, Board.SuggestReplyFailure failure)
    {
        if (my != gen)
        {
            return;
        }
        StopTimer();
        if (failure == Board.SuggestReplyFailure.NotSignedIn)
        {
            LearnSignedIn(false);
        }
        Created = null;
        LogFailed(logger, failure);
        SetState(new Board.SuggestReplyState.Failed(id, failure));
    }

    // Deletes a draft the request created and that is not linked.
    private void Delete(DraftRef created) =>
        Track(API.DraftDelete, new DraftDeleteParams { AccountId = new AccountId(created.AccountId), DraftId = new DraftId(created.DraftId) }, o =>
        {
            if (!o.IsSuccess)
            {
                CallFailed("draft.delete", o.Error);
            }
        });

    // One call on its way, kept in the pending calls until its answer was
    // handled (or the controller was disposed); sent even then.
    private void Track<TParams, TResult>(RpcMethod<TParams, TResult> method, TParams parameters, Action<Outcome<TResult>> after)
    {
        var key = ++nextPending;
        var task = scope.PerformPastClose(client, method, parameters, o =>
        {
            pending.Remove(key);
            after(o);
            // IsIdle changed.
            Notify();
        });
        if (!task.IsCompleted)
        {
            pending[key] = task;
        }
    }

    private void Refresh()
    {
        if (OnRefresh is { } refresh)
        {
            scope.Guard(refresh);
        }
    }

    private void LearnSignedIn(bool? s)
    {
        signInGen++;
        SetSignedIn(s);
    }

    private void SetSignedIn(bool? s)
    {
        if (s == signedIn)
        {
            return;
        }
        signedIn = s;
        Notify();
    }

    private void SetState(Board.SuggestReplyState s)
    {
        if (s == state)
        {
            return;
        }
        state = s;
        Notify();
    }

    private void Notify() => scope.Guard(observers.Notify);

    // The method and the kind of its failure: the daemon's code or the
    // exception's type, never a message.
    private void CallFailed(string method, Exception? e)
    {
        if (!logger.IsEnabled(LogLevel.Information))
        {
            return;
        }
        var kind = e switch
        {
            RpcException r => r.Error.Code.ToString(),
            null => "",
            _ => e.GetType().Name,
        };
        LogCallFailed(logger, method, kind);
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "board reply: {Step}")]
    private static partial void LogStep(ILogger logger, string step);

    [LoggerMessage(Level = LogLevel.Information, Message = "board reply: failed, {Failure}")]
    private static partial void LogFailed(ILogger logger, Board.SuggestReplyFailure failure);

    [LoggerMessage(Level = LogLevel.Information, Message = "board reply: {Method}: {Kind}")]
    private static partial void LogCallFailed(ILogger logger, string method, string kind);
}
