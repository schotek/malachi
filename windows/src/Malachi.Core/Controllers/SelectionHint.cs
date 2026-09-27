// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MailboxController+List.swift
// (SelectionHint); GTK: ui/internal/window/threads.go (reconcileRows, its
// keep / neighbour / clear branches).

namespace Malachi.Core.Controllers;

/// <summary>
/// How the selection was reconciled with a new set of rows (threads.go
/// <c>reconcileRows</c>), handed to the view with the rows so it can mirror
/// the controller's <see cref="ListController.SelectedKey"/> afterwards.
/// </summary>
public enum SelectionHint
{
    /// <summary>
    /// The same key stays selected; when a member row folded away its
    /// conversation row takes over; otherwise the selection is dropped.
    /// </summary>
    Keep,

    /// <summary>
    /// As <see cref="Keep"/>, but a selected row that is gone hands the
    /// selection to the row now at its place (a removal).
    /// </summary>
    Neighbour,

    /// <summary>
    /// The list was emptied for another folder, mode or filter; the
    /// selection is dropped without looking.
    /// </summary>
    Clear,
}
