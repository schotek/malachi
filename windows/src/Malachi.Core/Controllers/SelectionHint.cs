// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MailboxController+List.swift
// (SelectionHint); GTK: ui/internal/window/threads.go (reconcileRows, its
// keep / neighbour / clear branches).

namespace Malachi.Core.Controllers;

/// <summary>
/// How the selection was reconciled with a new set of rows (threads.go
/// <c>reconcileRows</c>), handed to the view with the rows so it can mirror
/// the controller's <see cref="ListController.SelectedKeys"/> afterwards.
/// </summary>
public enum SelectionHint
{
    /// <summary>
    /// The same keys stay selected; when a member row folded away its
    /// conversation row takes over; a key that is gone leaves the selection.
    /// </summary>
    Keep,

    /// <summary>
    /// As <see cref="Keep"/>, but when every selected row is gone the
    /// selection goes to the row now at the first one's place (a removal).
    /// </summary>
    Neighbour,

    /// <summary>
    /// The list was emptied for another folder, mode or filter; the
    /// selection is dropped without looking.
    /// </summary>
    Clear,
}
