// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/contextmenu/rules_test.go (TestMessageItemsRule,
// TestFolderItemsRules, TestRulesLabels). The groups are written as in Go
// ("id id- | id", "-" after a disabled item), with Go's ids
// (ContextMenu.Name); the process-wide catalogue is English, so every msgid
// is its own translation.

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Xunit;
using static Malachi.Core.ContextMenus.ContextMenu;

namespace Malachi.Core.Tests.ContextMenus;

public sealed class ContextMenuRulesTests
{
    private static string Show(IReadOnlyList<IReadOnlyList<Item>> groups) =>
        string.Join(" | ", groups.Select(g => string.Join(" ", g.Select(it => Name(it.Id) + (it.Enabled ? "" : "-")))));

    // Create Rule from Message… is a group of its own before the Assistant,
    // for one message outside the outbox and the drafts only.
    [Fact]
    public void MessageItemsOfferCreateRuleForOneMessage()
    {
        var baseState = new MessageState { OfferRule = true, ToggleFlag = true, Assistant = true };
        Assert.Equal("open | toggleFlag | createRule | assistant", Show(MessageItems(baseState)));
        Assert.Equal(
            "open | toggleFlag | assistant",
            Show(MessageItems(baseState with { Count = 2, Open = true })));
        Assert.Equal("edit | toggleFlag | assistant", Show(MessageItems(baseState with { Draft = true })));
        Assert.Equal("open | toggleFlag | trash- | assistant", Show(MessageItems(baseState with { Outbox = true })));
        Assert.Equal("open | toggleFlag | assistant", Show(MessageItems(baseState with { OfferRule = false })));
    }

    // Apply Rules… is in the acting group of a mail folder, disabled
    // without a rule for the account or without messages.
    [Fact]
    public void FolderItemsOfferApplyRules()
    {
        (string Name, FolderState State, string Want)[] cases =
        [
            (
                "inbox with rules",
                new FolderState { Selectable = true, Role = FolderRole.Inbox, Total = 5, OfferRules = true, Rules = true },
                "checkMail | markAllRead- applyRules | favourite"
            ),
            (
                "no rule for the account",
                new FolderState { Selectable = true, Role = FolderRole.Inbox, Total = 5, OfferRules = true },
                "checkMail | markAllRead- applyRules- | favourite"
            ),
            (
                "empty folder",
                new FolderState { Selectable = true, Role = FolderRole.None, OfferRules = true, Rules = true },
                "checkMail | markAllRead- applyRules- | favourite"
            ),
            (
                "outbox",
                new FolderState { Selectable = true, Role = FolderRole.Outbox, Total = 1, OfferRules = true, Rules = true },
                "favourite"
            ),
            (
                "drafts",
                new FolderState { Selectable = true, Role = FolderRole.Drafts, Total = 1, OfferRules = true, Rules = true },
                "checkMail | markAllRead- | favourite"
            ),
            (
                "issue tracker",
                new FolderState { Selectable = true, Role = FolderRole.None, Total = 1, Rules = true },
                "checkMail | markAllRead- | favourite"
            ),
        ];
        foreach (var (name, state, want) in cases)
        {
            Assert.True(Show(FolderItems(state)) == want, $"{name}: got \"{Show(FolderItems(state))}\", want \"{want}\"");
        }
    }

    [Fact]
    public void TheRulesItemsHaveTheirLabelsAndGoIds()
    {
        Assert.Equal("Create Rule from Message…", Label(ItemId.CreateRule, new Labels()));
        Assert.Equal("Apply Rules…", Label(ItemId.ApplyRules, new Labels()));
        Assert.Equal("createRule", Name(ItemId.CreateRule));
        Assert.Equal("applyRules", Name(ItemId.ApplyRules));
    }
}
