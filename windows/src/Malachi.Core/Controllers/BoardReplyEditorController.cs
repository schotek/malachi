// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/BoardReplyEditorController.swift
// (BoardReplyEditorController: Key, Failure, Phase, phase, observe, show,
// retry, ended, reset, start); GTK: ui/internal/boardreply/editor.go (Key,
// EditorFailure, EditorPhase, Editor, Show, Retry, Ended).
//
// What the board's detail shows in place of the suggested reply: nothing
// (the Suggest Reply control), the draft loading, the draft ready to be
// edited inline, or why it could not be opened. The suggested reply is a
// local draft the case links (Board.Case.Draft); the editor opens it with
// draft.get and keeps it while the case is selected. Every autosave bumps
// the case's version and the board lists the case again, so the editor is
// keyed by case, account and draft, never by the case's version: a refresh
// of the same case never reloads it.
//
// The ready phase holds the draft as a compose form opens it, Swift's
// ComposeParams (Prefill.FromDraft with ComposeKind.Edit); Go keeps the
// api.Draft because its compose package is outside. Windows: draft.get goes
// through the controller infrastructure (§7.2); Swift's Task cancel is the
// generation, as Go's. Dispose (no answer reported afterwards) is Windows'
// own.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Compose;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>The inline reply editor's state for the selected case.</summary>
public sealed partial class BoardReplyEditorController : IDisposable
{
    private readonly RpcClient client;
    private readonly ILogger logger;
    private readonly ControllerScope scope;
    private readonly BoardObservers observers = new();

    // Drafts the inline pane sent or discarded: hidden until the board no
    // longer links them to their case.
    private readonly HashSet<Key> ended = [];

    // Bumped by every load and every change of what is shown, so an answer
    // that arrives later than another request is dropped.
    private int generation;

    /// <summary>An editor that loads drafts through <paramref name="client"/>.</summary>
    /// <param name="client">The daemon (<c>draft.get</c>).</param>
    /// <param name="logger">Receives the failures' codes.</param>
    /// <param name="pending">Counts the background work; one of its own when null.</param>
    public BoardReplyEditorController(RpcClient client, ILogger<BoardReplyEditorController>? logger = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        this.client = client;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>Why the draft could not be opened.</summary>
    public enum Failure
    {
        /// <summary><c>draft.get</c> said draftNotFound: the board drops the link soon.</summary>
        Gone,

        /// <summary>Anything else: Try Again (<see cref="Retry"/>).</summary>
        Backend,
    }

    /// <summary>What the editor shows now.</summary>
    public Phase Current { get; private set; } = new Phase.None();

    /// <summary>Calls <paramref name="f"/> after <see cref="Current"/> changed; the token removes it.</summary>
    public BoardObserverToken Observe(Action f) => observers.Add(f);

    /// <summary>
    /// The selected case (null: none). A case with a suggested reply loads it
    /// with <c>draft.get</c> unless the editor has it already: the same key
    /// changes nothing, whatever else changed in the case (an autosave bumps
    /// its version, never the editor). A draft <see cref="Ended"/> stays
    /// hidden until the case no longer links it. The host does not call this
    /// for the invented samples, which have no draft behind them.
    /// </summary>
    public void Show(Board.Case? c)
    {
        scope.VerifyAccess();
        if (c is null)
        {
            Reset();
            return;
        }
        // The board dropped (or replaced) a link the pane ended: forget it.
        ended.RemoveWhere(k => k.CaseId == c.Id && (c.Draft is null || k.Draft != c.Draft.Id));
        if (c.Draft is not { } draft)
        {
            Reset();
            return;
        }
        var key = new Key(c.Id, c.Account, draft.Id);
        if (ended.Contains(key))
        {
            Reset();
            return;
        }
        if (Current.Key == key)
        {
            return;
        }
        Start(key);
    }

    /// <summary>Loads the draft again after a failure (a draft that is gone is tried again too).</summary>
    public void Retry()
    {
        scope.VerifyAccess();
        if (Current is Phase.Failed f)
        {
            Start(f.Of);
        }
    }

    /// <summary>
    /// The pane sent or the user discarded <paramref name="key"/>: the editor
    /// hides it until the board drops the link (<see cref="Show"/> with the
    /// case without it).
    /// </summary>
    public void Ended(Key key)
    {
        scope.VerifyAccess();
        ended.Add(key);
        if (Current.Key == key)
        {
            Reset();
        }
    }

    /// <summary>Stops loading; no answer is reported any more.</summary>
    public void Dispose()
    {
        generation++;
        scope.Close();
    }

    private void Reset()
    {
        generation++;
        SetPhase(new Phase.None());
    }

    private void Start(Key key)
    {
        var mine = ++generation;
        SetPhase(new Phase.Loading(key));
        scope.Perform(client, API.DraftGet, new DraftGetParams { AccountId = key.Account, DraftId = key.Draft }, o =>
        {
            if (mine != generation)
            {
                return;
            }
            if (o.TryGetValue(out var r, out var error))
            {
                SetPhase(new Phase.Ready(key, Prefill.FromDraft(ComposeKind.Edit, r.Draft, new BlockedContent())));
                return;
            }
            var code = (error as RpcException)?.Error.Code;
            if (logger.IsEnabled(LogLevel.Information))
            {
                var kind = code?.ToString() ?? error?.GetType().Name ?? "";
                LogGetFailed(logger, kind);
            }
            SetPhase(new Phase.Failed(key, code == ErrorCode.DraftNotFound ? Failure.Gone : Failure.Backend));
        });
    }

    private void SetPhase(Phase p)
    {
        if (p == Current)
        {
            return;
        }
        Current = p;
        scope.Guard(observers.Notify);
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "board reply: draft.get: {Kind}")]
    private static partial void LogGetFailed(ILogger logger, string kind);

    /// <summary>What the editor edits: the draft of the case in its account.</summary>
    /// <param name="CaseId">The case.</param>
    /// <param name="Account">The case's account.</param>
    /// <param name="Draft">The draft the case links.</param>
    public readonly record struct Key(BoardCaseId CaseId, AccountId Account, DraftId Draft);

    /// <summary>The editor's phase.</summary>
    public abstract record Phase
    {
        // Only the cases below derive from it.
        private Phase()
        {
        }

        /// <summary>The key of every phase but <see cref="None"/>.</summary>
        public virtual Key? Key => null;

        /// <summary>No suggested reply (or none to show): the Suggest control shows.</summary>
        public sealed record None : Phase;

        /// <summary>The draft is loading.</summary>
        /// <param name="Of">The key.</param>
        public sealed record Loading(Key Of) : Phase
        {
            /// <inheritdoc/>
            public override Key? Key => Of;
        }

        /// <summary>
        /// The draft as <c>draft.get</c> returned it, as a compose form opens
        /// it (<see cref="Prefill.FromDraft"/> with <see cref="ComposeKind.Edit"/>,
        /// its id and version included).
        /// </summary>
        /// <param name="Of">The key.</param>
        /// <param name="Params">The draft as a compose form opens it.</param>
        public sealed record Ready(Key Of, ComposeParams Params) : Phase
        {
            /// <inheritdoc/>
            public override Key? Key => Of;
        }

        /// <summary>The draft could not be opened; <see cref="Failure.Backend"/> offers Try Again.</summary>
        /// <param name="Of">The key.</param>
        /// <param name="Why">Why.</param>
        public sealed record Failed(Key Of, Failure Why) : Phase
        {
            /// <inheritdoc/>
            public override Key? Key => Of;
        }
    }
}
