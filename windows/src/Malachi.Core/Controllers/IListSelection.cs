// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only seam: what macos/Sources/MalachiMail/Actions/
// MessageActionsController.swift asks of the ListController (selectedRow,
// selectedRows, selectedIDs) to act on the selection; GTK: selection.go
// selectedRow, selectedRows and selectedIDs. ListController implements it;
// the router's tests answer it without a mailbox.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

/// <summary>The message list's selection, as the main window's actions see it.</summary>
public interface IListSelection
{
    /// <summary>The selected rows in list order, empty for none (several are of one account).</summary>
    IReadOnlyList<ListRow> SelectedRows { get; }

    /// <summary>The selected row when it is one; null for none or several.</summary>
    ListRow? SelectedRow { get; }

    /// <summary>
    /// Runs <paramref name="done"/> with the selected rows and every message
    /// they stand for: one each, or all the folder members of a conversation
    /// row, fetched first when they are not known yet (selection.go
    /// <c>selectedIDs</c>).
    /// </summary>
    void SelectedIds(Action<IReadOnlyList<ListRow>, IReadOnlyList<MessageId>> done);
}
