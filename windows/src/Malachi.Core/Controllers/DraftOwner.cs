// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ComposeDraftController.swift
// (DraftOwner); GTK: ui/internal/compose/draft.go (Owner, OwnerWindow,
// OwnerBoard).

namespace Malachi.Core.Controllers;

/// <summary>Who keeps the draft a compose form edits.</summary>
public enum DraftOwner
{
    /// <summary>
    /// The compose window (compose/draft.go): closing may ask, delete an
    /// unsaved draft or a comment's copy, and a conflict or a draft deleted
    /// elsewhere starts a new draft.
    /// </summary>
    Window,

    /// <summary>
    /// A board case's suggested reply edited inline (a local draft the case
    /// links): the board keeps it, so nothing here ever deletes it except
    /// Discard (through <see cref="ComposeDraftController.DiscardStored"/>),
    /// closing never asks, a conflict keeps our text in the same draft
    /// (<c>draft.get</c> for the version) and a draft deleted elsewhere is
    /// reported (<see cref="ComposeDraftController.OnLost"/>), never recreated.
    /// </summary>
    Board,
}
