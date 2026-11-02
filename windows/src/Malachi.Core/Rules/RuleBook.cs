// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/rules/rules.go (Fields, ActionKinds, BulkValues,
// ValueTrue, ValueFalse, ValueKindOf, IsAddressField, OperatorsFor,
// DefaultValue, NewCondition, NewForm, FromRule, Form.Input, IsOutgoing,
// AvailableActions, Validate, Clean, cleanText, ValidAddress, FromMessage,
// truncate, NeedsConfirm, outgoingKey, Toggled, Moved, AppliesToAccount,
// CanApplyIn, CanCreateFrom, MoveFolders); Swift: macos/Sources/MalachiCore/
// Rules/ (written at the same time).
//
// The mail rules' pure UI logic (docs/plans/rules.md, docs/api.md §4.21):
// the editable form of a rule and its validation, the choices each row of
// the editor offers, a rule prefilled from a message (Create Rule from
// Message…), the order after a drag, which outgoing changes need the user's
// confirmation, which rules apply to an account (Apply Rules… of a folder's
// menu). The daemon stores and runs the rules (rules.*); this only turns
// them into view models and back. Every string of a rule may come from mail
// and is shown as plain text only.
//
// Windows: the class is RuleBook, not Rules, so that the namespace
// Malachi.Core.Rules is not hidden (as ContactBook for Contacts); Go's
// Translator is L10n, and lengths are UTF-8 bytes as in Go.

using System;
using System.Buffers;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Contacts;

namespace Malachi.Core.Rules;

/// <summary>ui/internal/rules: the logic of the mail rules' editor, page and menus.</summary>
public static class RuleBook
{
    /// <summary>rules.ValueTrue: the value of a hasAttachments condition that asks for attachments.</summary>
    public const string ValueTrue = "true";

    /// <summary>rules.ValueFalse: the value of a hasAttachments condition that asks for none.</summary>
    public const string ValueFalse = "false";

    private static readonly SearchValues<char> AddressForbidden = SearchValues.Create(" <>,;:\"()[]\\");

    /// <summary>rules.Fields: the condition fields in the order the editor offers them.</summary>
    public static IReadOnlyList<RuleField> Fields { get; } =
    [
        RuleField.From, RuleField.To, RuleField.Cc, RuleField.ToOrCc,
        RuleField.Subject, RuleField.Body, RuleField.ListId,
        RuleField.Bulk, RuleField.HasAttachments,
    ];

    /// <summary>rules.ActionKinds: the action kinds in the order the editor offers them (and the daemon runs them).</summary>
    public static IReadOnlyList<RuleActionKind> ActionKinds { get; } =
    [
        RuleActionKind.MarkRead, RuleActionKind.Flag,
        RuleActionKind.Forward, RuleActionKind.AutoReply,
        RuleActionKind.Move, RuleActionKind.Junk, RuleActionKind.Trash,
    ];

    /// <summary>rules.BulkValues: the values of a bulk condition, in the editor's order.</summary>
    public static IReadOnlyList<string> BulkValues { get; } =
        [RuleValues.BulkAny, BulkKind.Newsletter, BulkKind.List, BulkKind.Automated];

    /// <summary>rules.ValueKindOf: the editor of a field's value.</summary>
    public static RuleValueKind ValueKindOf(RuleField f) => f.Value switch
    {
        RuleField.Bulk => RuleValueKind.Bulk,
        RuleField.HasAttachments => RuleValueKind.Bool,
        _ => RuleValueKind.TextField,
    };

    /// <summary>rules.IsAddressField: the field holds addresses.</summary>
    public static bool IsAddressField(RuleField f) =>
        f.Value is RuleField.From or RuleField.To or RuleField.Cc or RuleField.ToOrCc;

    /// <summary>rules.OperatorsFor: the operators a field takes, in the editor's order; the first is the default.</summary>
    public static IReadOnlyList<RuleOperator> OperatorsFor(RuleField f)
    {
        switch (ValueKindOf(f))
        {
            case RuleValueKind.Bulk:
                return [RuleOperator.Is, RuleOperator.IsNot];
            case RuleValueKind.Bool:
                return [RuleOperator.Is];
        }
        List<RuleOperator> ops =
        [
            RuleOperator.Contains, RuleOperator.NotContains, RuleOperator.Is,
            RuleOperator.IsNot, RuleOperator.StartsWith, RuleOperator.EndsWith,
        ];
        if (IsAddressField(f))
        {
            ops.Add(RuleOperator.DomainIs);
        }
        return ops;
    }

    /// <summary>rules.DefaultValue: the value a condition gets when its field is chosen.</summary>
    public static string DefaultValue(RuleField f) => ValueKindOf(f) switch
    {
        RuleValueKind.Bulk => RuleValues.BulkAny,
        RuleValueKind.Bool => ValueTrue,
        _ => "",
    };

    /// <summary>
    /// rules.NewCondition: the condition a new row of the editor starts as
    /// (From contains), or the one of a row whose field the user changed
    /// (<paramref name="old"/>): the operator kept when the new field takes
    /// it, the value when it is text both times.
    /// </summary>
    public static RuleCondition NewCondition(RuleField f, RuleCondition? old = null)
    {
        var c = new RuleCondition { Field = f, Op = OperatorsFor(f)[0], Value = DefaultValue(f) };
        if (old is null)
        {
            return c;
        }
        foreach (var op in OperatorsFor(f))
        {
            if (op == old.Op)
            {
                c = c with { Op = op };
            }
        }
        if (ValueKindOf(f) == RuleValueKind.TextField && ValueKindOf(old.Field) == RuleValueKind.TextField)
        {
            c = c with { Value = old.Value };
        }
        return c;
    }

    /// <summary>
    /// rules.NewForm: the editor of a new rule: enabled, every condition,
    /// one empty From condition, no action yet; for the account given (the
    /// one the main window shows), null for every account.
    /// </summary>
    public static RuleForm NewForm(AccountId? accountId) => new()
    {
        Enabled = true,
        AccountId = accountId is { Value.Length: > 0 } ? accountId : null,
        Match = RuleMatch.All,
        Conditions = [NewCondition(RuleField.From)],
    };

    /// <summary>rules.FromRule: the editor of a stored rule; the rows are copies.</summary>
    public static RuleForm FromRule(Rule r)
    {
        ArgumentNullException.ThrowIfNull(r);
        return new RuleForm
        {
            Id = r.Id,
            Name = r.Name,
            Enabled = r.Enabled,
            AccountId = r.AccountId,
            Match = string.IsNullOrEmpty(r.Match.Value) ? RuleMatch.All : r.Match,
            Conditions = [.. r.Conditions],
            Actions = [.. r.Actions],
            Stop = r.Stop,
        };
    }

    /// <summary>
    /// rules.Form.Input: what rules.create and rules.update send: the
    /// strings trimmed, the fields of other action kinds cleared.
    /// </summary>
    public static RuleInput Input(RuleForm f)
    {
        ArgumentNullException.ThrowIfNull(f);
        var conditions = new List<RuleCondition>(f.Conditions.Count);
        foreach (var c in f.Conditions)
        {
            conditions.Add(ValueKindOf(c.Field) == RuleValueKind.TextField ? c with { Value = ContactBook.TrimSpace(c.Value) } : c);
        }
        var actions = new List<RuleAction>(f.Actions.Count);
        foreach (var a in f.Actions)
        {
            actions.Add(a.Kind.Value switch
            {
                RuleActionKind.Move => new RuleAction { Kind = a.Kind, FolderId = a.FolderId },
                RuleActionKind.Forward => new RuleAction { Kind = a.Kind, To = ContactBook.TrimSpace(a.To) },
                RuleActionKind.AutoReply => new RuleAction
                {
                    Kind = a.Kind,
                    Subject = ContactBook.TrimSpace(a.Subject),
                    Text = (a.Text ?? "").TrimEnd(' ', '\t', '\r', '\n'),
                },
                _ => new RuleAction { Kind = a.Kind },
            });
        }
        return new RuleInput
        {
            Name = ContactBook.TrimSpace(f.Name),
            Enabled = f.Enabled,
            AccountId = f.AccountId,
            Match = f.Match,
            Conditions = conditions,
            Actions = actions,
            Stop = f.Stop,
        };
    }

    /// <summary>rules.IsOutgoing: the rule input has an outgoing action.</summary>
    public static bool IsOutgoing(RuleInput input)
    {
        ArgumentNullException.ThrowIfNull(input);
        return input.Actions.Any(a => a.Kind.IsOutgoing);
    }

    /// <summary>
    /// rules.AvailableActions: the kinds an Add Action menu offers: each
    /// kind once, move only with an account, and none of move, junk and trash
    /// once the rule has one of them.
    /// </summary>
    public static IReadOnlyList<RuleActionKind> AvailableActions(RuleForm f)
    {
        ArgumentNullException.ThrowIfNull(f);
        var has = f.Actions.Select(a => a.Kind.Value).ToHashSet(StringComparer.Ordinal);
        var place = has.Contains(RuleActionKind.Move) || has.Contains(RuleActionKind.Junk) || has.Contains(RuleActionKind.Trash);
        var output = new List<RuleActionKind>();
        foreach (var k in ActionKinds)
        {
            if (has.Contains(k.Value))
            {
                continue;
            }
            if (IsPlacing(k) && place)
            {
                continue;
            }
            if (k.Value == RuleActionKind.Move && f.AccountId is not { Value.Length: > 0 })
            {
                continue;
            }
            output.Add(k);
        }
        return output;
    }

    /// <summary>
    /// rules.Validate: every problem of the form, in the order of the editor;
    /// none means Save is enabled. The daemon checks again (RuleInvalidData).
    /// </summary>
    public static IReadOnlyList<RuleFormProblem> Validate(RuleForm f)
    {
        ArgumentNullException.ThrowIfNull(f);
        var input = Input(f);
        var output = new List<RuleFormProblem>();
        void Add(string field, int i, RuleFormProblemKind k) => output.Add(new RuleFormProblem(field, i, k));

        if (input.Name.Length == 0)
        {
            Add("name", 0, RuleFormProblemKind.NameMissing);
        }
        else if (ByteCount(input.Name) > RuleLimits.MaxRuleNameBytes)
        {
            Add("name", 0, RuleFormProblemKind.TooLong);
        }
        else if (!Clean(input.Name))
        {
            Add("name", 0, RuleFormProblemKind.BadCharacters);
        }

        if (input.Conditions.Count == 0)
        {
            Add("conditions", 0, RuleFormProblemKind.NoCondition);
        }
        else if (input.Conditions.Count > RuleLimits.MaxRuleConditions)
        {
            Add("conditions", 0, RuleFormProblemKind.TooManyConditions);
        }
        for (var i = 0; i < input.Conditions.Count; i++)
        {
            var c = input.Conditions[i];
            if (!OperatorOf(c.Field, c.Op))
            {
                Add("conditions", i, RuleFormProblemKind.BadOperator);
                continue;
            }
            switch (ValueKindOf(c.Field))
            {
                case RuleValueKind.Bulk:
                    if (!BulkValues.Contains(c.Value))
                    {
                        Add("conditions", i, RuleFormProblemKind.ValueMissing);
                    }
                    break;
                case RuleValueKind.Bool:
                    if (c.Value != ValueTrue && c.Value != ValueFalse)
                    {
                        Add("conditions", i, RuleFormProblemKind.ValueMissing);
                    }
                    break;
                default:
                    if (c.Value.Length == 0)
                    {
                        Add("conditions", i, RuleFormProblemKind.ValueMissing);
                    }
                    else if (ByteCount(c.Value) > RuleLimits.MaxRuleValueBytes)
                    {
                        Add("conditions", i, RuleFormProblemKind.TooLong);
                    }
                    else if (!Clean(c.Value))
                    {
                        Add("conditions", i, RuleFormProblemKind.BadCharacters);
                    }
                    break;
            }
        }

        if (input.Actions.Count == 0)
        {
            Add("actions", 0, RuleFormProblemKind.NoAction);
        }
        else if (input.Actions.Count > RuleLimits.MaxRuleActions)
        {
            Add("actions", 0, RuleFormProblemKind.TooManyActions);
        }
        var seen = new HashSet<string>(StringComparer.Ordinal);
        var placed = false;
        for (var i = 0; i < input.Actions.Count; i++)
        {
            var a = input.Actions[i];
            if (!seen.Add(a.Kind.Value))
            {
                Add("actions", i, RuleFormProblemKind.ActionTwice);
                continue;
            }
            if (IsPlacing(a.Kind))
            {
                if (placed)
                {
                    Add("actions", i, RuleFormProblemKind.OnePlace);
                    continue;
                }
                placed = true;
            }
            switch (a.Kind.Value)
            {
                case RuleActionKind.Move:
                    if (input.AccountId is not { Value.Length: > 0 })
                    {
                        Add("actions", i, RuleFormProblemKind.MoveNeedsAccount);
                    }
                    else if (a.FolderId is not { Value.Length: > 0 })
                    {
                        Add("actions", i, RuleFormProblemKind.FolderMissing);
                    }
                    break;
                case RuleActionKind.Forward:
                    if (!ValidAddress(a.To))
                    {
                        Add("actions", i, RuleFormProblemKind.AddressInvalid);
                    }
                    break;
                case RuleActionKind.AutoReply:
                    var text = a.Text ?? "";
                    var subject = a.Subject ?? "";
                    if (ContactBook.TrimSpace(text).Length == 0)
                    {
                        Add("actions", i, RuleFormProblemKind.ReplyMissing);
                    }
                    else if (ByteCount(text) > RuleLimits.MaxRuleReplyBytes || ByteCount(subject) > RuleLimits.MaxRuleValueBytes)
                    {
                        Add("actions", i, RuleFormProblemKind.TooLong);
                    }
                    else if (!Clean(subject) || !CleanText(text))
                    {
                        Add("actions", i, RuleFormProblemKind.BadCharacters);
                    }
                    break;
            }
        }
        return output;
    }

    /// <summary>
    /// rules.Clean: a one-line value has no control, format (bidi,
    /// invisible) or line-breaking character and is valid text, the daemon's
    /// rule for every string of a rule.
    /// </summary>
    public static bool Clean(string? s)
    {
        s ??= "";
        var i = 0;
        while (i < s.Length)
        {
            if (!Rune.TryGetRuneAt(s, i, out var r))
            {
                return false;
            }
            if (Rune.GetUnicodeCategory(r) is UnicodeCategory.Control or UnicodeCategory.Format || r.Value is 0x2028 or 0x2029)
            {
                return false;
            }
            i += r.Utf16SequenceLength;
        }
        return true;
    }

    /// <summary>rules.ValidAddress: a bare address the daemon accepts as forward's To: local@domain, no spaces, angle brackets, commas or control characters, a dot in the domain.</summary>
    public static bool ValidAddress(string? s)
    {
        s = ContactBook.TrimSpace(s);
        if (s.Length == 0 || ByteCount(s) > 254 || !Clean(s) || s.AsSpan().IndexOfAny(AddressForbidden) >= 0)
        {
            return false;
        }
        var at = s.LastIndexOf('@');
        if (at <= 0 || at == s.Length - 1 || s.Count(c => c == '@') != 1)
        {
            return false;
        }
        var domain = s[(at + 1)..];
        return domain.Contains('.', StringComparison.Ordinal) && !domain.StartsWith('.') && !domain.EndsWith('.')
            && !domain.Contains("..", StringComparison.Ordinal);
    }

    /// <summary>
    /// rules.FromMessage: the editor of Create Rule from Message…: named
    /// after the sender, From is the sender's address and, for a mailing list
    /// or a newsletter with a List-Id, List ID is that id instead (one
    /// condition, which is what such mail has in common); one Move To action
    /// without a folder yet, so the user picks where it goes; for the
    /// message's account.
    /// </summary>
    public static RuleForm FromMessage(MessageSummary m, AccountId? accountId)
    {
        ArgumentNullException.ThrowIfNull(m);
        var f = NewForm(accountId);
        var sender = "";
        if (m.From.Count > 0)
        {
            sender = ContactBook.TrimSpace(m.From[0].Email);
            var name = ContactBook.TrimSpace(m.From[0].Name);
            if (name.Length > 0 && Clean(name))
            {
                f.Name = RulesText.PrefillName(name);
            }
            else if (sender.Length > 0)
            {
                f.Name = RulesText.PrefillName(sender);
            }
        }
        if (m.Bulk is { ListId.Length: > 0 } bulk && Clean(bulk.ListId))
        {
            f.Conditions = [new RuleCondition { Field = RuleField.ListId, Op = RuleOperator.Is, Value = bulk.ListId }];
        }
        else if (sender.Length > 0 && Clean(sender))
        {
            f.Conditions = [new RuleCondition { Field = RuleField.From, Op = RuleOperator.Is, Value = sender }];
        }
        if (ByteCount(f.Name) > RuleLimits.MaxRuleNameBytes)
        {
            f.Name = Truncate(f.Name, RuleLimits.MaxRuleNameBytes);
        }
        if (accountId is { Value.Length: > 0 })
        {
            f.Actions = [new RuleAction { Kind = RuleActionKind.Move }];
        }
        return f;
    }

    /// <summary>
    /// rules.NeedsConfirm: saving the form must ask
    /// <see cref="RulesText.OutgoingQuestion"/> and send confirmOutgoing
    /// (docs/api.md §4.21): the rule is enabled with an outgoing action and
    /// either <paramref name="old"/> (the stored rule, null for a new one)
    /// was not, or an outgoing action's To, Subject or Text changed.
    /// </summary>
    public static bool NeedsConfirm(Rule? old, RuleInput input)
    {
        ArgumentNullException.ThrowIfNull(input);
        if (!input.Enabled || !IsOutgoing(input))
        {
            return false;
        }
        if (old is null || !old.Enabled || !IsOutgoing(old))
        {
            return true;
        }
        return OutgoingKey(old) != OutgoingKey(input);
    }

    /// <summary>rules.Toggled: the input of a row's switch: the rule with Enabled flipped.</summary>
    public static RuleInput Toggled(Rule r)
    {
        ArgumentNullException.ThrowIfNull(r);
        var f = FromRule(r);
        f.Enabled = !r.Enabled;
        return new RuleInput
        {
            Name = f.Name,
            Enabled = f.Enabled,
            AccountId = f.AccountId,
            Match = f.Match,
            Conditions = f.Conditions,
            Actions = f.Actions,
            Stop = f.Stop,
        };
    }

    /// <summary>
    /// rules.Moved: the order of rules.reorder after the rule at
    /// <paramref name="from"/> was dropped at <paramref name="to"/> (an index
    /// of the list without it); an index out of range leaves the order as it
    /// is.
    /// </summary>
    public static IReadOnlyList<string> Moved(IReadOnlyList<string> ids, int from, int to)
    {
        ArgumentNullException.ThrowIfNull(ids);
        var output = new List<string>(ids);
        if (from < 0 || from >= output.Count || to < 0 || to >= output.Count || from == to)
        {
            return output;
        }
        var id = output[from];
        output.RemoveAt(from);
        output.Insert(to, id);
        return output;
    }

    /// <summary>
    /// rules.AppliesToAccount: an enabled rule runs on the account's mail
    /// (its own or one for every account): Apply Rules… of the account's
    /// folders is offered then.
    /// </summary>
    public static bool AppliesToAccount(IEnumerable<Rule> rules, AccountId accountId)
    {
        ArgumentNullException.ThrowIfNull(rules);
        return rules.Any(r => r.Enabled && (r.AccountId is null || r.AccountId == accountId));
    }

    /// <summary>
    /// The mail accounts a rule can be for (docs/api.md §4.21): imap and
    /// graph, never an issue tracker's, a CalDAV or a CRM account, in the
    /// order given.
    /// </summary>
    public static IReadOnlyList<Account> MailAccounts(IEnumerable<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        return [.. accounts.Where(a => a.Config.ProtocolKind.Value is AccountKind.Imap or AccountKind.Graph)];
    }

    /// <summary>rules.CanApplyIn: Apply Rules… is offered for a folder of a mail account: one that holds messages, not the outbox or the drafts.</summary>
    public static bool CanApplyIn(Folder f)
    {
        ArgumentNullException.ThrowIfNull(f);
        return f.Selectable && IsRealFolder(f) && f.Role.Value != FolderRole.Outbox && f.Role.Value != FolderRole.Drafts;
    }

    /// <summary>rules.CanCreateFrom: Create Rule from Message… is offered for a message in a folder of the role: not in the outbox, the drafts or the sent mail.</summary>
    public static bool CanCreateFrom(FolderRole role) =>
        role.Value != FolderRole.Outbox && role.Value != FolderRole.Drafts && role.Value != FolderRole.Sent;

    /// <summary>
    /// rules.MoveFolders: the folders a Move To action offers, from the
    /// account's folders in the sidebar's order: the selectable ones other
    /// than the outbox, the drafts, an \All folder and an issue tracker's
    /// views.
    /// </summary>
    public static IReadOnlyList<Folder> MoveFolders(IEnumerable<Folder> folders)
    {
        ArgumentNullException.ThrowIfNull(folders);
        var output = new List<Folder>();
        foreach (var f in folders)
        {
            if (!f.Selectable || !IsRealFolder(f))
            {
                continue;
            }
            if (f.Role.Value is FolderRole.Outbox or FolderRole.Drafts or FolderRole.All)
            {
                continue;
            }
            output.Add(f);
        }
        return output;
    }

    private static bool IsRealFolder(Folder f) => f.Virtual is not { Value.Length: > 0 };

    private static bool IsPlacing(RuleActionKind k) =>
        k.Value is RuleActionKind.Move or RuleActionKind.Junk or RuleActionKind.Trash;

    private static bool OperatorOf(RuleField f, RuleOperator op) => OperatorsFor(f).Contains(op);

    // cleanText: Clean that lets line breaks and tabs through (the text of an automatic reply).
    private static bool CleanText(string s) =>
        Clean(s.Replace("\r\n", "", StringComparison.Ordinal).Replace("\n", "", StringComparison.Ordinal).Replace("\t", "", StringComparison.Ordinal));

    private static string OutgoingKey(RuleInput input)
    {
        var b = new StringBuilder();
        foreach (var k in new[] { RuleActionKind.Forward, RuleActionKind.AutoReply })
        {
            foreach (var a in input.Actions)
            {
                if (a.Kind.Value == k)
                {
                    b.Append(k).Append('\0').Append(a.To).Append('\0').Append(a.Subject).Append('\0').Append(a.Text).Append('\0');
                }
            }
        }
        return b.ToString();
    }

    private static int ByteCount(string s) => Encoding.UTF8.GetByteCount(s);

    // truncate: s cut to at most n UTF-8 bytes on a character boundary.
    private static string Truncate(string s, int n)
    {
        if (ByteCount(s) <= n)
        {
            return s;
        }
        var bytes = 0;
        var b = new StringBuilder();
        foreach (var r in s.EnumerateRunes())
        {
            var len = r.Utf8SequenceLength;
            if (bytes + len > n)
            {
                break;
            }
            bytes += len;
            b.Append(r.ToString());
        }
        return b.ToString();
    }
}
