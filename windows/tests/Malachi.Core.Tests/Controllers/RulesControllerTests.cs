// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/RulesControllerTests.swift (written
// at the same time); GTK: the flow at the top of ui/internal/rules/rules.go.
//
// The application's rules controller against a fake daemon: the list and
// its reload, the switch of a row with the outgoing question, a reorder
// that is put back, Remove with its question, Apply Rules… with its toast,
// and the sentences of the failures. The process-wide catalogue is English,
// so every msgid is its own translation.

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

public sealed class RulesControllerTests
{
    private static readonly RuleAction Forward = new() { Kind = RuleActionKind.Forward, To = "boss@acme.cz" };

    private static (RulesController Controller, FakeRulesCaller Daemon) Standard()
    {
        var daemon = new FakeRulesCaller();
        daemon.Answer(
            API.RulesList.Name,
            new RulesListResult { Rules = [Sample("r_1", "One"), Sample("r_2", "Two", enabled: false, actions: [Forward]), Sample("r_3", "Three")] });
        return (new RulesController(daemon), daemon);
    }

    private static string Ids(RulesController c) => string.Join(' ', c.Rules.Select(r => r.Id));

    [Fact]
    public async Task ReloadListsTheRulesAndTellsTheViews()
    {
        var (c, daemon) = Standard();
        var changes = 0;
        c.Changed += (_, _) => changes++;
        Assert.False(c.Loaded);
        await c.ReloadAsync();
        Assert.True(c.Loaded);
        Assert.Equal("r_1 r_2 r_3", Ids(c));
        Assert.Equal(1, changes);
        Assert.Equal("", c.Error);
        Assert.Equal("Two", c.Find("r_2")!.Name);
        Assert.Null(c.Find("r_9"));

        // A failure keeps the rules and says why.
        daemon.Fail(API.RulesList.Name, Error(ErrorCode.Unavailable));
        await c.ReloadAsync();
        Assert.Equal("r_1 r_2 r_3", Ids(c));
        Assert.NotEqual("", c.Error);
        Assert.Equal(2, changes);
    }

    [Fact]
    public async Task AppliesToAnAccountOnlyWithAnEnabledRuleForIt()
    {
        var (c, _) = Standard();
        await c.ReloadAsync();
        Assert.True(c.AppliesTo("acc_1")); // r_1 is for every account and on
        var daemon = new FakeRulesCaller();
        daemon.Answer(
            API.RulesList.Name,
            new RulesListResult { Rules = [Sample("r_1", account: "acc_1", enabled: false), Sample("r_2", account: "acc_2")] });
        var only = new RulesController(daemon);
        await only.ReloadAsync();
        Assert.False(only.AppliesTo("acc_1"));
        Assert.True(only.AppliesTo("acc_2"));
        Assert.False(only.AppliesTo("acc_3"));
    }

    [Fact]
    public async Task TheSwitchFlipsEnabledWithoutAQuestionForALocalRule()
    {
        var (c, daemon) = Standard();
        await c.ReloadAsync();
        daemon.On(API.RulesUpdate.Name, raw => new RulesUpdateResult { Rule = Sample("r_1", "One", enabled: false) });
        var asked = 0;
        var result = await c.SetEnabledAsync("r_1", false, _ =>
        {
            asked++;
            return Task.FromResult(true);
        });
        Assert.True(result.Ok);
        Assert.Equal(0, asked);
        var sent = daemon.Last(API.RulesUpdate);
        Assert.Equal("r_1", sent.Id);
        Assert.False(sent.Rule.Enabled);
        Assert.Null(sent.ConfirmOutgoing);
        Assert.False(c.Find("r_1")!.Enabled);
    }

    [Fact]
    public async Task TheSwitchOfAnOutgoingRuleAsksFirst()
    {
        var (c, daemon) = Standard();
        await c.ReloadAsync();
        daemon.On(API.RulesUpdate.Name, raw => new RulesUpdateResult { Rule = Sample("r_2", "Two", actions: [Forward]) });
        RuleQuestion? question = null;

        var declined = await c.SetEnabledAsync("r_2", true, q =>
        {
            question = q;
            return Task.FromResult(false);
        });
        Assert.Equal(RuleResultKind.Declined, declined.Kind);
        Assert.Equal(0, daemon.Count(API.RulesUpdate.Name));
        Assert.Equal("Send Mail Automatically?", question!.Heading);
        Assert.Contains("boss@acme.cz", question.Body, StringComparison.Ordinal);
        Assert.Equal("Turn On", question.Confirm);
        Assert.False(c.Find("r_2")!.Enabled);

        var done = await c.SetEnabledAsync("r_2", true, _ => Task.FromResult(true));
        Assert.True(done.Ok);
        var sent = daemon.Last(API.RulesUpdate);
        Assert.True(sent.Rule.Enabled);
        Assert.True(sent.ConfirmOutgoing);
        Assert.True(c.Find("r_2")!.Enabled);

        // Turning it off needs no question.
        daemon.On(API.RulesUpdate.Name, raw => new RulesUpdateResult { Rule = Sample("r_2", "Two", enabled: false, actions: [Forward]) });
        var off = await c.SetEnabledAsync("r_2", false, _ => throw new InvalidOperationException("no question expected"));
        Assert.True(off.Ok);
        Assert.Null(daemon.Last(API.RulesUpdate).ConfirmOutgoing);
    }

    [Fact]
    public async Task AQuickOnAndOffIsDecidedOnWhatTheEarlierFlipLeft()
    {
        var (c, daemon) = Standard();
        await c.ReloadAsync();
        // r_2 sends mail and is off: turning it on asks the question; the user is slow to answer.
        daemon.On(API.RulesUpdate.Name, raw => new RulesUpdateResult
        {
            Rule = Sample("r_2", "Two", enabled: raw.GetProperty("rule").GetProperty("enabled").GetBoolean(), actions: [Forward]),
        });
        var answer = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        var on = c.SetEnabledAsync("r_2", true, _ => answer.Task);
        var off = c.SetEnabledAsync("r_2", false, _ => throw new InvalidOperationException("turning off asks nothing"));
        Assert.False(off.IsCompleted);
        Assert.Equal(0, daemon.Count(API.RulesUpdate.Name));
        answer.SetResult(true);
        Assert.True((await on).Ok);
        Assert.True((await off).Ok);
        // On first, then off: the second flip saw the rule on, so it was sent.
        Assert.Equal(2, daemon.Count(API.RulesUpdate.Name));
        Assert.False(daemon.Last(API.RulesUpdate).Rule.Enabled);
        Assert.False(c.Find("r_2")!.Enabled);
    }

    [Fact]
    public void AnInvalidArgumentIsNeverTheDaemonsMessage()
    {
        var rpc = new RpcException(Error(ErrorCode.InvalidArgument));
        var text = RulesController.FailureText("Remove Rule", rpc);
        Assert.Equal("Remove Rule failed", text);
        Assert.DoesNotContain("fake", text, StringComparison.Ordinal);
        Assert.Equal("Remove Rule timed out", RulesController.FailureText("Remove Rule", new TimeoutException()));
    }

    [Fact]
    public async Task AFailedSwitchSaysWhyAndKeepsTheRule()
    {
        var (c, daemon) = Standard();
        await c.ReloadAsync();
        daemon.Fail(API.RulesUpdate.Name, Error(ErrorCode.ServerError));
        var result = await c.SetEnabledAsync("r_1", false, _ => Task.FromResult(true));
        Assert.Equal(RuleResultKind.Failed, result.Kind);
        Assert.Equal("The rule could not be saved.", result.Text);
        Assert.True(c.Find("r_1")!.Enabled);

        // A rule that is not in the list is not found.
        var missing = await c.SetEnabledAsync("r_9", true, _ => Task.FromResult(true));
        Assert.Equal(RuleResultKind.Failed, missing.Kind);
        Assert.Equal("This rule no longer exists.", missing.Text);
    }

    [Fact]
    public async Task ADragIsShownAtOnceAndSentAsTheWholeOrder()
    {
        var (c, daemon) = Standard();
        await c.ReloadAsync();
        daemon.On(API.RulesReorder.Name, raw => new RulesReorderResult
        {
            Rules = [Sample("r_2", "Two"), Sample("r_3", "Three"), Sample("r_1", "One")],
        });
        var result = await c.MoveAsync(0, 2);
        Assert.True(result.Ok);
        Assert.Equal(["r_2", "r_3", "r_1"], daemon.Last(API.RulesReorder).Ids);
        Assert.Equal("r_2 r_3 r_1", Ids(c));

        // Dropped where it was: nothing is sent.
        var before = daemon.Count(API.RulesReorder.Name);
        Assert.True((await c.MoveAsync(1, 1)).Ok);
        Assert.Equal(before, daemon.Count(API.RulesReorder.Name));
    }

    [Fact]
    public async Task ARefusedReorderIsPutBack()
    {
        var (c, daemon) = Standard();
        await c.ReloadAsync();
        daemon.Fail(API.RulesReorder.Name, Error(ErrorCode.InvalidArgument));
        var shown = new List<string>();
        c.Changed += (_, _) => shown.Add(Ids(c));
        var result = await c.MoveAsync(2, 0);
        Assert.Equal(RuleResultKind.Failed, result.Kind);
        Assert.Equal("r_1 r_2 r_3", Ids(c));
        // Shown moved first, then put back.
        Assert.Equal("r_3 r_1 r_2", shown[0]);
        Assert.Equal("r_1 r_2 r_3", shown[^1]);
    }

    [Fact]
    public async Task RemoveAsksFirstAndDeletes()
    {
        var (c, daemon) = Standard();
        await c.ReloadAsync();
        daemon.Answer(API.RulesDelete.Name, new EmptyResult());
        RuleQuestion? question = null;

        var declined = await c.DeleteAsync("r_1", q =>
        {
            question = q;
            return Task.FromResult(false);
        });
        Assert.Equal(RuleResultKind.Declined, declined.Kind);
        Assert.Equal(0, daemon.Count(API.RulesDelete.Name));
        Assert.Equal("Remove the Rule “One”?", question!.Heading);
        Assert.Equal("Remove", question.Confirm);
        Assert.True(question.Destructive);

        var done = await c.DeleteAsync("r_1", _ => Task.FromResult(true));
        Assert.True(done.Ok);
        Assert.Equal("r_1", daemon.Last(API.RulesDelete).Id);
        Assert.Equal("r_2 r_3", Ids(c));
    }

    [Fact]
    public async Task ARuleAnotherClientRemovedIsNotFoundAndGoneFromTheList()
    {
        var (c, daemon) = Standard();
        await c.ReloadAsync();
        daemon.Fail(API.RulesDelete.Name, Error(ErrorCode.RuleNotFound));
        daemon.Answer(API.RulesList.Name, new RulesListResult { Rules = [Sample("r_2", "Two"), Sample("r_3", "Three")] });
        var result = await c.DeleteAsync("r_1", _ => Task.FromResult(true));
        Assert.Equal(RuleResultKind.Failed, result.Kind);
        Assert.Equal("This rule no longer exists.", result.Text);
        Assert.Equal("r_2 r_3", Ids(c));
    }

    [Fact]
    public async Task ApplyAsksThenRunsOnTheFolderAndAnswersTheToast()
    {
        var (c, daemon) = Standard();
        daemon.Answer(API.RulesApply.Name, new RulesApplyResult { Examined = 120, Matched = 14, Changed = 12, SkippedOutgoing = 2 });
        RuleQuestion? question = null;

        var declined = await c.ApplyAsync("acc_1", "f_inbox", "Inbox", q =>
        {
            question = q;
            return Task.FromResult(false);
        });
        Assert.Equal(RuleResultKind.Declined, declined.Kind);
        Assert.Equal(0, daemon.Count(API.RulesApply.Name));
        Assert.Equal("Apply Rules to “Inbox”?", question!.Heading);
        Assert.Equal("Apply", question.Confirm);

        var done = await c.ApplyAsync("acc_1", "f_inbox", "Inbox", _ => Task.FromResult(true));
        Assert.True(done.Ok);
        Assert.Equal("The rules changed 12 messages.", done.Text);
        var sent = daemon.Last(API.RulesApply);
        Assert.Equal("acc_1", sent.AccountId.Value);
        Assert.Equal("f_inbox", sent.FolderId!.Value.Value);
        Assert.Empty(sent.MessageIds);
        Assert.Empty(sent.RuleIds);

        daemon.Answer(API.RulesApply.Name, new RulesApplyResult { Examined = 5 });
        Assert.Equal("The rules changed no message.", (await c.ApplyAsync("acc_1", "f_inbox", "Inbox", _ => Task.FromResult(true))).Text);
    }

    [Fact]
    public async Task ApplyFailuresAreSentences()
    {
        var (c, daemon) = Standard();
        daemon.Fail(API.RulesApply.Name, Error(ErrorCode.StorageError));
        var failed = await c.ApplyAsync("acc_1", "f_inbox", "Inbox", _ => Task.FromResult(true));
        Assert.Equal(RuleResultKind.Failed, failed.Kind);
        Assert.Equal("The rules could not be applied.", failed.Text);

        daemon.Fail(API.RulesApply.Name, Error(ErrorCode.RuleNotFound));
        daemon.Answer(API.RulesList.Name, new RulesListResult());
        Assert.Equal("This rule no longer exists.", (await c.ApplyAsync("acc_1", "f_inbox", "Inbox", _ => Task.FromResult(true))).Text);
    }

    [Fact]
    public void ErrorTextNamesWhatTheDaemonRefused()
    {
        var rpc = new RpcException(Error(ErrorCode.InvalidArgument, new RuleInvalidData { Field = "actions", Index = 1 }));
        Assert.Equal("An action of this rule cannot be used.", RulesController.ErrorText(rpc, "fallback"));
        rpc = new RpcException(Error(ErrorCode.InvalidArgument, new RuleInvalidData { Field = "confirmOutgoing" }));
        Assert.Equal("Rules that send mail need your confirmation.", RulesController.ErrorText(rpc, "fallback"));
        rpc = new RpcException(Error(ErrorCode.InvalidArgument));
        Assert.Equal("fallback", RulesController.ErrorText(rpc, "fallback"));
        rpc = new RpcException(Error(ErrorCode.RuleNotFound));
        Assert.Equal("This rule no longer exists.", RulesController.ErrorText(rpc, "fallback"));
        Assert.Equal("fallback", RulesController.ErrorText(new InvalidOperationException("x"), "fallback"));
    }

    [Fact]
    public async Task AnEditorsRuleJoinsTheListAtOnce()
    {
        var (c, _) = Standard();
        await c.ReloadAsync();
        c.Upsert(Sample("r_1", "Renamed"));
        c.Upsert(Sample("r_4", "Four"));
        Assert.Equal("r_1 r_2 r_3 r_4", Ids(c));
        Assert.Equal("Renamed", c.Find("r_1")!.Name);
    }
}
