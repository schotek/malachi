// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ConversationTests.swift
// (ConversationFoldTests), the counterpart of
// ui/internal/conversation/fold_test.go: how every card of the conversation
// starts folded or open, the user's choices over that, and the Collapse All
// / Expand All button above the conversation. The fixtures are
// ConversationTests'.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Tests.I18n;
using Xunit;
using static Malachi.Core.Tests.Model.ConversationTests;

namespace Malachi.Core.Tests.Model;

public sealed class ConversationFoldTests
{
    // A card dated by its id's length, as fold_test.go's card.
    private static ConversationItem Card(string id, bool sent) =>
        new() { Kind = ConversationItemKind.Message, Message = Mail(id, id.Length, true), Sent = sent };

    // A fold state as "id=folded" pairs in the order of the ids, so that two
    // states compare whatever order their dictionaries keep.
    private static string Text(IReadOnlyDictionary<MessageId, bool> state) =>
        string.Join(" ", state.OrderBy(e => e.Key.Value, StringComparer.Ordinal).Select(e => $"{e.Key.Value}={(e.Value ? "folded" : "open")}"));

    private static string Text(params (string Id, bool Folded)[] state) =>
        Text(state.ToDictionary(e => new MessageId(e.Id), e => e.Folded));

    private static readonly ConversationItem Event = new() { Kind = ConversationItemKind.Event, Message = Mail("e", 0, true) };

    private static readonly ConversationItem More = new() { Kind = ConversationItemKind.Truncated, Text = "2 earlier messages are not shown" };

    private static readonly ConversationItem NoId = new() { Kind = ConversationItemKind.Message, Message = Mail("", 0, true) };

    public static TheoryData<string, ConversationItem[], string?, string> DefaultCases => new()
    {
        { "nothing", [], null, Text() },
        { "one message", [Card("a", false)], "a", Text(("a", false)) },
        { "opening folded while another follows", [Card("a", false), Card("bb", false)], "a", Text(("a", true), ("bb", false)) },
        { "a message and its status changes", [Card("a", false), Event, More], "a", Text(("a", false)) },
        {
            "a message and the user's replies", [Card("a", false), Card("rr", true), Card("rrr", true)], "a",
            Text(("a", false), ("rr", true), ("rrr", true))
        },
        {
            "replies among messages", [Card("a", false), Card("rr", true), Card("bbb", false)], "a",
            Text(("a", true), ("rr", true), ("bbb", false))
        },
        { "the user's reply opened it", [Card("r", true), Card("aa", false)], "r", Text(("r", true), ("aa", false)) },
        { "no opening", [More, Card("a", false), Card("bb", false)], null, Text(("a", false), ("bb", false)) },
        // Pathological: every card a reply. The newest opens.
        { "all sent", [Card("r", true), Card("rrr", true), Card("rr", true)], "r", Text(("r", true), ("rr", true), ("rrr", false)) },
        {
            "a repeated id keeps its first state", [Card("a", false), Card("bb", true), Card("bb", false)], "a",
            Text(("a", false), ("bb", true))
        },
        { "no id", [NoId, Card("a", false)], null, Text(("a", false)) },
    };

    [Theory]
    [MemberData(nameof(DefaultCases))]
    public void DefaultFolds(string name, ConversationItem[] items, string? opening, string want) =>
        Assert.True(
            want == Text(Conversation.DefaultFolds(items, opening is null ? (MessageId?)null : new MessageId(opening))),
            $"{name}: {Text(Conversation.DefaultFolds(items, opening is null ? (MessageId?)null : new MessageId(opening)))}");

    [Fact]
    public void Folds()
    {
        ConversationItem[] items = [Card("a", false), Card("rr", true), Card("bbb", false)];
        var f = new ConversationFolds();
        f.Show("t1");
        Assert.Equal(Text(("a", true), ("rr", true), ("bbb", false)), Text(f.State(items, "a"))); // defaults
        f.Set("a", false);
        f.Set("bbb", true);
        f.Set("", true);
        Assert.Equal(Text(("a", false), ("rr", true), ("bbb", true)), Text(f.State(items, "a"))); // choices
        // An update of the same conversation keeps the choices; a card that
        // arrives starts as its default.
        f.Show("t1");
        ConversationItem[] more = [.. items, Card("cccc", false)];
        Assert.Equal(Text(("a", false), ("rr", true), ("bbb", true), ("cccc", false)), Text(f.State(more, "a"))); // after an arrival
        // Collapse All, then a card arrives: it starts open, and the button
        // offers Collapse All again.
        f.SetAll(items, ConversationFoldAll.Collapse.Folded);
        Assert.Equal(ConversationFoldAll.Expand, Conversation.FoldAllOffer(f.State(items, "a"))); // after Collapse All
        Assert.Equal(ConversationFoldAll.Collapse, Conversation.FoldAllOffer(f.State(more, "a"))); // after an arrival
        f.SetAll(more, ConversationFoldAll.Expand.Folded);
        Assert.Equal(Text(("a", false), ("rr", false), ("bbb", false), ("cccc", false)), Text(f.State(more, "a"))); // after Expand All
        // Another conversation forgets them.
        f.Show("t2");
        var other = f.State(items, "a");
        Assert.True(other["a"] && other["rr"]); // another conversation
    }

    [Fact]
    public void FoldAllOffer()
    {
        var cases = new (Dictionary<MessageId, bool> State, ConversationFoldAll Want, string Label)[]
        {
            (new(), ConversationFoldAll.None, ""),
            (new() { ["a"] = false }, ConversationFoldAll.None, ""),
            (new() { ["a"] = true, ["b"] = false }, ConversationFoldAll.Collapse, "Collapse All"),
            (new() { ["a"] = false, ["b"] = false }, ConversationFoldAll.Collapse, "Collapse All"),
            (new() { ["a"] = true, ["b"] = true }, ConversationFoldAll.Expand, "Expand All"),
        };
        foreach (var (state, want, label) in cases)
        {
            var got = Conversation.FoldAllOffer(state);
            Assert.True(got == want && got.Label == label, $"{Text(state)}: {got} {got.Label}");
        }
        Assert.True(ConversationFoldAll.Collapse.Folded && !ConversationFoldAll.Expand.Folded && !ConversationFoldAll.None.Folded);
    }

    // conversation_layout_test.go: the display order hands every card its
    // default fold, the opening card's included.
    [Fact]
    public void DisplayOrderCarriesTheFolds()
    {
        var m = Conversation.Build(MailThread(2), [Mail("m1", 0, true), Mail("m2", 20, true)], MailAccount, [Reply("r1", 10)]);
        var d = ConversationLayout.DisplayOrder(m.Items);
        Assert.Equal("m1", d.Opening?.Value);
        Assert.True(d.RootFolded);
        Assert.Equal(Text(("m1", true), ("r1", true), ("m2", false)), Text(d.Folded));
        // A message and the user's reply: the message shows whole.
        var one = Conversation.Build(MailThread(1), [Mail("m1", 0, true)], MailAccount, [Reply("r1", 10)]);
        var d1 = ConversationLayout.DisplayOrder(one.Items);
        Assert.False(d1.RootFolded);
        Assert.Equal(Text(("m1", false), ("r1", true)), Text(d1.Folded));
    }

    // po_test.go TestMsgidsInTemplate for fold.go: the button's two msgids
    // are the template's, and the port translates them as such.
    [Fact]
    public void TheButtonsTextsAreTheTemplatesMsgids()
    {
        var pot = PoFile.Parse(File.ReadAllText(RepositoryPo.Pot), "malachi.pot");
        foreach (var msgid in new[] { "Collapse All", "Expand All" })
        {
            var entry = pot.Entries.Single(e => e.Msgid == msgid && e.Msgctxt is null);
            Assert.Null(entry.MsgidPlural);
        }
        Assert.Equal(L10n.T("Collapse All"), ConversationFoldAll.Collapse.Label);
        Assert.Equal(L10n.T("Expand All"), ConversationFoldAll.Expand.Label);
    }

    private static MessageSummary Reply(string id, int min) =>
        Mail(id, min, false) with { FolderId = "fs", From = [Petr], To = [Jana] };
}
