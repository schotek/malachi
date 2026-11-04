// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/AppState.swift (crm) and
// App/Integration+CRM.swift (wireCRM: the accounts, the folder roles and
// the cache the shared state reads; the refreshes on the daemon's
// notifications); GTK: the window's CRM state of docs/eway.md §2.6
// (written in T7).
//
// eWay-CRM (docs/eway.md): the accounts, the messages a save or a removal
// runs for and a call's own answer for a few seconds, shared by every
// window's CrmController (Crm/CrmWindowSupport). The Integration wires its
// readers once the mailbox and the cache exist (WireCrm); every window then
// redraws its CRM controls on CrmState.Changed, which the daemon's
// notify.messagesChanged and notify.accountsChanged, a loaded message and
// a call that starts or ends raise.

using Malachi.Core.Controllers;

namespace Malachi.App.Shell;

/// <summary>The application's eWay-CRM state.</summary>
public sealed partial class AppState
{
    /// <summary>
    /// eWay-CRM: the accounts, the messages a save or removal runs for,
    /// shared by every window's <see cref="CrmController"/>.
    /// </summary>
    public CrmState Crm { get; } = new();
}
