// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/RuleEditorControllerTests.swift
// (written at the same time); GTK: the editor's flow at the top of
// ui/internal/rules/rules.go.
//
// The rule editor's controller against a fake daemon: the edits of the rows,
// the problems that keep Save disabled, Save with the outgoing question and
// the daemon's refusals, and Try It over the folder chosen. The
// process-wide catalogue is English, so every msgid is its own translation.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Rules;
using Malachi.Core.Transport;
using Xunit;
using static Malachi.Core.Tests.Controllers.FakeRulesCaller;

namespace Malachi.Core.Tests.Controllers;

public sealed class RuleEditorControllerTests
{
    private static Account Mail(string id, string email) => new()
    {
        Id = id,
        Enabled = true,
        Config = new AccountConfig { Name = email, Email = email },
        State = new SyncState { AccountId = id, Status = SyncStatus.Idle },
    };

    private static Folder MakeFolder(string account, string id, string role = FolderRole.None, bool selectable = true) => new()
    {
        Id = id,
        AccountId = account,
        Name = id,
        Path = id,
        Role = role,
        Subscribed = true,
        Selectable = selectable,
        Synced = true,
        Unread = 0,
        Total = 0,
    };

    private static IReadOnlyList<Folder> FoldersOf(AccountId account) => account.Value switch
    {
        "acc_1" => [MakeFolder("acc_1", "f_in", FolderRole.Inbox), MakeFolder("acc_1", "f_news"), MakeFolder("acc_1", "f_out", FolderRole.Outbox)],
        "acc_2" => [MakeFolder("acc_2", "g_in", FolderRole.Inbox), MakeFolder("acc_2", "g_all", FolderRole.All)],
        _ => [],
    };

    private static RuleEditorController Editor(FakeRulesCaller daemon, RuleForm form, Rule? original = null) =>
        new(daemon, form, original, [Mail("acc_1", "one@example.test"), Mail("acc_2", "two@example.test")], FoldersOf);

    private static RuleEditorController NewRule(FakeRulesCaller daemon, AccountId? account = null) => Editor(daemon, RuleBook.NewForm(account));

    private static void Fill(RuleEditorController e)
    {
        e.SetName("  Acme  ");
        e.SetConditionValue(0, " acme.cz ");
        e.AddAction(RuleActionKind.Flag);
    }

    [Fact]
    public void ANewRuleHasProblemsUntilItIsFilledIn()
    {
        var e = NewRule(new FakeRulesCaller());
        Assert.False(e.CanSave);
        Assert.Equal("New Rule", e.Title);
        Assert.Equal("Enter a name for the rule.", e.ProblemText("name"));
        Assert.Equal("Enter a value.", e.ProblemText("conditions", 0));
        Assert.Equal("Add at least one action.", e.GroupProblemText("actions"));
        Assert.Equal("", e.ProblemText("actions"));
        Assert.Equal("", e.GroupProblemText("conditions"));
        Assert.Equal("", e.ProblemText("conditions", 3));
        Fill(e);
        Assert.True(e.CanSave);
        Assert.Empty(e.Problems);
    }

    [Fact]
    public void EveryEditRaisesChangedAndClearsTheLastFailure()
    {
        var e = NewRule(new FakeRulesCaller());
        var changes = 0;
        e.Changed += (_, _) => changes++;
        e.SetName("x");
        e.SetEnabled(false);
        e.SetMatch(RuleMatch.Any);
        e.SetStop(true);
        e.AddCondition();
        e.SetConditionOperator(1, RuleOperator.EndsWith);
        e.AddAction(RuleActionKind.MarkRead);
        Assert.Equal(7, changes);
        Assert.False(e.Form.Enabled);
        Assert.Equal(RuleMatch.Any, e.Form.Match.Value);
        Assert.True(e.Form.Stop);
        Assert.Equal(2, e.Form.Conditions.Count);
        Assert.Equal(RuleOperator.EndsWith, e.Form.Conditions[1].Op.Value);
        e.RemoveCondition(1);
        e.RemoveAction(0);
        Assert.Single(e.Form.Conditions);
        Assert.Empty(e.Form.Actions);
        // Rows that are not there are left alone.
        e.RemoveCondition(5);
        e.SetConditionValue(9, "x");
        Assert.Equal(9, changes);
    }

    [Fact]
    public void AChangedFieldKeepsWhatTheNewFieldTakes()
    {
        var e = NewRule(new FakeRulesCaller());
        e.SetConditionOperator(0, RuleOperator.DomainIs);
        e.SetConditionValue(0, "acme.cz");
        e.SetConditionField(0, RuleField.Subject);
        Assert.Equal(new RuleCondition { Field = "subject", Op = "contains", Value = "acme.cz" }, e.Form.Conditions[0]);
        e.SetConditionField(0, RuleField.Bulk);
        Assert.Equal(new RuleCondition { Field = "bulk", Op = "is", Value = "any" }, e.Form.Conditions[0]);
    }

    [Fact]
    public void ConditionsAndActionsStopAtTheirLimits()
    {
        var e = NewRule(new FakeRulesCaller(), "acc_1");
        for (var i = 0; i < 30; i++)
        {
            e.AddCondition();
        }
        Assert.Equal(RuleLimits.MaxRuleConditions, e.Form.Conditions.Count);
        // Each kind once, one place; the kinds the form has are not offered again.
        e.AddAction(RuleActionKind.Move);
        e.AddAction(RuleActionKind.Trash);
        e.AddAction(RuleActionKind.Flag);
        e.AddAction(RuleActionKind.Flag);
        Assert.Equal(["move", "flag"], e.Form.Actions.Select(a => a.Kind.Value));
    }

    [Fact]
    public void MoveToOffersTheFoldersOfTheRulesAccount()
    {
        var e = NewRule(new FakeRulesCaller());
        Assert.Empty(e.MoveFolders); // every account: no Move To
        e.SetAccount("acc_1");
        Assert.Equal(["f_in", "f_news"], e.MoveFolders.Select(f => f.Id.Value));
        e.AddAction(RuleActionKind.Move);
        e.SetMoveFolder(0, "f_news");
        Assert.Equal("f_news", e.Form.Actions[0].FolderId!.Value.Value);
        // Another account: the folder belonged to the first one.
        e.SetAccount("acc_2");
        Assert.Null(e.Form.Actions[0].FolderId);
        Assert.Equal("Choose a folder.", e.ProblemText("actions", 0));
        // Every account: the action goes.
        e.SetAccount(null);
        Assert.Empty(e.Form.Actions);
        // The rows that are of another kind are not changed by a call for a Move To row.
        e.AddAction(RuleActionKind.Flag);
        e.SetMoveFolder(0, "f_in");
        e.SetForwardTo(0, "x@y.cz");
        Assert.Equal(new RuleAction { Kind = RuleActionKind.Flag }, e.Form.Actions[0]);
    }

    [Fact]
    public async Task SaveCreatesTheTrimmedRule()
    {
        var daemon = new FakeRulesCaller();
        daemon.On(API.RulesCreate.Name, raw => new RulesCreateResult { Rule = Sample("r_9", "Acme", actions: [new RuleAction { Kind = RuleActionKind.Flag }]) });
        var e = NewRule(daemon);
        Fill(e);
        var result = await e.SaveAsync(_ => throw new InvalidOperationException("no question expected"));
        Assert.True(result.Ok);
        Assert.Equal("r_9", e.Saved!.Id);
        var sent = daemon.Last(API.RulesCreate);
        Assert.Equal("Acme", sent.Rule.Name);
        Assert.Equal("acme.cz", sent.Rule.Conditions[0].Value);
        Assert.Null(sent.ConfirmOutgoing);
        Assert.False(e.Saving);
    }

    [Fact]
    public async Task SaveOfAnInvalidFormSendsNothing()
    {
        var daemon = new FakeRulesCaller();
        var e = NewRule(daemon);
        var result = await e.SaveAsync(_ => Task.FromResult(true));
        Assert.Equal(RuleResultKind.Failed, result.Kind);
        Assert.Equal(0, daemon.Count(API.RulesCreate.Name));
    }

    [Fact]
    public async Task AnOutgoingRuleIsConfirmedBeforeItIsSaved()
    {
        var daemon = new FakeRulesCaller();
        var forward = new RuleAction { Kind = RuleActionKind.Forward, To = "boss@acme.cz" };
        daemon.On(API.RulesCreate.Name, raw => new RulesCreateResult { Rule = Sample("r_9", "Acme", actions: [forward]) });
        var e = NewRule(daemon);
        Fill(e);
        e.AddAction(RuleActionKind.Forward);
        e.SetForwardTo(1, " boss@acme.cz ");
        Assert.True(e.CanSave);
        RuleQuestion? question = null;

        var declined = await e.SaveAsync(q =>
        {
            question = q;
            return Task.FromResult(false);
        });
        Assert.Equal(RuleResultKind.Declined, declined.Kind);
        Assert.Equal(0, daemon.Count(API.RulesCreate.Name));
        Assert.Equal("Send Mail Automatically?", question!.Heading);
        Assert.Contains("boss@acme.cz", question.Body, StringComparison.Ordinal);

        var done = await e.SaveAsync(_ => Task.FromResult(true));
        Assert.True(done.Ok);
        var sent = daemon.Last(API.RulesCreate);
        Assert.True(sent.ConfirmOutgoing);
        Assert.Equal("boss@acme.cz", sent.Rule.Actions[1].To);
    }

    [Fact]
    public async Task EditingAStoredRuleUpdatesItAndAsksOnlyForAChangedOutgoingAction()
    {
        var daemon = new FakeRulesCaller();
        var forward = new RuleAction { Kind = RuleActionKind.Forward, To = "boss@acme.cz" };
        var stored = Sample("r_5", "Acme", actions: [forward]);
        daemon.On(API.RulesUpdate.Name, raw => new RulesUpdateResult { Rule = stored });
        var e = Editor(daemon, RuleBook.FromRule(stored), stored);
        Assert.Equal("Edit Rule", e.Title);

        // A rename needs no question.
        e.SetName("Renamed");
        var done = await e.SaveAsync(_ => throw new InvalidOperationException("no question expected"));
        Assert.True(done.Ok);
        var sent = daemon.Last(API.RulesUpdate);
        Assert.Equal("r_5", sent.Id);
        Assert.Equal("Renamed", sent.Rule.Name);
        Assert.Null(sent.ConfirmOutgoing);

        // Another address does.
        e.SetForwardTo(0, "other@acme.cz");
        var asked = 0;
        Assert.True((await e.SaveAsync(_ =>
        {
            asked++;
            return Task.FromResult(true);
        })).Ok);
        Assert.Equal(1, asked);
        Assert.True(daemon.Last(API.RulesUpdate).ConfirmOutgoing);
    }

    [Fact]
    public async Task AFailedSaveSaysWhyUnderTheForm()
    {
        var daemon = new FakeRulesCaller();
        var e = NewRule(daemon);
        Fill(e);

        daemon.Fail(API.RulesCreate.Name, Error(ErrorCode.InvalidArgument, new RuleInvalidData { Field = "accountId" }));
        var result = await e.SaveAsync(_ => Task.FromResult(true));
        Assert.Equal(RuleResultKind.Failed, result.Kind);
        Assert.Equal("The account of this rule no longer exists.", result.Text);
        Assert.Equal(result.Text, e.SaveError);
        Assert.Null(e.Saved);
        Assert.False(e.Saving);

        daemon.Fail(API.RulesCreate.Name, Error(ErrorCode.StorageError));
        Assert.Equal("The rule could not be saved.", (await e.SaveAsync(_ => Task.FromResult(true))).Text);

        // An edit clears the sentence.
        e.SetName("Other");
        Assert.Equal("", e.SaveError);

        daemon.Fail(API.RulesUpdate.Name, Error(ErrorCode.RuleNotFound));
        var stored = Sample("r_5");
        var existing = Editor(daemon, RuleBook.FromRule(stored), stored);
        Assert.Equal("This rule no longer exists.", (await existing.SaveAsync(_ => Task.FromResult(true))).Text);
    }

    [Fact]
    public async Task TryItLooksInTheInboxOfTheRulesAccountByDefault()
    {
        var daemon = new FakeRulesCaller();
        daemon.Answer(API.RulesTest.Name, new RulesTestResult { Count = 3 });
        var e = NewRule(daemon);
        Fill(e);
        // Every account: the first account's inbox.
        Assert.Equal("acc_1", e.TestAccountId!.Value.Value);
        Assert.Equal("f_in", e.TestFolderId!.Value.Value);
        Assert.Equal(["f_in", "f_news"], e.TestFolders.Select(f => f.Id.Value)); // not the outbox
        await e.TestAsync();
        var sent = daemon.Last(API.RulesTest);
        Assert.Equal("acc_1", sent.AccountId.Value);
        Assert.Equal("f_in", sent.FolderId.Value);
        Assert.Equal("3 messages in this folder match.", e.TestText);
        Assert.False(e.TestFailed);
        Assert.False(e.Testing);

        // The rule's account and a folder chosen.
        e.SetAccount("acc_2");
        Assert.Equal("g_in", e.TestFolderId!.Value.Value);
        e.SetTestFolder("g_all");
        Assert.Equal("g_all", e.TestFolderId!.Value.Value);
        Assert.Equal("", e.TestText); // the edit made the result stale
        await e.TestAsync();
        Assert.Equal("acc_2", daemon.Last(API.RulesTest).AccountId.Value);
        // A folder of another account is no choice.
        e.SetAccount("acc_1");
        Assert.Equal("f_in", e.TestFolderId!.Value.Value);
    }

    [Fact]
    public async Task TryItAsksOnlyForAFormWithoutProblemsAndSaysWhyItFailed()
    {
        var daemon = new FakeRulesCaller();
        var e = NewRule(daemon);
        Assert.False(e.CanTest);
        await e.TestAsync();
        Assert.Equal(0, daemon.Count(API.RulesTest.Name));

        Fill(e);
        Assert.True(e.CanTest);
        daemon.Fail(API.RulesTest.Name, Error(ErrorCode.InvalidArgument, new RuleInvalidData { Field = "conditions" }));
        await e.TestAsync();
        Assert.True(e.TestFailed);
        Assert.Equal("A condition of this rule cannot be used.", e.TestText);

        daemon.Fail(API.RulesTest.Name, Error(ErrorCode.FolderNotFound));
        await e.TestAsync();
        Assert.True(e.TestFailed);
        Assert.NotEqual("", e.TestText);

        daemon.Answer(API.RulesTest.Name, new RulesTestResult { Count = 0 });
        await e.TestAsync();
        Assert.False(e.TestFailed);
        Assert.Equal("No message in this folder matches.", e.TestText);
    }
}
