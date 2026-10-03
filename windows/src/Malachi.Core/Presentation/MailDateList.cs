// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/maildate/groups.go and
// macos/Sources/MalachiCore/Model/MailDateGroups.swift; disclosure and
// selection projection from window/date_groups.go and
// macos/Sources/MalachiMail/MessageList/MessageListViewController.swift.

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Globalization;
using System.Linq;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <summary>Inbox disclosure state and stable row identities across snapshots and flag moves.</summary>
public sealed class MailDateList
{
    private readonly TimeProvider time;
    private readonly HashSet<MailDateGroup> collapsed = [];
    private readonly Dictionary<ListKey, MessageRow> cache = [];
    private IReadOnlyList<MailDateSection> snapshot = [];
    private FolderKey? folder;

    /// <summary>The clock is injectable for midnight and calendar tests.</summary>
    public MailDateList(TimeProvider? time = null) => this.time = time ?? TimeProvider.System;

    /// <summary>Visible message items, also used for keyboard focus.</summary>
    public ObservableCollection<MessageRow> Rows { get; } = [];

    /// <summary>Native collection groups, including collapsed headers.</summary>
    public ObservableCollection<MailDateSectionRow> Sections { get; } = [];

    /// <summary>Only the normal inbox view enables sections.</summary>
    public bool Enabled { get; private set; }

    /// <summary>Automatic pagination must pause while loaded items are hidden.</summary>
    public bool HasCollapsed => Enabled && snapshot.Any(s => collapsed.Contains(s.Group));

    /// <summary>Reveals a programmatic selection, even after a flag or date move.</summary>
    public bool Reveal(ListKey key)
    {
        var section = snapshot.FirstOrDefault(s => s.Rows.Any(r => r.Key == key));
        return section is not null && collapsed.Remove(section.Group);
    }

    /// <summary>Returns whether collapsing this section hides the selected message.</summary>
    public bool Toggle(MailDateGroup group, ListKey? selection)
    {
        if (collapsed.Remove(group))
        {
            return false;
        }
        collapsed.Add(group);
        return snapshot.Any(s => s.Group == group && s.Rows.Any(r => r.Key == selection));
    }

    /// <summary>Applies loaded rows; projection updates remain entirely on the UI thread.</summary>
    public void Apply(IReadOnlyList<ListRow> source, FolderKey? scope, bool enabled, ListKey? selection,
        Action<MessageRow, ListRow, DateTimeOffset> update, TimeZoneInfo? zone = null, DayOfWeek? firstDay = null)
    {
        ArgumentNullException.ThrowIfNull(source);
        ArgumentNullException.ThrowIfNull(update);
        if (folder != scope)
        {
            collapsed.Clear();
            folder = scope;
        }
        Enabled = enabled;
        var now = time.GetUtcNow();
        snapshot = enabled ? MailDateGroups.Sections(source, now, zone ?? TimeZoneInfo.Local,
            firstDay ?? CultureInfo.CurrentCulture.DateTimeFormat.FirstDayOfWeek) : [];
        if (selection is { } selected)
        {
            Reveal(selected);
        }
        var keys = source.Select(r => r.Key).ToHashSet();
        foreach (var key in cache.Keys.Where(k => !keys.Contains(k)).ToArray())
        {
            cache.Remove(key);
        }
        foreach (var row in source)
        {
            if (!cache.TryGetValue(row.Key, out var view))
            {
                view = new MessageRow(row.Key);
                cache.Add(row.Key, view);
            }
            update(view, row, now);
        }
        // Remove departing children before inserting them in their destination.
        // Native collection views must never observe one instance in two groups,
        // even during the synchronous notifications of a flag move.
        var destinations = snapshot.ToDictionary(s => s.Group,
            s => collapsed.Contains(s.Group) ? new HashSet<ListKey>() : s.Rows.Select(r => r.Key).ToHashSet());
        foreach (var section in Sections)
        {
            destinations.TryGetValue(section.Group, out var wanted);
            for (var i = section.Rows.Count - 1; i >= 0; i--)
            {
                if (wanted is null || !wanted.Contains(section.Rows[i].Key))
                {
                    section.Rows.RemoveAt(i);
                }
            }
        }
        KeyedListSync.Apply(Sections, snapshot, s => s.Group, s => s.Group,
            s => new MailDateSectionRow(s.Group), (view, section) =>
            {
                var hidden = collapsed.Contains(section.Group);
                view.SetCollapsed(hidden);
                var children = hidden ? Array.Empty<ListRow>() : section.Rows;
                KeyedListSync.Apply(view.Rows, children, r => r.Key, r => r.Key,
                    r => cache[r.Key], (_, _) => { });
            });
        var visible = enabled ? snapshot.Where(s => !collapsed.Contains(s.Group)).SelectMany(s => s.Rows).ToArray() : source;
        KeyedListSync.Apply(Rows, visible, r => r.Key, r => r.Key, r => cache[r.Key], (_, _) => { });
        for (var i = 0; i < Rows.Count; i++)
        {
            Rows[i].IsLast = i == Rows.Count - 1;
        }
    }
}
