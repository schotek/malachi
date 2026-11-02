// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Integration+Rules.swift (AppRules,
// wireRules); GTK: ui/internal/window/rules_notify.go and rules_entry.go
// (docs/plans/rules.md R14, T2).
//
// The application's mail rules: one RulesController over the daemon
// (RpcRulesCaller over the app's client) or the invented rules of
// MALACHI_RULES_SAMPLES=1 (RulesSampleCaller, whose writes come back as
// Notified, handed on as the daemon's notify.rulesChanged), asked again on
// every notify.rulesChanged and every new connection (a restarted daemon),
// and the ways in to the rule editor: Add Rule… of the Rules page, Edit… of
// its rows and Create Rule from Message… of a message's context menu; and
// Apply Rules… of a folder's context menu. The editor lists the mail
// accounts and their folders as the main window's mailbox knows them (in
// the sidebar's order); a rule is made for the account the main window
// shows, if it is a mail account. Nothing here logs a rule: a rule holds
// the user's addresses and the words they filter by.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Rules;
using Microsoft.UI.Xaml;

namespace Malachi.App.MailRules;

/// <summary>The mail rules of the whole application (macOS <c>AppRules</c>).</summary>
public sealed class AppRules : IDisposable
{
    private readonly AppState state;
    private readonly List<IDisposable> tokens = [];
    private readonly RulesSampleCaller? samples;

    /// <summary>The mail rules of <paramref name="state"/>.</summary>
    public AppRules(AppState state)
    {
        ArgumentNullException.ThrowIfNull(state);
        this.state = state;
        if (RulesSampleCaller.SamplesOn)
        {
            samples = new RulesSampleCaller(TimeProvider.System.GetUtcNow());
            Caller = samples;
            // The samples' writes, as the daemon's notification, on the UI thread.
            samples.Notified += (_, _) => state.Dispatcher.TryEnqueue(Controller.Refresh);
        }
        else
        {
            Caller = new RpcRulesCaller(state.Client);
        }
        Controller = new RulesController(Caller);
        tokens.Add(state.Notifications.AddRulesChanged(Controller.Refresh));
        tokens.Add(state.Notifications.AddConnectionState(s =>
        {
            if (s is ConnectionState.Connected or ConnectionState.InfoFailed)
            {
                Controller.Refresh();
            }
        }));
        if (samples is not null)
        {
            Controller.Refresh();
        }
    }

    /// <summary>The daemon the rules controllers call.</summary>
    public IRulesCaller Caller { get; }

    /// <summary>The rules as last listed, and the actions on them.</summary>
    public RulesController Controller { get; }

    /// <summary>The invented rules of MALACHI_RULES_SAMPLES=1.</summary>
    public bool Samples => samples is not null;

    /// <summary>The mail accounts (imap, graph; enabled or not), as the main window's mailbox lists them.</summary>
    public IReadOnlyList<Account> MailAccounts =>
        state.Integration is { } integration ? RuleBook.MailAccounts(integration.Mailbox.Model.Accounts) : [];

    /// <summary>The name of a mail account for a rule's row; "" for an account that is gone.</summary>
    public string AccountName(AccountId id) =>
        MailAccounts.FirstOrDefault(a => a.Id == id) is { } account ? ContactBook.AccountName(account) : "";

    /// <summary>Add Rule…: the editor of a new rule over <paramref name="owner"/>, for the account the main window shows.</summary>
    public RuleEditorWindow? AddRule(Window? owner) => Edit(owner, RuleBook.NewForm(ShownAccount()), null);

    /// <summary>Edit…: the editor of the stored rule <paramref name="id"/>; null when it is gone.</summary>
    public RuleEditorWindow? EditRule(Window? owner, string id) =>
        Controller.Find(id) is { } rule ? Edit(owner, RuleBook.FromRule(rule), rule) : null;

    /// <summary>Create Rule from Message…: the editor of a rule prefilled from <paramref name="message"/> (RuleBook.FromMessage).</summary>
    public RuleEditorWindow? CreateFromMessage(Window? owner, MessageSummary message)
    {
        ArgumentNullException.ThrowIfNull(message);
        return Edit(owner, RuleBook.FromMessage(message, message.AccountId), null);
    }

    /// <summary>
    /// Apply Rules… of a folder's menu: the question over the main window
    /// (null) or <paramref name="owner"/>, the progress as a toast, then the
    /// result as a toast (docs/plans/rules.md R14).
    /// </summary>
    public async Task ApplyAsync(Window? owner, Folder folder, IToasts toasts)
    {
        ArgumentNullException.ThrowIfNull(folder);
        ArgumentNullException.ThrowIfNull(toasts);
        var name = FolderTree.FolderTitle(folder);
        var asked = false;
        var result = await Controller.ApplyAsync(folder.AccountId, folder.Id, name, async question =>
        {
            var confirmed = await state.Alerts.ConfirmAsync(owner, question.Heading, question.Body, question.Confirm, L10n.T("Cancel"));
            asked = confirmed;
            if (confirmed)
            {
                // The call is under way: it may take a while on a large folder.
                toasts.Show(RulesText.ApplyingText, 0);
            }
            return confirmed;
        });
        if (!asked)
        {
            return;
        }
        toasts.Show(result.Text.Length > 0 ? result.Text : RulesText.ApplyFailedText);
    }

    /// <summary>Quit's Release: the routing goes.</summary>
    public void Dispose()
    {
        foreach (var t in tokens)
        {
            t.Dispose();
        }
        tokens.Clear();
    }

    // The editor lists the enabled mail accounts, and the rule's own when it is off.
    private RuleEditorWindow? Edit(Window? owner, RuleForm form, Rule? original)
    {
        List<Account> accounts = state.Integration is { } integration
            ? [.. RuleBook.MailAccounts(integration.Mailbox.Model.Accounts, enabledOnly: true)]
            : [];
        if (form.AccountId is { } own && accounts.All(a => a.Id != own) && MailAccounts.FirstOrDefault(a => a.Id == own) is { } off)
        {
            accounts.Add(off);
        }
        return RuleEditorWindow.Show(state, owner, Caller, form, original, accounts, FoldersOf, saved => Controller.Upsert(saved));
    }

    // The account the main window shows, when it is a mail account a rule can be for.
    private AccountId? ShownAccount()
    {
        if (state.Integration is not { } integration)
        {
            return null;
        }
        var shown = integration.Mailbox.Model.ListFolder?.Account;
        return shown is { } id && MailAccounts.Any(a => a.Id == id) ? id : null;
    }

    // An account's folders in the sidebar's order (its tree, nothing folded, no Favourites).
    private IReadOnlyList<Folder> FoldersOf(AccountId id)
    {
        if (state.Integration is not { } integration || integration.Mailbox.Model.Account(id) is not { } account)
        {
            return [];
        }
        IReadOnlyList<Folder> folders = integration.Mailbox.Model.Folders.TryGetValue(id, out var known) ? known : [];
        var ordered = new List<Folder>();
        foreach (var e in FolderTree.FolderTreeRows(account, folders, new CollapseState(), new FavouriteState()))
        {
            if (!e.Header && e.Folder is { } f)
            {
                ordered.Add(f);
            }
        }
        return ordered;
    }
}
