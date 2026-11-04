// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Integration+CRM.swift (wireCRM);
// GTK: the window's CRM wiring of docs/eway.md §2.6 (written in T7). The
// shared CrmState reads the mailbox's accounts, the folder role of a
// message and the cache's full messages; it tells every window to redraw
// its CRM controls on the daemon's notify.messagesChanged and
// notify.accountsChanged (after the mailbox and the cache took them: the
// list's crmSaved and message.get's records are the truth), on every
// message the cache loads and once the accounts are loaded. The main
// window's button, menus and bar are MainWindow.Crm.cs.

using Malachi.App.Reader;

namespace Malachi.App.Shell;

/// <summary>The eWay-CRM half of the Integration.</summary>
public sealed partial class Integration
{
    private void WireCrm(ReaderHub hub)
    {
        var crm = state.Crm;
        crm.Accounts = () => Mailbox.Model.Accounts;
        crm.RoleOf = hub.Services.Router.FolderRoleOf;
        crm.Loaded = id => Cache.Loaded(id)?.Msg;
        Mailbox.AccountsLoaded += (_, _) => crm.NotifyChanged();
        Mailbox.MessagesChanged += (_, _) => crm.NotifyChanged();
        Cache.MessageLoaded += (_, _) => crm.NotifyChanged();
        tokens.Add(state.Notifications.AddAccountsChanged(crm.NotifyChanged));
        mainWindow.AttachCrm(this, hub);
    }
}
