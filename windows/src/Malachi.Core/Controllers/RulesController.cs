// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/RulesController.swift
// (written at the same time); GTK: the flow described at the top of
// ui/internal/rules/rules.go ("The flow every client follows"):
//
//   - the Rules page of Preferences (after Integrations) calls rules.list
//     at open and on every notify.rulesChanged and shows Rows; the switch of
//     a row calls rules.update with Enabled flipped (asking OutgoingQuestion
//     first when NeedsConfirm says so), a drag or Alt+Up/Down calls
//     rules.reorder with Moved, Remove asks DeleteQuestion and calls
//     rules.delete;
//   - Apply Rules… of a folder's menu (offered when AppliesToAccount finds a
//     rule) asks ApplyQuestion, calls rules.apply {accountId, folderId} and
//     shows ApplyResultText as a toast.
//
// The application's one RulesController keeps the rules as last listed (the
// Rules page shows them, the folder menu asks AppliesTo), asks the daemon
// again on notify.rulesChanged and on every new connection, and runs the
// page's actions and Apply Rules…. Every question goes through a delegate
// the view answers (a ContentDialog); every failure comes back as the
// sentence to show, never the daemon's message.
//
// Windows: the controller is used on the UI thread, whose synchronization
// context brings every await back to it; a late answer of an older
// rules.list is dropped (the generation).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Rules;
using Malachi.Core.Text;

namespace Malachi.Core.Controllers;

/// <summary>How an action of the rules controllers ended.</summary>
public enum RuleResultKind
{
    /// <summary>It was done.</summary>
    Done,

    /// <summary>The user answered a question with Cancel; nothing was sent.</summary>
    Declined,

    /// <summary>The daemon refused or did not answer; <see cref="RuleResult.Text"/> is the sentence.</summary>
    Failed,
}

/// <summary>The end of an action of the rules controllers.</summary>
/// <param name="Kind">How it ended.</param>
/// <param name="Text">The sentence to show: the toast of Apply Rules…, the reason of a failure; "" otherwise.</param>
public sealed record RuleResult(RuleResultKind Kind, string Text = "")
{
    /// <summary>It was done.</summary>
    public bool Ok => Kind == RuleResultKind.Done;
}

/// <summary>The application's mail rules: the list the page shows and the actions on it.</summary>
/// <remarks>Create and use it on the UI thread.</remarks>
public sealed class RulesController
{
    private readonly IRulesCaller caller;
    private List<Rule> rules = [];
    private int generation;

    /// <summary>A controller over <paramref name="caller"/>.</summary>
    public RulesController(IRulesCaller caller)
    {
        ArgumentNullException.ThrowIfNull(caller);
        this.caller = caller;
    }

    /// <summary>The rules or the error changed; the views read them anew.</summary>
    public event EventHandler? Changed;

    /// <summary>The rules in their order, as last listed.</summary>
    public IReadOnlyList<Rule> Rules => rules;

    /// <summary>The first answer arrived.</summary>
    public bool Loaded { get; private set; }

    /// <summary>The last failure's sentence of listing ("" none).</summary>
    public string Error { get; private set; } = "";

    /// <summary>The daemon the controller calls.</summary>
    internal IRulesCaller Caller => caller;

    /// <summary>The rule with the id, or null.</summary>
    public Rule? Find(string id) => rules.FirstOrDefault(r => r.Id == id);

    /// <summary>An enabled rule runs on the account's mail (the folder menu's Apply Rules… is enabled then).</summary>
    public bool AppliesTo(AccountId account) => RuleBook.AppliesToAccount(rules, account);

    /// <summary>Asks the daemon again without waiting (notify.rulesChanged, a new connection).</summary>
    public void Refresh() => _ = ReloadAsync();

    /// <summary>rules.list: replaces the rules; a failure keeps the old ones and says why in <see cref="Error"/>.</summary>
    public async Task ReloadAsync()
    {
        var g = ++generation;
        try
        {
            var result = await caller.CallAsync(API.RulesList, new EmptyParams(), API.RulesList.Timeout, CancellationToken.None);
            if (g != generation)
            {
                return;
            }
            rules = [.. result.Rules];
            Loaded = true;
            Error = "";
        }
#pragma warning disable CA1031 // Every failure of the call is the sentence of the page.
        catch (Exception e)
#pragma warning restore CA1031
        {
            if (g != generation)
            {
                return;
            }
            Error = RpcErrorText.Text(RulesText.PageTitle, e);
        }
        RaiseChanged();
    }

    /// <summary>
    /// Takes a rule the editor saved into the list at once (before the
    /// daemon's notification and the next listing): replaced in place, else
    /// added at the end.
    /// </summary>
    public void Upsert(Rule saved)
    {
        ArgumentNullException.ThrowIfNull(saved);
        var i = rules.FindIndex(r => r.Id == saved.Id);
        if (i >= 0)
        {
            rules[i] = saved;
        }
        else
        {
            rules.Add(saved);
        }
        RaiseChanged();
    }

    /// <summary>
    /// The switch of a row: rules.update with Enabled flipped, after
    /// <see cref="RulesText.OutgoingQuestion"/> when <see cref="RuleBook.NeedsConfirm"/>
    /// says so (<paramref name="confirm"/> answers it). The views draw the
    /// rule again afterwards, whatever came of it.
    /// </summary>
    public async Task<RuleResult> SetEnabledAsync(string id, bool on, Func<RuleQuestion, Task<bool>> confirm)
    {
        ArgumentNullException.ThrowIfNull(confirm);
        if (Find(id) is not { } rule)
        {
            return Missing();
        }
        if (rule.Enabled == on)
        {
            RaiseChanged();
            return new RuleResult(RuleResultKind.Done);
        }
        var input = RuleBook.Toggled(rule);
        var ask = RuleBook.NeedsConfirm(rule, input);
        if (ask && !await confirm(RulesText.OutgoingQuestion(input)))
        {
            RaiseChanged();
            return new RuleResult(RuleResultKind.Declined);
        }
        try
        {
            var result = await caller.CallAsync(
                API.RulesUpdate,
                new RulesUpdateParams { Id = id, Rule = input, ConfirmOutgoing = ask ? true : null },
                API.RulesUpdate.Timeout,
                CancellationToken.None);
            Upsert(result.Rule);
            return new RuleResult(RuleResultKind.Done);
        }
#pragma warning disable CA1031 // Every failure of the call is the sentence of the toast.
        catch (Exception e)
#pragma warning restore CA1031
        {
            return await FailedAsync(e, RulesText.SaveFailedText);
        }
    }

    /// <summary>
    /// A drag or Alt+Up/Down: rules.reorder with <see cref="RuleBook.Moved"/>,
    /// shown at once and put back when the daemon refuses.
    /// </summary>
    public async Task<RuleResult> MoveAsync(int from, int to)
    {
        var ids = rules.Select(r => r.Id).ToList();
        var order = RuleBook.Moved(ids, from, to);
        if (order.SequenceEqual(ids))
        {
            return new RuleResult(RuleResultKind.Done);
        }
        var before = rules;
        rules = [.. order.Select(id => before.First(r => r.Id == id))];
        RaiseChanged();
        try
        {
            var result = await caller.CallAsync(
                API.RulesReorder, new RulesReorderParams { Ids = order }, API.RulesReorder.Timeout, CancellationToken.None);
            rules = [.. result.Rules];
            RaiseChanged();
            return new RuleResult(RuleResultKind.Done);
        }
#pragma warning disable CA1031 // Every failure of the call is the sentence of the toast.
        catch (Exception e)
#pragma warning restore CA1031
        {
            rules = before;
            RaiseChanged();
            return await FailedAsync(e, RulesText.SaveFailedText);
        }
    }

    /// <summary>Remove: asks <see cref="RulesText.DeleteQuestion"/> (<paramref name="confirm"/>), then rules.delete.</summary>
    public async Task<RuleResult> DeleteAsync(string id, Func<RuleQuestion, Task<bool>> confirm)
    {
        ArgumentNullException.ThrowIfNull(confirm);
        if (Find(id) is not { } rule)
        {
            return Missing();
        }
        if (!await confirm(RulesText.DeleteQuestion(rule.Name)))
        {
            return new RuleResult(RuleResultKind.Declined);
        }
        try
        {
            await caller.CallAsync(API.RulesDelete, new RulesDeleteParams { Id = id }, API.RulesDelete.Timeout, CancellationToken.None);
            rules.RemoveAll(r => r.Id == id);
            RaiseChanged();
            return new RuleResult(RuleResultKind.Done);
        }
#pragma warning disable CA1031 // Every failure of the call is the sentence of the toast.
        catch (Exception e)
#pragma warning restore CA1031
        {
            return await FailedAsync(e, RpcErrorText.Text(RulesText.RemoveRule, e));
        }
    }

    /// <summary>
    /// Apply Rules… of a folder's menu: asks <see cref="RulesText.ApplyQuestion"/>
    /// (<paramref name="confirm"/>), runs rules.apply on the folder and
    /// answers the toast's sentence (<see cref="RulesText.ApplyResultText"/>).
    /// </summary>
    public async Task<RuleResult> ApplyAsync(AccountId account, FolderId folder, string folderName, Func<RuleQuestion, Task<bool>> confirm)
    {
        ArgumentNullException.ThrowIfNull(confirm);
        if (!await confirm(RulesText.ApplyQuestion(folderName)))
        {
            return new RuleResult(RuleResultKind.Declined);
        }
        try
        {
            var result = await caller.CallAsync(
                API.RulesApply,
                new RulesApplyParams { AccountId = account, FolderId = folder },
                API.RulesApply.Timeout,
                CancellationToken.None);
            return new RuleResult(RuleResultKind.Done, RulesText.ApplyResultText(result));
        }
#pragma warning disable CA1031 // Every failure of the call is the sentence of the toast.
        catch (Exception e)
#pragma warning restore CA1031
        {
            return await FailedAsync(e, RulesText.ApplyFailedText);
        }
    }

    /// <summary>
    /// The sentence for a failed rules call: ruleNotFound, the daemon's
    /// invalidArgument with <see cref="RuleInvalidData"/>, else
    /// <paramref name="fallback"/>. Never the daemon's own message.
    /// </summary>
    public static string ErrorText(Exception? error, string fallback)
    {
        ArgumentNullException.ThrowIfNull(fallback);
        if (RpcErrorText.DaemonError(error) is not { } e)
        {
            return fallback;
        }
        if (e.Code == ErrorCode.RuleNotFound)
        {
            return RulesText.NotFoundText;
        }
        if (e.Code == ErrorCode.InvalidArgument && InvalidData(e) is { } data)
        {
            return RulesText.InvalidText(data);
        }
        return fallback;
    }

    /// <summary>The daemon's <see cref="RuleInvalidData"/> of an invalidArgument; null when it has none.</summary>
    public static RuleInvalidData? InvalidData(RpcError error)
    {
        ArgumentNullException.ThrowIfNull(error);
        if (error.Data is not { ValueKind: JsonValueKind.Object } data)
        {
            return null;
        }
        try
        {
            return data.Deserialize(ApiJsonContext.Wire.RuleInvalidData);
        }
        catch (JsonException)
        {
            return null;
        }
    }

    private static RuleResult Missing() => new(RuleResultKind.Failed, RulesText.NotFoundText);

    // A rule another window removed is gone from the list too.
    private async Task<RuleResult> FailedAsync(Exception e, string fallback)
    {
        var gone = RpcErrorText.DaemonError(e) is { } d && d.Code == ErrorCode.RuleNotFound;
        var text = ErrorText(e, fallback);
        if (gone)
        {
            await ReloadAsync();
        }
        else
        {
            RaiseChanged();
        }
        return new RuleResult(RuleResultKind.Failed, text);
    }

    private void RaiseChanged() => Changed?.Invoke(this, EventArgs.Empty);
}
