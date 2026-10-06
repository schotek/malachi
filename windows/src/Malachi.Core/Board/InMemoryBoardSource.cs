// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardSource.swift
// (InMemoryBoardSource); GTK: ui/internal/board/source.go (InMemorySource,
// NewDummySource, remindCase).
//
// A source over a snapshot held in memory: the dummy board
// (MALACHI_BOARD_SAMPLES=1), and the tests. Writes are emulated locally
// (Archive marks the case done and says it moved its messages when the case
// can archive). A write to an unknown case, or one that changes nothing, is
// ignored and calls no one. A change makes new lists, so snapshots handed
// out stay as they were.

using System;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Board;

/// <summary>A source over a snapshot held in memory: the dummy board, and the tests.</summary>
public sealed class InMemoryBoardSource(Board.Snapshot snapshot) : IBoardSource
{
    /// <inheritdoc/>
    public Board.Snapshot Snapshot { get; private set; } = snapshot ?? throw new ArgumentNullException(nameof(snapshot));

    /// <inheritdoc/>
    public Action? OnChange { get; set; }

    /// <inheritdoc/>
    public Action<string>? OnError { get; set; }

    /// <inheritdoc/>
    public Action<string>? OnNotice { get; set; }

    /// <summary>
    /// The dummy board: the invented sample cases
    /// (<see cref="Board.SampleSnapshot"/>) dated relative to
    /// <paramref name="now"/>, or none.
    /// </summary>
    public static InMemoryBoardSource Dummy(bool samples, DateTimeOffset now, TimeZoneInfo? timeZone = null) =>
        new(samples ? Board.SampleSnapshot(now, timeZone) : Board.Snapshot.Empty);

    /// <summary>Replaces the whole snapshot, as a source does when its data arrives; the same snapshot again calls no one.</summary>
    public void Replace(Board.Snapshot next)
    {
        ArgumentNullException.ThrowIfNull(next);
        if (next == Snapshot)
        {
            return;
        }
        Snapshot = next;
        OnChange?.Invoke();
    }

    /// <inheritdoc/>
    public void SetState(Board.State? state, BoardCaseId id) => Update(id, c => c with { UserState = state });

    /// <inheritdoc/>
    public void SetDone(bool done, BoardCaseId id) => Update(id, c => c.WithDone(done));

    /// <inheritdoc/>
    public void Remind(DateTimeOffset? until, BoardCaseId id) => Update(id, c =>
    {
        if (until is { } u)
        {
            return c with { Visibility = Board.Visibility.Snoozed(u) };
        }
        return c.Visibility.RemindAt is not null ? c with { Visibility = Board.Visibility.Live } : c;
    });

    /// <inheritdoc/>
    public void Archive(BoardCaseId id)
    {
        if (Snapshot.FindCase(id) is not { } c)
        {
            return;
        }
        var moved = c.CanArchive ? int.Max(1, c.MessageCount) : 0;
        Update(id, x => x with { Visibility = Board.Visibility.Done(), CanArchive = false });
        OnNotice?.Invoke(Board.Text.Archived(moved, !c.CanArchive));
    }

    /// <inheritdoc/>
    public void SetCommitmentDone(bool done, BoardCommitmentId id)
    {
        var ks = Snapshot.Commitments.ToList();
        var i = ks.FindIndex(k => k.Id == id);
        if (i < 0)
        {
            return;
        }
        var state = done ? Board.CommitmentState.Done : Board.CommitmentState.Open;
        if (ks[i].State == state)
        {
            return;
        }
        ks[i] = ks[i] with { State = state };
        Snapshot = Snapshot with { Commitments = ks };
        OnChange?.Invoke();
    }

    /// <inheritdoc/>
    public void DiscardDraft(BoardCaseId id) => Update(id, c => c with { Draft = null });

    /// <summary>The samples have no message to unflag: a placeholder toast.</summary>
    public void Unflag(BoardCaseId id)
    {
        if (Snapshot.FindCase(id) is null)
        {
            return;
        }
        OnNotice?.Invoke(Board.Text.Later);
    }

    /// <summary>The samples carry their messages; a case without any gets none.</summary>
    public void LoadMessages(BoardCaseId id) => Update(id, c => c.Messages is null ? c with { Messages = [] } : c);

    /// <summary>There is nothing to ask.</summary>
    public void Refresh()
    {
    }

    // Changes case id with change, on a copy of the cases, and reports it
    // when it changed.
    private void Update(BoardCaseId id, Func<Board.Case, Board.Case> change)
    {
        var cases = Snapshot.Cases.ToList();
        var i = cases.FindIndex(c => c.Id == id);
        if (i < 0)
        {
            return;
        }
        var c = change(cases[i]);
        if (c == cases[i])
        {
            return;
        }
        cases[i] = c;
        Snapshot = Snapshot with { Cases = cases };
        OnChange?.Invoke();
    }
}
