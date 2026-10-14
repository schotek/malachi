// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/BoardReplyPanes.swift
// (BoardReplyPane); GTK: ui/internal/boardreply/panes.go (Pane). Swift's
// settle() is SettleAsync; Go's Settle(done) calls back instead.

using System.Threading.Tasks;

namespace Malachi.Core.Controllers;

/// <summary>
/// What <see cref="BoardReplyPanes"/> needs of a compose pane editing a
/// case's suggested reply (the inline compose pane in the app, fakes in the
/// tests), over a <see cref="ComposeDraftController"/> with
/// <see cref="DraftOwner.Board"/>. Called on the UI thread.
/// </summary>
public interface IBoardReplyPane
{
    /// <summary>Edits not yet saved, or a save under way.</summary>
    bool HasUnsavedText { get; }

    /// <summary>Send was pressed and has not answered yet.</summary>
    bool IsSending { get; }

    /// <summary>The draft was deleted elsewhere (draftNotFound).</summary>
    bool IsLost { get; }

    /// <summary>The reply's subject (or a comment's title), for a toast about a pane out of sight.</summary>
    string ReplyTitle { get; }

    /// <summary>
    /// Saves everything typed (flushes the editor, saves while there are
    /// unsaved edits or a save is under way; waits for a send under way).
    /// True when nothing typed is unsaved. Never cleans up: the pane stays
    /// usable either way (<see cref="ComposeDraftController.SettleAsync"/>).
    /// </summary>
    Task<bool> SettleAsync();

    /// <summary>
    /// Ends the pane for good once nothing is at stake (sent, discarded, or
    /// settled): the draft controller cleans up. Idempotent.
    /// </summary>
    void Close();

    /// <summary>Forgets the pane without saving: its draft is gone. Idempotent.</summary>
    void Abandon();
}
