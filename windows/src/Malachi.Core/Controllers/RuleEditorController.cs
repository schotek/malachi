// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/RuleEditorController.swift
// (written at the same time); GTK: ui/internal/rules/rules.go ("Add Rule…
// opens the editor with NewForm, Edit… with FromRule; the editor shows
// Fields, OperatorsFor, ValueKindOf and ActionKinds, Save is enabled when
// Validate finds nothing and sends rules.create / rules.update with Input
// (after OutgoingQuestion when NeedsConfirm), Try It calls rules.test over
// the folder chosen and shows TestResultText").
//
// The state of the rule editor window: the form being edited, every change
// of a row, the problems Validate finds (Save is enabled without them),
// the choices of folders for a Move To action and for Try It, and the three
// calls: rules.create / rules.update (Save), rules.test (Try It). A
// failure comes back as the sentence to show (RulesController.ErrorText),
// never the daemon's message. The view draws the controller anew on every
// Changed.
//
// Windows: choosing another account clears the folder of a Move To action
// (a folder belongs to its account) and removes the action when the rule
// is for every account (it takes no Move To), so that the editor never
// holds a folder of the wrong account; Try It is enabled only for a form
// without problems, so that the daemon is not asked about a rule it will
// refuse. Create and use it on the UI thread.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Rules;
using Malachi.Core.Text;

namespace Malachi.Core.Controllers;

/// <summary>The rule editor window's state and calls.</summary>
/// <remarks>Create and use it on the UI thread.</remarks>
public sealed class RuleEditorController
{
    private readonly IRulesCaller caller;
    private readonly Func<AccountId, IReadOnlyList<Folder>> foldersOf;
    private FolderId? testFolder;

    /// <summary>An editor over <paramref name="form"/>.</summary>
    /// <param name="caller">The daemon.</param>
    /// <param name="form">The rule being edited (<see cref="RuleBook.NewForm"/>, <see cref="RuleBook.FromRule"/>, <see cref="RuleBook.FromMessage"/>).</param>
    /// <param name="original">The stored rule being edited; null for a new one (decides whether Save asks the outgoing question).</param>
    /// <param name="accounts">The mail accounts a rule can be for, in the sidebar's order.</param>
    /// <param name="foldersOf">The folders of an account in the sidebar's order.</param>
    public RuleEditorController(
        IRulesCaller caller, RuleForm form, Rule? original, IReadOnlyList<Account> accounts, Func<AccountId, IReadOnlyList<Folder>> foldersOf)
    {
        ArgumentNullException.ThrowIfNull(caller);
        ArgumentNullException.ThrowIfNull(form);
        ArgumentNullException.ThrowIfNull(accounts);
        ArgumentNullException.ThrowIfNull(foldersOf);
        this.caller = caller;
        Form = form;
        Original = original;
        Accounts = accounts;
        this.foldersOf = foldersOf;
    }

    /// <summary>The form changed or a call ended; the view reads the controller anew.</summary>
    public event EventHandler? Changed;

    /// <summary>The form being edited.</summary>
    public RuleForm Form { get; }

    /// <summary>The stored rule being edited; null for a new one.</summary>
    public Rule? Original { get; }

    /// <summary>The mail accounts a rule can be for.</summary>
    public IReadOnlyList<Account> Accounts { get; }

    /// <summary>The rule rules.create or rules.update answered; null until Save succeeded.</summary>
    public Rule? Saved { get; private set; }

    /// <summary>A call is under way (Save is not offered again).</summary>
    public bool Saving { get; private set; }

    /// <summary>rules.test is under way.</summary>
    public bool Testing { get; private set; }

    /// <summary>The sentence of the last failed Save ("" none); cleared by the next change.</summary>
    public string SaveError { get; private set; } = "";

    /// <summary>The line under Try It: the result or the failure ("" none); cleared by the next change.</summary>
    public string TestText { get; private set; } = "";

    /// <summary><see cref="TestText"/> is a failure.</summary>
    public bool TestFailed { get; private set; }

    /// <summary>The editor's title.</summary>
    public string Title => RulesText.EditorTitle(Form);

    /// <summary>Every problem of the form, in the order of the editor.</summary>
    public IReadOnlyList<RuleFormProblem> Problems => RuleBook.Validate(Form);

    /// <summary>Save can run: the form has no problem and no call is under way.</summary>
    public bool CanSave => !Saving && Problems.Count == 0;

    /// <summary>Try It can run: the form has no problem, there is a folder to look in and no call is under way.</summary>
    public bool CanTest => !Saving && !Testing && Problems.Count == 0 && TestFolderId is not null;

    /// <summary>
    /// The text of the first problem of a row (or of the name, "name" and
    /// index 0), shown under it; "" for none. The problems of a whole group
    /// (no condition, too many, no action) are <see cref="GroupProblemText"/>.
    /// </summary>
    public string ProblemText(string field, int index = 0)
    {
        var p = Problems.FirstOrDefault(x => x.Field == field && x.Index == index && !IsGroupProblem(x.Kind));
        return p is null ? "" : RulesText.ProblemText(p);
    }

    /// <summary>The text of the problem of a whole group of rows ("conditions" or "actions": none, or too many), shown under the group; "" for none.</summary>
    public string GroupProblemText(string field)
    {
        var p = Problems.FirstOrDefault(x => x.Field == field && IsGroupProblem(x.Kind));
        return p is null ? "" : RulesText.ProblemText(p);
    }

    private static bool IsGroupProblem(RuleFormProblemKind kind) =>
        kind is RuleFormProblemKind.NoCondition or RuleFormProblemKind.TooManyConditions
            or RuleFormProblemKind.NoAction or RuleFormProblemKind.TooManyActions;

    /// <summary>The folders a Move To action offers: those of the rule's account; none for a rule for every account.</summary>
    public IReadOnlyList<Folder> MoveFolders =>
        Form.AccountId is { Value.Length: > 0 } account ? RuleBook.MoveFolders(foldersOf(account)) : [];

    /// <summary>The account Try It looks in: the rule's, else the first mail account.</summary>
    public AccountId? TestAccountId
    {
        get
        {
            if (Form.AccountId is { Value.Length: > 0 } account)
            {
                return account;
            }
            if (Accounts.Count > 0)
            {
                return Accounts[0].Id;
            }
            return null;
        }
    }

    /// <summary>The folders Try It offers: the account's folders that hold messages (<see cref="RuleBook.CanApplyIn"/>).</summary>
    public IReadOnlyList<Folder> TestFolders =>
        TestAccountId is { } account ? [.. foldersOf(account).Where(RuleBook.CanApplyIn)] : [];

    /// <summary>The folder Try It looks in: the one chosen, else the inbox of the account, else its first folder; null without one.</summary>
    public FolderId? TestFolderId
    {
        get
        {
            var folders = TestFolders;
            if (testFolder is { } chosen && folders.Any(f => f.Id == chosen))
            {
                return chosen;
            }
            foreach (var f in folders)
            {
                if (f.Role.Value == FolderRole.Inbox)
                {
                    return f.Id;
                }
            }
            if (folders.Count > 0)
            {
                return folders[0].Id;
            }
            return null;
        }
    }

    /// <summary>Chooses the folder Try It looks in.</summary>
    public void SetTestFolder(FolderId? folder)
    {
        testFolder = folder;
        Edited();
    }

    /// <summary>The name.</summary>
    public void SetName(string name)
    {
        Form.Name = name ?? "";
        Edited();
    }

    /// <summary>The "Rule is on" switch.</summary>
    public void SetEnabled(bool on)
    {
        Form.Enabled = on;
        Edited();
    }

    /// <summary>
    /// Apply to: the rule's account, null for every mail account. A Move To
    /// action loses its folder (it belongs to the old account) and goes
    /// when the rule is for every account.
    /// </summary>
    public void SetAccount(AccountId? account)
    {
        var next = account is { Value.Length: > 0 } ? account : null;
        if (next == Form.AccountId)
        {
            return;
        }
        Form.AccountId = next;
        var actions = new List<RuleAction>();
        foreach (var a in Form.Actions)
        {
            if (a.Kind.Value != RuleActionKind.Move)
            {
                actions.Add(a);
            }
            else if (next is not null)
            {
                actions.Add(a with { FolderId = null });
            }
        }
        Form.Actions = actions;
        testFolder = null;
        Edited();
    }

    /// <summary>How the conditions combine.</summary>
    public void SetMatch(RuleMatch match)
    {
        Form.Match = match;
        Edited();
    }

    /// <summary>The Stop check box.</summary>
    public void SetStop(bool stop)
    {
        Form.Stop = stop;
        Edited();
    }

    /// <summary>Add Condition: a new row (From contains) unless the rule has the most.</summary>
    public void AddCondition()
    {
        if (Form.Conditions.Count >= RuleLimits.MaxRuleConditions)
        {
            return;
        }
        Form.Conditions.Add(RuleBook.NewCondition(RuleField.From));
        Edited();
    }

    /// <summary>The remove button of a condition row.</summary>
    public void RemoveCondition(int index)
    {
        if (index < 0 || index >= Form.Conditions.Count)
        {
            return;
        }
        Form.Conditions.RemoveAt(index);
        Edited();
    }

    /// <summary>A row's field: the operator is kept when the new field takes it, the value when it is text both times.</summary>
    public void SetConditionField(int index, RuleField field)
    {
        if (index < 0 || index >= Form.Conditions.Count)
        {
            return;
        }
        Form.Conditions[index] = RuleBook.NewCondition(field, Form.Conditions[index]);
        Edited();
    }

    /// <summary>A row's operator.</summary>
    public void SetConditionOperator(int index, RuleOperator op)
    {
        if (index < 0 || index >= Form.Conditions.Count)
        {
            return;
        }
        Form.Conditions[index] = Form.Conditions[index] with { Op = op };
        Edited();
    }

    /// <summary>A row's value (the text typed, or one of the fixed values).</summary>
    public void SetConditionValue(int index, string value)
    {
        if (index < 0 || index >= Form.Conditions.Count)
        {
            return;
        }
        Form.Conditions[index] = Form.Conditions[index] with { Value = value ?? "" };
        Edited();
    }

    /// <summary>Add Action: a new row of the kind, which must be one of <see cref="RuleBook.AvailableActions"/>.</summary>
    public void AddAction(RuleActionKind kind)
    {
        if (!RuleBook.AvailableActions(Form).Contains(kind) || Form.Actions.Count >= RuleLimits.MaxRuleActions)
        {
            return;
        }
        Form.Actions.Add(new RuleAction { Kind = kind });
        Edited();
    }

    /// <summary>The remove button of an action row.</summary>
    public void RemoveAction(int index)
    {
        if (index < 0 || index >= Form.Actions.Count)
        {
            return;
        }
        Form.Actions.RemoveAt(index);
        Edited();
    }

    /// <summary>The folder of a Move To row.</summary>
    public void SetMoveFolder(int index, FolderId? folder)
    {
        UpdateAction(index, RuleActionKind.Move, a => a with { FolderId = folder });
    }

    /// <summary>The address of a Forward To row.</summary>
    public void SetForwardTo(int index, string to)
    {
        UpdateAction(index, RuleActionKind.Forward, a => a with { To = to });
    }

    /// <summary>The subject of a Reply Automatically row.</summary>
    public void SetReplySubject(int index, string subject)
    {
        UpdateAction(index, RuleActionKind.AutoReply, a => a with { Subject = subject });
    }

    /// <summary>The text of a Reply Automatically row.</summary>
    public void SetReplyText(int index, string text)
    {
        UpdateAction(index, RuleActionKind.AutoReply, a => a with { Text = text });
    }

    /// <summary>
    /// Save: asks <see cref="RulesText.OutgoingQuestion"/> first when
    /// <see cref="RuleBook.NeedsConfirm"/> says so (<paramref name="confirm"/>
    /// answers it; Cancel is <see cref="RuleResultKind.Declined"/>), then
    /// rules.create or rules.update with confirmOutgoing. The rule saved is
    /// <see cref="Saved"/>; a failure is <see cref="SaveError"/> and the
    /// result's text.
    /// </summary>
    public async Task<RuleResult> SaveAsync(Func<RuleQuestion, Task<bool>> confirm)
    {
        ArgumentNullException.ThrowIfNull(confirm);
        if (!CanSave)
        {
            return new RuleResult(RuleResultKind.Failed);
        }
        var input = RuleBook.Input(Form);
        var ask = RuleBook.NeedsConfirm(Original, input);
        if (ask && !await confirm(RulesText.OutgoingQuestion(input)))
        {
            return new RuleResult(RuleResultKind.Declined);
        }
        Saving = true;
        SaveError = "";
        RaiseChanged();
        try
        {
            Rule saved;
            if (Form.Id.Length == 0)
            {
                saved = (await caller.CallAsync(
                    API.RulesCreate,
                    new RulesCreateParams { Rule = input, ConfirmOutgoing = ask ? true : null },
                    API.RulesCreate.Timeout,
                    CancellationToken.None)).Rule;
            }
            else
            {
                saved = (await caller.CallAsync(
                    API.RulesUpdate,
                    new RulesUpdateParams { Id = Form.Id, Rule = input, ConfirmOutgoing = ask ? true : null },
                    API.RulesUpdate.Timeout,
                    CancellationToken.None)).Rule;
            }
            Saved = saved;
            Saving = false;
            RaiseChanged();
            return new RuleResult(RuleResultKind.Done);
        }
#pragma warning disable CA1031 // Every failure of the call is the sentence under the form.
        catch (Exception e)
#pragma warning restore CA1031
        {
            Saving = false;
            SaveError = RulesController.ErrorText(e, RulesText.SaveFailedText);
            RaiseChanged();
            return new RuleResult(RuleResultKind.Failed, SaveError);
        }
    }

    /// <summary>
    /// Try It: rules.test of the rule as edited over <see cref="TestFolderId"/>;
    /// <see cref="TestText"/> says how many stored messages of the folder
    /// match (<see cref="RulesText.TestResultText"/>), or why it failed.
    /// </summary>
    public async Task TestAsync()
    {
        if (!CanTest || TestAccountId is not { } account || TestFolderId is not { } folder)
        {
            return;
        }
        Testing = true;
        TestText = "";
        TestFailed = false;
        RaiseChanged();
        try
        {
            var result = await caller.CallAsync(
                API.RulesTest,
                new RulesTestParams { Rule = RuleBook.Input(Form), AccountId = account, FolderId = folder },
                API.RulesTest.Timeout,
                CancellationToken.None);
            TestText = RulesText.TestResultText(result.Count);
        }
#pragma warning disable CA1031 // Every failure of the call is the line under Try It.
        catch (Exception e)
#pragma warning restore CA1031
        {
            TestFailed = true;
            TestText = RulesController.ErrorText(e, RpcErrorText.Text(RulesText.TestButton, e));
        }
        Testing = false;
        RaiseChanged();
    }

    private void UpdateAction(int index, string kind, Func<RuleAction, RuleAction> change)
    {
        if (index < 0 || index >= Form.Actions.Count || Form.Actions[index].Kind.Value != kind)
        {
            return;
        }
        Form.Actions[index] = change(Form.Actions[index]);
        Edited();
    }

    // An edit makes the last failure and the last test result stale.
    private void Edited()
    {
        SaveError = "";
        TestText = "";
        TestFailed = false;
        RaiseChanged();
    }

    private void RaiseChanged() => Changed?.Invoke(this, EventArgs.Empty);
}
