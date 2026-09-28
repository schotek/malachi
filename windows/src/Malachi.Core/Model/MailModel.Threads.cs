// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/MailModel+Threads.swift (the
// MailModel extension and the free functions summaryThread,
// membersFromListing, sameShape, insertByDate, frontParticipants,
// unionFlags, applyFlagChange; ListKey, ListRow, ThreadMembers,
// ThreadSnapshot, Removal and RowThread have files of their own); GTK:
// ui/internal/window/thread_model.go.
//
// The grouped message list: one row per conversation of the selected folder
// (thread.list), expandable to the messages the folder holds (thread.get).
// The daemon computes the threads and their aggregates; what happens here is
// bookkeeping between two loads: a notified arrival, an optimistic flag
// change, a removal and its undo. Flat mode (the switch off) keeps Messages
// as it always was; nothing below is consulted then except the
// mode-dispatching accessors.
//
// Where Swift changes a copy of a struct and writes it back (var t =
// threads[i]; …; threads[i] = t), this builds the changed record with
// `with`; member lists are never changed in place but replaced, so the
// snapshot a removal keeps is what Swift's value copy is.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

public sealed partial class MailModel
{
    private readonly List<ThreadSummary> threads = [];
    private readonly Dictionary<ThreadId, int> tindex = new();
    private readonly Dictionary<MessageId, ThreadId> memberOf = new();
    private readonly HashSet<ThreadId> expanded = [];
    private List<ListRow> rows = [];
    private Dictionary<ListKey, int> rowIdx = new();

    /// <summary>
    /// Grouped mode: <see cref="Threads"/> are the conversations of
    /// <see cref="ListFolder"/>, <see cref="Members"/> what is known of their
    /// members, <see cref="Rows"/> the list view. <see cref="Messages"/>
    /// stays empty then, and the other way round.
    /// </summary>
    public bool Grouped { get; set; }

    /// <summary>The conversations listed, a view of the model's own state.</summary>
    public IReadOnlyList<ThreadSummary> Threads => threads;

    /// <summary>The position of each conversation in <see cref="Threads"/>.</summary>
    public IReadOnlyDictionary<ThreadId, int> TIndex => tindex;

    /// <summary>
    /// What is known of each listed conversation's members. The controller
    /// replaces an entry to mark it fetching (<c>with { Fetching = true }</c>);
    /// the member lists are never changed in place.
    /// </summary>
    public Dictionary<ThreadId, ThreadMembers> Members { get; } = new();

    /// <summary>The conversation of each known member.</summary>
    public IReadOnlyDictionary<MessageId, ThreadId> MemberOf => memberOf;

    /// <summary>The unfolded conversations.</summary>
    public IReadOnlySet<ThreadId> Expanded => expanded;

    /// <summary>The rows of the grouped list; replaced whole, never changed in place.</summary>
    public IReadOnlyList<ListRow> Rows => rows;

    /// <summary>The position of each row in <see cref="Rows"/>.</summary>
    public IReadOnlyDictionary<ListKey, int> RowIdx => rowIdx;

    /// <summary>How many rows the list has in either mode.</summary>
    public int RowCount => Grouped ? rows.Count : messages.Count;

    /// <summary>
    /// Projects a conversation onto what its row displays (thread_model.go
    /// <c>summaryThread</c>).
    /// </summary>
    public static RowThread SummaryThread(ThreadSummary t, bool expanded, bool loading)
    {
        ArgumentNullException.ThrowIfNull(t);
        return new RowThread
        {
            Participants = t.Participants,
            Subject = t.Subject,
            Snippet = t.Snippet,
            Date = t.LatestDate,
            Count = t.MessageCount,
            Unread = t.UnreadCount,
            Flagged = FolderTree.HasFlag(t.Flags, Flag.Flagged),
            HasAttachments = t.HasAttachments,
            Expanded = expanded,
            Loading = loading,
        };
    }

    /// <summary>
    /// What thread.list tells of a conversation's folder members: the newest
    /// one, which for a single-message conversation is all of them
    /// (thread_model.go <c>membersFromListing</c>).
    /// </summary>
    public static ThreadMembers MembersFromListing(ThreadSummary t)
    {
        ArgumentNullException.ThrowIfNull(t);
        return new ThreadMembers([t.Latest], Complete: t.MessageCount <= 1);
    }

    /// <summary>
    /// Whether a conversation's listing has not changed in what would
    /// invalidate its fetched members (thread_model.go <c>sameShape</c>).
    /// </summary>
    public static bool SameShape(ThreadSummary a, ThreadSummary b)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(b);
        return a.MessageCount == b.MessageCount && a.UnreadCount == b.UnreadCount
            && a.LatestDate == b.LatestDate && a.Latest.Id == b.Latest.Id;
    }

    /// <summary>
    /// A new list with <paramref name="s"/> placed among
    /// <paramref name="list"/> (oldest first) by date (thread_model.go
    /// <c>insertByDate</c>).
    /// </summary>
    public static IReadOnlyList<MessageSummary> InsertByDate(IReadOnlyList<MessageSummary> list, MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(list);
        ArgumentNullException.ThrowIfNull(s);
        var output = new List<MessageSummary>(list);
        var at = output.FindIndex(x => s.Date < x.Date);
        output.Insert(at < 0 ? output.Count : at, s);
        return output;
    }

    /// <summary>
    /// Moves the senders of the newest message to the front of a participant
    /// list, each address once, compared case-insensitively, capped at
    /// <see cref="API.Limits.MaxThreadParticipants"/> (thread_model.go
    /// <c>frontParticipants</c>).
    /// </summary>
    public static IReadOnlyList<Address> FrontParticipants(IReadOnlyList<Address> list, IReadOnlyList<Address> from)
    {
        ArgumentNullException.ThrowIfNull(list);
        ArgumentNullException.ThrowIfNull(from);
        var output = new List<Address>();
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var a in from.Concat(list))
        {
            var key = CodePoints.ToLower((a.Email ?? "").Trim());
            if (key.Length == 0)
            {
                key = "name:" + CodePoints.ToLower((a.Name ?? "").Trim());
            }
            if (key == "name:" || !seen.Add(key))
            {
                continue;
            }
            output.Add(a);
            if (output.Count == API.Limits.MaxThreadParticipants)
            {
                break;
            }
        }
        return output;
    }

    /// <summary>a ∪ b in a's order, then b's (thread_model.go <c>unionFlags</c>).</summary>
    public static IReadOnlyList<Flag> UnionFlags(IReadOnlyList<Flag> a, IReadOnlyList<Flag> b)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(b);
        var output = new List<Flag>(a);
        foreach (var f in b)
        {
            if (!FolderTree.HasFlag(output, f))
            {
                output.Add(f);
            }
        }
        return output;
    }

    /// <summary>
    /// <paramref name="flags"/> with <paramref name="set"/> added and
    /// <paramref name="clear"/> removed (a flag in both ends up set) and
    /// whether anything changed (thread_model.go <c>applyFlagChange</c>).
    /// </summary>
    public static (IReadOnlyList<Flag> Flags, bool Changed) ApplyFlagChange(
        IReadOnlyList<Flag> flags, IReadOnlyList<Flag> set, IReadOnlyList<Flag> clear)
    {
        ArgumentNullException.ThrowIfNull(flags);
        ArgumentNullException.ThrowIfNull(set);
        ArgumentNullException.ThrowIfNull(clear);
        var changed = false;
        var output = new List<Flag>(flags.Count + set.Count);
        foreach (var f in flags)
        {
            if (FolderTree.HasFlag(clear, f) && !FolderTree.HasFlag(set, f))
            {
                changed = true;
                continue;
            }
            output.Add(f);
        }
        foreach (var f in set)
        {
            if (!FolderTree.HasFlag(output, f))
            {
                output.Add(f);
                changed = true;
            }
        }
        return (output, changed);
    }

    // Loading

    /// <summary>
    /// Replaces the list with the first page of thread.list. A conversation
    /// that was listed before with the same shape keeps its fetched members,
    /// so a reload (sync finished) does not blink an expanded conversation
    /// through the spinner. Duplicate ids keep the first.
    /// </summary>
    public void SetThreads(IReadOnlyList<ThreadSummary> list, PageInfo page)
    {
        ArgumentNullException.ThrowIfNull(list);
        ArgumentNullException.ThrowIfNull(page);
        var prevSummary = new Dictionary<ThreadId, ThreadSummary>();
        foreach (var t in threads)
        {
            prevSummary[t.Id] = t;
        }
        var prevMembers = new Dictionary<ThreadId, ThreadMembers>(Members);
        threads.Clear();
        tindex.Clear();
        Members.Clear();
        foreach (var t in list)
        {
            if (tindex.ContainsKey(t.Id))
            {
                continue;
            }
            tindex[t.Id] = threads.Count;
            threads.Add(t);
            if (prevMembers.TryGetValue(t.Id, out var prev) && prev.Complete
                && prevSummary.TryGetValue(t.Id, out var ps) && SameShape(ps, t))
            {
                Members[t.Id] = prev;
            }
            else
            {
                Members[t.Id] = MembersFromListing(t);
            }
        }
        NextCursor = page.NextCursor;
        Total = page.Total;
        ListErr = null;
        ReindexMembers();
        RebuildRows();
    }

    /// <summary>Adds a further page and returns how many rows it added.</summary>
    public int AppendThreads(IReadOnlyList<ThreadSummary> list, PageInfo page)
    {
        ArgumentNullException.ThrowIfNull(list);
        ArgumentNullException.ThrowIfNull(page);
        var added = 0;
        foreach (var t in list)
        {
            if (tindex.ContainsKey(t.Id))
            {
                continue;
            }
            tindex[t.Id] = threads.Count;
            threads.Add(t);
            Members[t.Id] = MembersFromListing(t);
            added++;
        }
        NextCursor = page.NextCursor;
        Total = page.Total;
        ReindexMembers();
        RebuildRows();
        return added;
    }

    /// <summary>Forgets the grouped list (folder switch, disconnect).</summary>
    public void ClearThreads()
    {
        threads.Clear();
        tindex.Clear();
        Members.Clear();
        memberOf.Clear();
        expanded.Clear();
        rows = [];
        rowIdx = new();
    }

    /// <summary>Rebuilds the message → conversation map from the member lists.</summary>
    public void ReindexMembers()
    {
        memberOf.Clear();
        foreach (var (tid, mem) in Members)
        {
            foreach (var s in mem.List)
            {
                memberOf[s.Id] = tid;
            }
        }
    }

    // Folding

    /// <summary>Folds or unfolds a conversation row.</summary>
    public void SetExpanded(ThreadId tid, bool on)
    {
        if (on)
        {
            expanded.Add(tid);
        }
        else
        {
            expanded.Remove(tid);
        }
        RebuildRows();
    }

    /// <summary>
    /// Stores the thread.get answer: the folder members, oldest first, and
    /// the summary as the daemon aggregated it. An empty list means the
    /// conversation left the folder meanwhile; its row goes.
    /// </summary>
    public void SetMembers(ThreadId tid, ThreadSummary t, IReadOnlyList<MessageSummary> list)
    {
        ArgumentNullException.ThrowIfNull(t);
        ArgumentNullException.ThrowIfNull(list);
        if (!tindex.TryGetValue(tid, out var i))
        {
            return;
        }
        if (list.Count == 0)
        {
            DropThread(tid);
            RebuildRows();
            return;
        }
        threads[i] = t;
        Members[tid] = new ThreadMembers([.. list], Complete: true);
        ReindexMembers();
        RebuildRows();
    }

    /// <summary>Removes a conversation from the list.</summary>
    public void DropThread(ThreadId tid)
    {
        if (!tindex.TryGetValue(tid, out var i))
        {
            return;
        }
        threads.RemoveAt(i);
        Members.Remove(tid);
        expanded.Remove(tid);
        ReindexThreads();
        ReindexMembers();
        if (Total > 0)
        {
            Total--;
        }
    }

    /// <summary>
    /// Folds every conversation whose members never arrived (the connection
    /// dropped), so no spinner outlives its reply.
    /// </summary>
    public void CollapseLoading()
    {
        foreach (var tid in expanded.ToArray())
        {
            if (Members.TryGetValue(tid, out var mem) && mem.Complete)
            {
                continue;
            }
            expanded.Remove(tid);
        }
        foreach (var (tid, mem) in Members.ToArray())
        {
            if (mem.Fetching)
            {
                Members[tid] = mem with { Fetching = false };
            }
        }
        RebuildRows();
    }

    // Changes between loads

    /// <summary>
    /// Folds a notified arrival into the grouped list. A listed conversation
    /// takes the message (aggregates bumped, moved to the top; when selected
    /// as a single-message row it unfolds so what the user is reading stays a
    /// row); an unlisted one is added at the top when the filter would list
    /// it. The filter is not applied to a listed conversation, the same
    /// policy as <see cref="FolderTree.MatchesFilter"/>. False means the
    /// message carries no thread id and the list has to be loaded again.
    /// </summary>
    public bool ApplyNewMessage(MessageSummary s, MessageFilter filter, ListKey selected)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (s.ThreadId is not { } threadId)
        {
            return false;
        }
        if (memberOf.ContainsKey(s.Id))
        {
            return true;
        }
        if (!tindex.TryGetValue(threadId, out var i))
        {
            if (!FolderTree.MatchesFilter(s, filter))
            {
                return true;
            }
            var fresh = new ThreadSummary
            {
                Id = threadId,
                AccountId = s.AccountId,
                Subject = s.Subject,
                Participants = s.From,
                MessageCount = 1,
                UnreadCount = FolderTree.HasFlag(s.Flags, Flag.Seen) ? 0 : 1,
                LatestDate = s.Date,
                Latest = s,
                Snippet = s.Snippet,
                Flags = s.Flags,
                HasAttachments = s.HasAttachments,
                FolderIds = [s.FolderId],
            };
            threads.Insert(0, fresh);
            Members[fresh.Id] = new ThreadMembers([s], Complete: true);
            ReindexThreads();
            ReindexMembers();
            if (Total >= 0)
            {
                Total++;
            }
            RebuildRows();
            return true;
        }
        var t = threads[i];
        // The row the user is reading was this conversation's only message:
        // keep it as a member row rather than jumping to the reply.
        if (selected.Thread == t.Id && selected.Message is not null && t.MessageCount <= 1)
        {
            SetExpanded(t.Id, true);
        }
        if (Members.TryGetValue(t.Id, out var mem))
        {
            IReadOnlyList<MessageSummary> list = mem.List.Count > 0 && s.Date >= mem.List[^1].Date
                ? [.. mem.List, s]
                : InsertByDate(mem.List, s);
            Members[t.Id] = mem with { List = list };
        }
        memberOf[s.Id] = t.Id;
        t = t with
        {
            MessageCount = t.MessageCount + 1,
            UnreadCount = FolderTree.HasFlag(s.Flags, Flag.Seen) ? t.UnreadCount : t.UnreadCount + 1,
        };
        if (s.Date >= t.LatestDate)
        {
            t = t with { LatestDate = s.Date, Latest = s, Snippet = s.Snippet };
        }
        t = t with
        {
            Flags = UnionFlags(t.Flags, s.Flags),
            HasAttachments = t.HasAttachments || s.HasAttachments,
            Participants = FrontParticipants(t.Participants, s.From),
        };
        // To the top.
        threads.RemoveAt(i);
        threads.Insert(0, t);
        ReindexThreads();
        RebuildRows();
        return true;
    }

    /// <summary>
    /// Changes the flags of the given messages in whichever mode the list is
    /// in and returns the ids that actually changed. In grouped mode the
    /// conversation's aggregates follow: recomputed from the members when
    /// they are all known, adjusted by the change otherwise.
    /// </summary>
    public IReadOnlyList<MessageId> ApplyFlags(IReadOnlyList<MessageId> ids, IReadOnlyList<Flag>? set = null, IReadOnlyList<Flag>? clear = null)
    {
        ArgumentNullException.ThrowIfNull(ids);
        set ??= [];
        clear ??= [];
        var changed = new List<MessageId>();
        if (!Grouped)
        {
            foreach (var id in ids)
            {
                if (UpdateFlags(id, set, clear))
                {
                    changed.Add(id);
                }
            }
            return changed;
        }
        var touched = new HashSet<ThreadId>();
        foreach (var id in ids)
        {
            if (!memberOf.TryGetValue(id, out var tid) || !Members.TryGetValue(tid, out var mem))
            {
                continue;
            }
            for (var j = 0; j < mem.List.Count; j++)
            {
                if (mem.List[j].Id != id)
                {
                    continue;
                }
                var (flags, did) = ApplyFlagChange(mem.List[j].Flags, set, clear);
                if (!did)
                {
                    break;
                }
                var wasUnread = !FolderTree.HasFlag(mem.List[j].Flags, Flag.Seen);
                var list = mem.List.ToArray();
                list[j] = list[j] with { Flags = flags };
                mem = mem with { List = list };
                Members[tid] = mem;
                changed.Add(id);
                touched.Add(tid);
                if (tindex.TryGetValue(tid, out var i) && !mem.Complete)
                {
                    var t = threads[i];
                    if (t.Latest.Id == id)
                    {
                        t = t with { Latest = t.Latest with { Flags = flags } };
                    }
                    var nowUnread = !FolderTree.HasFlag(flags, Flag.Seen);
                    if (wasUnread && !nowUnread && t.UnreadCount > 0)
                    {
                        t = t with { UnreadCount = t.UnreadCount - 1 };
                    }
                    else if (!wasUnread && nowUnread && t.UnreadCount < t.MessageCount)
                    {
                        t = t with { UnreadCount = t.UnreadCount + 1 };
                    }
                    t = t with { Flags = UnionFlags(t.Flags, set) };
                    if (t.MessageCount <= 1)
                    {
                        t = t with { Flags = flags };
                    }
                    threads[i] = t;
                }
                break;
            }
        }
        foreach (var tid in touched)
        {
            RecomputeThread(tid);
        }
        if (changed.Count > 0)
        {
            RebuildRows();
        }
        return changed;
    }

    /// <summary>Derives a conversation's aggregates from its members when they are all known.</summary>
    public void RecomputeThread(ThreadId tid)
    {
        if (!tindex.TryGetValue(tid, out var i) || !Members.TryGetValue(tid, out var mem) || !mem.Complete || mem.List.Count == 0)
        {
            return;
        }
        var unread = 0;
        var hasAttachments = false;
        IReadOnlyList<Flag> flags = [];
        var senders = new List<Address>(); // newest message first
        for (var j = mem.List.Count - 1; j >= 0; j--)
        {
            var s = mem.List[j];
            if (!FolderTree.HasFlag(s.Flags, Flag.Seen))
            {
                unread++;
            }
            hasAttachments = hasAttachments || s.HasAttachments;
            flags = UnionFlags(flags, s.Flags);
            senders.AddRange(s.From);
        }
        var latest = mem.List[^1];
        threads[i] = threads[i] with
        {
            MessageCount = mem.List.Count,
            UnreadCount = unread,
            HasAttachments = hasAttachments,
            Flags = flags,
            Participants = FrontParticipants([], senders),
            Latest = latest,
            LatestDate = latest.Date,
            Snippet = latest.Snippet,
        };
    }

    /// <summary>
    /// Drops the given messages from the grouped list and returns what
    /// <see cref="RestoreRemoval"/> needs. A conversation whose members are
    /// not all known cannot be edited in place: null, and the caller loads
    /// the list again.
    /// </summary>
    public Removal? RemoveMessages(IReadOnlyList<MessageId> ids)
    {
        ArgumentNullException.ThrowIfNull(ids);
        var byThread = new Dictionary<ThreadId, List<MessageId>>();
        var order = new List<ThreadId>();
        foreach (var id in ids)
        {
            if (!memberOf.TryGetValue(id, out var tid))
            {
                continue;
            }
            if (!byThread.TryGetValue(tid, out var gone))
            {
                order.Add(tid);
                gone = [];
                byThread[tid] = gone;
            }
            gone.Add(id);
        }
        foreach (var tid in order)
        {
            if (!Members.TryGetValue(tid, out var mem) || !mem.Complete)
            {
                return null;
            }
        }
        var snapshots = new List<ThreadSnapshot>();
        foreach (var tid in order)
        {
            if (!tindex.TryGetValue(tid, out var i) || !Members.TryGetValue(tid, out var mem))
            {
                continue;
            }
            var snap = new ThreadSnapshot(i, threads[i], new ThreadMembers([.. mem.List], Complete: true), expanded.Contains(tid));
            var gone = byThread[tid].ToHashSet();
            MessageSummary[] kept = [.. mem.List.Where(s => !gone.Contains(s.Id))];
            if (kept.Length == 0)
            {
                snapshots.Add(snap with { Dropped = true });
                DropThread(tid);
                continue;
            }
            Members[tid] = mem with { List = kept };
            snapshots.Add(snap);
            RecomputeThread(tid);
        }
        ReindexMembers();
        RebuildRows();
        return new Removal(snapshots);
    }

    /// <summary>Puts the conversations of a failed removal back as they were.</summary>
    public void RestoreRemoval(Removal r)
    {
        ArgumentNullException.ThrowIfNull(r);
        foreach (var snap in r.Threads)
        {
            var tid = snap.Summary.Id;
            if (snap.Dropped)
            {
                threads.Insert(Math.Min(snap.Index, threads.Count), snap.Summary);
                ReindexThreads();
                if (Total >= 0)
                {
                    Total++;
                }
            }
            else if (tindex.TryGetValue(tid, out var i))
            {
                threads[i] = snap.Summary;
            }
            else
            {
                continue;
            }
            Members[tid] = snap.Members;
            if (snap.Expanded)
            {
                expanded.Add(tid);
            }
        }
        ReindexMembers();
        RebuildRows();
    }

    // Rows

    /// <summary>
    /// Lays the grouped list out: a conversation with one member is a plain
    /// row; one with more is a conversation row, followed by its members
    /// (oldest first) when unfolded and known. The rows are a new list; the
    /// one handed out before stays as it was.
    /// </summary>
    public void RebuildRows()
    {
        var output = new List<ListRow>();
        foreach (var t in threads)
        {
            Members.TryGetValue(t.Id, out var mem);
            var latest = t.Latest;
            if (mem is { List.Count: > 0 })
            {
                latest = mem.List[^1];
            }
            if (t.MessageCount <= 1)
            {
                output.Add(new ListRow { Key = new ListKey(t.Id, latest.Id), Message = latest });
                continue;
            }
            var exp = expanded.Contains(t.Id);
            var complete = mem?.Complete ?? false;
            output.Add(new ListRow
            {
                Key = new ListKey(t.Id),
                Thread = true,
                Message = latest,
                Summary = t,
                Expanded = exp,
                Loading = exp && !complete,
            });
            if (exp && mem is { Complete: true })
            {
                foreach (var s in mem.List)
                {
                    output.Add(new ListRow { Key = new ListKey(t.Id, s.Id), Member = true, Message = s });
                }
            }
        }
        var idx = new Dictionary<ListKey, int>(output.Count);
        for (var i = 0; i < output.Count; i++)
        {
            idx[output[i].Key] = i;
        }
        rows = output;
        rowIdx = idx;
    }

    /// <summary>The row at list position <paramref name="idx"/> in either mode.</summary>
    public ListRow? RowAt(int idx)
    {
        if (Grouped)
        {
            return idx >= 0 && idx < rows.Count ? rows[idx] : null;
        }
        return MessageAt(idx) is { } s ? new ListRow { Key = new ListKey(Message: s.Id), Message = s } : null;
    }

    /// <summary>The list position of <paramref name="k"/>, -1 when absent.</summary>
    public int RowIndexOf(ListKey k)
    {
        if (Grouped)
        {
            return rowIdx.TryGetValue(k, out var i) ? i : -1;
        }
        return k.Message is { } id && index.TryGetValue(id, out var at) ? at : -1;
    }

    /// <summary>
    /// The row key a message has in the current mode. In grouped mode a
    /// conversation's newest member has a row of its own only while the
    /// conversation is unfolded (or has one member); the conversation row is
    /// keyed by the thread alone.
    /// </summary>
    public ListKey KeyFor(MessageId id)
    {
        if (!Grouped)
        {
            return new ListKey(Message: id);
        }
        return new ListKey(memberOf.TryGetValue(id, out var tid) ? tid : (ThreadId?)null, id);
    }

    /// <summary>
    /// Every message a row stands for: all known folder members of a
    /// conversation row (null while they are not all known), else the one
    /// message.
    /// </summary>
    public IReadOnlyList<MessageId>? RowIds(ListRow r) => RowMessages(r)?.Select(s => s.Id).ToArray();

    /// <summary><see cref="RowIds"/> with the summaries.</summary>
    public IReadOnlyList<MessageSummary>? RowMessages(ListRow r)
    {
        ArgumentNullException.ThrowIfNull(r);
        if (!r.Thread)
        {
            return [r.Message];
        }
        if (r.Key.Thread is not { } tid || !Members.TryGetValue(tid, out var mem) || !mem.Complete)
        {
            return null;
        }
        return mem.List;
    }

    /// <summary>
    /// The flagged state toggle-flag moves a row to: a conversation with no
    /// flagged member gets every member flagged, one with any flagged member
    /// gets them all unflagged.
    /// </summary>
    public static bool FlagTarget(ListRow r)
    {
        ArgumentNullException.ThrowIfNull(r);
        if (r.Thread && r.Summary is { } summary)
        {
            return !FolderTree.HasFlag(summary.Flags, Flag.Flagged);
        }
        return !FolderTree.HasFlag(r.Message.Flags, Flag.Flagged);
    }

    private void ReindexThreads()
    {
        tindex.Clear();
        for (var i = 0; i < threads.Count; i++)
        {
            tindex[threads[i].Id] = i;
        }
    }

    // The grouped side of Clone: the containers copied, the immutable lists
    // and records shared.
    private void CloneThreads(MailModel c)
    {
        c.threads.AddRange(threads);
        foreach (var (id, i) in tindex)
        {
            c.tindex[id] = i;
        }
        foreach (var (id, mem) in Members)
        {
            c.Members[id] = mem;
        }
        foreach (var (id, tid) in memberOf)
        {
            c.memberOf[id] = tid;
        }
        c.expanded.UnionWith(expanded);
        c.rows = rows;
        c.rowIdx = rowIdx;
    }
}
