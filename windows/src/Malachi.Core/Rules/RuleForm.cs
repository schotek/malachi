// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/rules/rules.go (Form, NewForm, FromRule, Input,
// Problem, ProblemKind, ValueKind); Swift: macos/Sources/MalachiCore/Rules/
// (written at the same time).
//
// The rule as the editor holds it, and what Validate finds in it. Go's Form
// is a struct that embeds api.RuleInput; here it is a class with the same
// members, as the conventions have it for a form the view edits
// (docs/windows-port.md §3.1; ContactBook.Form). Every string in it may come
// from mail (a prefilled sender, a list id) and is shown as plain text only.

using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Rules;

/// <summary>rules.ValueKind: what the value of a condition is edited with.</summary>
public enum RuleValueKind
{
    /// <summary>A text field.</summary>
    TextField,

    /// <summary>A choice of <see cref="RuleBook.BulkValues"/>.</summary>
    Bulk,

    /// <summary>A choice of <see cref="RuleBook.ValueTrue"/> and <see cref="RuleBook.ValueFalse"/>.</summary>
    Bool,
}

/// <summary>rules.ProblemKind: what is wrong in a form.</summary>
public enum RuleFormProblemKind
{
    /// <summary>The name is empty.</summary>
    NameMissing = 1,

    /// <summary>A string is longer than the daemon takes.</summary>
    TooLong,

    /// <summary>No condition.</summary>
    NoCondition,

    /// <summary>More than <see cref="RuleLimits.MaxRuleConditions"/> conditions.</summary>
    TooManyConditions,

    /// <summary>A condition without a value.</summary>
    ValueMissing,

    /// <summary>A control, bidi, invisible or line-breaking character.</summary>
    BadCharacters,

    /// <summary>No action.</summary>
    NoAction,

    /// <summary>More than <see cref="RuleLimits.MaxRuleActions"/> actions.</summary>
    TooManyActions,

    /// <summary>The same kind of action twice.</summary>
    ActionTwice,

    /// <summary>More than one of move, junk and trash.</summary>
    OnePlace,

    /// <summary>A move action of a rule for every account.</summary>
    MoveNeedsAccount,

    /// <summary>A move action without a folder.</summary>
    FolderMissing,

    /// <summary>A forward address that is not one address.</summary>
    AddressInvalid,

    /// <summary>An automatic reply without text.</summary>
    ReplyMissing,

    /// <summary>An operator the field does not take.</summary>
    BadOperator,
}

/// <summary>
/// rules.Problem: what <see cref="RuleBook.Validate"/> found: the field
/// ("name", "accountId", "conditions", "actions"), for the last two the row,
/// and the kind that picks the text (<see cref="RulesText.ProblemText"/>).
/// </summary>
/// <param name="Field">The form's field.</param>
/// <param name="Index">The row of a condition or an action.</param>
/// <param name="Kind">What is wrong.</param>
public sealed record RuleFormProblem(string Field, int Index, RuleFormProblemKind Kind);

/// <summary>rules.Form: a rule as the editor holds it; <see cref="AccountId"/> null is every mail account.</summary>
public sealed class RuleForm
{
    /// <summary>The edited rule's id, "" for a new one.</summary>
    public string Id { get; set; } = "";

    /// <summary>The name.</summary>
    public string Name { get; set; } = "";

    /// <summary>The rule is on.</summary>
    public bool Enabled { get; set; }

    /// <summary>The one mail account the rule is for; null every mail account.</summary>
    public AccountId? AccountId { get; set; }

    /// <summary>How the conditions combine.</summary>
    public RuleMatch Match { get; set; } = RuleMatch.All;

    /// <summary>The condition rows.</summary>
    public List<RuleCondition> Conditions { get; set; } = [];

    /// <summary>The action rows.</summary>
    public List<RuleAction> Actions { get; set; } = [];

    /// <summary>No later rule runs on a message this rule matched.</summary>
    public bool Stop { get; set; }
}
