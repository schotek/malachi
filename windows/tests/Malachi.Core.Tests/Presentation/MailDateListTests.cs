// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Presentation counterpart of ui/internal/window/date_groups_test.go and
// macOS MessageListViewController.swift's section projection.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using static Malachi.Core.Tests.Model.MailDateGroupsTests;

namespace Malachi.Core.Tests.Presentation;

public sealed class MailDateListTests
{
    private static readonly DateTimeOffset Now = new(2026, 10, 15, 12, 0, 0, TimeSpan.Zero);
    private static readonly FolderKey Inbox = new("account", "inbox");
    private static readonly MailDateGroup Today = new(MailDateGroupKind.Today);
    private static readonly MailDateGroup Flagged = new(MailDateGroupKind.Flagged);

    [Fact]
    public void CollapseKeepsHeaderAndReportsHiddenSelection()
    {
        var model = new MailDateList(new FakeTimeProvider(Now));
        var row = Row("a", Now);
        Apply(model, [row]);
        var header = Assert.Single(model.Sections);
        var view = Assert.Single(model.Rows);
        Assert.True(model.Toggle(Today, row.Key));
        Apply(model, [row]); // the view clears the controller selection before applying
        Assert.Same(header, Assert.Single(model.Sections));
        Assert.Empty(header.Rows);
        Assert.Empty(model.Rows);
        Assert.True(model.HasCollapsed);
        Assert.False(model.Toggle(Today, null));
        Apply(model, [row]);
        Assert.Same(view, Assert.Single(model.Rows));
        Assert.False(model.HasCollapsed);
    }

    [Fact]
    public void FlagMoveRevealsDestinationAndPreservesIdentity()
    {
        var model = new MailDateList(new FakeTimeProvider(Now));
        var row = Row("a", Now);
        var older = Row("b", Now.AddYears(-1), Flag.Flagged);
        Apply(model, [row, older]);
        var selected = model.Rows.Single(r => r.Key == row.Key);
        Assert.False(model.Toggle(Flagged, row.Key));
        Apply(model, [row, older], row.Key);
        Assert.True(model.HasCollapsed);
        var flagged = row with { Message = row.Message with { Flags = [Flag.Flagged] } };
        Apply(model, [flagged, older], row.Key);
        Assert.False(model.HasCollapsed);
        Assert.Same(selected, model.Rows.Single(r => r.Key == row.Key));
        Assert.Equal(2, model.Rows.Count);
        Apply(model, [row, older], row.Key);
        Assert.Same(selected, model.Rows.Single(r => r.Key == row.Key));
        Assert.Equal([older.Key, row.Key], model.Rows.Select(r => r.Key));
    }

    [Fact]
    public void FlagMovesNeverPublishDuplicateItemsBetweenNativeGroups()
    {
        var model = new MailDateList(new FakeTimeProvider(Now));
        var row = Row("a", Now);
        var older = Row("b", Now.AddYears(-1), Flag.Flagged);
        Apply(model, [row, older]);
        foreach (var section in model.Sections)
        {
            section.Rows.CollectionChanged += (_, _) =>
            {
                var keys = model.Sections.SelectMany(s => s.Rows).Select(r => r.Key).ToArray();
                Assert.Equal(keys.Length, keys.Distinct().Count());
            };
        }
        Apply(model, [row with { Message = row.Message with { Flags = [Flag.Flagged] } }, older], row.Key);
        Apply(model, [row, older], row.Key);
    }

    [Fact]
    public void UnflaggingSelectionRevealsCollapsedDateSection()
    {
        var model = new MailDateList(new FakeTimeProvider(Now));
        var selected = Row("a", Now, Flag.Flagged);
        var other = Row("b", Now);
        Apply(model, [selected, other], selected.Key);
        var view = model.Rows.Single(r => r.Key == selected.Key);
        Assert.False(model.Toggle(Today, selected.Key));
        Apply(model, [selected, other], selected.Key);
        Assert.True(model.HasCollapsed);
        Apply(model, [selected with { Message = selected.Message with { Flags = [] } }, other], selected.Key);
        Assert.False(model.HasCollapsed);
        Assert.Same(view, model.Rows.Single(r => r.Key == selected.Key));
        Assert.Equal(Today, Assert.Single(model.Sections).Group);
    }

    [Fact]
    public void PagingAddsOldFlaggedMailWithoutDuplicatesOrOpeningCollapsedGroups()
    {
        var model = new MailDateList(new FakeTimeProvider(Now));
        var today = Row("a", Now);
        Apply(model, [today]);
        model.Toggle(Today, null);
        var old = Row("b", Now.AddYears(-1), Flag.Flagged);
        Apply(model, [today, old]);
        Assert.Equal(Flagged, model.Sections[0].Group);
        Assert.Equal(old.Key, Assert.Single(model.Rows).Key);
        Assert.True(model.HasCollapsed);
        Apply(model, [old]);
        Assert.Single(model.Sections);
        Assert.False(model.HasCollapsed);
    }

    [Fact]
    public void SearchIsFlatAndFolderChangeResetsDisclosure()
    {
        var model = new MailDateList(new FakeTimeProvider(Now));
        var row = Row("a", Now);
        Apply(model, [row]);
        model.Toggle(Today, null);
        model.Apply([row], Inbox, false, null, (_, _, _) => { });
        Assert.Empty(model.Sections);
        Assert.Single(model.Rows);
        Assert.False(model.HasCollapsed);
        Apply(model, [row]);
        Assert.Empty(model.Rows); // same inbox remembers folding through search
        model.Apply([row], new FolderKey("account", "sent"), false, null, (_, _, _) => { });
        Apply(model, [row]);
        Assert.Single(model.Rows);
        Assert.False(model.HasCollapsed);
    }

    [Fact]
    public void MidnightRegroupsUsingInjectedClockAndKeepsSelectedRow()
    {
        var clock = new FakeTimeProvider(Now);
        var model = new MailDateList(clock);
        var row = Row("a", Now);
        Apply(model, [row], row.Key);
        var view = Assert.Single(model.Rows);
        clock.Advance(TimeSpan.FromDays(1));
        Apply(model, [row], row.Key);
        Assert.Equal(MailDateGroupKind.Yesterday, Assert.Single(model.Sections).Group.Kind);
        Assert.Same(view, Assert.Single(model.Rows));
    }

    [Fact]
    public void ProgrammaticFocusCanRevealCollapsedMessage()
    {
        var model = new MailDateList(new FakeTimeProvider(Now));
        var row = Row("a", Now);
        Apply(model, [row]);
        model.Toggle(Today, null);
        Apply(model, [row]);
        Assert.True(model.Reveal(row.Key));
        Apply(model, [row]);
        Assert.Equal(row.Key, Assert.Single(model.Rows).Key);
    }

    private static void Apply(MailDateList model, IReadOnlyList<ListRow> rows, ListKey? selection = null) =>
        model.Apply(rows, Inbox, true, selection, (_, _, _) => { }, TimeZoneInfo.Utc, DayOfWeek.Monday);
}
