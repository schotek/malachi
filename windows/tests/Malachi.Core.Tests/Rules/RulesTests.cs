// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/rules/rules_test.go (TestOperatorsFor, TestNewCondition,
// TestInput, TestValidate, TestValidAddress, TestAvailableActions,
// TestFromMessage, TestNeedsConfirm, TestMoved, TestAppliesAndOffers,
// TestSummaries). Go passes a translator that returns the msgids; the
// process-wide catalogue is English here, so every msgid is its own
// translation. The problems are written as Go's {field, index, kind}.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Rules;
using Xunit;

namespace Malachi.Core.Tests.Rules;

public sealed class RulesTests
{
    private static string Join<T>(IEnumerable<T> items) => string.Join(" ", items.Select(i => i!.ToString()));

    [Fact]
    public void OperatorsForTheFields()
    {
        var cases = new Dictionary<string, string>
        {
            [RuleField.From] = "contains notContains is isNot startsWith endsWith domainIs",
            [RuleField.ToOrCc] = "contains notContains is isNot startsWith endsWith domainIs",
            [RuleField.Subject] = "contains notContains is isNot startsWith endsWith",
            [RuleField.ListId] = "contains notContains is isNot startsWith endsWith",
            [RuleField.Bulk] = "is isNot",
            [RuleField.HasAttachments] = "is",
        };
        foreach (var (field, want) in cases)
        {
            Assert.Equal(want, Join(RuleBook.OperatorsFor(field)));
        }
    }

    [Fact]
    public void ANewConditionKeepsWhatTheNewFieldTakes()
    {
        Assert.Equal(new RuleCondition { Field = "from", Op = "contains" }, RuleBook.NewCondition(RuleField.From));
        var old = new RuleCondition { Field = RuleField.From, Op = RuleOperator.DomainIs, Value = "acme.cz" };
        // Subject has no domainIs: the first operator, the text kept.
        Assert.Equal(new RuleCondition { Field = "subject", Op = "contains", Value = "acme.cz" }, RuleBook.NewCondition(RuleField.Subject, old));
        old = old with { Op = RuleOperator.IsNot };
        Assert.Equal(new RuleCondition { Field = "bulk", Op = "isNot", Value = "any" }, RuleBook.NewCondition(RuleField.Bulk, old));
        var bulk = new RuleCondition { Field = RuleField.Bulk, Op = RuleOperator.Is, Value = "list" };
        Assert.Equal(new RuleCondition { Field = "to", Op = "is" }, RuleBook.NewCondition(RuleField.To, bulk));
        Assert.Equal(RuleBook.ValueTrue, RuleBook.NewCondition(RuleField.HasAttachments).Value);
    }

    private static RuleForm Valid()
    {
        var f = RuleBook.NewForm("acc");
        f.Name = " News ";
        f.Conditions[0] = f.Conditions[0] with { Value = " acme.cz " };
        f.Actions =
        [
            new RuleAction { Kind = RuleActionKind.Move, FolderId = "f_news", To = "stale" },
            new RuleAction { Kind = RuleActionKind.MarkRead },
        ];
        return f;
    }

    [Fact]
    public void InputTrimsAndClearsTheFieldsOfOtherKinds()
    {
        var input = RuleBook.Input(Valid());
        Assert.Equal("News", input.Name);
        Assert.Equal("acme.cz", input.Conditions[0].Value);
        Assert.Equal(new RuleAction { Kind = RuleActionKind.Move, FolderId = "f_news" }, input.Actions[0]);

        var f = Valid();
        f.Actions = [new RuleAction { Kind = RuleActionKind.AutoReply, Subject = " s ", Text = "Hi\n\n", FolderId = "x" }];
        Assert.Equal(new RuleAction { Kind = RuleActionKind.AutoReply, Subject = "s", Text = "Hi" }, RuleBook.Input(f).Actions[0]);

        // The form's own rows are not touched.
        f = Valid();
        _ = RuleBook.Input(f);
        Assert.Equal(" News ", f.Name);
        Assert.Equal(" acme.cz ", f.Conditions[0].Value);
    }

    private static string Kinds(IEnumerable<RuleFormProblem> problems) =>
        string.Join(" | ", problems.Select(p => $"{p.Field}:{new string('i', p.Index)}:{RulesText.ProblemText(p)}"));

    [Fact]
    public void ValidateFindsEveryProblemInTheOrderOfTheEditor()
    {
        Assert.Empty(RuleBook.Validate(Valid()));
        var cases = new (string Name, Action<RuleForm> Edit, RuleFormProblem[] Want)[]
        {
            ("no name", f => f.Name = "  ", [new("name", 0, RuleFormProblemKind.NameMissing)]),
            ("long name", f => f.Name = new string('x', RuleLimits.MaxRuleNameBytes + 1), [new("name", 0, RuleFormProblemKind.TooLong)]),
            ("bidi in name", f => f.Name = "a‮b", [new("name", 0, RuleFormProblemKind.BadCharacters)]),
            ("newline in value", f => f.Conditions[0] = f.Conditions[0] with { Value = "a\nb" }, [new("conditions", 0, RuleFormProblemKind.BadCharacters)]),
            ("no condition", f => f.Conditions = [], [new("conditions", 0, RuleFormProblemKind.NoCondition)]),
            (
                "empty value",
                f => f.Conditions.Add(new RuleCondition { Field = RuleField.Subject, Op = RuleOperator.Is, Value = " " }),
                [new("conditions", 1, RuleFormProblemKind.ValueMissing)]
            ),
            (
                "domain of a subject",
                f => f.Conditions[0] = f.Conditions[0] with { Field = RuleField.Subject, Op = RuleOperator.DomainIs },
                [new("conditions", 0, RuleFormProblemKind.BadOperator)]
            ),
            (
                "bad bulk",
                f => f.Conditions[0] = new RuleCondition { Field = RuleField.Bulk, Op = RuleOperator.Is, Value = "spam" },
                [new("conditions", 0, RuleFormProblemKind.ValueMissing)]
            ),
            ("no action", f => f.Actions = [], [new("actions", 0, RuleFormProblemKind.NoAction)]),
            (
                "twice",
                f => f.Actions.Add(new RuleAction { Kind = RuleActionKind.MarkRead }),
                [new("actions", 2, RuleFormProblemKind.ActionTwice)]
            ),
            (
                "two places",
                f => f.Actions.Add(new RuleAction { Kind = RuleActionKind.Trash }),
                [new("actions", 2, RuleFormProblemKind.OnePlace)]
            ),
            ("move for every account", f => f.AccountId = null, [new("actions", 0, RuleFormProblemKind.MoveNeedsAccount)]),
            ("move nowhere", f => f.Actions[0] = f.Actions[0] with { FolderId = null }, [new("actions", 0, RuleFormProblemKind.FolderMissing)]),
            (
                "forward to a list",
                f => f.Actions = [new RuleAction { Kind = RuleActionKind.Forward, To = "a@b.cz, c@d.cz" }],
                [new("actions", 0, RuleFormProblemKind.AddressInvalid)]
            ),
            (
                "empty reply",
                f => f.Actions = [new RuleAction { Kind = RuleActionKind.AutoReply, Text = " \n" }],
                [new("actions", 0, RuleFormProblemKind.ReplyMissing)]
            ),
            (
                "reply with tabs and lines",
                f => f.Actions = [new RuleAction { Kind = RuleActionKind.AutoReply, Text = "Hi\r\n\tthere" }],
                []
            ),
            (
                "reply with a bidi override",
                f => f.Actions = [new RuleAction { Kind = RuleActionKind.AutoReply, Text = "Hi ‮" }],
                [new("actions", 0, RuleFormProblemKind.BadCharacters)]
            ),
        };
        foreach (var (name, edit, want) in cases)
        {
            var f = Valid();
            edit(f);
            var got = RuleBook.Validate(f);
            Assert.True(want.SequenceEqual(got), $"{name}: got {Kinds(got)}, want {Kinds(want)}");
        }
    }

    [Theory]
    [InlineData("a@b.cz", true)]
    [InlineData(" a.b+c@mail.example.com ", true)]
    [InlineData("", false)]
    [InlineData("a", false)]
    [InlineData("a@b", false)]
    [InlineData("@b.cz", false)]
    [InlineData("a@", false)]
    [InlineData("a@@b.cz", false)]
    [InlineData("A <a@b.cz>", false)]
    [InlineData("a@b..cz", false)]
    [InlineData("a@.b.cz", false)]
    [InlineData("a b@c.cz", false)]
    [InlineData("a@b.cz‮", false)]
    public void ValidAddressTakesOneBareAddress(string address, bool want) => Assert.Equal(want, RuleBook.ValidAddress(address));

    [Fact]
    public void AvailableActionsOfferEachKindOnceAndOnePlace()
    {
        var f = RuleBook.NewForm("acc");
        Assert.Equal("markRead flag forward autoReply move junk trash", Join(RuleBook.AvailableActions(f)));
        f.AccountId = null;
        Assert.Equal("markRead flag forward autoReply junk trash", Join(RuleBook.AvailableActions(f)));
        f.Actions = [new RuleAction { Kind = RuleActionKind.Junk }, new RuleAction { Kind = RuleActionKind.Flag }];
        Assert.Equal("markRead forward autoReply", Join(RuleBook.AvailableActions(f)));
    }

    private static MessageSummary Message(params Address[] from) => new()
    {
        Id = "m",
        AccountId = "acc",
        FolderId = "f",
        Subject = "",
        Date = DateTimeOffset.UnixEpoch,
        Snippet = "",
        HasAttachments = false,
        Size = 0,
        From = from,
    };

    [Fact]
    public void FromMessagePrefillsTheSenderOrTheList()
    {
        var m = Message(new Address { Name = "Acme News", Email = "news@acme.cz" });
        var f = RuleBook.FromMessage(m, "acc");
        Assert.Equal("Messages from Acme News", f.Name);
        Assert.Equal("acc", f.AccountId!.Value.Value);
        Assert.True(f.Enabled);
        Assert.Equal([new RuleCondition { Field = "from", Op = "is", Value = "news@acme.cz" }], f.Conditions);
        Assert.Equal([new RuleAction { Kind = RuleActionKind.Move }], f.Actions);

        m = m with { Bulk = new BulkInfo { Kind = BulkKind.List, ListId = "dev.lists.acme.cz" } };
        f = RuleBook.FromMessage(m, "acc");
        Assert.Equal([new RuleCondition { Field = "listId", Op = "is", Value = "dev.lists.acme.cz" }], f.Conditions);

        // A name with a bidi override is not used; the address is.
        m = Message(new Address { Name = "evil‮", Email = "x@y.cz" });
        f = RuleBook.FromMessage(m, null);
        Assert.Equal("Messages from x@y.cz", f.Name);
        Assert.Empty(f.Actions);

        // Without a sender: the empty From condition of a new rule.
        f = RuleBook.FromMessage(Message(), "acc");
        Assert.Equal("", f.Name);
        Assert.Equal("", f.Conditions[0].Value);

        // A long name is cut on a character boundary.
        m = Message(new Address { Name = new string('ž', 200), Email = "a@b.cz" });
        f = RuleBook.FromMessage(m, "acc");
        Assert.True(System.Text.Encoding.UTF8.GetByteCount(f.Name) <= RuleLimits.MaxRuleNameBytes);
        Assert.True(RuleBook.Clean(f.Name));
    }

    [Fact]
    public void OutgoingChangesNeedTheUsersConfirmation()
    {
        var fwd = new RuleInput { Enabled = true, Actions = [new RuleAction { Kind = RuleActionKind.Forward, To = "a@b.cz" }] };
        var local = new RuleInput { Enabled = true, Actions = [new RuleAction { Kind = RuleActionKind.MarkRead }] };
        Assert.False(RuleBook.NeedsConfirm(null, local));
        Assert.True(RuleBook.NeedsConfirm(null, fwd));
        var off = fwd with { Enabled = false };
        Assert.False(RuleBook.NeedsConfirm(null, off));

        static Rule Stored(RuleInput input) => new()
        {
            Id = "r_1",
            Name = input.Name,
            Enabled = input.Enabled,
            AccountId = input.AccountId,
            Match = input.Match,
            Conditions = input.Conditions,
            Actions = input.Actions,
            Stop = input.Stop,
        };

        var stored = Stored(fwd);
        Assert.False(RuleBook.NeedsConfirm(stored, fwd), "unchanged");
        Assert.False(RuleBook.NeedsConfirm(stored, fwd with { Name = "x" }), "renamed");
        var moved = new RuleInput { Enabled = true, Actions = [new RuleAction { Kind = RuleActionKind.Forward, To = "c@d.cz" }] };
        Assert.True(RuleBook.NeedsConfirm(stored, moved), "other address");
        Assert.True(RuleBook.NeedsConfirm(Stored(off), fwd), "turned on");
        Assert.True(RuleBook.NeedsConfirm(Stored(local), fwd), "became outgoing");
        Assert.True(RuleBook.NeedsConfirm(Stored(off), RuleBook.Toggled(Stored(off))), "switch on");
    }

    [Fact]
    public void MovedDropsTheRuleAtTheIndexOfTheListWithoutIt()
    {
        string[] ids = ["a", "b", "c", "d"];
        (int From, int To, string Want)[] cases =
        [
            (0, 3, "b c d a"), (3, 0, "d a b c"), (1, 2, "a c b d"), (2, 2, "a b c d"), (-1, 0, "a b c d"), (0, 4, "a b c d"),
        ];
        foreach (var (from, to, want) in cases)
        {
            Assert.Equal(want, string.Join(' ', RuleBook.Moved(ids, from, to)));
        }
        Assert.Equal("abcd", string.Concat(ids));
    }

    [Fact]
    public void RulesApplyToAnAccountAndMenusAreOfferedFor()
    {
        Rule R(AccountId? account, bool enabled) => new() { Id = "r", Name = "", AccountId = account, Enabled = enabled };
        var rules = new List<Rule> { R("a", false), R("b", true) };
        Assert.False(RuleBook.AppliesToAccount(rules, "a"));
        Assert.True(RuleBook.AppliesToAccount(rules, "b"));
        rules.Add(R(null, true));
        Assert.True(RuleBook.AppliesToAccount(rules, "a"));

        static Folder F(string id, string role = FolderRole.None, bool selectable = true, string? virt = null) => new()
        {
            Id = id,
            AccountId = "acc",
            Name = id,
            Path = id,
            Role = role,
            Subscribed = true,
            Selectable = selectable,
            Synced = true,
            Unread = 0,
            Total = 0,
            Virtual = virt is null ? default(VirtualFolder?) : new VirtualFolder(virt),
        };

        Assert.True(RuleBook.CanApplyIn(F("in", FolderRole.Inbox)));
        Assert.False(RuleBook.CanApplyIn(F("out", FolderRole.Outbox)));
        Assert.False(RuleBook.CanApplyIn(F("box", selectable: false)));
        Assert.False(RuleBook.CanApplyIn(F("v", virt: "open")));
        Assert.True(RuleBook.CanCreateFrom(FolderRole.Inbox));
        Assert.True(RuleBook.CanCreateFrom(FolderRole.Junk));
        Assert.False(RuleBook.CanCreateFrom(FolderRole.Sent));
        Assert.False(RuleBook.CanCreateFrom(FolderRole.Drafts));
        Assert.False(RuleBook.CanCreateFrom(FolderRole.Outbox));

        Folder[] folders =
        [
            F("in", FolderRole.Inbox), F("box", selectable: false), F("out", FolderRole.Outbox), F("all", FolderRole.All),
            F("news"), F("v", virt: "open"),
        ];
        Assert.Equal("in news", string.Join(' ', RuleBook.MoveFolders(folders).Select(f => f.Id.Value)));
    }

    [Fact]
    public void SummariesAreSentences()
    {
        var names = new RuleNames(Folder: (_, id) => id.Value == "f_news" ? "News" : "");
        var input = new RuleInput
        {
            Match = RuleMatch.Any,
            Stop = true,
            Conditions =
            [
                new RuleCondition { Field = RuleField.From, Op = RuleOperator.DomainIs, Value = "acme.cz" },
                new RuleCondition { Field = RuleField.Bulk, Op = RuleOperator.Is, Value = "newsletter" },
            ],
            Actions =
            [
                new RuleAction { Kind = RuleActionKind.Move, FolderId = "f_news" },
                new RuleAction { Kind = RuleActionKind.MarkRead },
            ],
        };
        Assert.Equal("From is from domain “acme.cz” or Bulk mail is newsletter", RulesText.ConditionsLine(input));
        Assert.Equal("mark as read, move to “News”, stop processing more rules", RulesText.ActionsLine(input, "acc", names));
        input = input with { Actions = [input.Actions[0] with { FolderId = "gone" }, input.Actions[1]] };
        Assert.Contains("move to a missing folder", RulesText.ActionsLine(input, "acc", names), StringComparison.Ordinal);

        var at = new DateTimeOffset(2026, 11, 2, 8, 0, 0, TimeSpan.Zero);
        var rule = new Rule
        {
            Id = "r_1",
            Name = input.Name,
            Match = input.Match,
            Conditions = input.Conditions,
            Actions = input.Actions,
            Stop = input.Stop,
            Applied = 2,
            LastAppliedAt = at,
            Skipped = 1,
            Problem = RuleProblem.FolderGone,
            Outgoing = true,
        };
        var rows = RulesText.Rows([rule], names, _ => "today");
        var row = Assert.Single(rows);
        Assert.Equal("Used on 2 messages, last today · 1 outgoing message held back", row.Status);
        Assert.Equal("All Mail Accounts", row.Scope);
        Assert.NotEqual("", row.Problem);
        Assert.True(row.Outgoing);
        Assert.Equal("Removed account", RulesText.ScopeText("acc", new RuleNames(Account: _ => "")));

        var question = RulesText.OutgoingQuestion(
            new RuleInput { Actions = [new RuleAction { Kind = RuleActionKind.Forward, To = "boss@acme.cz" }] });
        Assert.Contains("boss@acme.cz", question.Body, StringComparison.Ordinal);
        Assert.Equal("Turn On", question.Confirm);
        Assert.Equal("The rules changed 1 message.", RulesText.ApplyResultText(new RulesApplyResult { Changed = 1 }));
        Assert.Equal("The rules changed no message.", RulesText.ApplyResultText(new RulesApplyResult()));
        Assert.Equal("The rules changed 2 messages.", RulesText.ApplyResultText(new RulesApplyResult { Changed = 2 }));
    }

    [Fact]
    public void StatusLineWithoutADateAndNeverUsed()
    {
        Assert.Equal("Not used yet", RulesText.StatusLine(new Rule { Id = "r", Name = "" }, null));
        Assert.Equal("Used on 3 messages", RulesText.StatusLine(new Rule { Id = "r", Name = "", Applied = 3 }, _ => "x"));
        Assert.Equal(
            "Used on 1 message, last " + new DateTimeOffset(2026, 11, 2, 8, 0, 0, TimeSpan.Zero).ToString("d", CultureInfo.InvariantCulture),
            RulesText.StatusLine(
                new Rule { Id = "r", Name = "", Applied = 1, LastAppliedAt = new DateTimeOffset(2026, 11, 2, 8, 0, 0, TimeSpan.Zero) },
                t => t.ToString("d", CultureInfo.InvariantCulture)));
    }

    [Fact]
    public void EveryTextOfTheEditorIsAMsgid()
    {
        // Every function that picks a msgid answers a sentence, never the empty string, for its documented inputs.
        foreach (var f in RuleBook.Fields)
        {
            Assert.NotEqual("", RulesText.FieldText(f));
            foreach (var op in RuleBook.OperatorsFor(f))
            {
                Assert.NotEqual("", RulesText.OperatorText(f, op));
            }
        }
        foreach (var k in RuleBook.ActionKinds)
        {
            Assert.NotEqual("", RulesText.ActionText(k));
            Assert.NotEqual("", RulesText.OutgoingHint(k));
        }
        foreach (var kind in Enum.GetValues<RuleFormProblemKind>())
        {
            Assert.NotEqual("", RulesText.ProblemText(new RuleFormProblem("x", 0, kind)));
        }
        foreach (var field in new[] { "name", "accountId", "confirmOutgoing", "conditions", "actions", "other" })
        {
            Assert.NotEqual("", RulesText.InvalidText(new RuleInvalidData { Field = field }));
        }
        Assert.Equal("any kind", RulesText.ValueText(RuleField.Bulk, RuleValues.BulkAny));
        Assert.Equal("none", RulesText.ValueText(RuleField.HasAttachments, RuleBook.ValueFalse));
        Assert.Equal("present", RulesText.ValueText(RuleField.HasAttachments, RuleBook.ValueTrue));
        Assert.Equal("x", RulesText.ValueText(RuleField.Subject, "x"));
        Assert.Equal("example.com", RulesText.ValuePlaceholder(RuleField.From, RuleOperator.DomainIs));
        Assert.Equal("Name or address", RulesText.ValuePlaceholder(RuleField.To, RuleOperator.Contains));
        Assert.Equal("list.example.com", RulesText.ValuePlaceholder(RuleField.ListId, RuleOperator.Contains));
        Assert.Equal("Text", RulesText.ValuePlaceholder(RuleField.Subject, RuleOperator.Contains));
        Assert.Equal("Attachments are present", RulesText.ConditionText(
            new RuleCondition { Field = RuleField.HasAttachments, Op = RuleOperator.Is, Value = RuleBook.ValueTrue }));
        Assert.Equal("A rule can have at most 20 conditions.", RulesText.ProblemText(new RuleFormProblem("conditions", 0, RuleFormProblemKind.TooManyConditions)));
        Assert.Equal("A rule can have at most 10 actions.", RulesText.ProblemText(new RuleFormProblem("actions", 0, RuleFormProblemKind.TooManyActions)));
        Assert.Equal("No message in this folder matches.", RulesText.TestResultText(0));
        Assert.Equal("1 message in this folder matches.", RulesText.TestResultText(1));
        Assert.Equal("3 messages in this folder match.", RulesText.TestResultText(3));
        Assert.Equal("New Rule", RulesText.EditorTitle(new RuleForm()));
        Assert.Equal("Edit Rule", RulesText.EditorTitle(new RuleForm { Id = "r_1" }));
        Assert.Equal("and any of these conditions is met", RulesText.MatchText(RuleMatch.Any));
        Assert.Equal("and all of these conditions are met", RulesText.MatchText(RuleMatch.All));
        Assert.True(RulesText.DeleteQuestion("x").Destructive);
        Assert.Equal("Apply Rules to “Inbox”?", RulesText.ApplyQuestion("Inbox").Heading);
    }
}
