// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only tests (docs/windows-port.md §7.5): applying snapshots to an
// ObservableCollection by key, as the GTK UI mirrors rows by key
// (ui/internal/window/threads.go): entries that stay keep their object and,
// where the snapshot keeps their order, raise nothing.

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Collections.Specialized;
using System.Linq;
using Malachi.Core.Controllers.Infrastructure;
using Xunit;

namespace Malachi.Core.Tests.Controllers.Infrastructure;

public sealed class KeyedListSyncTests
{
    [Fact]
    public void FillsAnEmptyCollection()
    {
        var (target, log) = Watched();
        var changes = Apply(target, Rows("a", "b", "c"));
        Assert.Equal(["a", "b", "c"], Keys(target));
        Assert.Equal(new KeyedListChanges(3, 0, 0, 0), changes);
        Assert.Equal(["Add a", "Add b", "Add c"], log);
    }

    [Fact]
    public void ANewEntryOnTopTouchesNothingElse()
    {
        var (target, log) = Watched(Rows("a", "b", "c"));
        var before = target.ToArray();
        var changes = Apply(target, Rows("n", "a", "b", "c"));
        Assert.Equal(["Add n"], log);
        Assert.Equal(new KeyedListChanges(1, 0, 0, 3), changes);
        Assert.Equal(before, target.Skip(1));
    }

    [Fact]
    public void RemovedEntriesGoAndTheRestStay()
    {
        var (target, log) = Watched(Rows("a", "b", "c", "d"));
        var c = target[2];
        Apply(target, Rows("a", "c"));
        Assert.Equal(["Remove b", "Remove d"], log);
        Assert.Same(c, target[1]);
    }

    /// <summary>Changed values reach the views in place: the collection raises nothing, the objects stay.</summary>
    [Fact]
    public void UpdatesInPlace()
    {
        var (target, log) = Watched(Rows("a", "b"));
        var before = target.ToArray();
        var changes = Apply(target, [new Row("a", "A!"), new Row("b", "B!")]);
        Assert.Empty(log);
        Assert.True(changes.KeptStructure);
        Assert.Equal(2, changes.Updated);
        Assert.Equal(before, target);
        Assert.Equal(["A!", "B!"], target.Select(v => v.Text));
    }

    /// <summary>One entry moves: one move, whichever way, and the others are not touched.</summary>
    [Theory]
    [InlineData(new[] { "a", "b", "c", "d" }, new[] { "b", "c", "d", "a" })]
    [InlineData(new[] { "a", "b", "c", "d" }, new[] { "d", "a", "b", "c" })]
    [InlineData(new[] { "a", "b", "c", "d" }, new[] { "a", "c", "b", "d" })]
    public void OneEntryMovesOnce(string[] from, string[] to)
    {
        var (target, log) = Watched(Rows(from));
        var views = target.ToDictionary(v => v.Key);
        var changes = Apply(target, Rows(to));
        Assert.Equal(to, Keys(target));
        Assert.Equal(1, changes.Moved);
        Assert.Single(log);
        foreach (var v in target)
        {
            Assert.Same(views[v.Key], v);
        }
    }

    [Fact]
    public void ItemsThemselvesAreReplacedOnlyWhenTheyChanged()
    {
        var target = new ObservableCollection<Row>(Rows("a", "b", "c"));
        var log = Log(target);
        var changes = KeyedListSync.Apply(target, [new Row("a", "a"), new Row("b", "B!"), new Row("c", "c")], r => r.Key);
        Assert.Equal(["Replace b"], log);
        Assert.Equal(new KeyedListChanges(0, 0, 0, 1), changes);
        Assert.Equal("B!", target[1].Text);
    }

    [Fact]
    public void RepeatedKeysAreRefused()
    {
        var target = new ObservableCollection<View>();
        Assert.Throws<ArgumentException>(() => Apply(target, Rows("a", "b", "a")));
    }

    [Fact]
    public void RepeatsInTheCollectionAreDropped()
    {
        var target = new ObservableCollection<View> { new("a", "1"), new("b", "1"), new("a", "2") };
        Apply(target, Rows("a", "b"));
        Assert.Equal(["a", "b"], Keys(target));
    }

    /// <summary>
    /// Random edits: the collection always ends as the snapshot, every entry
    /// that stays keeps its object, and only the entries outside the longest
    /// run already in order move.
    /// </summary>
    [Fact]
    public void RandomSnapshots()
    {
        var random = new Random(20260927);
        var (target, _) = Watched();
        var next = 0;
        var current = new List<string>();
        for (var round = 0; round < 500; round++)
        {
            var snapshot = current.Where(_ => random.Next(10) > 0).ToList();
            for (var i = random.Next(4); i > 0; i--)
            {
                snapshot.Insert(random.Next(snapshot.Count + 1), $"k{next++}");
            }
            for (var i = random.Next(3); i > 0 && snapshot.Count > 1; i--)
            {
                var from = random.Next(snapshot.Count);
                var key = snapshot[from];
                snapshot.RemoveAt(from);
                snapshot.Insert(random.Next(snapshot.Count + 1), key);
            }
            var views = target.ToDictionary(v => v.Key);
            var stayed = snapshot.Where(views.ContainsKey).Select(k => current.IndexOf(k)).ToList();
            var changes = Apply(target, Rows([.. snapshot]));
            Assert.Equal(snapshot, Keys(target));
            foreach (var v in target.Where(v => views.ContainsKey(v.Key)))
            {
                Assert.Same(views[v.Key], v);
            }
            Assert.Equal(stayed.Count - LongestIncreasing(stayed), changes.Moved);
            current = snapshot;
        }
    }

    private static KeyedListChanges Apply(ObservableCollection<View> target, IReadOnlyList<Row> rows) =>
        KeyedListSync.Apply(target, rows, r => r.Key, v => v.Key, r => new View(r.Key, r.Text), (v, r) => v.Text = r.Text);

    private static Row[] Rows(params string[] keys) => [.. keys.Select(k => new Row(k, k))];

    private static List<string> Keys(IEnumerable<View> views) => [.. views.Select(v => v.Key)];

    private static (ObservableCollection<View> Target, List<string> Log) Watched(IEnumerable<Row>? rows = null)
    {
        var target = new ObservableCollection<View>((rows ?? []).Select(r => new View(r.Key, r.Text)));
        return (target, Log(target));
    }

    private static List<string> Log<T>(ObservableCollection<T> target)
    {
        var log = new List<string>();
        target.CollectionChanged += (_, e) => log.Add(e.Action switch
        {
            NotifyCollectionChangedAction.Add => $"Add {KeyOf(e.NewItems![0])}",
            NotifyCollectionChangedAction.Remove => $"Remove {KeyOf(e.OldItems![0])}",
            NotifyCollectionChangedAction.Move => $"Move {KeyOf(e.NewItems![0])}",
            NotifyCollectionChangedAction.Replace => $"Replace {KeyOf(e.NewItems![0])}",
            _ => e.Action.ToString(),
        });
        return log;
    }

    private static string? KeyOf(object? item) => item switch
    {
        View v => v.Key,
        Row r => r.Key,
        _ => null,
    };

    private static int LongestIncreasing(List<int> values)
    {
        var tails = new List<int>();
        foreach (var v in values)
        {
            var i = tails.BinarySearch(v);
            i = i < 0 ? ~i : i;
            if (i == tails.Count)
            {
                tails.Add(v);
            }
            else
            {
                tails[i] = v;
            }
        }
        return tails.Count;
    }

    private sealed record Row(string Key, string Text);

    /// <summary>A row view model: an object with an identity and a mutable text.</summary>
    private sealed class View(string key, string text)
    {
        public string Key { get; } = key;

        public string Text { get; set; } = text;
    }
}
