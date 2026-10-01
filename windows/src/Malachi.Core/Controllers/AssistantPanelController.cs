// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController: the hooks, running, isPinned, effectiveContext,
// contextLabel, anotherSelected, canRunActions, quickTarget, pinnedLabel,
// subjectLabel, messageCount, placeholder, pendingLabel, subtitle,
// setContext, removeContext, addSelection, add, pinnedKey, pinnedContext,
// newKey, run, run(_:on:), perform, summarizeUnread, askAttachment,
// cancelPending, submit, retry, signIn, openDraft, stop, newConversation,
// close, start, prepare, prompt, unresolved, resolvePinned, settle, resolve,
// launch, handle, exited, fail, clearRetries, endProcess, endSignIn,
// closeSignIn, closeTurn, closeStreaming, append, localDate, Once); GTK:
// ui/internal/assistantpanel/controller.go (Controller, its port).
//
// Windows differences:
// - Every Swift Task goes through the controller scope (docs/windows-port.md
//   §7.2), yield-first and tracked: a question's steps and a folded
//   conversation's members (Run); the resolve timeout waits detached on the
//   injected TimeProvider (RunDetached), made when the members are asked
//   for, and a Close ends it. A question's steps end at once when a Stop, a
//   New Conversation or a Close came before they began, as GTK's askConsent
//   checks (Swift's prepare starts with the consent).
// - The process is ClaudeCodeProcess: Terminate closes stdin and kills the
//   process tree after the kill grace, on the TimeProvider. Each process is
//   counted in the PendingWork from its start until its end was reported.
// - The working directory is made private by an IPrivateDirectoryFactory (a
//   protected DACL), as ClaudeCodeLocator and AssistantRequest make it, where
//   Swift sets mode 0700; Directory.CreateDirectory without one. A failure
//   is "the assistant's directory: …", GTK's words.
// - The hooks: Consent is a Func<Task<bool>>; one that throws declines and
//   is reported as a callback's failure (§7.5), as AssistantRequest's. The
//   ResolveContext hook, and the done it is given, run on the UI thread; a
//   hook that throws is reported and the timeout answers. Swift's onChange,
//   onState, onFocusInput and onRestoreInput are the events Changed,
//   StateChanged, FocusInputRequested and RestoreInputRequested, each
//   handler guarded (§7.5).
// - Swift's Content and Pending enums are record hierarchies at the
//   namespace's top level (AssistantPanelContent, AssistantPanelPending);
//   Item, Context and Pinned are immutable records the controller replaces
//   where Swift mutates. pinnedLabel and messageCount are public (Swift's
//   are internal, reached by @testable; Core grants no internals to its
//   tests), and so is the process, read-only, as Swift's.
// - An empty bridge counts as none (GTK's ""): without --mcp-config the
//   panel's Claude Code would run without the tools.
// - The question field's words are trimmed of White_Space (Go's
//   strings.TrimSpace, the set of Swift's .whitespacesAndNewlines).
// - Close closes the scope too: nothing is resolved or started afterwards,
//   and a late answer changes nothing. Dispose is Close (IDisposable, CA1001).
// - Only kinds, numbers and tool names are logged (the prompt's error kind
//   where Swift logs Go's text, the start failure's kind, the turn's
//   success, cost and denials, each denied tool as Swift); never mail or
//   model text.

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.ComponentModel;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Daemon;
using Malachi.Core.I18n;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The assistant panel of the main window (the In App target) without its
/// views: the conversation with the user's own Claude Code, restricted to
/// the <c>malachi-mcp</c> tools, as a list of items the panel shows.
/// </summary>
/// <remarks>
/// <para>A question goes through these steps, each of which may end it:</para>
/// <list type="number">
/// <item><see cref="Phase.Idle"/> → <see cref="Phase.Preparing"/>. The first
/// question ever asks for consent (<see cref="Consent"/>, the dialog "Send
/// Mail to Claude?"; the answer is kept in <c>assistant-consent</c>).
/// Declined: nothing happens, and the typed text goes back into the field
/// (<see cref="RestoreInputRequested"/>).</item>
/// <item>The question appears in the transcript (a user item: the action's
/// label and the typed text) and a waiting message action is used up. The
/// first question of a conversation pins its context (see below).</item>
/// <item>The ids of the contexts the prompt names are completed: a folded
/// conversation stands for its newest message until its members are known
/// (<see cref="ResolveContext"/>, which the application answers with the
/// list's selection; after <see cref="ResolveTimeout"/> the newest message
/// alone is used).</item>
/// <item>Without a running process: Claude Code is located (none: "Claude
/// Code was not found on this computer"), the bridge must be there (none:
/// the tools are not available), and Claude Code must not say it is signed
/// out (<see cref="ClaudeCodeLocator.SignedInAsync"/>, asked afresh: "Claude
/// Code is not signed in" with Sign In…, see below; not known counts as
/// signed in, and the process then says what is wrong). Then the process
/// starts
/// (<see cref="Assistant.Args"/>, <see cref="Assistant.ChildEnvironment"/>,
/// the private directory) and is kept for the follow-up questions of the
/// conversation.</item>
/// <item><see cref="Phase.Running"/>: the turn is written to stdin. Its
/// <c>system/init</c> must report the bridge connected, or the conversation
/// ends with "The Malachi Mail tools are not available to the assistant".
/// Text deltas stream into the current answer item, a whole text block
/// replaces it; a tool call is an activity line until its result; a
/// create_draft result whose bridge line names a draft
/// (<see cref="Assistant.ParseDraftResult"/>) adds a draft card, whose Open
/// Draft the application checks (<see cref="OpenDraft"/>). The result ends
/// the turn (<see cref="Phase.Idle"/>); one that is not a success adds "The
/// assistant stopped: …". The process ending during a turn does the same
/// with its stderr's first line. A message Claude Code wrote itself because
/// the API refused the turn (<see cref="AssistantEventKind.Failure"/>) is no
/// answer: the result repeats it. When the API refused the sign-in (expired
/// or revoked, whatever <c>claude auth status</c> says), the turn ends with
/// "Claude Code is not signed in" and Sign In…, and the process ends, for a
/// new sign-in takes a new one. When Claude Code could not refresh its
/// sign-in (<see cref="AssistantEvent.RefreshFailed"/>: another Claude Code
/// was refreshing it, or ended in the middle of that), the turn ends with the
/// result's "The assistant stopped: …" and both Try Again (Claude Code takes
/// the refresh over after about a minute) and Sign In…, and the process ends
/// the same way.</item>
/// </list>
/// <para>
/// Sign In… (<see cref="SignIn"/>) sends the same question once more, with
/// Claude Code's own sign-in in front of step 4's start: the line "Waiting
/// for the sign-in in your browser…" is an activity while <c>claude auth
/// login</c> runs (<see cref="ClaudeCodeLocator.SignInAsync"/>: the browser
/// opens, the application sees no credential), then the process starts and
/// the question is asked. A sign-in that fails, takes too long or is taken
/// over by the settings' ends the turn with its reason and Sign In… again;
/// <see cref="Stop"/> ends it like any turn. "Claude Code was not found on
/// this computer" offers Get Claude Code… (<see cref="ErrorOffer.Install"/>),
/// which the view opens in the browser, beside Try Again.
/// </para>
/// <para>
/// <see cref="Stop"/> ends the process and the turn with the note "The
/// conversation was stopped"; the next question starts a new process (a new
/// conversation for Claude). <see cref="NewConversation"/> ends the process
/// and empties the transcript. Errors of steps 4 and 5 offer Try Again,
/// which sends the same question once more. Nothing is kept on disk: Claude
/// Code runs with <c>--no-session-persistence</c> in an empty directory, and
/// the transcript lives in memory. Costs are logged as numbers, never mail
/// or model text.
/// </para>
/// <para>
/// A conversation keeps its context. Before its first question the chip
/// follows the list's selection (<see cref="SetContext"/>, called by the
/// application) unless its remove button (<see cref="RemoveContext"/>)
/// leaves it at all mail until the next selection change. The first
/// question (a quick action, a waiting action's words, a free question, a
/// menu's action, Summarize Unread in This Folder) pins what the chip showed
/// then: <see cref="PinnedContexts"/> starts with it, the chip names it
/// (<see cref="Assistant.ConversationLabel"/>), and from then on the
/// selection only decides whether the bar "Another message is selected"
/// shows (<see cref="AnotherSelected"/>). Its Add to Conversation
/// (<see cref="AddSelection"/>) appends the selection to the pinned
/// contexts; its New Conversation ends the conversation, and the chip
/// follows the selection again. The quick actions act on the newest pinned
/// context; a menu's action (<see cref="RunOn"/>) or an attachment's
/// question on a message that is part of no pinned context adds it first.
/// </para>
/// <para>
/// The model hears of each pinned context once per Claude Code process: a
/// free question carries a model-facing line in front of it for every
/// context not yet told (<see cref="Assistant.ContextPreamble"/> for the
/// first, <see cref="Assistant.AddedContextPreamble"/> for one added later),
/// and an action's own prompt, which names its ids, tells the model of its
/// context. A new process (after Stop, or when Claude Code ended) knows
/// nothing, so the contexts are told again.
/// </para>
/// <para>
/// Summarize and Tasks and Deadlines send at once, Draft a Reply… and Ask
/// About This Message… (and an attachment's question) wait for the user's
/// words (<see cref="Pending"/>: the field's placeholder changes); Summarize
/// Unread in This Folder sends for a folder. Create it, and call it, on the
/// UI thread.
/// </para>
/// </remarks>
public sealed partial class AssistantPanelController : IDisposable
{
    /// <summary>How long the members of a folded conversation are waited for.</summary>
    public static readonly TimeSpan DefaultResolveTimeout = TimeSpan.FromSeconds(10);

    private readonly string? bridge;
    private readonly string socket;
    private readonly string directory;
    private readonly IReadOnlyDictionary<string, string> environment;
    private readonly TimeSpan killGrace;
    private readonly IPrivateDirectoryFactory? directories;
    private readonly ILogger logger;
    private readonly ILogger<ClaudeCodeProcess>? processLogger;
    private readonly ControllerScope scope;

    private readonly List<Item> items = [];
    private readonly ReadOnlyCollection<Item> itemsView;
    private readonly List<Pinned> pinned = [];
    private readonly ReadOnlyCollection<Pinned> pinnedView;

    // The activity items by tool call id, and each call's tool.
    private readonly Dictionary<string, int> activities = new(StringComparer.Ordinal);
    private readonly Dictionary<string, string> toolNames = new(StringComparer.Ordinal);

    // The pinned folded conversations whose members are being asked for.
    private readonly Dictionary<int, Task> resolving = [];

    private SettingsChangeToken? settingsToken;

    // What the waiting action acts on; null for the chip's context when its
    // words are sent (before the first question).
    private Target? pendingTarget;

    // Bumped by every question, Stop, NewConversation and Close: the steps
    // of an older question stop at their next await.
    private int gen;
    private int nextId;

    // The answer item that deltas stream into.
    private int? streaming;

    // The question of the turn under way (or the last that failed), for Try
    // Again.
    private Request? lastRequest;

    // The API refused the sign-in in the turn under way; refreshFailed:
    // Claude Code could not refresh it.
    private bool authFailed;
    private bool refreshFailed;

    // The sign-in's activity line, and the sign-in this question started.
    private int? signingIn;
    private Task<ClaudeCodeSignIn>? signInRun;

    // The next Pinned.Key.
    private int nextKey;

    /// <summary>A panel with an empty transcript, on the calling (UI) thread.</summary>
    /// <param name="settings">The model, the consent, Claude Code's path.</param>
    /// <param name="locator">Finds and asks Claude Code (the application's, shared with the one-shot requests).</param>
    /// <param name="bridge"><c>malachi-mcp.exe</c> beside the application (<see cref="Paths.McpBridge"/>); null (or "") without one.</param>
    /// <param name="socket">The daemon's socket, for the bridge (<see cref="Paths.Socket"/>).</param>
    /// <param name="directory">Claude Code's working directory, empty and private (<see cref="Paths.AssistantDir"/>).</param>
    /// <param name="environment">The application's environment, filtered by <see cref="Assistant.ChildEnvironment"/>.</param>
    /// <param name="killGrace">From the end of stdin to the kill (<see cref="ClaudeCodeProcess.DefaultKillGrace"/>; tests shorten it).</param>
    /// <param name="time">The clock of the kill, of the resolve timeout and of <see cref="Today"/>; the system's when null.</param>
    /// <param name="logger">Receives kinds and numbers, never mail or model text.</param>
    /// <param name="pending">Counts the background work and the processes; one of its own when null.</param>
    /// <param name="directories">Makes <paramref name="directory"/> private; Directory.CreateDirectory when null.</param>
    /// <param name="processLogger">The logger of the conversations' processes.</param>
    public AssistantPanelController(
        SettingsStore settings,
        ClaudeCodeLocator locator,
        string? bridge,
        string socket,
        string directory,
        IReadOnlyDictionary<string, string> environment,
        TimeSpan? killGrace = null,
        TimeProvider? time = null,
        ILogger<AssistantPanelController>? logger = null,
        PendingWork? pending = null,
        IPrivateDirectoryFactory? directories = null,
        ILogger<ClaudeCodeProcess>? processLogger = null)
    {
        ArgumentNullException.ThrowIfNull(settings);
        ArgumentNullException.ThrowIfNull(locator);
        ArgumentNullException.ThrowIfNull(socket);
        ArgumentException.ThrowIfNullOrEmpty(directory);
        ArgumentNullException.ThrowIfNull(environment);
        Settings = settings;
        Locator = locator;
        this.bridge = bridge;
        this.socket = socket;
        this.directory = directory;
        this.environment = environment;
        this.killGrace = killGrace ?? ClaudeCodeProcess.DefaultKillGrace;
        Time = time ?? TimeProvider.System;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        this.directories = directories;
        this.processLogger = processLogger;
        scope = new ControllerScope(pending);
        itemsView = items.AsReadOnly();
        pinnedView = pinned.AsReadOnly();
        Today = () => LocalDate(Time);
        Language = () => Assistant.LanguageName(L10n.Catalogue.Language);
        settingsToken = settings.OnChange(SettingsKey.AssistantModel, RaiseState);
    }

    /// <summary><see cref="Items"/> changed (Swift <c>onChange</c>).</summary>
    public event EventHandler<Change>? Changed;

    /// <summary>The phase, the context, the waiting action or the model changed (Swift <c>onState</c>).</summary>
    public event EventHandler? StateChanged;

    /// <summary>
    /// The question field should take the keyboard: a message action waits
    /// for the user's words (Swift <c>onFocusInput</c>).
    /// </summary>
    public event EventHandler? FocusInputRequested;

    /// <summary>
    /// A question that was not sent (consent declined): its text goes back
    /// into the field (Swift <c>onRestoreInput</c>).
    /// </summary>
    public event EventHandler<string>? RestoreInputRequested;

    /// <summary>The model, the consent, Claude Code's path.</summary>
    public SettingsStore Settings { get; }

    /// <summary>Finds and asks Claude Code.</summary>
    public ClaudeCodeLocator Locator { get; }

    /// <summary>The clock of the kill, of the resolve timeout and of <see cref="Today"/>.</summary>
    public TimeProvider Time { get; }

    // Hooks

    /// <summary>
    /// Asks the user before the first question ever; true allows. Without a
    /// hook nothing is ever sent; a hook that fails declines.
    /// </summary>
    public Func<Task<bool>>? Consent { get; set; }

    /// <summary>
    /// Completes a partial context (a folded conversation's members, newest
    /// first), on the UI thread; its <c>done</c> should be called once, on the
    /// UI thread, and the context's own selection is used when it is not
    /// called within <see cref="ResolveTimeout"/>.
    /// </summary>
    public Action<Context, Action<AssistantSelection>>? ResolveContext { get; set; }

    /// <summary>Open Draft of a draft card (the application checks the draft is there and opens it).</summary>
    public Action<DraftRef>? OpenDraft { get; set; }

    /// <summary>The date for the system prompt, YYYY-MM-DD: <see cref="LocalDate"/> on <see cref="Time"/> by default.</summary>
    public Func<string> Today { get; set; }

    /// <summary>
    /// The UI language's English name for the system prompt: the
    /// <see cref="Assistant.LanguageName"/> of the catalogue's language by
    /// default ("" is English).
    /// </summary>
    public Func<string> Language { get; set; }

    /// <summary>How long <see cref="ResolveContext"/> is waited for (<see cref="DefaultResolveTimeout"/>).</summary>
    public TimeSpan ResolveTimeout { get; set; } = DefaultResolveTimeout;

    // State

    /// <summary>The transcript, oldest first; a live read-only view.</summary>
    public IReadOnlyList<Item> Items => itemsView;

    /// <summary>Where the question under way is.</summary>
    public Phase CurrentPhase { get; private set; } = Phase.Idle;

    /// <summary>
    /// The list's selection, as the application last set it: the chip's
    /// context before the conversation's first question, compared with the
    /// pinned contexts after it.
    /// </summary>
    public Context? CurrentContext { get; private set; }

    /// <summary>The chip's remove button: all mail until the selection changes.</summary>
    public bool ContextRemoved { get; private set; }

    /// <summary>
    /// What the conversation is about, in the order it was pinned; empty
    /// until its first question. A live read-only view.
    /// </summary>
    public IReadOnlyList<Pinned> PinnedContexts => pinnedView;

    /// <summary>The message action that waits for the user's words; null when none does.</summary>
    public AssistantPanelPending? Pending { get; private set; }

    /// <summary>The conversation's Claude Code, kept between the questions; null without one.</summary>
    public ClaudeCodeProcess? Process { get; private set; }

    /// <summary>Nothing runs any more (the application quits).</summary>
    public bool IsClosed => scope.IsClosed;

    // Reading

    /// <summary>A question is under way.</summary>
    public bool IsRunning => CurrentPhase != Phase.Idle;

    /// <summary>
    /// A question is under way and nothing in the transcript shows it: no
    /// answer streams, no tool and no sign-in is at work. The view shows that
    /// it waits (a spinner below the transcript): from the question until
    /// Claude Code answers, and between a tool's result and what comes next.
    /// It is read from the items, so it holds at every <see cref="Changed"/>
    /// and <see cref="StateChanged"/>. (Swift: <c>waiting</c>; GTK:
    /// <c>Controller.Waiting</c>.)
    /// </summary>
    public bool IsWaiting =>
        CurrentPhase != Phase.Idle
        && !items.Any(it => it.Content is AnswerContent { Streaming: true } or ActivityContent { Done: false });

    /// <summary>The conversation keeps its context: its first question was asked.</summary>
    public bool IsPinned => pinned.Count > 0;

    /// <summary>The chip's context before the conversation's first question: the list's, unless removed.</summary>
    public Context? EffectiveContext => ContextRemoved ? null : CurrentContext;

    /// <summary>
    /// The chip's text: the selection it follows (<see cref="Assistant.ContextLabel"/>)
    /// until the conversation's first question, then what the conversation
    /// is about (<see cref="PinnedLabel"/>).
    /// </summary>
    public string ContextLabel =>
        IsPinned ? PinnedLabel([.. pinned.Select(p => p.Context)]) : Assistant.ContextLabel(EffectiveContext?.Count ?? 0);

    /// <summary>
    /// Whether the bar "Another message is selected" shows: the conversation
    /// keeps its context and the list's selection is part of none of it.
    /// </summary>
    public bool AnotherSelected
    {
        get
        {
            if (!IsPinned || IsClosed || CurrentContext is not { } c)
            {
                return false;
            }
            return !pinned.Any(p => p.Context?.Overlaps(c) == true);
        }
    }

    /// <summary>
    /// Whether the quick actions (Summarize, Draft a Reply…, Tasks and
    /// Deadlines) can run: nothing under way, and something to act on.
    /// </summary>
    public bool CanRunActions => CurrentPhase == Phase.Idle && !IsClosed && QuickTarget is not null;

    /// <summary>
    /// Whether the quick action Summarize Unread in This Folder can run
    /// (Controller.CanSummarizeUnread): <paramref name="folder"/> says
    /// whether the window has a folder it can be summarised for (the
    /// Assistant menu item's condition), and nothing is under way. It needs
    /// no selected message.
    /// </summary>
    public bool CanSummarizeUnread(bool folder) => folder && CurrentPhase == Phase.Idle && !IsClosed;

    /// <summary>The question field's placeholder for the waiting action.</summary>
    public string Placeholder
    {
        get
        {
            var t = Assistant.PanelTexts();
            return Pending switch
            {
                PendingAction { Action: AssistantAction.DraftReply } => t.ReplyPlaceholder,
                PendingAction or PendingAttachment => t.AskPlaceholder,
                _ => t.Placeholder,
            };
        }
    }

    /// <summary>The label over the question field while an action waits; "" when none does.</summary>
    public string PendingLabel => Pending switch
    {
        PendingAction a => Assistant.Label(a.Action),
        PendingAttachment => Assistant.Texts().AskFile,
        _ => "",
    };

    /// <summary>The panel's subtitle: "Claude Code · Sonnet" (two translated names, GTK's and Swift's separator).</summary>
    public string Subtitle => Assistant.TargetName(AssistantTarget.Code) + " · " + Assistant.ModelName(Settings.AssistantModel);

    // What the quick actions act on: the chip's context before the
    // conversation's first question, the newest pinned context after it
    // (none when that is all mail).
    private Target? QuickTarget
    {
        get
        {
            if (!IsPinned)
            {
                return EffectiveContext is { } c ? new ContextTarget(c) : null;
            }
            var p = pinned[^1];
            if (p.Context is not { } pc || pc.Selection.MessageIds.Count == 0)
            {
                return null;
            }
            return new PinnedTarget(p.Key);
        }
    }

    /// <summary>
    /// The chip's text for what a conversation is about (the contexts in
    /// pinned order, null for all mail): one context its subject (without
    /// one, the message or the conversation's count), all mail "All mail",
    /// several the number of their messages, each counted once
    /// (<see cref="Assistant.ConversationLabel"/>).
    /// </summary>
    public static string PinnedLabel(IReadOnlyList<Context?> contexts)
    {
        ArgumentNullException.ThrowIfNull(contexts);
        var mail = contexts.OfType<Context>().Where(c => c.Selection.MessageIds.Count > 0).ToList();
        if (contexts.Count == 1)
        {
            return mail.Count > 0 ? SubjectLabel(mail[0]) : Assistant.ContextLabel(0);
        }
        var n = MessageCount(mail);
        return n switch
        {
            0 => Assistant.ContextLabel(0),
            1 => SubjectLabel(mail[0]),
            _ => Assistant.ConversationLabel("", n),
        };
    }

    /// <summary>
    /// The messages of <paramref name="contexts"/>, each counted once (by
    /// account and id); a folded conversation's members that are not known
    /// yet count as its count says.
    /// </summary>
    public static int MessageCount(IEnumerable<Context> contexts)
    {
        ArgumentNullException.ThrowIfNull(contexts);
        var seen = new HashSet<(string Account, string Id)>();
        var unknown = 0;
        foreach (var c in contexts)
        {
            var ids = c.Selection.MessageIds.Where(id => id.Length > 0).ToList();
            foreach (var id in ids)
            {
                seen.Add((c.Selection.AccountId, id));
            }
            if (c.Partial)
            {
                unknown += Math.Max(0, c.Count - ids.Count);
            }
        }
        return seen.Count + unknown;
    }

    /// <summary>Today on <paramref name="time"/>'s clock in its (the user's) time zone, YYYY-MM-DD, Gregorian.</summary>
    public static string LocalDate(TimeProvider time)
    {
        ArgumentNullException.ThrowIfNull(time);
        return time.GetLocalNow().ToString("yyyy-MM-dd", CultureInfo.InvariantCulture);
    }

    // One context's chip: its subject; without one, the message or the
    // conversation's count.
    private static string SubjectLabel(Context c)
    {
        var hasSubject = Assistant.OneLine(c.Subject, Assistant.MaxSubject).Length > 0;
        return Assistant.ConversationLabel(c.Subject, hasSubject ? 1 : c.Count);
    }

    // The context

    /// <summary>
    /// The list's selection changed: before the conversation's first
    /// question the chip follows it (and a removed context comes back); after
    /// it, the bar "Another message is selected" may come or go.
    /// </summary>
    public void SetContext(Context? c)
    {
        scope.VerifyAccess();
        if (c == CurrentContext && !ContextRemoved)
        {
            return;
        }
        CurrentContext = c;
        ContextRemoved = false;
        if (!IsPinned && c is null && Pending is PendingAction)
        {
            Pending = null;
            pendingTarget = null;
        }
        RaiseState();
    }

    /// <summary>The chip's remove button: all mail until the next selection. Only before the conversation's first question.</summary>
    public void RemoveContext()
    {
        scope.VerifyAccess();
        if (IsPinned || ContextRemoved || CurrentContext is null)
        {
            return;
        }
        ContextRemoved = true;
        if (Pending is PendingAction)
        {
            Pending = null;
            pendingTarget = null;
        }
        RaiseState();
    }

    /// <summary>
    /// The bar's Add to Conversation: the list's selection joins what the
    /// conversation is about; the next free question tells the model
    /// (<see cref="Assistant.AddedContextPreamble"/>).
    /// </summary>
    public void AddSelection()
    {
        scope.VerifyAccess();
        if (IsClosed || !AnotherSelected || CurrentContext is not { } c)
        {
            return;
        }
        Add(c);
        RaiseState();
    }

    // Appends c to what the conversation is about; a folded conversation's
    // members are asked for at once, while the list still shows it selected.
    private int Add(Context c)
    {
        var key = NewKey();
        pinned.Add(new Pinned(c, false, key));
        if (c.Partial)
        {
            _ = ResolvePinned(key);
        }
        return key;
    }

    // The newest pinned context c is part of.
    private int? PinnedKey(Context c) => pinned.FindLast(p => p.Context?.Overlaps(c) == true)?.Key;

    private Context? PinnedContext(int key) => pinned.Find(p => p.Key == key)?.Context;

    private int NewKey() => nextKey++;

    // Questions

    /// <summary>
    /// A quick action (the panel's buttons) on the chip's context, or, once
    /// the conversation keeps its context, on its newest pinned context:
    /// Summarize and Tasks and Deadlines send at once, Draft a Reply… and Ask
    /// About This Message… wait for the user's words. Nothing while a
    /// question is under way or without anything to act on.
    /// </summary>
    public void Run(AssistantAction a)
    {
        scope.VerifyAccess();
        if (IsClosed || CurrentPhase != Phase.Idle || !Assistant.MessageActions.Contains(a) || QuickTarget is not { } target)
        {
            return;
        }
        Perform(a, target);
    }

    /// <summary>
    /// A message action of a menu (the Assistant menu, a message window) on
    /// <paramref name="c"/>, the selection or the window's message. Before the
    /// conversation's first question the chip takes it and the action runs
    /// on it; after it, the action runs on the pinned context it is part of,
    /// and one that is part of none is added first (the action's prompt
    /// names its ids, so the model needs no other word of it).
    /// </summary>
    public void RunOn(AssistantAction a, Context c)
    {
        ArgumentNullException.ThrowIfNull(c);
        scope.VerifyAccess();
        if (IsClosed || CurrentPhase != Phase.Idle || !Assistant.MessageActions.Contains(a))
        {
            return;
        }
        if (!IsPinned)
        {
            SetContext(c);
            Run(a);
            return;
        }
        Perform(a, new PinnedTarget(PinnedKey(c) ?? Add(c)));
    }

    private void Perform(AssistantAction a, Target target)
    {
        switch (a)
        {
            case AssistantAction.Summarize or AssistantAction.Tasks:
                Start(new Request(new ActionRequest(a), Assistant.Label(a), "", EffectiveContext, target));
                break;
            default:
                Pending = new PendingAction(a);
                pendingTarget = IsPinned ? target : null;
                RaiseState();
                Raise(FocusInputRequested);
                break;
        }
    }

    /// <summary>Summarize Unread in This Folder, for a folder: sent at once.</summary>
    public void SummarizeUnread(string accountId, string folderId)
    {
        ArgumentNullException.ThrowIfNull(accountId);
        ArgumentNullException.ThrowIfNull(folderId);
        scope.VerifyAccess();
        if (IsClosed || CurrentPhase != Phase.Idle || accountId.Length == 0 || folderId.Length == 0)
        {
            return;
        }
        Start(new Request(new UnreadRequest(accountId, folderId), Assistant.Label(AssistantAction.Unread), "", EffectiveContext, null));
    }

    /// <summary>
    /// An attachment's question: waits for the user's words. Once the
    /// conversation keeps its context, the attachment's message is added to
    /// it when it is part of none of it (<paramref name="subject"/> and
    /// <paramref name="threadId"/> are the message's).
    /// </summary>
    public void AskAttachment(string accountId, string messageId, string partId, string subject = "", string threadId = "")
    {
        ArgumentNullException.ThrowIfNull(accountId);
        ArgumentNullException.ThrowIfNull(messageId);
        ArgumentNullException.ThrowIfNull(partId);
        ArgumentNullException.ThrowIfNull(subject);
        ArgumentNullException.ThrowIfNull(threadId);
        scope.VerifyAccess();
        if (IsClosed || CurrentPhase != Phase.Idle || accountId.Length == 0 || messageId.Length == 0 || partId.Length == 0)
        {
            return;
        }
        var c = new Context(new AssistantSelection(accountId, [messageId]), Subject: subject, ThreadId: threadId);
        Pending = new PendingAttachment(accountId, messageId, partId);
        pendingTarget = IsPinned ? new PinnedTarget(PinnedKey(c) ?? Add(c)) : new ContextTarget(c);
        RaiseState();
        Raise(FocusInputRequested);
    }

    /// <summary>Drops the waiting action (a context it added stays).</summary>
    public void CancelPending()
    {
        scope.VerifyAccess();
        if (Pending is null)
        {
            return;
        }
        Pending = null;
        pendingTarget = null;
        RaiseState();
    }

    /// <summary>
    /// The question field's Send: with a waiting action its prompt and the
    /// words, otherwise a free question. False when nothing was taken (empty,
    /// or a question under way); the field keeps its text then.
    /// </summary>
    public bool Submit(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        scope.VerifyAccess();
        if (IsClosed || CurrentPhase != Phase.Idle)
        {
            return false;
        }
        var words = TrimSpace(text);
        switch (Pending)
        {
            case PendingAction { Action: var a }:
                // A reply may be drafted without instructions.
                if (words.Length == 0 && a != AssistantAction.DraftReply)
                {
                    return false;
                }
                var target = pendingTarget ?? (EffectiveContext is { } e ? new ContextTarget(e) : null);
                if (target is null)
                {
                    return false;
                }
                Start(new Request(new ActionRequest(a), Assistant.Label(a), words, EffectiveContext, target));
                break;
            case PendingAttachment(var account, var message, var part):
                if (words.Length == 0)
                {
                    return false;
                }
                Start(new Request(new AttachmentRequest(account, message, part), Assistant.Texts().AskFile, words, EffectiveContext, pendingTarget));
                break;
            default:
                if (words.Length == 0)
                {
                    return false;
                }
                Start(new Request(new FreeQuestion(), "", words, EffectiveContext, null));
                break;
        }
        return true;
    }

    /// <summary>Try Again on an error item: the same question once more.</summary>
    public void Retry(int itemId)
    {
        scope.VerifyAccess();
        if (IsClosed || CurrentPhase != Phase.Idle || lastRequest is not { } request)
        {
            return;
        }
        var idx = items.FindIndex(i => i.Id == itemId);
        if (idx < 0 || items[idx].Content is not ErrorContent { Retry: true } error)
        {
            return;
        }
        SetContent(idx, error with { Retry = false });
        Start(request, echo: false);
    }

    /// <summary>
    /// Sign In… on an error item: Claude Code's own sign-in in the browser,
    /// then the same question once more.
    /// </summary>
    public void SignIn(int itemId)
    {
        scope.VerifyAccess();
        if (IsClosed || CurrentPhase != Phase.Idle || lastRequest is not { } request)
        {
            return;
        }
        var idx = items.FindIndex(i => i.Id == itemId);
        if (idx < 0 || items[idx].Content is not ErrorContent { Offer: ErrorOffer.SignIn } error)
        {
            return;
        }
        SetContent(idx, error with { Offer = ErrorOffer.None });
        Start(request with { SignIn = true }, echo: false);
    }

    /// <summary>A draft card's Open Draft.</summary>
    public void OpenDraftItem(int itemId)
    {
        scope.VerifyAccess();
        if (items.Find(i => i.Id == itemId) is not { Content: DraftContent { Draft: var draft } } || OpenDraft is not { } open)
        {
            return;
        }
        scope.Guard(() => open(draft));
    }

    /// <summary>
    /// Stop: ends the process and the turn under way. The conversation keeps
    /// its context; the next question starts a new process, which is told
    /// the context again.
    /// </summary>
    public void Stop()
    {
        scope.VerifyAccess();
        if (CurrentPhase == Phase.Idle)
        {
            return;
        }
        gen++;
        EndSignIn();
        EndProcess();
        CloseTurn();
        CurrentPhase = Phase.Idle;
        Append(new NoteContent(Assistant.PanelTexts().Stopped));
        RaiseState();
    }

    /// <summary>
    /// New Conversation (the header's button and the bar's): ends the
    /// process, empties the transcript and forgets what the conversation was
    /// about; the chip follows the selection again.
    /// </summary>
    public void NewConversation()
    {
        scope.VerifyAccess();
        gen++;
        EndSignIn();
        EndProcess();
        items.Clear();
        streaming = null;
        signingIn = null;
        activities.Clear();
        toolNames.Clear();
        Pending = null;
        pendingTarget = null;
        lastRequest = null;
        pinned.Clear();
        resolving.Clear();
        ContextRemoved = false;
        CurrentPhase = Phase.Idle;
        RaiseChange(ChangeKind.Reset, 0);
        RaiseState();
    }

    /// <summary>
    /// Ends the conversation for good (the application quits): its process
    /// is terminated, and nothing is resolved, started or reported
    /// afterwards. Idempotent.
    /// </summary>
    public void Close()
    {
        scope.VerifyAccess();
        if (IsClosed)
        {
            return;
        }
        gen++;
        EndSignIn();
        EndProcess();
        settingsToken?.Cancel();
        settingsToken = null;
        scope.Close();
    }

    /// <summary>Closes the panel.</summary>
    public void Dispose() => Close();

    // Sending

    private void Start(Request request, bool echo = true)
    {
        if (IsClosed || CurrentPhase != Phase.Idle)
        {
            return;
        }
        var my = ++gen;
        CurrentPhase = Phase.Preparing;
        RaiseState();
        scope.Run(_ => PrepareAsync(request, my, echo));
    }

    private async Task PrepareAsync(Request request, int my, bool echo)
    {
        // GTK's check: a Stop or a new conversation came first.
        if (my != gen)
        {
            return;
        }
        // 1. Consent, once ever.
        if (!Settings.AssistantConsent)
        {
            var allowed = await AskConsentAsync();
            if (my != gen)
            {
                return;
            }
            if (!allowed)
            {
                CurrentPhase = Phase.Idle;
                RaiseState();
                if (request.Text.Length > 0)
                {
                    scope.Raise(RestoreInputRequested, this, request.Text);
                }
                return;
            }
            Settings.AssistantConsent = true;
        }
        // 2. The question in the transcript; the conversation's first
        // question pins what the chip showed, and what the question is about
        // joins it when it is part of none of it; a waiting action is used
        // up.
        if (pinned.Count == 0)
        {
            var key = NewKey();
            pinned.Add(new Pinned(request.InEffect, false, key));
            if (request.InEffect?.Partial == true)
            {
                _ = ResolvePinned(key);
            }
        }
        if (request.Target is ContextTarget { Context: var about })
        {
            request = request with { Target = new PinnedTarget(PinnedKey(about) ?? Add(about)) };
        }
        lastRequest = request with { SignIn = false };
        ClearRetries();
        if (echo)
        {
            Append(new UserContent(request.Label, request.Text));
        }
        Pending = null;
        pendingTarget = null;
        RaiseState();
        // 3. What the model is told: a new Claude Code knows nothing of the
        // conversation yet; the contexts the prompt names have their members
        // first.
        if (Process?.Running != true)
        {
            for (var i = 0; i < pinned.Count; i++)
            {
                pinned[i] = pinned[i] with { Announced = false };
            }
        }
        foreach (var key in Unresolved(request))
        {
            await ResolvePinned(key);
            if (my != gen)
            {
                return;
            }
        }
        string prompt;
        IReadOnlyList<int> told;
        try
        {
            (prompt, told) = Prompt(request);
        }
        catch (AssistantException e)
        {
            LogPromptFailed(logger, e.Kind);
            Fail(Assistant.StoppedText(e.Message), retry: false);
            return;
        }
        // 4. Claude Code, started when the conversation has none.
        if (Process?.Running != true)
        {
            Process = null;
            if (Locator.Locate() is not { } path)
            {
                Fail(Assistant.PanelTexts().NotFound, retry: true, ErrorOffer.Install);
                return;
            }
            if (bridge is not { Length: > 0 } bridgePath)
            {
                Fail(Assistant.PanelTexts().ToolsMissing, retry: false);
                return;
            }
            if (request.SignIn)
            {
                // Sign In…: Claude Code's own sign-in, shown as an activity
                // line; anything but signed in ends the turn with the reason
                // and Sign In… again.
                signingIn = Append(new ActivityContent(Assistant.SignInTexts().Waiting, false));
                var run = Locator.SignInAsync();
                signInRun = run;
                var outcome = await run;
                if (my != gen)
                {
                    return;
                }
                signInRun = null;
                CloseSignIn();
                switch (outcome)
                {
                    case ClaudeCodeSignIn.Done:
                        break;
                    case ClaudeCodeSignIn.Failed failed:
                        Fail(Assistant.SignInFailedText(failed.Reason), retry: false, ErrorOffer.SignIn);
                        return;
                    case ClaudeCodeSignIn.TimedOut:
                        Fail(Assistant.SignInTexts().TimedOut, retry: false, ErrorOffer.SignIn);
                        return;
                    case ClaudeCodeSignIn.NotFound:
                        Fail(Assistant.PanelTexts().NotFound, retry: true, ErrorOffer.Install);
                        return;
                    default: // Cancelled: the settings' sign-in took its place
                        Fail(Assistant.PanelTexts().NotSignedIn, retry: false, ErrorOffer.SignIn);
                        return;
                }
            }
            else
            {
                Locator.Refresh();
                var signedIn = await Locator.SignedInAsync();
                if (my != gen)
                {
                    return;
                }
                if (signedIn == false)
                {
                    Fail(Assistant.PanelTexts().NotSignedIn, retry: false, ErrorOffer.SignIn);
                    return;
                }
            }
            try
            {
                Process = Launch(path, bridgePath);
            }
            catch (ClaudeCodeStartException e)
            {
                LogStartFailed(logger, e.Failure);
                Fail(Assistant.StoppedText(e.Message), retry: true);
                return;
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException or ArgumentException or NotSupportedException or InvalidOperationException or Win32Exception)
            {
                LogDirectoryFailed(logger, e.GetType().Name, e.HResult);
                Fail(Assistant.StoppedText("the assistant's directory: " + e.Message), retry: true);
                return;
            }
        }
        // 5. The turn.
        if (Process is not { } p || !p.Send(Assistant.UserMessage(prompt)))
        {
            Fail(Assistant.StoppedText("claude is not running"), retry: true);
            return;
        }
        for (var i = 0; i < pinned.Count; i++)
        {
            if (told.Contains(pinned[i].Key))
            {
                pinned[i] = pinned[i] with { Announced = true };
            }
        }
        authFailed = false;
        refreshFailed = false;
        CurrentPhase = Phase.Running;
        RaiseState();
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

    // The prompt of a question and the keys of the pinned contexts it tells
    // the model about. A free question carries a line for every context not
    // told yet (Assistant.ContextPreamble for the conversation's first,
    // Assistant.AddedContextPreamble for one added later); a message action
    // names its context's ids, and an attachment's question its message's.
    private (string Prompt, IReadOnlyList<int> Told) Prompt(Request request)
    {
        switch (request.Kind)
        {
            case ActionRequest(var a):
                if (request.Target is not PinnedTarget(var key) || PinnedContext(key) is not { } c)
                {
                    throw new AssistantException(AssistantError.NoMessages, "assistant: no message ids");
                }
                return (Assistant.Prompt(AssistantTarget.App, a, c.Selection) + request.Text, [key]);
            case UnreadRequest(var account, var folder):
                return (Assistant.UnreadPrompt(account, folder), []);
            case AttachmentRequest(var account, var message, var part):
                var prompt = Assistant.AttachmentPrompt(account, message, part) + request.Text;
                if (request.Target is PinnedTarget(var own)
                    && PinnedContext(own)?.Selection == new AssistantSelection(account, [message]))
                {
                    return (prompt, [own]);
                }
                return (prompt, []);
            default:
                var lines = new List<string>();
                var told = new List<int>();
                for (var i = 0; i < pinned.Count; i++)
                {
                    var p = pinned[i];
                    if (p.Announced)
                    {
                        continue;
                    }
                    told.Add(p.Key);
                    if (p.Context is not { } context)
                    {
                        continue;
                    }
                    var line = i == 0 ? Assistant.ContextPreamble(context.Selection) : Assistant.AddedContextPreamble(context.Selection);
                    if (line.Length > 0)
                    {
                        lines.Add(line);
                    }
                }
                var preamble = string.Join("\n", lines);
                return (preamble.Length == 0 ? request.Text : preamble + "\n\n" + request.Text, told);
        }
    }

    // The pinned folded conversations whose members the prompt of request
    // needs: every one a free question tells the model about, a message
    // action's own.
    private List<int> Unresolved(Request request)
    {
        IEnumerable<int> keys;
        switch (request.Kind)
        {
            case FreeQuestion:
                keys = pinned.Where(p => !p.Announced).Select(p => p.Key);
                break;
            case ActionRequest:
                if (request.Target is not PinnedTarget(var key))
                {
                    return [];
                }
                keys = [key];
                break;
            default:
                return [];
        }
        return [.. keys.Where(k => PinnedContext(k)?.Partial == true)];
    }

    // Asks once for the members of a pinned folded conversation (the
    // application answers while the list still shows it selected, and with
    // its own selection otherwise); a question that needs them waits for the
    // same answer.
    private Task ResolvePinned(int key)
    {
        if (resolving.TryGetValue(key, out var running))
        {
            return running;
        }
        var task = scope.Run(async _ =>
        {
            if (IsClosed || PinnedContext(key) is not { Partial: true } c)
            {
                return;
            }
            var selection = await ResolveAsync(c);
            if (IsClosed)
            {
                return;
            }
            resolving.Remove(key);
            Settle(key, selection);
        });
        resolving[key] = task;
        return task;
    }

    // A pinned folded conversation's members arrived.
    private void Settle(int key, AssistantSelection selection)
    {
        var i = pinned.FindIndex(p => p.Key == key);
        if (i < 0 || pinned[i].Context is not { Partial: true } c || selection.MessageIds.Count == 0 || selection == c.Selection)
        {
            return;
        }
        pinned[i] = pinned[i] with { Context = c with { Selection = selection, Count = selection.MessageIds.Count, Partial = false } };
        RaiseState();
    }

    // The context's selection, its members asked for when partial: once,
    // whatever answers first, the hook or the timeout (Swift's Once). The
    // wait is made now, on the clock, so that a fake clock's next step is
    // sure to see it; a Close ends it with the context's own selection.
    private Task<AssistantSelection> ResolveAsync(Context c)
    {
        if (!c.Partial || ResolveContext is not { } hook)
        {
            return Task.FromResult(c.Selection);
        }
        var answer = new TaskCompletionSource<AssistantSelection>(TaskCreationOptions.RunContinuationsAsynchronously);
        var fired = false;
        var timer = CancellationTokenSource.CreateLinkedTokenSource(scope.Lifetime);
        var delay = Task.Delay(ResolveTimeout, Time, timer.Token);
        scope.RunDetached(async _ =>
        {
            try
            {
                await delay;
            }
            catch (OperationCanceledException)
            {
                // Answered, or closed.
            }
            finally
            {
                timer.Dispose();
            }
            if (Fire())
            {
                answer.TrySetResult(c.Selection);
            }
        });
        scope.Guard(() => hook(c, selection =>
        {
            if (Fire())
            {
                answer.TrySetResult(selection.MessageIds.Count == 0 ? c.Selection : selection);
                CancelTimer();
            }
        }));
        return answer.Task;

        bool Fire()
        {
            if (fired)
            {
                return false;
            }
            fired = true;
            return true;
        }

        void CancelTimer()
        {
            try
            {
                timer.Cancel();
            }
            catch (ObjectDisposedException)
            {
                // The wait is over already.
            }
        }
    }

    // Starts Claude Code for a new conversation, in the private directory.
    private ClaudeCodeProcess Launch(string path, string bridgePath)
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
            Bridge = bridgePath,
            Socket = socket,
            Model = Settings.AssistantModel,
            SystemPrompt = Assistant.SystemPrompt(Language(), Today()),
        };
        var p = new ClaudeCodeProcess(
            path, Assistant.Args(options), Assistant.ChildEnvironment(environment, path), directory, killGrace, Time, processLogger);
        p.EventsReceived += (_, events) =>
        {
            if (p == Process)
            {
                Handle(events);
            }
        };
        p.Exited += (_, exit) =>
        {
            if (p == Process)
            {
                Exited(exit);
            }
        };
        p.Start();
        // Counted until its end was reported (after the handler above).
        var ended = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        p.Exited += (_, _) => ended.TrySetResult();
        scope.Pending.Track(ended.Task);
        return p;
    }

    // The stream

    private void Handle(IReadOnlyList<AssistantEvent> events)
    {
        foreach (var e in events)
        {
            if (Process is null)
            {
                return;
            }
            switch (e.Kind)
            {
                case AssistantEventKind.SystemInit:
                    if (!e.BridgeConnected)
                    {
                        LogBridgeNotConnected(logger);
                        EndProcess();
                        Fail(Assistant.PanelTexts().ToolsMissing, retry: false);
                        return;
                    }
                    break;
                case AssistantEventKind.TextDelta:
                    if (streaming is { } open && items[open].Content is AnswerContent streamed)
                    {
                        SetContent(open, new AnswerContent(streamed.Text + e.Text, true));
                    }
                    else if (e.Text.Length > 0)
                    {
                        streaming = Append(new AnswerContent(e.Text, true));
                    }
                    break;
                case AssistantEventKind.Text:
                    if (streaming is { } whole && items[whole].Content is AnswerContent sofar)
                    {
                        items[whole] = items[whole] with { Content = new AnswerContent(e.Text.Length == 0 ? sofar.Text : e.Text, false) };
                        streaming = null;
                        RaiseChange(ChangeKind.Updated, whole);
                    }
                    else if (e.Text.Length > 0)
                    {
                        Append(new AnswerContent(e.Text, false));
                    }
                    break;
                case AssistantEventKind.ToolUse:
                    CloseStreaming();
                    var idx = Append(new ActivityContent(Assistant.ActivityLabel(e.Tool), false));
                    if (e.ToolUseId.Length > 0)
                    {
                        activities[e.ToolUseId] = idx;
                        toolNames[e.ToolUseId] = e.Tool;
                    }
                    break;
                case AssistantEventKind.ToolResult:
                    if (activities.Remove(e.ToolUseId, out var at) && items[at].Content is ActivityContent { Done: false } activity)
                    {
                        SetContent(at, activity with { Done = true });
                    }
                    toolNames.Remove(e.ToolUseId, out var tool);
                    if (tool == "create_draft" && !e.IsError && Assistant.ParseDraftResult(e.ResultText) is { } draft)
                    {
                        Append(new DraftContent(draft));
                    }
                    break;
                case AssistantEventKind.Failure:
                    // Claude Code's own words for a turn the API refused: the
                    // result repeats them.
                    LogRefused(logger, e.NotSignedIn);
                    if (e.NotSignedIn)
                    {
                        authFailed = true;
                    }
                    else if (e.RefreshFailed)
                    {
                        refreshFailed = true;
                    }
                    break;
                case AssistantEventKind.Result:
                    LogTurn(logger, e.Success, e.CostUsd, e.Denied.Count);
                    foreach (var denied in e.Denied)
                    {
                        LogDenied(logger, denied);
                    }
                    CloseTurn();
                    CurrentPhase = Phase.Idle;
                    if (e.Success)
                    {
                        lastRequest = null;
                    }
                    else if (authFailed)
                    {
                        // A new sign-in takes a new Claude Code.
                        EndProcess();
                        Append(new ErrorContent(Assistant.PanelTexts().NotSignedIn, false, ErrorOffer.SignIn));
                    }
                    else if (refreshFailed)
                    {
                        // Its words say what happened and what helps: Try
                        // Again in a minute, or a new sign-in now; either way
                        // a new Claude Code.
                        EndProcess();
                        Append(new ErrorContent(Assistant.StoppedText(e.ResultText), true, ErrorOffer.SignIn));
                    }
                    else
                    {
                        Append(new ErrorContent(Assistant.StoppedText(e.ResultText), true));
                    }
                    authFailed = false;
                    refreshFailed = false;
                    RaiseState();
                    break;
                default:
                    continue;
            }
        }
    }

    // The process ended: during a turn that is an error with its reason;
    // between turns (or while the next question is being prepared) the next
    // question starts a new one.
    private void Exited(ClaudeCodeExit exit)
    {
        Process = null;
        LogEnded(logger, exit.Status);
        if (CurrentPhase != Phase.Running)
        {
            return;
        }
        CloseTurn();
        CurrentPhase = Phase.Idle;
        Append(new ErrorContent(Assistant.StoppedText(exit.Description), true));
        RaiseState();
    }

    // Ends the turn with an error line and its buttons.
    private void Fail(string text, bool retry, ErrorOffer offer = ErrorOffer.None)
    {
        CloseTurn();
        CurrentPhase = Phase.Idle;
        Append(new ErrorContent(text, retry, offer));
        RaiseState();
    }

    // Try Again and the offers belong to the last question only.
    private void ClearRetries()
    {
        for (var idx = 0; idx < items.Count; idx++)
        {
            if (items[idx].Content is ErrorContent error && (error.Retry || error.Offer != ErrorOffer.None))
            {
                SetContent(idx, error with { Retry = false, Offer = ErrorOffer.None });
            }
        }
    }

    // Ends the sign-in the question under way started; its end is not
    // reported.
    private void EndSignIn()
    {
        if (signInRun is { } run)
        {
            signInRun = null;
            Locator.CancelSignIn(run);
        }
    }

    // The sign-in's activity line is over.
    private void CloseSignIn()
    {
        if (signingIn is not { } idx)
        {
            return;
        }
        signingIn = null;
        if (idx < items.Count && items[idx].Content is ActivityContent { Done: false } activity)
        {
            SetContent(idx, activity with { Done = true });
        }
    }

    // Terminates the conversation's process; its end is not reported.
    private void EndProcess()
    {
        var p = Process;
        Process = null;
        p?.Terminate();
    }

    // Nothing streams any more and every activity is over.
    private void CloseTurn()
    {
        CloseStreaming();
        CloseSignIn();
        foreach (var idx in activities.Values.Order())
        {
            if (items[idx].Content is ActivityContent { Done: false } activity)
            {
                SetContent(idx, activity with { Done = true });
            }
        }
        activities.Clear();
        toolNames.Clear();
    }

    private void CloseStreaming()
    {
        if (streaming is not { } idx)
        {
            return;
        }
        streaming = null;
        if (items[idx].Content is AnswerContent { Streaming: true } answer)
        {
            SetContent(idx, answer with { Streaming = false });
        }
    }

    private int Append(AssistantPanelContent content)
    {
        items.Add(new Item(nextId, content));
        nextId++;
        var idx = items.Count - 1;
        RaiseChange(ChangeKind.Appended, idx);
        return idx;
    }

    // The item at idx shows content now.
    private void SetContent(int idx, AssistantPanelContent content)
    {
        items[idx] = items[idx] with { Content = content };
        RaiseChange(ChangeKind.Updated, idx);
    }

    // strings.TrimSpace of the typed words: every White_Space character is
    // in the BMP and no surrogate, so a char is one.
    private static string TrimSpace(string s)
    {
        var lo = 0;
        var hi = s.Length;
        while (lo < hi && Assistant.IsSpace(s[lo]))
        {
            lo++;
        }
        while (hi > lo && Assistant.IsSpace(s[hi - 1]))
        {
            hi--;
        }
        return s[lo..hi];
    }

    // Outputs, each handler guarded (docs/windows-port.md §7.5).

    private void RaiseChange(ChangeKind kind, int index) => scope.Raise(Changed, this, new Change(kind, index));

    private void RaiseState() => Raise(StateChanged);

    private void Raise(EventHandler? handlers)
    {
        if (handlers is null)
        {
            return;
        }
        foreach (var handler in handlers.GetInvocationList())
        {
            scope.Guard(() => ((EventHandler)handler).Invoke(this, EventArgs.Empty));
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "assistant prompt: {Kind}")]
    private static partial void LogPromptFailed(ILogger logger, AssistantError kind);

    [LoggerMessage(Level = LogLevel.Warning, Message = "assistant: claude could not be started: {Failure}")]
    private static partial void LogStartFailed(ILogger logger, ClaudeCodeStartFailure failure);

    [LoggerMessage(Level = LogLevel.Warning, Message = "assistant: the directory could not be made: {Kind} 0x{HResult:X8}")]
    private static partial void LogDirectoryFailed(ILogger logger, string kind, int hResult);

    [LoggerMessage(Level = LogLevel.Warning, Message = "assistant: the malachi MCP server is not connected")]
    private static partial void LogBridgeNotConnected(ILogger logger);

    [LoggerMessage(Level = LogLevel.Information, Message = "assistant turn: success {Success}, cost {CostUsd} USD, {Denied} denied")]
    private static partial void LogTurn(ILogger logger, bool success, double costUsd, int denied);

    [LoggerMessage(Level = LogLevel.Information, Message = "assistant: the API refused the turn (sign-in: {SignIn})")]
    private static partial void LogRefused(ILogger logger, bool signIn);

    [LoggerMessage(Level = LogLevel.Information, Message = "assistant: denied {Tool}")]
    private static partial void LogDenied(ILogger logger, string tool);

    [LoggerMessage(Level = LogLevel.Information, Message = "assistant: claude ended with status {Status}")]
    private static partial void LogEnded(ILogger logger, int status);
}
