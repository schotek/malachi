// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/RulesSamples.swift (written
// at the same time); GTK: the sample caller of the rules (docs/plans/rules.md
// T2), after ui/internal/contactsctl samples.go.
//
// DEVELOPMENT AID: the invented rules of MALACHI_RULES_SAMPLES=1 (like
// MALACHI_CONTACTS_SAMPLES): an in-memory daemon that answers the rules
// methods, so that the Rules page and the rule editor can be seen and
// clicked through without a daemon that has the rules (the dev start steps
// prefs-rules and rules-editor), and the UI smoke tests have something to
// show. Writes change the memory only and raise Notified as the daemon's
// notify.rulesChanged. Every name, address and number is invented. The
// rules refer to every account, but one that moves to the folder of an
// account that is not there (the page's "Removed account" and "missing
// folder"). Other methods answer methodNotFound.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Rules;
using Malachi.Core.Transport;

namespace Malachi.Core.Controllers;

/// <summary>The in-memory daemon of the rules samples; safe for calls from any thread.</summary>
public sealed class RulesSampleCaller : IRulesCaller
{
    /// <summary>The variable that turns the samples on ("1").</summary>
    public const string SamplesEnv = "MALACHI_RULES_SAMPLES";

    private readonly Lock gate = new();
    private readonly List<Rule> rules;
    private readonly DateTimeOffset now;
    private int next;

    /// <summary>The sample daemon as of <paramref name="now"/>.</summary>
    public RulesSampleCaller(DateTimeOffset now)
    {
        this.now = now;
        // Windows-only strings: the invented rules, never translated.
        rules =
        [
            Sample(
                "Newsletters",
                [new RuleCondition { Field = RuleField.Bulk, Op = RuleOperator.Is, Value = BulkKind.Newsletter }],
                [new RuleAction { Kind = RuleActionKind.MarkRead }],
                applied: 42,
                last: now.AddHours(-3)),
            Sample(
                "Invoices from Acme",
                [
                    new RuleCondition { Field = RuleField.From, Op = RuleOperator.DomainIs, Value = "acme.example" },
                    new RuleCondition { Field = RuleField.Subject, Op = RuleOperator.Contains, Value = "invoice" },
                ],
                [new RuleAction { Kind = RuleActionKind.Flag }, new RuleAction { Kind = RuleActionKind.Forward, To = "accounting@example.test" }],
                match: new RuleMatch(RuleMatch.Any),
                outgoing: true,
                applied: 7,
                last: now.AddDays(-1),
                skipped: 2),
            Sample(
                "Away",
                [new RuleCondition { Field = RuleField.To, Op = RuleOperator.Is, Value = "me@example.test" }],
                [new RuleAction { Kind = RuleActionKind.AutoReply, Subject = "Out of office", Text = "I am away until Monday." }],
                enabled: false,
                outgoing: true,
                stop: true),
            Sample(
                "Project Atlas",
                [new RuleCondition { Field = RuleField.ListId, Op = RuleOperator.Is, Value = "atlas.lists.example.test" }],
                [new RuleAction { Kind = RuleActionKind.Move, FolderId = new FolderId("f_gone") }],
                account: new AccountId("acc_gone"),
                enabled: false,
                problem: new RuleProblem(RuleProblem.FolderGone)),
        ];
    }

    /// <summary>The samples' writes, as the daemon's notify.rulesChanged.</summary>
    public event EventHandler? Notified;

    /// <summary>MALACHI_RULES_SAMPLES=1.</summary>
    public static bool SamplesOn => Environment.GetEnvironmentVariable(SamplesEnv) == "1";

    /// <inheritdoc/>
    public Task<TResult> CallAsync<TParams, TResult>(
        RpcMethod<TParams, TResult> method, TParams parameters, TimeSpan timeout, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(method);
        cancellationToken.ThrowIfCancellationRequested();
        object result;
        bool changed;
        lock (gate)
        {
            (result, changed) = Answer(method.Name, parameters);
        }
        if (changed)
        {
            Notified?.Invoke(this, EventArgs.Empty);
        }
        return Task.FromResult((TResult)result);
    }

    private static RpcException Fail(int code, string message) => new(new RpcError { Code = code, Message = message });

    private Rule Sample(
        string name,
        IReadOnlyList<RuleCondition> conditions,
        IReadOnlyList<RuleAction> actions,
        RuleMatch? match = null,
        AccountId? account = null,
        bool enabled = true,
        bool stop = false,
        bool outgoing = false,
        RuleProblem? problem = null,
        int applied = 0,
        DateTimeOffset? last = null,
        int skipped = 0)
    {
        next++;
        return new Rule
        {
            Id = "r_sample" + next.ToString(CultureInfo.InvariantCulture),
            Name = name,
            Enabled = enabled,
            AccountId = account,
            Match = match ?? RuleMatch.All,
            Conditions = conditions,
            Actions = actions,
            Stop = stop,
            Outgoing = outgoing,
            Problem = problem,
            CreatedAt = now.AddDays(-30),
            UpdatedAt = now.AddDays(-2),
            Applied = applied,
            LastAppliedAt = last,
            Skipped = skipped,
        };
    }

    private int IndexOf(string id)
    {
        var i = rules.FindIndex(r => r.Id == id);
        return i >= 0 ? i : throw Fail(ErrorCode.RuleNotFound, "no rule " + id);
    }

    private (object Result, bool Changed) Answer(string method, object? parameters)
    {
        switch (method)
        {
            case "rules.list":
                return (new RulesListResult { Rules = [.. rules] }, false);
            case "rules.create" when parameters is RulesCreateParams create:
                {
                    var made = Sample(create.Rule.Name, create.Rule.Conditions, create.Rule.Actions, create.Rule.Match, create.Rule.AccountId, create.Rule.Enabled, create.Rule.Stop);
                    made = made with { Outgoing = RuleBook.IsOutgoing(create.Rule) };
                    rules.Add(made);
                    return (new RulesCreateResult { Rule = made }, true);
                }
            case "rules.update" when parameters is RulesUpdateParams update:
                {
                    var i = IndexOf(update.Id);
                    var kept = rules[i];
                    rules[i] = new Rule
                    {
                        Id = kept.Id,
                        Name = update.Rule.Name,
                        Enabled = update.Rule.Enabled,
                        AccountId = update.Rule.AccountId,
                        Match = update.Rule.Match,
                        Conditions = update.Rule.Conditions,
                        Actions = update.Rule.Actions,
                        Stop = update.Rule.Stop,
                        Outgoing = RuleBook.IsOutgoing(update.Rule),
                        CreatedAt = kept.CreatedAt,
                        UpdatedAt = now,
                        Applied = kept.Applied,
                        LastAppliedAt = kept.LastAppliedAt,
                        Skipped = kept.Skipped,
                    };
                    return (new RulesUpdateResult { Rule = rules[i] }, true);
                }
            case "rules.delete" when parameters is RulesDeleteParams delete:
                rules.RemoveAt(IndexOf(delete.Id));
                return (new EmptyResult(), true);
            case "rules.reorder" when parameters is RulesReorderParams reorder:
                {
                    if (reorder.Ids.Count != rules.Count || reorder.Ids.Distinct().Count() != rules.Count)
                    {
                        throw Fail(ErrorCode.InvalidArgument, "ids");
                    }
                    var ordered = reorder.Ids.Select(id => rules[IndexOf(id)]).ToList();
                    rules.Clear();
                    rules.AddRange(ordered);
                    return (new RulesReorderResult { Rules = [.. rules] }, true);
                }
            case "rules.test":
                return (new RulesTestResult { Count = 3 }, false);
            case "rules.apply":
                return (new RulesApplyResult { Examined = 120, Matched = 14, Changed = 12, SkippedOutgoing = 2 }, false);
        }
        throw Fail(ErrorCode.MethodNotFound, "samples do not answer " + method);
    }
}
