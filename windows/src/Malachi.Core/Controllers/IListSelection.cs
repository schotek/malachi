// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only seam: what macos/Sources/MalachiMail/Actions/
// MessageActionsController.swift asks of the ListController (selectedRow,
// selectedIDs) to act on the selection; GTK: window.go selectedRow and
// threads.go selectedIDs. ListController implements it; the router's
// tests answer it without a mailbox.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

/// <summary>The message list's selection, as the main window's actions see it.</summary>
public interface IListSelection
{
    /// <summary>The selected row, null when nothing is selected.</summary>
    ListRow? SelectedRow { get; }

    /// <summary>
    /// Runs <paramref name="done"/> with the selected row and every message
    /// it stands for: one, or all the folder members of a conversation row,
    /// fetched first when they are not known yet (threads.go <c>selectedIDs</c>).
    /// </summary>
    void SelectedIds(Action<ListRow, IReadOnlyList<MessageId>> done);
}
