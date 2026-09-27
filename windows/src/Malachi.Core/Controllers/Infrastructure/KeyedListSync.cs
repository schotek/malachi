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
using System.Runtime.InteropServices;

namespace Malachi.Core.Controllers.Infrastructure;

/// <summary>
/// Applies a snapshot of a list to an <see cref="ObservableCollection{T}"/>
/// by key: entries whose key is gone are removed, new keys inserted, and
/// entries that stay keep their object, so a <c>ListView</c> keeps their
/// containers and its scroll position. Of the entries that stay, the longest
/// run already in the snapshot's order is not touched at all; each other
/// one is moved once. Keys must be unique in the snapshot. Call it on the UI
/// thread.
/// </summary>
/// <remarks>
/// <para>
/// Cost: O(n log n) for the run, plus for each entry that moves a scan of an
/// int array (vectorised) and the collection's own shifting of its
/// elements, which a Move costs anyway; a first fill appends. No key is
/// compared more than a few times.
/// </para>
/// <para>
/// Selection: WinUI and UWP have been reported to turn an
/// <see cref="ObservableCollection{T}.Move(int, int)"/> into a removal and an
/// insertion, which may deselect the moved row (not verified for WinUI 3;
/// phase E checks it). A list controller therefore re-applies its selection
/// by key after a sync whose <see cref="KeyedListChanges.Moved"/> is not 0,
/// rather than trusting the <c>ListView</c> to keep it.
/// </para>
/// </remarks>
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

        // Keys that are gone, and repeats of a key in the collection (the
        // first one stays), removed from the end so that each removal shifts
        // only what follows it.
        var kept = new HashSet<TKey>(keys);
        var goes = new bool[target.Count];
        for (var i = 0; i < target.Count; i++)
        {
            var k = viewKey(target[i]);
            goes[i] = !(wanted.ContainsKey(k) && kept.Add(k));
        }
        var removed = 0;
        for (var i = goes.Length - 1; i >= 0; i--)
        {
            if (goes[i])
            {
                target.RemoveAt(i);
                removed++;
            }
        }

        // The collection as the snapshot indexes of its entries, kept in step
        // with it: a key is compared once here, and an entry is found again
        // by a scan of ints. Where each wanted key is now (-1 for a new one);
        // the longest run already in order stays.
        var order = new List<int>(snapshot.Count);
        var positions = new int[snapshot.Count];
        Array.Fill(positions, -1);
        for (var p = 0; p < target.Count; p++)
        {
            var at = wanted[viewKey(target[p])];
            order.Add(at);
            positions[at] = p;
        }
        var stays = LongestIncreasingRun(positions);

        // Every other entry goes right behind the one the snapshot puts
        // before it; that one is in its final place relative to the others
        // already, so the order comes out as the snapshot's. Where the step
        // before put its entry is known; after an entry that stayed, it is
        // looked up.
        var inserted = 0;
        var moved = 0;
        int? previousAt = null;
        for (var i = 0; i < snapshot.Count; i++)
        {
            if (stays[i])
            {
                previousAt = null;
                continue;
            }
            var after = i == 0 ? -1 : previousAt ?? IndexOf(order, i - 1);
            if (positions[i] < 0)
            {
                target.Insert(after + 1, create(snapshot[i]));
                order.Insert(after + 1, i);
                inserted++;
                previousAt = after + 1;
                continue;
            }
            var from = IndexOf(order, i);
            var to = from <= after ? after : after + 1;
            if (from != to)
            {
                target.Move(from, to);
                order.RemoveAt(from);
                order.Insert(to, i);
                moved++;
            }
            previousAt = to;
        }
        Debug.Assert(target.Count == snapshot.Count, "the collection has the snapshot's length");
        return new KeyedListChanges(inserted, removed, moved, 0);
    }

    private static int IndexOf(List<int> order, int entry) => CollectionsMarshal.AsSpan(order).IndexOf(entry);

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
