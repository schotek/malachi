// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The counterpart of the GTK UI's mirroring of rows by their keys
// (ui/internal/window/threads.go, syncRows: rows whose key stays are kept,
// so the ListBox keeps its selection and scroll position) and of the
// NSTableView diffing of the macOS views: docs/windows-port.md §7.5. The
// controllers publish whole snapshots; this applies one to the
// ObservableCollection a WinUI ListView shows with the fewest changes.

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Diagnostics;

namespace Malachi.Core.Controllers.Infrastructure;

/// <summary>
/// Applies a snapshot of a list to an <see cref="ObservableCollection{T}"/>
/// by key: entries whose key is gone are removed, new keys inserted, and
/// entries that stay keep their object, so a <c>ListView</c> keeps their
/// containers, its selection and its scroll position. Of the entries that
/// stay, the longest run already in the snapshot's order is not touched at
/// all; each other one is moved once. Keys must be unique in the snapshot.
/// Call it on the UI thread.
/// </summary>
public static class KeyedListSync
{
    /// <summary>
    /// Makes <paramref name="target"/> show <paramref name="snapshot"/>
    /// through views: a new key gets <paramref name="create"/>'s view, and
    /// every view that stays is handed its new item through
    /// <paramref name="update"/> in place (an
    /// <c>INotifyPropertyChanged</c> row view model updates its bindings; the
    /// collection raises nothing for it).
    /// </summary>
    public static KeyedListChanges Apply<TKey, TItem, TView>(
        ObservableCollection<TView> target,
        IReadOnlyList<TItem> snapshot,
        Func<TItem, TKey> itemKey,
        Func<TView, TKey> viewKey,
        Func<TItem, TView> create,
        Action<TView, TItem> update,
        IEqualityComparer<TKey>? keyComparer = null)
        where TKey : notnull
        where TView : class
    {
        ArgumentNullException.ThrowIfNull(target);
        ArgumentNullException.ThrowIfNull(snapshot);
        ArgumentNullException.ThrowIfNull(itemKey);
        ArgumentNullException.ThrowIfNull(viewKey);
        ArgumentNullException.ThrowIfNull(create);
        ArgumentNullException.ThrowIfNull(update);
        var changes = Arrange(target, snapshot, itemKey, viewKey, create, keyComparer ?? EqualityComparer<TKey>.Default);
        for (var i = 0; i < snapshot.Count; i++)
        {
            update(target[i], snapshot[i]);
        }
        return changes with { Updated = snapshot.Count - changes.Inserted };
    }

    /// <summary>
    /// Makes <paramref name="target"/> hold <paramref name="snapshot"/>'s
    /// items themselves (immutable records): an item whose key stays but
    /// whose value changed replaces the old one at its place, a
    /// <c>Replace</c> the view re-renders; an equal one is left alone.
    /// </summary>
    public static KeyedListChanges Apply<TKey, TItem>(
        ObservableCollection<TItem> target,
        IReadOnlyList<TItem> snapshot,
        Func<TItem, TKey> key,
        IEqualityComparer<TItem>? itemComparer = null,
        IEqualityComparer<TKey>? keyComparer = null)
        where TKey : notnull
    {
        ArgumentNullException.ThrowIfNull(target);
        ArgumentNullException.ThrowIfNull(snapshot);
        ArgumentNullException.ThrowIfNull(key);
        var items = itemComparer ?? EqualityComparer<TItem>.Default;
        var changes = Arrange(target, snapshot, key, key, item => item, keyComparer ?? EqualityComparer<TKey>.Default);
        var replaced = 0;
        for (var i = 0; i < snapshot.Count; i++)
        {
            if (!items.Equals(target[i], snapshot[i]))
            {
                target[i] = snapshot[i];
                replaced++;
            }
        }
        return changes with { Updated = replaced };
    }

    /// <summary>
    /// Gives <paramref name="target"/> the keys of <paramref name="snapshot"/>
    /// in its order: removes, inserts and moves, never replaces.
    /// </summary>
    private static KeyedListChanges Arrange<TKey, TItem, TView>(
        ObservableCollection<TView> target,
        IReadOnlyList<TItem> snapshot,
        Func<TItem, TKey> itemKey,
        Func<TView, TKey> viewKey,
        Func<TItem, TView> create,
        IEqualityComparer<TKey> keys)
        where TKey : notnull
    {
        var wanted = new Dictionary<TKey, int>(snapshot.Count, keys);
        for (var i = 0; i < snapshot.Count; i++)
        {
            if (!wanted.TryAdd(itemKey(snapshot[i]), i))
            {
                throw new ArgumentException($"the key of entry {i} repeats an earlier entry's", nameof(snapshot));
            }
        }

        // Keys that are gone, and repeats of a key in the collection.
        var removed = 0;
        var kept = new HashSet<TKey>(keys);
        for (var i = 0; i < target.Count;)
        {
            var k = viewKey(target[i]);
            if (wanted.ContainsKey(k) && kept.Add(k))
            {
                i++;
                continue;
            }
            target.RemoveAt(i);
            removed++;
        }

        // Where each wanted key is now; the longest run already in order stays.
        var before = new Dictionary<TKey, int>(target.Count, keys);
        for (var i = 0; i < target.Count; i++)
        {
            before[viewKey(target[i])] = i;
        }
        var positions = new int[snapshot.Count];
        for (var i = 0; i < snapshot.Count; i++)
        {
            positions[i] = before.TryGetValue(itemKey(snapshot[i]), out var at) ? at : -1;
        }
        var stays = LongestIncreasingRun(positions);

        // Every other entry goes right behind the one the snapshot puts
        // before it; that one is in its final place relative to the others
        // already, so the order comes out as the snapshot's.
        var inserted = 0;
        var moved = 0;
        for (var i = 0; i < snapshot.Count; i++)
        {
            if (stays[i])
            {
                continue;
            }
            var key = itemKey(snapshot[i]);
            var after = i == 0 ? -1 : IndexOf(target, viewKey, itemKey(snapshot[i - 1]), keys);
            if (positions[i] < 0)
            {
                target.Insert(after + 1, create(snapshot[i]));
                inserted++;
                continue;
            }
            var from = IndexOf(target, viewKey, key, keys);
            var to = from <= after ? after : after + 1;
            if (from != to)
            {
                target.Move(from, to);
                moved++;
            }
        }
        Debug.Assert(target.Count == snapshot.Count, "the collection has the snapshot's length");
        return new KeyedListChanges(inserted, removed, moved, 0);
    }

    private static int IndexOf<TKey, TView>(ObservableCollection<TView> target, Func<TView, TKey> viewKey, TKey key, IEqualityComparer<TKey> keys)
    {
        for (var i = 0; i < target.Count; i++)
        {
            if (keys.Equals(viewKey(target[i]), key))
            {
                return i;
            }
        }
        return -1;
    }

    /// <summary>
    /// The entries of <paramref name="positions"/> (−1 for none) that form
    /// the longest strictly increasing run, as flags per entry (patience
    /// sorting, O(n log n)).
    /// </summary>
    internal static bool[] LongestIncreasingRun(IReadOnlyList<int> positions)
    {
        var n = positions.Count;
        var tails = new List<int>(n); // index of the entry ending the best run of each length
        var previous = new int[n];
        for (var i = 0; i < n; i++)
        {
            previous[i] = -1;
            var p = positions[i];
            if (p < 0)
            {
                continue;
            }
            int lo = 0, hi = tails.Count;
            while (lo < hi)
            {
                var mid = (lo + hi) / 2;
                if (positions[tails[mid]] < p)
                {
                    lo = mid + 1;
                }
                else
                {
                    hi = mid;
                }
            }
            previous[i] = lo > 0 ? tails[lo - 1] : -1;
            if (lo == tails.Count)
            {
                tails.Add(i);
            }
            else
            {
                tails[lo] = i;
            }
        }
        var run = new bool[n];
        for (var i = tails.Count > 0 ? tails[^1] : -1; i >= 0; i = previous[i])
        {
            run[i] = true;
        }
        return run;
    }
}
