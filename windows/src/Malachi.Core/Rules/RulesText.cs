// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/rules/text.go (PageTitle, PageDescription,
// EmptyTitle, EmptyDescription, AddRule, EditRule, RemoveRule,
// EnabledTooltip, Row, Rows, ScopeText, AllAccounts, StatusLine, ProblemOf,
// Question, DeleteQuestion, OutgoingQuestion, EditorTitle, NameLabel,
// AccountLabel, MatchLabel, MatchText, ActionsHeading, AddCondition,
// AddAction, RemoveRow, EnabledLabel, StopLabel, StopHint, FieldText,
// OperatorText, ValueText, ValuePlaceholder, ActionText, FolderPlaceholder,
// ForwardPlaceholder, ReplySubjectPlaceholder, ReplyTextPlaceholder,
// OutgoingHint, ProblemText, InvalidText, NotFoundText, SaveFailedText,
// TestButton, TestFolderLabel, TestResultText, ConditionText,
// ConditionsLine, ActionPhrase, ActionsLine, PrefillName,
// CreateFromMessageLabel, ApplyLabel, ApplyQuestion, ApplyingText,
// ApplyResultText, ApplyFailedText); Swift: macos/Sources/MalachiCore/Rules/
// (written at the same time).
//
// Every text of the Rules page, the editor, the questions and the two
// context menu items, with the GTK msgid as the key; Go's Translator is
// L10n, as in the other ports. A rule's strings may come from mail (a
// prefilled sender, a list id): they are formatted into a msgid, never
// concatenated into a sentence, and shown as plain text only.
//
// Windows: the class is RulesText so that the namespace Malachi.Core.Rules
// is not hidden; Go's DateText is a Func over DateTimeOffset, Names a
// record of two Funcs, and fmt.Sprintf(tr.T(…), x) is L10n.T(…, x).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Rules;

/// <summary>rules.Question: what a dialog asks; <see cref="Confirm"/> is the button that goes ahead (the other one is Cancel).</summary>
/// <param name="Heading">The heading.</param>
/// <param name="Body">The explanation.</param>
/// <param name="Confirm">The label of the button that goes ahead.</param>
/// <param name="Destructive">The button that goes ahead removes something.</param>
public sealed record RuleQuestion(string Heading, string Body, string Confirm, bool Destructive = false);

/// <summary>
/// rules.Names: the names a summary shows for ids: an account's ("" when it
/// is gone) and a folder's ("" when it is gone); a null function names
/// nothing.
/// </summary>
/// <param name="Account">An account's name.</param>
/// <param name="Folder">A folder's name.</param>
public sealed record RuleNames(Func<AccountId, string>? Account = null, Func<AccountId, FolderId, string>? Folder = null);

/// <summary>rules.Row: a rule of the page's list.</summary>
public sealed record RuleRow
{
    /// <summary>The rule's id.</summary>
    public required string Id { get; init; }

    /// <summary>The switch.</summary>
    public bool Enabled { get; init; }

    /// <summary>The rule's name.</summary>
    public required string Title { get; init; }

    /// <summary>The conditions in one line (<see cref="RulesText.ConditionsLine"/>).</summary>
    public required string Conditions { get; init; }

    /// <summary>The actions in one line (<see cref="RulesText.ActionsLine"/>).</summary>
    public required string Actions { get; init; }

    /// <summary>The account the rule is for, or All Mail Accounts.</summary>
    public required string Scope { get; init; }

    /// <summary>How often the rule acted (<see cref="RulesText.StatusLine"/>).</summary>
    public required string Status { get; init; }

    /// <summary>Why the daemon turned it off; "" when it did not; shown as a warning.</summary>
    public string Problem { get; init; } = "";

    /// <summary>The rule sends mail: the row shows a sending icon.</summary>
    public bool Outgoing { get; init; }
}

/// <summary>ui/internal/rules (text.go): the texts of the mail rules.</summary>
public static class RulesText
{
    // The Rules page of Preferences

    /// <summary>rules.PageTitle: the Preferences page (after Integrations) and its tab.</summary>
    public static string PageTitle =>
        // TRANSLATORS: the Preferences page of the mail rules (filters that move, mark, forward or answer new mail).
        L10n.T("Rules");

    /// <summary>rules.PageDescription: the text over the list of rules.</summary>
    public static string PageDescription =>
        L10n.T("Rules act on new mail in the inbox as it arrives, in the order of this list. Malachi Mail runs them on this computer, only while it is running.");

    /// <summary>rules.EmptyTitle: the page without rules.</summary>
    public static string EmptyTitle => L10n.T("No Rules");

    /// <summary>rules.EmptyDescription: explains rules on the empty page.</summary>
    public static string EmptyDescription =>
        L10n.T("Move, mark or forward new messages automatically. You can also create a rule from a message in its context menu.");

    /// <summary>rules.AddRule: the page's button that opens the editor of a new rule.</summary>
    public static string AddRule => L10n.T("Add Rule…");

    /// <summary>rules.EditRule: a row's button (and its tooltip) that opens the editor.</summary>
    public static string EditRule => L10n.T("Edit Rule…");

    /// <summary>rules.RemoveRule: a row's button (and its tooltip) that asks <see cref="DeleteQuestion"/>.</summary>
    public static string RemoveRule => L10n.T("Remove Rule");

    /// <summary>rules.EnabledTooltip: the tooltip of a row's switch.</summary>
    public static string EnabledTooltip => L10n.T("Turn the rule on or off");

    /// <summary>rules.Rows: the page's rows in the rules' order.</summary>
    public static IReadOnlyList<RuleRow> Rows(IEnumerable<Rule> rules, RuleNames names, Func<DateTimeOffset, string>? date)
    {
        ArgumentNullException.ThrowIfNull(rules);
        ArgumentNullException.ThrowIfNull(names);
        var output = new List<RuleRow>();
        foreach (var r in rules)
        {
            output.Add(new RuleRow
            {
                Id = r.Id,
                Enabled = r.Enabled,
                Title = r.Name,
                Conditions = ConditionsLine(r),
                Actions = ActionsLine(r, r.AccountId, names),
                Scope = ScopeText(r.AccountId, names),
                Status = StatusLine(r, date),
                Problem = ProblemOf(r.Problem),
                Outgoing = r.Outgoing,
            });
        }
        return output;
    }

    /// <summary>rules.ScopeText: the account a rule is for.</summary>
    public static string ScopeText(AccountId? id, RuleNames names)
    {
        ArgumentNullException.ThrowIfNull(names);
        if (id is not { Value.Length: > 0 } account)
        {
            return AllAccounts;
        }
        if (names.Account is { } f && f(account) is { Length: > 0 } n)
        {
            return n;
        }
        return L10n.T("Removed account");
    }

    /// <summary>rules.AllAccounts: the account choice of a rule for every mail account.</summary>
    public static string AllAccounts => L10n.T("All Mail Accounts");

    /// <summary>rules.StatusLine: how often the rule acted and when last, and how many outgoing messages its protections held back.</summary>
    public static string StatusLine(Rule r, Func<DateTimeOffset, string>? date)
    {
        ArgumentNullException.ThrowIfNull(r);
        string line;
        if (r.Applied == 0)
        {
            line = L10n.T("Not used yet");
        }
        else if (r.LastAppliedAt is { } last && date is not null)
        {
            line = L10n.N("Used on %d message, last %s", "Used on %d messages, last %s", r.Applied, r.Applied, date(last));
        }
        else
        {
            line = L10n.N("Used on %d message", "Used on %d messages", r.Applied);
        }
        if (r.Skipped > 0)
        {
            // TRANSLATORS: after the status line of a rule: messages it would have forwarded or answered and did not (bulk mail, an automatic message, the user's own address, a limit).
            line = L10n.N("%s · %d outgoing message held back", "%s · %d outgoing messages held back", r.Skipped, line, r.Skipped);
        }
        return line;
    }

    /// <summary>rules.ProblemOf: why the daemon turned a rule off; "" for none.</summary>
    public static string ProblemOf(RuleProblem? p) => p?.Value switch
    {
        RuleProblem.FolderGone => L10n.T("The folder this rule moves messages to no longer exists, so the rule was turned off."),
        RuleProblem.SendFailed => L10n.T("This rule could not send a message, so it was turned off."),
        _ => "",
    };

    /// <summary>rules.DeleteQuestion: asks before Remove deletes a rule.</summary>
    public static RuleQuestion DeleteQuestion(string name) => new(
        L10n.T("Remove the Rule “%s”?", name),
        L10n.T("Messages it already moved or marked stay as they are."),
        L10n.T("Remove"),
        Destructive: true);

    /// <summary>
    /// rules.OutgoingQuestion: asks before a rule that sends mail is saved
    /// enabled or turned on (<see cref="RuleBook.NeedsConfirm"/>). It names
    /// where forward sends and says that replies go out on their own.
    /// </summary>
    public static RuleQuestion OutgoingQuestion(RuleInput input)
    {
        ArgumentNullException.ThrowIfNull(input);
        var to = "";
        var reply = false;
        foreach (var a in input.Actions)
        {
            switch (a.Kind.Value)
            {
                case RuleActionKind.Forward:
                    to = a.To ?? "";
                    break;
                case RuleActionKind.AutoReply:
                    reply = true;
                    break;
            }
        }
        string body;
        if (to.Length > 0 && reply)
        {
            body = L10n.T("This rule forwards every matching message to %s and answers its sender, without asking you. Bulk mail, automatic messages and your own messages are never forwarded or answered.", to);
        }
        else if (to.Length > 0)
        {
            body = L10n.T("This rule forwards every matching message to %s without asking you. Bulk mail, automatic messages and your own messages are never forwarded.", to);
        }
        else
        {
            body = L10n.T("This rule answers the sender of every matching message without asking you, at most once in four days per sender. Bulk mail, automatic messages and your own messages are never answered.");
        }
        return new RuleQuestion(OutgoingTitle, body, L10n.T("Turn On"));
    }

    /// <summary>The heading of <see cref="OutgoingQuestion"/>; the tooltip and the name of the sending icon of a rule's row.</summary>
    public static string OutgoingTitle => L10n.T("Send Mail Automatically?");

    // The editor

    /// <summary>rules.EditorTitle: the editor's title: New Rule or Edit Rule.</summary>
    public static string EditorTitle(RuleForm f)
    {
        ArgumentNullException.ThrowIfNull(f);
        if (f.Id.Length == 0)
        {
            return L10n.T("New Rule");
        }
        // TRANSLATORS: the title of the editor of a stored mail rule.
        return L10n.T("Edit Rule");
    }

    /// <summary>rules.NameLabel: the label of the rule's name field.</summary>
    public static string NameLabel => L10n.T("Name");

    /// <summary>rules.AccountLabel: the label of the account choice (the accounts, then <see cref="AllAccounts"/>).</summary>
    public static string AccountLabel =>
        // TRANSLATORS: the label of the account choice of the rule editor.
        L10n.T("Apply to");

    /// <summary>rules.MatchLabel: the label of the choice of how conditions combine.</summary>
    public static string MatchLabel => L10n.T("When a new message arrives");

    /// <summary>rules.MatchText: a choice of how conditions combine.</summary>
    public static string MatchText(RuleMatch m) => m.Value == RuleMatch.Any
        ? L10n.T("and any of these conditions is met")
        : L10n.T("and all of these conditions are met");

    /// <summary>rules.ActionsHeading: the heading over the actions.</summary>
    public static string ActionsHeading => L10n.T("Do the following");

    /// <summary>rules.AddCondition: the button under the condition rows.</summary>
    public static string AddCondition => L10n.T("Add Condition");

    /// <summary>rules.AddAction: the button (a menu of <see cref="RuleBook.AvailableActions"/>) under the actions.</summary>
    public static string AddAction => L10n.T("Add Action");

    /// <summary>rules.RemoveRow: the tooltip of a row's remove button.</summary>
    public static string RemoveRow => L10n.T("Remove");

    /// <summary>rules.EnabledLabel: the editor's switch.</summary>
    public static string EnabledLabel => L10n.T("Rule is on");

    /// <summary>rules.StopLabel: the editor's check box of <see cref="RuleInput.Stop"/>.</summary>
    public static string StopLabel => L10n.T("Stop processing more rules");

    /// <summary>rules.StopHint: the line under <see cref="StopLabel"/>.</summary>
    public static string StopHint => L10n.T("Rules below this one do not run on a message this rule matched.");

    /// <summary>rules.FieldText: a condition field's choice.</summary>
    public static string FieldText(RuleField f) => f.Value switch
    {
        RuleField.From => L10n.C("rule field", "From"),
        RuleField.To => L10n.C("rule field", "To"),
        RuleField.Cc => L10n.C("rule field", "Cc"),
        RuleField.ToOrCc => L10n.C("rule field", "To or Cc"),
        RuleField.Subject => L10n.C("rule field", "Subject"),
        RuleField.Body => L10n.C("rule field", "Message text"),
        // TRANSLATORS: the List-Id of mailing-list mail.
        RuleField.ListId => L10n.C("rule field", "Mailing list ID"),
        RuleField.Bulk => L10n.C("rule field", "Bulk mail"),
        RuleField.HasAttachments => L10n.C("rule field", "Attachments"),
        _ => f.Value ?? "",
    };

    /// <summary>rules.OperatorText: an operator's choice; it reads after <see cref="FieldText"/> ("From contains …").</summary>
    public static string OperatorText(RuleField f, RuleOperator op)
    {
        if (RuleBook.ValueKindOf(f) == RuleValueKind.Bool)
        {
            // TRANSLATORS: Attachments [are] yes/no in the rule editor.
            return L10n.C("rule operator", "are");
        }
        return op.Value switch
        {
            RuleOperator.Contains => L10n.C("rule operator", "contains"),
            RuleOperator.NotContains => L10n.C("rule operator", "does not contain"),
            RuleOperator.Is => L10n.C("rule operator", "is"),
            RuleOperator.IsNot => L10n.C("rule operator", "is not"),
            RuleOperator.StartsWith => L10n.C("rule operator", "begins with"),
            RuleOperator.EndsWith => L10n.C("rule operator", "ends with"),
            RuleOperator.DomainIs => L10n.C("rule operator", "is from domain"),
            _ => op.Value ?? "",
        };
    }

    /// <summary>rules.ValueText: a value's choice of a bulk or a hasAttachments condition; a text value is itself.</summary>
    public static string ValueText(RuleField f, string value)
    {
        switch (RuleBook.ValueKindOf(f))
        {
            case RuleValueKind.Bulk:
                switch (value)
                {
                    case RuleValues.BulkAny:
                        return L10n.C("bulk kind", "any kind");
                    case BulkKind.Newsletter:
                        return L10n.C("bulk kind", "newsletter");
                    case BulkKind.List:
                        return L10n.C("bulk kind", "mailing list");
                    case BulkKind.Automated:
                        return L10n.C("bulk kind", "automated message");
                }
                break;
            case RuleValueKind.Bool:
                return value == RuleBook.ValueFalse ? L10n.C("attachments", "none") : L10n.C("attachments", "present");
        }
        return value;
    }

    /// <summary>rules.ValuePlaceholder: the placeholder of a condition's text value.</summary>
    public static string ValuePlaceholder(RuleField f, RuleOperator op)
    {
        if (op.Value == RuleOperator.DomainIs)
        {
            return L10n.T("example.com");
        }
        if (RuleBook.IsAddressField(f))
        {
            return L10n.T("Name or address");
        }
        if (f.Value == RuleField.ListId)
        {
            return L10n.T("list.example.com");
        }
        return L10n.T("Text");
    }

    /// <summary>rules.ActionText: an action kind's choice (and an item of Add Action).</summary>
    public static string ActionText(RuleActionKind k) => k.Value switch
    {
        RuleActionKind.Move => L10n.C("rule action", "Move to folder"),
        RuleActionKind.MarkRead => L10n.C("rule action", "Mark as read"),
        RuleActionKind.Flag => L10n.C("rule action", "Star"),
        RuleActionKind.Trash => L10n.C("rule action", "Move to Trash"),
        RuleActionKind.Junk => L10n.C("rule action", "Move to Junk"),
        RuleActionKind.Forward => L10n.C("rule action", "Forward to"),
        RuleActionKind.AutoReply => L10n.C("rule action", "Reply automatically"),
        _ => k.Value ?? "",
    };

    /// <summary>rules.FolderPlaceholder: the Move To action's folder choice before a folder is chosen.</summary>
    public static string FolderPlaceholder => L10n.T("Choose a Folder");

    /// <summary>rules.ForwardPlaceholder: the placeholder of forward's address.</summary>
    public static string ForwardPlaceholder => L10n.T("name@example.com");

    /// <summary>rules.ReplySubjectPlaceholder: the placeholder of autoReply's subject.</summary>
    public static string ReplySubjectPlaceholder => L10n.T("Subject (Re: and the original subject if empty)");

    /// <summary>rules.ReplyTextPlaceholder: the placeholder of autoReply's text.</summary>
    public static string ReplyTextPlaceholder => L10n.T("Reply text");

    /// <summary>rules.OutgoingHint: the line under an outgoing action in the editor.</summary>
    public static string OutgoingHint(RuleActionKind k) => k.Value == RuleActionKind.AutoReply
        ? L10n.T("Each sender gets at most one automatic reply in four days. Bulk mail, automatic messages and your own messages are never answered.")
        : L10n.T("The message is sent whole as an attachment. Bulk mail, automatic messages and your own messages are never forwarded.");

    /// <summary>rules.ProblemText: the text of a problem Validate found, shown under its row.</summary>
    public static string ProblemText(RuleFormProblem p)
    {
        ArgumentNullException.ThrowIfNull(p);
        return p.Kind switch
        {
            RuleFormProblemKind.NameMissing => L10n.T("Enter a name for the rule."),
            RuleFormProblemKind.TooLong => L10n.T("This is too long."),
            RuleFormProblemKind.BadCharacters => L10n.T("This contains characters that cannot be used."),
            RuleFormProblemKind.NoCondition => L10n.T("Add at least one condition."),
            RuleFormProblemKind.TooManyConditions =>
                L10n.N("A rule can have at most %d condition.", "A rule can have at most %d conditions.", RuleLimits.MaxRuleConditions),
            RuleFormProblemKind.ValueMissing => L10n.T("Enter a value."),
            RuleFormProblemKind.BadOperator => L10n.T("Choose how to compare."),
            RuleFormProblemKind.NoAction => L10n.T("Add at least one action."),
            RuleFormProblemKind.TooManyActions =>
                L10n.N("A rule can have at most %d action.", "A rule can have at most %d actions.", RuleLimits.MaxRuleActions),
            RuleFormProblemKind.ActionTwice => L10n.T("This action is already in the rule."),
            RuleFormProblemKind.OnePlace => L10n.T("A rule can move a message to only one place."),
            RuleFormProblemKind.MoveNeedsAccount => L10n.T("Choose an account to move messages to one of its folders."),
            RuleFormProblemKind.FolderMissing => L10n.T("Choose a folder."),
            RuleFormProblemKind.AddressInvalid => L10n.T("Enter one valid email address."),
            RuleFormProblemKind.ReplyMissing => L10n.T("Enter the text of the reply."),
            _ => "",
        };
    }

    /// <summary>rules.InvalidText: the text of the daemon's invalidArgument with <see cref="RuleInvalidData"/>, for what the editor did not catch.</summary>
    public static string InvalidText(RuleInvalidData d)
    {
        ArgumentNullException.ThrowIfNull(d);
        return d.Field switch
        {
            "name" => L10n.T("Enter a name for the rule."),
            "accountId" => L10n.T("The account of this rule no longer exists."),
            "confirmOutgoing" => L10n.T("Rules that send mail need your confirmation."),
            "conditions" => L10n.T("A condition of this rule cannot be used."),
            "actions" => L10n.T("An action of this rule cannot be used."),
            _ => L10n.T("This rule cannot be saved."),
        };
    }

    /// <summary>rules.NotFoundText: ruleNotFound (another window or client removed it).</summary>
    public static string NotFoundText => L10n.T("This rule no longer exists.");

    /// <summary>rules.SaveFailedText: the toast of a failed rules.create or rules.update for any other error.</summary>
    public static string SaveFailedText => L10n.T("The rule could not be saved.");

    /// <summary>rules.TestButton: the editor's button that calls rules.test.</summary>
    public static string TestButton =>
        // TRANSLATORS: a button of the rule editor: shows which stored messages of a folder the rule would match.
        L10n.T("Try It");

    /// <summary>rules.TestFolderLabel: the label of the folder Try It looks in (the inbox of the rule's account, or of the first account, by default).</summary>
    public static string TestFolderLabel => L10n.T("Try in");

    /// <summary>rules.TestResultText: the result of rules.test.</summary>
    public static string TestResultText(int count) => count == 0
        ? L10n.T("No message in this folder matches.")
        : L10n.N("%d message in this folder matches.", "%d messages in this folder match.", count);

    // Summaries

    /// <summary>rules.ConditionText: one condition as a phrase: field, operator, value.</summary>
    public static string ConditionText(RuleCondition c)
    {
        ArgumentNullException.ThrowIfNull(c);
        var value = ValueText(c.Field, c.Value);
        if (RuleBook.ValueKindOf(c.Field) == RuleValueKind.TextField)
        {
            // TRANSLATORS: a condition of a rule in its summary: field, comparison, quoted value ("From contains “acme”").
            return L10n.Format(L10n.C("rule condition", "%s %s “%s”"), FieldText(c.Field), OperatorText(c.Field, c.Op), value);
        }
        // TRANSLATORS: a condition with a chosen value ("Bulk mail is newsletter").
        return L10n.Format(L10n.C("rule condition", "%s %s %s"), FieldText(c.Field), OperatorText(c.Field, c.Op), value);
    }

    /// <summary>rules.ConditionsLine: a rule's conditions in one line, joined by the rule's match.</summary>
    public static string ConditionsLine(RuleInput input)
    {
        ArgumentNullException.ThrowIfNull(input);
        var parts = input.Conditions.Select(ConditionText);
        // TRANSLATORS: joins the conditions of a rule that all have to hold (with spaces around it).
        var separator = L10n.C("all conditions", " and ");
        if (input.Match.Value == RuleMatch.Any)
        {
            // TRANSLATORS: joins the conditions of a rule of which one has to hold (with spaces around it).
            separator = L10n.C("any condition", " or ");
        }
        return string.Join(separator, parts);
    }

    /// <summary>rules.ActionPhrase: one action as a phrase.</summary>
    public static string ActionPhrase(RuleAction a, AccountId? account, RuleNames names)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(names);
        switch (a.Kind.Value)
        {
            case RuleActionKind.Move:
                var name = "";
                if (names.Folder is { } f && account is { } acc && a.FolderId is { } folder)
                {
                    name = f(acc, folder);
                }
                return name.Length == 0 ? L10n.T("move to a missing folder") : L10n.T("move to “%s”", name);
            case RuleActionKind.MarkRead:
                return L10n.T("mark as read");
            case RuleActionKind.Flag:
                return L10n.T("star");
            case RuleActionKind.Trash:
                return L10n.T("move to Trash");
            case RuleActionKind.Junk:
                return L10n.T("move to Junk");
            case RuleActionKind.Forward:
                return L10n.T("forward to %s", a.To ?? "");
            case RuleActionKind.AutoReply:
                return L10n.T("reply automatically");
        }
        return a.Kind.Value ?? "";
    }

    /// <summary>rules.ActionsLine: a rule's actions in one line in the order the daemon runs them, with stop at the end.</summary>
    public static string ActionsLine(RuleInput input, AccountId? account, RuleNames names)
    {
        ArgumentNullException.ThrowIfNull(input);
        var parts = new List<string>();
        foreach (var k in RuleBook.ActionKinds)
        {
            foreach (var a in input.Actions)
            {
                if (a.Kind == k)
                {
                    parts.Add(ActionPhrase(a, account, names));
                }
            }
        }
        if (input.Stop)
        {
            parts.Add(L10n.T("stop processing more rules"));
        }
        // TRANSLATORS: joins the actions of a rule in its summary.
        return string.Join(L10n.C("rule actions", ", "), parts);
    }

    /// <summary>rules.PrefillName: the name of a rule made from a message: after its sender.</summary>
    public static string PrefillName(string sender) =>
        // TRANSLATORS: the name of a rule made from a message; %s is the sender's name or address.
        L10n.T("Messages from %s", sender);

    // The menus and Apply Rules

    /// <summary>rules.CreateFromMessageLabel: the item of a message's context menu.</summary>
    public static string CreateFromMessageLabel => L10n.T("Create Rule from Message…");

    /// <summary>rules.ApplyLabel: the item of a folder's context menu.</summary>
    public static string ApplyLabel => L10n.T("Apply Rules…");

    /// <summary>rules.ApplyQuestion: asks before Apply Rules… runs the account's rules on the folder.</summary>
    public static RuleQuestion ApplyQuestion(string folder) => new(
        L10n.T("Apply Rules to “%s”?", folder),
        L10n.T("The rules that are on run on the messages of this folder kept on this computer. Rules do not forward or reply to these messages."),
        L10n.T("Apply"));

    /// <summary>rules.ApplyingText: the progress line while rules.apply runs.</summary>
    public static string ApplyingText => L10n.T("Applying rules");

    /// <summary>rules.ApplyResultText: the toast after rules.apply.</summary>
    public static string ApplyResultText(RulesApplyResult r)
    {
        ArgumentNullException.ThrowIfNull(r);
        return r.Changed == 0
            ? L10n.T("The rules changed no message.")
            : L10n.N("The rules changed %d message.", "The rules changed %d messages.", r.Changed);
    }

    /// <summary>rules.ApplyFailedText: the toast of a failed rules.apply.</summary>
    public static string ApplyFailedText => L10n.T("The rules could not be applied.");
}
