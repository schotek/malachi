// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The part of macos/Sources/MalachiCore/Controllers/MailboxController+List.swift
// (ListController) that ActionsController.swift uses (applyFlags,
// removeRows); GTK: ui/internal/window/actions.go and messages.go
// (model.applyFlags with refreshRows, removeRows, removeMessageRow) and
// undo.go (afterUndo's loadMessages, takeUndoReselect). See
// IActionsMailbox for why the actions name the members they use.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Undos;

namespace Malachi.Core.Controllers;

/// <summary>
/// The list half of the main window as <see cref="ActionsController"/> sees
/// it: the rows' flags and their optimistic removal. UI-thread-affine.
/// </summary>
public interface IActionsList
{
    /// <summary>
    /// Changes the flags of the given messages in the model, refreshes their
    /// rows and the actions, and returns the ids that actually changed (the
    /// <c>apply</c> step of actions.go <c>setSeenIDs</c> /
    /// <c>setFlaggedIDs</c>).
    /// </summary>
    IReadOnlyList<MessageId> ApplyFlags(IReadOnlyList<MessageId> ids, IReadOnlyList<Flag>? setFlags = null, IReadOnlyList<Flag>? clearFlags = null);

    /// <summary>
    /// Drops messages from the list in either mode and returns what puts them
    /// back after a failed move or delete (Swift's <c>Restore</c>; messages.go
    /// <c>removeRows</c>). A restore after the list moved on (a reload) does
    /// nothing.
    /// </summary>
    Action RemoveRows(IReadOnlyList<MessageId> ids);

    /// <summary>
    /// After an Undo (window/undo.go <c>afterUndo</c> and
    /// <c>takeUndoReselect</c>): lists the selected folder again and, once
    /// its first page arrived, selects the rows of the messages in
    /// <paramref name="back"/> (a message's row, or its conversation's;
    /// <see cref="Undo.ReselectKeys"/>); none of them listed, the selection
    /// stays as the reload leaves it.
    /// </summary>
    void ReloadSelecting(IReadOnlyList<Undo.Moved> back);
}
