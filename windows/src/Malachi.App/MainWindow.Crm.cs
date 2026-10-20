// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Integration+CRM.swift (wireCRM's
// main window: the refreshes on the list's selection, single and several
// rows, and on every message the cache loads) and
// MainWindow/MainWindowController.swift (the toolbar's CRM item, the
// subjects and the conversations of the selection); GTK: window/crm_actions.go
// of docs/eway.md §2.6 (written in T7). The main window's eWay-CRM: the
// button of the message pane's header bar, the list's context menu and the
// bar of the reading pane (Reader/MessageView), over one CrmController that
// acts on the selection: the shown message of each selected row; a
// conversation's row offers nothing (owner, Gate B 2026-10-17).

using System;
using System.Linq;
using Malachi.App.Crm;
using Malachi.App.Reader;
using Malachi.App.Shell;

namespace Malachi.App;

/// <summary>The main window's eWay-CRM.</summary>
public sealed partial class MainWindow
{
    /// <summary>
    /// The Integration and the reader exist: the eWay-CRM button and the
    /// context menu act on the list's selection.
    /// </summary>
    internal void AttachCrm(Integration integration, ReaderHub reader)
    {
        ArgumentNullException.ThrowIfNull(integration);
        ArgumentNullException.ThrowIfNull(reader);
        var list = integration.List;
        var c = CrmWindowSupport.Make(
            state,
            this,
            Toasts,
            subjects: () => [.. list.SelectedRows.Select(r => r.Message)],
            conversations: () => list.SelectedRows.Where(r => r.Thread).Select(r => r.Message.Id).ToHashSet());
        ListPane.Crm = c;
        var refresh = CrmWindowSupport.Bind(MessageCommands.CrmButton, c, state, this);
        list.ActionFlagsChanged += (_, _) => refresh();
        list.SelectedMessageChanged += (_, _) => refresh();
        list.SelectionCleared += (_, _) => refresh();
        list.SeveralSelected += (_, _) => refresh();
        // The pane's buttons changed, and with them what it needs.
        MessageCommands.CrmButton.SizeChanged += (_, _) => ApplyLayout();
    }
}
