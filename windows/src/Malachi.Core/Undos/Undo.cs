// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/undo/undo.go (Limit, Kind, Moved, Entry, Record,
// FromBoard, Calls, Reselect, Stack); the texts are UndoText.cs (text.go).
// No Swift port exists yet: Go is the reference.
//
// What Undo (Ctrl+Z, and the button of the toast after a move) takes back:
// the moves of mail the user made in this session — Move to Trash,
// Archive, Junk, Move To, and the board's Archive and Delete — newest
// first. The daemon has no undo of its own: a moved message keeps its id
// (docs/api.md, message.move), so taking a move back is message.move of
// each message to the folder it came from (Calls).
//
// What cannot be taken back never gets an entry: a message expunged from
// Trash, a cancelled send of the outbox, a move to a folder the daemon does
// not keep (Gmail's All Mail drops the row). The history lives in memory
// only and ends with the application.
//
// Windows: the namespace is Undos, as Boards and Selections are (a
// namespace Undo would hide the class); Go's Stack is History (CA1711
// reserves the suffix), its ok results are nulls. ReselectKeys is the key
// choice of GTK's window/undo.go takeUndoReselect, pure here so that the
// list controller and the tests share it.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Undos;

/// <summary>ui/internal/undo: the moves of mail Undo takes back.</summary>
public static partial class Undo
{
    /// <summary>undo.Limit: how many moves the history keeps; a push past it forgets the oldest.</summary>
    public const int Limit = 20;

    /// <summary>undo.Kind: what the move was, for its texts.</summary>
    public enum Kind
    {
        /// <summary>Move to Trash (message.delete of messages not in Trash).</summary>
        Trash,

        /// <summary>Archive (message.move to the archive role).</summary>
        Archive,

        /// <summary>Junk (message.move to the junk role).</summary>
        Junk,

        /// <summary>Move To (message.move to a folder the user picked).</summary>
        Move,

        /// <summary>The board's Archive (board.archive moved).</summary>
        BoardArchive,

        /// <summary>The board's Delete (board.trash moved).</summary>
        BoardTrash,
    }

    /// <summary>
    /// Kind.Board: whether the move was the board's; taking it back also
    /// puts the case back on the board (board.setDone done false).
    /// </summary>
    public static bool IsBoard(this Kind kind) => kind is Kind.BoardArchive or Kind.BoardTrash;

    /// <summary>
    /// undo.Moved: one message moved and the folder it came from, with its
    /// conversation (null when not known: the board's moves, an older
    /// daemon) for a list grouped by conversation to select again.
    /// </summary>
    /// <param name="MessageId">The message.</param>
    /// <param name="FromFolderId">The folder it came from.</param>
    /// <param name="ThreadId">Its conversation, if known.</param>
    public readonly record struct Moved(MessageId MessageId, FolderId FromFolderId, ThreadId? ThreadId = null);

    /// <summary>undo.Entry: one move the user made, all of one account.</summary>
    public sealed record Entry
    {
        /// <summary>
        /// The entry's number in the history, given by
        /// <see cref="History.Push"/>; the toast's button takes back its own
        /// entry by it (<see cref="History.Take"/>).
        /// </summary>
        public int Id { get; init; }

        /// <summary>What the move was.</summary>
        public Kind Kind { get; init; }

        /// <summary>The account of every message.</summary>
        public AccountId Account { get; init; }

        /// <summary>
        /// The folder the messages went to; empty for the board's moves when
        /// the daemon did not say.
        /// </summary>
        public FolderId Target { get; init; }

        /// <summary>The folder's name for the text of Move To.</summary>
        public string TargetName { get; init => field = value ?? ""; } = "";

        /// <summary>The messages and where they came from, in their order.</summary>
        public IReadOnlyList<Moved> Moves { get; init => field = value ?? []; } = [];

        /// <summary>The board's case of a board move (<see cref="IsBoard"/>).</summary>
        public BoardCaseId Case { get; init; }

        /// <summary>How many messages the entry moved.</summary>
        public int Count => Moves.Count;

        /// <summary>Whether both are the same entry, the moves compared in order.</summary>
        public bool Equals(Entry? other) =>
            other is not null && Id == other.Id && Kind == other.Kind && Account == other.Account && Target == other.Target
            && TargetName == other.TargetName && Moves.SequenceEqual(other.Moves) && Case == other.Case;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Id, Kind, Account, Target, Moves.Count);
    }

    /// <summary>
    /// undo.Record: the entry of moving <paramref name="list"/> (summaries
    /// taken before their rows went) to <paramref name="target"/> with
    /// <paramref name="kind"/>: the account is the first message's;
    /// messages of another account, without an id or a folder, or already
    /// in the target are left out (they did not move). Null when nothing
    /// moved.
    /// </summary>
    public static Entry? Record(Kind kind, FolderId target, IReadOnlyList<MessageSummary> list)
    {
        ArgumentNullException.ThrowIfNull(list);
        if (list.Count == 0)
        {
            return null;
        }
        var account = list[0].AccountId;
        var moves = new List<Moved>();
        foreach (var s in list)
        {
            if (s.AccountId != account || IsEmpty(s.Id.Value) || IsEmpty(s.FolderId.Value) || s.FolderId == target)
            {
                continue;
            }
            if (moves.Any(m => m.MessageId == s.Id))
            {
                continue;
            }
            moves.Add(new Moved(s.Id, s.FolderId, s.ThreadId));
        }
        return moves.Count == 0 ? null : new Entry { Kind = kind, Account = account, Target = target, Moves = moves };
    }

    /// <summary>
    /// undo.FromBoard: the entry of the board's Archive
    /// (<paramref name="trash"/> false) or Delete of case
    /// <paramref name="id"/> in <paramref name="account"/>, from what the
    /// daemon moved (board.archive and board.trash moved). Null when it
    /// moved nothing: the board then offers no Undo either.
    /// </summary>
    public static Entry? FromBoard(bool trash, AccountId account, BoardCaseId id, IReadOnlyList<BoardMoved>? moved)
    {
        var moves = new List<Moved>();
        foreach (var m in moved ?? [])
        {
            if (IsEmpty(m.MessageId.Value) || IsEmpty(m.FromFolderId.Value))
            {
                continue;
            }
            moves.Add(new Moved(m.MessageId, m.FromFolderId));
        }
        return moves.Count == 0
            ? null
            : new Entry { Kind = trash ? Kind.BoardTrash : Kind.BoardArchive, Account = account, Case = id, Moves = moves };
    }

    /// <summary>
    /// undo.Calls: the message.move calls that take <paramref name="e"/>
    /// back: one per folder the messages came from, in the order the
    /// folders first appear, the messages in their order, each once.
    /// </summary>
    public static IReadOnlyList<MessageMoveParams> Calls(Entry e)
    {
        ArgumentNullException.ThrowIfNull(e);
        var folders = new List<FolderId>();
        var messages = new List<List<MessageId>>();
        foreach (var m in e.Moves)
        {
            if (IsEmpty(m.MessageId.Value) || IsEmpty(m.FromFolderId.Value))
            {
                continue;
            }
            var i = folders.IndexOf(m.FromFolderId);
            if (i < 0)
            {
                i = folders.Count;
                folders.Add(m.FromFolderId);
                messages.Add([]);
            }
            if (!messages[i].Contains(m.MessageId))
            {
                messages[i].Add(m.MessageId);
            }
        }
        var calls = new List<MessageMoveParams>(folders.Count);
        for (var i = 0; i < folders.Count; i++)
        {
            calls.Add(new MessageMoveParams { AccountId = e.Account, MessageIds = messages[i], TargetFolderId = folders[i] });
        }
        return calls;
    }

    /// <summary>
    /// undo.Reselect: the moves of <paramref name="e"/> that came back to
    /// <paramref name="folder"/> of <paramref name="account"/>, the folder
    /// the window shows: after Undo the window selects their rows again (a
    /// message's, or its conversation's). None when the window shows
    /// another folder.
    /// </summary>
    public static IReadOnlyList<Moved> Reselect(Entry e, AccountId account, FolderId folder)
    {
        ArgumentNullException.ThrowIfNull(e);
        if (account != e.Account || IsEmpty(folder.Value))
        {
            return [];
        }
        var back = new List<Moved>();
        foreach (var m in e.Moves)
        {
            if (m.FromFolderId == folder && !back.Any(b => b.MessageId == m.MessageId))
            {
                back.Add(m);
            }
        }
        return back;
    }

    /// <summary>
    /// GTK window/undo.go <c>takeUndoReselect</c>'s choice: the rows that
    /// stand for the messages Undo brought back, one per message, the first
    /// of its message row, its member row in a conversation and its
    /// conversation row that the list shows (<paramref name="listed"/>),
    /// each once, in the order of <paramref name="back"/>.
    /// </summary>
    public static IReadOnlyList<ListKey> ReselectKeys(IReadOnlyList<Moved> back, Func<ListKey, bool> listed)
    {
        ArgumentNullException.ThrowIfNull(back);
        ArgumentNullException.ThrowIfNull(listed);
        var keys = new List<ListKey>();
        foreach (var m in back)
        {
            var thread = m.ThreadId is { } t && !IsEmpty(t.Value) ? t : (ThreadId?)null;
            var message = IsEmpty(m.MessageId.Value) ? (MessageId?)null : m.MessageId;
            ListKey[] candidates =
            [
                new(Message: message),
                new(thread, message),
                new(thread),
            ];
            foreach (var k in candidates)
            {
                if (k.Thread is null && k.Message is null)
                {
                    continue;
                }
                if (listed(k) && !keys.Contains(k))
                {
                    keys.Add(k);
                    break;
                }
            }
        }
        return keys;
    }

    private static bool IsEmpty(string? value) => string.IsNullOrEmpty(value);

    /// <summary>
    /// undo.Stack: the moves of the session, newest last. UI-thread-affine,
    /// as the window that holds it.
    /// </summary>
    public sealed class History
    {
        private readonly List<Entry> entries = [];
        private int next;

        /// <summary>How many moves the history holds (Go <c>Len</c>).</summary>
        public int Count => entries.Count;

        /// <summary>
        /// Adds <paramref name="e"/> as the newest move and returns it with
        /// its <see cref="Entry.Id"/>; past <see cref="Limit"/> the oldest is
        /// forgotten.
        /// </summary>
        public Entry Push(Entry e)
        {
            ArgumentNullException.ThrowIfNull(e);
            next++;
            var pushed = e with { Id = next };
            entries.Add(pushed);
            var over = entries.Count - Limit;
            if (over > 0)
            {
                entries.RemoveRange(0, over);
            }
            return pushed;
        }

        /// <summary>Takes the newest move off the history (Ctrl+Z); null when empty.</summary>
        public Entry? Pop()
        {
            if (entries.Count == 0)
            {
                return null;
            }
            var e = entries[^1];
            entries.RemoveAt(entries.Count - 1);
            return e;
        }

        /// <summary>The newest move, left in the history; null when empty.</summary>
        public Entry? Peek() => entries.Count == 0 ? null : entries[^1];

        /// <summary>
        /// Takes the move with <paramref name="id"/> off the history wherever
        /// it is (the button of its toast); null when it is gone (taken back
        /// already, forgotten).
        /// </summary>
        public Entry? Take(int id)
        {
            var i = entries.FindIndex(e => e.Id == id);
            if (i < 0)
            {
                return null;
            }
            var e = entries[i];
            entries.RemoveAt(i);
            return e;
        }

        /// <summary>Drops the moves of <paramref name="account"/> (removed or paused: its messages are gone from the store).</summary>
        public void Forget(AccountId account) => entries.RemoveAll(e => e.Account == account);

        /// <summary>
        /// Drops the moves of <paramref name="account"/> into
        /// <paramref name="folder"/> (Empty Trash and Empty Junk deleted the
        /// messages for good).
        /// </summary>
        public void ForgetFolder(AccountId account, FolderId folder) =>
            entries.RemoveAll(e => e.Account == account && e.Target == folder);
    }
}
