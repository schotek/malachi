// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/BoardTriageController.swift
// (BoardTriageController) and of its BoardAutoTriageTarget extension in
// BoardAutoTriageScheduler.swift; GTK: ui/internal/boardtriage/controller.go
// (Controller).
//
// The board's triage run (docs/api.md §4.13 "Triage and runs", docs/mcp.md
// "Triage of the board", docs/security.md §10.2), once for the whole
// application: the user's Claude Code (or, with the ChatGPT provider, its
// Codex session) annotates the cases of the board through the bridge's
// triage tools, in one bounded one-shot request (AssistantRequest with
// Tools). The daemon never talks to an assistant; this controller starts the
// run on the user's click (Start(Manual)) or on the schedule's
// (BoardAutoTriageScheduler).
//
// A run, each step of which may end it (State):
//
// 1. What it needs: triage available (the Assistant shown with the In App
//    target, TriageNeedsInAppTarget), the bridge beside the application,
//    Claude Code found and not signed out (asked afresh for a manual run),
//    and consent (ConsentGiven: the panel's assistant-consent, the board's
//    own board-triage-consent and the board's assistant preference). A
//    manual run without consent asks (Consent, the sheet) and gives it
//    (GiveConsentAsync); an automatic run never asks. A manual run whose
//    queue is known to be empty ends at once (NothingToDo).
// 2. board.runStart with the trigger and the source (Assistant.TriageSource,
//    "malachi-chatgpt" for the ChatGPT provider).
// 3. The request: Assistant.TriageMessage for at most the run's limit under
//    Assistant.TriageSystemPrompt, with the board's own model
//    (board-triage-model, read when the run starts; not the panel's
//    assistant-model), with the bridge started as --socket <socket>
//    --allow-triage --triage-run <runId> --triage-max <limit> and only
//    Assistant.TriageTools (no create_draft for an automatic run), for at
//    most Timeout. Every annotate_case call the bridge accepted counts as
//    progress, of the queue's size at the start capped by the limit; refused
//    ones are counted apart. When the accepted ones reach the limit the run
//    has succeeded: the bridge refuses more and closes the queue, telling
//    the model to stop, so the request is left to end by itself and deliver
//    Claude Code's result, whose usage is the whole run's; after Grace
//    (LimitGrace, 45 s) without it the request is cancelled.
// 4. board.runEnd with the failure's class, or none, and the tokens the run
//    used as far as its Claude Code reported them (AssistantUsageTally: the
//    result's usage, else the sum of the API messages seen before it ended,
//    a lower bound; none when nothing reported any); then RefreshRequested
//    asks the board to list again.
//
// One run at a time: Start while one is active does nothing. Cancel ends it
// (Cancelled, recorded so); so does losing what it needs while it runs. An
// automatic run that ends with no note accepted although its queue had
// cases fails (NotesRefused, NoProgress), so the schedule backs off. Nothing
// the model writes is shown or logged, nor any mail text.
//
// The provider follows Swift (the Windows client has the ChatGPT provider,
// Go's GTK client has the same seams as Source, ModelID and
// ConsentIdentity): a change of assistant-provider, assistant-codex-path,
// assistant-chatgpt-model or board-triage-chatgpt-model stops a run (and a
// provider switch turns automatic triage off, provider_test.go), and a
// consent whose write the switch overtook approves nothing.
//
// Windows: Swift's onRefresh is the event RefreshRequested, observe and
// observeEnded return BoardObserverTokens as Swift's; the clock, the grace,
// the timeout and cancelAndEnd's bound run on the injected TimeProvider, its
// now closure and Calendar are the TimeProvider and a time zone. The steps
// of a run are one tracked task of the controller's scope (docs/windows-
// port.md §7.2), the waits on the clock run detached, and board.runEnd is
// sent past a close (PerformPastClose). Close (IDisposable) stops listening
// and the clock, as Go's Close; a run under way goes on (CancelAndEndAsync
// ends it). Create it, and call it, on the UI thread.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Boards;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>The board's triage run, once for the application (Swift <c>BoardTriageController</c>).</summary>
public sealed partial class BoardTriageController : IBoardAutoTriageTarget, IDisposable
{
    /// <summary>
    /// Owner's decision (2026-10-01): triage is offered only under the
    /// assistant's "In App (experimental)" target, the condition of the panel,
    /// the compose rewrite and the search in the user's own words. It is the
    /// only target under which the application itself starts Claude Code, and
    /// it stays experimental until Anthropic confirms the terms for running
    /// Claude Code from an application.
    /// </summary>
    public const bool TriageNeedsInAppTarget = true;

    /// <summary>
    /// How long a run that reached its limit waits for Claude Code's result,
    /// the only report of the whole run's usage: one or two more API calls
    /// over its cached context (about 10 s each on the owner's first real
    /// run), so 45 s leaves room for a slow pair.
    /// </summary>
    public static readonly TimeSpan LimitGrace = TimeSpan.FromSeconds(45);

    /// <summary>How long <see cref="CancelAndEndAsync"/> waits for <c>board.runEnd</c> at most.</summary>
    public static readonly TimeSpan EndWait = TimeSpan.FromSeconds(2);

    /// <summary>How often the view is published again while it names a relative time (<see cref="Board.TriageView.RelativeTime"/>).</summary>
    public static readonly TimeSpan ClockTick = TimeSpan.FromSeconds(60);

    private readonly RpcClient client;
    private readonly string? bridge;
    private readonly string socket;
    private readonly Func<bool> available;
    private readonly TimeProvider time;
    private readonly TimeZoneInfo timeZone;
    private readonly Func<string> language;
    private readonly Func<string> today;
    private readonly ControllerScope scope;
    private readonly ILogger logger;
    private readonly BoardObservers observers = new();
    private readonly BoardObservers ended = new();
    private readonly List<BoardObserverToken> tokens = [];
    private readonly List<SettingsChangeToken> settingsTokens = [];
    private readonly Dictionary<int, Task> endTasks = [];
    private AssistantController? assistant;

    private Board.TriageState state = new Board.TriageState.Idle();
    private bool? signedIn;
    private int providerEpoch;

    // Bumped by every Start and Cancel: the steps of an older run stop at
    // their next await.
    private int gen;

    // The run of the daemon under way, once board.runStart answered.
    private BoardRunId? runId;

    // The annotate_case calls of the run waiting for their results, and the
    // ones the bridge refused.
    private HashSet<string> annotateCalls = [];

    // The distinct cases the run's accepted notes named (Tool).
    private HashSet<string> annotatedCases = new(StringComparer.Ordinal);
    private int refused;

    // The tokens the run's Claude Code reported so far.
    private AssistantUsageTally usage = new();

    // The most accepted annotate_case calls of the run.
    private int runLimit = Assistant.TriageBatch;

    // The run reached its limit and waits for Claude Code's result: it ends
    // as a success however it ends. graceStop ends the wait.
    private bool limitHit;
    private CancellationTokenSource? graceStop;

    // The run's queue was known to have cases when it started.
    private bool queueHadCases;

    // The run passed its consent step: a consent lost from now on stops it.
    private bool permitted;

    // Consent is being given: the repair of a stray assistant preference
    // waits.
    private bool granting;

    // Bumped by every sign-in check and every sign-in a run learnt: an older
    // check's late answer is dropped.
    private int signInGen;

    // The board.runEnd calls on their way.
    private int ending;
    private int nextEnd;

    // Publishes the view again while it names a relative time.
    private CancellationTokenSource? clockStop;

    /// <summary>A triage over <paramref name="client"/>, idle, on the calling (UI) thread.</summary>
    /// <param name="client">The daemon (board.runStart, board.runEnd).</param>
    /// <param name="settings">The consents, the models, the provider.</param>
    /// <param name="locator">The application's Claude Code (shared with the panel).</param>
    /// <param name="preferences">The board's preferences (the application's).</param>
    /// <param name="request">The one-shot request the runs use (its consent hook is cleared: the run asks itself).</param>
    /// <param name="bridge"><c>malachi-mcp</c> beside the application; null without.</param>
    /// <param name="socket">The daemon's socket, for the bridge.</param>
    /// <param name="available">Whether triage is available (<see cref="TriageAvailable"/>); its changes come through <see cref="AvailabilityChanged"/>.</param>
    /// <param name="time">The clock; the system's when null.</param>
    /// <param name="timeZone">Where "today" is counted; the local zone when null.</param>
    /// <param name="language">The English name of the UI language ("" English), for the system prompt.</param>
    /// <param name="today">Today as YYYY-MM-DD, for the system prompt.</param>
    /// <param name="logger">Receives steps, kinds and counts; never model text or mail.</param>
    /// <param name="pending">Counts the controller's background work; one of its own when null.</param>
    public BoardTriageController(
        RpcClient client,
        SettingsStore settings,
        ClaudeCodeLocator locator,
        BoardPreferencesController preferences,
        AssistantRequest request,
        string? bridge,
        string socket,
        Func<bool> available,
        TimeProvider? time = null,
        TimeZoneInfo? timeZone = null,
        Func<string>? language = null,
        Func<string>? today = null,
        ILogger<BoardTriageController>? logger = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(settings);
        ArgumentNullException.ThrowIfNull(locator);
        ArgumentNullException.ThrowIfNull(preferences);
        ArgumentNullException.ThrowIfNull(request);
        ArgumentNullException.ThrowIfNull(socket);
        ArgumentNullException.ThrowIfNull(available);
        this.client = client;
        Settings = settings;
        Locator = locator;
        Preferences = preferences;
        Request = request;
        this.bridge = bridge;
        this.socket = socket;
        this.available = available;
        this.time = time ?? TimeProvider.System;
        this.timeZone = timeZone ?? TimeZoneInfo.Local;
        this.language = language ?? (() => Assistant.LanguageName(L10n.Catalogue.Language));
        this.today = today ?? (() => AssistantPanelController.LocalDate(this.time));
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
        Timeout = Assistant.TriageTimeout;
        Grace = LimitGrace;
        // The run checks consent itself and never lets the request ask.
        request.Consent = null;
        request.UsesBoardConsent = true;
        request.ProviderModelId = () => settings.BoardChatGptModel;
        foreach (var key in new[] { SettingsKey.AssistantProvider, SettingsKey.AssistantCodexPath })
        {
            // The provider itself, or the ChatGPT provider's executable while
            // it is the one selected: a change of a provider not in use stops
            // nothing. A model (the panel's or the triage's ChatGPT model)
            // never ends a run: it applies from the next one (Swift
            // providerChangeConcernsActive); the consents go through
            // PermissionsChanged.
            var disable = key == SettingsKey.AssistantProvider;
            settingsTokens.Add(settings.OnChange(key, () =>
            {
                if (disable || settings.AssistantProvider == AssistantProviderID.ChatGpt)
                {
                    ProviderChanged(disable);
                }
            }));
        }
        tokens.Add(preferences.Observe(PermissionsChanged));
        tokens.Add(preferences.ObserveLoaded(RepairAssistantPreference));
        foreach (var key in new[] { SettingsKey.AssistantConsent, SettingsKey.BoardTriageConsent, SettingsKey.AssistantChatGptConsentVersion, SettingsKey.BoardChatGptConsentVersion })
        {
            settingsTokens.Add(settings.OnChange(key, PermissionsChanged));
        }
        // A sign-in that starts or ends: the view says it waits for the
        // browser meanwhile, and asks the sign-in again after.
        locator.SigningInChanged += OnSigningInChanged;
    }

    /// <summary>
    /// The application's triage: available as <see cref="TriageAvailable"/>
    /// says for <paramref name="assistant"/>'s <see cref="AssistantController.Shown"/>
    /// and the <c>assistant-target</c> setting, whose changes are reported here.
    /// </summary>
    public BoardTriageController(
        RpcClient client,
        SettingsStore settings,
        ClaudeCodeLocator locator,
        BoardPreferencesController preferences,
        AssistantRequest request,
        AssistantController assistant,
        string? bridge,
        string socket,
        TimeProvider? time = null,
        ILogger<BoardTriageController>? logger = null,
        PendingWork? pending = null)
        : this(
            client, settings, locator, preferences, request, bridge, socket,
            () => TriageAvailable(assistant?.Shown ?? false, settings?.AssistantTarget ?? AssistantTarget.Desktop),
            time, logger: logger, pending: pending)
    {
        ArgumentNullException.ThrowIfNull(assistant);
        this.assistant = assistant;
        assistant.Changed += OnAssistantChanged;
        settingsTokens.Add(settings.OnChange(SettingsKey.AssistantTarget, AvailabilityChanged));
    }

    /// <summary>
    /// Called after a run ended and from <see cref="RelistBoard"/>: the board
    /// should list again (Swift <c>onRefresh</c>; the application wires it to
    /// its board sources' Refresh).
    /// </summary>
    public event EventHandler<EventArgs>? RefreshRequested;

    /// <summary>The consents, the models, the provider.</summary>
    public SettingsStore Settings { get; }

    /// <summary>The application's Claude Code.</summary>
    public ClaudeCodeLocator Locator { get; }

    /// <summary>The board's preferences.</summary>
    public BoardPreferencesController Preferences { get; }

    /// <summary>The one-shot request the runs use.</summary>
    public AssistantRequest Request { get; }

    /// <summary>Where the application's run is.</summary>
    public Board.TriageState State
    {
        get => state;
        private set
        {
            if (value == state)
            {
                return;
            }
            state = value;
            Publish();
        }
    }

    /// <summary>Whether Claude Code is signed in, as last asked; null when not known.</summary>
    public bool? SignedIn
    {
        get => signedIn;
        private set
        {
            if (value == signedIn)
            {
                return;
            }
            signedIn = value;
            Publish();
        }
    }

    /// <summary>What the board last reported about the triage (<see cref="BoardChanged"/>).</summary>
    public BoardInfo Board { get; private set; } = new();

    /// <summary>Bumped whenever <see cref="Board"/> changed.</summary>
    public int BoardRevision { get; private set; }

    /// <summary>
    /// The board's last decisive phase from a snapshot (ready, preparing, off,
    /// unsupported); null before one. Off and unsupported take the triage away.
    /// </summary>
    public Boards.Board.Phase? BoardPhase { get; private set; }

    /// <summary>Why automatic triage pauses, as the schedule set it (<see cref="SetAutoPause"/>), for the view.</summary>
    public Boards.Board.AutoTriagePause? AutoPause { get; private set; }

    /// <summary>
    /// Asks the user whether the board's mail may go to the assistant (the
    /// sheet: <see cref="Boards.Board.Text.TriageConsentHeading"/>, the panel's
    /// Allow and Cancel); true allows. Without it a manual run that needs
    /// consent ends as declined; a hook that fails declines.
    /// </summary>
    public Func<Task<bool>>? Consent { get; set; }

    /// <summary>How long a run may take (<see cref="Assistant.TriageTimeout"/>; tests shorten it).</summary>
    public TimeSpan Timeout { get; set; }

    /// <summary>How long a run at its limit waits for the result (<see cref="LimitGrace"/>; tests shorten it).</summary>
    public TimeSpan Grace { get; set; }

    /// <summary>The run that ended last: its trigger and failure (null after a success).</summary>
    public BoardTriageEnd? LastEnded { get; private set; }

    /// <summary>Nothing is waiting or on its way (the tests wait for it).</summary>
    public bool IsIdle => !State.IsActive && ending == 0;

    /// <summary>The sign-in checks that were answered, taken or dropped (the tests wait for them).</summary>
    public int SignInAnswers { get; private set; }

    /// <summary>
    /// The tokens the run's assistant reported so far (the tests read it:
    /// Windows, where Swift's tests wait on the clock instead).
    /// </summary>
    public AssistantUsage? RunUsage => usage.Total;

    /// <summary>The clock of relative times is running (the tests read it).</summary>
    public bool ClockRunning => clockStop is not null;

    /// <summary>Whether <see cref="Close"/> ran.</summary>
    public bool IsClosed => scope.IsClosed;

    private bool ChatGpt => Settings.AssistantProvider == AssistantProviderID.ChatGpt;

    private bool RuntimeAvailable => ChatGpt ? Request.Provider?.Available == true : Locator.Locate() is not null;

    // The board's consent of the selected provider (Swift selectedBoardConsent).
    private bool SelectedBoardConsent
    {
        get => ChatGpt ? Settings.BoardChatGptConsentVersion == 1 : Settings.BoardTriageConsent;
        set
        {
            if (ChatGpt)
            {
                Settings.BoardChatGptConsentVersion = value ? 1 : 0;
            }
            else
            {
                Settings.BoardTriageConsent = value;
            }
        }
    }

    private DateTimeOffset Now => time.GetUtcNow();

    /// <summary>Whether triage is available with the Assistant <paramref name="shown"/> and <paramref name="target"/> chosen (<see cref="TriageNeedsInAppTarget"/>).</summary>
    public static bool TriageAvailable(bool shown, AssistantTarget target) =>
        shown && (!TriageNeedsInAppTarget || target == AssistantTarget.App);

    /// <summary>Calls <paramref name="f"/> after any change of what the view shows: the state, the sign-in, the board's triage, the consents, the preferences, the pause.</summary>
    public BoardObserverToken Observe(Action f)
    {
        ArgumentNullException.ThrowIfNull(f);
        return observers.Add(() => scope.Guard(f));
    }

    /// <summary>
    /// Calls <paramref name="f"/> once each time a run ended, read through
    /// <see cref="LastEnded"/>; <see cref="State"/> still says the run is
    /// active then, and changes right after.
    /// </summary>
    public BoardObserverToken ObserveEnded(Action f)
    {
        ArgumentNullException.ThrowIfNull(f);
        return ended.Add(() => scope.Guard(f));
    }

    /// <summary>Stops listening to the preferences, the settings, the locator and the assistant, and stops the clock; a run under way goes on.</summary>
    public void Close()
    {
        scope.VerifyAccess();
        foreach (var t in tokens)
        {
            t.Cancel();
        }
        tokens.Clear();
        foreach (var t in settingsTokens)
        {
            t.Cancel();
        }
        settingsTokens.Clear();
        Locator.SigningInChanged -= OnSigningInChanged;
        if (assistant is { } a)
        {
            a.Changed -= OnAssistantChanged;
            assistant = null;
        }
        StopClock();
        scope.Close();
    }

    /// <summary>Closes the controller.</summary>
    public void Dispose() => Close();

    // Inputs

    /// <summary>
    /// The board reported (<see cref="DaemonBoardSource.OnSnapshot"/>): the
    /// triage's queue and counts and the daemon's last run. A snapshot of a
    /// board that could not be listed changes nothing.
    /// </summary>
    public void BoardChanged(Boards.Board.Snapshot s)
    {
        ArgumentNullException.ThrowIfNull(s);
        switch (s.Phase)
        {
            case Boards.Board.Phase.Ready or Boards.Board.Phase.Preparing or Boards.Board.Phase.Off:
                break;
            case Boards.Board.Phase.Unsupported:
                // An older daemon: no board to triage, nothing else to learn.
                if (BoardPhase != Boards.Board.Phase.Unsupported)
                {
                    BoardPhase = Boards.Board.Phase.Unsupported;
                    Publish();
                }
                return;
            default:
                return;
        }
        if (BoardPhase != s.Phase)
        {
            BoardPhase = s.Phase;
            Publish();
        }
        var b = new BoardInfo
        {
            Known = true,
            AssistantOn = s.Annotated,
            Queue = s.Triage.Queue,
            AnnotatedToday = s.Triage.AnnotatedToday,
            CountedAt = Now,
            LastRun = s.Run,
            Usage24h = s.Triage.Usage24h,
        };
        if (Board.Known && b with { CountedAt = Board.CountedAt } == Board)
        {
            Board = Board with { CountedAt = b.CountedAt };
            return;
        }
        Board = b;
        BoardRevision++;
        Publish();
    }

    /// <summary>
    /// Whether triage is available, or Claude Code where it is, may have
    /// changed: a run that lost it stops, the sign-in is asked again, and the
    /// change reported.
    /// </summary>
    public void AvailabilityChanged()
    {
        Enforce();
        CheckSignIn();
        Publish();
    }

    /// <summary>
    /// The provider or its profile changed: a run under way stops, a
    /// provider switch turns automatic triage off
    /// (<paramref name="disableAutomaticTriage"/>: a consent given to one
    /// provider never starts runs of another), the daemon's assistant
    /// preference goes off when the board's consent is not given for the
    /// provider now selected, and a consent being given approves nothing.
    /// Both changes go in one quiet write, so that the repair is not skipped
    /// for the write under way (Go <c>ProviderChanged</c>).
    /// </summary>
    public void ProviderChanged(bool disableAutomaticTriage = false)
    {
        providerEpoch++;
        Cancel();
        var repair = !granting && !SelectedBoardConsent && Preferences.Stored?.Assistant != false;
        if (disableAutomaticTriage || repair)
        {
            if (repair)
            {
                LogRepaired(logger);
            }
            Preferences.Update(
                p => p with
                {
                    AutoTriage = !disableAutomaticTriage && p.AutoTriage,
                    Assistant = !repair && p.Assistant,
                },
                quiet: true);
        }
        AvailabilityChanged();
    }

    /// <summary>
    /// Asks the board to list again (<see cref="RefreshRequested"/>), so the
    /// tokens of the last 24 hours, which age out without a notification, are
    /// current (Preferences → AI asks whenever its page comes up).
    /// </summary>
    public void RelistBoard() => scope.Raise(RefreshRequested, this, EventArgs.Empty);

    /// <summary>Set by the schedule.</summary>
    public void SetAutoPause(Boards.Board.AutoTriagePause? pause)
    {
        if (pause == AutoPause)
        {
            return;
        }
        AutoPause = pause;
        Publish();
    }

    /// <summary>
    /// Asks Claude Code whether it is signed in (its cached answer unless a
    /// sign-in or the locator's Refresh dropped it). An answer that a later
    /// check, or a run's own finding, overtook is dropped.
    /// </summary>
    public void CheckSignIn()
    {
        var g = ++signInGen;
        if (ChatGpt)
        {
            SignedIn = Request.Provider?.Connected ?? false;
            return;
        }
        if (!RuntimeAvailable)
        {
            SignedIn = null;
            return;
        }
        if (IsClosed)
        {
            return;
        }
        scope.Run(async _ =>
        {
            var s = await Locator.SignedInAsync();
            SignInAnswers++;
            if (g != signInGen || IsClosed)
            {
                return;
            }
            SignedIn = s;
        });
    }

    /// <summary>Asks afresh (after a sign-in elsewhere, such as in a terminal).</summary>
    public void RecheckSignIn()
    {
        Locator.Refresh();
        CheckSignIn();
    }

    /// <summary>What a run learnt about the sign-in, which overtakes any check under way (public for the tests).</summary>
    public void LearnSignedIn(bool? s)
    {
        signInGen++;
        SignedIn = s;
    }

    // What it can do

    /// <summary>Triage is available, Claude Code is there and so is the bridge.</summary>
    public bool CanRun => available() && bridge is not null && RuntimeAvailable;

    /// <summary>Both consents and the board's assistant preference.</summary>
    public bool ConsentGiven =>
        (ChatGpt || Settings.AssistantConsent) && SelectedBoardConsent && Preferences.Preferences?.Assistant == true;

    /// <summary>A manual run would ask for consent first.</summary>
    public bool NeedsConsent => !ConsentGiven;

    /// <summary>
    /// The board source should run even while the Board is not shown: the
    /// schedule learns the queue only from its snapshots. True while automatic
    /// triage is on, consent is given and triage can run.
    /// </summary>
    public bool WantsBoardData => Preferences.Preferences?.AutoTriage == true && ConsentGiven && CanRun;

    /// <summary>
    /// The user agreed: the board's assistant preference goes on and, once the
    /// daemon stored it, both consents are kept (the board's sheet grants the
    /// panel's consent too: one sheet, both keys). True once stored; false
    /// sets no key, and a refusal is reported through the preferences'
    /// <see cref="BoardPreferencesController.ToastRequested"/> unless
    /// <paramref name="quiet"/> (a run reports its own failure). A provider
    /// switched meanwhile approves nothing: the preference goes off again.
    /// </summary>
    public async Task<bool> GiveConsentAsync(bool quiet = false)
    {
        var epoch = providerEpoch;
        var provider = Settings.AssistantProvider;
        granting = true;
        try
        {
            if (Preferences.Preferences?.Assistant != true || Preferences.Writing)
            {
                if (!await Preferences.UpdateAsync(p => p with { Assistant = true }, quiet))
                {
                    return false;
                }
            }
            if (epoch != providerEpoch || provider != Settings.AssistantProvider)
            {
                Preferences.Update(p => p with { Assistant = false, AutoTriage = false }, quiet: true);
                return false;
            }
            if (provider == AssistantProviderID.Claude)
            {
                Settings.AssistantConsent = true;
                Settings.BoardTriageConsent = true;
            }
            else
            {
                Settings.BoardChatGptConsentVersion = 1;
            }
            return true;
        }
        finally
        {
            granting = false;
        }
    }

    /// <summary>
    /// The user withdrew the board's consent (Preferences → AI): a run under
    /// way stops, the board's assistant preference goes off (its notes no
    /// longer count), so does automatic triage (a consent given again later
    /// must not bring back runs the user did not turn on again), and the
    /// assistant's consent for the panel stays.
    /// </summary>
    public void WithdrawConsent()
    {
        Cancel();
        SelectedBoardConsent = false;
        Preferences.Update(p => p with { Assistant = false, AutoTriage = false });
    }

    /// <summary>The Triage control and the status strip.</summary>
    public Boards.Board.TriageView View
    {
        get
        {
            var now = Now;
            int? annotatedToday = null;
            if (Board.Known && Board.CountedAt is { } at && SameDay(at, now))
            {
                annotatedToday = Board.AnnotatedToday;
            }
            var p = Preferences.Preferences;
            return Boards.Board.TriageViewOf(new Boards.Board.TriageViewInputs
            {
                Provider = Settings.AssistantProvider,
                Shown = available(),
                ClaudeFound = RuntimeAvailable,
                Bridge = bridge is not null,
                SignedIn = SignedIn,
                NeedsConsent = NeedsConsent,
                AssistantOn = Board.Known ? Board.AssistantOn : ConsentGiven,
                State = State,
                LastRun = Board.LastRun,
                AutoTriage = p?.AutoTriage ?? false,
                Pause = AutoPause,
                BackendFailed = p is null && Preferences.LastLoadFailed,
                AnnotatedToday = annotatedToday,
                BoardPhase = ViewBoardPhase,
                SigningIn = !ChatGpt && Locator.SigningIn,
                UsageKnown = Board.Known,
                Usage24h = Board.Usage24h,
                Queue = Board.Known && Board.AssistantOn ? Board.Queue : null,
                Now = now,
                TimeZone = timeZone,
            });
        }
    }

    // The board's phase for the view: off as the daemon's preferences say
    // once known (they are newer than a snapshot), else as the board last
    // reported.
    private Boards.Board.Phase? ViewBoardPhase
    {
        get
        {
            if (Preferences.Preferences is not { } p || BoardPhase == Boards.Board.Phase.Unsupported)
            {
                return BoardPhase;
            }
            if (!p.Enabled)
            {
                return Boards.Board.Phase.Off;
            }
            return BoardPhase == Boards.Board.Phase.Off ? null : BoardPhase;
        }
    }

    /// <summary>The rule's inputs as the controller knows them (Swift <c>autoTriageInputs</c>, Go <c>AutoInputs</c>).</summary>
    public Boards.Board.AutoTriage.Inputs AutoTriageInputs
    {
        get
        {
            var p = Preferences.Preferences;
            var lastAuto = Board.LastRun is { Trigger: BoardTrigger.Auto } run ? run.Started : null;
            return new Boards.Board.AutoTriage.Inputs
            {
                Trigger = Boards.Board.TriageTrigger.Automatic,
                Enabled = p?.AutoTriage ?? false,
                Available = CanRun,
                SignedIn = SignedIn,
                Consent = ConsentGiven,
                Running = State.IsActive,
                Queue = Board.Known && Board.AssistantOn ? Board.Queue : 0,
                AnnotatedToday = Board.AnnotatedToday,
                CountedAt = Board.CountedAt,
                DailyCap = p?.AutoTriageDailyCases ?? BoardLimits.DefaultBoardAutoTriageDailyCases,
                Minutes = p?.AutoTriageMinutes ?? BoardLimits.DefaultBoardAutoTriageMinutes,
                LastAttempt = lastAuto,
                Failures = 0,
                Now = Now,
            };
        }
    }

    // A run

    /// <summary>
    /// Starts a run (see the class's comment); <paramref name="limit"/> is the
    /// most cases it asks for (null: <see cref="Assistant.TriageBatch"/>).
    /// False when one is active.
    /// </summary>
    public bool Start(Boards.Board.TriageTrigger trigger, int? limit = null)
    {
        scope.VerifyAccess();
        if (State.IsActive)
        {
            return false;
        }
        var my = ++gen;
        runId = null;
        annotateCalls = [];
        annotatedCases = [];
        refused = 0;
        usage = new AssistantUsageTally();
        limitHit = false;
        permitted = false;
        queueHadCases = false;
        var max = Math.Max(1, Math.Min(limit ?? Assistant.TriageBatch, Assistant.TriageBatch));
        runLimit = max;
        State = new Boards.Board.TriageState.Starting(trigger);
        scope.Run(_ => RunAsync(my, trigger, max));
        return true;
    }

    /// <summary>Ends the run under way as cancelled; one that already reached its limit and waits for its result ends at once as the success it is.</summary>
    public void Cancel()
    {
        if (!State.IsActive || State.TriggeredBy is not { } trigger)
        {
            return;
        }
        gen++;
        Request.Cancel();
        var id = runId;
        runId = null;
        Finish(trigger, limitHit ? null : Boards.Board.TriageFailure.Cancelled, id);
    }

    /// <summary>
    /// <see cref="Cancel"/>, then waits until the <c>board.runEnd</c> calls on
    /// their way were answered, at most <paramref name="wait"/>
    /// (<see cref="EndWait"/> when null): quitting awaits it before the
    /// connection stops, and never hangs on a daemon that does not answer.
    /// </summary>
    public async Task CancelAndEndAsync(TimeSpan? wait = null)
    {
        Cancel();
        var pending = endTasks.Values.ToList();
        if (pending.Count == 0)
        {
            return;
        }
        using var stop = new CancellationTokenSource();
        var bound = Task.Delay(wait ?? EndWait, time, stop.Token);
        await Task.WhenAny(Task.WhenAll(pending), bound);
        await stop.CancelAsync();
    }

    private async Task RunAsync(int my, Boards.Board.TriageTrigger trigger, int limit)
    {
        // 1. What it needs.
        if (!available())
        {
            Fail(my, trigger, Boards.Board.TriageFailure.AssistantOff);
            return;
        }
        if (bridge is not { } bridgePath)
        {
            Fail(my, trigger, Boards.Board.TriageFailure.ToolsMissing);
            return;
        }
        if (!RuntimeAvailable)
        {
            Fail(my, trigger, Boards.Board.TriageFailure.NotFound);
            return;
        }
        if (Preferences.Preferences is null)
        {
            await Preferences.LoadNowAsync();
            if (my != gen)
            {
                return;
            }
            // A daemon that does not answer the board's preferences (or does
            // not know the board) cannot take a consent: no sheet.
            if (Preferences.Preferences is null)
            {
                Fail(my, trigger, Boards.Board.TriageFailure.Backend);
                return;
            }
        }
        if (!ConsentGiven)
        {
            if (trigger != Boards.Board.TriageTrigger.Manual || Consent is not { } ask)
            {
                Fail(my, trigger, Boards.Board.TriageFailure.Declined);
                return;
            }
            var allowed = await AskAsync(ask);
            if (my != gen)
            {
                return;
            }
            if (!allowed)
            {
                Fail(my, trigger, Boards.Board.TriageFailure.Declined);
                return;
            }
            var stored = await GiveConsentAsync(quiet: true);
            if (my != gen)
            {
                return;
            }
            if (!stored)
            {
                Fail(my, trigger, Boards.Board.TriageFailure.Backend);
                return;
            }
        }
        permitted = true;
        // A consent lost while the sheet was up stops it here.
        if (!ConsentGiven)
        {
            Fail(my, trigger, Boards.Board.TriageFailure.Declined);
            return;
        }
        // The queue is known only from a board that listed with the
        // assistant on.
        var queueKnown = Board.Known && Board.AssistantOn;
        if (queueKnown && Board.Queue <= 0)
        {
            Fail(my, trigger, Boards.Board.TriageFailure.NothingToDo);
            return;
        }
        queueHadCases = queueKnown && Board.Queue > 0;
        if (trigger == Boards.Board.TriageTrigger.Manual)
        {
            Locator.Refresh();
        }
        var signed = await RuntimeSignedInAsync();
        if (my != gen)
        {
            return;
        }
        LearnSignedIn(signed);
        if (signed == false)
        {
            Fail(my, trigger, Boards.Board.TriageFailure.NotSignedIn);
            return;
        }
        // 2. The daemon's run.
        BoardRunId id;
        try
        {
            var r = await client.CallAsync(
                API.BoardRunStart,
                new BoardRunStartParams { Trigger = trigger.Wire, Source = ChatGpt ? "malachi-chatgpt" : Assistant.TriageSource },
                scope.Lifetime);
            id = r.RunId;
        }
        catch (Exception e) when (e is RpcException or RpcClientException or OperationCanceledException or TimeoutException)
        {
            LogCallFailed(API.BoardRunStart.Name, e);
            Fail(my, trigger, Boards.Board.TriageFailure.Backend);
            return;
        }
        if (my != gen)
        {
            // Cancelled while it started: it is ended as such.
            EndRun(id, BoardRunError.Cancelled);
            return;
        }
        runId = id;
        var total = queueKnown ? Math.Min(Board.Queue, limit) : limit;
        State = new Boards.Board.TriageState.Running(trigger, 0, total);
        LogRunStarted(logger, trigger);
        // 3. The request.
        var drafts = Assistant.TriageDrafts(trigger == Boards.Board.TriageTrigger.Manual);
        Request.Start(
            Assistant.TriageSystemPrompt(language(), today()),
            Assistant.TriageMessage(limit, drafts),
            o => Answered(my, trigger, o),
            tools: new AssistantRequest.Tools
            {
                Bridge = bridgePath,
                Socket = socket,
                BridgeArgs = Assistant.TriageBridgeArgs(id.Value, limit),
                Allowed = Assistant.TriageTools(drafts),
                Policy = drafts ? AssistantToolPolicy.TriageDrafts : AssistantToolPolicy.Triage,
            },
            timeout: Timeout,
            model: Settings.BoardTriageModel,
            onTool: e => Tool(my, trigger, e),
            onUsage: e => Counted(my, e));
    }

    // The consent hook's answer; one that fails declines, and is reported.
    private async Task<bool> AskAsync(Func<Task<bool>> ask)
    {
        try
        {
            return await ask();
        }
#pragma warning disable CA1031 // A failed hook is a declined consent, and reported.
        catch (Exception e)
#pragma warning restore CA1031
        {
            scope.Pending.Report(e);
            return false;
        }
    }

    private async Task<bool?> RuntimeSignedInAsync()
    {
        if (ChatGpt)
        {
            return Request.Provider?.Connected ?? false;
        }
        return await Locator.SignedInAsync();
    }

    // Counts an annotate_case of run my, accepted (each case once) or
    // refused, until the accepted cases reach the run's limit
    // (LimitReached). The bridge takes a second note on a case without
    // charging another of the run's cases, so Done counts distinct cases; a
    // result that names no case counts as a case of its own.
    private void Tool(int my, Boards.Board.TriageTrigger trigger, AssistantEvent e)
    {
        if (my != gen || limitHit || State is not Boards.Board.TriageState.Running running)
        {
            return;
        }
        if (e.Kind == AssistantEventKind.ToolUse && e.Tool == Assistant.TriageAnnotateTool)
        {
            annotateCalls.Add(e.ToolUseId);
        }
        else if (e.Kind == AssistantEventKind.ToolResult && annotateCalls.Remove(e.ToolUseId))
        {
            if (e.IsError)
            {
                refused++;
                return;
            }
            var key = Assistant.TriageAnnotatedCase(e.ResultText) ?? "call:" + e.ToolUseId;
            if (!annotatedCases.Add(key))
            {
                return;
            }
            var done = annotatedCases.Count;
            State = new Boards.Board.TriageState.Running(trigger, done, running.Total);
            if (done >= runLimit)
            {
                LimitReached(my, trigger);
            }
        }
    }

    // Counts the usage an event of run my carries.
    private void Counted(int my, AssistantEvent e)
    {
        if (my == gen)
        {
            usage.Add(e);
        }
    }

    // The run's limit of accepted notes is reached: the run has succeeded
    // whatever the model does next. The request goes on until Claude Code's
    // result (Answered), which carries the whole run's usage, for at most
    // Grace; then it is cancelled and the run ends with the usage seen so
    // far, a lower bound. The wait is made now, on the clock, so that a fake
    // clock's next step is sure to see it.
    private void LimitReached(int my, Boards.Board.TriageTrigger trigger)
    {
        LogLimitReached(logger, runLimit);
        limitHit = true;
        LearnSignedIn(true);
        graceStop?.Cancel();
        var stop = CancellationTokenSource.CreateLinkedTokenSource(scope.Lifetime);
        graceStop = stop;
        var delay = Task.Delay(Grace, time, stop.Token);
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
            if (my != gen || !limitHit)
            {
                return;
            }
            LogNoResult(logger);
            gen++;
            Request.Cancel();
            var id = runId;
            runId = null;
            Finish(trigger, null, id);
        });
    }

    // The request of run my ended.
    private void Answered(int my, Boards.Board.TriageTrigger trigger, AssistantRequest.Outcome outcome)
    {
        if (my != gen)
        {
            return;
        }
        var notSignedIn = outcome is AssistantRequest.Outcome.Failed { Failure: AssistantRequest.Failure.NotSignedIn };
        if (limitHit)
        {
            // At its limit the run has succeeded, whatever the result says;
            // its usage was counted before this.
            if (notSignedIn)
            {
                LearnSignedIn(false);
            }
            if (outcome is AssistantRequest.Outcome.Answered)
            {
                usage.Finished();
            }
            var done = runId;
            runId = null;
            Finish(trigger, null, done);
            return;
        }
        Boards.Board.TriageFailure? failure = null;
        switch (outcome)
        {
            case AssistantRequest.Outcome.Answered:
                usage.Finished();
                if (State is Boards.Board.TriageState.Running { Done: 0 })
                {
                    if (refused > 0)
                    {
                        failure = Boards.Board.TriageFailure.NotesRefused;
                    }
                    else if (trigger == Boards.Board.TriageTrigger.Automatic && queueHadCases)
                    {
                        failure = Boards.Board.TriageFailure.NoProgress;
                    }
                }
                break;
            case AssistantRequest.Outcome.Declined:
                failure = Boards.Board.TriageFailure.Declined;
                break;
            case AssistantRequest.Outcome.Failed f:
                failure = f.Failure switch
                {
                    AssistantRequest.Failure.NotFound => Boards.Board.TriageFailure.NotFound,
                    AssistantRequest.Failure.NotSignedIn => Boards.Board.TriageFailure.NotSignedIn,
                    AssistantRequest.Failure.ToolsMissing => Boards.Board.TriageFailure.ToolsMissing,
                    AssistantRequest.Failure.Limit => Boards.Board.TriageFailure.Limit,
                    AssistantRequest.Failure.Stopped { Detail: AssistantRequest.TimedOut } => Boards.Board.TriageFailure.Timeout,
                    _ => Boards.Board.TriageFailure.Stopped,
                };
                break;
        }
        if (failure == Boards.Board.TriageFailure.NotSignedIn)
        {
            LearnSignedIn(false);
        }
        else if (outcome is AssistantRequest.Outcome.Answered)
        {
            LearnSignedIn(true);
        }
        var id = runId;
        runId = null;
        Finish(trigger, failure, id);
    }

    // Ends the run with failure (null: a success): the daemon's run is
    // ended, the board asked again, the end reported and the state set.
    private void Finish(Boards.Board.TriageTrigger trigger, Boards.Board.TriageFailure? failure, BoardRunId? run)
    {
        graceStop?.Cancel();
        graceStop = null;
        limitHit = false;
        var annotated = State is Boards.Board.TriageState.Running r ? r.Done : 0;
        if (run is { } id)
        {
            BoardUsage? total = usage.Total is { } u
                ? new BoardUsage
                {
                    InputTokens = u.InputTokens,
                    OutputTokens = u.OutputTokens,
                    CacheCreationInputTokens = u.CacheCreationInputTokens,
                    CacheReadInputTokens = u.CacheReadInputTokens,
                    LowerBound = usage.LowerBound ? true : null,
                }
                : null;
            EndRun(id, failure?.RunError, total);
        }
        // The end first, while State still says active: the schedule counts
        // the failure before the state's change asks it again.
        LastEnded = new BoardTriageEnd(trigger, failure);
        ended.Notify();
        permitted = false;
        if (failure is { } f)
        {
            LogEnded(logger, f);
            State = new Boards.Board.TriageState.Failed(trigger, f, Now);
        }
        else
        {
            LogFinished(logger, annotated, refused);
            State = new Boards.Board.TriageState.Finished(trigger, annotated, refused, Now);
        }
    }

    // A failure before the daemon's run started.
    private void Fail(int my, Boards.Board.TriageTrigger trigger, Boards.Board.TriageFailure failure)
    {
        if (my != gen)
        {
            return;
        }
        if (failure == Boards.Board.TriageFailure.NotSignedIn)
        {
            LearnSignedIn(false);
        }
        Finish(trigger, failure, null);
    }

    // board.runEnd (usage null: not known, left out), then the board listed
    // again; sent even when the controller closes meanwhile.
    private void EndRun(BoardRunId id, BoardRunError? error, BoardUsage? total = null)
    {
        ending++;
        var key = ++nextEnd;
        endTasks[key] = scope.PerformPastClose(
            client,
            API.BoardRunEnd,
            new BoardRunEndParams { RunId = id, Error = error, Usage = total },
            outcome =>
            {
                if (!outcome.TryGetValue(out _, out var e))
                {
                    LogCallFailed(API.BoardRunEnd.Name, e);
                }
                ending--;
                endTasks.Remove(key);
                scope.Raise(RefreshRequested, this, EventArgs.Empty);
            });
    }

    // The consents or the preferences changed.
    private void PermissionsChanged()
    {
        Enforce();
        Publish();
    }

    // Stops the run under way when it lost what it needs (see the class's
    // comment).
    private void Enforce()
    {
        if (!State.IsActive || State.TriggeredBy is not { } trigger)
        {
            return;
        }
        var p = Preferences.Preferences;
        var lost = !available() || (permitted && !ConsentGiven) || p?.Enabled == false
            || (trigger == Boards.Board.TriageTrigger.Automatic && p?.AutoTriage == false);
        if (lost)
        {
            LogStopped(logger);
            Cancel();
        }
    }

    // The daemon answered its preferences: its assistant preference on while
    // the board's consent is not given here (a withdrawal whose write
    // failed, say) is turned off again, so the daemon stops handing mail to
    // a triage bridge. Not while consent is being given or a write is under
    // way (each write reads afresh first).
    private void RepairAssistantPreference()
    {
        if (granting || Preferences.Writing || SelectedBoardConsent || Preferences.Stored?.Assistant != true)
        {
            return;
        }
        LogRepaired(logger);
        Preferences.Update(p => p with { Assistant = false }, quiet: true);
    }

    private void OnSigningInChanged(object? sender, EventArgs e) => scope.Guard(() =>
    {
        CheckSignIn();
        Publish();
    });

    private void OnAssistantChanged(object? sender, EventArgs e) => scope.Guard(AvailabilityChanged);

    // Reports a change, and keeps the clock of relative times running exactly
    // while the view names one.
    private void Publish()
    {
        observers.Notify();
        UpdateClock();
    }

    private void UpdateClock()
    {
        if (IsClosed || !View.RelativeTime)
        {
            StopClock();
            return;
        }
        if (clockStop is not null)
        {
            return;
        }
        var stop = CancellationTokenSource.CreateLinkedTokenSource(scope.Lifetime);
        clockStop = stop;
        var delay = Task.Delay(ClockTick, time, stop.Token);
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
            if (clockStop != stop)
            {
                return;
            }
            // Published again; UpdateClock starts the next tick while the
            // view still names a relative time.
            clockStop = null;
            stop.Dispose();
            Publish();
        });
    }

    private void StopClock()
    {
        if (clockStop is { } c)
        {
            clockStop = null;
            c.Cancel();
        }
    }

    private bool SameDay(DateTimeOffset a, DateTimeOffset b) =>
        TimeZoneInfo.ConvertTime(a, timeZone).Date == TimeZoneInfo.ConvertTime(b, timeZone).Date;

    private void LogCallFailed(string method, Exception? error)
    {
        if (logger.IsEnabled(LogLevel.Information))
        {
            var (kind, daemonError) = RpcErrorText.Classify(error);
            LogFailed(logger, method, kind, daemonError?.Code.Value ?? 0);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "{Method} failed: {Kind} {Code}")]
    private static partial void LogFailed(ILogger logger, string method, RpcErrorText.FailureKind kind, int code);

    [LoggerMessage(Level = LogLevel.Information, Message = "board triage: run started ({Trigger})")]
    private static partial void LogRunStarted(ILogger logger, Boards.Board.TriageTrigger trigger);

    [LoggerMessage(Level = LogLevel.Information, Message = "board triage: the run's limit of {Limit} notes is reached")]
    private static partial void LogLimitReached(ILogger logger, int limit);

    [LoggerMessage(Level = LogLevel.Information, Message = "board triage: no result after the limit in time")]
    private static partial void LogNoResult(ILogger logger);

    [LoggerMessage(Level = LogLevel.Information, Message = "board triage: ended, {Failure}")]
    private static partial void LogEnded(ILogger logger, Boards.Board.TriageFailure failure);

    [LoggerMessage(Level = LogLevel.Information, Message = "board triage: ended, {Annotated} annotated, {Refused} refused")]
    private static partial void LogFinished(ILogger logger, int annotated, int refused);

    [LoggerMessage(Level = LogLevel.Information, Message = "board triage: stopped, no longer allowed or available")]
    private static partial void LogStopped(ILogger logger);

    [LoggerMessage(Level = LogLevel.Information, Message = "board triage: the assistant preference was on without consent; turning it off")]
    private static partial void LogRepaired(ILogger logger);
}
